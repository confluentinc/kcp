package init

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/confluentinc/kcp/internal/manifest"
	"github.com/confluentinc/kcp/internal/services/gateway"
	"github.com/confluentinc/kcp/internal/services/migration"
	"github.com/confluentinc/kcp/internal/utils"
	"github.com/spf13/cobra"
)

var (
	manifestFile       string
	migrationStateFile string
	skipValidate       bool
)

func NewMigrationInitCmd() *cobra.Command {
	migrationInitCmd := &cobra.Command{
		Use:   "init",
		Short: "Initialize a new migration",
		Long: `Initialize a new migration by validating infrastructure and persisting migration state.

The migration is described by a single ` + "`kind: GatewayMigration`" + ` YAML file — the source
and destination topology, the cluster link, the gateway CRs, and the credentials for each
connection leg. See docs/assets/gateway-examples/gateway-migration.yaml.

This command validates the cluster link and mirror topics on the destination cluster,
fetches the current gateway CR from Kubernetes, and proves the redundant auth each
fence route's switchover depends on is already staged — the target streaming domain
is declared, the route carries pre-staged auth for it, and every secret that auth
references exists in the namespace — before writing the migration configuration to
the state file.

Validating up front matters because the alternative is discovering the problem at cutover,
after client traffic has already been fenced.

The state file can then be used by 'kcp migration execute' to run the migration.

metadata.name in the manifest is the migration's identity and is written into the state
file's migration_id, so re-running init updates that migration rather than creating a
second one. Init refuses to overwrite a migration that is already past the point of no
return; run 'kcp migration execute' there instead — it proceeds with the edited spec
(warning loudly) rather than discarding the state a live cutover needs.

The manifest is secret-bearing when credentials are written inline. Keep it 0600, or
reference a credentials file and/or use ${ENV_VAR} interpolation (interpolate: true).`,
		Example: `  # Initialize from a manifest
  kcp migration init --migration-yaml gateway-migration.yaml

  # Register the migration without contacting the gateway or destination
  kcp migration init --migration-yaml gateway-migration.yaml --skip-validate`,
		SilenceErrors: true,
		// A runtime failure must not bury the error under Cobra's usage block.
		SilenceUsage: true,
		Args:         cobra.NoArgs,
		PreRunE:      preRunMigrationInit,
		RunE:         runMigrationInit,
	}

	migrationInitCmd.Flags().StringVar(&manifestFile, "migration-yaml", "", "Path to the GatewayMigration manifest describing this migration.")
	migrationInitCmd.Flags().StringVar(&migrationStateFile, "migration-state-file", "migration-state.json", "The path to the migration state file. If it doesn't exist, it will be created. If it exists, the new migration will be appended.")
	migrationInitCmd.Flags().BoolVar(&skipValidate, "skip-validate", false, "Skip infrastructure validation. Creates migration metadata without resolving credentials or validating gateway/Kubernetes resources. Useful for testing.")

	_ = migrationInitCmd.MarkFlagRequired("migration-yaml")

	return migrationInitCmd
}

func preRunMigrationInit(cmd *cobra.Command, args []string) error {
	return utils.BindEnvToFlags(cmd)
}

func runMigrationInit(cmd *cobra.Command, args []string) error {
	g, err := manifest.LoadGatewayMigrationFile(manifestFile)
	if err != nil {
		return err
	}

	// ===== PHASE 1: Load or create state =====
	var migrationState *migration.MigrationState
	if _, err := os.Stat(migrationStateFile); err == nil {
		migrationState, err = migration.NewMigrationStateFromFile(migrationStateFile)
		if err != nil {
			return fmt.Errorf("failed to load migration state: %w", err)
		}
	} else {
		migrationState = migration.NewMigrationState()
	}

	// metadata.name keys the row, so a second init is an UPDATE. Before the
	// point of no return that is exactly what §13 asks for ("re-run init to
	// adopt the new spec"); after it, overwriting would discard the FSM position
	// and the pre-disable link-config snapshot and strand a live cutover.
	//
	// This runs BEFORE credentials are resolved: it is a safety refusal, and an
	// operator re-running init mid-cutover needs to hear "this migration is
	// fenced" rather than have it masked by an unset environment variable.
	if err := checkReInitIsSafe(migrationState, g.Metadata.Name); err != nil {
		return err
	}

	// ===== PHASE 2: Build the config =====
	kubeConfigPathResolved, err := resolveKubeConfigPath(g)
	if err != nil {
		return err
	}
	slog.Debug("using kube config path", "path", kubeConfigPathResolved)

	config := migration.MigrationConfig{
		MigrationId:             g.Metadata.Name,
		SourceBootstrap:         strings.Join(g.Spec.Source.BootstrapServers, ","),
		ClusterBootstrap:        strings.Join(g.Spec.Target.Kafka.BootstrapServers, ","),
		K8sNamespace:            g.Spec.Gateway.Namespace,
		InitialCrName:           g.Spec.Gateway.CRs.Initial,
		KubeConfigPath:          kubeConfigPathResolved,
		ClusterId:               g.Spec.Target.ClusterID,
		ClusterRestEndpoint:     g.Spec.Target.Kafka.RestEndpoint,
		ClusterLinkName:         g.Spec.ClusterLink.Name,
		Topics:                  topicsOf(g),
		FenceRoutes:             g.Spec.Gateway.RouteNames(),
		SwitchoverTargets:       switchoverTargetsOf(g),
		CurrentState:            migration.StateUninitialized,
		PauseConsumerOffsetSync: g.Spec.ClusterLink.PauseConsumerOffsetSync,
		// The init-time capability probe dials /config on this port; without it the
		// probe falls back to the hardcoded default and fails on a non-default port
		// that execute (which reads the manifest fresh) would resolve correctly.
		GatewayConfigPort: g.Spec.DefaultPolicies.GatewayConfigPort,
	}

	// ===== PHASE 3: Early write =====
	// CRITICAL: the file MUST exist before the orchestrator runs.
	migrationState.UpsertMigration(config)
	if err := migrationState.WriteToFile(migrationStateFile); err != nil {
		return fmt.Errorf("failed to write migration state file: %w", err)
	}

	// ===== PHASE 4: Skip-validate exits early =====
	if skipValidate {
		if config.PauseConsumerOffsetSync {
			// Not a hard error: the pre-disable snapshot is taken by the
			// Initialize FSM step on the first execute, two steps before offset
			// sync can be paused, so skipping init-time validation does not
			// leave the restore bookend with nothing to diff against.
			slog.Warn("⚠️ validation skipped for a migration with spec.clusterLink.pauseConsumerOffsetSync: the cluster link's consumer.offset.sync.enable is not checked until execute")
		}
		fmt.Printf("✅ Migration created (validation skipped): %s\n", config.MigrationId)
		return nil
	}

	// ===== PHASE 5: Validation orchestration =====
	if err := checkCredentialsResolve(g); err != nil {
		return err
	}

	restCreds, err := g.RestCredentials()
	if err != nil {
		return fmt.Errorf("resolving destination REST credentials: %w", err)
	}

	opts := MigrationInitializerOpts{
		MigrationStateFile: migrationStateFile,
		MigrationState:     *migrationState,
		MigrationConfig:    config,
		RestCreds:          restCreds,
	}
	if err := NewMigrationInitializer(opts).Run(); err != nil {
		return err
	}

	fmt.Printf("✅ Migration initialized: %s\n", config.MigrationId)
	return nil
}

// checkCredentialsResolve resolves every credential block without using the
// result, so that "you got the auth wrong" is an init-time error rather than
// one discovered at execute, after the operator has scheduled a cutover
// window — the fail-fast the six --use-* flags used to buy at the cost of
// declaring source auth twice.
//
// It runs as part of Phase 5, alongside RestCredentials(), so --skip-validate
// (which returns before Phase 5) skips resolving source credentials exactly
// as it already skipped resolving destination credentials — neither leg is
// singled out for eager validation. That symmetry matters because both legs
// are growing more auth methods (e.g. mTLS certs), each of which may need
// local file access to resolve; --skip-validate promising "no infrastructure
// contact, no local credential resolution" for one leg and not the other
// would be an arbitrary distinction.
func checkCredentialsResolve(g *manifest.GatewayMigration) error {
	if _, errs := g.SourceCredentials(); len(errs) > 0 {
		return fmt.Errorf("spec.source.credentials: %w", manifest.JoinProblems("the migration manifest", errs))
	}
	if _, errs := g.DestinationKafkaCredentials(); len(errs) > 0 {
		return fmt.Errorf("spec.target.kafka.credentials: %w", manifest.JoinProblems("the migration manifest", errs))
	}
	return nil
}

// checkReInitIsSafe refuses to replace a migration that has irreversible work
// behind it.
func checkReInitIsSafe(state *migration.MigrationState, name string) error {
	existing, err := state.GetMigrationById(name)
	if err != nil || existing == nil {
		return nil // no such migration yet — a fresh registration
	}
	if migration.IsReversibleState(existing.CurrentState) {
		return nil
	}
	return fmt.Errorf(
		"migration %q is already at state %q and cannot be re-initialised — re-running init would discard the state needed to complete or roll back the cutover.\n"+
			"To proceed with an edited spec, run: kcp migration execute --migration-yaml <file> --migration-state-file <state-file>",
		name, existing.CurrentState)
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

// topicsOf flattens spec.topics. Omitted stays an empty list, which the
// Initialize FSM step back-fills with every active mirror topic.
func topicsOf(g *manifest.GatewayMigration) []string {
	if g.Spec.Topics == nil {
		return []string{}
	}
	return *g.Spec.Topics
}

// switchoverTargetsOf projects each route's streaming domain target — one
// entry per route, since a target is required on every entry (D4). There is
// no separately-snapshotted switched CR: execute derives it from
// InitialCrYAML plus these targets, the same way it derives the fenced CR
// from InitialCrYAML plus FenceRoutes.
func switchoverTargetsOf(g *manifest.GatewayMigration) []gateway.RouteSwitchoverTarget {
	routes := g.Spec.Gateway.Routes
	targets := make([]gateway.RouteSwitchoverTarget, len(routes))
	for i, r := range routes {
		targets[i] = gateway.RouteSwitchoverTarget{
			RouteName:           r.Name,
			StreamingDomainName: r.StreamingDomain.Name,
			BootstrapServerId:   r.StreamingDomain.BootstrapServerId,
		}
	}
	return targets
}
