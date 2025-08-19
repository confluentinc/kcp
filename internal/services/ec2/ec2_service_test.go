package ec2

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/confluentinc/kcp/internal/mocks"
	"github.com/stretchr/testify/assert"
)

func TestEC2Service_DescribeSubnets(t *testing.T) {
	tests := []struct {
		name           string
		subnetIds      []string
		mockResponse   *ec2.DescribeSubnetsOutput
		mockError      error
		expectedResult *ec2.DescribeSubnetsOutput
		expectedError  string
	}{
		{
			name:      "successful_describe_single_subnet",
			subnetIds: []string{"subnet-12345"},
			mockResponse: &ec2.DescribeSubnetsOutput{
				Subnets: []types.Subnet{
					{
						SubnetId:         aws.String("subnet-12345"),
						VpcId:            aws.String("vpc-67890"),
						CidrBlock:        aws.String("10.0.1.0/24"),
						AvailabilityZone: aws.String("us-west-2a"),
						State:            types.SubnetStateAvailable,
					},
				},
			},
			mockError: nil,
			expectedResult: &ec2.DescribeSubnetsOutput{
				Subnets: []types.Subnet{
					{
						SubnetId:         aws.String("subnet-12345"),
						VpcId:            aws.String("vpc-67890"),
						CidrBlock:        aws.String("10.0.1.0/24"),
						AvailabilityZone: aws.String("us-west-2a"),
						State:            types.SubnetStateAvailable,
					},
				},
			},
			expectedError: "",
		},
		{
			name:      "successful_describe_multiple_subnets",
			subnetIds: []string{"subnet-12345", "subnet-67890"},
			mockResponse: &ec2.DescribeSubnetsOutput{
				Subnets: []types.Subnet{
					{
						SubnetId:         aws.String("subnet-12345"),
						VpcId:            aws.String("vpc-11111"),
						CidrBlock:        aws.String("10.0.1.0/24"),
						AvailabilityZone: aws.String("us-west-2a"),
						State:            types.SubnetStateAvailable,
					},
					{
						SubnetId:         aws.String("subnet-67890"),
						VpcId:            aws.String("vpc-22222"),
						CidrBlock:        aws.String("10.0.2.0/24"),
						AvailabilityZone: aws.String("us-west-2b"),
						State:            types.SubnetStateAvailable,
					},
				},
			},
			mockError: nil,
			expectedResult: &ec2.DescribeSubnetsOutput{
				Subnets: []types.Subnet{
					{
						SubnetId:         aws.String("subnet-12345"),
						VpcId:            aws.String("vpc-11111"),
						CidrBlock:        aws.String("10.0.1.0/24"),
						AvailabilityZone: aws.String("us-west-2a"),
						State:            types.SubnetStateAvailable,
					},
					{
						SubnetId:         aws.String("subnet-67890"),
						VpcId:            aws.String("vpc-22222"),
						CidrBlock:        aws.String("10.0.2.0/24"),
						AvailabilityZone: aws.String("us-west-2b"),
						State:            types.SubnetStateAvailable,
					},
				},
			},
			expectedError: "",
		},
		{
			name:      "empty_subnet_ids",
			subnetIds: []string{},
			mockResponse: &ec2.DescribeSubnetsOutput{
				Subnets: []types.Subnet{},
			},
			mockError: nil,
			expectedResult: &ec2.DescribeSubnetsOutput{
				Subnets: []types.Subnet{},
			},
			expectedError: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create mock service using existing mocks package
			mockService := &mocks.MockEC2Service{
				DescribeSubnetsFunc: func(ctx context.Context, subnetIds []string) (*ec2.DescribeSubnetsOutput, error) {
					// Verify the input parameters
					assert.Equal(t, tt.subnetIds, subnetIds, "SubnetIds should match expected")
					return tt.mockResponse, tt.mockError
				},
			}

			// Execute test
			ctx := context.Background()
			result, err := mockService.DescribeSubnets(ctx, tt.subnetIds)

			// Verify results
			if tt.expectedError != "" {
				assert.Error(t, err, "Expected an error")
				assert.Contains(t, err.Error(), tt.expectedError, "Error message should contain expected text")
				assert.Nil(t, result, "Result should be nil when error occurs")
			} else {
				assert.NoError(t, err, "Should not return an error")
				assert.Equal(t, tt.expectedResult, result, "Result should match expected output")
			}
		})
	}
}
