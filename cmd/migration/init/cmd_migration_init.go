package migration_init

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
	requiredFlags.StringVar(&stateFile, "state-file", "", "The path to the state file to use for the migration.")
	requiredFlags.StringVar(&gatewayNamespace, "gateway-namespace", "", "The Kubernetes namespace under which the gateway has been deployed to.")
	requiredFlags.StringVar(&gatewayCrdName, "gateway-crd-name", "", "The name of the gateway CRD to use by the migration.")
	requiredFlags.StringVar(&clusterId, "cluster-id", "", "The ID of the cluster to use by the migration.")
	requiredFlags.StringVar(&clusterRestEndpoint, "cluster-rest-endpoint", "", "The REST endpoint of the cluster to use by the migration.")
	requiredFlags.StringVar(&clusterLinkName, "cluster-link-name", "", "The name of the cluster link to use by the migration.")
	requiredFlags.StringVar(&clusterApiKey, "cluster-api-key", "", "The API key of the cluster to use by the migration.")
	requiredFlags.StringVar(&clusterApiSecret, "cluster-api-secret", "", "The API secret of the cluster to use by the migration.")
	migrationInitCmd.Flags().AddFlagSet(requiredFlags)
	groups[requiredFlags] = "Required Flags"

	optionalFlags := pflag.NewFlagSet("optional", pflag.ExitOnError)
	optionalFlags.SortFlags = false
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

	migrationInitCmd.MarkFlagRequired("state-file")
	migrationInitCmd.MarkFlagRequired("gateway-namespace")
	migrationInitCmd.MarkFlagRequired("gateway-crd-name")
	migrationInitCmd.MarkFlagRequired("cluster-id")
	migrationInitCmd.MarkFlagRequired("cluster-rest-endpoint")
	migrationInitCmd.MarkFlagRequired("cluster-link-name")
	migrationInitCmd.MarkFlagRequired("cluster-api-key")
	migrationInitCmd.MarkFlagRequired("cluster-api-secret")

	return migrationInitCmd
}

func preRunMigrationInit(cmd *cobra.Command, args []string) error {
	if err := utils.BindEnvToFlags(cmd); err != nil {
		return err
	}

	return nil
}

func runMigrationInit(cmd *cobra.Command, args []string) error {
	opts, err := parseMigrationInitOpts()
	if err != nil {
		return fmt.Errorf("failed to parse migration opts: %v", err)
	}

	migrationInit := NewMigrationInit(*opts)
	if err := migrationInit.Run(); err != nil {
		return fmt.Errorf("failed to migrate: %v", err)
	}

	return nil
}

func parseMigrationInitOpts() (*MigrationInitOpts, error) {
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

	return &MigrationInitOpts{
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
