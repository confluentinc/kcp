package discover

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
	"github.com/confluentinc/kcp/internal/services/metrics"
	"github.com/confluentinc/kcp/internal/types"
)

type ClusterDiscovererMSKService interface {
	DescribeClusterV2(ctx context.Context, clusterArn string) (*kafka.DescribeClusterV2Output, error)
	GetBootstrapBrokers(ctx context.Context, clusterArn string) (*kafka.GetBootstrapBrokersOutput, error)
	ListClientVpcConnections(ctx context.Context, clusterArn string, maxResults int32) ([]kafkatypes.ClientVpcConnection, error)
	ListClusterOperationsV2(ctx context.Context, clusterArn string, maxResults int32) ([]kafkatypes.ClusterOperationV2Summary, error)
	ListNodes(ctx context.Context, clusterArn string, maxResults int32) ([]kafkatypes.NodeInfo, error)
	ListScramSecrets(ctx context.Context, clusterArn string, maxResults int32) ([]string, error)
	GetClusterPolicy(ctx context.Context, clusterArn string) (*kafka.GetClusterPolicyOutput, error)
	GetCompatibleKafkaVersions(ctx context.Context, clusterArn string) (*kafka.GetCompatibleKafkaVersionsOutput, error)
	IsFetchFromFollowerEnabled(ctx context.Context, cluster kafkatypes.Cluster) (bool, error)
	GetTopicsWithConfigs(ctx context.Context, clusterArn string) ([]types.TopicDetails, error)
}

type ClusterDiscovererMetricService interface {
	ProcessProvisionedCluster(ctx context.Context, cluster kafkatypes.Cluster, followerFetching bool, timeWindow types.CloudWatchTimeWindow) (*types.ClusterMetrics, error)
	ProcessServerlessCluster(ctx context.Context, cluster kafkatypes.Cluster, timeWindow types.CloudWatchTimeWindow) (*types.ClusterMetrics, error)
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

func (cd *ClusterDiscoverer) Discover(ctx context.Context, clusterArn, region string, skipTopics bool, skipMetrics bool, metricsGranularity string) (*types.DiscoveredCluster, error) {
	awsClientInfo, kafkaClientInfo, err := cd.discoverAWSClientInformation(ctx, clusterArn, skipTopics)
	if err != nil {
		return nil, err
	}

	var clusterMetric *types.ClusterMetrics
	if skipMetrics {
		fmt.Printf("  ⏭️  Skipping metrics discovery\n")
		clusterMetric = &types.ClusterMetrics{}
	} else {
		clusterMetric, err = cd.discoverMetrics(ctx, clusterArn, metricsGranularity)
		if err != nil {
			return nil, err
		}
	}

	return &types.DiscoveredCluster{
		Name:                        aws.ToString(awsClientInfo.MskClusterConfig.ClusterName),
		Arn:                         clusterArn,
		Region:                      region,
		AWSClientInformation:        *awsClientInfo,
		KafkaAdminClientInformation: *kafkaClientInfo,
		ClusterMetrics:              *clusterMetric,
	}, nil
}

func (cd *ClusterDiscoverer) discoverAWSClientInformation(ctx context.Context, clusterArn string, skipTopics bool) (*types.AWSClientInformation, *types.KafkaAdminClientInformation, error) {
	awsClientInfo := types.AWSClientInformation{}
	kafkaClientInfo := types.KafkaAdminClientInformation{}

	cluster, err := cd.describeCluster(ctx, clusterArn)
	if err != nil {
		return nil, nil, err
	}
	if cluster.ClusterInfo == nil {
		return nil, nil, fmt.Errorf("describeClusterV2 returned nil ClusterInfo for %s", clusterArn)
	}
	awsClientInfo.MskClusterConfig = *cluster.ClusterInfo

	// MSK Serverless does not support several AWS-API metadata scans (VPC
	// connections, nodes, SCRAM secrets, compatible versions, networking) or the
	// AWS topic APIs. Announce it once per cluster; the individual per-scan skips
	// stay in kcp.log at Debug.
	isServerless := cluster.ClusterInfo.ClusterType == kafkatypes.ClusterTypeServerless
	if isServerless {
		slog.Warn("⚠️ MSK Serverless cluster; skipping unsupported scans (VPC connections, nodes, SCRAM secrets, compatible versions, networking, topics)")
	}

	brokers, err := cd.getBootstrapBrokers(ctx, clusterArn)
	if err != nil {
		return nil, nil, err
	}
	awsClientInfo.BootstrapBrokers = *brokers

	connections, err := cd.scanClusterVpcConnections(ctx, clusterArn)
	if err != nil {
		return nil, nil, err
	}
	awsClientInfo.ClientVpcConnections = connections

	operations, err := cd.scanClusterOperations(ctx, clusterArn)
	if err != nil {
		return nil, nil, err
	}
	awsClientInfo.ClusterOperations = operations

	nodes, err := cd.scanClusterNodes(ctx, clusterArn)
	if err != nil {
		return nil, nil, err
	}
	awsClientInfo.Nodes = nodes

	scramSecrets, err := cd.scanClusterScramSecrets(ctx, clusterArn)
	if err != nil {
		return nil, nil, err
	}
	awsClientInfo.ScramSecrets = scramSecrets

	policy, err := cd.getClusterPolicy(ctx, clusterArn)
	if err != nil {
		return nil, nil, err
	}
	awsClientInfo.Policy = *policy

	versions, err := cd.getCompatibleKafkaVersions(ctx, clusterArn)
	if err != nil {
		return nil, nil, err
	}
	awsClientInfo.CompatibleVersions = *versions

	if isServerless {
		slog.Debug("⏭️ skipping networking scan for MSK Serverless cluster")
	} else {
		networking, err := cd.scanNetworkingInfo(ctx, cluster, nodes)
		if err != nil {
			return nil, nil, err
		}
		awsClientInfo.ClusterNetworking = networking
	}

	switch {
	case skipTopics:
		fmt.Printf("  ⏭️  Skipping topic discovery\n")
	case isServerless:
		// The AWS topic APIs are unsupported on serverless, so we skip discovery
		// entirely (the serverless notice above already covers this). Unlike the
		// default path we never call SetTopics here, so Topics stays nil and
		// serializes as "topics": null; all consumers nil-guard it.
		slog.Debug("⏭️ skipping topic discovery for MSK Serverless cluster", "clusterArn", clusterArn)
	default:
		topics, err := cd.discoverTopics(ctx, clusterArn)
		if err != nil {
			return nil, nil, err
		}
		kafkaClientInfo.SetTopics(topics)
	}

	return &awsClientInfo, &kafkaClientInfo, nil
}

func (cd *ClusterDiscoverer) describeCluster(ctx context.Context, clusterArn string) (*kafka.DescribeClusterV2Output, error) {
	fmt.Printf("  🔍 Describing cluster %s\n", clusterArn)

	cluster, err := cd.mskService.DescribeClusterV2(ctx, clusterArn)
	if err != nil {
		return nil, fmt.Errorf("failed to describe cluster: %v", err)
	}

	return cluster, nil
}

func (cd *ClusterDiscoverer) getBootstrapBrokers(ctx context.Context, clusterArn string) (*kafka.GetBootstrapBrokersOutput, error) {
	slog.Debug("scanning for bootstrap brokers", "clusterArn", clusterArn)

	brokers, err := cd.mskService.GetBootstrapBrokers(ctx, clusterArn)
	if err != nil {
		return nil, fmt.Errorf("failed to scan brokers: %v", err)
	}
	return brokers, nil
}

func (cd *ClusterDiscoverer) scanClusterVpcConnections(ctx context.Context, clusterArn string) ([]kafkatypes.ClientVpcConnection, error) {
	slog.Debug("scanning for client vpc connections", "clusterArn", clusterArn)

	connections, err := cd.mskService.ListClientVpcConnections(ctx, clusterArn, int32(100))
	if err != nil {
		// Check if it's an MSK Serverless VPC connectivity error - this should be handled gracefully
		if strings.Contains(err.Error(), "This Region doesn't currently support VPC connectivity with Amazon MSK Serverless clusters") {
			slog.Warn("⚠️ VPC connectivity not supported for MSK Serverless clusters in this region, skipping VPC connections scan")
			return []kafkatypes.ClientVpcConnection{}, nil
		}
		return nil, fmt.Errorf("failed listing client vpc connections: %v", err)
	}
	return connections, nil
}

func (cd *ClusterDiscoverer) scanClusterOperations(ctx context.Context, clusterArn string) ([]kafkatypes.ClusterOperationV2Summary, error) {
	slog.Debug("scanning for cluster operations", "clusterArn", clusterArn)

	operations, err := cd.mskService.ListClusterOperationsV2(ctx, clusterArn, int32(100))
	if err != nil {
		return nil, fmt.Errorf("failed listing operations: %v", err)
	}
	return operations, nil
}

func (cd *ClusterDiscoverer) scanClusterNodes(ctx context.Context, clusterArn string) ([]kafkatypes.NodeInfo, error) {
	slog.Debug("scanning for cluster nodes", "clusterArn", clusterArn)

	nodes, err := cd.mskService.ListNodes(ctx, clusterArn, int32(100))
	if err != nil {
		// Check if it's an MSK Serverless error - this should be handled gracefully
		if strings.Contains(err.Error(), "This operation cannot be performed on serverless clusters.") {
			slog.Warn("⚠️ Node listing not supported for MSK Serverless clusters, skipping Nodes scan")
			return []kafkatypes.NodeInfo{}, nil
		}
		return nil, fmt.Errorf("failed listing nodes: %v", err)
	}

	return nodes, nil
}

func (cd *ClusterDiscoverer) scanClusterScramSecrets(ctx context.Context, clusterArn string) ([]string, error) {
	slog.Debug("scanning for cluster scram secrets", "clusterArn", clusterArn)

	secrets, err := cd.mskService.ListScramSecrets(ctx, clusterArn, int32(100))
	if err != nil {
		// Check if it's an MSK Serverless error - this should be handled gracefully
		if strings.Contains(err.Error(), "This operation cannot be performed on serverless clusters.") {
			slog.Warn("⚠️ Scram secret listing not supported for MSK Serverless clusters, skipping scram secrets scan")
			return []string{}, nil
		}
		return nil, fmt.Errorf("failed listing secrets: %v", err)
	}

	return secrets, nil
}

func (cd *ClusterDiscoverer) getClusterPolicy(ctx context.Context, clusterArn string) (*kafka.GetClusterPolicyOutput, error) {
	slog.Debug("scanning for cluster policy", "clusterArn", clusterArn)

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
	slog.Debug("scanning for compatible kafka versions", "clusterArn", clusterArn)

	versions, err := cs.mskService.GetCompatibleKafkaVersions(ctx, clusterArn)
	if err != nil {
		// Check if it's an MSK Serverless error - this should be handled gracefully
		if strings.Contains(err.Error(), "This operation cannot be performed on serverless clusters.") {
			slog.Warn("⚠️ Compatible versions not supported for MSK Serverless clusters, skipping compatible versions scan")
			return &kafka.GetCompatibleKafkaVersionsOutput{
				CompatibleKafkaVersions: []kafkatypes.CompatibleKafkaVersion{},
			}, nil
		}
		return nil, fmt.Errorf("failed to get compatible versions: %v", err)
	}
	return versions, nil
}

func (cd *ClusterDiscoverer) scanNetworkingInfo(ctx context.Context, cluster *kafka.DescribeClusterV2Output, nodes []kafkatypes.NodeInfo) (types.ClusterNetworking, error) {
	if cluster.ClusterInfo == nil || cluster.ClusterInfo.Provisioned == nil || cluster.ClusterInfo.Provisioned.BrokerNodeGroupInfo == nil {
		return types.ClusterNetworking{}, fmt.Errorf("cluster has no broker node group info, cannot determine networking")
	}
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
	if len(subnetIds) == 0 {
		return "", fmt.Errorf("no subnets provided, cannot determine VPC ID")
	}
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

func (cd *ClusterDiscoverer) discoverMetrics(ctx context.Context, clusterArn string, metricsGranularity string) (*types.ClusterMetrics, error) {
	// TODO: this issues a second DescribeClusterV2 call for the same cluster, and also
	// drops the caller's ctx by using context.Background(). Consider refactoring to
	// accept the already-fetched cluster from discoverAWSClientInformation to eliminate
	// the redundant API call and restore correct context propagation.
	cluster, err := cd.mskService.DescribeClusterV2(context.Background(), clusterArn)
	if err != nil {
		return nil, fmt.Errorf("failed to get clusters: %v", err)
	}
	if cluster.ClusterInfo == nil {
		return nil, fmt.Errorf("describeClusterV2 returned nil ClusterInfo for %s", clusterArn)
	}

	followerFetching, err := cd.mskService.IsFetchFromFollowerEnabled(context.Background(), *cluster.ClusterInfo)
	if err != nil {
		return nil, fmt.Errorf("failed to check if follower fetching is enabled: %v", err)
	}

	// this time window can be extracted as a parameter in future
	now := time.Now().UTC()
	previousMidnight := time.Date(now.Year(), now.Month(), now.Day()-1, 0, 0, 0, 0, time.UTC)
	endTime := previousMidnight.Add(24 * time.Hour)

	timeWindow, err := metrics.GetTimeWindowForGranularity(endTime, metricsGranularity)
	if err != nil {
		return nil, fmt.Errorf("failed to calculate time window: %v", err)
	}

	var clusterMetrics *types.ClusterMetrics
	if cluster.ClusterInfo.ClusterType == kafkatypes.ClusterTypeProvisioned {
		clusterMetrics, err = cd.metricService.ProcessProvisionedCluster(ctx, *cluster.ClusterInfo, followerFetching, timeWindow)
		if err != nil {
			return nil, fmt.Errorf("failed to process provisioned cluster: %v", err)
		}
	} else {
		clusterMetrics, err = cd.metricService.ProcessServerlessCluster(ctx, *cluster.ClusterInfo, timeWindow)
		if err != nil {
			return nil, fmt.Errorf("failed to process serverless cluster: %v", err)
		}
	}

	return clusterMetrics, nil
}

func (cd *ClusterDiscoverer) discoverTopics(ctx context.Context, clusterArn string) ([]types.TopicDetails, error) {
	fmt.Printf("  🔍 Scanning for topics\n")

	topics, err := cd.mskService.GetTopicsWithConfigs(ctx, clusterArn)
	if err != nil {
		// Non-fatal: continue without topic details. Serverless is handled by the
		// caller, so reaching here means a genuine provisioned-cluster failure.
		slog.Warn("⚠️ failed to list topics; continuing without topic details", "error", err)
		slog.Debug("failed to list topics", "clusterArn", clusterArn, "error", err)
	}

	var topicDetails []types.TopicDetails
	for _, topic := range topics {

		topicDetails = append(topicDetails, types.TopicDetails{
			Name:              topic.Name,
			Partitions:        topic.Partitions,
			ReplicationFactor: topic.ReplicationFactor,
			Configurations:    topic.Configurations,
		})
	}

	return topicDetails, nil
}
