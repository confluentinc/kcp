package cluster

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/kafka"
	kafkatypes "github.com/aws/aws-sdk-go-v2/service/kafka/types"
	"github.com/confluentinc/kcp/internal/client"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/confluentinc/kcp/internal/utils"
)

// KafkaAdminFactory is a function type that creates a KafkaAdmin client
type KafkaAdminFactory func(brokerAddresses []string, clientBrokerEncryptionInTransit kafkatypes.ClientBroker, kafkaVersion string) (client.KafkaAdmin, error)

type ClusterScannerOpts struct {
	Region            string
	ClusterArn        string
	SkipKafka         bool
	AuthType          types.AuthType
	SASLScramUsername string
	SASLScramPassword string
	TLSCACert         string
	TLSClientCert     string
	TLSClientKey      string
}

type ClusterScanner struct {
	mskService        MSKService
	ec2Service        EC2Service
	kafkaAdminFactory KafkaAdminFactory
	region            string
	clusterArn        string
	skipKafka         bool
	authType          types.AuthType
}

type MSKService interface {
	GetBootstrapBrokers(ctx context.Context, clusterArn *string) (*kafka.GetBootstrapBrokersOutput, error)
	ParseBrokerAddresses(brokers kafka.GetBootstrapBrokersOutput, authType types.AuthType) ([]string, error)
	GetCompatibleKafkaVersions(ctx context.Context, clusterArn *string) (*kafka.GetCompatibleKafkaVersionsOutput, error)
	GetClusterPolicy(ctx context.Context, clusterArn *string) (*kafka.GetClusterPolicyOutput, error)
	DescribeClusterV2(ctx context.Context, clusterArn *string) (*kafka.DescribeClusterV2Output, error)
	ListClientVpcConnections(ctx context.Context, clusterArn *string) ([]kafkatypes.ClientVpcConnection, error)
	ListClusterOperationsV2(ctx context.Context, clusterArn *string) ([]kafkatypes.ClusterOperationV2Summary, error)
	ListNodes(ctx context.Context, clusterArn *string) ([]kafkatypes.NodeInfo, error)
	ListScramSecrets(ctx context.Context, clusterArn *string) ([]string, error)
}

type EC2Service interface {
	DescribeSubnets(ctx context.Context, subnetIds []string) (*ec2.DescribeSubnetsOutput, error)
}

// NewClusterScanner creates a new ClusterScanner instance.
func NewClusterScanner(mskService MSKService, ec2Service EC2Service, kafkaAdminFactory KafkaAdminFactory, opts ClusterScannerOpts) *ClusterScanner {
	return &ClusterScanner{
		mskService:        mskService,
		ec2Service:        ec2Service,
		kafkaAdminFactory: kafkaAdminFactory,
		region:            opts.Region,
		clusterArn:        opts.ClusterArn,
		skipKafka:         opts.SkipKafka,
		authType:          opts.AuthType,
	}
}

func (cs *ClusterScanner) Run() error {
	slog.Info("🚀 starting cluster scan", "cluster", cs.clusterArn)

	ctx := context.TODO()

	clusterInfo, err := cs.ScanCluster(ctx)
	if err != nil {
		return err
	}

	if err := clusterInfo.WriteAsJson(); err != nil {
		return fmt.Errorf("❌ Failed to generate json report: %v", err)
	}

	// Generate markdown report
	if err := clusterInfo.WriteAsMarkdown(false); err != nil {
		return fmt.Errorf("❌ Failed to generate markdown report: %v", err)
	}

	slog.Info("✅ cluster scan complete",
		"cluster", cs.clusterArn,
		"clusterName", clusterInfo.Cluster.ClusterName,
		"topicCount", len(clusterInfo.Topics),
		"filePath", clusterInfo.GetJsonPath(),
		"markdownPath", clusterInfo.GetMarkdownPath(),
	)

	return nil
}

func (cs *ClusterScanner) ScanCluster(ctx context.Context) (*types.ClusterInformation, error) {
	clusterInfo := &types.ClusterInformation{
		Timestamp: time.Now(),
		Region:    cs.region,
	}

	if err := cs.scanAWSResources(ctx, clusterInfo); err != nil {
		return nil, err
	}

	if !cs.skipKafka {
		if err := cs.scanKafkaResources(clusterInfo); err != nil {
			return nil, err
		}
	} else {
		slog.Info("🔍 skipping kafka level cluster scan", "clusterArn", cs.clusterArn)
	}

	return clusterInfo, nil
}

func (cs *ClusterScanner) scanAWSResources(ctx context.Context, clusterInfo *types.ClusterInformation) error {

	cluster, err := cs.describeCluster(ctx, &cs.clusterArn)
	if err != nil {
		return err
	}
	clusterInfo.Cluster = *cluster.ClusterInfo

	brokers, err := cs.getBootstrapBrokers(ctx, &cs.clusterArn)
	if err != nil {
		return err
	}
	clusterInfo.BootstrapBrokers = *brokers

	connections, err := cs.scanClusterVpcConnections(ctx, &cs.clusterArn)
	if err != nil {
		return err
	}
	clusterInfo.ClientVpcConnections = connections

	operations, err := cs.scanClusterOperations(ctx, &cs.clusterArn)
	if err != nil {
		return err
	}
	clusterInfo.ClusterOperations = operations

	nodes, err := cs.scanClusterNodes(ctx, &cs.clusterArn)
	if err != nil {
		return err
	}
	clusterInfo.Nodes = nodes

	scramSecrets, err := cs.scanClusterScramSecrets(ctx, &cs.clusterArn)
	if err != nil {
		return err
	}
	clusterInfo.ScramSecrets = scramSecrets

	policy, err := cs.getClusterPolicy(ctx, &cs.clusterArn)
	if err != nil {
		return err
	}
	clusterInfo.Policy = *policy

	versions, err := cs.getCompatibleKafkaVersions(ctx, &cs.clusterArn)
	if err != nil {
		return err
	}
	clusterInfo.CompatibleVersions = *versions

	if clusterInfo.Cluster.ClusterType == kafkatypes.ClusterTypeProvisioned {
		networking, err := cs.scanNetworkingInfo(ctx, cluster, nodes)
		if err != nil {
			return err
		}
		clusterInfo.ClusterNetworking = networking
	} else {
		slog.Warn("⚠️ Cluster networking not supported for MSK Serverless clusters, skipping networking scan")
	}

	return nil
}

func (cs *ClusterScanner) getCompatibleKafkaVersions(ctx context.Context, clusterArn *string) (*kafka.GetCompatibleKafkaVersionsOutput, error) {
	slog.Info("🔍 scanning for compatible kafka versions", "clusterArn", cs.clusterArn)

	versions, err := cs.mskService.GetCompatibleKafkaVersions(ctx, clusterArn)
	if err != nil {
		// Check if it's an MSK Serverless error - this should be handled gracefully
		if strings.Contains(err.Error(), "This operation cannot be performed on serverless clusters.") {
			slog.Warn("⚠️ Compatible versions not supported for MSK Serverless clusters, skipping compatible versions scan")
			return &kafka.GetCompatibleKafkaVersionsOutput{
				CompatibleKafkaVersions: []kafkatypes.CompatibleKafkaVersion{},
			}, nil
		}
		return nil, fmt.Errorf("❌ Failed to get compatible versions: %v", err)
	}
	return versions, nil
}

func (cs *ClusterScanner) getClusterPolicy(ctx context.Context, clusterArn *string) (*kafka.GetClusterPolicyOutput, error) {
	slog.Info("🔍 scanning for cluster policy", "clusterArn", cs.clusterArn)

	policy, err := cs.mskService.GetClusterPolicy(ctx, clusterArn)
	if err != nil {
		// Check if it's a NotFoundException - this is expected and should be handled gracefully
		var notFoundErr *kafkatypes.NotFoundException
		if errors.As(err, &notFoundErr) {
			// Return empty policy for NotFoundException - this is expected behavior
			return &kafka.GetClusterPolicyOutput{}, nil
		}
		return nil, err
	}
	return policy, nil
}

func (cs *ClusterScanner) getBootstrapBrokers(ctx context.Context, clusterArn *string) (*kafka.GetBootstrapBrokersOutput, error) {
	slog.Info("🔍 scanning for bootstrap brokers", "clusterArn", cs.clusterArn)

	brokers, err := cs.mskService.GetBootstrapBrokers(ctx, clusterArn)
	if err != nil {
		return nil, fmt.Errorf("❌ Failed to scan brokers: %v", err)
	}
	return brokers, nil
}

func (cs *ClusterScanner) describeCluster(ctx context.Context, clusterArn *string) (*kafka.DescribeClusterV2Output, error) {
	slog.Info("🔍 describing cluster", "clusterArn", cs.clusterArn)

	cluster, err := cs.mskService.DescribeClusterV2(ctx, clusterArn)
	if err != nil {
		return nil, fmt.Errorf("❌ Failed to describe cluster: %v", err)
	}

	return cluster, nil
}

func (cs *ClusterScanner) scanNetworkingInfo(ctx context.Context, cluster *kafka.DescribeClusterV2Output, nodes []kafkatypes.NodeInfo) (types.ClusterNetworking, error) {
	subnetIds := cluster.ClusterInfo.Provisioned.BrokerNodeGroupInfo.ClientSubnets
	securityGroups := cluster.ClusterInfo.Provisioned.BrokerNodeGroupInfo.SecurityGroups

	vpcId, err := cs.getVpcIdFromSubnets(ctx, subnetIds)
	if err != nil {
		return types.ClusterNetworking{}, fmt.Errorf("failed to get VPC ID: %v", err)
	}

	subnetDetails, err := cs.getSubnetDetails(ctx, subnetIds)
	if err != nil {
		return types.ClusterNetworking{}, fmt.Errorf("failed to get subnet details: %v", err)
	}

	subnetInfo := cs.createCombinedSubnetBrokerInfo(nodes, subnetDetails)

	return types.ClusterNetworking{
		VpcId:          vpcId,
		SubnetIds:      subnetIds,
		SecurityGroups: securityGroups,
		Subnets:        subnetInfo,
	}, nil
}

func (cs *ClusterScanner) getVpcIdFromSubnets(ctx context.Context, subnetIds []string) (string, error) {
	// Only way to get the VPC ID is to query the subnets belonging to the cluster brokers.
	result, err := cs.ec2Service.DescribeSubnets(ctx, []string{subnetIds[0]})
	if err != nil {
		return "", fmt.Errorf("failed to describe subnet %s: %v", subnetIds[0], err)
	}

	if len(result.Subnets) > 0 && result.Subnets[0].VpcId != nil {
		return aws.ToString(result.Subnets[0].VpcId), nil
	}

	return "", fmt.Errorf("no VPC ID found for subnet %s", subnetIds[0])
}

func (cs *ClusterScanner) getSubnetDetails(ctx context.Context, subnetIds []string) (map[string]types.SubnetInfo, error) {
	result, err := cs.ec2Service.DescribeSubnets(ctx, subnetIds)
	if err != nil {
		return nil, fmt.Errorf("failed to describe subnets: %v", err)
	}

	subnets := make(map[string]types.SubnetInfo)
	for _, subnet := range result.Subnets {
		subnetInfo := types.SubnetInfo{
			SubnetId:         aws.ToString(subnet.SubnetId),
			AvailabilityZone: aws.ToString(subnet.AvailabilityZone),
			CidrBlock:        aws.ToString(subnet.CidrBlock),
		}
		subnets[subnetInfo.SubnetId] = subnetInfo
	}

	return subnets, nil
}

func (cs *ClusterScanner) createCombinedSubnetBrokerInfo(nodes []kafkatypes.NodeInfo, subnetDetails map[string]types.SubnetInfo) []types.SubnetInfo {
	var subnetInfo []types.SubnetInfo

	for _, node := range nodes {
		// Grab subnets only from broker nodes.
		if node.NodeType == kafkatypes.NodeTypeBroker && node.BrokerNodeInfo != nil {
			subnetId := aws.ToString(node.BrokerNodeInfo.ClientSubnet)

			if details, exists := subnetDetails[subnetId]; exists {
				brokerId := 0

				if node.BrokerNodeInfo.BrokerId != nil {
					brokerId = int(*node.BrokerNodeInfo.BrokerId)
				}

				combinedSubnet := details
				combinedSubnet.SubnetMskBrokerId = brokerId
				combinedSubnet.PrivateIpAddress = aws.ToString(node.BrokerNodeInfo.ClientVpcIpAddress)

				subnetInfo = append(subnetInfo, combinedSubnet)
			}
		}
	}

	return subnetInfo
}

func (cs *ClusterScanner) scanClusterVpcConnections(ctx context.Context, clusterArn *string) ([]kafkatypes.ClientVpcConnection, error) {
	slog.Info("🔍 scanning for client vpc connections", "clusterArn", cs.clusterArn)

	connections, err := cs.mskService.ListClientVpcConnections(ctx, clusterArn)
	if err != nil {
		// Check if it's an MSK Serverless VPC connectivity error - this should be handled gracefully
		if strings.Contains(err.Error(), "This Region doesn't currently support VPC connectivity with Amazon MSK Serverless clusters") {
			slog.Warn("⚠️ VPC connectivity not supported for MSK Serverless clusters in this region, skipping VPC connections scan")
			return []kafkatypes.ClientVpcConnection{}, nil
		}
		return nil, fmt.Errorf("❌ Failed listing client vpc connections: %v", err)
	}
	return connections, nil
}

func (cs *ClusterScanner) scanClusterOperations(ctx context.Context, clusterArn *string) ([]kafkatypes.ClusterOperationV2Summary, error) {
	slog.Info("🔍 scanning for cluster operations", "clusterArn", cs.clusterArn)

	operations, err := cs.mskService.ListClusterOperationsV2(ctx, clusterArn)
	if err != nil {
		return nil, fmt.Errorf("❌ Failed listing operations: %v", err)
	}
	return operations, nil
}

func (cs *ClusterScanner) scanClusterNodes(ctx context.Context, clusterArn *string) ([]kafkatypes.NodeInfo, error) {
	slog.Info("🔍 scanning for cluster nodes", "clusterArn", cs.clusterArn)

	nodes, err := cs.mskService.ListNodes(ctx, clusterArn)
	if err != nil {
		// Check if it's an MSK Serverless error - this should be handled gracefully
		if strings.Contains(err.Error(), "This operation cannot be performed on serverless clusters.") {
			slog.Warn("⚠️ Node listing not supported for MSK Serverless clusters, skipping Nodes scan")
			return []kafkatypes.NodeInfo{}, nil
		}
		return nil, fmt.Errorf("❌ Failed listing nodes: %v", err)
	}

	return nodes, nil
}

func (cs *ClusterScanner) scanClusterScramSecrets(ctx context.Context, clusterArn *string) ([]string, error) {
	slog.Info("🔍 scanning for cluster scram secrets", "clusterArn", cs.clusterArn)

	secrets, err := cs.mskService.ListScramSecrets(ctx, clusterArn)
	if err != nil {
		// Check if it's an MSK Serverless error - this should be handled gracefully
		if strings.Contains(err.Error(), "This operation cannot be performed on serverless clusters.") {
			slog.Warn("⚠️ Scram secret listing not supported for MSK Serverless clusters, skipping scram secrets scan")
			return []string{}, nil
		}
		return nil, fmt.Errorf("❌ Failed listing secrets: %v", err)
	}

	return secrets, nil
}

func (cs *ClusterScanner) scanClusterTopics(admin client.KafkaAdmin) ([]string, error) {
	slog.Info("🔍 scanning for cluster topics", "clusterArn", cs.clusterArn)

	topics, err := admin.ListTopics()
	if err != nil {
		return nil, fmt.Errorf("❌ Failed to list topics: %v", err)
	}

	topicList := make([]string, 0, len(topics))
	for topic := range topics {
		topicList = append(topicList, topic)
	}

	return topicList, nil
}

// retrieveClusterId gets cluster metadata and returns the cluster ID along with logging information
func (cs *ClusterScanner) describeKafkaCluster(admin client.KafkaAdmin) (*client.ClusterKafkaMetadata, error) {
	slog.Info("🔍 describing kafka cluster", "clusterArn", cs.clusterArn)

	clusterMetadata, err := admin.GetClusterKafkaMetadata()
	if err != nil {
		return nil, fmt.Errorf("❌ Failed to describe kafka cluster: %v", err)
	}
	return clusterMetadata, nil
}

func (cs *ClusterScanner) scanKafkaResources(clusterInfo *types.ClusterInformation) error {

	brokerAddresses, err := cs.mskService.ParseBrokerAddresses(clusterInfo.BootstrapBrokers, cs.authType)
	if err != nil {
		return err
	}

	clientBrokerEncryptionInTransit := types.GetClientBrokerEncryptionInTransit(clusterInfo.Cluster)
	kafkaVersion := cs.getKafkaVersion(clusterInfo)

	admin, err := cs.kafkaAdminFactory(brokerAddresses, clientBrokerEncryptionInTransit, kafkaVersion)
	if err != nil {
		return fmt.Errorf("❌ Failed to setup admin client: %v", err)
	}

	defer admin.Close()

	// Get cluster metadata including broker information and ClusterID
	clusterMetadata, err := cs.describeKafkaCluster(admin)
	if err != nil {
		return err
	}

	clusterInfo.ClusterID = clusterMetadata.ClusterID

	topics, err := cs.scanClusterTopics(admin)
	if err != nil {
		return err
	}
	clusterInfo.Topics = topics

	// Serverless clusters do not support Kafka Admin API and instead returns an EOF error - this should be handled gracefully
	if clusterInfo.Cluster.ClusterType == kafkatypes.ClusterTypeProvisioned {
		acls, err := cs.scanKafkaAcls(admin)
		if err != nil {
			return err
		}
		clusterInfo.Acls = acls
	} else {
		slog.Warn("⚠️ Serverless clusters do not support querying Kafka ACLs, skipping ACLs scan")
	}

	return nil
}

func (cs *ClusterScanner) scanKafkaAcls(admin client.KafkaAdmin) ([]types.Acls, error) {
	slog.Info("🔍 scanning for kafka acls", "clusterArn", cs.clusterArn)

	acls, err := admin.ListAcls()
	if err != nil {
		return nil, fmt.Errorf("❌ Failed to list acls: %v", err)
	}

	// Flatten the ACLs for easier processing
	var flattenedAcls []types.Acls
	for _, resourceAcl := range acls {
		for _, acl := range resourceAcl.Acls {
			flattenedAcl := types.Acls{
				ResourceType:        resourceAcl.ResourceType.String(),
				ResourceName:        resourceAcl.ResourceName,
				ResourcePatternType: resourceAcl.ResourcePatternType.String(),
				Principal:           acl.Principal,
				Host:                acl.Host,
				Operation:           acl.Operation.String(),
				PermissionType:      acl.PermissionType.String(),
			}
			flattenedAcls = append(flattenedAcls, flattenedAcl)
		}
	}

	return flattenedAcls, nil
}

func (cs *ClusterScanner) getKafkaVersion(clusterInfo *types.ClusterInformation) string {
	switch clusterInfo.Cluster.ClusterType {
	case kafkatypes.ClusterTypeProvisioned:
		return utils.ConvertKafkaVersion(clusterInfo.Cluster.Provisioned.CurrentBrokerSoftwareInfo.KafkaVersion)
	case kafkatypes.ClusterTypeServerless:
		slog.Warn("⚠️ Serverless clusters do not return a Kafka version, defaulting to 4.0.0")
		return "4.0.0"
	default:
		slog.Warn(fmt.Sprintf("⚠️ Unknown cluster type: %v, defaulting to 4.0.0", clusterInfo.Cluster.ClusterType))
		return "4.0.0"
	}
}
