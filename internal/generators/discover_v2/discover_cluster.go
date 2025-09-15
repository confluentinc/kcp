package discover_v2

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
	"github.com/confluentinc/kcp/internal/types"
)

type ClusterDiscovererMSKService interface {
	DescribeClusterV2(ctx context.Context, clusterArn string) (*kafka.DescribeClusterV2Output, error)
	GetBootstrapBrokers(ctx context.Context, clusterArn string) (*kafka.GetBootstrapBrokersOutput, error)
	ListClientVpcConnections(ctx context.Context, clusterArn string) ([]kafkatypes.ClientVpcConnection, error)
	ListClusterOperationsV2(ctx context.Context, clusterArn string) ([]kafkatypes.ClusterOperationV2Summary, error)
	ListNodes(ctx context.Context, clusterArn string) ([]kafkatypes.NodeInfo, error)
	ListScramSecrets(ctx context.Context, clusterArn string) ([]string, error)
	GetClusterPolicy(ctx context.Context, clusterArn string) (*kafka.GetClusterPolicyOutput, error)
	GetCompatibleKafkaVersions(ctx context.Context, clusterArn string) (*kafka.GetCompatibleKafkaVersionsOutput, error)
	IsFetchFromFollowerEnabled(ctx context.Context, cluster kafkatypes.Cluster) (*bool, error)
}

type ClusterDiscovererMetricService interface {
	ProcessProvisionedCluster(ctx context.Context, cluster kafkatypes.Cluster, startDate time.Time, endDate time.Time) (*types.ClusterMetricsV2, error)
	ProcessServerlessCluster(ctx context.Context, cluster kafkatypes.Cluster, startDate time.Time, endDate time.Time) (*types.ClusterMetricsV2, error)
}

type ClusterDiscovererEC2Service interface {
	DescribeSubnets(ctx context.Context, subnetIds []string) (*ec2.DescribeSubnetsOutput, error)
}

type ClusterDiscoverer struct {
	mskService    ClusterDiscovererMSKService
	ec2Service    ClusterDiscovererEC2Service
	metricService ClusterDiscovererMetricService
}

func NewClusterDiscoverer(mskService ClusterDiscovererMSKService, ec2Service ClusterDiscovererEC2Service, metricService ClusterDiscovererMetricService) ClusterDiscoverer {
	return ClusterDiscoverer{
		mskService:    mskService,
		ec2Service:    ec2Service,
		metricService: metricService,
	}
}

func (cd *ClusterDiscoverer) Discover(ctx context.Context, clusterArn string) (*types.DiscoveredCluster, error) {
	awsClientInfo, err := cd.discoverAWSClientInformation(ctx, clusterArn)
	if err != nil {
		return nil, err
	}

	clusterMetric, err := cd.discoverMetrics(ctx, clusterArn)
	if err != nil {
		return nil, err
	}

	return &types.DiscoveredCluster{
		Name:                 aws.ToString(awsClientInfo.MskClusterConfig.ClusterName),
		AWSClientInformation: *awsClientInfo,
		ClusterMetricsV2:     *clusterMetric,
	}, nil
}

func (cd *ClusterDiscoverer) discoverAWSClientInformation(ctx context.Context, clusterArn string) (*types.AWSClientInformation, error) {
	awsClientInfo := types.AWSClientInformation{}

	cluster, err := cd.describeCluster(ctx, clusterArn)
	if err != nil {
		return nil, err
	}
	awsClientInfo.MskClusterConfig = *cluster.ClusterInfo

	brokers, err := cd.getBootstrapBrokers(ctx, clusterArn)
	if err != nil {
		return nil, err
	}
	awsClientInfo.BootstrapBrokers = *brokers

	connections, err := cd.scanClusterVpcConnections(ctx, clusterArn)
	if err != nil {
		return nil, err
	}
	awsClientInfo.ClientVpcConnections = connections

	operations, err := cd.scanClusterOperations(ctx, clusterArn)
	if err != nil {
		return nil, err
	}
	awsClientInfo.ClusterOperations = operations

	nodes, err := cd.scanClusterNodes(ctx, clusterArn)
	if err != nil {
		return nil, err
	}
	awsClientInfo.Nodes = nodes

	scramSecrets, err := cd.scanClusterScramSecrets(ctx, clusterArn)
	if err != nil {
		return nil, err
	}
	awsClientInfo.ScramSecrets = scramSecrets

	policy, err := cd.getClusterPolicy(ctx, clusterArn)
	if err != nil {
		return nil, err
	}
	awsClientInfo.Policy = *policy

	versions, err := cd.getCompatibleKafkaVersions(ctx, clusterArn)
	if err != nil {
		return nil, err
	}
	awsClientInfo.CompatibleVersions = *versions

	if cluster.ClusterInfo.ClusterType == kafkatypes.ClusterTypeServerless {
		slog.Warn("⚠️ Cluster networking not supported for MSK Serverless clusters, skipping networking scan")
	} else {
		networking, err := cd.scanNetworkingInfo(ctx, cluster, nodes)
		if err != nil {
			return nil, err
		}
		awsClientInfo.ClusterNetworking = networking
	}

	return &awsClientInfo, nil
}

func (cd *ClusterDiscoverer) describeCluster(ctx context.Context, clusterArn string) (*kafka.DescribeClusterV2Output, error) {
	slog.Info("🔍 describing cluster", "clusterArn", clusterArn)

	cluster, err := cd.mskService.DescribeClusterV2(ctx, clusterArn)
	if err != nil {
		return nil, fmt.Errorf("❌ Failed to describe cluster: %v", err)
	}

	return cluster, nil
}

func (cd *ClusterDiscoverer) getBootstrapBrokers(ctx context.Context, clusterArn string) (*kafka.GetBootstrapBrokersOutput, error) {
	slog.Info("🔍 scanning for bootstrap brokers", "clusterArn", clusterArn)

	brokers, err := cd.mskService.GetBootstrapBrokers(ctx, clusterArn)
	if err != nil {
		return nil, fmt.Errorf("❌ Failed to scan brokers: %v", err)
	}
	return brokers, nil
}

func (cd *ClusterDiscoverer) scanClusterVpcConnections(ctx context.Context, clusterArn string) ([]kafkatypes.ClientVpcConnection, error) {
	slog.Info("🔍 scanning for client vpc connections", "clusterArn", clusterArn)

	connections, err := cd.mskService.ListClientVpcConnections(ctx, clusterArn)
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

func (cd *ClusterDiscoverer) scanClusterOperations(ctx context.Context, clusterArn string) ([]kafkatypes.ClusterOperationV2Summary, error) {
	slog.Info("🔍 scanning for cluster operations", "clusterArn", clusterArn)

	operations, err := cd.mskService.ListClusterOperationsV2(ctx, clusterArn)
	if err != nil {
		return nil, fmt.Errorf("❌ Failed listing operations: %v", err)
	}
	return operations, nil
}

func (cd *ClusterDiscoverer) scanClusterNodes(ctx context.Context, clusterArn string) ([]kafkatypes.NodeInfo, error) {
	slog.Info("🔍 scanning for cluster nodes", "clusterArn", clusterArn)

	nodes, err := cd.mskService.ListNodes(ctx, clusterArn)
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

func (cd *ClusterDiscoverer) scanClusterScramSecrets(ctx context.Context, clusterArn string) ([]string, error) {
	slog.Info("🔍 scanning for cluster scram secrets", "clusterArn", clusterArn)

	secrets, err := cd.mskService.ListScramSecrets(ctx, clusterArn)
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

func (cd *ClusterDiscoverer) getClusterPolicy(ctx context.Context, clusterArn string) (*kafka.GetClusterPolicyOutput, error) {
	slog.Info("🔍 scanning for cluster policy", "clusterArn", clusterArn)

	policy, err := cd.mskService.GetClusterPolicy(ctx, clusterArn)
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

func (cs *ClusterDiscoverer) getCompatibleKafkaVersions(ctx context.Context, clusterArn string) (*kafka.GetCompatibleKafkaVersionsOutput, error) {
	slog.Info("🔍 scanning for compatible kafka versions", "clusterArn", clusterArn)

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

func (cd *ClusterDiscoverer) scanNetworkingInfo(ctx context.Context, cluster *kafka.DescribeClusterV2Output, nodes []kafkatypes.NodeInfo) (types.ClusterNetworking, error) {
	subnetIds := cluster.ClusterInfo.Provisioned.BrokerNodeGroupInfo.ClientSubnets
	securityGroups := cluster.ClusterInfo.Provisioned.BrokerNodeGroupInfo.SecurityGroups

	vpcId, err := cd.getVpcIdFromSubnets(ctx, subnetIds)
	if err != nil {
		return types.ClusterNetworking{}, fmt.Errorf("failed to get VPC ID: %v", err)
	}

	subnetDetails, err := cd.getSubnetDetails(ctx, subnetIds)
	if err != nil {
		return types.ClusterNetworking{}, fmt.Errorf("failed to get subnet details: %v", err)
	}

	subnetInfo := cd.createCombinedSubnetBrokerInfo(nodes, subnetDetails)

	return types.ClusterNetworking{
		VpcId:          vpcId,
		SubnetIds:      subnetIds,
		SecurityGroups: securityGroups,
		Subnets:        subnetInfo,
	}, nil
}

func (cd *ClusterDiscoverer) getVpcIdFromSubnets(ctx context.Context, subnetIds []string) (string, error) {
	// Only way to get the VPC ID is to query the subnets belonging to the cluster brokers.
	result, err := cd.ec2Service.DescribeSubnets(ctx, []string{subnetIds[0]})
	if err != nil {
		return "", fmt.Errorf("failed to describe subnet %s: %v", subnetIds[0], err)
	}

	if len(result.Subnets) > 0 && result.Subnets[0].VpcId != nil {
		return aws.ToString(result.Subnets[0].VpcId), nil
	}

	return "", fmt.Errorf("no VPC ID found for subnet %s", subnetIds[0])
}

func (cd *ClusterDiscoverer) getSubnetDetails(ctx context.Context, subnetIds []string) (map[string]types.SubnetInfo, error) {
	result, err := cd.ec2Service.DescribeSubnets(ctx, subnetIds)
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

func (cd *ClusterDiscoverer) createCombinedSubnetBrokerInfo(nodes []kafkatypes.NodeInfo, subnetDetails map[string]types.SubnetInfo) []types.SubnetInfo {
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

func (cd *ClusterDiscoverer) discoverMetrics(ctx context.Context, clusterArn string) (*types.ClusterMetricsV2, error) {
	cluster, err := cd.mskService.DescribeClusterV2(context.Background(), clusterArn)
	if err != nil {
		return nil, fmt.Errorf("❌ Failed to get clusters: %v", err)
	}

	followerFetching, err := cd.mskService.IsFetchFromFollowerEnabled(context.Background(), *cluster.ClusterInfo)
	if err != nil {
		return nil, fmt.Errorf("failed to check if follower fetching is enabled: %v", err)
	}

	// Handle case where followerFetching is nil (cluster doesn't have configuration info)
	followerFetchingEnabled := aws.ToBool(followerFetching)

	now := time.Now().UTC()
	endDate := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	startDate := endDate.AddDate(0, 0, -7)

	var clusterMetric *types.ClusterMetricsV2

	if cluster.ClusterInfo.ClusterType == kafkatypes.ClusterTypeProvisioned {
		clusterMetric, err = cd.metricService.ProcessProvisionedCluster(ctx, *cluster.ClusterInfo, startDate, endDate)
		if err != nil {
			return nil, fmt.Errorf("failed to process provisioned cluster: %v", err)
		}
	} else {
		clusterMetric, err = cd.metricService.ProcessServerlessCluster(ctx, *cluster.ClusterInfo, startDate, endDate)
		if err != nil {
			return nil, fmt.Errorf("failed to process serverless cluster: %v", err)
		}
	}

	clusterMetric.MetricMetadata.FollowerFetching = followerFetchingEnabled

	return clusterMetric, nil
}
