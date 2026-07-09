package msk_connectors

import (
	"os"
	"testing"

	"github.com/confluentinc/kcp/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeTempState(t *testing.T, state *types.State) string {
	t.Helper()
	tmp, err := os.CreateTemp(t.TempDir(), "state-*.json")
	require.NoError(t, err)
	require.NoError(t, state.PersistStateFile(tmp.Name()))
	return tmp.Name()
}

func TestParseScanMSKConnectorsOpts_Region(t *testing.T) {
	path := writeTempState(t, &types.State{
		MSKSources: &types.MSKSourcesState{
			Regions: []types.DiscoveredRegion{{Name: "us-east-1"}},
		},
	})

	stateFile = path
	regions = []string{"us-east-1"}
	clusterArns = nil

	opts, err := parseScanMSKConnectorsOpts()
	require.NoError(t, err)
	assert.Equal(t, []string{"us-east-1"}, opts.Regions)
	assert.Empty(t, opts.ClusterArns)
}

func TestParseScanMSKConnectorsOpts_ClusterArn_DerivesRegion(t *testing.T) {
	arn := "arn:aws:kafka:eu-west-3:123456789012:cluster/c/abc-1"
	path := writeTempState(t, &types.State{
		MSKSources: &types.MSKSourcesState{
			Regions: []types.DiscoveredRegion{
				{Name: "eu-west-3", Clusters: []types.DiscoveredCluster{{Arn: arn}}},
			},
		},
	})

	stateFile = path
	regions = nil
	clusterArns = []string{arn}

	opts, err := parseScanMSKConnectorsOpts()
	require.NoError(t, err)
	assert.Equal(t, []string{arn}, opts.ClusterArns)
	assert.Equal(t, []string{"eu-west-3"}, opts.Regions)
}

func TestParseScanMSKConnectorsOpts_ClusterArn_NotInState(t *testing.T) {
	arn := "arn:aws:kafka:eu-west-3:123456789012:cluster/c/abc-1"
	path := writeTempState(t, &types.State{
		MSKSources: &types.MSKSourcesState{Regions: []types.DiscoveredRegion{}},
	})

	stateFile = path
	regions = nil
	clusterArns = []string{arn}

	_, err := parseScanMSKConnectorsOpts()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found in state file")
}

func TestParseScanMSKConnectorsOpts_BadArn(t *testing.T) {
	path := writeTempState(t, &types.State{})

	stateFile = path
	regions = nil
	clusterArns = []string{"not-an-arn"}

	_, err := parseScanMSKConnectorsOpts()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid cluster ARN")
}

func TestParseScanMSKConnectorsOpts_MissingStateFile(t *testing.T) {
	stateFile = "/nonexistent/state.json"
	regions = []string{"us-east-1"}
	clusterArns = nil

	_, err := parseScanMSKConnectorsOpts()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "state file does not exist")
}
