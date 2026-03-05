package init

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/confluentinc/kcp/internal/types"
	"github.com/confluentinc/kcp/internal/utils"
	"github.com/google/uuid"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

var (
	migrationStateFile string
	skipValidate       bool

	gatewayNamespace string
	gatewayCrdName   string
	sourceName       string
	// destinationName      string
	sourceRouteName string
	// destinationRouteName string
	kubeConfigPath string

	clusterId            string
	clusterRestEndpoint  string
	clusterLinkName      string
	clusterApiKey        string
	clusterApiSecret     string
	topics               []string
	authMode             string
	ccBootstrapEndpoint  string
	loadBalancerEndpoint string
)

func NewMigrationInitCmd() *cobra.Command {
	migrationInitCmd := &cobra.Command{
		Use:           "init",
		Short:         "PLACEHOLDER",
		Long:          "PLACEHOLDER",
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		PreRunE:       preRunMigrationInit,
		RunE:          runMigrationInit,
	}

	groups := map[*pflag.FlagSet]string{}

	requiredFlags := pflag.NewFlagSet("required", pflag.ExitOnError)
	requiredFlags.SortFlags = false
	requiredFlags.StringVar(&gatewayNamespace, "gateway-namespace", "", "The Kubernetes namespace under which the gateway has been deployed to.")
	requiredFlags.StringVar(&gatewayCrdName, "gateway-crd-name", "", "The name of the gateway CRD to use by the migration.")
	requiredFlags.StringVar(&clusterId, "cluster-id", "", "The ID of the cluster to use by the migration.")
	requiredFlags.StringVar(&clusterRestEndpoint, "cluster-rest-endpoint", "", "The REST endpoint of the cluster to use by the migration.")
	requiredFlags.StringVar(&clusterLinkName, "cluster-link-name", "", "The name of the cluster link to use by the migration.")
	requiredFlags.StringVar(&clusterApiKey, "cluster-api-key", "", "The API key of the cluster to use by the migration.")
	requiredFlags.StringVar(&clusterApiSecret, "cluster-api-secret", "", "The API secret of the cluster to use by the migration.")
	requiredFlags.StringVar(&sourceName, "source-name", "", "The name of the streaming domain for the source (MSK) cluster.")
	// requiredFlags.StringVar(&destinationName, "dest-name", "", "The name of the streaming domain for the destination (CC) cluster.")
	requiredFlags.StringVar(&sourceRouteName, "source-route-name", "", "The name of the source route that is currently in use.")
	// requiredFlags.StringVar(&destinationRouteName, "dest-route-name", "", "The name of the destination route that will be used for the migration.")
	requiredFlags.StringVar(&ccBootstrapEndpoint, "cc-bootstrap-endpoint", "", "The bootstrap endpoint of the Confluent Cloud cluster.")
	requiredFlags.StringVar(&loadBalancerEndpoint, "load-balancer-endpoint", "", "The load balancer endpoint of the Confluent Cloud cluster.")

	migrationInitCmd.Flags().AddFlagSet(requiredFlags)
	groups[requiredFlags] = "Required Flags"

	optionalFlags := pflag.NewFlagSet("optional", pflag.ExitOnError)
	optionalFlags.SortFlags = false
	optionalFlags.StringVar(&migrationStateFile, "migration-state-file", "migration-state.json", "The path to the migration state file. If it doesn't exist, it will be created. If it exists, the new migration will be appended.")
	optionalFlags.BoolVar(&skipValidate, "skip-validate", false, "Skip infrastructure validation. Creates migration metadata without validating gateway/Kubernetes resources. Useful for testing.")
	optionalFlags.StringVar(&kubeConfigPath, "kube-path", "", "The path to the Kubernetes config file to use for the migration.")
	optionalFlags.StringSliceVar(&topics, "topics", []string{}, "The topics to migrate (comma separated list or repeated flag).")
	optionalFlags.StringVar(&authMode, "auth-mode", "dest_swap", "The authentication mode to use for the migration. ('source_swap', 'dest_swap')")
	migrationInitCmd.Flags().AddFlagSet(optionalFlags)
	groups[optionalFlags] = "Optional Flags"

	migrationInitCmd.SetUsageFunc(func(c *cobra.Command) error {
		fmt.Printf("%s\n\n", c.Short)

		flagOrder := []*pflag.FlagSet{requiredFlags, optionalFlags}
		groupNames := []string{"Required Flags", "Optional Flags"}

		for i, fs := range flagOrder {
			usage := fs.FlagUsages()
			if usage != "" {
				fmt.Printf("%s:\n%s\n", groupNames[i], usage)
			}
		}

		fmt.Println("All flags can be provided via environment variables (uppercase, with underscores).")

		return nil
	})

	migrationInitCmd.MarkFlagRequired("gateway-namespace")
	migrationInitCmd.MarkFlagRequired("gateway-crd-name")
	migrationInitCmd.MarkFlagRequired("cluster-id")
	migrationInitCmd.MarkFlagRequired("cluster-rest-endpoint")
	migrationInitCmd.MarkFlagRequired("cluster-link-name")
	migrationInitCmd.MarkFlagRequired("cluster-api-key")
	migrationInitCmd.MarkFlagRequired("cluster-api-secret")
	migrationInitCmd.MarkFlagRequired("source-name")
	// migrationInitCmd.MarkFlagRequired("dest-name")
	migrationInitCmd.MarkFlagRequired("source-route-name")
	migrationInitCmd.MarkFlagRequired("cc-bootstrap-endpoint")
	migrationInitCmd.MarkFlagRequired("load-balancer-endpoint")
	// migrationInitCmd.MarkFlagRequired("dest-route-name")

	return migrationInitCmd
}

func preRunMigrationInit(cmd *cobra.Command, args []string) error {
	if err := utils.BindEnvToFlags(cmd); err != nil {
		return err
	}

	return nil
}

func runMigrationInit(cmd *cobra.Command, args []string) error {
	// ===== PHASE 1: Load or create state =====
	var migrationState *types.MigrationState
	if _, err := os.Stat(migrationStateFile); err == nil {
		// File exists, load it
		migrationState, err = types.NewMigrationStateFromFile(migrationStateFile)
		if err != nil {
			return fmt.Errorf("failed to load migration state: %w", err)
		}
	} else {
		// File doesn't exist, create new state
		migrationState = types.NewMigrationState()
	}

	// ===== PHASE 2: Create new MigrationConfig with generated UUID =====
	migrationId := fmt.Sprintf("migration-%s", uuid.New().String())

	// Parse kube config path with default
	kubeConfigPathResolved := kubeConfigPath
	if kubeConfigPathResolved == "" {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("failed to get user home directory: %v", err)
		}
		kubeConfigPathResolved = filepath.Join(homeDir, ".kube", "config")
	}
	slog.Info("using kube config path", "path", kubeConfigPathResolved)

	config := &types.MigrationConfig{
		MigrationId:          migrationId,
		GatewayNamespace:     gatewayNamespace,
		GatewayCrdName:       gatewayCrdName,
		SourceName:           sourceName,
		DestinationName:      "", // Not used currently
		SourceRouteName:      sourceRouteName,
		DestinationRouteName: "", // Not used currently
		KubeConfigPath:       kubeConfigPathResolved,
		ClusterId:            clusterId,
		ClusterRestEndpoint:  clusterRestEndpoint,
		ClusterLinkName:      clusterLinkName,
		Topics:               topics,
		AuthMode:             authMode,
		CCBootstrapEndpoint:  ccBootstrapEndpoint,
		LoadBalancerEndpoint: loadBalancerEndpoint,
		CurrentState:         types.StateUninitialized,
	}

	// ===== PHASE 3: Early write - upsert migration and write to file =====
	// CRITICAL: File MUST exist before orchestrator runs to prevent panic
	migrationState.UpsertMigration(*config)
	if err := migrationState.WriteToFile(migrationStateFile); err != nil {
		return fmt.Errorf("failed to write migration state file: %w", err)
	}

	// ===== PHASE 4: Handle skip-validate flag (exit early if set) =====
	if skipValidate {
		slog.Info("migration created (validation skipped)",
			"migrationId", config.MigrationId,
			"currentState", config.CurrentState,
			"stateFile", migrationStateFile)
		return nil
	}

	// ===== PHASE 5: Pass to initializer for validation orchestration only =====
	opts := parseMigrationInitializerOpts(*migrationState, *config)
	migrationInitializer := NewMigrationInitializer(opts)
	if err := migrationInitializer.Run(); err != nil {
		return err
	}

	slog.Info("migration initialized",
		"migrationId", config.MigrationId,
		"currentState", config.CurrentState,
		"stateFile", migrationStateFile)
	return nil
}

func parseMigrationInitializerOpts(migrationState types.MigrationState, config types.MigrationConfig) MigrationInitializerOpts {
	return MigrationInitializerOpts{
		MigrationStateFile: migrationStateFile,
		MigrationState:     migrationState,
		MigrationConfig:    config,
		ClusterApiKey:      clusterApiKey,
		ClusterApiSecret:   clusterApiSecret,
	}
}
