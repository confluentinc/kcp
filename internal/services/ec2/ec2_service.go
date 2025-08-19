package ec2

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/confluentinc/kcp/internal/client"
)

type EC2Service struct {
	client *ec2.Client
}

func NewEC2Service(region string) (*EC2Service, error) {
	client, err := client.NewEC2Client(region)
	if err != nil {
		return nil, err
	}
	return &EC2Service{client: client}, nil
}

func (e *EC2Service) DescribeSubnets(ctx context.Context, subnetIds []string) (*ec2.DescribeSubnetsOutput, error) {
	input := &ec2.DescribeSubnetsInput{
		SubnetIds: subnetIds,
	}
	return e.client.DescribeSubnets(ctx, input)
}
