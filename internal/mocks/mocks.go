package mocks

import (
	"context"

	"github.com/IBM/sarama"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/kafka"
	kafkatypes "github.com/aws/aws-sdk-go-v2/service/kafka/types"
	"github.com/confluentinc/kcp/internal/client"
	"github.com/confluentinc/kcp/internal/types"
)

// MockKafkaAdmin is a mock implementation of the KafkaAdmin interface
type MockKafkaAdmin struct {
	ListTopicsWithConfigsFunc   func() (map[string]sarama.TopicDetail, error)
	GetClusterKafkaMetadataFunc func() (*client.ClusterKafkaMetadata, error)
	DescribeConfigFunc          func() ([]sarama.ConfigEntry, error)
	ListAclsFunc                func() ([]sarama.ResourceAcls, error)
	CloseFunc                   func() error
}

func (m *MockKafkaAdmin) ListTopicsWithConfigs() (map[string]sarama.TopicDetail, error) {
	return m.ListTopicsWithConfigsFunc()
}

func (m *MockKafkaAdmin) GetClusterKafkaMetadata() (*client.ClusterKafkaMetadata, error) {
	return m.GetClusterKafkaMetadataFunc()
}

func (m *MockKafkaAdmin) DescribeConfig() ([]sarama.ConfigEntry, error) {
	return m.DescribeConfigFunc()
}

func (m *MockKafkaAdmin) ListAcls() ([]sarama.ResourceAcls, error) {
	return m.ListAclsFunc()
}

func (m *MockKafkaAdmin) Close() error {
	return m.CloseFunc()
}

// MockMSKService is a mock implementation of the MSKService interface
type MockMSKService struct {
	GetBootstrapBrokersFunc        func(ctx context.Context, clusterArn string) (*kafka.GetBootstrapBrokersOutput, error)
	ParseBrokerAddressesFunc       func(brokers kafka.GetBootstrapBrokersOutput, authType types.AuthType) ([]string, error)
	GetCompatibleKafkaVersionsFunc func(ctx context.Context, clusterArn string) (*kafka.GetCompatibleKafkaVersionsOutput, error)
	GetClusterPolicyFunc           func(ctx context.Context, clusterArn string) (*kafka.GetClusterPolicyOutput, error)
	DescribeClusterV2Func          func(ctx context.Context, clusterArn string) (*kafka.DescribeClusterV2Output, error)
	ListClientVpcConnectionsFunc   func(ctx context.Context, clusterArn string) ([]kafkatypes.ClientVpcConnection, error)
	ListClusterOperationsV2Func    func(ctx context.Context, clusterArn string) ([]kafkatypes.ClusterOperationV2Summary, error)
	ListNodesFunc                  func(ctx context.Context, clusterArn string) ([]kafkatypes.NodeInfo, error)
	ListScramSecretsFunc           func(ctx context.Context, clusterArn string) ([]string, error)
}

func (m *MockMSKService) GetBootstrapBrokers(ctx context.Context, clusterArn string) (*kafka.GetBootstrapBrokersOutput, error) {
	return m.GetBootstrapBrokersFunc(ctx, clusterArn)
}

func (m *MockMSKService) ParseBrokerAddresses(brokers kafka.GetBootstrapBrokersOutput, authType types.AuthType) ([]string, error) {
	return m.ParseBrokerAddressesFunc(brokers, authType)
}

func (m *MockMSKService) GetCompatibleKafkaVersions(ctx context.Context, clusterArn string) (*kafka.GetCompatibleKafkaVersionsOutput, error) {
	return m.GetCompatibleKafkaVersionsFunc(ctx, clusterArn)
}

func (m *MockMSKService) GetClusterPolicy(ctx context.Context, clusterArn string) (*kafka.GetClusterPolicyOutput, error) {
	return m.GetClusterPolicyFunc(ctx, clusterArn)
}

func (m *MockMSKService) DescribeClusterV2(ctx context.Context, clusterArn string) (*kafka.DescribeClusterV2Output, error) {
	return m.DescribeClusterV2Func(ctx, clusterArn)
}

func (m *MockMSKService) ListClientVpcConnections(ctx context.Context, clusterArn string) ([]kafkatypes.ClientVpcConnection, error) {
	return m.ListClientVpcConnectionsFunc(ctx, clusterArn)
}

func (m *MockMSKService) ListClusterOperationsV2(ctx context.Context, clusterArn string) ([]kafkatypes.ClusterOperationV2Summary, error) {
	return m.ListClusterOperationsV2Func(ctx, clusterArn)
}

func (m *MockMSKService) ListNodes(ctx context.Context, clusterArn string) ([]kafkatypes.NodeInfo, error) {
	return m.ListNodesFunc(ctx, clusterArn)
}

func (m *MockMSKService) ListScramSecrets(ctx context.Context, clusterArn string) ([]string, error) {
	return m.ListScramSecretsFunc(ctx, clusterArn)
}

// MockEC2Service is a mock implementation of the EC2Service interface
type MockEC2Service struct {
	DescribeSubnetsFunc func(ctx context.Context, subnetIds []string) (*ec2.DescribeSubnetsOutput, error)
}

func (m *MockEC2Service) DescribeSubnets(ctx context.Context, subnetIds []string) (*ec2.DescribeSubnetsOutput, error) {
	return m.DescribeSubnetsFunc(ctx, subnetIds)
}
