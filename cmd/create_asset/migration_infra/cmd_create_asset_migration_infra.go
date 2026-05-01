package migration_infra

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/confluentinc/kcp/internal/services/iampolicy"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/confluentinc/kcp/internal/utils"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

var (
	stateFile          string
	migrationInfraType string
	clusterLinkName    string

	sourceType               string
	clusterId                string
	oskVpcId                 string
	oskRegion                string
	sourceSaslScramMechanism string

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
	targetClusterType          string
)

func NewMigrationInfraCmd() *cobra.Command {
	migrationInfraCmd := &cobra.Command{
		Use:   "migration-infra",
		Short: "Create migration infrastructure Terraform for a source cluster",
		Long: `Generate the Terraform needed to provision the migration path between the source Kafka cluster and Confluent Cloud. The --type flag selects the migration topology and authentication method.

Type options:

1. Public MSK endpoints — Cluster Link (SASL/SCRAM)
2. Private MSK endpoints — External Outbound Cluster Link (SASL/SCRAM, Enterprise only)
3. Private MSK endpoints — External Outbound Cluster Link (Unauthenticated Plaintext, Enterprise only)
4. Private MSK endpoints — Jump Cluster (SASL/SCRAM)
5. Private MSK endpoints — Jump Cluster (IAM, MSK only)

> **Note:** External Outbound Cluster Linking (Types 2 and 3) is only supported for Enterprise clusters. Dedicated clusters with private MSK endpoints must use Jump Clusters (Type 4 or 5). Dedicated clusters with public MSK endpoints can use Type 1.

**Output:**

The command creates a directory (default: ` + "`migration-infra/`" + `) containing Terraform that provisions, depending on the chosen ` + "`--type`" + `:

- **Jump Cluster Setup Host** (Types 4 & 5) — EC2 instance that bootstraps the Confluent Platform jump cluster.
- **N Jump Cluster broker nodes** (Types 4 & 5) — EC2 instances hosting the Confluent Platform jump cluster brokers, one per source broker.
- **Networking** (Types 2-5) — NAT gateway, Elastic IPs, subnets, security groups, route tables and associations.
- **Private Link** (Types 2-5) — VPC connectivity between the source VPC and Confluent Cloud (consumer-side endpoint or provider-side endpoint service depending on type).
- **Confluent Cloud cluster link** (all types) — connects the source-side bridge to the Confluent Cloud target cluster.`,
		Example: `  # Type 4 — Jump Cluster with SASL/SCRAM, against a private MSK
  kcp create-asset migration-infra \
      --state-file kcp-state.json \
      --source-type msk \
      --cluster-id arn:aws:kafka:us-east-1:XXX:cluster/my-cluster/abc-5 \
      --type 4 \
      --existing-internet-gateway \
      --output-dir type-4 \
      --existing-private-link-vpce-id vpce-0abc123def456789 \
      --jump-cluster-broker-subnet-cidr 10.0.101.0/24,10.0.102.0/24,10.0.103.0/24 \
      --jump-cluster-setup-host-subnet-cidr 10.0.104.0/24 \
      --cluster-link-name type-4-link \
      --target-environment-id env-a1bcde \
      --target-cluster-id lkc-w89xyz \
      --target-rest-endpoint https://lkc-w89xyz.XXX.aws.private.confluent.cloud:443 \
      --target-bootstrap-endpoint lkc-w89xyz.XXX.aws.private.confluent.cloud:9092

  # Type 1 — Public MSK, simple cluster link
  kcp create-asset migration-infra \
      --state-file kcp-state.json \
      --source-type msk \
      --cluster-id arn:aws:kafka:us-east-1:XXX:cluster/my-cluster/abc-5 \
      --type 1 \
      --cluster-link-name simple-link \
      --target-cluster-id lkc-w89xyz \
      --target-rest-endpoint https://lkc-w89xyz.us-east-1.aws.confluent.cloud:443`,
		Annotations: map[string]string{
			iampolicy.AnnotationKey: iamAnnotation(),
		},
		SilenceErrors: true,
		RunE:          runMigrationInfra,
		PreRunE:       preRunMigrationInfra,
	}

	groups := map[*pflag.FlagSet]string{}

	requiredFlags := pflag.NewFlagSet("required", pflag.ExitOnError)
	requiredFlags.SortFlags = false
	requiredFlags.StringVar(&stateFile, "state-file", "", "The path to the kcp state file where the cluster discovery reports have been written to.")
	requiredFlags.StringVar(&sourceType, "source-type", "", "Source type: 'msk' or 'osk' (required)")
	requiredFlags.StringVar(&clusterId, "cluster-id", "", "The cluster identifier (ARN for MSK, cluster ID from credentials file for OSK).")
	requiredFlags.StringVar(&migrationInfraType, "type", "", "The migration-infra type. See README for available options.")
	migrationInfraCmd.Flags().AddFlagSet(requiredFlags)
	groups[requiredFlags] = "Required Flags"

	oskFlags := pflag.NewFlagSet("osk", pflag.ExitOnError)
	oskFlags.SortFlags = false
	oskFlags.StringVar(&oskVpcId, "vpc-id", "", "The VPC ID where the OSK cluster resides. (required for OSK)")
	oskFlags.StringVar(&oskRegion, "region", "", "The AWS region where the OSK cluster's VPC resides. (required for OSK)")
	oskFlags.StringVar(&sourceSaslScramMechanism, "source-sasl-scram-mechanism", "", "[Optional] The SASL/SCRAM mechanism for the source cluster (SCRAM-SHA-256 or SCRAM-SHA-512). Overrides the value from the state file.")
	migrationInfraCmd.Flags().AddFlagSet(oskFlags)
	groups[oskFlags] = "OSK Flags"

	optionalFlags := pflag.NewFlagSet("optional", pflag.ExitOnError)
	optionalFlags.SortFlags = false
	optionalFlags.BoolVar(&existingInternetGateway, "existing-internet-gateway", false, "Whether to use an existing internet gateway. (default: false)")
	optionalFlags.StringVar(&outputDir, "output-dir", "", "The directory to output the migration infrastructure assets to. (default: 'migration-infra')")
	migrationInfraCmd.Flags().AddFlagSet(optionalFlags)
	groups[optionalFlags] = "Optional Flags"

	baseFlags := pflag.NewFlagSet("base", pflag.ExitOnError)
	baseFlags.SortFlags = false
	baseFlags.StringVar(&clusterLinkName, "cluster-link-name", "", "The name of the cluster link that will be created as part of the migration.")
	baseFlags.StringVar(&targetClusterId, "target-cluster-id", "", "The Confluent Cloud cluster ID.")
	baseFlags.StringVar(&targetRestEndpoint, "target-rest-endpoint", "", "The Confluent Cloud cluster REST endpoint.")
	baseFlags.StringVar(&targetClusterType, "target-cluster-type", "", "The Confluent Cloud target cluster type ('dedicated' or 'enterprise').")
	baseFlags.StringVar(&targetEnvironmentId, "target-environment-id", "", "The Confluent Cloud environment ID.")
	migrationInfraCmd.Flags().AddFlagSet(baseFlags)
	groups[baseFlags] = "Base Flags"

	typeTwoThreeFlags := pflag.NewFlagSet("type-two-three", pflag.ExitOnError)
	typeTwoThreeFlags.SortFlags = false
	typeTwoThreeFlags.StringVar(&extOutboundSubnetId, "subnet-id", "", "[Optional] Subnet ID for the EC2 instance that provisions the cluster link. (default:  MSK broker #1 subnet).")
	typeTwoThreeFlags.StringVar(&extOutboundSecurityGroupId, "security-group-id", "", "[Optional] Security group ID for the EC2 instance that provisions the cluster link. (default: MSK cluster security group).")
	migrationInfraCmd.Flags().AddFlagSet(typeTwoThreeFlags)
	groups[typeTwoThreeFlags] = "Type Two/Three Flags"

	typeFourFlags := pflag.NewFlagSet("type-four", pflag.ExitOnError)
	typeFourFlags.SortFlags = false
	typeFourFlags.StringVar(&targetBootstrapEndpoint, "target-bootstrap-endpoint", "", "The bootstrap endpoint to use for the Confluent Cloud cluster.")
	typeFourFlags.StringVar(&existingPrivateLinkVpceId, "existing-private-link-vpce-id", "", "The ID of the existing VPC endpoint for the Private Link connection to Confluent Cloud.")
	typeFourFlags.IPNetSliceVar(&jumpClusterBrokerSubnetCidr, "jump-cluster-broker-subnet-cidr", []net.IPNet{}, "The CIDR blocks to use for the jump cluster broker subnets. You should provide as many CIDRs as the MSK cluster has broker nodes.")
	typeFourFlags.IPNetVar(&jumpClusterSetupHostSubnetCidr, "jump-cluster-setup-host-subnet-cidr", net.IPNet{}, "The CIDR block to use for the jump cluster setup host subnet.")
	typeFourFlags.StringVar(&jumpClusterInstanceType, "jump-cluster-instance-type", "", "[Optional] The instance type to use for the jump cluster. (default: MSK broker type).")
	typeFourFlags.IntVar(&jumpClusterBrokerStorage, "jump-cluster-broker-storage", 0, "[Optional] The storage size to use for the jump cluster brokers. (default: MSK cluster broker storage size).")
	migrationInfraCmd.Flags().AddFlagSet(typeFourFlags)
	groups[typeFourFlags] = "Type Four Flags"

	typeFiveFlags := pflag.NewFlagSet("type-five", pflag.ExitOnError)
	typeFiveFlags.SortFlags = false
	typeFiveFlags.StringVar(&targetBootstrapEndpoint, "target-bootstrap-endpoint", "", "The bootstrap endpoint to use for the Confluent Cloud cluster.")
	typeFiveFlags.StringVar(&existingPrivateLinkVpceId, "existing-private-link-vpce-id", "", "The ID of the existing VPC endpoint for the Private Link connection to Confluent Cloud.")
	typeFiveFlags.IPNetSliceVar(&jumpClusterBrokerSubnetCidr, "jump-cluster-broker-subnet-cidr", []net.IPNet{}, "The CIDR blocks to use for the jump cluster broker subnets. You should provide as many CIDRs as the MSK cluster has broker nodes.")
	typeFiveFlags.IPNetVar(&jumpClusterSetupHostSubnetCidr, "jump-cluster-setup-host-subnet-cidr", net.IPNet{}, "The CIDR block to use for the jump cluster setup host subnet.")
	typeFiveFlags.StringVar(&jumpClusterIamAuthRoleName, "jump-cluster-iam-auth-role-name", "", " The IAM role name to authenticate the cluster link between MSK and the jump cluster.")
	typeFiveFlags.StringVar(&jumpClusterInstanceType, "jump-cluster-instance-type", "", "[Optional] The instance type to use for the jump cluster. (default: MSK broker type).")
	typeFiveFlags.IntVar(&jumpClusterBrokerStorage, "jump-cluster-broker-storage", 0, "[Optional] The storage size to use for the jump cluster brokers. (default: MSK cluster broker storage size).")
	migrationInfraCmd.Flags().AddFlagSet(typeFiveFlags)
	groups[typeFiveFlags] = "Type Five Flags"

	migrationInfraCmd.SetUsageFunc(func(c *cobra.Command) error {
		flagOrder := []*pflag.FlagSet{requiredFlags, oskFlags, optionalFlags, baseFlags, typeTwoThreeFlags, typeFourFlags, typeFiveFlags}
		groupNames := []string{"Required Flags", "OSK Flags", "Optional Flags", "Base Migration Flags", "Type Two/Three Flags", "Type Four Flags", "Type Five Flags"}

		/*
			Type 1 = `HasPublicMskEndpoints` = true
			Type 2 = `HasPublicMskEndpoints` = false | `UseJumpClusters` = false | SASL/SCRAM
			Type 3 = `HasPublicMskEndpoints` = false | `UseJumpClusters` = false | Unauthenticated Plaintext
			Type 4 = `HasPublicMskEndpoints` = false | `UseJumpClusters` = true | `MskJumpClusterAuthType` = SASL/SCRAM
			Type 5 = `HasPublicMskEndpoints` = false | `UseJumpClusters` = true | `MskJumpClusterAuthType` = IAM

			`HasExistingInternetGateway` does not require new branching as it does not require new inputs from the user. Instead
			it just uses a data source for the existing internet gateway instead of creating a new one.
		*/
		fmt.Println(`
Available Migration Types:
  Public MSK Endpoints:
    Type 1: Cluster Link [SASL/SCRAM] (MSK & OSK)
  Private MSK Endpoints:
    Type 2: External Outbound Cluster Link [SASL/SCRAM] (Enterprise clusters only) (MSK & OSK)
    Type 3: External Outbound Cluster Link [Unauthenticated Plaintext] (Enterprise clusters only) (MSK & OSK)
    Type 4: Jump Cluster [SASL/SCRAM] (MSK & OSK)
    Type 5: Jump Cluster [IAM] (MSK)

Note: Types 2 and 3 are only supported for Enterprise clusters. Dedicated clusters with private endpoints must use Type 4 or 5.

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

	_ = migrationInfraCmd.MarkFlagRequired("state-file")
	_ = migrationInfraCmd.MarkFlagRequired("source-type")
	_ = migrationInfraCmd.MarkFlagRequired("cluster-id")
	_ = migrationInfraCmd.MarkFlagRequired("type")
	_ = migrationInfraCmd.MarkFlagRequired("cluster-link-name")
	_ = migrationInfraCmd.MarkFlagRequired("target-cluster-id")
	_ = migrationInfraCmd.MarkFlagRequired("target-rest-endpoint")

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

	if (targetType == types.ExternalOutboundClusterLink || targetType == types.ExternalOutboundClusterLinkPlaintext) && targetClusterType == "dedicated" {
		return fmt.Errorf("external outbound cluster linking (Type 2/3) is not supported for dedicated clusters. Please use jump clusters (Type 4 or 5) for private networking, or Type 1 (Cluster Link) if your MSK brokers are publicly accessible")
	}

	if targetType != types.PublicMskEndpoints {
		_ = cmd.MarkFlagRequired("target-environment-id")
	}

	switch targetType {
	case types.PublicMskEndpoints:
		// No additional flag requirements.

	case types.ExternalOutboundClusterLink, types.ExternalOutboundClusterLinkPlaintext:
		// No additional flags beyond target-environment-id.

	case types.JumpClusterSaslScram:
		_ = cmd.MarkFlagRequired("target-bootstrap-endpoint")
		_ = cmd.MarkFlagRequired("existing-private-link-vpce-id")
		_ = cmd.MarkFlagRequired("jump-cluster-broker-subnet-cidr")
		_ = cmd.MarkFlagRequired("jump-cluster-setup-host-subnet-cidr")

	case types.JumpClusterIam:
		_ = cmd.MarkFlagRequired("target-bootstrap-endpoint")
		_ = cmd.MarkFlagRequired("existing-private-link-vpce-id")
		_ = cmd.MarkFlagRequired("jump-cluster-broker-subnet-cidr")
		_ = cmd.MarkFlagRequired("jump-cluster-setup-host-subnet-cidr")
		_ = cmd.MarkFlagRequired("jump-cluster-iam-auth-role-name")
	}

	return nil
}

func runMigrationInfra(cmd *cobra.Command, args []string) error {
	switch sourceType {
	case "msk":
		// clusterId is validated by MarkFlagRequired
	case "osk":
		if oskVpcId == "" {
			return fmt.Errorf("--vpc-id is required when --source-type is osk")
		}
		if oskRegion == "" {
			return fmt.Errorf("--region is required when --source-type is osk")
		}
		targetType, _ := types.ToMigrationType(migrationInfraType)
		if targetType == types.JumpClusterIam {
			return fmt.Errorf("migration type 5 (Jump Cluster [IAM]) is not supported for OSK sources")
		}
	default:
		return fmt.Errorf("invalid --source-type: %s (must be 'msk' or 'osk')", sourceType)
	}

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
	switch sourceType {
	case "msk":
		return parseMSKMigrationInfraOpts()
	case "osk":
		return parseOSKMigrationInfraOpts()
	default:
		return nil, fmt.Errorf("unsupported source type: %s", sourceType)
	}
}

func parseMSKMigrationInfraOpts() (*MigrationInfraOpts, error) {
	targetType, _ := types.ToMigrationType(migrationInfraType)

	file, err := os.ReadFile(stateFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read statefile %s: %w", stateFile, err)
	}

	var state types.State
	if err := json.Unmarshal(file, &state); err != nil {
		return nil, fmt.Errorf("failed to parse statefile JSON: %w", err)
	}

	cluster, err := state.GetClusterByArn(clusterId)
	if err != nil {
		return nil, fmt.Errorf("failed to get cluster: %w", err)
	}

	if cluster.AWSClientInformation.MskClusterConfig.Provisioned == nil {
		return nil, fmt.Errorf("cluster %s has no provisioned configuration, this could be because the cluster is a serverless cluster which is not supported for migration", cluster.Name)
	}

	// Recurring statefile values.
	vpcId := aws.ToString(&cluster.AWSClientInformation.ClusterNetworking.VpcId)
	region := aws.ToString(&cluster.Region)

	if aws.ToString(&cluster.KafkaAdminClientInformation.ClusterID) == "" {
		return nil, fmt.Errorf("cluster %s has no cluster ID, this could be because the cluster has not had the `kcp scan cluster` command run against it", cluster.Name)
	}
	mskClusterId := aws.ToString(&cluster.KafkaAdminClientInformation.ClusterID)

	opts := &MigrationInfraOpts{
		MigrationWizardRequest: types.MigrationWizardRequest{
			HasExistingInternetGateway: existingInternetGateway,

			VpcId:           vpcId,
			SourceRegion:    region,
			SourceClusterId: mskClusterId,

			ClusterLinkName:          clusterLinkName,
			TargetEnvironmentId:      targetEnvironmentId,
			TargetClusterId:          targetClusterId,
			TargetRestEndpoint:       targetRestEndpoint,
			SourceSaslScramMechanism: "SCRAM-SHA-512",
		},
		OutputDir:     outputDir,
		MigrationType: targetType,
	}

	slog.Info("using MSK default SASL/SCRAM mechanism", "mechanism", opts.MigrationWizardRequest.SourceSaslScramMechanism)

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
		opts.MigrationWizardRequest.HasPublicEndpoints = true
		opts.MigrationWizardRequest.UseJumpClusters = false

		opts.MigrationWizardRequest.SourceSaslScramBootstrapServers = bootstrapBrokers

	case types.ExternalOutboundClusterLink:
		opts.MigrationWizardRequest.HasPublicEndpoints = false
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
			if len(cluster.AWSClientInformation.ClusterNetworking.SecurityGroups) > 0 {
				opts.MigrationWizardRequest.ExtOutboundSecurityGroupId = cluster.AWSClientInformation.ClusterNetworking.SecurityGroups[0]
			} else {
				return nil, fmt.Errorf("no security groups found in cluster networking information")
			}
		}

		opts.MigrationWizardRequest.SourceSaslScramBootstrapServers = bootstrapBrokers

		extOutboundBrokers, err := buildExtOutboundBrokers(cluster)
		if err != nil {
			return nil, fmt.Errorf("failed to build external outbound brokers: %w", err)
		}
		opts.MigrationWizardRequest.ExtOutboundBrokers = extOutboundBrokers

	case types.ExternalOutboundClusterLinkPlaintext:
		opts.MigrationWizardRequest.HasPublicEndpoints = false
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
			if len(cluster.AWSClientInformation.ClusterNetworking.SecurityGroups) > 0 {
				opts.MigrationWizardRequest.ExtOutboundSecurityGroupId = cluster.AWSClientInformation.ClusterNetworking.SecurityGroups[0]
			} else {
				return nil, fmt.Errorf("no security groups found in cluster networking information")
			}
		}

		opts.MigrationWizardRequest.JumpClusterAuthType = "plaintext"
		opts.MigrationWizardRequest.SourcePlaintextBootstrapServers = bootstrapBrokers

		extOutboundBrokers, err := buildExtOutboundBrokersForPlaintext(cluster)
		if err != nil {
			return nil, fmt.Errorf("failed to build external outbound brokers: %w", err)
		}
		opts.MigrationWizardRequest.ExtOutboundBrokers = extOutboundBrokers

	case types.JumpClusterSaslScram:
		opts.MigrationWizardRequest.HasPublicEndpoints = false
		opts.MigrationWizardRequest.UseJumpClusters = true

		if len(jumpClusterBrokerSubnetCidr) != cluster.ClusterMetrics.MetricMetadata.NumberOfBrokerNodes {
			return nil, fmt.Errorf("the number of jump cluster broker subnet CIDRs (%d) does not match the number of broker nodes in the MSK cluster (%d), you should provide as many CIDRs as the MSK cluster has broker nodes", len(jumpClusterBrokerSubnetCidr), cluster.ClusterMetrics.MetricMetadata.NumberOfBrokerNodes)
		}

		if jumpClusterInstanceType == "" {
			jumpClusterInstanceType = strings.TrimPrefix(aws.ToString(cluster.AWSClientInformation.MskClusterConfig.Provisioned.BrokerNodeGroupInfo.InstanceType), "kafka.")
		}

		if jumpClusterBrokerStorage == 0 {
			jumpClusterBrokerStorage = int(*cluster.AWSClientInformation.MskClusterConfig.Provisioned.BrokerNodeGroupInfo.StorageInfo.EbsStorageInfo.VolumeSize)
		}

		opts.MigrationWizardRequest.TargetBootstrapEndpoint = targetBootstrapEndpoint
		opts.MigrationWizardRequest.ExistingPrivateLinkVpceId = existingPrivateLinkVpceId

		opts.MigrationWizardRequest.JumpClusterBrokerSubnetCidr = convertIpToStrings(jumpClusterBrokerSubnetCidr)
		opts.MigrationWizardRequest.JumpClusterSetupHostSubnetCidr = jumpClusterSetupHostSubnetCidr.String()
		opts.MigrationWizardRequest.JumpClusterInstanceType = jumpClusterInstanceType
		opts.MigrationWizardRequest.JumpClusterBrokerStorage = jumpClusterBrokerStorage

		opts.MigrationWizardRequest.JumpClusterAuthType = "sasl_scram"
		opts.MigrationWizardRequest.SourceSaslScramBootstrapServers = bootstrapBrokers

	case types.JumpClusterIam:
		opts.MigrationWizardRequest.HasPublicEndpoints = false
		opts.MigrationWizardRequest.UseJumpClusters = true

		if len(jumpClusterBrokerSubnetCidr) != cluster.ClusterMetrics.MetricMetadata.NumberOfBrokerNodes {
			return nil, fmt.Errorf("the number of jump cluster broker subnet CIDRs (%d) does not match the number of broker nodes in the MSK cluster (%d), you should provide as many CIDRs as the MSK cluster has broker nodes", len(jumpClusterBrokerSubnetCidr), cluster.ClusterMetrics.MetricMetadata.NumberOfBrokerNodes)
		}

		if jumpClusterInstanceType == "" {
			jumpClusterInstanceType = strings.TrimPrefix(aws.ToString(cluster.AWSClientInformation.MskClusterConfig.Provisioned.BrokerNodeGroupInfo.InstanceType), "kafka.")
		}

		if jumpClusterBrokerStorage == 0 {
			jumpClusterBrokerStorage = int(*cluster.AWSClientInformation.MskClusterConfig.Provisioned.BrokerNodeGroupInfo.StorageInfo.EbsStorageInfo.VolumeSize)
		}

		opts.MigrationWizardRequest.TargetBootstrapEndpoint = targetBootstrapEndpoint
		opts.MigrationWizardRequest.ExistingPrivateLinkVpceId = existingPrivateLinkVpceId

		opts.MigrationWizardRequest.JumpClusterBrokerSubnetCidr = convertIpToStrings(jumpClusterBrokerSubnetCidr)
		opts.MigrationWizardRequest.JumpClusterSetupHostSubnetCidr = jumpClusterSetupHostSubnetCidr.String()
		opts.MigrationWizardRequest.JumpClusterInstanceType = jumpClusterInstanceType
		opts.MigrationWizardRequest.JumpClusterBrokerStorage = jumpClusterBrokerStorage

		opts.MigrationWizardRequest.JumpClusterAuthType = "iam"
		opts.MigrationWizardRequest.SourceSaslIamBootstrapServers = bootstrapBrokers
		opts.MigrationWizardRequest.JumpClusterIamAuthRoleName = jumpClusterIamAuthRoleName
	}

	return opts, nil
}

func parseOSKMigrationInfraOpts() (*MigrationInfraOpts, error) {
	targetType, _ := types.ToMigrationType(migrationInfraType)

	file, err := os.ReadFile(stateFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read statefile %s: %w", stateFile, err)
	}

	var state types.State
	if err := json.Unmarshal(file, &state); err != nil {
		return nil, fmt.Errorf("failed to parse statefile JSON: %w", err)
	}

	oskCluster, err := state.GetOSKClusterByID(clusterId)
	if err != nil {
		return nil, fmt.Errorf("failed to get OSK cluster: %w", err)
	}

	if oskCluster.KafkaAdminClientInformation.ClusterID == "" {
		return nil, fmt.Errorf("OSK cluster '%s' has no cluster ID. Run 'kcp scan clusters --source-type osk' first", clusterId)
	}

	sourceClusterId := oskCluster.KafkaAdminClientInformation.ClusterID
	bootstrapServers := strings.Join(oskCluster.BootstrapServers, ",")

	opts := &MigrationInfraOpts{
		MigrationWizardRequest: types.MigrationWizardRequest{
			HasExistingInternetGateway: existingInternetGateway,
			VpcId:                      oskVpcId,
			SourceRegion:               oskRegion,
			SourceClusterId:            sourceClusterId,
			ClusterLinkName:            clusterLinkName,
			TargetClusterId:            targetClusterId,
			TargetRestEndpoint:         targetRestEndpoint,
			SourceSaslScramMechanism:   oskCluster.KafkaAdminClientInformation.SaslMechanism,
		},
		OutputDir:     outputDir,
		MigrationType: targetType,
	}

	switch {
	case sourceSaslScramMechanism != "":
		normalized := types.NormalizeSaslMechanism(sourceSaslScramMechanism)
		if normalized != "SCRAM-SHA-256" && normalized != "SCRAM-SHA-512" {
			return nil, fmt.Errorf("invalid --source-sasl-scram-mechanism value %q: must be SCRAM-SHA-256, SCRAM-SHA-512, SHA256, or SHA512", sourceSaslScramMechanism)
		}
		opts.MigrationWizardRequest.SourceSaslScramMechanism = normalized
		slog.Info("using SASL/SCRAM mechanism from --source-sasl-scram-mechanism flag", "mechanism", normalized)
	case opts.MigrationWizardRequest.SourceSaslScramMechanism != "":
		slog.Info("using SASL/SCRAM mechanism from state file", "mechanism", opts.MigrationWizardRequest.SourceSaslScramMechanism)
	case targetType.RequiresSaslScram():
		return nil, fmt.Errorf("SASL/SCRAM mechanism is required for migration type %d but was not found in the state file. "+
			"Provide it with --source-sasl-scram-mechanism (SCRAM-SHA-256 or SCRAM-SHA-512)", int(targetType))
	}

	if opts.MigrationWizardRequest.ClusterLinkName == "" {
		slog.Warn("no cluster link name provided, using default", "default", "kcp-osk-to-cc-link")
		opts.MigrationWizardRequest.ClusterLinkName = "kcp-osk-to-cc-link"
	}

	switch targetType {
	case types.PublicMskEndpoints:
		opts.MigrationWizardRequest.HasPublicEndpoints = true
		opts.MigrationWizardRequest.UseJumpClusters = false
		opts.MigrationWizardRequest.SourceSaslScramBootstrapServers = bootstrapServers

	case types.ExternalOutboundClusterLink:
		opts.MigrationWizardRequest.HasPublicEndpoints = false
		opts.MigrationWizardRequest.UseJumpClusters = false
		if extOutboundSubnetId == "" {
			return nil, fmt.Errorf("--subnet-id is required for OSK sources with migration type 2")
		}
		if extOutboundSecurityGroupId == "" {
			return nil, fmt.Errorf("--security-group-id is required for OSK sources with migration type 2")
		}
		opts.MigrationWizardRequest.ExtOutboundSubnetId = extOutboundSubnetId
		opts.MigrationWizardRequest.ExtOutboundSecurityGroupId = extOutboundSecurityGroupId
		opts.MigrationWizardRequest.TargetEnvironmentId = targetEnvironmentId
		opts.MigrationWizardRequest.SourceSaslScramBootstrapServers = bootstrapServers
		extOutboundBrokers, err := buildOSKExtOutboundBrokers(oskCluster)
		if err != nil {
			return nil, fmt.Errorf("failed to build external outbound brokers: %w", err)
		}
		opts.MigrationWizardRequest.ExtOutboundBrokers = extOutboundBrokers

	case types.JumpClusterSaslScram:
		opts.MigrationWizardRequest.HasPublicEndpoints = false
		opts.MigrationWizardRequest.UseJumpClusters = true
		if jumpClusterInstanceType == "" {
			return nil, fmt.Errorf("--jump-cluster-instance-type is required for OSK sources with migration type 3")
		}
		if jumpClusterBrokerStorage == 0 {
			return nil, fmt.Errorf("--jump-cluster-broker-storage is required for OSK sources with migration type 3")
		}
		opts.MigrationWizardRequest.TargetEnvironmentId = targetEnvironmentId
		opts.MigrationWizardRequest.TargetBootstrapEndpoint = targetBootstrapEndpoint
		opts.MigrationWizardRequest.ExistingPrivateLinkVpceId = existingPrivateLinkVpceId
		opts.MigrationWizardRequest.JumpClusterBrokerSubnetCidr = convertIpToStrings(jumpClusterBrokerSubnetCidr)
		opts.MigrationWizardRequest.JumpClusterSetupHostSubnetCidr = jumpClusterSetupHostSubnetCidr.String()
		opts.MigrationWizardRequest.JumpClusterInstanceType = jumpClusterInstanceType
		opts.MigrationWizardRequest.JumpClusterBrokerStorage = jumpClusterBrokerStorage
		opts.MigrationWizardRequest.JumpClusterAuthType = "sasl_scram"
		opts.MigrationWizardRequest.SourceSaslScramBootstrapServers = bootstrapServers
	}

	return opts, nil
}

func buildOSKExtOutboundBrokers(cluster *types.OSKDiscoveredCluster) ([]types.ExtOutboundClusterKafkaBroker, error) {
	if len(cluster.BootstrapServers) == 0 {
		return nil, fmt.Errorf("no bootstrap servers found for OSK cluster %s", cluster.ID)
	}

	var brokers []types.ExtOutboundClusterKafkaBroker
	for i, server := range cluster.BootstrapServers {
		host, portStr, err := net.SplitHostPort(server)
		if err != nil {
			return nil, fmt.Errorf("failed to parse bootstrap server '%s': %w", server, err)
		}
		port, err := strconv.Atoi(portStr)
		if err != nil {
			return nil, fmt.Errorf("failed to parse port from '%s': %w", server, err)
		}
		brokers = append(brokers, types.ExtOutboundClusterKafkaBroker{
			ID: fmt.Sprintf("osk-broker-%d", i),
			Endpoints: []types.ExtOutboundClusterKafkaEndpoint{
				{Host: host, Port: port},
			},
		})
	}
	return brokers, nil
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
	case types.ExternalOutboundClusterLinkPlaintext:
		bootstrap = aws.ToString(cluster.AWSClientInformation.BootstrapBrokers.BootstrapBrokerString)
		authMethod = "Plaintext"
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
		return nil, fmt.Errorf("sasl/scram bootstrap brokers string is empty for cluster %s", cluster.Name)
	}

	return buildExtOutboundBrokersFromBootstrap(cluster, bootstrapStr, 9096)
}

func buildExtOutboundBrokersForPlaintext(cluster *types.DiscoveredCluster) ([]types.ExtOutboundClusterKafkaBroker, error) {
	bootstrapStr := aws.ToString(cluster.AWSClientInformation.BootstrapBrokers.BootstrapBrokerString)
	if bootstrapStr == "" {
		return nil, fmt.Errorf("plaintext bootstrap brokers string is empty for cluster %s", cluster.Name)
	}

	return buildExtOutboundBrokersFromBootstrap(cluster, bootstrapStr, 9092)
}

func buildExtOutboundBrokersFromBootstrap(cluster *types.DiscoveredCluster, bootstrapStr string, port int) ([]types.ExtOutboundClusterKafkaBroker, error) {
	bootstrapBrokers := strings.Split(bootstrapStr, ",")

	var formattedBootstrapBrokers []string
	for _, broker := range bootstrapBrokers {
		// Strip the port suffix (e.g., ":9096" or ":9094")
		host, _, _ := strings.Cut(broker, ":")
		formattedBootstrapBrokers = append(formattedBootstrapBrokers, host)
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
					Port: port,
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
