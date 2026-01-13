package migration

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/confluentinc/kcp/internal/utils"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

var (
	gatewayName    string
	gatewayCrdName string
	kubeConfigPath string

	clusterId           string
	clusterRestEndpoint string
	clusterLinkName     string
	clusterApiKey       string
	clusterApiSecret    string
	topics              []string
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
	requiredFlags.StringVar(&gatewayName, "gateway-namespace", "", "The Kubernetes namespace under which the gateway has been deployed to.")
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

	migrationCmd.MarkFlagRequired("gateway-name")
	migrationCmd.MarkFlagRequired("gateway-crd-name")
	migrationCmd.MarkFlagRequired("cluster-id")
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
		kubeConfigPath = filepath.Join(os.Getenv("HOME"), ".kube", "config") // Is `HOME` reliable?
	}

	return &MigrationOpts{
		gatewayName:         gatewayName,
		kubeConfigPath:      kubeConfigPath,
		gatewayCrdName:      gatewayCrdName,
		clusterLinkName:     clusterLinkName,
		clusterRestEndpoint: clusterRestEndpoint,
		clusterId:           clusterId,
		clusterApiKey:       clusterApiKey,
		clusterApiSecret:    clusterApiSecret,
		topics:              topics,
	}, nil
}
