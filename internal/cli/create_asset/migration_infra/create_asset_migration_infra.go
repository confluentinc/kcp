package migration_infra

import (
	"encoding/json"
	"fmt"
	"net"
	"os"

	"github.com/confluentinc/kcp/internal/generators/create_asset/migration_infra"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/confluentinc/kcp/internal/utils"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

var (
	region             string
	vpcId              string
	ccEnvName          string
	ccClusterName      string
	clusterFile        string
	migrationInfraType string

	ccClusterType                 string
	jumpClusterBrokerSubnetConfig net.IPNet
	ansibleControlNodeSubnetCIDR  net.IPNet

	jumpClusterBrokerIAMRoleName string
)

func NewMigrationInfraCmd() *cobra.Command {
	migrationInfraCmd := &cobra.Command{
		Use:           "migration-infra",
		Short:         "Create assets for the migration infrastructure",
		Long:          "Create Terraform assets that provision the migration infrastructure - Confluent Cloud, cluster linking, etc.",
		SilenceErrors: true,
		PreRunE:       preRunCreateMigrationInfra,
		RunE:          runCreateMigrationInfra,
	}

	groups := map[*pflag.FlagSet]string{}

	// Required flags.
	requiredFlags := pflag.NewFlagSet("required", pflag.ExitOnError)
	requiredFlags.SortFlags = false
	requiredFlags.StringVar(&region, "region", "", "AWS region the cost report is generated for")
	requiredFlags.StringVar(&vpcId, "vpc-id", "", "Existing MSK VPC ID")
	requiredFlags.StringVar(&ccEnvName, "cc-env-name", "", "Confluent Cloud environment name")
	requiredFlags.StringVar(&ccClusterName, "cc-cluster-name", "", "Confluent Cloud cluster name")
	requiredFlags.StringVar(&clusterFile, "cluster-file", "", "Cluster scan JSON file produced from 'kcp scan cluster' command")
	requiredFlags.StringVar(&migrationInfraType, "type", "", "The migration-infra type. See README for available options")
	migrationInfraCmd.Flags().AddFlagSet(requiredFlags)
	groups[requiredFlags] = "Required Flags"

	// Private Networking Migration flags.
	privNetworkMigrationFlags := pflag.NewFlagSet("private-network-migration", pflag.ExitOnError)
	privNetworkMigrationFlags.SortFlags = false
	privNetworkMigrationFlags.StringVar(&ccClusterType, "cc-cluster-type", "", "Confluent Cloud cluster type")
	privNetworkMigrationFlags.IPNetVar(&ansibleControlNodeSubnetCIDR, "ansible-control-node-subnet-cidr", net.IPNet{}, "Ansible control node subnet CIDR")
	privNetworkMigrationFlags.IPNetVar(&jumpClusterBrokerSubnetConfig, "jump-cluster-broker-subnet-config", net.IPNet{}, "Jump cluster broker subnet config")
	migrationInfraCmd.Flags().AddFlagSet(privNetworkMigrationFlags)
	groups[privNetworkMigrationFlags] = "Private Network Migration Flags"

	// Type 1 flags.
	typeOneFlags := pflag.NewFlagSet("type-1", pflag.ExitOnError)
	typeOneFlags.SortFlags = false
	typeOneFlags.StringVar(&jumpClusterBrokerIAMRoleName, "jump-cluster-broker-iam-role-name", "", "The Jump cluster broker IAM role name")
	migrationInfraCmd.Flags().AddFlagSet(typeOneFlags)
	groups[typeOneFlags] = "Type 1 Flags"

	migrationInfraCmd.SetUsageFunc(func(c *cobra.Command) error {
		fmt.Printf("%s\n\n", c.Short)

		flagOrder := []*pflag.FlagSet{requiredFlags, privNetworkMigrationFlags, typeOneFlags}
		groupNames := []string{"Required Flags", "Private Network Migration Flags", "Type 1 Required Flags"}

		for i, fs := range flagOrder {
			usage := fs.FlagUsages()
			if usage != "" {
				fmt.Printf("%s:\n%s\n", groupNames[i], usage)
			}
		}

		fmt.Println("All flags can be provided via environment variables (uppercase, with underscores).")

		return nil
	})

	migrationInfraCmd.MarkFlagRequired("region")
	migrationInfraCmd.MarkFlagRequired("vpc-id")
	migrationInfraCmd.MarkFlagRequired("cc-env-name")
	migrationInfraCmd.MarkFlagRequired("cc-cluster-name")
	migrationInfraCmd.MarkFlagRequired("cluster-file")
	migrationInfraCmd.MarkFlagRequired("type")

	return migrationInfraCmd
}

// sets flag values from corresponding environment variables if flags weren't explicitly provided
func preRunCreateMigrationInfra(cmd *cobra.Command, args []string) error {
	if err := utils.BindEnvToFlags(cmd); err != nil {
		return err
	}

	targetType, err := types.ToMigrationInfraType(migrationInfraType)
	if err != nil {
		return fmt.Errorf("invalid --type: %v", err)
	}

	switch targetType {
	case types.MskCpCcPrivateSaslIam:
		cmd.MarkFlagRequired("jump-cluster-broker-subnet-config")
		cmd.MarkFlagRequired("ansible-control-node-subnet-cidr")
		cmd.MarkFlagRequired("cc-cluster-type")
		cmd.MarkFlagRequired("jump-cluster-broker-iam-role-name")

	case types.MskCpCcPrivateSaslScram:
		cmd.MarkFlagRequired("jump-cluster-broker-subnet-config")
		cmd.MarkFlagRequired("ansible-control-node-subnet-cidr")
		cmd.MarkFlagRequired("cc-cluster-type")
	}

	return nil
}

func runCreateMigrationInfra(cmd *cobra.Command, args []string) error {
	opts, err := parseMigrationInfraOpts()
	if err != nil {
		return fmt.Errorf("failed to parse migration infra opts: %v", err)
	}

	migrationInfraAssetGenerator := migration_infra.NewMigrationInfraAssetGenerator(*opts)
	if err := migrationInfraAssetGenerator.Run(); err != nil {
		return fmt.Errorf("failed to create migration infrastructure assets: %v", err)
	}

	return nil
}

func parseMigrationInfraOpts() (*migration_infra.MigrationInfraOpts, error) {
	// ignoring error as already validated in preRunCreateMigrationInfra
	migrationInfraType, _ := types.ToMigrationInfraType(migrationInfraType)

	// Parse cluster information from JSON file
	file, err := os.ReadFile(clusterFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read cluster file: %v", err)
	}

	var clusterInfo types.ClusterInformation
	if err := json.Unmarshal(file, &clusterInfo); err != nil {
		return nil, fmt.Errorf("failed to unmarshal cluster info: %v", err)
	}

	opts := migration_infra.MigrationInfraOpts{
		Region:                        region,
		VPCId:                         vpcId,
		JumpClusterBrokerSubnetConfig: jumpClusterBrokerSubnetConfig.String(),
		CCEnvName:                     ccEnvName,
		CCClusterName:                 ccClusterName,
		CCClusterType:                 ccClusterType,
		AnsibleControlNodeSubnetCIDR:  ansibleControlNodeSubnetCIDR.String(),
		JumpClusterBrokerIAMRoleName:  jumpClusterBrokerIAMRoleName,
		ClusterInfo:                   clusterInfo,
		MigrationInfraType:            migrationInfraType,
	}

	return &opts, nil
}
