package discover

import (
	"context"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/kafka"
	kafkatypes "github.com/aws/aws-sdk-go-v2/service/kafka/types"
	"github.com/aws/aws-sdk-go-v2/service/kafkaconnect"
	costexplorertypes "github.com/aws/aws-sdk-go-v2/service/costexplorer/types"
	"github.com/confluentinc/kcp/internal/types"
)

// ── stubMSKService ─────────────────────────────────────────────────────────────
// Implements ClusterDiscovererMSKService (10 methods).
// Unset function fields return safe empty defaults.

type stubMSKService struct {
	describeClusterV2Fn          func(ctx context.Context, clusterArn string) (*kafka.DescribeClusterV2Output, error)
	getBootstrapBrokersFn        func(ctx context.Context, clusterArn string) (*kafka.GetBootstrapBrokersOutput, error)
	listClientVpcConnectionsFn   func(ctx context.Context, clusterArn string, maxResults int32) ([]kafkatypes.ClientVpcConnection, error)
	listClusterOperationsV2Fn    func(ctx context.Context, clusterArn string, maxResults int32) ([]kafkatypes.ClusterOperationV2Summary, error)
	listNodesFn                  func(ctx context.Context, clusterArn string, maxResults int32) ([]kafkatypes.NodeInfo, error)
	listScramSecretsFn           func(ctx context.Context, clusterArn string, maxResults int32) ([]string, error)
	getClusterPolicyFn           func(ctx context.Context, clusterArn string) (*kafka.GetClusterPolicyOutput, error)
	getCompatibleKafkaVersionsFn func(ctx context.Context, clusterArn string) (*kafka.GetCompatibleKafkaVersionsOutput, error)
	isFetchFromFollowerEnabledFn func(ctx context.Context, cluster kafkatypes.Cluster) (bool, error)
	getTopicsWithConfigsFn       func(ctx context.Context, clusterArn string) ([]types.TopicDetails, error)
}

func (s *stubMSKService) DescribeClusterV2(ctx context.Context, clusterArn string) (*kafka.DescribeClusterV2Output, error) {
	if s.describeClusterV2Fn != nil {
		return s.describeClusterV2Fn(ctx, clusterArn)
	}
	return &kafka.DescribeClusterV2Output{}, nil
}
func (s *stubMSKService) GetBootstrapBrokers(ctx context.Context, clusterArn string) (*kafka.GetBootstrapBrokersOutput, error) {
	if s.getBootstrapBrokersFn != nil {
		return s.getBootstrapBrokersFn(ctx, clusterArn)
	}
	return &kafka.GetBootstrapBrokersOutput{}, nil
}
func (s *stubMSKService) ListClientVpcConnections(ctx context.Context, clusterArn string, maxResults int32) ([]kafkatypes.ClientVpcConnection, error) {
	if s.listClientVpcConnectionsFn != nil {
		return s.listClientVpcConnectionsFn(ctx, clusterArn, maxResults)
	}
	return []kafkatypes.ClientVpcConnection{}, nil
}
func (s *stubMSKService) ListClusterOperationsV2(ctx context.Context, clusterArn string, maxResults int32) ([]kafkatypes.ClusterOperationV2Summary, error) {
	if s.listClusterOperationsV2Fn != nil {
		return s.listClusterOperationsV2Fn(ctx, clusterArn, maxResults)
	}
	return []kafkatypes.ClusterOperationV2Summary{}, nil
}
func (s *stubMSKService) ListNodes(ctx context.Context, clusterArn string, maxResults int32) ([]kafkatypes.NodeInfo, error) {
	if s.listNodesFn != nil {
		return s.listNodesFn(ctx, clusterArn, maxResults)
	}
	return []kafkatypes.NodeInfo{}, nil
}
func (s *stubMSKService) ListScramSecrets(ctx context.Context, clusterArn string, maxResults int32) ([]string, error) {
	if s.listScramSecretsFn != nil {
		return s.listScramSecretsFn(ctx, clusterArn, maxResults)
	}
	return []string{}, nil
}
func (s *stubMSKService) GetClusterPolicy(ctx context.Context, clusterArn string) (*kafka.GetClusterPolicyOutput, error) {
	if s.getClusterPolicyFn != nil {
		return s.getClusterPolicyFn(ctx, clusterArn)
	}
	return &kafka.GetClusterPolicyOutput{}, nil
}
func (s *stubMSKService) GetCompatibleKafkaVersions(ctx context.Context, clusterArn string) (*kafka.GetCompatibleKafkaVersionsOutput, error) {
	if s.getCompatibleKafkaVersionsFn != nil {
		return s.getCompatibleKafkaVersionsFn(ctx, clusterArn)
	}
	return &kafka.GetCompatibleKafkaVersionsOutput{}, nil
}
func (s *stubMSKService) IsFetchFromFollowerEnabled(ctx context.Context, cluster kafkatypes.Cluster) (bool, error) {
	if s.isFetchFromFollowerEnabledFn != nil {
		return s.isFetchFromFollowerEnabledFn(ctx, cluster)
	}
	return false, nil
}
func (s *stubMSKService) GetTopicsWithConfigs(ctx context.Context, clusterArn string) ([]types.TopicDetails, error) {
	if s.getTopicsWithConfigsFn != nil {
		return s.getTopicsWithConfigsFn(ctx, clusterArn)
	}
	return []types.TopicDetails{}, nil
}

// ── stubMetricService ──────────────────────────────────────────────────────────
// Implements ClusterDiscovererMetricService (2 methods).

type stubMetricService struct {
	processProvisionedClusterFn func(ctx context.Context, cluster kafkatypes.Cluster, followerFetching bool, timeWindow types.CloudWatchTimeWindow) (*types.ClusterMetrics, error)
	processServerlessClusterFn  func(ctx context.Context, cluster kafkatypes.Cluster, timeWindow types.CloudWatchTimeWindow) (*types.ClusterMetrics, error)
}

func (s *stubMetricService) ProcessProvisionedCluster(ctx context.Context, cluster kafkatypes.Cluster, followerFetching bool, timeWindow types.CloudWatchTimeWindow) (*types.ClusterMetrics, error) {
	if s.processProvisionedClusterFn != nil {
		return s.processProvisionedClusterFn(ctx, cluster, followerFetching, timeWindow)
	}
	return &types.ClusterMetrics{}, nil
}
func (s *stubMetricService) ProcessServerlessCluster(ctx context.Context, cluster kafkatypes.Cluster, timeWindow types.CloudWatchTimeWindow) (*types.ClusterMetrics, error) {
	if s.processServerlessClusterFn != nil {
		return s.processServerlessClusterFn(ctx, cluster, timeWindow)
	}
	return &types.ClusterMetrics{}, nil
}

// ── stubEC2Service ─────────────────────────────────────────────────────────────
// Implements ClusterDiscovererEC2Service (1 method).

type stubEC2Service struct {
	describeSubnetsFn func(ctx context.Context, subnetIds []string) (*ec2.DescribeSubnetsOutput, error)
}

func (s *stubEC2Service) DescribeSubnets(ctx context.Context, subnetIds []string) (*ec2.DescribeSubnetsOutput, error) {
	if s.describeSubnetsFn != nil {
		return s.describeSubnetsFn(ctx, subnetIds)
	}
	return &ec2.DescribeSubnetsOutput{
		Subnets: []ec2types.Subnet{
			{
				SubnetId:         aws.String("subnet-default"),
				VpcId:            aws.String("vpc-default"),
				AvailabilityZone: aws.String("us-east-1a"),
				CidrBlock:        aws.String("10.0.0.0/24"),
			},
		},
	}, nil
}

// ── stubMSKConnectService ──────────────────────────────────────────────────────
// Implements ClusterDiscovererMSKConnectService (2 methods).

type stubMSKConnectService struct {
	listConnectorsFn    func(ctx context.Context, params *kafkaconnect.ListConnectorsInput, optFns ...func(*kafkaconnect.Options)) (*kafkaconnect.ListConnectorsOutput, error)
	describeConnectorFn func(ctx context.Context, params *kafkaconnect.DescribeConnectorInput, optFns ...func(*kafkaconnect.Options)) (*kafkaconnect.DescribeConnectorOutput, error)
}

func (s *stubMSKConnectService) ListConnectors(ctx context.Context, params *kafkaconnect.ListConnectorsInput, optFns ...func(*kafkaconnect.Options)) (*kafkaconnect.ListConnectorsOutput, error) {
	if s.listConnectorsFn != nil {
		return s.listConnectorsFn(ctx, params, optFns...)
	}
	return &kafkaconnect.ListConnectorsOutput{}, nil
}
func (s *stubMSKConnectService) DescribeConnector(ctx context.Context, params *kafkaconnect.DescribeConnectorInput, optFns ...func(*kafkaconnect.Options)) (*kafkaconnect.DescribeConnectorOutput, error) {
	if s.describeConnectorFn != nil {
		return s.describeConnectorFn(ctx, params, optFns...)
	}
	return &kafkaconnect.DescribeConnectorOutput{}, nil
}

// ── stubRegionMSKService ───────────────────────────────────────────────────────
// Implements RegionDiscovererMSKService (2 methods).

type stubRegionMSKService struct {
	listClustersFn      func(ctx context.Context, maxResults int32) ([]kafkatypes.Cluster, error)
	getConfigurationsFn func(ctx context.Context, maxResults int32) ([]kafka.DescribeConfigurationRevisionOutput, error)
}

func (s *stubRegionMSKService) ListClusters(ctx context.Context, maxResults int32) ([]kafkatypes.Cluster, error) {
	if s.listClustersFn != nil {
		return s.listClustersFn(ctx, maxResults)
	}
	return []kafkatypes.Cluster{}, nil
}
func (s *stubRegionMSKService) GetConfigurations(ctx context.Context, maxResults int32) ([]kafka.DescribeConfigurationRevisionOutput, error) {
	if s.getConfigurationsFn != nil {
		return s.getConfigurationsFn(ctx, maxResults)
	}
	return []kafka.DescribeConfigurationRevisionOutput{}, nil
}

// ── stubCostService ────────────────────────────────────────────────────────────
// Implements RegionDiscovererCostService (1 method).

type stubCostService struct {
	getCostsForTimeRangeFn func(ctx context.Context, region string, startDate time.Time, endDate time.Time, granularity costexplorertypes.Granularity, tags map[string][]string) (types.CostInformation, error)
}

func (s *stubCostService) GetCostsForTimeRange(ctx context.Context, region string, startDate time.Time, endDate time.Time, granularity costexplorertypes.Granularity, tags map[string][]string) (types.CostInformation, error) {
	if s.getCostsForTimeRangeFn != nil {
		return s.getCostsForTimeRangeFn(ctx, region, startDate, endDate, granularity, tags)
	}
	return types.CostInformation{}, nil
}

// ── Cluster builder helpers ────────────────────────────────────────────────────

const (
	testClusterArn  = "arn:aws:kafka:us-east-1:123456789012:cluster/test-cluster/abc-123"
	testClusterName = "test-cluster"
	testRegion      = "us-east-1"
)

// buildFullProvisionedCluster returns a provisioned cluster with all fields
// populated — use as the baseline for happy-path tests.
func buildFullProvisionedCluster() *kafka.DescribeClusterV2Output {
	return &kafka.DescribeClusterV2Output{
		ClusterInfo: &kafkatypes.Cluster{
			ClusterName: aws.String(testClusterName),
			ClusterArn:  aws.String(testClusterArn),
			ClusterType: kafkatypes.ClusterTypeProvisioned,
			Provisioned: &kafkatypes.Provisioned{
				NumberOfBrokerNodes: aws.Int32(3),
				EnhancedMonitoring:  kafkatypes.EnhancedMonitoringDefault,
				StorageMode:         kafkatypes.StorageModeLocal,
				CurrentBrokerSoftwareInfo: &kafkatypes.BrokerSoftwareInfo{
					KafkaVersion: aws.String("3.5.1"),
				},
				BrokerNodeGroupInfo: &kafkatypes.BrokerNodeGroupInfo{
					InstanceType:   aws.String("kafka.m5.large"),
					ClientSubnets:  []string{"subnet-12345"},
					SecurityGroups: []string{"sg-12345"},
					StorageInfo: &kafkatypes.StorageInfo{
						EbsStorageInfo: &kafkatypes.EBSStorageInfo{
							VolumeSize: aws.Int32(100),
						},
					},
				},
				ClientAuthentication: &kafkatypes.ClientAuthentication{
					Sasl: &kafkatypes.Sasl{
						Iam: &kafkatypes.Iam{Enabled: aws.Bool(true)},
					},
				},
			},
		},
	}
}

// buildFullServerlessCluster returns a serverless cluster with all fields populated.
func buildFullServerlessCluster() *kafka.DescribeClusterV2Output {
	return &kafka.DescribeClusterV2Output{
		ClusterInfo: &kafkatypes.Cluster{
			ClusterName: aws.String(testClusterName),
			ClusterArn:  aws.String(testClusterArn),
			ClusterType: kafkatypes.ClusterTypeServerless,
			Serverless:  &kafkatypes.Serverless{},
		},
	}
}
