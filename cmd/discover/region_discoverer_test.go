package discover

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	costexplorertypes "github.com/aws/aws-sdk-go-v2/service/costexplorer/types"
	"github.com/aws/aws-sdk-go-v2/service/kafka"
	kafkatypes "github.com/aws/aws-sdk-go-v2/service/kafka/types"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRegionDiscoverer_HappyPath(t *testing.T) {
	msk := &stubRegionMSKService{
		listClustersFn: func(_ context.Context, _ int32) ([]kafkatypes.Cluster, error) {
			return []kafkatypes.Cluster{
				{ClusterArn: aws.String(testClusterArn)},
			}, nil
		},
	}
	cost := &stubCostService{}

	rd := NewRegionDiscoverer(msk, cost)
	result, err := rd.Discover(context.Background(), testRegion, true /* skipCosts */)

	require.NoError(t, err)
	assert.Equal(t, testRegion, result.Name)
	require.Len(t, result.ClusterArns, 1)
	assert.Equal(t, testClusterArn, result.ClusterArns[0])
}

func TestRegionDiscoverer_EmptyClusterList(t *testing.T) {
	msk := &stubRegionMSKService{
		listClustersFn: func(_ context.Context, _ int32) ([]kafkatypes.Cluster, error) {
			return []kafkatypes.Cluster{}, nil
		},
	}
	cost := &stubCostService{}

	rd := NewRegionDiscoverer(msk, cost)
	result, err := rd.Discover(context.Background(), testRegion, true)

	require.NoError(t, err)
	assert.Empty(t, result.ClusterArns)
}

func TestRegionDiscoverer_SkipCosts(t *testing.T) {
	msk := &stubRegionMSKService{}
	costCalled := false
	cost := &stubCostService{
		getCostsForTimeRangeFn: func(_ context.Context, _ string, _ time.Time, _ time.Time, _ costexplorertypes.Granularity, _ map[string][]string) (types.CostInformation, error) {
			costCalled = true
			return types.CostInformation{}, nil
		},
	}

	rd := NewRegionDiscoverer(msk, cost)
	_, err := rd.Discover(context.Background(), testRegion, true /* skipCosts=true */)

	require.NoError(t, err)
	assert.False(t, costCalled, "cost service should not be called when skipCosts=true")
}

func TestRegionDiscoverer_CostAPIError(t *testing.T) {
	msk := &stubRegionMSKService{}
	cost := &stubCostService{
		getCostsForTimeRangeFn: func(_ context.Context, _ string, _ time.Time, _ time.Time, _ costexplorertypes.Granularity, _ map[string][]string) (types.CostInformation, error) {
			return types.CostInformation{}, errors.New("cost API unavailable")
		},
	}

	rd := NewRegionDiscoverer(msk, cost)
	_, err := rd.Discover(context.Background(), testRegion, false /* skipCosts=false, will call cost API */)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "cost API unavailable")
}

func TestRegionDiscoverer_ConfigurationsIncluded(t *testing.T) {
	msk := &stubRegionMSKService{
		getConfigurationsFn: func(_ context.Context, _ int32) ([]kafka.DescribeConfigurationRevisionOutput, error) {
			return []kafka.DescribeConfigurationRevisionOutput{
				{Arn: aws.String("arn:aws:kafka:us-east-1:123:configuration/test/1")},
			}, nil
		},
	}
	cost := &stubCostService{}

	rd := NewRegionDiscoverer(msk, cost)
	result, err := rd.Discover(context.Background(), testRegion, true)

	require.NoError(t, err)
	require.Len(t, result.Configurations, 1)
}
