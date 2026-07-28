package discover

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/kafka"
	kafkatypes "github.com/aws/aws-sdk-go-v2/service/kafka/types"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestClusterDiscoverer(msk *stubMSKService, ec2svc *stubEC2Service, metrics *stubMetricService) *ClusterDiscoverer {
	cd := NewClusterDiscoverer(msk, ec2svc, metrics, &stubMSKConnectService{})
	return &cd
}

func newTestClusterDiscovererWithConnect(msk *stubMSKService, ec2svc *stubEC2Service, metrics *stubMetricService, connect *stubMSKConnectService) *ClusterDiscoverer {
	cd := NewClusterDiscoverer(msk, ec2svc, metrics, connect)
	return &cd
}

func defaultStubs() (*stubMSKService, *stubEC2Service, *stubMetricService) {
	return &stubMSKService{}, &stubEC2Service{}, &stubMetricService{}
}

func TestClusterDiscoverer_NilClusterInfo(t *testing.T) {
	// DescribeClusterV2 returns a response with nil ClusterInfo —
	// should return an error, not panic.
	msk, ec2svc, metrics := defaultStubs()
	msk.describeClusterV2Fn = func(_ context.Context, _ string) (*kafka.DescribeClusterV2Output, error) {
		return &kafka.DescribeClusterV2Output{ClusterInfo: nil}, nil
	}

	cd := newTestClusterDiscoverer(msk, ec2svc, metrics)
	_, err := cd.Discover(context.Background(), testClusterArn, testRegion, true, true, "60s")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "nil ClusterInfo")
}

func TestClusterDiscoverer_DescribeClusterError(t *testing.T) {
	msk, ec2svc, metrics := defaultStubs()
	msk.describeClusterV2Fn = func(_ context.Context, _ string) (*kafka.DescribeClusterV2Output, error) {
		return nil, errors.New("AWS API error")
	}

	cd := newTestClusterDiscoverer(msk, ec2svc, metrics)
	_, err := cd.Discover(context.Background(), testClusterArn, testRegion, true, true, "60s")

	require.Error(t, err)
}

func TestClusterDiscoverer_NilBrokerNodeGroupInfo(t *testing.T) {
	// Provisioned cluster where BrokerNodeGroupInfo is nil —
	// networking scan should be skipped gracefully, not panic.
	msk, ec2svc, metrics := defaultStubs()
	full := buildFullProvisionedCluster()
	full.ClusterInfo.Provisioned.BrokerNodeGroupInfo = nil
	msk.describeClusterV2Fn = func(_ context.Context, _ string) (*kafka.DescribeClusterV2Output, error) {
		return full, nil
	}

	cd := newTestClusterDiscoverer(msk, ec2svc, metrics)
	_, err := cd.Discover(context.Background(), testClusterArn, testRegion, true, true, "60s")

	// Expect an error (networking cannot proceed without BrokerNodeGroupInfo),
	// but NOT a panic.
	require.Error(t, err)
	assert.Contains(t, err.Error(), "broker node group info")
}

func TestClusterDiscoverer_EmptySubnets(t *testing.T) {
	// Provisioned cluster where BrokerNodeGroupInfo has empty ClientSubnets —
	// getVpcIdFromSubnets should return an error, not panic on subnetIds[0].
	msk, ec2svc, metrics := defaultStubs()
	full := buildFullProvisionedCluster()
	full.ClusterInfo.Provisioned.BrokerNodeGroupInfo.ClientSubnets = []string{}
	msk.describeClusterV2Fn = func(_ context.Context, _ string) (*kafka.DescribeClusterV2Output, error) {
		return full, nil
	}

	cd := newTestClusterDiscoverer(msk, ec2svc, metrics)
	_, err := cd.Discover(context.Background(), testClusterArn, testRegion, true, true, "60s")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "subnet")
}

func TestClusterDiscoverer_ServerlessCluster(t *testing.T) {
	// Serverless cluster — networking scan skipped, no provisioned-only fields accessed.
	msk, ec2svc, metrics := defaultStubs()
	msk.describeClusterV2Fn = func(_ context.Context, _ string) (*kafka.DescribeClusterV2Output, error) {
		return buildFullServerlessCluster(), nil
	}

	cd := newTestClusterDiscoverer(msk, ec2svc, metrics)
	result, err := cd.Discover(context.Background(), testClusterArn, testRegion, true, true, "60s")

	require.NoError(t, err)
	assert.Equal(t, testClusterName, result.Name)
}

func TestClusterDiscoverer_SkipMetrics(t *testing.T) {
	// skipMetrics=true — metric service should never be called.
	msk, ec2svc, metrics := defaultStubs()
	msk.describeClusterV2Fn = func(_ context.Context, _ string) (*kafka.DescribeClusterV2Output, error) {
		return buildFullProvisionedCluster(), nil
	}
	ec2svc.describeSubnetsFn = func(_ context.Context, subnetIds []string) (*ec2.DescribeSubnetsOutput, error) {
		return &ec2.DescribeSubnetsOutput{
			Subnets: []ec2types.Subnet{
				{
					SubnetId:         aws.String(subnetIds[0]),
					VpcId:            aws.String("vpc-12345"),
					AvailabilityZone: aws.String("us-east-1a"),
					CidrBlock:        aws.String("10.0.0.0/24"),
				},
			},
		}, nil
	}
	metricsCalled := false
	metrics.processProvisionedClusterFn = func(_ context.Context, _ kafkatypes.Cluster, _ bool, _ types.CloudWatchTimeWindow) (*types.ClusterMetrics, error) {
		metricsCalled = true
		return &types.ClusterMetrics{}, nil
	}

	cd := newTestClusterDiscoverer(msk, ec2svc, metrics)
	_, err := cd.Discover(context.Background(), testClusterArn, testRegion, true /* skipTopics */, true /* skipMetrics */, "60s")

	require.NoError(t, err)
	assert.False(t, metricsCalled, "metric service should not be called when skipMetrics=true")
}

func TestClusterDiscoverer_NilClusterInfoInDiscoverMetrics(t *testing.T) {
	// The first DescribeClusterV2 call (in discoverAWSClientInformation) returns a valid cluster.
	// The second call (in discoverMetrics) returns nil ClusterInfo.
	// Should return an error, not panic.
	msk, ec2svc, metrics := defaultStubs()

	callCount := 0
	msk.describeClusterV2Fn = func(_ context.Context, _ string) (*kafka.DescribeClusterV2Output, error) {
		callCount++
		if callCount == 1 {
			return buildFullProvisionedCluster(), nil
		}
		// Second call (discoverMetrics) returns nil ClusterInfo
		return &kafka.DescribeClusterV2Output{ClusterInfo: nil}, nil
	}
	// EC2 needs to succeed for the first call to complete
	ec2svc.describeSubnetsFn = func(_ context.Context, subnetIds []string) (*ec2.DescribeSubnetsOutput, error) {
		return &ec2.DescribeSubnetsOutput{
			Subnets: []ec2types.Subnet{{
				SubnetId:         aws.String(subnetIds[0]),
				VpcId:            aws.String("vpc-12345"),
				AvailabilityZone: aws.String("us-east-1a"),
				CidrBlock:        aws.String("10.0.0.0/24"),
			}},
		}, nil
	}

	cd := newTestClusterDiscoverer(msk, ec2svc, metrics)
	_, err := cd.Discover(context.Background(), testClusterArn, testRegion, true, false /* skipMetrics=false */, "60s")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "nil ClusterInfo")
}

// TestDiscoverTopics_LogsClusterArnOnFailure proves the non-fatal topic-listing
// failure keeps cluster attribution in kcp.log: the surviving WARN stays clean,
// but a paired DEBUG line records which clusterArn failed, so a support engineer
// reading a multi-cluster run can still tell which cluster the failure belongs to.
func TestDiscoverTopics_LogsClusterArnOnFailure(t *testing.T) {
	const testArn = "arn:aws:kafka:us-east-1:123456789012:cluster/prod/xyz-9"

	var logBuf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer slog.SetDefault(prev)

	msk, ec2svc, metrics := defaultStubs()
	msk.getTopicsWithConfigsFn = func(ctx context.Context, clusterArn string) ([]types.TopicDetails, error) {
		return nil, errors.New("broker unreachable")
	}
	cd := newTestClusterDiscoverer(msk, ec2svc, metrics)

	// discoverTopics is non-fatal: it logs and returns a nil error.
	_, err := cd.discoverTopics(context.Background(), testArn)
	require.NoError(t, err)

	out := logBuf.String()
	found := false
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "level=DEBUG") && strings.Contains(line, "clusterArn="+testArn) {
			found = true
			break
		}
	}
	assert.True(t, found,
		"discoverTopics must record clusterArn on a DEBUG line when topic listing fails; got:\n%s", out)
}
