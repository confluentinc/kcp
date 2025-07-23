package msk

import (
	"context"

	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/kafka"
	kafkatypes "github.com/aws/aws-sdk-go-v2/service/kafka/types"
)

type MSKService struct {
	client *kafka.Client
}

func NewMSKService(client *kafka.Client) *MSKService {
	return &MSKService{client: client}
}

func (ms *MSKService) GetClusters(ctx context.Context) ([]kafkatypes.Cluster, error) {
	clusters, err := ms.client.ListClustersV2(ctx, &kafka.ListClustersV2Input{})
	if err != nil {
		return nil, err
	}
	return clusters.ClusterInfoList, nil
}

func (ms *MSKService) IsFetchFromFollowerEnabled(ctx context.Context, cluster kafkatypes.Cluster) (*bool, error) {

	if cluster.Provisioned == nil ||
		cluster.Provisioned.CurrentBrokerSoftwareInfo == nil ||
		cluster.Provisioned.CurrentBrokerSoftwareInfo.ConfigurationArn == nil ||
		cluster.Provisioned.CurrentBrokerSoftwareInfo.ConfigurationRevision == nil {
		return nil, nil
	}

	configurationArn := cluster.Provisioned.CurrentBrokerSoftwareInfo.ConfigurationArn
	configurationRevision := cluster.Provisioned.CurrentBrokerSoftwareInfo.ConfigurationRevision

	describeConfigurationRevisionInput := &kafka.DescribeConfigurationRevisionInput{
		Arn:      configurationArn,
		Revision: configurationRevision,
	}

	revision, err := ms.client.DescribeConfigurationRevision(ctx, describeConfigurationRevisionInput)
	if err != nil {
		return nil, fmt.Errorf("failed to describe configuration revision: %v", err)
	}

	serverProperties := revision.ServerProperties

	// ServerProperties is already a []byte containing plain text configuration
	// First try to use it directly as plain text
	propertiesText := string(serverProperties)

	if strings.Contains(propertiesText, "replica.selector.class=org.apache.kafka.common.replica.RackAwareReplicaSelector") {
		return aws.Bool(true), nil
	}
	return aws.Bool(false), nil
}
