package msk_connect

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/service/kafkaconnect"
)

type MSKConnectService struct {
	client *kafkaconnect.Client
}

func NewMSKConnectService(client *kafkaconnect.Client) *MSKConnectService {
	return &MSKConnectService{client: client}
}

func (ms *MSKConnectService) ListConnectors(ctx context.Context, clusterArn *string) (*kafkaconnect.ListConnectorsOutput, error) {
	input := &kafkaconnect.ListConnectorsInput{}

	return ms.client.ListConnectors(ctx, input)
}

func (ms *MSKConnectService) DescribeConnector(ctx context.Context, connectorArn *string) (*kafkaconnect.DescribeConnectorOutput, error) {
	input := &kafkaconnect.DescribeConnectorInput{
		ConnectorArn: connectorArn,
	}

	return ms.client.DescribeConnector(ctx, input)
}
