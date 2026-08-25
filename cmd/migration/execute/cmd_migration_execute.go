package execute

import (
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/confluentinc/kcp/internal/manifest"
	"github.com/confluentinc/kcp/internal/services/gateway"
	"github.com/confluentinc/kcp/internal/services/migration"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/confluentinc/kcp/internal/utils"
	"github.com/spf13/cobra"
)

var (
	manifestFile       string
	migrationStateFile string
	migrationId        string
	// Per-policy overrides. Each mirrors a field in spec.defaultPolicies and,
	// when the flag (or its bound env var) is explicitly set, replaces the
	// manifest's default for this one run. "Explicitly set" is read from
	// cmd.Flags().Changed, so an override to a zero value — which carries meaning
	// for every one of these — is distinguishable from an omitted flag.
	lagThresholdOverride                    int
	promoteBatchSizeOverride                int
	rolloutTimeoutOverride                  time.Duration
	detectUnroutedProducersDurationOverride time.Duration
	consumerOffsetSyncDrainDurationOverride time.Duration
	hotReloadTimeoutOverride                time.Duration
	gatewayConfigPortOverride               int
	// runReport is the diagnostics knob carried over from #408. It stays a flag
	// rather than a manifest policy field: the path is a per-run, machine-specific
	// output location — operational, not versioned desired state — and the
	// external migration performance rig (its only consumer) drives it this way.
	runReport string
)

const executeLong = `Execute a migration: run the cutover described by a GatewayMigration manifest.

The migration must already be registered with 'kcp migration init'. Topology comes from
the state file's snapshot, taken at init; policy and credentials are read FRESH from the
manifest on every run, so they can be varied between runs.

Each spec.defaultPolicies value can also be overridden for a single run with its flag
(e.g. --detect-unrouted-producers-duration), without editing the manifest.

If the manifest's topology no longer matches the snapshot, execute stops rather than
silently reconciling. Before the point of no return the answer is to re-run init; once
past it — where re-running init would strand the live cutover — execute warns loudly and
proceeds with the edited spec, since there is no longer a safe alternative.

If a run is interrupted at any step, re-running 'kcp migration execute' resumes from the
last completed step.`

// NewMigrationExecuteCmd builds the `execute` command.
func NewMigrationExecuteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "execute",
		Short: "Execute a migration (run the cutover)",
		Long:  executeLong,
		Example: `  # Run (or resume) the cutover
  kcp migration execute --migration-yaml gateway-migration.yaml --migration-state-file migration-state.json

  # Override a policy default for this run only
  kcp migration execute --migration-yaml gateway-migration.yaml --migration-state-file migration-state.json --detect-unrouted-producers-duration 60s`,
		SilenceErrors: true,
		// A runtime failure mid-cutover (e.g. a source-connect error) must not
		// bury the error under Cobra's usage block.
		SilenceUsage: true,
		Args:         cobra.NoArgs,
		PreRunE:      func(c *cobra.Command, _ []string) error { return utils.BindEnvToFlags(c) },
		RunE:         runMigrationExecute,
	}

	cmd.Flags().StringVar(&manifestFile, "migration-yaml", "", "Path to the GatewayMigration manifest describing this migration.")
	cmd.Flags().StringVar(&migrationStateFile, "migration-state-file", "", "Path to the migration-state.json file (produced by kcp migration init).")
	cmd.Flags().StringVar(&migrationId, "migration-id", "", "Address a migration by id instead of by the manifest's metadata.name. Needed only for migrations registered before metadata.name became the identity.")

	// Per-policy overrides. Each replaces the matching spec.defaultPolicies value
	// for this run only; omit the flag to use the manifest's default. Only a flag
	// the operator explicitly set (checked via Flags().Changed) overrides, so a
	// zero value — meaningful for all of these — is not confused with "unset".
	cmd.Flags().IntVar(&lagThresholdOverride, "lag-threshold", 0, "Override spec.defaultPolicies.lagThreshold: total replication lag (sum of all partition lags) tolerated before proceeding.")
	cmd.Flags().IntVar(&promoteBatchSizeOverride, "promote-batch-size", 0, "Override spec.defaultPolicies.promoteBatchSize: max mirror topics promoted per batch. 0 promotes all at once.")
	cmd.Flags().DurationVar(&rolloutTimeoutOverride, "rollout-timeout", 0, "Override spec.defaultPolicies.rolloutTimeout: max wait for the operator to report the gateway Ready during fence and switchover (e.g. 10m). 0 means no deadline.")
	cmd.Flags().DurationVar(&detectUnroutedProducersDurationOverride, "detect-unrouted-producers-duration", 0, "Override spec.defaultPolicies.detectUnroutedProducersDuration: window to monitor source offsets after fencing for producers bypassing the gateway. 0 skips the check; minimum 10s when set.")
	cmd.Flags().DurationVar(&consumerOffsetSyncDrainDurationOverride, "consumer-offset-sync-drain-duration", 0, "Override spec.defaultPolicies.consumerOffsetSyncDrainDuration: wait after fencing before disabling the link's consumer offset sync. Has no effect unless pauseConsumerOffsetSync is set. 0 means no wait.")
	cmd.Flags().DurationVar(&hotReloadTimeoutOverride, "hot-reload-timeout", 0, "Override spec.defaultPolicies.hotReloadTimeout: max wait for every gateway pod to report the new config revision when the gateway supports hot-reload. Unlike --rollout-timeout this is never unbounded: a hot-reload moves no Kubernetes signal, so 0 uses the built-in 90s budget rather than waiting forever.")
	cmd.Flags().IntVar(&gatewayConfigPortOverride, "gateway-config-port", 0, "Override spec.defaultPolicies.gatewayConfigPort: port serving the gateway's /config endpoint, polled per pod to confirm a config revision was applied. 0 uses the persisted value, falling back to the gateway default (9180).")

	// Hidden pending schema validation by the migration performance rig, its
	// first consumer; intended to become user-facing, since the natural audience
	// for per-stage timings is someone rehearsing their own migration. It is a
	// flag, not a manifest policy field, because the path is a per-run output
	// location rather than versioned desired state. PreRunE's BindEnvToFlags also
	// binds it to the RUN_REPORT env var.
	cmd.Flags().StringVar(&runReport, "run-report", "", "Write per-stage migration timings to <path> as JSON.")
	_ = cmd.Flags().MarkHidden("run-report")

	_ = cmd.MarkFlagRequired("migration-yaml")
	_ = cmd.MarkFlagRequired("migration-state-file")
	return cmd
}

func runMigrationExecute(cmd *cobra.Command, args []string) error {
	g, err := manifest.LoadGatewayMigrationFile(manifestFile)
	if err != nil {
		return err
	}

	// Command-line overrides replace the manifest's per-policy defaults for this
	// run, then the effective block is re-validated: an override can carry a
	// value the manifest itself never did (e.g. a sub-10s detect duration).
	applyPolicyOverrides(cmd, &g.Spec.DefaultPolicies)
	if errs := g.Spec.DefaultPolicies.Validate(); len(errs) > 0 {
		return manifest.JoinProblems("the effective migration policy (manifest defaults with command-line overrides applied)", errs)
	}

	state, err := migration.NewMigrationStateFromFile(migrationStateFile)
	if err != nil {
		return fmt.Errorf("failed to load migration state file %q: %w\nRun 'kcp migration init --migration-yaml %s' to create a new migration first", migrationStateFile, err, manifestFile)
	}

	id := resolveMigrationID(g, migrationId)
	config, err := state.GetMigrationById(id)
	if err != nil {
		return fmt.Errorf("migration '%s' not found in %s\nRun 'kcp migration list' to see available migrations", id, migrationStateFile)
	}

	// Record what this run will execute with — the effective policy (manifest
	// defaults with any per-run overrides). kcp.log keeps everything at Debug+, so
	// this is the durable audit trail of the knobs a given execute used; the same
	// values are also snapshotted into the state file as LastRunPolicies.
	slog.Info("executing migration with effective policy",
		"migration_id", id,
		"state", config.CurrentState,
		"lag_threshold", g.Spec.DefaultPolicies.LagThreshold,
		"promote_batch_size", g.Spec.DefaultPolicies.PromoteBatchSize,
		"rollout_timeout", g.Spec.DefaultPolicies.RolloutTimeout,
		"detect_unrouted_producers_duration", g.Spec.DefaultPolicies.DetectUnroutedProducersDuration,
		"consumer_offset_sync_drain_duration", g.Spec.DefaultPolicies.ConsumerOffsetSyncDrainDuration,
	)

	if err := checkSpecDrift(g, config); err != nil {
		return err
	}

	opts, err := buildExecutorOpts(g, config, *state, migrationStateFile)
	if err != nil {
		return err
	}
	// run-report is an execute-time diagnostics path, not part of the manifest;
	// carry it straight from the flag onto the opts.
	opts.RunReportPath = runReport
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

// applyPolicyOverrides replaces each default that the operator set explicitly on
// the command line (or via its bound env var). Only a Changed flag overrides:
// zero is a legitimate, meaningful value for every one of these, so it must not
// be mistaken for "unset" and silently clobber a manifest default.
func applyPolicyOverrides(cmd *cobra.Command, p *manifest.DefaultPolicies) {
	if cmd.Flags().Changed("lag-threshold") {
		p.LagThreshold = lagThresholdOverride
	}
	if cmd.Flags().Changed("promote-batch-size") {
		p.PromoteBatchSize = promoteBatchSizeOverride
	}
	if cmd.Flags().Changed("rollout-timeout") {
		p.RolloutTimeout = rolloutTimeoutOverride
	}
	if cmd.Flags().Changed("detect-unrouted-producers-duration") {
		p.DetectUnroutedProducersDuration = detectUnroutedProducersDurationOverride
	}
	if cmd.Flags().Changed("consumer-offset-sync-drain-duration") {
		p.ConsumerOffsetSyncDrainDuration = consumerOffsetSyncDrainDurationOverride
	}
	if cmd.Flags().Changed("hot-reload-timeout") {
		p.HotReloadTimeout = hotReloadTimeoutOverride
	}
	if cmd.Flags().Changed("gateway-config-port") {
		p.GatewayConfigPort = gatewayConfigPortOverride
	}
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

	// Before the point of no return, drift is a hard stop: re-running init is
	// safe and is how a new spec is adopted, so a scrolled-past warning would not
	// be consent. The trailing guidance line is deliberately part of the error —
	// it is the only thing the operator can act on.
	if migration.IsReversibleState(config.CurrentState) {
		header := fmt.Sprintf("config file has changed since this migration was initialised:\n   %s", strings.Join(drift, ",\n   "))
		return fmt.Errorf("%s\n   Re-run init to adopt the new spec", header) //nolint:staticcheck // multi-line operator guidance
	}

	// Past the point of no return, re-running init would discard the FSM position
	// and pre-disable snapshot and strand the live cutover — so proceeding with
	// the edited spec is the only safe path. Warn loudly rather than block.
	slog.Warn("⚠️ proceeding with an edited spec: this migration is past the point where re-running init is safe",
		"state", config.CurrentState, "sections", strings.Join(drift, "; "))
	return nil
}

// detectDrift returns the changed sections, described by field path and count.
//
// It compares only the topology fields. Credentials are out of scope
// automatically — they are never persisted. Policy is out of scope by omission,
// not by absence: two policy fields (the detect-unrouted and offset-sync-drain
// durations) DO reach the state file via MigrationConfig, but detectDrift never
// reads them back, because policy is re-read fresh from the manifest every run
// so the snapshot's copy is never authoritative. Two fields are additionally
// excluded on purpose: the kubeconfig path, because execute is resume-safe and
// may legitimately run from a different machine or pod; and anything
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
	// fence.routes drifts as a set (reordering is not a change). Counts only,
	// never names — a bare flag, like the retired fenced-CR byte check.
	if added, removed := diffCounts(g.Spec.Gateway.Fence.RouteNames(), config.FenceRoutes); added > 0 || removed > 0 {
		gatewayChanges = append(gatewayChanges, "fence.routes")
	}
	if switchoverTargetsChanged(g.Spec.Gateway.Fence.Routes, config.SwitchoverTargets) {
		gatewayChanges = append(gatewayChanges, "switchover targets")
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

// switchoverTarget is the comparable half of gateway.RouteSwitchoverTarget
// (routeName is the map key), so two target lists can be compared as sets —
// reordering is not a change, matching fence.routes' own drift semantics.
type switchoverTarget struct {
	streamingDomainName string
	bootstrapServerId   string
}

// switchoverTargetsChanged reports whether the manifest's declared per-route
// switchover targets differ from the snapshot taken at init. Unlike the
// retired file-based switchover CR, there is no file to read and therefore no
// "unreadable" case to degrade: the target is pure manifest data, resolved
// the moment the document parses.
func switchoverTargetsChanged(routes []manifest.GatewayFenceRoute, snapshot []gateway.RouteSwitchoverTarget) bool {
	want := make(map[string]switchoverTarget, len(routes))
	for _, r := range routes {
		want[r.Name] = switchoverTarget{r.Switchover.StreamingDomain.Name, r.Switchover.StreamingDomain.BootstrapServerId}
	}
	have := make(map[string]switchoverTarget, len(snapshot))
	for _, t := range snapshot {
		have[t.RouteName] = switchoverTarget{t.StreamingDomainName, t.BootstrapServerId}
	}
	return !maps.Equal(want, have)
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

	// Policy is re-read fresh from the manifest on every run and overwrites
	// whatever the config held, so the snapshot's copy is never authoritative.
	// These two DO reach the state file (MigrationConfig carries json tags for
	// both); the rest of policy was already purely execute-time.
	config.DetectUnroutedProducersDuration = g.Spec.DefaultPolicies.DetectUnroutedProducersDuration
	config.ConsumerOffsetSyncDrainDuration = g.Spec.DefaultPolicies.ConsumerOffsetSyncDrainDuration

	// Record the full effective policy (manifest defaults with this run's
	// overrides applied) as an observational snapshot that saveState persists.
	// Unlike the two duration fields above — runtime plumbing the workflow reads
	// back — this block exists purely for the operator and support, is never read
	// by kcp, and is therefore exempt from drift detection.
	config.LastRunPolicies = &migration.LastRunPolicies{
		LagThreshold:                    g.Spec.DefaultPolicies.LagThreshold,
		PromoteBatchSize:                g.Spec.DefaultPolicies.PromoteBatchSize,
		RolloutTimeout:                  g.Spec.DefaultPolicies.RolloutTimeout,
		DetectUnroutedProducersDuration: g.Spec.DefaultPolicies.DetectUnroutedProducersDuration,
		ConsumerOffsetSyncDrainDuration: g.Spec.DefaultPolicies.ConsumerOffsetSyncDrainDuration,
		HotReloadTimeout:                g.Spec.DefaultPolicies.HotReloadTimeout,
		GatewayConfigPort:               g.Spec.DefaultPolicies.GatewayConfigPort,
	}

	opts := MigrationExecutorOpts{
		MigrationStateFile: stateFile,
		MigrationState:     state,
		MigrationConfig:    *config,
		LagThreshold:       int64(g.Spec.DefaultPolicies.LagThreshold),
		ClusterBootstrap:   config.ClusterBootstrap,
		SourceBootstrap:    config.SourceBootstrap,
		RolloutTimeout:     g.Spec.DefaultPolicies.RolloutTimeout,
		HotReloadTimeout:   g.Spec.DefaultPolicies.HotReloadTimeout,
		GatewayConfigPort:  g.Spec.DefaultPolicies.GatewayConfigPort,
		PromoteBatchSize:   g.Spec.DefaultPolicies.PromoteBatchSize,

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
