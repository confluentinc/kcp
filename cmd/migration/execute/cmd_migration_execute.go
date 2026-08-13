package execute

import (
	"bytes"
	"fmt"
	"log/slog"
	"os"
	"slices"
	"strings"

	"github.com/confluentinc/kcp/internal/manifest"
	"github.com/confluentinc/kcp/internal/services/migration"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/confluentinc/kcp/internal/utils"
	"github.com/spf13/cobra"
)

var (
	manifestFile       string
	migrationStateFile string
	migrationId        string
	acceptSpecChange   bool
)

// reversibleStates are the states with nothing irreversible behind them. Drift
// found here is answered with "re-run init", which is cheap and re-validates
// the new spec properly. Past these, producers are fenced and/or offset sync is
// disabled, and re-running init would discard what completes or rolls back the
// cutover — so the only way forward is an explicit --accept-spec-change.
var reversibleStates = []string{
	migration.StateUninitialized,
	migration.StateInitialized,
	migration.StateLagsOk,
}

const executeLong = `Execute a migration: run the cutover described by a GatewayMigration manifest.

The migration must already be registered with 'kcp migration init'. Topology comes from
the state file's snapshot, taken at init; policy and credentials are read FRESH from the
manifest on every run, so they can be varied between runs.

If the manifest's topology no longer matches the snapshot, execute stops rather than
silently reconciling. Before the point of no return the answer is to re-run init; once
producers are fenced, pass --accept-spec-change to proceed with the edited spec.

If a run is interrupted at any step, re-running 'kcp migration execute' resumes from the
last completed step.`

// NewMigrationExecuteCmd builds the `execute` command.
func NewMigrationExecuteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "execute",
		Short: "Execute a migration (run the cutover)",
		Long:  executeLong,
		Example: `  # Run (or resume) the cutover
  kcp migration execute -f gateway-migration.yaml

  # Proceed mid-cutover with an edited spec
  kcp migration execute -f gateway-migration.yaml --accept-spec-change`,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		PreRunE:       func(c *cobra.Command, _ []string) error { return utils.BindEnvToFlags(c) },
		RunE:          runMigrationExecute,
	}

	cmd.Flags().StringVarP(&manifestFile, "file", "f", "", "Path to the GatewayMigration manifest describing this migration.")
	cmd.Flags().StringVar(&migrationStateFile, "migration-state-file", "migration-state.json", "The path to the migration state file.")
	cmd.Flags().StringVar(&migrationId, "migration-id", "", "Address a migration by id instead of by the manifest's metadata.name. Needed only for migrations registered before metadata.name became the identity.")
	cmd.Flags().BoolVar(&acceptSpecChange, "accept-spec-change", false, "Proceed even though the manifest no longer matches the topology snapshot taken at init. Only meaningful once the cutover is past the point where re-running init is safe.")

	_ = cmd.MarkFlagRequired("file")
	return cmd
}

func runMigrationExecute(cmd *cobra.Command, args []string) error {
	g, err := manifest.LoadGatewayMigrationFile(manifestFile)
	if err != nil {
		return err
	}

	state, err := migration.NewMigrationStateFromFile(migrationStateFile)
	if err != nil {
		return fmt.Errorf("failed to load migration state file %q: %w\nRun 'kcp migration init -f %s' to create a new migration first", migrationStateFile, err, manifestFile)
	}

	id := resolveMigrationID(g, migrationId)
	config, err := state.GetMigrationById(id)
	if err != nil {
		return fmt.Errorf("migration '%s' not found in %s\nRun 'kcp migration list' to see available migrations", id, migrationStateFile)
	}

	if err := checkSpecDrift(g, config); err != nil {
		return err
	}

	opts, err := buildExecutorOpts(g, config, *state, migrationStateFile)
	if err != nil {
		return err
	}
	return NewMigrationExecutor(opts).Run()
}

// resolveMigrationID prefers an explicit override. metadata.name is the
// identity for anything registered by a config-driven init; --migration-id
// remains the only way to address a row keyed by a generated uuid.
func resolveMigrationID(g *manifest.GatewayMigration, override string) string {
	if override != "" {
		return override
	}
	return g.Metadata.Name
}

// checkSpecDrift compares the manifest against the topology snapshot and
// converts any difference into the response §13 prescribes for the current FSM
// state. A warning would not do in either row: users are taught this YAML is
// desired state, and a line that scrolls past during an irreversible cutover is
// not consent.
func checkSpecDrift(g *manifest.GatewayMigration, config *migration.MigrationConfig) error {
	drift := detectDrift(g, config)
	if len(drift) == 0 {
		return nil
	}

	// Per-item detail goes to the log for support; the terminal gets section
	// names and counts only, never a topic list.
	slog.Debug("manifest differs from the topology snapshot taken at init",
		"migration_id", config.MigrationId, "state", config.CurrentState, "sections", strings.Join(drift, "; "))

	if acceptSpecChange {
		slog.Warn("⚠️ proceeding with an edited spec (--accept-spec-change)", "sections", strings.Join(drift, "; "))
		return nil
	}

	header := fmt.Sprintf("config file has changed since this migration was initialised:\n   %s", strings.Join(drift, ",\n   "))
	// The trailing guidance line is deliberately part of the error: it is the
	// only thing the operator can act on, and by the time this fires mid-cutover
	// they are reading it under time pressure.
	if slices.Contains(reversibleStates, config.CurrentState) {
		return fmt.Errorf("%s\n   Re-run init to adopt the new spec", header) //nolint:staticcheck // multi-line operator guidance
	}
	return fmt.Errorf("%s\n   This migration is already %s. Re-running init is not safe here;\n   pass --accept-spec-change to proceed with the edited spec", //nolint:staticcheck // multi-line operator guidance
		header, config.CurrentState)
}

// detectDrift returns the changed sections, described by field path and count.
//
// It compares only what the state file actually holds, so policy and
// credentials are out of scope automatically rather than by rule. Two fields
// are deliberately excluded: the kubeconfig path, because execute is resume-safe
// and may legitimately run from a different machine or pod; and anything
// credential-bearing, because credentials are never persisted.
func detectDrift(g *manifest.GatewayMigration, config *migration.MigrationConfig) []string {
	var drift []string

	if strings.Join(g.Spec.Source.BootstrapServers, ",") != config.SourceBootstrap {
		drift = append(drift, "spec.source (bootstrapServers)")
	}

	var targetChanges []string
	if k := g.Spec.Target.Kafka; k != nil {
		if strings.Join(k.BootstrapServers, ",") != config.ClusterBootstrap {
			targetChanges = append(targetChanges, "kafka.bootstrapServers")
		}
		if k.RestEndpoint != config.ClusterRestEndpoint {
			targetChanges = append(targetChanges, "kafka.restEndpoint")
		}
	}
	if g.Spec.Target.ClusterID != config.ClusterId {
		targetChanges = append(targetChanges, "clusterId")
	}
	if len(targetChanges) > 0 {
		drift = append(drift, fmt.Sprintf("spec.target (%s)", strings.Join(targetChanges, ", ")))
	}

	var linkChanges []string
	if g.Spec.ClusterLink.Name != config.ClusterLinkName {
		linkChanges = append(linkChanges, "name")
	}
	if g.Spec.ClusterLink.PauseConsumerOffsetSync != config.PauseConsumerOffsetSync {
		linkChanges = append(linkChanges, "pauseConsumerOffsetSync")
	}
	if len(linkChanges) > 0 {
		drift = append(drift, fmt.Sprintf("spec.clusterLink (%s)", strings.Join(linkChanges, ", ")))
	}

	var gatewayChanges []string
	if g.Spec.Gateway.Namespace != config.K8sNamespace {
		gatewayChanges = append(gatewayChanges, "namespace")
	}
	if g.Spec.Gateway.CRs.Initial != config.InitialCrName {
		gatewayChanges = append(gatewayChanges, "crs.initial")
	}
	if crChanged(g.Spec.Gateway.CRs.Fenced, config.FencedCrYAML) {
		gatewayChanges = append(gatewayChanges, "fenced CR")
	}
	if crChanged(g.Spec.Gateway.CRs.Switchover, config.SwitchoverCrYAML) {
		gatewayChanges = append(gatewayChanges, "switchover CR")
	}
	if len(gatewayChanges) > 0 {
		drift = append(drift, fmt.Sprintf("spec.gateway (%s)", strings.Join(gatewayChanges, ", ")))
	}

	// An omitted spec.topics means "every active mirror topic", and after the
	// first execute the snapshot holds whatever that expanded to — so omitted
	// must compare equal to the expansion, not to an empty list.
	if g.Spec.Topics != nil {
		added, removed := diffCounts(*g.Spec.Topics, config.Topics)
		if added > 0 || removed > 0 {
			drift = append(drift, fmt.Sprintf("spec.topics (%d added, %d removed)", added, removed))
		}
	}

	return drift
}

// crChanged reports whether the CR file on disk differs from the snapshot taken
// at init. An unreadable file is NOT drift: execute may be re-run from a
// different cwd or pod after a crash, possibly with the gateway already fenced,
// and a moved file must not strand a mid-flight cutover.
func crChanged(path string, snapshot []byte) bool {
	current, err := os.ReadFile(path)
	if err != nil {
		slog.Warn("⚠️ could not verify CR drift; proceeding on the snapshot taken at init", "path", path, "error", err)
		return false
	}
	return !bytes.Equal(current, snapshot)
}

// diffCounts returns how many entries want adds and drops relative to have.
// Counts only — never the names.
func diffCounts(want, have []string) (added, removed int) {
	for _, w := range want {
		if !slices.Contains(have, w) {
			added++
		}
	}
	for _, h := range have {
		if !slices.Contains(want, h) {
			removed++
		}
	}
	return added, removed
}

// buildExecutorOpts resolves every credential leg and the execute-time policy
// from the manifest. The manifest is a second deserializer into the same
// struct the flags filled, so nothing downstream changes shape.
func buildExecutorOpts(g *manifest.GatewayMigration, config *migration.MigrationConfig, state migration.MigrationState, stateFile string) (MigrationExecutorOpts, error) {
	srcCreds, errs := g.SourceCredentials()
	if len(errs) > 0 {
		return MigrationExecutorOpts{}, manifest.JoinProblems("spec.source.credentials", errs)
	}
	restCreds, err := g.RestCredentials()
	if err != nil {
		return MigrationExecutorOpts{}, fmt.Errorf("resolving destination REST credentials: %w", err)
	}
	dstCreds, errs := g.DestinationKafkaCredentials()
	if len(errs) > 0 {
		return MigrationExecutorOpts{}, manifest.JoinProblems("spec.target.kafka.credentials", errs)
	}
	// The sasl_plain-only rule is enforced in two validators upstream; assert it
	// here rather than dereferencing on an invariant that lives elsewhere,
	// because the alternative failure is a nil panic mid-cutover.
	if dstCreds.SASLPlain == nil {
		return MigrationExecutorOpts{}, fmt.Errorf("spec.target.kafka.credentials: only sasl_plain is supported for the destination in this release")
	}

	// Policy is read FRESH on every run and never snapshotted. Only these two
	// reach the config; the rest were already purely execute-time.
	config.DetectUnroutedProducersDuration = g.Spec.Policy.DetectUnroutedProducersDuration
	config.ConsumerOffsetSyncDrainDuration = g.Spec.Policy.ConsumerOffsetSyncDrainDuration

	opts := MigrationExecutorOpts{
		MigrationStateFile: stateFile,
		MigrationState:     state,
		MigrationConfig:    *config,
		LagThreshold:       int64(g.Spec.Policy.LagThreshold),
		ClusterBootstrap:   config.ClusterBootstrap,
		SourceBootstrap:    config.SourceBootstrap,
		RolloutTimeout:     g.Spec.Policy.RolloutTimeout,
		PromoteBatchSize:   g.Spec.Policy.PromoteBatchSize,

		// The destination Kafka leg authenticates with the KAFKA block. When
		// restCredentials is spelled out it may name a different, broader
		// principal, and sending that to the broker would invert least privilege.
		ClusterApiKey:    dstCreds.SASLPlain.Username,
		ClusterApiSecret: dstCreds.SASLPlain.Password,

		RestApiKey:        restCreds.APIKey,
		RestApiSecret:     restCreds.APISecret,
		ClusterRestCACert: restCreds.CACert,

		// Each leg carries only what its own block asked for. Collapsing these
		// would mean relaxing TLS for a self-signed source also stops verifying
		// the destination connections that carry the destination API key.
		SourceInsecureSkipTLSVerify:    srcCreds.InsecureSkipTLSVerify,
		DestKafkaInsecureSkipTLSVerify: dstCreds.InsecureSkipTLSVerify,
		RestInsecureSkipTLSVerify:      restCreds.InsecureSkipVerify,
	}
	applySourceAuth(&opts, srcCreds)
	return opts, nil
}

// applySourceAuth flattens the resolved source credentials onto the executor's
// per-method fields — the same shape the six --use-* flags and their credential
// strings used to fill.
func applySourceAuth(opts *MigrationExecutorOpts, creds types.MigrateClusterCredentials) {
	switch {
	case creds.IAM != nil:
		opts.AuthType = types.AuthTypeIAM
		// iam.region replaces --aws-region, which init never had — the drift
		// that made an IAM-authenticated source pass init and fail at execute.
		opts.AWSRegion = creds.IAM.Region
	case creds.SASLScram != nil:
		opts.AuthType = types.AuthTypeSASLSCRAM
		opts.SaslScramUsername = creds.SASLScram.Username
		opts.SaslScramPassword = creds.SASLScram.Password
		opts.SaslScramMechanism = creds.SASLScram.Mechanism
		opts.TlsCaCert = creds.SASLScram.CACert
	case creds.SASLPlain != nil:
		opts.AuthType = types.AuthTypeSASLPlain
		opts.SaslPlainUsername = creds.SASLPlain.Username
		opts.SaslPlainPassword = creds.SASLPlain.Password
		opts.TlsCaCert = creds.SASLPlain.CACert
		opts.SaslPlainUseTLS = creds.SASLPlain.UseTLS
	case creds.MTLS != nil:
		opts.AuthType = types.AuthTypeTLS
		opts.TlsCaCert = creds.MTLS.CACert
		opts.TlsClientCert = creds.MTLS.ClientCert
		opts.TlsClientKey = creds.MTLS.ClientKey
	case creds.UnauthenticatedTLS != nil:
		opts.AuthType = types.AuthTypeUnauthenticatedTLS
		opts.TlsCaCert = creds.UnauthenticatedTLS.CACert
	case creds.UnauthenticatedPlaintext != nil:
		opts.AuthType = types.AuthTypeUnauthenticatedPlaintext
	}
}
