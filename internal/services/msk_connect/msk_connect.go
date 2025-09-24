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

func (ms *MSKConnectService) ListConnectors(ctx context.Context, params *kafkaconnect.ListConnectorsInput, optFns ...func(*kafkaconnect.Options)) (*kafkaconnect.ListConnectorsOutput, error) {
	return ms.client.ListConnectors(ctx, params, optFns...)
}

func (ms *MSKConnectService) DescribeConnector(ctx context.Context, params *kafkaconnect.DescribeConnectorInput, optFns ...func(*kafkaconnect.Options)) (*kafkaconnect.DescribeConnectorOutput, error) {
	return ms.client.DescribeConnector(ctx, params, optFns...)
}
