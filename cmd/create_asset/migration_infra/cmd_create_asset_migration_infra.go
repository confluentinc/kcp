package migration_infra

import (
	"encoding/json"
	"fmt"
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
	outputDir          string

	targetEnvironmentId string
	targetClusterId     string
	targetRestEndpoint  string
	subnetId            string
	securityGroupId     string
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
	optionalFlags.StringVar(&outputDir, "output-dir", "", "The directory to output the migration infrastructure assets to.")
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
	typeTwoFlags.StringVar(&subnetId, "subnet-id", "", "Override subnet ID for the EC2 instance (defaults to MSK broker 1 subnet).")
	typeTwoFlags.StringVar(&securityGroupId, "security-group-id", "", "Override security group ID for the EC2 instance (defaults to MSK cluster security group).")
	migrationInfraCmd.Flags().AddFlagSet(typeTwoFlags)
	groups[typeTwoFlags] = "Type Two Flags"

	migrationInfraCmd.SetUsageFunc(func(c *cobra.Command) error {
		flagOrder := []*pflag.FlagSet{requiredFlags, optionalFlags, baseFlags}
		groupNames := []string{"Required Flags", "Optional Flags", "Base Migration Flags"}

		fmt.Println(`Types:
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

	// Recurring statefile values.
	vpcId := aws.ToString(&cluster.AWSClientInformation.ClusterNetworking.VpcId)
	region := aws.ToString(&cluster.Region)
	mskClusterId := aws.ToString(&cluster.KafkaAdminClientInformation.ClusterID)
	bootstrapBrokers, err := getBootstrapBrokers(cluster, targetType)
	if err != nil {
		return nil, fmt.Errorf("failed to get bootstrap brokers: %v", err)
	}

	opts := &MigrationInfraOpts{
		VpcId:            vpcId,
		Region:           region,
		MskClusterId:     mskClusterId,
		BootstrapBrokers: bootstrapBrokers,

		ClusterLinkName:     clusterLinkName,
		TargetEnvironmentId: targetEnvironmentId,
		TargetClusterId:     targetClusterId,
		TargetRestEndpoint:  targetRestEndpoint,
		SubnetId:            subnetId,
		SecurityGroupId:     securityGroupId,

		MigrationType: targetType,
	}

	switch targetType {
	case types.ExternalOutboundClusterLink:
		if opts.SubnetId == "" {
			if len(cluster.AWSClientInformation.ClusterNetworking.SubnetIds) > 0 {
				opts.SubnetId = cluster.AWSClientInformation.ClusterNetworking.SubnetIds[0]
			} else {
				return nil, fmt.Errorf("no subnet IDs found in cluster networking information")
			}
		}

		if opts.SecurityGroupId == "" {
			if len(cluster.AWSClientInformation.ClusterNetworking.SecurityGroups) > 0 {
				opts.SecurityGroupId = cluster.AWSClientInformation.ClusterNetworking.SecurityGroups[0]
			} else {
				return nil, fmt.Errorf("no security groups found in cluster networking information")
			}
		}

		if opts.ClusterLinkName == "" {
			opts.ClusterLinkName = "kcp-msk-to-cc-link"
		}

		// Extract broker information for external outbound
		opts.ExtOutboundBrokers = extractBrokerInformation(cluster)
	}

	return opts, nil
}

func getBootstrapBrokers(cluster *types.DiscoveredCluster, migrationType types.MigrationType) (string, error) {
	switch migrationType {
	case types.PublicMskEndpoints:
		return aws.ToString(cluster.AWSClientInformation.BootstrapBrokers.BootstrapBrokerStringPublicSaslScram), nil
	default:
		return "<bootstrap broker address not found>", fmt.Errorf("invalid target type: %d", migrationType)
	}
}

func extractBrokerInformation(cluster *types.DiscoveredCluster) []types.ExtOutboundClusterKafkaBroker {
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
