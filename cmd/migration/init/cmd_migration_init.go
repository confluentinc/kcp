package init

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/confluentinc/kcp/internal/manifest"
	"github.com/confluentinc/kcp/internal/services/migration"
	"github.com/confluentinc/kcp/internal/utils"
	"github.com/spf13/cobra"
)

var (
	manifestFile       string
	migrationStateFile string
	skipValidate       bool
)

// reInitSafeStates are the states from which re-running init is safe: nothing
// irreversible has happened yet, so replacing the persisted config loses
// nothing. Past these, producers have been fenced and/or offset sync disabled,
// and the FSM position plus the pre-disable link-config snapshot are the only
// things that can complete or roll back the cutover.
var reInitSafeStates = []string{
	migration.StateUninitialized,
	migration.StateInitialized,
	migration.StateLagsOk,
}

func NewMigrationInitCmd() *cobra.Command {
	migrationInitCmd := &cobra.Command{
		Use:   "init",
		Short: "Initialize a new migration",
		Long: `Initialize a new migration by validating infrastructure and persisting migration state.

The migration is described by a single ` + "`kind: GatewayMigration`" + ` YAML file — the source
and destination topology, the cluster link, the gateway CRs, and the credentials for each
connection leg. See docs/assets/migration-assets/gateway-examples/gateway-migration.yaml.

This command validates the cluster link and mirror topics on the destination cluster,
fetches the current gateway CR from Kubernetes, validates the initial, fenced and switchover
gateway CRs against each other — including that the Kubernetes secrets they reference exist
in the namespace — and writes the migration configuration to the state file.

Validating up front matters because the alternative is discovering the problem at cutover,
after client traffic has already been fenced.

The state file can then be used by 'kcp migration apply' to run the migration.

metadata.name in the manifest is the migration's identity and is written into the state
file's migration_id, so re-running init updates that migration rather than creating a
second one. Init refuses to overwrite a migration that is already past the point of no
return; use 'kcp migration apply --accept-spec-change' there instead.

The manifest is secret-bearing when credentials are written inline. Keep it 0600, or
reference a credentials file and/or use ${ENV_VAR} interpolation (interpolate: true).`,
		Example: `  # Initialize from a manifest
  kcp migration init -f gateway-migration.yaml

  # Register the migration without contacting the gateway or destination
  kcp migration init -f gateway-migration.yaml --skip-validate`,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		PreRunE:       preRunMigrationInit,
		RunE:          runMigrationInit,
	}

	migrationInitCmd.Flags().StringVarP(&manifestFile, "file", "f", "", "Path to the GatewayMigration manifest describing this migration.")
	migrationInitCmd.Flags().StringVar(&migrationStateFile, "migration-state-file", "migration-state.json", "The path to the migration state file. If it doesn't exist, it will be created. If it exists, the new migration will be appended.")
	migrationInitCmd.Flags().BoolVar(&skipValidate, "skip-validate", false, "Skip infrastructure validation. Creates migration metadata without validating gateway/Kubernetes resources. Useful for testing.")

	_ = migrationInitCmd.MarkFlagRequired("file")

	return migrationInitCmd
}

func preRunMigrationInit(cmd *cobra.Command, args []string) error {
	return utils.BindEnvToFlags(cmd)
}

func runMigrationInit(cmd *cobra.Command, args []string) error {
	g, err := loadGatewayManifest(manifestFile)
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
	if err := checkReInitIsSafe(migrationState, g.Metadata.Name); err != nil {
		return err
	}

	// ===== PHASE 2: Read the CR files =====
	// Both are snapshotted into the state file and applied FROM there at
	// cutover, so a file edited or deleted after init cannot change what gets
	// applied mid-flight.
	fencedCrYAML, err := os.ReadFile(g.Spec.Gateway.CRs.Fenced)
	if err != nil {
		return fmt.Errorf("failed to read spec.gateway.crs.fenced: %w", err)
	}
	switchoverCrYAML, err := os.ReadFile(g.Spec.Gateway.CRs.Switchover)
	if err != nil {
		return fmt.Errorf("failed to read spec.gateway.crs.switchover: %w", err)
	}

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
		FencedCrYAML:            fencedCrYAML,
		SwitchoverCrYAML:        switchoverCrYAML,
		CurrentState:            migration.StateUninitialized,
		PauseConsumerOffsetSync: g.Spec.ClusterLink.PauseConsumerOffsetSync,
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
			// Initialize FSM step on the first apply, two steps before offset
			// sync can be paused, so skipping init-time validation does not
			// leave the restore bookend with nothing to diff against.
			slog.Warn("⚠️ validation skipped for a migration with spec.clusterLink.pauseConsumerOffsetSync: the cluster link's consumer.offset.sync.enable is not checked until apply")
		}
		fmt.Printf("✅ Migration created (validation skipped): %s\n", config.MigrationId)
		return nil
	}

	// ===== PHASE 5: Validation orchestration =====
	restCreds, err := g.RestCredentials()
	if err != nil {
		return fmt.Errorf("resolving destination REST credentials: %w", err)
	}

	opts := MigrationInitializerOpts{
		MigrationStateFile:    migrationStateFile,
		MigrationState:        *migrationState,
		MigrationConfig:       config,
		ClusterApiKey:         restCreds.APIKey,
		ClusterApiSecret:      restCreds.APISecret,
		ClusterRestCACert:     restCreds.CACert,
		InsecureSkipTLSVerify: restCreds.InsecureSkipVerify,
	}
	if err := NewMigrationInitializer(opts).Run(); err != nil {
		return err
	}

	fmt.Printf("✅ Migration initialized: %s\n", config.MigrationId)
	return nil
}

// loadGatewayManifest reads, parses, validates and credential-checks the
// manifest. Credentials are resolved here — not only structurally validated —
// so that a bad auth block fails before anything is written, which is the
// fail-fast the six --use-* flags used to buy at the cost of declaring source
// auth twice.
func loadGatewayManifest(path string) (*manifest.GatewayMigration, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading migration manifest: %w", err)
	}
	warnIfGroupOrWorldReadable(path)

	g, err := manifest.ParseGatewayMigration(data)
	if err != nil {
		return nil, err
	}
	if errs := g.Validate(); len(errs) > 0 {
		return nil, joinValidationErrors(errs)
	}

	// kcp never contacts the source during init, but resolving the block here
	// turns "you got the auth wrong" into an init-time error rather than one
	// discovered at apply, after the operator has scheduled a cutover window.
	if _, errs := g.SourceCredentials(); len(errs) > 0 {
		return nil, fmt.Errorf("spec.source.credentials: %w", joinValidationErrors(errs))
	}
	if _, errs := g.DestinationKafkaCredentials(); len(errs) > 0 {
		return nil, fmt.Errorf("spec.target.kafka.credentials: %w", joinValidationErrors(errs))
	}
	return g, nil
}

// checkReInitIsSafe refuses to replace a migration that has irreversible work
// behind it.
func checkReInitIsSafe(state *migration.MigrationState, name string) error {
	existing, err := state.GetMigrationById(name)
	if err != nil || existing == nil {
		return nil // no such migration yet — a fresh registration
	}
	if slices.Contains(reInitSafeStates, existing.CurrentState) {
		return nil
	}
	return fmt.Errorf(
		"migration %q is already at state %q and cannot be re-initialised — re-running init would discard the state needed to complete or roll back the cutover.\n"+
			"To proceed with an edited spec, run: kcp migration apply -f <file> --accept-spec-change",
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

// warnIfGroupOrWorldReadable flags a secret-bearing manifest with loose
// permissions. A warning rather than an error: the file may legitimately be a
// read-only Kubernetes projected volume, and refusing to read it would break
// the in-cluster path entirely.
func warnIfGroupOrWorldReadable(path string) {
	info, err := os.Stat(path)
	if err != nil {
		return
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		slog.Warn("⚠️ migration manifest is group- or world-readable and may contain credentials", "path", path, "mode", fmt.Sprintf("%#o", perm))
	}
}

// joinValidationErrors renders all problems at once, so an operator fixes the
// file in one pass rather than one error per run.
func joinValidationErrors(errs []error) error {
	msgs := make([]string, len(errs))
	for i, e := range errs {
		msgs[i] = "  - " + e.Error()
	}
	return fmt.Errorf("%d problem(s) found in the migration manifest:\n%s", len(errs), strings.Join(msgs, "\n"))
}
