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

	k8sNamespace      string
	passthroughCrName string
	kubeConfigPath    string

	sourceClusterArn    string
	clusterId           string
	clusterRestEndpoint string
	clusterLinkName     string
	clusterApiKey       string
	clusterApiSecret    string
	topics              []string

	fencedCrYamlPath     string
	switchoverCrYamlPath string

	useSaslIam                  bool
	useSaslScram                bool
	useTls                      bool
	useUnauthenticatedTLS       bool
	useUnauthenticatedPlaintext bool

	saslScramUsername string
	saslScramPassword string

	tlsCaCert     string
	tlsClientCert string
	tlsClientKey  string
)

func NewMigrationInitCmd() *cobra.Command {
	migrationInitCmd := &cobra.Command{
		Use:   "init",
		Short: "Initialize a new migration",
		Long: `Initialize a new migration by validating infrastructure and persisting migration state.

This command validates the cluster link and mirror topics on the destination cluster,
fetches the current gateway CR from Kubernetes, validates consistency across the initial,
fenced, and switchover gateway CRs, and writes the migration configuration to the state file.

The state file can then be used by 'kcp migration execute' to run the migration.`,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		PreRunE:       preRunMigrationInit,
		RunE:          runMigrationInit,
	}

	groups := map[*pflag.FlagSet]string{}

	requiredFlags := pflag.NewFlagSet("required", pflag.ExitOnError)
	requiredFlags.SortFlags = false
	requiredFlags.StringVar(&k8sNamespace, "k8s-namespace", "", "Kubernetes namespace where the gateway is deployed.")
	requiredFlags.StringVar(&passthroughCrName, "passthrough-cr-name", "", "Name of the passthrough gateway custom resource in Kubernetes.")
	requiredFlags.StringVar(&sourceClusterArn, "source-cluster-arn", "", "ARN of the source MSK cluster.")
	requiredFlags.StringVar(&clusterId, "cluster-id", "", "Confluent Cloud destination cluster ID (e.g. lkc-abc123).")
	requiredFlags.StringVar(&clusterRestEndpoint, "cluster-rest-endpoint", "", "REST endpoint of the destination Confluent Cloud cluster.")
	requiredFlags.StringVar(&clusterLinkName, "cluster-link-name", "", "Name of the cluster link on the destination cluster.")
	requiredFlags.StringVar(&clusterApiKey, "cluster-api-key", "", "API key for authenticating with the destination cluster.")
	requiredFlags.StringVar(&clusterApiSecret, "cluster-api-secret", "", "API secret for authenticating with the destination cluster.")
	requiredFlags.StringVar(&fencedCrYamlPath, "fenced-cr-yaml", "", "Path to the gateway CR YAML that blocks traffic during migration.")
	requiredFlags.StringVar(&switchoverCrYamlPath, "switchover-cr-yaml", "", "Path to the gateway CR YAML that routes traffic to Confluent Cloud.")

	migrationInitCmd.Flags().AddFlagSet(requiredFlags)
	groups[requiredFlags] = "Required Flags"

	optionalFlags := pflag.NewFlagSet("optional", pflag.ExitOnError)
	optionalFlags.SortFlags = false
	optionalFlags.StringVar(&migrationStateFile, "migration-state-file", "migration-state.json", "The path to the migration state file. If it doesn't exist, it will be created. If it exists, the new migration will be appended.")
	optionalFlags.BoolVar(&skipValidate, "skip-validate", false, "Skip infrastructure validation. Creates migration metadata without validating gateway/Kubernetes resources. Useful for testing.")
	optionalFlags.StringVar(&kubeConfigPath, "kube-path", "", "The path to the Kubernetes config file to use for the migration.")
	optionalFlags.StringSliceVar(&topics, "topics", []string{}, "The topics to migrate (comma separated list or repeated flag).")
	migrationInitCmd.Flags().AddFlagSet(optionalFlags)
	groups[optionalFlags] = "Optional Flags"

	// Authentication flags.
	authFlags := pflag.NewFlagSet("auth", pflag.ExitOnError)
	authFlags.SortFlags = false
	authFlags.BoolVar(&useSaslIam, "use-sasl-iam", false, "Use IAM authentication for the source MSK cluster.")
	authFlags.BoolVar(&useSaslScram, "use-sasl-scram", false, "Use SASL/SCRAM authentication for the source MSK cluster.")
	authFlags.BoolVar(&useTls, "use-tls", false, "Use TLS authentication for the source MSK cluster.")
	authFlags.BoolVar(&useUnauthenticatedTLS, "use-unauthenticated-tls", false, "Use unauthenticated (TLS encryption) for the source MSK cluster.")
	authFlags.BoolVar(&useUnauthenticatedPlaintext, "use-unauthenticated-plaintext", false, "Use unauthenticated (plaintext) for the source MSK cluster.")
	migrationInitCmd.Flags().AddFlagSet(authFlags)
	groups[authFlags] = "Source Cluster Authentication Flags"

	// SASL/SCRAM credential flags.
	saslScramFlags := pflag.NewFlagSet("sasl-scram", pflag.ExitOnError)
	saslScramFlags.SortFlags = false
	saslScramFlags.StringVar(&saslScramUsername, "sasl-scram-username", "", "SASL/SCRAM username for the source MSK cluster.")
	saslScramFlags.StringVar(&saslScramPassword, "sasl-scram-password", "", "SASL/SCRAM password for the source MSK cluster.")
	migrationInitCmd.Flags().AddFlagSet(saslScramFlags)
	groups[saslScramFlags] = "SASL/SCRAM Flags"

	// TLS credential flags.
	tlsFlags := pflag.NewFlagSet("tls", pflag.ExitOnError)
	tlsFlags.SortFlags = false
	tlsFlags.StringVar(&tlsCaCert, "tls-ca-cert", "", "Path to the TLS CA certificate for the source MSK cluster.")
	tlsFlags.StringVar(&tlsClientCert, "tls-client-cert", "", "Path to the TLS client certificate for the source MSK cluster.")
	tlsFlags.StringVar(&tlsClientKey, "tls-client-key", "", "Path to the TLS client key for the source MSK cluster.")
	migrationInitCmd.Flags().AddFlagSet(tlsFlags)
	groups[tlsFlags] = "TLS Flags"

	migrationInitCmd.SetUsageFunc(func(c *cobra.Command) error {
		fmt.Printf("%s\n\n", c.Short)

		flagOrder := []*pflag.FlagSet{requiredFlags, optionalFlags, authFlags, saslScramFlags, tlsFlags}
		groupNames := []string{"Required Flags", "Optional Flags", "Source Cluster Authentication Flags", "SASL/SCRAM Flags", "TLS Flags"}

		for i, fs := range flagOrder {
			usage := fs.FlagUsages()
			if usage != "" {
				fmt.Printf("%s:\n%s\n", groupNames[i], usage)
			}
		}

		fmt.Println("All flags can be provided via environment variables (uppercase, with underscores).")

		return nil
	})

	migrationInitCmd.MarkFlagRequired("source-cluster-arn")
	migrationInitCmd.MarkFlagRequired("k8s-namespace")
	migrationInitCmd.MarkFlagRequired("passthrough-cr-name")
	migrationInitCmd.MarkFlagRequired("cluster-id")
	migrationInitCmd.MarkFlagRequired("cluster-rest-endpoint")
	migrationInitCmd.MarkFlagRequired("cluster-link-name")
	migrationInitCmd.MarkFlagRequired("cluster-api-key")
	migrationInitCmd.MarkFlagRequired("cluster-api-secret")
	migrationInitCmd.MarkFlagRequired("fenced-cr-yaml")
	migrationInitCmd.MarkFlagRequired("switchover-cr-yaml")

	migrationInitCmd.MarkFlagsMutuallyExclusive("use-sasl-iam", "use-sasl-scram", "use-tls", "use-unauthenticated-tls", "use-unauthenticated-plaintext")

	return migrationInitCmd
}

func preRunMigrationInit(cmd *cobra.Command, args []string) error {
	if err := utils.BindEnvToFlags(cmd); err != nil {
		return err
	}

	if useSaslScram {
		cmd.MarkFlagRequired("sasl-scram-username")
		cmd.MarkFlagRequired("sasl-scram-password")
	}

	if useTls {
		cmd.MarkFlagRequired("tls-ca-cert")
		cmd.MarkFlagRequired("tls-client-cert")
		cmd.MarkFlagRequired("tls-client-key")
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

	// ===== PHASE 2: Read YAML files =====
	fencedCrYAML, err := os.ReadFile(fencedCrYamlPath)
	if err != nil {
		return fmt.Errorf("failed to read fenced CR YAML file: %w", err)
	}

	switchoverCrYAML, err := os.ReadFile(switchoverCrYamlPath)
	if err != nil {
		return fmt.Errorf("failed to read switchover CR YAML file: %w", err)
	}

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
		MigrationId:         fmt.Sprintf("migration-%s", uuid.New().String()),
		SourceClusterArn:    sourceClusterArn,
		K8sNamespace:        k8sNamespace,
		PassthroughCrName:   passthroughCrName,
		KubeConfigPath:      kubeConfigPathResolved,
		ClusterId:           clusterId,
		ClusterRestEndpoint: clusterRestEndpoint,
		ClusterLinkName:     clusterLinkName,
		Topics:              topics,
		FencedCrYAML:        fencedCrYAML,
		SwitchoverCrYAML:    switchoverCrYAML,
		CurrentState:        types.StateUninitialized,
		AuthMode:            resolveAuthMode(),
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
		"stateFile", migrationStateFile)
	return nil
}

func resolveAuthMode() string {
	switch {
	case useSaslIam:
		return string(types.AuthTypeIAM)
	case useSaslScram:
		return string(types.AuthTypeSASLSCRAM)
	case useTls:
		return string(types.AuthTypeTLS)
	case useUnauthenticatedTLS:
		return string(types.AuthTypeUnauthenticatedTLS)
	case useUnauthenticatedPlaintext:
		return string(types.AuthTypeUnauthenticatedPlaintext)
	default:
		return ""
	}
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
