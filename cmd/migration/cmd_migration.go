package migration

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/confluentinc/kcp/internal/types"
	"github.com/confluentinc/kcp/internal/utils"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

var (
	stateFile string

	gatewayNamespace string
	gatewayCrdName   string
	kubeConfigPath   string

	clusterId           string
	clusterRestEndpoint string
	clusterLinkName     string
	clusterApiKey       string
	clusterApiSecret    string
	topics              []string
	authMode            string
)

func NewMigrationCmd() *cobra.Command {
	migrationCmd := &cobra.Command{
		Use:           "migration",
		Short:         "Migrate Kafka resources from one cluster to another",
		Long:          "Migrate Kafka resources from one cluster to another",
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		PreRunE:       preRunMigration,
		RunE:          runMigration,
	}

	groups := map[*pflag.FlagSet]string{}

	requiredFlags := pflag.NewFlagSet("required", pflag.ExitOnError)
	requiredFlags.SortFlags = false
	requiredFlags.StringVar(&stateFile, "state-file", "", "The path to the state file to use for the migration.")
	requiredFlags.StringVar(&gatewayNamespace, "gateway-namespace", "", "The Kubernetes namespace under which the gateway has been deployed to.")
	requiredFlags.StringVar(&gatewayCrdName, "gateway-crd-name", "", "The name of the gateway CRD to use by the migration.")
	requiredFlags.StringVar(&clusterId, "cluster-id", "", "The ID of the cluster to use by the migration.")
	requiredFlags.StringVar(&clusterRestEndpoint, "cluster-rest-endpoint", "", "The REST endpoint of the cluster to use by the migration.")
	requiredFlags.StringVar(&clusterLinkName, "cluster-link-name", "", "The name of the cluster link to use by the migration.")
	requiredFlags.StringVar(&clusterApiKey, "cluster-api-key", "", "The API key of the cluster to use by the migration.")
	requiredFlags.StringVar(&clusterApiSecret, "cluster-api-secret", "", "The API secret of the cluster to use by the migration.")
	migrationCmd.Flags().AddFlagSet(requiredFlags)
	groups[requiredFlags] = "Required Flags"

	optionalFlags := pflag.NewFlagSet("optional", pflag.ExitOnError)
	optionalFlags.SortFlags = false
	optionalFlags.StringVar(&kubeConfigPath, "kube-path", "", "The path to the Kubernetes config file to use for the migration.")
	optionalFlags.StringSliceVar(&topics, "topics", []string{}, "The topics to migrate (comma separated list or repeated flag).")
	optionalFlags.StringVar(&authMode, "auth-mode", "dest_swap", "The authentication mode to use for the migration. ('source_swap', 'dest_swap')")
	migrationCmd.Flags().AddFlagSet(optionalFlags)
	groups[optionalFlags] = "Optional Flags"

	migrationCmd.SetUsageFunc(func(c *cobra.Command) error {
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

	migrationCmd.MarkFlagRequired("state-file")
	migrationCmd.MarkFlagRequired("gateway-namespace")
	migrationCmd.MarkFlagRequired("gateway-crd-name")
	migrationCmd.MarkFlagRequired("cluster-id")
	migrationCmd.MarkFlagRequired("cluster-rest-endpoint")
	migrationCmd.MarkFlagRequired("cluster-link-name")
	migrationCmd.MarkFlagRequired("cluster-api-key")
	migrationCmd.MarkFlagRequired("cluster-api-secret")

	return migrationCmd
}

func preRunMigration(cmd *cobra.Command, args []string) error {
	if err := utils.BindEnvToFlags(cmd); err != nil {
		return err
	}

	return nil
}

func runMigration(cmd *cobra.Command, args []string) error {
	opts, err := parseMigrationOpts()
	if err != nil {
		return fmt.Errorf("failed to parse migration opts: %v", err)
	}

	migration := NewMigration(*opts)
	if err := migration.Run(); err != nil {
		return fmt.Errorf("failed to migrate: %v", err)
	}

	return nil
}

func parseMigrationOpts() (*MigrationOpts, error) {
	if kubeConfigPath == "" {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("failed to get user home directory: %v", err)
		}

		kubeConfigPath = filepath.Join(homeDir, ".kube", "config")
	}
	slog.Info("using kube config path", "path", kubeConfigPath)

	state, err := types.NewStateFromFile(stateFile)
	if err != nil {
		return nil, fmt.Errorf("failed to load existing state file: %v", err)
	}

	return &MigrationOpts{
		stateFile: stateFile,
		state:     *state,

		gatewayNamespace:    gatewayNamespace,
		kubeConfigPath:      kubeConfigPath,
		gatewayCrdName:      gatewayCrdName,
		clusterLinkName:     clusterLinkName,
		clusterRestEndpoint: clusterRestEndpoint,
		clusterId:           clusterId,
		clusterApiKey:       clusterApiKey,
		clusterApiSecret:    clusterApiSecret,
		topics:              topics,
		authMode:            authMode,
	}, nil
}
