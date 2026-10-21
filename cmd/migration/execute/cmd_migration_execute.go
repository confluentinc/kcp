package execute

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/confluentinc/kcp/internal/manifest"
	"github.com/confluentinc/kcp/internal/services/gateway"
	"github.com/confluentinc/kcp/internal/services/migplan"
	"github.com/confluentinc/kcp/internal/services/migration"
	"github.com/confluentinc/kcp/internal/utils"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

var (
	manifestFile string
	dryRun       bool
	// Per-policy overrides. Each mirrors a field in the declared section's policies and,
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
	detectUnroutedCommitsDurationOverride   time.Duration
	offsetSyncConcurrencyOverride           int
)

const executeLong = `Execute a migration: run the cutover described by a migration manifest.

Every run reads the manifest and the live state of the gateway and the cluster
link, works out what is left to do, and starts the cutover from its first step.
Each step is idempotent, so a migration already partway through cutover re-applies
its completed steps as no-ops and continues wherever the live state says work
remains. When nothing is left to do (every topic already migrated and no consumer
offset-sync restore owed), execute changes nothing and says so. Policy defaults
and credentials are read FRESH from the manifest on every run, so they can be
varied between runs or overridden with flags.

Each policies value of the declared migration section can be overridden for a single run with
its flag, without editing the manifest. A flag that does not apply to the declared migration
type is an error. Every flag can also be set through an upper-case environment variable
named after it (--lag-threshold is LAG_THRESHOLD), which overrides the manifest in the same way.

If a run is interrupted at any step, simply re-run 'kcp migration execute' — it resumes
from the live state.

The manifest's fields, validation rules and a fully-annotated example are documented in the
[Migration manifest reference](../../migration-manifest-reference.md).`

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
  kcp migration execute --migration-yaml gateway-static-migration.yaml

  # Override a policy default for this run only (a flag that does not apply to
  # the declared migration type is an error)
  kcp migration execute --migration-yaml gateway-static-migration.yaml --detect-unrouted-producers-duration 60s`,
		SilenceErrors: true,
		// A runtime failure mid-cutover (e.g. a source-connect error) must not
		// bury the error under Cobra's usage block.
		SilenceUsage: true,
		Args:         cobra.NoArgs,
		PreRunE:      bindEnvAndMarkDerived,
		RunE: func(c *cobra.Command, args []string) error {
			return runMigrationExecute(c, args, deps)
		},
	}

	cmd.Flags().StringVar(&manifestFile, "migration-yaml", "", "Path to the migration manifest describing this migration.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Run only the reconcile step and print its plan report; change nothing. Exits non-zero if the plan is refused.")

	// Per-policy overrides. Each replaces the matching policies value of the
	// declared section for this run only; omit the flag to use the manifest's default. Only a flag
	// the operator explicitly set (checked via Flags().Changed) overrides, so a
	// zero value — meaningful for all of these — is not confused with "unset".
	cmd.Flags().IntVar(&lagThresholdOverride, "lag-threshold", 0, "Override policies.lagThreshold (staticMigration, dynamicMigration): per-topic replication lag (the sum of that topic's partition lags) tolerated before fencing. Every selected topic must be at or below it; 0 means fully caught up.")
	cmd.Flags().IntVar(&promoteBatchSizeOverride, "promote-batch-size", 0, "Override policies.promoteBatchSize (staticMigration, dynamicMigration): max mirror topics promoted per batch. 0 promotes all at once.")
	cmd.Flags().DurationVar(&rolloutTimeoutOverride, "rollout-timeout", 0, "Override policies.rolloutTimeout (all migration types): max wait for the operator to report the gateway Ready during fence and switchover (e.g. 10m). 0 means no deadline.")
	cmd.Flags().DurationVar(&detectUnroutedProducersDurationOverride, "detect-unrouted-producers-duration", 0, "Override policies.detectUnroutedProducersDuration (staticMigration, dynamicMigration): window to monitor source offsets after fencing for producers bypassing the gateway. 0 skips the check; minimum 10s when set.")
	cmd.Flags().DurationVar(&consumerOffsetSyncDrainDurationOverride, "consumer-offset-sync-drain-duration", 0, "Override policies.consumerOffsetSyncDrainDuration (staticMigration only): wait after fencing before disabling the link's consumer offset sync. Has no effect unless consumerOffsetSync.pause is set. 0 means no wait.")
	cmd.Flags().DurationVar(&hotReloadTimeoutOverride, "hot-reload-timeout", 0, "Override policies.hotReloadTimeout (all migration types): max wait for every gateway pod to report the new config revision when the gateway supports hot-reload. Unlike --rollout-timeout this is never unbounded: a hot-reload moves no Kubernetes signal, so 0 uses the built-in 90s budget rather than waiting forever.")
	cmd.Flags().IntVar(&gatewayConfigPortOverride, "gateway-config-port", 0, "Override policies.gatewayConfigPort (all migration types): port serving the gateway's /config endpoint, polled per pod to confirm a config revision was applied. Unset, uses the manifest's policies.gatewayConfigPort; 0 means the gateway default (9180).")
	cmd.Flags().DurationVar(&detectUnroutedCommitsDurationOverride, "detect-unrouted-commits-duration", 0, "Override policies.detectUnroutedCommitsDuration (dynamicToStaticConversion only): window between the two committed-offset snapshots taken after fencing a route conversion, to catch a consumer committing to the source directly. 0 uses the built-in 30s; minimum 10s when set.")
	cmd.Flags().IntVar(&offsetSyncConcurrencyOverride, "offset-sync-concurrency", 0, "Override policies.offsetSyncConcurrency (dynamicToStaticConversion only): number of workers (each with its own broker connection) reading and writing consumer-group offsets during a route conversion. 0 uses the built-in 8.")

	_ = cmd.MarkFlagRequired("migration-yaml")
	return cmd
}

// buildFreshMigrationConfig builds a run's MigrationConfig from the manifest
// alone — no live call. The plan fields (Topics, FenceYAML, SwitchoverYAML,
// GatewayYAML and the rest) are not set here: the FSM's initialize transition
// copies them in from the reconcile result. The effective policy, including
// the gateway config port, is applied by the declared type's executor.
func buildFreshMigrationConfig(g *manifest.GatewayMigration, id string) migration.MigrationConfig {
	pause, baseline := g.Spec.DeclaredOffsetSync()
	return migration.MigrationConfig{
		MigrationId:                id,
		K8sNamespace:               g.Spec.Gateway.Namespace,
		InitialCrName:              g.Spec.Gateway.CrName,
		ClusterId:                  g.Spec.Target.ClusterID,
		ClusterRestEndpoint:        g.Spec.Target.Kafka.RestEndpoint,
		ClusterLinkName:            g.Spec.ClusterLink.Name,
		Route:                      g.Spec.DeclaredRoute().Name,
		PauseConsumerOffsetSync:    pause,
		ConsumerOffsetSyncBaseline: baseline,
	}
}

func runMigrationExecute(cmd *cobra.Command, args []string, deps executorDependencies) error {
	g, err := manifest.LoadGatewayMigrationFile(manifestFile)
	if err != nil {
		return err
	}

	// Command-line overrides replace the declared section's per-policy defaults
	// for this run, then the effective block is re-validated: an override can carry a
	// value the manifest itself never did (e.g. a sub-10s detect duration).
	// Applied unconditionally, before the --dry-run branch below, so a dry run
	// rejects an invalid override exactly as a real run would (e.g. --dry-run
	// --lag-threshold=-1 must fail the same way --lag-threshold=-1 alone does),
	// rather than silently accepting it because nothing downstream reads it.
	if err := applyPolicyOverrides(cmd, g); err != nil {
		return err
	}
	if errs := g.Spec.ValidatePolicies(); len(errs) > 0 {
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
	slog.Info("executing migration with effective policy", effectivePolicyLogArgs(id, g.Spec)...)

	// migplan.Reconcile runs on every invocation: it is the single component
	// that observes live state and decides what remains outstanding. Both FSMs
	// start at uninitialized and apply this run's fresh result idempotently,
	// and the route's mode comes from it.
	reconcileResult, err := migplan.Reconcile(cmd.Context(), g)
	if err != nil {
		return fmt.Errorf("failed to produce the reconcile plan: %w", err)
	}
	return executePlan(cmd, g, &config, reconcileResult, deps)
}

// executePlan acts on this run's reconcile result and makes no decision of its
// own about feasibility: it refuses what reconcile refused, runs nothing when
// reconcile found nothing to do, and otherwise hands the result to the state
// machine of the manifest's DECLARED migration type.
func executePlan(
	cmd *cobra.Command,
	g *manifest.GatewayMigration,
	config *migration.MigrationConfig,
	reconcileResult *migplan.Result,
	deps executorDependencies,
) error {
	if reconcileResult.Refused {
		return fmt.Errorf("reconcile plan refused:\n%s", strings.Join(reconcileResult.Reasons, "\n"))
	}

	t := g.Spec.MigrationType()
	if t == "" {
		return fmt.Errorf("spec: exactly one of staticMigration, dynamicMigration, dynamicToStaticConversion is required")
	}
	// reconcile labels its artifacts by shape; the declared type chose the
	// strategy. They must agree, or the run's artifacts would be misread.
	if want := planModeFor(t); reconcileResult.Mode != want {
		return fmt.Errorf("internal error: the reconcile plan's mode %q does not match the declared %s (expected %q)", reconcileResult.Mode, t.SectionKey(), want)
	}

	// Nothing to do: no state machine runs, but the stored spec must still be live.
	if reconcileResult.NothingToDo {
		if err := confirmGatewayAccepted(cmd.Context(), g, deps); err != nil {
			return err
		}
		if t == manifest.MigrationTypeDynamicToStatic {
			slog.Info("✅ nothing to do: the route is already static on its target domain", "migration_id", config.MigrationId)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "✅ Route conversion completed: %s — nothing to do: the route is already static on its target domain\n", config.MigrationId)
			return nil
		}
		slog.Info("✅ nothing to do: no topic in the migration still needs migrating", "migration_id", config.MigrationId)
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "✅ Migration completed: %s — nothing to do: no topic in it still needs migrating\n", config.MigrationId)
		return nil
	}

	switch t {
	case manifest.MigrationTypeDynamic:
		return runDynamicBranch(cmd, g, config, reconcileResult, deps)
	case manifest.MigrationTypeDynamicToStatic:
		return runConvertBranch(cmd, g, config, reconcileResult, deps)
	default: // manifest.MigrationTypeStatic
		return runStaticBranch(cmd, g, config, reconcileResult, deps)
	}
}

// Reconcile reads the stored spec, not the operator's verdict on it.
func confirmGatewayAccepted(ctx context.Context, g *manifest.GatewayMigration, deps executorDependencies) error {
	namespace, name := g.Spec.Gateway.Namespace, g.Spec.Gateway.CrName
	svc, err := deps.gateway(g)
	if err != nil {
		return fmt.Errorf("failed to build gateway service: %w", err)
	}
	stored, err := svc.GetGatewayYAML(ctx, namespace, name)
	if err != nil {
		return fmt.Errorf("failed to read gateway %q to confirm the migration landed: %w", name, err)
	}
	if err := gateway.CheckAccepted(stored, name); err != nil {
		return fmt.Errorf("the migration is not complete, the gateway is not running its stored spec: %w\n"+
			"   kubectl -n %s get gateway %s -o jsonpath='{.status.conditions}'", err, namespace, name)
	}
	return nil
}

// planModeFor is the migplan.Result.Mode a plan reconciled for t carries.
func planModeFor(t manifest.MigrationType) string {
	switch t {
	case manifest.MigrationTypeStatic:
		return "static"
	case manifest.MigrationTypeDynamic:
		return "dynamic"
	default:
		return "convert"
	}
}

// applyCommonPolicy copies the policy every migration type carries onto config:
// the gateway config port the executors' actions read back during execute.
func applyCommonPolicy(config *migration.MigrationConfig, p manifest.CommonPolicies) {
	if p.GatewayConfigPort > 0 {
		config.GatewayConfigPort = p.GatewayConfigPort
	}
}

// effectivePolicyLogArgs renders the declared migration's effective policy as
// slog key/value pairs for the audit log line. The per-type field list lives in
// manifest's LogArgs methods, so a new field cannot silently drop out of it.
func effectivePolicyLogArgs(migrationID string, spec manifest.GatewaySpec) []any {
	return append([]any{"migration_id", migrationID, "migration_type", string(spec.MigrationType())}, spec.PolicyLogArgs()...)
}

// envDerivedAnnotation tags a flag whose value was filled from its environment
// variable rather than typed on the command line.
const envDerivedAnnotation = "kcp.env-derived"

// bindEnvAndMarkDerived binds env vars to flags, then tags each flag the binding
// set. BindEnvToFlags goes through pflag's Set, which marks a flag Changed, so
// afterwards an env-derived value looks like an explicit --flag; the tag keeps
// the two apart. An env var is a shell-wide default, not a request about this
// migration, so an inapplicable one must stay silently ignored (as it was before
// the type was declared) rather than fail the run.
func bindEnvAndMarkDerived(cmd *cobra.Command, _ []string) error {
	explicit := map[string]bool{}
	cmd.Flags().VisitAll(func(f *pflag.Flag) { explicit[f.Name] = f.Changed })
	if err := utils.BindEnvToFlags(cmd); err != nil {
		return err
	}
	var err error
	cmd.Flags().VisitAll(func(f *pflag.Flag) {
		if f.Changed && !explicit[f.Name] {
			err = errors.Join(err, cmd.Flags().SetAnnotation(f.Name, envDerivedAnnotation, []string{"true"}))
		}
	})
	return err
}

// policyFlagTypes names, per policy flag, the migration types whose policies
// carry that field. A flag set for any other type fails the run before anything
// is touched: a flag that silently does nothing is a hazard ahead of a cutover.
var policyFlagTypes = map[string][]manifest.MigrationType{
	"lag-threshold":                       {manifest.MigrationTypeStatic, manifest.MigrationTypeDynamic},
	"promote-batch-size":                  {manifest.MigrationTypeStatic, manifest.MigrationTypeDynamic},
	"rollout-timeout":                     {manifest.MigrationTypeStatic, manifest.MigrationTypeDynamic, manifest.MigrationTypeDynamicToStatic},
	"detect-unrouted-producers-duration":  {manifest.MigrationTypeStatic, manifest.MigrationTypeDynamic},
	"consumer-offset-sync-drain-duration": {manifest.MigrationTypeStatic},
	"hot-reload-timeout":                  {manifest.MigrationTypeStatic, manifest.MigrationTypeDynamic, manifest.MigrationTypeDynamicToStatic},
	"gateway-config-port":                 {manifest.MigrationTypeStatic, manifest.MigrationTypeDynamic, manifest.MigrationTypeDynamicToStatic},
	"detect-unrouted-commits-duration":    {manifest.MigrationTypeDynamicToStatic},
	"offset-sync-concurrency":             {manifest.MigrationTypeDynamicToStatic},
}

// checkPolicyFlagsApply errors on a policy flag the operator set on the command line that
// the declared migration type does not have. Flags are checked in sorted order
// so the error is deterministic when several are inapplicable.
func checkPolicyFlagsApply(cmd *cobra.Command, t manifest.MigrationType) error {
	flags := make([]string, 0, len(policyFlagTypes))
	for flag := range policyFlagTypes {
		flags = append(flags, flag)
	}
	sort.Strings(flags)
	for _, flag := range flags {
		if !cmd.Flags().Changed(flag) || cmd.Flags().Lookup(flag).Annotations[envDerivedAnnotation] != nil {
			continue
		}
		types := policyFlagTypes[flag]
		if slices.Contains(types, t) {
			continue
		}
		names := make([]string, 0, len(types))
		for _, ft := range types {
			names = append(names, ft.SectionKey())
		}
		return fmt.Errorf("--%s does not apply to a %s (it applies to: %s)", flag, t.SectionKey(), strings.Join(names, ", "))
	}
	return nil
}

// applyPolicyOverrides replaces each default the operator set explicitly on the
// command line (or via its bound env var) in the DECLARED section's policies.
// Only a Changed flag overrides: zero is a legitimate, meaningful value for every
// one of these, so it must not be mistaken for "unset" and silently clobber a
// manifest default.
func applyPolicyOverrides(cmd *cobra.Command, g *manifest.GatewayMigration) error {
	t := g.Spec.MigrationType()
	if t == "" {
		return fmt.Errorf("spec: exactly one of staticMigration, dynamicMigration, dynamicToStaticConversion is required")
	}
	if err := checkPolicyFlagsApply(cmd, t); err != nil {
		return err
	}
	changed := cmd.Flags().Changed
	switch t {
	case manifest.MigrationTypeStatic:
		p := &g.Spec.StaticMigration.Policies
		if changed("lag-threshold") {
			p.LagThreshold = lagThresholdOverride
		}
		if changed("promote-batch-size") {
			p.PromoteBatchSize = promoteBatchSizeOverride
		}
		if changed("rollout-timeout") {
			p.RolloutTimeout = rolloutTimeoutOverride
		}
		if changed("detect-unrouted-producers-duration") {
			p.DetectUnroutedProducersDuration = detectUnroutedProducersDurationOverride
		}
		if changed("consumer-offset-sync-drain-duration") {
			p.ConsumerOffsetSyncDrainDuration = consumerOffsetSyncDrainDurationOverride
		}
		overrideCommon(cmd, &p.CommonPolicies)
	case manifest.MigrationTypeDynamic:
		p := &g.Spec.DynamicMigration.Policies
		if changed("lag-threshold") {
			p.LagThreshold = lagThresholdOverride
		}
		if changed("promote-batch-size") {
			p.PromoteBatchSize = promoteBatchSizeOverride
		}
		if changed("rollout-timeout") {
			p.RolloutTimeout = rolloutTimeoutOverride
		}
		if changed("detect-unrouted-producers-duration") {
			p.DetectUnroutedProducersDuration = detectUnroutedProducersDurationOverride
		}
		overrideCommon(cmd, &p.CommonPolicies)
	case manifest.MigrationTypeDynamicToStatic:
		p := &g.Spec.DynamicToStaticConversion.Policies
		if changed("rollout-timeout") {
			p.RolloutTimeout = rolloutTimeoutOverride
		}
		if changed("detect-unrouted-commits-duration") {
			p.DetectUnroutedCommitsDuration = detectUnroutedCommitsDurationOverride
		}
		if changed("offset-sync-concurrency") {
			p.OffsetSyncConcurrency = offsetSyncConcurrencyOverride
		}
		overrideCommon(cmd, &p.CommonPolicies)
	}
	return nil
}

func overrideCommon(cmd *cobra.Command, p *manifest.CommonPolicies) {
	if cmd.Flags().Changed("hot-reload-timeout") {
		p.HotReloadTimeout = hotReloadTimeoutOverride
	}
	if cmd.Flags().Changed("gateway-config-port") {
		p.GatewayConfigPort = gatewayConfigPortOverride
	}
}

// tracerName is the instrumentation scope of this package's spans.
const tracerName = "kcp/cmd/migration/execute"

// commandContext is the command's context, which carries the command's root
// span. A command run outside cobra's Execute — as the executor tests do — has
// none, so it falls back to Background.
func commandContext(cmd *cobra.Command) context.Context {
	if ctx := cmd.Context(); ctx != nil {
		return ctx
	}
	return context.Background()
}
