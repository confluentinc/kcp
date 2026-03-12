package migration_infra

import (
	"encoding/json"
	"fmt"
	"log/slog"
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

	existingInternetGateway   bool
	existingPrivateLinkVpceId string
	outputDir                 string

	targetEnvironmentId     string
	targetClusterId         string
	targetRestEndpoint      string
	targetBootstrapEndpoint string

	extOutboundSubnetId        string
	extOutboundSecurityGroupId string

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
	typeTwoFlags.StringVar(&extOutboundSubnetId, "subnet-id", "", "[Optional] Subnet ID for the EC2 instance that provisions the cluster link. (default:  MSK broker #1 subnet).")
	typeTwoFlags.StringVar(&extOutboundSecurityGroupId, "security-group-id", "", "[Optional] Security group ID for the EC2 instance that provisions the cluster link. (default: MSK cluster security group).")
	migrationInfraCmd.Flags().AddFlagSet(typeTwoFlags)
	groups[typeTwoFlags] = "Type Two Flags"

	typeThreeFlags := pflag.NewFlagSet("type-three", pflag.ExitOnError)
	typeThreeFlags.SortFlags = false
	typeThreeFlags.StringVar(&targetEnvironmentId, "target-environment-id", "", "The Confluent Cloud environment ID.")
	typeThreeFlags.StringVar(&targetBootstrapEndpoint, "target-bootstrap-endpoint", "", "The bootstrap endpoint to use for the Confluent Cloud cluster.")
	typeThreeFlags.StringVar(&existingPrivateLinkVpceId, "existing-private-link-vpce-id", "", "The ID of the existing VPC endpoint for the Private Link connection to Confluent Cloud.")
	typeThreeFlags.IPNetSliceVar(&jumpClusterBrokerSubnetCidr, "jump-cluster-broker-subnet-cidr", []net.IPNet{}, "The CIDR blocks to use for the jump cluster broker subnets. You should provide as many CIDRs as the MSK cluster has broker nodes.")
	typeThreeFlags.IPNetVar(&jumpClusterSetupHostSubnetCidr, "jump-cluster-setup-host-subnet-cidr", net.IPNet{}, "The CIDR block to use for the jump cluster setup host subnet.")
	typeThreeFlags.StringVar(&jumpClusterInstanceType, "jump-cluster-instance-type", "", "[Optional] The instance type to use for the jump cluster. (default: MSK broker type).")
	typeThreeFlags.IntVar(&jumpClusterBrokerStorage, "jump-cluster-broker-storage", 0, "[Optional] The storage size to use for the jump cluster brokers. (default: MSK cluster broker storage size).")
	migrationInfraCmd.Flags().AddFlagSet(typeThreeFlags)
	groups[typeThreeFlags] = "Type Three Flags"

	typeFourFlags := pflag.NewFlagSet("type-four", pflag.ExitOnError)
	typeFourFlags.SortFlags = false
	typeFourFlags.StringVar(&targetEnvironmentId, "target-environment-id", "", "The Confluent Cloud environment ID.")
	typeFourFlags.StringVar(&targetBootstrapEndpoint, "target-bootstrap-endpoint", "", "The bootstrap endpoint to use for the Confluent Cloud cluster.")
	typeFourFlags.StringVar(&existingPrivateLinkVpceId, "existing-private-link-vpce-id", "", "The ID of the existing VPC endpoint for the Private Link connection to Confluent Cloud.")
	typeFourFlags.IPNetSliceVar(&jumpClusterBrokerSubnetCidr, "jump-cluster-broker-subnet-cidr", []net.IPNet{}, "The CIDR blocks to use for the jump cluster broker subnets. You should provide as many CIDRs as the MSK cluster has broker nodes.")
	typeFourFlags.IPNetVar(&jumpClusterSetupHostSubnetCidr, "jump-cluster-setup-host-subnet-cidr", net.IPNet{}, "The CIDR block to use for the jump cluster setup host subnet.")
	typeFourFlags.StringVar(&jumpClusterIamAuthRoleName, "jump-cluster-iam-auth-role-name", "", " The IAM role name to authenticate the cluster link between MSK and the jump cluster.")
	typeFourFlags.StringVar(&jumpClusterInstanceType, "jump-cluster-instance-type", "", "[Optional] The instance type to use for the jump cluster. (default: MSK broker type).")
	typeFourFlags.IntVar(&jumpClusterBrokerStorage, "jump-cluster-broker-storage", 0, "[Optional] The storage size to use for the jump cluster brokers. (default: MSK cluster broker storage size).")
	migrationInfraCmd.Flags().AddFlagSet(typeFourFlags)
	groups[typeFourFlags] = "Type Four Flags"

	migrationInfraCmd.SetUsageFunc(func(c *cobra.Command) error {
		flagOrder := []*pflag.FlagSet{requiredFlags, optionalFlags, baseFlags, typeTwoFlags, typeThreeFlags, typeFourFlags}
		groupNames := []string{"Required Flags", "Optional Flags", "Base Migration Flags", "Type Two Flags", "Type Three Flags", "Type Four Flags"}

		/*
			Type 1 = `HasPublicMskEndpoints` = true
			Type 2 = `HasPublicMskEndpoints` = false | `UseJumpClusters` = false
			Type 3 = `HasPublicMskEndpoints` = false | `UseJumpClusters` = true | `MskJumpClusterAuthType` = SASL/SCRAM
			Type 4 = `HasPublicMskEndpoints` = false | `UseJumpClusters` = true | `MskJumpClusterAuthType` = IAM

			`HasExistingInternetGateway` does not require new branching as it does not require new inputs from the user. Instead
			it just uses a data source for the existing internet gateway instead of creating a new one.
		*/
		fmt.Println(`
Available Migration Types:
  Public MSK Endpoints:
    Type 1: Cluster Link [SASL/SCRAM]
  Private MSK Endpoints:
    Type 2: External Outbound Cluster Link [SASL/SCRAM]
    Type 3: Jump Cluster [SASL/SCRAM]
    Type 4: Jump Cluster [IAM]

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

	case types.JumpClusterSaslScram:
		cmd.MarkFlagRequired("target-environment-id")
		cmd.MarkFlagRequired("target-bootstrap-endpoint")
		cmd.MarkFlagRequired("existing-private-link-vpce-id")
		cmd.MarkFlagRequired("jump-cluster-broker-subnet-cidr")
		cmd.MarkFlagRequired("jump-cluster-setup-host-subnet-cidr")

	case types.JumpClusterIam:
		cmd.MarkFlagRequired("target-environment-id")
		cmd.MarkFlagRequired("target-bootstrap-endpoint")
		cmd.MarkFlagRequired("existing-private-link-vpce-id")
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

	if aws.ToString(&cluster.KafkaAdminClientInformation.ClusterID) == "" {
		return nil, fmt.Errorf("cluster %s has no cluster ID. This could be because the cluster has not had the `kcp scan cluster` command run against it.", cluster.Name)
	}
	mskClusterId := aws.ToString(&cluster.KafkaAdminClientInformation.ClusterID)

	opts := &MigrationInfraOpts{
		MigrationWizardRequest: types.MigrationWizardRequest{
			HasExistingInternetGateway: existingInternetGateway,

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
		slog.Warn("⚠️ no cluster link name provided, using default", "default", "kcp-msk-to-cc-link")
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

		opts.MigrationWizardRequest.MskSaslScramBootstrapServers = bootstrapBrokers

		extOutboundBrokers, err := buildExtOutboundBrokers(cluster)
		if err != nil {
			return nil, fmt.Errorf("failed to build external outbound brokers: %w", err)
		}
		opts.MigrationWizardRequest.ExtOutboundBrokers = extOutboundBrokers

	case types.JumpClusterSaslScram:
		opts.MigrationWizardRequest.HasPublicMskEndpoints = false
		opts.MigrationWizardRequest.UseJumpClusters = true

		if len(jumpClusterBrokerSubnetCidr) != cluster.ClusterMetrics.MetricMetadata.NumberOfBrokerNodes {
			return nil, fmt.Errorf("the number of jump cluster broker subnet CIDRs (%d) does not match the number of broker nodes in the MSK cluster (%d). You should provide as many CIDRs as the MSK cluster has broker nodes.", len(jumpClusterBrokerSubnetCidr), cluster.ClusterMetrics.MetricMetadata.NumberOfBrokerNodes)
		}

		if jumpClusterInstanceType == "" {
			jumpClusterInstanceType = strings.TrimPrefix(aws.ToString(cluster.AWSClientInformation.MskClusterConfig.Provisioned.BrokerNodeGroupInfo.InstanceType), "kafka.")
		}

		if jumpClusterBrokerStorage == 0 {
			jumpClusterBrokerStorage = int(*cluster.AWSClientInformation.MskClusterConfig.Provisioned.BrokerNodeGroupInfo.StorageInfo.EbsStorageInfo.VolumeSize)
		}

		opts.MigrationWizardRequest.TargetEnvironmentId = targetEnvironmentId
		opts.MigrationWizardRequest.TargetBootstrapEndpoint = targetBootstrapEndpoint
		opts.MigrationWizardRequest.ExistingPrivateLinkVpceId = existingPrivateLinkVpceId

		opts.MigrationWizardRequest.JumpClusterBrokerSubnetCidr = convertIpToStrings(jumpClusterBrokerSubnetCidr)
		opts.MigrationWizardRequest.JumpClusterSetupHostSubnetCidr = jumpClusterSetupHostSubnetCidr.String()
		opts.MigrationWizardRequest.JumpClusterInstanceType = jumpClusterInstanceType
		opts.MigrationWizardRequest.JumpClusterBrokerStorage = jumpClusterBrokerStorage

		opts.MigrationWizardRequest.MskJumpClusterAuthType = "sasl_scram"
		opts.MigrationWizardRequest.MskSaslScramBootstrapServers = bootstrapBrokers

	case types.JumpClusterIam:
		opts.MigrationWizardRequest.HasPublicMskEndpoints = false
		opts.MigrationWizardRequest.UseJumpClusters = true

		if len(jumpClusterBrokerSubnetCidr) != cluster.ClusterMetrics.MetricMetadata.NumberOfBrokerNodes {
			return nil, fmt.Errorf("the number of jump cluster broker subnet CIDRs (%d) does not match the number of broker nodes in the MSK cluster (%d). You should provide as many CIDRs as the MSK cluster has broker nodes.", len(jumpClusterBrokerSubnetCidr), cluster.ClusterMetrics.MetricMetadata.NumberOfBrokerNodes)
		}

		if jumpClusterInstanceType == "" {
			jumpClusterInstanceType = strings.TrimPrefix(aws.ToString(cluster.AWSClientInformation.MskClusterConfig.Provisioned.BrokerNodeGroupInfo.InstanceType), "kafka.")
		}

		if jumpClusterBrokerStorage == 0 {
			jumpClusterBrokerStorage = int(*cluster.AWSClientInformation.MskClusterConfig.Provisioned.BrokerNodeGroupInfo.StorageInfo.EbsStorageInfo.VolumeSize)
		}

		opts.MigrationWizardRequest.TargetEnvironmentId = targetEnvironmentId
		opts.MigrationWizardRequest.TargetBootstrapEndpoint = targetBootstrapEndpoint
		opts.MigrationWizardRequest.ExistingPrivateLinkVpceId = existingPrivateLinkVpceId

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
	var bootstrap string
	var authMethod string

	switch migrationType {
	case types.PublicMskEndpoints:
		bootstrap = aws.ToString(cluster.AWSClientInformation.BootstrapBrokers.BootstrapBrokerStringPublicSaslScram)
		authMethod = "public SASL/SCRAM"
	case types.ExternalOutboundClusterLink:
		bootstrap = aws.ToString(cluster.AWSClientInformation.BootstrapBrokers.BootstrapBrokerStringSaslScram)
		authMethod = "SASL/SCRAM"
	case types.JumpClusterSaslScram:
		bootstrap = aws.ToString(cluster.AWSClientInformation.BootstrapBrokers.BootstrapBrokerStringSaslScram)
		authMethod = "SASL/SCRAM"
	case types.JumpClusterIam:
		bootstrap = aws.ToString(cluster.AWSClientInformation.BootstrapBrokers.BootstrapBrokerStringSaslIam)
		authMethod = "IAM"
	default:
		return "<bootstrap broker address not found>", fmt.Errorf("invalid target type: %d", migrationType)
	}

	if bootstrap == "" {
		return "", fmt.Errorf("no %s bootstrap brokers found for cluster %s. Ensure the cluster has %s authentication enabled", authMethod, cluster.Name, authMethod)
	}

	return bootstrap, nil
}

func buildExtOutboundBrokers(cluster *types.DiscoveredCluster) ([]types.ExtOutboundClusterKafkaBroker, error) {
	bootstrapStr := aws.ToString(cluster.AWSClientInformation.BootstrapBrokers.BootstrapBrokerStringSaslScram)
	if bootstrapStr == "" {
		return nil, fmt.Errorf("SASL/SCRAM bootstrap brokers string is empty for cluster %s", cluster.Name)
	}

	bootstrapBrokers := strings.Split(bootstrapStr, ",")

	var formattedBootstrapBrokers []string
	for _, broker := range bootstrapBrokers {
		formattedBootstrapBrokers = append(formattedBootstrapBrokers, strings.TrimSuffix(broker, ":9096"))
	}
	slices.Sort(formattedBootstrapBrokers)

	var brokers []types.ExtOutboundClusterKafkaBroker
	for _, subnet := range cluster.AWSClientInformation.ClusterNetworking.Subnets {
		brokerIndex := subnet.SubnetMskBrokerId - 1
		if brokerIndex < 0 || brokerIndex >= len(formattedBootstrapBrokers) {
			return nil, fmt.Errorf("broker ID %d is out of range for the available bootstrap brokers (count: %d)", subnet.SubnetMskBrokerId, len(formattedBootstrapBrokers))
		}

		broker := types.ExtOutboundClusterKafkaBroker{
			ID:       fmt.Sprintf("%d", subnet.SubnetMskBrokerId),
			SubnetID: subnet.SubnetId,
			Endpoints: []types.ExtOutboundClusterKafkaEndpoint{
				{
					Host: formattedBootstrapBrokers[brokerIndex],
					Port: 9096, // Default port for SASL/SCRAM.
					IP:   subnet.PrivateIpAddress,
				},
			},
		}

		brokers = append(brokers, broker)
	}

	return brokers, nil
}

// `net.IP` slice is used for input validation from flag input. However, the Terraform module expects a string slice.
func convertIpToStrings(ips []net.IPNet) []string {
	var ipStrings []string
	for _, ip := range ips {
		ipStrings = append(ipStrings, ip.String())
	}

	return ipStrings
}
