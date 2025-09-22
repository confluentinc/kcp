package migration_infra

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/confluentinc/kcp/internal/generators/create_asset/migration_infra"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/confluentinc/kcp/internal/utils"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

var (
	stateFile          string
	clusterArn         string
	migrationInfraType string

	ccEnvName     string
	ccClusterName string

	securityGroupIds []string

	ccClusterType                 string
	jumpClusterBrokerSubnetConfig string
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
	requiredFlags.StringVar(&stateFile, "state-file", "", "The path to the kcp state file where the MSK cluster discovery reports have been written to.")
	requiredFlags.StringVar(&clusterArn, "cluster-arn", "", "The ARN of the MSK cluster to create migration infrastructure for.")
	requiredFlags.StringVar(&migrationInfraType, "type", "", "The migration-infra type. See README for available options")
	migrationInfraCmd.Flags().AddFlagSet(requiredFlags)
	groups[requiredFlags] = "Required Flags"

	// Optional flags.
	optionalFlags := pflag.NewFlagSet("optional", pflag.ExitOnError)
	optionalFlags.SortFlags = false
	optionalFlags.StringVar(&ccEnvName, "cc-env-name", "", "Confluent Cloud environment name")
	optionalFlags.StringVar(&ccClusterName, "cc-cluster-name", "", "Confluent Cloud cluster name")
	optionalFlags.StringSliceVar(&securityGroupIds, "security-group-ids", []string{}, "Existing list of comma separated AWS security group ids")
	migrationInfraCmd.Flags().AddFlagSet(optionalFlags)
	groups[optionalFlags] = "Optional Flags"

	// Type 1 flags.
	typeOneFlags := pflag.NewFlagSet("type-1", pflag.ExitOnError)
	typeOneFlags.SortFlags = false
	typeOneFlags.StringVar(&ccClusterType, "cc-cluster-type", "", "Confluent Cloud cluster type - 'Dedicated' or 'Enterprise'")
	typeOneFlags.IPNetVar(&ansibleControlNodeSubnetCIDR, "ansible-control-node-subnet-cidr", net.IPNet{}, "Ansible control node subnet CIDR (e.g. 10.0.255.0/24)")
	typeOneFlags.StringVar(&jumpClusterBrokerSubnetConfig, "jump-cluster-broker-subnet-config", "", "Jump cluster broker subnet config (e.g. us-east-1a:10.0.150.0/24,us-east-1b:10.0.160.0/24,us-east-1c:10.0.170.0/24)")
	typeOneFlags.StringVar(&jumpClusterBrokerIAMRoleName, "jump-cluster-broker-iam-role-name", "", "The Jump cluster broker IAM role name")
	migrationInfraCmd.Flags().AddFlagSet(typeOneFlags)
	groups[typeOneFlags] = "Type 1 Flags"

	typeTwoFlags := pflag.NewFlagSet("type-2", pflag.ExitOnError)
	typeTwoFlags.SortFlags = false
	typeTwoFlags.StringVar(&ccClusterType, "cc-cluster-type", "", "Confluent Cloud cluster type - 'Dedicated' or 'Enterprise'")
	typeTwoFlags.IPNetVar(&ansibleControlNodeSubnetCIDR, "ansible-control-node-subnet-cidr", net.IPNet{}, "Ansible control node subnet CIDR (e.g. 10.0.255.0/24)")
	typeTwoFlags.StringVar(&jumpClusterBrokerSubnetConfig, "jump-cluster-broker-subnet-config", "", "Jump cluster broker subnet config (e.g. us-east-1a:10.0.150.0/24,us-east-1b:10.0.160.0/24,us-east-1c:10.0.170.0/24)")
	migrationInfraCmd.Flags().AddFlagSet(typeTwoFlags)
	groups[typeTwoFlags] = "Type 2 Flags"

	migrationInfraCmd.SetUsageFunc(func(c *cobra.Command) error {
		fmt.Printf("%s\n\n", c.Short)

		flagOrder := []*pflag.FlagSet{requiredFlags, optionalFlags, typeOneFlags, typeTwoFlags}
		groupNames := []string{"Required Flags", "Optional Flags", "Type 1 Flags", "Type 2 Flags"}

		for i, fs := range flagOrder {
			usage := fs.FlagUsages()
			if usage != "" {
				fmt.Printf("%s:\n%s\n", groupNames[i], usage)
			}
		}

		fmt.Println("All flags can be provided via environment variables (uppercase, with underscores).")

		return nil
	})

	migrationInfraCmd.MarkFlagRequired("state-file")
	migrationInfraCmd.MarkFlagRequired("cluster-arn")
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

	file, err := os.ReadFile(stateFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read statefile %s: %w", stateFile, err)
	}

	var state types.State
	if err := json.Unmarshal(file, &state); err != nil {
		return nil, fmt.Errorf("failed to parse statefile JSON: %w", err)
	}

	cluster, err := utils.GetClusterByArn(&state, clusterArn)
	if err != nil {
		return nil, fmt.Errorf("failed to get cluster: %w", err)
	}

	region := aws.ToString(&cluster.Region)
	vpcId := aws.ToString(&cluster.AWSClientInformation.ClusterNetworking.VpcId)

	if ccEnvName == "" {
		ccEnvName = aws.ToString(&cluster.Name)
	}

	if ccClusterName == "" {
		ccClusterName = aws.ToString(&cluster.Name)
	}

	bootstrapBrokers, err := getBootstrapBrokers(cluster, migrationInfraType)
	if err != nil {
		return nil, fmt.Errorf("failed to get bootstrap brokers: %v", err)
	}

	opts := migration_infra.MigrationInfraOpts{
		Region:                        region,
		VPCId:                         vpcId,
		JumpClusterBrokerSubnetConfig: jumpClusterBrokerSubnetConfig,
		CcEnvName:                     ccEnvName,
		CcClusterName:                 ccClusterName,
		CcClusterType:                 strings.ToLower(ccClusterType),
		AnsibleControlNodeSubnetCIDR:  ansibleControlNodeSubnetCIDR.String(),
		JumpClusterBrokerIAMRoleName:  jumpClusterBrokerIAMRoleName,
		MigrationInfraType:            migrationInfraType,
		SecurityGroupIds:              securityGroupIds,
		BootstrapBrokers:              bootstrapBrokers,
		MskClusterId:                  aws.ToString(&cluster.KafkaAdminClientInformation.ClusterID),
		MskClusterArn:                 aws.ToString(&cluster.Arn),
	}

	return &opts, nil
}

func getBootstrapBrokers(cluster *types.DiscoveredCluster, migrationInfraType types.MigrationInfraType) (string, error) {
	switch migrationInfraType {
	case types.MskCpCcPrivateSaslIam:
		return aws.ToString(cluster.AWSClientInformation.BootstrapBrokers.BootstrapBrokerStringSaslIam), nil
	case types.MskCpCcPrivateSaslScram:
		return aws.ToString(cluster.AWSClientInformation.BootstrapBrokers.BootstrapBrokerStringSaslScram), nil
	case types.MskCcPublic:
		return aws.ToString(cluster.AWSClientInformation.BootstrapBrokers.BootstrapBrokerStringPublicSaslScram), nil
	default:
		return "", fmt.Errorf("invalid target type: %d", migrationInfraType)
	}
}
