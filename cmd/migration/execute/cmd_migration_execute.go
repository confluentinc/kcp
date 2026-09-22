package execute

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/confluentinc/kcp/internal/manifest"
	"github.com/confluentinc/kcp/internal/services/migplan"
	"github.com/confluentinc/kcp/internal/services/migration"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/confluentinc/kcp/internal/utils"
	"github.com/spf13/cobra"
)

var (
	manifestFile string
	migrationId  string
	dryRun       bool
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

There is no migration state file: every run reads the manifest and the live cluster
state fresh, via migplan.Reconcile, and the FSM always starts at uninitialized. Each
step's action is idempotent, so a migration already partway through cutover walks
forward re-applying already-completed steps as no-ops and picking up wherever the live
state says work remains. Policy defaults and credentials are read FRESH from the
manifest on every run, so they can be varied between runs or overridden with flags.

Each spec.defaultPolicies value can also be overridden for a single run with its flag
(e.g. --detect-unrouted-producers-duration), without editing the manifest.

If a run is interrupted at any step, simply re-run 'kcp migration execute' — it resumes
from the live state, not from any persisted position.`

// NewMigrationExecuteCmd builds the `execute` command bound to real, live
// dependencies for a dynamic-mode (TBM) run. A static-mode (AAO) run never
// uses these — it calls migplan.Reconcile and its own live service
// constructors directly, exactly as before this command was unified.
func NewMigrationExecuteCmd() *cobra.Command {
	return newMigrationExecuteCmd(buildTBMOffsetProviders, buildTBMGatewayService, buildTBMClusterLinkService)
}

// newMigrationExecuteCmd builds the command with the TBM branch's live
// dependencies injected, so this package's own tests can pass stubs for a
// dynamic-mode run without dialing Kafka, Kubernetes, or a cluster-link REST
// endpoint. A static-mode (AAO) run has no equivalent injection point — the
// test posture between the two branches is deliberately asymmetric.
func newMigrationExecuteCmd(buildTBMOffsets offsetProvidersFunc, buildTBMGateway gatewayServiceFunc, buildTBMClusterLink clusterLinkServiceFunc) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "execute",
		Short: "Execute a migration (run the cutover)",
		Long:  executeLong,
		Example: `  # Run (or resume) the cutover
  kcp migration execute --migration-yaml gateway-migration.yaml

  # Override a policy default for this run only
  kcp migration execute --migration-yaml gateway-migration.yaml --detect-unrouted-producers-duration 60s`,
		SilenceErrors: true,
		// A runtime failure mid-cutover (e.g. a source-connect error) must not
		// bury the error under Cobra's usage block.
		SilenceUsage: true,
		Args:         cobra.NoArgs,
		PreRunE:      func(c *cobra.Command, _ []string) error { return utils.BindEnvToFlags(c) },
		RunE: func(c *cobra.Command, args []string) error {
			return runMigrationExecute(c, args, buildTBMOffsets, buildTBMGateway, buildTBMClusterLink)
		},
	}

	cmd.Flags().StringVar(&manifestFile, "migration-yaml", "", "Path to the GatewayMigration manifest describing this migration.")
	cmd.Flags().StringVar(&migrationId, "migration-id", "", "Address a migration by id instead of by the manifest's metadata.name. Needed only for migrations registered before metadata.name became the identity.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Run only the reconcile step and print its plan report; run no FSM transition.")

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
	return cmd
}

// resolveKubeConfigPath applies the ~/.kube/config default. spec.gateway.
// kubeconfig is the one manifest field where a leading ~/ is expanded.
func resolveKubeConfigPath(g *manifest.GatewayMigration) (string, error) {
	p, err := g.KubeconfigPath()
	if err != nil {
		return "", err
	}
	if p != "" {
		return p, nil
	}
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get user home directory: %w", err)
	}
	return filepath.Join(homeDir, ".kube", "config"), nil
}

// buildFreshMigrationConfig builds the MigrationConfig for a migration seen
// for the first time — pure manifest projections, no live call. Mirrors
// exactly what `kcp migration init`'s Phase 2 used to populate before it was
// retired: Topics/FenceYAML/SwitchoverYAML/GatewayYAML/Mode are deliberately
// NOT set here — they require migplan.Reconcile (a live call), which only
// runs once the FSM actually reaches the initialize transition, exactly as
// before.
func buildFreshMigrationConfig(g *manifest.GatewayMigration, id, kubeConfigPath string) migration.MigrationConfig {
	entry := g.Spec.Route.TopicGroup[0] // manifest validation guarantees exactly one entry
	var topicPatterns []string
	if entry.TopicPatterns != nil {
		topicPatterns = *entry.TopicPatterns
	}
	return migration.MigrationConfig{
		MigrationId:                id,
		SourceBootstrap:            strings.Join(g.Spec.Source.BootstrapServers, ","),
		ClusterBootstrap:           strings.Join(g.Spec.Target.Kafka.BootstrapServers, ","),
		K8sNamespace:               g.Spec.Gateway.Namespace,
		InitialCrName:              g.Spec.Gateway.CrName,
		KubeConfigPath:             kubeConfigPath,
		ClusterId:                  g.Spec.Target.ClusterID,
		ClusterRestEndpoint:        g.Spec.Target.Kafka.RestEndpoint,
		ClusterLinkName:            g.Spec.ClusterLink.Name,
		Route:                      g.Spec.Route.Name,
		TargetDomain:               g.Spec.Route.TargetStreamingDomain,
		TopicPatterns:              topicPatterns,
		PauseConsumerOffsetSync:    g.Spec.ClusterLink.PauseConsumerOffsetSync,
		ConsumerOffsetSyncBaseline: g.Spec.ClusterLink.ConsumerOffsetSyncBaseline,
		GatewayConfigPort:          g.Spec.DefaultPolicies.GatewayConfigPort,
	}
}

func runMigrationExecute(cmd *cobra.Command, args []string, buildTBMOffsets offsetProvidersFunc, buildTBMGateway gatewayServiceFunc, buildTBMClusterLink clusterLinkServiceFunc) error {
	g, err := manifest.LoadGatewayMigrationFile(manifestFile)
	if err != nil {
		return err
	}

	// Command-line overrides replace the manifest's per-policy defaults for this
	// run, then the effective block is re-validated: an override can carry a
	// value the manifest itself never did (e.g. a sub-10s detect duration).
	// Applied unconditionally, before the --dry-run branch below, so a dry run
	// rejects an invalid override exactly as a real run would (e.g. --dry-run
	// --lag-threshold=-1 must fail the same way --lag-threshold=-1 alone does),
	// rather than silently accepting it because nothing downstream reads it.
	applyPolicyOverrides(cmd, &g.Spec.DefaultPolicies)
	if errs := g.Spec.DefaultPolicies.Validate(); len(errs) > 0 {
		return manifest.JoinProblems("the effective migration policy (manifest defaults with command-line overrides applied)", errs)
	}

	// --dry-run stops here: the reconcile step is self-contained (it opens its
	// own live Gateway CR + source/target/cluster-link reads directly from the
	// manifest) and renders its own report to the command's writer. Nothing
	// past this point — config resolution, any FSM transition — runs under
	// --dry-run. Mirrors execute-tbm's identical --dry-run branch.
	if dryRun {
		res, err := migplan.Reconcile(cmd.Context(), g, migplan.WithOutput(cmd.OutOrStdout()))
		if err != nil {
			return fmt.Errorf("failed to produce the reconcile plan: %w", err)
		}
		if res.Refused {
			return fmt.Errorf("dry-run: reconcile plan refused (see reasons above)")
		}
		cmd.Printf("✅ dry-run complete: reconcile plan produced for %s (no state changes, no actions executed)\n", g.Metadata.Name)
		return nil
	}

	// metadata.name already uniquely identifies the migration and doubles as
	// migration_id — a separate mandatory path is not required for a fresh or
	// resumed run — mirrors execute-tbm's own default before this command
	// absorbed it.
	id := resolveMigrationID(g, migrationId)

	// There is no migration state file: every run builds a fresh
	// MigrationConfig straight from the manifest — pure manifest
	// projections, no live call, no registration, no drift check. Live
	// reconcile (below) is what observes actual cluster state and decides
	// what remains outstanding.
	kubeConfigPathResolved, kerr := resolveKubeConfigPath(g)
	if kerr != nil {
		return kerr
	}
	config := buildFreshMigrationConfig(g, id, kubeConfigPathResolved)

	// Record what this run will execute with — the effective policy (manifest
	// defaults with any per-run overrides). kcp.log keeps everything at Debug+, so
	// this is the durable audit trail of the knobs a given execute used; the same
	// values are also snapshotted into LastRunPolicies for the run report.
	slog.Info("executing migration with effective policy", effectivePolicyLogArgs(id, migration.StateUninitialized, g.Spec.DefaultPolicies)...)

	// migplan.Reconcile now runs on EVERY invocation — not only when resuming a
	// migration still at StateUninitialized. reconcile is the single component
	// that observes live state and decides what remains outstanding; the AAO
	// FSM always starts at uninitialized (internal/services/migration) and
	// simply applies this run's fresh result idempotently. The route's mode
	// comes from this run's reconcile result every time now, not only at
	// first-registration (TBM resume still ignores a fresh result until Plan 2d).
	reconcileResult, err := migplan.Reconcile(cmd.Context(), g)
	if err != nil {
		return fmt.Errorf("failed to produce the reconcile plan: %w", err)
	}
	if reconcileResult.Refused {
		return fmt.Errorf("reconcile plan refused:\n%s", strings.Join(reconcileResult.Reasons, "\n"))
	}
	mode := reconcileResult.Mode

	// Pause-offset-sync has no effect for a topic-based (dynamic)
	// migration — TBM's FSM has no offset_sync_paused state at all. A
	// dynamic route requires consumer offset sync to be disabled, so
	// pausing it is contradictory; refuse rather than silently ignore.
	if pauseOffsetSyncRefusedForDynamic(mode, g) {
		return fmt.Errorf(
			"spec.clusterLink.pauseConsumerOffsetSync is not supported for a topic-based (dynamic) route %q: a dynamic route requires consumer offset sync to be disabled, so there is nothing to pause — remove pauseConsumerOffsetSync (and consumerOffsetSyncBaseline) from the manifest",
			g.Spec.Route.Name)
	}

	switch mode {
	case "dynamic":
		return runTBMBranch(cmd, g, &config, reconcileResult, buildTBMOffsets, buildTBMGateway, buildTBMClusterLink)
	default:
		// "static", and any value not yet recognized as dynamic — matches
		// today's behavior for every migration this codebase has ever
		// registered, none of which were dynamic-mode before this plan.
		opts, err := buildExecutorOpts(g, &config, reconcileResult)
		if err != nil {
			return err
		}
		// run-report is an execute-time diagnostics path, not part of the
		// manifest; carry it straight from the flag onto the opts.
		opts.RunReportPath = runReport
		return NewMigrationExecutor(opts).Run()
	}
}

// effectivePolicyLogArgs renders the effective execute-time policy as slog
// key/value pairs for the audit log line. It is the single place the log's copy
// of DefaultPolicies is spelled out, so it cannot drift field-by-field from the
// LastRunPolicies snapshot the way the previous hand-inlined call already had
// (hotReloadTimeout and gatewayConfigPort had been silently dropped).
func effectivePolicyLogArgs(migrationID, state string, p manifest.DefaultPolicies) []any {
	return []any{
		"migration_id", migrationID,
		"state", state,
		"lag_threshold", p.LagThreshold,
		"promote_batch_size", p.PromoteBatchSize,
		"rollout_timeout", p.RolloutTimeout,
		"detect_unrouted_producers_duration", p.DetectUnroutedProducersDuration,
		"consumer_offset_sync_drain_duration", p.ConsumerOffsetSyncDrainDuration,
		"hot_reload_timeout", p.HotReloadTimeout,
		"gateway_config_port", p.GatewayConfigPort,
	}
}

// pauseOffsetSyncRefusedForDynamic reports whether spec.clusterLink.
// pauseConsumerOffsetSync is set on a manifest that resolved to a topic-based
// (dynamic) migration, where the field has no effect — TBM's FSM has no
// offset_sync_paused state. It is the guard for the refusal
// runMigrationExecute makes on the StateUninitialized reconcile path; a static
// route honors the field, so this is false for one. Factored out so the
// decision can be unit-tested without a live migplan.Reconcile — the only path
// that reaches the refusal through the command.
func pauseOffsetSyncRefusedForDynamic(mode string, g *manifest.GatewayMigration) bool {
	return mode == "dynamic" && g.Spec.ClusterLink.PauseConsumerOffsetSync
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

// buildExecutorOpts resolves every credential leg and the execute-time policy
// from the manifest. The manifest is a second deserializer into the same
// struct the flags filled, so nothing downstream changes shape.
func buildExecutorOpts(g *manifest.GatewayMigration, config *migration.MigrationConfig, reconcileResult *migplan.Result) (MigrationExecutorOpts, error) {
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
		return MigrationExecutorOpts{}, manifest.JoinProblems("spec.target.kafka.clusterCredentials", errs)
	}

	// A nil bootstrap is fine here: MigrateConn folds it straight into
	// KafkaSourceConn.BootstrapServers, which auth-type mapping never reads
	// (see TestMigrateConn_NilBootstrapServers_AuthMappingUnaffected).
	destConn := types.MigrateConn(nil, dstCreds)
	destAuthType, err := destConn.GetSelectedAuthType()
	if err != nil {
		// The validators upstream already enforce exactly one method (and
		// reject iam), so this is an invariant, not an expected user error —
		// but failing loudly here beats a nil dereference mid-cutover.
		return MigrationExecutorOpts{}, fmt.Errorf("resolving destination auth method: %w", err)
	}
	destAuthMethod := destConn.AuthMethod

	// Backward-compat trap: the old destination client always dialled SASL/PLAIN
	// over TLS against the public trust store (WithSASLPlainAuth with an empty
	// ca_cert). AdminOptionForAuthMethod maps sasl_plain with NEITHER ca_cert nor
	// tls set to cleartext SASL_PLAINTEXT — a silent downgrade for a Confluent
	// Cloud destination. Default UseTLS=true in that case: the destination is a
	// managed/production cluster, always TLS, unlike a source which may
	// legitimately be on-prem plaintext. Every existing manifest (which never
	// set tls: nor ca_cert:) keeps dialling exactly as before.
	if sp := destAuthMethod.SASLPlain; sp != nil && sp.CACert == "" && !sp.UseTLS {
		sp.UseTLS = true
	}

	// Policy is re-read fresh from the manifest on every run, so these two
	// runtime fields (which the workflow reads back during execute) always
	// carry this run's effective value rather than any stale copy.
	config.DetectUnroutedProducersDuration = g.Spec.DefaultPolicies.DetectUnroutedProducersDuration
	config.ConsumerOffsetSyncDrainDuration = g.Spec.DefaultPolicies.ConsumerOffsetSyncDrainDuration

	// Record the full effective policy (manifest defaults with this run's
	// overrides applied) as an observational snapshot for the operator and
	// support — never read back by kcp.
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
		MigrationConfig:   *config,
		LagThreshold:      int64(g.Spec.DefaultPolicies.LagThreshold),
		ClusterBootstrap:  config.ClusterBootstrap,
		SourceBootstrap:   config.SourceBootstrap,
		RolloutTimeout:    g.Spec.DefaultPolicies.RolloutTimeout,
		HotReloadTimeout:  g.Spec.DefaultPolicies.HotReloadTimeout,
		GatewayConfigPort: g.Spec.DefaultPolicies.GatewayConfigPort,
		PromoteBatchSize:  g.Spec.DefaultPolicies.PromoteBatchSize,

		// nil except when resuming a migration still at StateUninitialized —
		// see runMigrationExecute's conditional migplan.Reconcile call.
		ReconcileResult: reconcileResult,

		// The destination Kafka leg authenticates with the KAFKA block. The
		// cluster-link REST credential (spec.clusterLink.linkCredentials) may name
		// a different, broader principal, and sending that to the broker would
		// invert least privilege.
		DestAuthType:   destAuthType,
		DestAuthMethod: destAuthMethod,

		// The full resolved REST credential — basic, bearer, mtls, or the
		// api_key form — carried through rather than flattened to a scalar
		// key/secret pair, so bearer/basic/mTLS headers and client certs reach
		// the REST leg.
		RestCreds: restCreds,

		// Each leg carries only what its own block asked for. Collapsing these
		// would mean relaxing TLS for a self-signed source also stops verifying
		// the destination connections that carry the destination API key.
		SourceInsecureSkipTLSVerify:    srcCreds.InsecureSkipTLSVerify,
		DestKafkaInsecureSkipTLSVerify: dstCreds.InsecureSkipTLSVerify,
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
