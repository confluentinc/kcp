package discover

import (
	"context"
	"fmt"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/kafka"
	kafkatypes "github.com/aws/aws-sdk-go-v2/service/kafka/types"
	"github.com/aws/aws-sdk-go-v2/service/kafkaconnect"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/stretchr/testify/assert"
)

// Mock implementations

type mockMSKService struct {
	topicsToReturn []types.TopicDetails
	topicsErr      error
}

func (m *mockMSKService) DescribeClusterV2(_ context.Context, _ string) (*kafka.DescribeClusterV2Output, error) {
	return &kafka.DescribeClusterV2Output{
		ClusterInfo: &kafkatypes.Cluster{
			ClusterName: aws.String("test-cluster"),
			ClusterArn:  aws.String("arn:aws:kafka:us-east-1:123456789012:cluster/test-cluster/abc-123"),
			ClusterType: kafkatypes.ClusterTypeProvisioned,
			Provisioned: &kafkatypes.Provisioned{
				BrokerNodeGroupInfo: &kafkatypes.BrokerNodeGroupInfo{
					ClientSubnets:  []string{"subnet-1"},
					SecurityGroups: []string{"sg-1"},
				},
				ClientAuthentication: &kafkatypes.ClientAuthentication{
					Sasl: &kafkatypes.Sasl{
						Iam: &kafkatypes.Iam{Enabled: aws.Bool(true)},
					},
				},
			},
		},
	}, nil
}

func (m *mockMSKService) GetBootstrapBrokers(_ context.Context, _ string) (*kafka.GetBootstrapBrokersOutput, error) {
	return &kafka.GetBootstrapBrokersOutput{
		BootstrapBrokerStringSaslIam: aws.String("b-1.test.kafka.us-east-1.amazonaws.com:9098"),
	}, nil
}

func (m *mockMSKService) ListClientVpcConnections(_ context.Context, _ string, _ int32) ([]kafkatypes.ClientVpcConnection, error) {
	return []kafkatypes.ClientVpcConnection{}, nil
}

func (m *mockMSKService) ListClusterOperationsV2(_ context.Context, _ string, _ int32) ([]kafkatypes.ClusterOperationV2Summary, error) {
	return []kafkatypes.ClusterOperationV2Summary{}, nil
}

func (m *mockMSKService) ListNodes(_ context.Context, _ string, _ int32) ([]kafkatypes.NodeInfo, error) {
	return []kafkatypes.NodeInfo{}, nil
}

func (m *mockMSKService) ListScramSecrets(_ context.Context, _ string, _ int32) ([]string, error) {
	return []string{}, nil
}

func (m *mockMSKService) GetClusterPolicy(_ context.Context, _ string) (*kafka.GetClusterPolicyOutput, error) {
	return &kafka.GetClusterPolicyOutput{}, nil
}

func (m *mockMSKService) GetCompatibleKafkaVersions(_ context.Context, _ string) (*kafka.GetCompatibleKafkaVersionsOutput, error) {
	return &kafka.GetCompatibleKafkaVersionsOutput{
		CompatibleKafkaVersions: []kafkatypes.CompatibleKafkaVersion{},
	}, nil
}

func (m *mockMSKService) IsFetchFromFollowerEnabled(_ context.Context, _ kafkatypes.Cluster) (bool, error) {
	return false, nil
}

func (m *mockMSKService) GetTopicsWithConfigs(_ context.Context, _ string) ([]types.TopicDetails, error) {
	return m.topicsToReturn, m.topicsErr
}

type mockEC2Service struct{}

func (m *mockEC2Service) DescribeSubnets(_ context.Context, subnetIds []string) (*ec2.DescribeSubnetsOutput, error) {
	var subnets []ec2types.Subnet
	for _, id := range subnetIds {
		subnets = append(subnets, ec2types.Subnet{
			SubnetId:         aws.String(id),
			VpcId:            aws.String("vpc-123"),
			AvailabilityZone: aws.String("us-east-1a"),
			CidrBlock:        aws.String("10.0.0.0/24"),
		})
	}
	return &ec2.DescribeSubnetsOutput{Subnets: subnets}, nil
}

type mockMSKConnectService struct {
	listErr error
}

func (m *mockMSKConnectService) ListConnectors(_ context.Context, _ *kafkaconnect.ListConnectorsInput, _ ...func(*kafkaconnect.Options)) (*kafkaconnect.ListConnectorsOutput, error) {
	if m.listErr != nil {
		return nil, m.listErr
	}
	return &kafkaconnect.ListConnectorsOutput{}, nil
}

func (m *mockMSKConnectService) DescribeConnector(_ context.Context, _ *kafkaconnect.DescribeConnectorInput, _ ...func(*kafkaconnect.Options)) (*kafkaconnect.DescribeConnectorOutput, error) {
	return &kafkaconnect.DescribeConnectorOutput{}, nil
}

func TestClusterDiscoverer_ConnectorFailureDoesNotBlockTopicDiscovery(t *testing.T) {
	expectedTopics := []types.TopicDetails{
		{Name: "test-topic", Partitions: 3, ReplicationFactor: 3},
	}

	mskService := &mockMSKService{
		topicsToReturn: expectedTopics,
	}
	mskConnectService := &mockMSKConnectService{
		listErr: fmt.Errorf("AccessDeniedException: User is not authorized to perform: kafkaconnect:ListConnectors"),
	}

	cd := NewClusterDiscoverer(mskService, &mockEC2Service{}, nil, mskConnectService)

	clusterArn := "arn:aws:kafka:us-east-1:123456789012:cluster/test-cluster/abc-123"
	result, err := cd.Discover(context.Background(), clusterArn, "us-east-1", false, true, false)

	assert.NoError(t, err, "connector failure should not cause Discover to fail")
	assert.NotNil(t, result)
	assert.Empty(t, result.AWSClientInformation.Connectors, "connectors should be empty on failure")
	assert.NotNil(t, result.KafkaAdminClientInformation.Topics, "topics should still be discovered")
	assert.Len(t, result.KafkaAdminClientInformation.Topics.Details, 1, "should have discovered 1 topic")
	assert.Equal(t, "test-topic", result.KafkaAdminClientInformation.Topics.Details[0].Name)
}
