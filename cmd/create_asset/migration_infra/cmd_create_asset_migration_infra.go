package migration_infra

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"slices"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/confluentinc/kcp/internal/utils"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

var (
	stateFile          string
	clusterArn         string
	migrationInfraType string
	clusterLinkName    string

	existingPrivateLink     bool
	existingInternetGateway bool
	outputDir               string

	targetEnvironmentId     string
	targetClusterId         string
	targetRestEndpoint      string
	targetBootstrapEndpoint string

	extOutboundSubnetId        string
	extOutboundSecurityGroupId string

	existingPrivateLinkSubnetIds []string
	newPrivateLinkSubnetCidr     []net.IPNet

	jumpClusterInstanceType        string
	jumpClusterBrokerStorage       int
	jumpClusterBrokerSubnetCidr    []net.IPNet
	jumpClusterSetupHostSubnetCidr net.IPNet

	jumpClusterIamAuthRoleName string
)

func NewMigrationInfraCmd() *cobra.Command {
	migrationInfraCmd := &cobra.Command{
		Use:           "migration-infra",
		Short:         "migration-infra",
		Long:          "migration-infra",
		SilenceErrors: true,
		RunE:          runMigrationInfra,
		PreRunE:       preRunMigrationInfra,
	}

	groups := map[*pflag.FlagSet]string{}

	requiredFlags := pflag.NewFlagSet("required", pflag.ExitOnError)
	requiredFlags.SortFlags = false
	requiredFlags.StringVar(&stateFile, "state-file", "", "The path to the kcp state file where the MSK cluster discovery reports have been written to.")
	requiredFlags.StringVar(&clusterArn, "cluster-arn", "", "The ARN of the MSK cluster to create migration infrastructure for.")
	requiredFlags.StringVar(&migrationInfraType, "type", "", "The migration-infra type. See README for available options.")
	migrationInfraCmd.Flags().AddFlagSet(requiredFlags)
	groups[requiredFlags] = "Required Flags"

	optionalFlags := pflag.NewFlagSet("optional", pflag.ExitOnError)
	optionalFlags.SortFlags = false
	optionalFlags.BoolVar(&existingInternetGateway, "existing-internet-gateway", false, "Whether to use an existing internet gateway. (default: false)")
	optionalFlags.BoolVar(&existingPrivateLink, "existing-private-link", false, "Whether to use an existing private link. (default: false)")
	optionalFlags.StringVar(&outputDir, "output-dir", "", "The directory to output the migration infrastructure assets to. (default: 'migration-infra')")
	migrationInfraCmd.Flags().AddFlagSet(optionalFlags)
	groups[optionalFlags] = "Optional Flags"

	baseFlags := pflag.NewFlagSet("base", pflag.ExitOnError)
	baseFlags.SortFlags = false
	baseFlags.StringVar(&clusterLinkName, "cluster-link-name", "", "The name of the cluster link for the migration.")
	baseFlags.StringVar(&targetClusterId, "target-cluster-id", "", "The Confluent Cloud cluster ID.")
	baseFlags.StringVar(&targetRestEndpoint, "target-rest-endpoint", "", "The Confluent Cloud cluster REST endpoint.")
	migrationInfraCmd.Flags().AddFlagSet(baseFlags)
	groups[baseFlags] = "Base Flags"

	typeTwoFlags := pflag.NewFlagSet("type-two", pflag.ExitOnError)
	typeTwoFlags.SortFlags = false
	typeTwoFlags.StringVar(&targetEnvironmentId, "target-environment-id", "", "The Confluent Cloud environment ID.")
	typeTwoFlags.StringVar(&extOutboundSubnetId, "subnet-id", "", "[Optional] Subnet ID for the EC2 instance that provisions the cluster link. (default:  MSK broker 1 subnet).")
	typeTwoFlags.StringVar(&extOutboundSecurityGroupId, "security-group-id", "", "[Optional] Security group ID for the EC2 instance that provisions the cluster link. (default: MSK cluster security group).")
	migrationInfraCmd.Flags().AddFlagSet(typeTwoFlags)
	groups[typeTwoFlags] = "Type Two Flags"

	typeThreeFlags := pflag.NewFlagSet("type-three", pflag.ExitOnError)
	typeThreeFlags.SortFlags = false
	typeThreeFlags.StringVar(&targetEnvironmentId, "target-environment-id", "", "The Confluent Cloud environment ID.")
	typeThreeFlags.StringVar(&targetBootstrapEndpoint, "target-bootstrap-endpoint", "", "The bootstrap endpoint to use for the Confluent Cloud cluster.")
	typeThreeFlags.StringSliceVar(&existingPrivateLinkSubnetIds, "pl-subnet-ids", []string{}, "[Optiona] The IDs of the existing private link subnets to use for the jump cluster. (default: MSK cluster broker subnets).")
	typeThreeFlags.IPNetSliceVar(&jumpClusterBrokerSubnetCidr, "jump-cluster-broker-subnet-cidr", []net.IPNet{}, "The CIDR blocks to use for the jump cluster broker subnets. You should provide as many CIDRs as the MSK cluster has broker nodes.")
	typeThreeFlags.IPNetVar(&jumpClusterSetupHostSubnetCidr, "jump-cluster-setup-host-subnet-cidr", net.IPNet{}, "The CIDR block to use for the jump cluster setup host subnet.")
	typeThreeFlags.StringVar(&jumpClusterInstanceType, "jump-cluster-instance-type", "", "[Optional] The instance type to use for the jump cluster. (default: t3.medium).")
	typeThreeFlags.IntVar(&jumpClusterBrokerStorage, "jump-cluster-broker-storage", 0, "[Optional] The storage size to use for the jump cluster brokers. (default: MSK cluster broker storage size).")
	migrationInfraCmd.Flags().AddFlagSet(typeThreeFlags)
	groups[typeThreeFlags] = "Type Three Flags"

	typeFourFlags := pflag.NewFlagSet("type-four", pflag.ExitOnError)
	typeFourFlags.SortFlags = false
	typeFourFlags.StringVar(&targetEnvironmentId, "target-environment-id", "", "The Confluent Cloud environment ID.")
	typeFourFlags.StringVar(&targetBootstrapEndpoint, "target-bootstrap-endpoint", "", "The bootstrap endpoint to use for the Confluent Cloud cluster.")
	typeFourFlags.StringSliceVar(&existingPrivateLinkSubnetIds, "pl-subnet-ids", []string{}, "[Optiona] The IDs of the existing private link subnets to use for the jump cluster. (default: MSK cluster broker subnets).")
	typeFourFlags.IPNetSliceVar(&jumpClusterBrokerSubnetCidr, "jump-cluster-broker-subnet-cidr", []net.IPNet{}, "The CIDR blocks to use for the jump cluster broker subnets. You should provide as many CIDRs as the MSK cluster has broker nodes.")
	typeFourFlags.IPNetVar(&jumpClusterSetupHostSubnetCidr, "jump-cluster-setup-host-subnet-cidr", net.IPNet{}, "The CIDR block to use for the jump cluster setup host subnet.")
	typeFourFlags.StringVar(&jumpClusterIamAuthRoleName, "jump-cluster-iam-auth-role-name", "", " The IAM role name to authenticate the cluster link between MSK and the jump cluster.")
	typeFourFlags.StringVar(&jumpClusterInstanceType, "jump-cluster-instance-type", "", "[Optional] The instance type to use for the jump cluster. (default: t3.medium).")
	typeFourFlags.IntVar(&jumpClusterBrokerStorage, "jump-cluster-broker-storage", 0, "[Optional] The storage size to use for the jump cluster brokers. (default: MSK cluster broker storage size).")
	migrationInfraCmd.Flags().AddFlagSet(typeFourFlags)
	groups[typeFourFlags] = "Type Four Flags"

	typeFiveFlags := pflag.NewFlagSet("type-five", pflag.ExitOnError)
	typeFiveFlags.SortFlags = false
	typeFiveFlags.StringVar(&targetEnvironmentId, "target-environment-id", "", "The Confluent Cloud environment ID.")
	typeFiveFlags.StringVar(&targetBootstrapEndpoint, "target-bootstrap-endpoint", "", "The bootstrap endpoint to use for the Confluent Cloud cluster.")
	typeFiveFlags.IPNetSliceVar(&newPrivateLinkSubnetCidr, "pl-subnet-cidr", []net.IPNet{}, "The CIDR blocks to use for the new private link subnets. Three CIDRs are required.")
	typeFiveFlags.IPNetSliceVar(&jumpClusterBrokerSubnetCidr, "jump-cluster-broker-subnet-cidr", []net.IPNet{}, "The CIDR blocks to use for the jump cluster broker subnets. You should provide as many CIDRs as the MSK cluster has broker nodes.")
	typeFiveFlags.IPNetVar(&jumpClusterSetupHostSubnetCidr, "jump-cluster-setup-host-subnet-cidr", net.IPNet{}, "The CIDR block to use for the jump cluster setup host subnet.")
	typeFiveFlags.StringVar(&jumpClusterInstanceType, "jump-cluster-instance-type", "", "[Optional] The instance type to use for the jump cluster. (default: t3.medium).")
	typeFiveFlags.IntVar(&jumpClusterBrokerStorage, "jump-cluster-broker-storage", 0, "[Optional] The storage size to use for the jump cluster brokers. (default: MSK cluster broker storage size).")
	migrationInfraCmd.Flags().AddFlagSet(typeFiveFlags)
	groups[typeFiveFlags] = "Type Five Flags"

	typeSixFlags := pflag.NewFlagSet("type-six", pflag.ExitOnError)
	typeSixFlags.SortFlags = false
	typeSixFlags.StringVar(&targetEnvironmentId, "target-environment-id", "", "The Confluent Cloud environment ID.")
	typeSixFlags.StringVar(&targetBootstrapEndpoint, "target-bootstrap-endpoint", "", "The bootstrap endpoint to use for the Confluent Cloud cluster.")
	typeSixFlags.IPNetSliceVar(&newPrivateLinkSubnetCidr, "pl-subnet-cidr", []net.IPNet{}, "The CIDR blocks to use for the new private link subnets. Three CIDRs are required.")
	typeSixFlags.IPNetSliceVar(&jumpClusterBrokerSubnetCidr, "jump-cluster-broker-subnet-cidr", []net.IPNet{}, "The CIDR blocks to use for the jump cluster broker subnets. You should provide as many CIDRs as the MSK cluster has broker nodes.")
	typeSixFlags.IPNetVar(&jumpClusterSetupHostSubnetCidr, "jump-cluster-setup-host-subnet-cidr", net.IPNet{}, "The CIDR block to use for the jump cluster setup host subnet.")
	typeSixFlags.StringVar(&jumpClusterIamAuthRoleName, "jump-cluster-iam-auth-role-name", "", " The IAM role name to authenticate the cluster link between MSK and the jump cluster.")
	typeSixFlags.StringVar(&jumpClusterInstanceType, "jump-cluster-instance-type", "", "[Optional] The instance type to use for the jump cluster. (default: t3.medium).")
	typeSixFlags.IntVar(&jumpClusterBrokerStorage, "jump-cluster-broker-storage", 0, "[Optional] The storage size to use for the jump cluster brokers. (default: MSK cluster broker storage size).")
	migrationInfraCmd.Flags().AddFlagSet(typeSixFlags)
	groups[typeSixFlags] = "Type Six Flags"

	migrationInfraCmd.SetUsageFunc(func(c *cobra.Command) error {
		flagOrder := []*pflag.FlagSet{requiredFlags, optionalFlags, baseFlags, typeTwoFlags, typeThreeFlags, typeFourFlags, typeFiveFlags, typeSixFlags}
		groupNames := []string{"Required Flags", "Optional Flags", "Base Migration Flags", "Type Two Flags", "Type Three Flags", "Type Four Flags", "Type Five Flags", "Type Six Flags"}

		/*
			Type 1 = `HasPublicMskEndpoints` = true
			Type 2 = `HasPublicMskEndpoints` = false | `UseJumpClusters` = false |
			Type 3 = `HasPublicMskEndpoints` = false | `UseJumpClusters` = true | `ReuseExistingSubnets` = true | `MskJumpClusterAuthType` = SASL/SCRAM
			Type 4 = `HasPublicMskEndpoints` = false | `UseJumpClusters` = true | `ReuseExistingSubnets` = true | `MskJumpClusterAuthType` = IAM
			Type 5 = `HasPublicMskEndpoints` = false | `UseJumpClusters` = true | `ReuseExistingSubnets` = false | `MskJumpClusterAuthType` = SASL/SCRAM
			Type 6 = `HasPublicMskEndpoints` = false | `UseJumpClusters` = true | `ReuseExistingSubnets` = false | `MskJumpClusterAuthType` = IAM

			`HasExistingPrivateLink` and `HasExistingInternetGateway` do not require new branching as they do not require new inputs from the user. Instead
			they just use a data source for the existing private link or internet gateway instead of creating a new one.
		*/
		fmt.Println(`
Available Migration Types:
  Public MSK Endpoints:
    Type 1: Cluster Link [SASL/SCRAM]
  Private MSK Endpoints:
    Type 2: External Outbound Cluster Link [SASL/SCRAM]
    Type 3: Jump Cluster, Reuse Existing Subnets [SASL/SCRAM]
    Type 4: Jump Cluster, Reuse Existing Subnets [IAM]
    Type 5: Jump Cluster, New Subnets [SASL/SCRAM]
    Type 6: Jump Cluster, New Subnets [IAM]

Refer to the kcp docs for more information on each migration type.
		`)

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
	migrationInfraCmd.MarkFlagRequired("cluster-link-name")
	migrationInfraCmd.MarkFlagRequired("target-cluster-id")
	migrationInfraCmd.MarkFlagRequired("target-rest-endpoint")

	return migrationInfraCmd
}

func preRunMigrationInfra(cmd *cobra.Command, args []string) error {
	if err := utils.BindEnvToFlags(cmd); err != nil {
		return err
	}

	targetType, err := types.ToMigrationType(migrationInfraType)
	if err != nil {
		return fmt.Errorf("invalid --type: %v", err)
	}

	switch targetType {
	case types.PublicMskEndpoints:
		// No additional flag requirements.

	case types.ExternalOutboundClusterLink:
		cmd.MarkFlagRequired("target-environment-id")

	case types.JumpClusterReuseExistingSubnetsSaslScram:
		cmd.MarkFlagRequired("target-environment-id")
		cmd.MarkFlagRequired("target-bootstrap-endpoint")
		cmd.MarkFlagRequired("jump-cluster-broker-subnet-cidr")
		cmd.MarkFlagRequired("jump-cluster-setup-host-subnet-cidr")

	case types.JumpClusterReuseExistingSubnetsIam:
		cmd.MarkFlagRequired("target-environment-id")
		cmd.MarkFlagRequired("target-bootstrap-endpoint")
		cmd.MarkFlagRequired("jump-cluster-broker-subnet-cidr")
		cmd.MarkFlagRequired("jump-cluster-setup-host-subnet-cidr")
		cmd.MarkFlagRequired("jump-cluster-iam-auth-role-name")

	case types.JumpClusterNewSubnetsSaslScram:
		cmd.MarkFlagRequired("target-environment-id")
		cmd.MarkFlagRequired("target-bootstrap-endpoint")
		cmd.MarkFlagRequired("pl-subnet-cidr")
		cmd.MarkFlagRequired("jump-cluster-broker-subnet-cidr")
		cmd.MarkFlagRequired("jump-cluster-setup-host-subnet-cidr")

	case types.JumpClusterNewSubnetsIam:
		cmd.MarkFlagRequired("target-environment-id")
		cmd.MarkFlagRequired("target-bootstrap-endpoint")
		cmd.MarkFlagRequired("pl-subnet-cidr")
		cmd.MarkFlagRequired("jump-cluster-broker-subnet-cidr")
		cmd.MarkFlagRequired("jump-cluster-setup-host-subnet-cidr")
		cmd.MarkFlagRequired("jump-cluster-iam-auth-role-name")
	}

	return nil
}

func runMigrationInfra(cmd *cobra.Command, args []string) error {
	opts, err := parseMigrationInfraOpts()
	if err != nil {
		return fmt.Errorf("failed to parse migration infra options: %w", err)
	}

	generator := NewMigrationInfraAssetGenerator(*opts)
	if err := generator.Run(); err != nil {
		return fmt.Errorf("failed to run migration infra generator: %w", err)
	}

	return nil
}

func parseMigrationInfraOpts() (*MigrationInfraOpts, error) {
	targetType, _ := types.ToMigrationType(migrationInfraType)

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

	if cluster.AWSClientInformation.MskClusterConfig.Provisioned == nil {
		return nil, fmt.Errorf("cluster %s has no provisioned configuration. This could be because the cluster is a serverless cluster which is not supported for migration.", cluster.Name)
	}

	// Recurring statefile values.
	vpcId := aws.ToString(&cluster.AWSClientInformation.ClusterNetworking.VpcId)
	region := aws.ToString(&cluster.Region)
	mskClusterId := aws.ToString(&cluster.KafkaAdminClientInformation.ClusterID)

	opts := &MigrationInfraOpts{
		MigrationWizardRequest: types.MigrationWizardRequest{
			HasExistingInternetGateway: existingInternetGateway,
			HasExistingPrivateLink:     existingPrivateLink,

			VpcId:        vpcId,
			MskRegion:    region,
			MskClusterId: mskClusterId,

			ClusterLinkName:    clusterLinkName,
			TargetClusterId:    targetClusterId,
			TargetRestEndpoint: targetRestEndpoint,
		},
		OutputDir:     outputDir,
		MigrationType: targetType,
	}

	bootstrapBrokers, err := getBootstrapBrokers(cluster, targetType)
	if err != nil {
		return nil, fmt.Errorf("failed to get bootstrap brokers: %v", err)
	}

	if opts.MigrationWizardRequest.ClusterLinkName == "" {
		opts.MigrationWizardRequest.ClusterLinkName = "kcp-msk-to-cc-link"
	}

	switch targetType {
	case types.PublicMskEndpoints:
		opts.MigrationWizardRequest.HasPublicMskEndpoints = true
		opts.MigrationWizardRequest.UseJumpClusters = false

		opts.MigrationWizardRequest.MskSaslScramBootstrapServers = bootstrapBrokers

	case types.ExternalOutboundClusterLink:
		opts.MigrationWizardRequest.HasPublicMskEndpoints = false
		opts.MigrationWizardRequest.UseJumpClusters = false

		opts.MigrationWizardRequest.ExtOutboundSubnetId = extOutboundSubnetId
		opts.MigrationWizardRequest.ExtOutboundSecurityGroupId = extOutboundSecurityGroupId

		if opts.MigrationWizardRequest.ExtOutboundSubnetId == "" {
			if len(cluster.AWSClientInformation.ClusterNetworking.SubnetIds) > 0 {
				opts.MigrationWizardRequest.ExtOutboundSubnetId = cluster.AWSClientInformation.ClusterNetworking.SubnetIds[0]
			} else {
				return nil, fmt.Errorf("no subnet IDs found in cluster networking information")
			}
		}

		if opts.MigrationWizardRequest.ExtOutboundSecurityGroupId == "" {
			opts.MigrationWizardRequest.TargetEnvironmentId = targetEnvironmentId

			if len(cluster.AWSClientInformation.ClusterNetworking.SecurityGroups) > 0 {
				opts.MigrationWizardRequest.ExtOutboundSecurityGroupId = cluster.AWSClientInformation.ClusterNetworking.SecurityGroups[0]
			} else {
				return nil, fmt.Errorf("no security groups found in cluster networking information")
			}
		}

		opts.MigrationWizardRequest.ExtOutboundBrokers = buildExtOutboundBrokers(cluster)

	case types.JumpClusterReuseExistingSubnetsSaslScram:
		opts.MigrationWizardRequest.HasPublicMskEndpoints = false
		opts.MigrationWizardRequest.UseJumpClusters = true

		if len(jumpClusterBrokerSubnetCidr) != cluster.ClusterMetrics.MetricMetadata.NumberOfBrokerNodes {
			return nil, fmt.Errorf("the number of jump cluster broker subnet CIDRs (%d) does not match the number of broker nodes in the MSK cluster (%d). You should provide as many CIDRs as the MSK cluster has broker nodes.", len(jumpClusterBrokerSubnetCidr), cluster.ClusterMetrics.MetricMetadata.NumberOfBrokerNodes)
		}

		if len(existingPrivateLinkSubnetIds) == 0 {
			existingPrivateLinkSubnetIds = cluster.AWSClientInformation.ClusterNetworking.SubnetIds
		}

		if jumpClusterInstanceType == "" {
			jumpClusterInstanceType = strings.TrimPrefix(aws.ToString(cluster.AWSClientInformation.MskClusterConfig.Provisioned.BrokerNodeGroupInfo.InstanceType), "kafka.")
		}

		if jumpClusterBrokerStorage == 0 {
			jumpClusterBrokerStorage = int(*cluster.AWSClientInformation.MskClusterConfig.Provisioned.BrokerNodeGroupInfo.StorageInfo.EbsStorageInfo.VolumeSize)
		}

		opts.MigrationWizardRequest.TargetEnvironmentId = targetEnvironmentId
		opts.MigrationWizardRequest.TargetBootstrapEndpoint = targetBootstrapEndpoint

		opts.MigrationWizardRequest.ReuseExistingSubnets = true
		opts.MigrationWizardRequest.PrivateLinkExistingSubnetIds = existingPrivateLinkSubnetIds

		opts.MigrationWizardRequest.JumpClusterBrokerSubnetCidr = convertIpToStrings(jumpClusterBrokerSubnetCidr)
		opts.MigrationWizardRequest.JumpClusterSetupHostSubnetCidr = jumpClusterSetupHostSubnetCidr.String()
		opts.MigrationWizardRequest.JumpClusterInstanceType = jumpClusterInstanceType
		opts.MigrationWizardRequest.JumpClusterBrokerStorage = jumpClusterBrokerStorage

		opts.MigrationWizardRequest.MskJumpClusterAuthType = "sasl_scram"
		opts.MigrationWizardRequest.MskSaslScramBootstrapServers = bootstrapBrokers

	case types.JumpClusterReuseExistingSubnetsIam:
		opts.MigrationWizardRequest.HasPublicMskEndpoints = false
		opts.MigrationWizardRequest.UseJumpClusters = true

		if len(jumpClusterBrokerSubnetCidr) != cluster.ClusterMetrics.MetricMetadata.NumberOfBrokerNodes {
			return nil, fmt.Errorf("the number of jump cluster broker subnet CIDRs (%d) does not match the number of broker nodes in the MSK cluster (%d). You should provide as many CIDRs as the MSK cluster has broker nodes.", len(jumpClusterBrokerSubnetCidr), cluster.ClusterMetrics.MetricMetadata.NumberOfBrokerNodes)
		}

		if len(existingPrivateLinkSubnetIds) == 0 {
			existingPrivateLinkSubnetIds = cluster.AWSClientInformation.ClusterNetworking.SubnetIds
		}

		if jumpClusterInstanceType == "" {
			jumpClusterInstanceType = strings.TrimPrefix(aws.ToString(cluster.AWSClientInformation.MskClusterConfig.Provisioned.BrokerNodeGroupInfo.InstanceType), "kafka.")
		}

		if jumpClusterBrokerStorage == 0 {
			jumpClusterBrokerStorage = int(*cluster.AWSClientInformation.MskClusterConfig.Provisioned.BrokerNodeGroupInfo.StorageInfo.EbsStorageInfo.VolumeSize)
		}

		opts.MigrationWizardRequest.TargetEnvironmentId = targetEnvironmentId
		opts.MigrationWizardRequest.TargetBootstrapEndpoint = targetBootstrapEndpoint

		opts.MigrationWizardRequest.ReuseExistingSubnets = true
		opts.MigrationWizardRequest.PrivateLinkExistingSubnetIds = existingPrivateLinkSubnetIds

		opts.MigrationWizardRequest.JumpClusterBrokerSubnetCidr = convertIpToStrings(jumpClusterBrokerSubnetCidr)
		opts.MigrationWizardRequest.JumpClusterSetupHostSubnetCidr = jumpClusterSetupHostSubnetCidr.String()
		opts.MigrationWizardRequest.JumpClusterInstanceType = jumpClusterInstanceType
		opts.MigrationWizardRequest.JumpClusterBrokerStorage = jumpClusterBrokerStorage

		opts.MigrationWizardRequest.MskJumpClusterAuthType = "iam"
		opts.MigrationWizardRequest.MskSaslIamBootstrapServers = bootstrapBrokers
		opts.MigrationWizardRequest.JumpClusterIamAuthRoleName = jumpClusterIamAuthRoleName

	case types.JumpClusterNewSubnetsSaslScram:
		opts.MigrationWizardRequest.HasPublicMskEndpoints = false
		opts.MigrationWizardRequest.UseJumpClusters = true

		if len(jumpClusterBrokerSubnetCidr) != cluster.ClusterMetrics.MetricMetadata.NumberOfBrokerNodes {
			return nil, fmt.Errorf("the number of jump cluster broker subnet CIDRs (%d) does not match the number of broker nodes in the MSK cluster (%d). You should provide as many CIDRs as the MSK cluster has broker nodes.", len(jumpClusterBrokerSubnetCidr), cluster.ClusterMetrics.MetricMetadata.NumberOfBrokerNodes)
		}

		if len(newPrivateLinkSubnetCidr) != 3 {
			return nil, fmt.Errorf("three private link subnet CIDRs are expected, but got %d.", len(newPrivateLinkSubnetCidr))
		}

		if jumpClusterInstanceType == "" {
			jumpClusterInstanceType = strings.TrimPrefix(aws.ToString(cluster.AWSClientInformation.MskClusterConfig.Provisioned.BrokerNodeGroupInfo.InstanceType), "kafka.")
		}

		if jumpClusterBrokerStorage == 0 {
			jumpClusterBrokerStorage = int(*cluster.AWSClientInformation.MskClusterConfig.Provisioned.BrokerNodeGroupInfo.StorageInfo.EbsStorageInfo.VolumeSize)
		}

		opts.MigrationWizardRequest.TargetEnvironmentId = targetEnvironmentId
		opts.MigrationWizardRequest.TargetBootstrapEndpoint = targetBootstrapEndpoint

		opts.MigrationWizardRequest.ReuseExistingSubnets = false
		opts.MigrationWizardRequest.PrivateLinkNewSubnetsCidr = convertIpToStrings(newPrivateLinkSubnetCidr)

		opts.MigrationWizardRequest.JumpClusterBrokerSubnetCidr = convertIpToStrings(jumpClusterBrokerSubnetCidr)
		opts.MigrationWizardRequest.JumpClusterSetupHostSubnetCidr = jumpClusterSetupHostSubnetCidr.String()
		opts.MigrationWizardRequest.JumpClusterInstanceType = jumpClusterInstanceType
		opts.MigrationWizardRequest.JumpClusterBrokerStorage = jumpClusterBrokerStorage

		opts.MigrationWizardRequest.MskJumpClusterAuthType = "sasl_scram"
		opts.MigrationWizardRequest.MskSaslScramBootstrapServers = bootstrapBrokers

	case types.JumpClusterNewSubnetsIam:
		opts.MigrationWizardRequest.HasPublicMskEndpoints = false
		opts.MigrationWizardRequest.UseJumpClusters = true

		if len(jumpClusterBrokerSubnetCidr) != cluster.ClusterMetrics.MetricMetadata.NumberOfBrokerNodes {
			return nil, fmt.Errorf("the number of jump cluster broker subnet CIDRs (%d) does not match the number of broker nodes in the MSK cluster (%d). You should provide as many CIDRs as the MSK cluster has broker nodes.", len(jumpClusterBrokerSubnetCidr), cluster.ClusterMetrics.MetricMetadata.NumberOfBrokerNodes)
		}

		if len(newPrivateLinkSubnetCidr) != 3 {
			return nil, fmt.Errorf("three private link subnet CIDRs are expected, but got %d.", len(newPrivateLinkSubnetCidr))
		}

		if jumpClusterInstanceType == "" {
			jumpClusterInstanceType = strings.TrimPrefix(aws.ToString(cluster.AWSClientInformation.MskClusterConfig.Provisioned.BrokerNodeGroupInfo.InstanceType), "kafka.")
		}

		if jumpClusterBrokerStorage == 0 {
			jumpClusterBrokerStorage = int(*cluster.AWSClientInformation.MskClusterConfig.Provisioned.BrokerNodeGroupInfo.StorageInfo.EbsStorageInfo.VolumeSize)
		}

		opts.MigrationWizardRequest.TargetEnvironmentId = targetEnvironmentId
		opts.MigrationWizardRequest.TargetBootstrapEndpoint = targetBootstrapEndpoint

		opts.MigrationWizardRequest.ReuseExistingSubnets = false
		opts.MigrationWizardRequest.PrivateLinkNewSubnetsCidr = convertIpToStrings(newPrivateLinkSubnetCidr)

		opts.MigrationWizardRequest.JumpClusterBrokerSubnetCidr = convertIpToStrings(jumpClusterBrokerSubnetCidr)
		opts.MigrationWizardRequest.JumpClusterSetupHostSubnetCidr = jumpClusterSetupHostSubnetCidr.String()
		opts.MigrationWizardRequest.JumpClusterInstanceType = jumpClusterInstanceType
		opts.MigrationWizardRequest.JumpClusterBrokerStorage = jumpClusterBrokerStorage

		opts.MigrationWizardRequest.MskJumpClusterAuthType = "iam"
		opts.MigrationWizardRequest.MskSaslIamBootstrapServers = bootstrapBrokers
		opts.MigrationWizardRequest.JumpClusterIamAuthRoleName = jumpClusterIamAuthRoleName
	}

	return opts, nil
}

func getBootstrapBrokers(cluster *types.DiscoveredCluster, migrationType types.MigrationType) (string, error) {
	switch migrationType {
	case types.PublicMskEndpoints:
		return aws.ToString(cluster.AWSClientInformation.BootstrapBrokers.BootstrapBrokerStringPublicSaslScram), nil
	case types.ExternalOutboundClusterLink:
		return aws.ToString(cluster.AWSClientInformation.BootstrapBrokers.BootstrapBrokerStringSaslScram), nil
	case types.JumpClusterReuseExistingSubnetsSaslScram:
		return aws.ToString(cluster.AWSClientInformation.BootstrapBrokers.BootstrapBrokerStringSaslScram), nil
	case types.JumpClusterReuseExistingSubnetsIam:
		return aws.ToString(cluster.AWSClientInformation.BootstrapBrokers.BootstrapBrokerStringSaslIam), nil
	case types.JumpClusterNewSubnetsSaslScram:
		return aws.ToString(cluster.AWSClientInformation.BootstrapBrokers.BootstrapBrokerStringSaslScram), nil
	case types.JumpClusterNewSubnetsIam:
		return aws.ToString(cluster.AWSClientInformation.BootstrapBrokers.BootstrapBrokerStringSaslIam), nil
	default:
		return "<bootstrap broker address not found>", fmt.Errorf("invalid target type: %d", migrationType)
	}
}

func buildExtOutboundBrokers(cluster *types.DiscoveredCluster) []types.ExtOutboundClusterKafkaBroker {
	var brokers []types.ExtOutboundClusterKafkaBroker
	bootstrapBrokers := strings.Split(aws.ToString(cluster.AWSClientInformation.BootstrapBrokers.BootstrapBrokerStringSaslScram), ",")

	var formattedBootstrapBrokers []string
	for _, broker := range bootstrapBrokers {
		formattedBootstrapBrokers = append(formattedBootstrapBrokers, strings.TrimSuffix(broker, ":9096"))
	}
	slices.Sort(formattedBootstrapBrokers)

	for _, subnet := range cluster.AWSClientInformation.ClusterNetworking.Subnets {
		broker := types.ExtOutboundClusterKafkaBroker{
			ID:       fmt.Sprintf("%d", subnet.SubnetMskBrokerId),
			SubnetID: subnet.SubnetId,
			Endpoints: []types.ExtOutboundClusterKafkaEndpoint{
				{
					Host: formattedBootstrapBrokers[subnet.SubnetMskBrokerId-1],
					Port: 9096, // Default port for SASL/SCRAM.
					IP:   subnet.PrivateIpAddress,
				},
			},
		}

		brokers = append(brokers, broker)
	}

	return brokers
}

// `net.IP` slice is used for input validation from flag input. However, the Terraform module expects a string slice.
func convertIpToStrings(ips []net.IPNet) []string {
	var ipStrings []string
	for _, ip := range ips {
		ipStrings = append(ipStrings, ip.String())
	}

	return ipStrings
}
