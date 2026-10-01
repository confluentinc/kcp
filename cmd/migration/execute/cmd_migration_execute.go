package execute

import (
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/confluentinc/kcp/internal/manifest"
	"github.com/confluentinc/kcp/internal/services/migplan"
	"github.com/confluentinc/kcp/internal/services/migration"
	"github.com/confluentinc/kcp/internal/utils"
	"github.com/spf13/cobra"
)

var (
	manifestFile string
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

Every run reads the manifest and the live state of the gateway and the cluster
link, works out what is left to do, and starts the cutover from its first step.
Each step is idempotent, so a migration already partway through cutover re-applies
its completed steps as no-ops and continues wherever the live state says work
remains. When nothing is left to do (every topic already migrated and no consumer
offset-sync restore owed), execute changes nothing and says so. Policy defaults
and credentials are read FRESH from the manifest on every run, so they can be
varied between runs or overridden with flags.

Each spec.defaultPolicies value can also be overridden for a single run with its flag
(e.g. --detect-unrouted-producers-duration), without editing the manifest.

If a run is interrupted at any step, simply re-run 'kcp migration execute' — it resumes
from the live state.`

// NewMigrationExecuteCmd builds the `execute` command bound to the live
// dependencies both branches share.
func NewMigrationExecuteCmd() *cobra.Command {
	return newMigrationExecuteCmd(liveExecutorDependencies)
}

// newMigrationExecuteCmd builds the command with deps injected, so this
// package's tests can pass stubs for either branch without dialing Kafka,
// Kubernetes, or a cluster-link REST endpoint.
func newMigrationExecuteCmd(deps executorDependencies) *cobra.Command {
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
			return runMigrationExecute(c, args, deps)
		},
	}

	cmd.Flags().StringVar(&manifestFile, "migration-yaml", "", "Path to the GatewayMigration manifest describing this migration.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Run only the reconcile step and print its plan report; change nothing.")

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
	cmd.Flags().IntVar(&gatewayConfigPortOverride, "gateway-config-port", 0, "Override spec.defaultPolicies.gatewayConfigPort: port serving the gateway's /config endpoint, polled per pod to confirm a config revision was applied. Unset, uses spec.defaultPolicies.gatewayConfigPort; 0 means the gateway default (9180).")

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

// buildFreshMigrationConfig builds a run's MigrationConfig from the manifest
// alone — no live call. The plan fields (Topics, FenceYAML, SwitchoverYAML,
// GatewayYAML and the rest) are not set here: the FSM's initialize transition
// copies them in from the reconcile result. The effective policy, including
// the gateway config port, is applied by applyEffectivePolicy.
func buildFreshMigrationConfig(g *manifest.GatewayMigration, id string) migration.MigrationConfig {
	return migration.MigrationConfig{
		MigrationId:                id,
		K8sNamespace:               g.Spec.Gateway.Namespace,
		InitialCrName:              g.Spec.Gateway.CrName,
		ClusterId:                  g.Spec.Target.ClusterID,
		ClusterRestEndpoint:        g.Spec.Target.Kafka.RestEndpoint,
		ClusterLinkName:            g.Spec.ClusterLink.Name,
		Route:                      g.Spec.Route.Name,
		PauseConsumerOffsetSync:    g.Spec.ClusterLink.PauseConsumerOffsetSync,
		ConsumerOffsetSyncBaseline: g.Spec.ClusterLink.ConsumerOffsetSyncBaseline,
	}
}

func runMigrationExecute(cmd *cobra.Command, args []string, deps executorDependencies) error {
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
	// --dry-run.
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

	// metadata.name identifies the migration and is its migration_id label.
	id := g.Metadata.Name

	// Build this run's MigrationConfig from the manifest (no live call); live
	// reconcile (below) observes the cluster and decides what remains
	// outstanding.
	config := buildFreshMigrationConfig(g, id)

	// Record what this run will execute with — the effective policy (manifest
	// defaults with any per-run overrides). kcp.log keeps everything at Debug+, so
	// this is the durable audit trail of the knobs a given execute used.
	slog.Info("executing migration with effective policy", effectivePolicyLogArgs(id, g.Spec.DefaultPolicies)...)

	// migplan.Reconcile runs on every invocation: it is the single component
	// that observes live state and decides what remains outstanding. Both FSMs
	// start at uninitialized and apply this run's fresh result idempotently,
	// and the route's mode comes from it.
	reconcileResult, err := migplan.Reconcile(cmd.Context(), g)
	if err != nil {
		return fmt.Errorf("failed to produce the reconcile plan: %w", err)
	}
	return executePlan(cmd, g, &config, reconcileResult, deps, runReport)
}

// executePlan acts on this run's reconcile result and makes no decision of its
// own: it refuses what reconcile refused, runs nothing when reconcile found
// nothing to do, and otherwise hands the result to the route mode's state
// machine.
func executePlan(
	cmd *cobra.Command,
	g *manifest.GatewayMigration,
	config *migration.MigrationConfig,
	reconcileResult *migplan.Result,
	deps executorDependencies,
	runReportPath string,
) error {
	if reconcileResult.Refused {
		return fmt.Errorf("reconcile plan refused:\n%s", strings.Join(reconcileResult.Reasons, "\n"))
	}

	// Nothing to do: every topic is already migrated and no offset-sync
	// restore is owed. No state machine runs and no service is built.
	if reconcileResult.NothingToDo {
		slog.Info("✅ nothing to do: no topic in the migration still needs migrating", "migration_id", config.MigrationId)
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "✅ Migration completed: %s — nothing to do: no topic in it still needs migrating\n", config.MigrationId)
		return nil
	}

	switch reconcileResult.Mode {
	case "dynamic":
		return runDynamicBranch(cmd, g, config, reconcileResult, deps)
	default:
		// "static", and any value not yet recognized as dynamic — the static
		// path is the safe default.
		return runStaticBranch(cmd, g, config, reconcileResult, deps, runReportPath)
	}
}

// applyEffectivePolicy copies the effective policy (the manifest's
// spec.defaultPolicies with any per-run flag overrides already applied) onto
// config, the same way for both branches: the gateway config port and the two
// runtime fields the static workflow reads back during execute.
func applyEffectivePolicy(config *migration.MigrationConfig, p manifest.DefaultPolicies) {
	if p.GatewayConfigPort > 0 {
		config.GatewayConfigPort = p.GatewayConfigPort
	}
	config.DetectUnroutedProducersDuration = p.DetectUnroutedProducersDuration
	config.ConsumerOffsetSyncDrainDuration = p.ConsumerOffsetSyncDrainDuration
}

// effectivePolicyLogArgs renders the effective execute-time policy as slog
// key/value pairs for the audit log line. It is the single place the log's copy
// of DefaultPolicies is spelled out, so a new field cannot silently drop out of
// it.
func effectivePolicyLogArgs(migrationID string, p manifest.DefaultPolicies) []any {
	return []any{
		"migration_id", migrationID,
		"lag_threshold", p.LagThreshold,
		"promote_batch_size", p.PromoteBatchSize,
		"rollout_timeout", p.RolloutTimeout,
		"detect_unrouted_producers_duration", p.DetectUnroutedProducersDuration,
		"consumer_offset_sync_drain_duration", p.ConsumerOffsetSyncDrainDuration,
		"hot_reload_timeout", p.HotReloadTimeout,
		"gateway_config_port", p.GatewayConfigPort,
	}
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
