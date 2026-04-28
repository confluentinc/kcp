package self_managed_connectors

import (
	"os"
	"testing"

	"github.com/confluentinc/kcp/internal/types"
	"github.com/stretchr/testify/assert"
)

func TestParseScanSelfManagedConnectorsOpts_MSK_Success(t *testing.T) {
	// Create temporary state file
	state := &types.State{
		MSKSources: &types.MSKSourcesState{
			Regions: []types.DiscoveredRegion{
				{
					Clusters: []types.DiscoveredCluster{
						{
							Arn: "arn:aws:kafka:us-east-1:123456789012:cluster/test-cluster/abc-123",
						},
					},
				},
			},
		},
	}
	tmpFile, err := os.CreateTemp("", "state-*.json")
	assert.NoError(t, err)
	defer func() { _ = os.Remove(tmpFile.Name()) }()

	err = state.PersistStateFile(tmpFile.Name())
	assert.NoError(t, err)

	// Set flags
	stateFile = tmpFile.Name()
	connectRestURL = "http://localhost:8083"
	clusterID = "arn:aws:kafka:us-east-1:123456789012:cluster/test-cluster/abc-123"
	useUnauthenticated = true

	opts, err := parseScanSelfManagedConnectorsOpts()
	assert.NoError(t, err)
	assert.Equal(t, "msk", opts.SourceType)
	assert.Equal(t, "arn:aws:kafka:us-east-1:123456789012:cluster/test-cluster/abc-123", opts.ClusterArn)
	assert.Equal(t, "", opts.ClusterID)
}

func TestParseScanSelfManagedConnectorsOpts_OSK_Success(t *testing.T) {
	// Create temporary state file
	state := &types.State{
		OSKSources: &types.OSKSourcesState{
			Clusters: []types.OSKDiscoveredCluster{
				{
					ID: "production-kafka",
				},
			},
		},
	}
	tmpFile, err := os.CreateTemp("", "state-*.json")
	assert.NoError(t, err)
	defer func() { _ = os.Remove(tmpFile.Name()) }()

	err = state.PersistStateFile(tmpFile.Name())
	assert.NoError(t, err)

	// Set flags
	stateFile = tmpFile.Name()
	connectRestURL = "http://localhost:8083"
	clusterID = "production-kafka"
	useUnauthenticated = true

	opts, err := parseScanSelfManagedConnectorsOpts()
	assert.NoError(t, err)
	assert.Equal(t, "osk", opts.SourceType)
	assert.Equal(t, "", opts.ClusterArn)
	assert.Equal(t, "production-kafka", opts.ClusterID)
}

func TestParseScanSelfManagedConnectorsOpts_MSK_ClusterNotFound(t *testing.T) {
	// Create temporary state file with no clusters
	state := &types.State{
		MSKSources: &types.MSKSourcesState{
			Regions: []types.DiscoveredRegion{},
		},
	}
	tmpFile, err := os.CreateTemp("", "state-*.json")
	assert.NoError(t, err)
	defer func() { _ = os.Remove(tmpFile.Name()) }()

	err = state.PersistStateFile(tmpFile.Name())
	assert.NoError(t, err)

	// Set flags
	stateFile = tmpFile.Name()
	connectRestURL = "http://localhost:8083"
	clusterID = "arn:aws:kafka:us-east-1:123456789012:cluster/test-cluster/abc-123"
	useUnauthenticated = true

	_, err = parseScanSelfManagedConnectorsOpts()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "cluster not found in state file")
}

func TestParseScanSelfManagedConnectorsOpts_OSK_ClusterNotFound(t *testing.T) {
	// Create temporary state file with no clusters
	state := &types.State{
		OSKSources: &types.OSKSourcesState{
			Clusters: []types.OSKDiscoveredCluster{},
		},
	}
	tmpFile, err := os.CreateTemp("", "state-*.json")
	assert.NoError(t, err)
	defer func() { _ = os.Remove(tmpFile.Name()) }()

	err = state.PersistStateFile(tmpFile.Name())
	assert.NoError(t, err)

	// Set flags
	stateFile = tmpFile.Name()
	connectRestURL = "http://localhost:8083"
	clusterID = "non-existent"
	useUnauthenticated = true

	_, err = parseScanSelfManagedConnectorsOpts()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "cluster not found in state file")
}

