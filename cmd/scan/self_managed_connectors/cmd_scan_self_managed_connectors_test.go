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
	assert.Equal(t, types.SourceTypeMSK, opts.SourceType)
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
	assert.Equal(t, types.SourceTypeOSK, opts.SourceType)
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

func TestParseScanSelfManagedConnectorsOpts_ExplicitSourceType_MSK(t *testing.T) {
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

	// Set flags with explicit source-type
	stateFile = tmpFile.Name()
	connectRestURL = "http://localhost:8083"
	clusterID = "arn:aws:kafka:us-east-1:123456789012:cluster/test-cluster/abc-123"
	sourceType = "msk"
	useUnauthenticated = true

	opts, err := parseScanSelfManagedConnectorsOpts()
	assert.NoError(t, err)
	assert.Equal(t, types.SourceTypeMSK, opts.SourceType)
	assert.Equal(t, "arn:aws:kafka:us-east-1:123456789012:cluster/test-cluster/abc-123", opts.ClusterArn)
	assert.Equal(t, "", opts.ClusterID)

	// Reset sourceType
	sourceType = ""
}

func TestParseScanSelfManagedConnectorsOpts_ExplicitSourceType_OSK(t *testing.T) {
	// Create temporary state file
	state := &types.State{
		OSKSources: &types.OSKSourcesState{
			Clusters: []types.OSKDiscoveredCluster{
				{
					ID: "my-cluster",
				},
			},
		},
	}
	tmpFile, err := os.CreateTemp("", "state-*.json")
	assert.NoError(t, err)
	defer func() { _ = os.Remove(tmpFile.Name()) }()

	err = state.PersistStateFile(tmpFile.Name())
	assert.NoError(t, err)

	// Set flags with explicit source-type
	stateFile = tmpFile.Name()
	connectRestURL = "http://localhost:8083"
	clusterID = "my-cluster"
	sourceType = "osk"
	useUnauthenticated = true

	opts, err := parseScanSelfManagedConnectorsOpts()
	assert.NoError(t, err)
	assert.Equal(t, types.SourceTypeOSK, opts.SourceType)
	assert.Equal(t, "", opts.ClusterArn)
	assert.Equal(t, "my-cluster", opts.ClusterID)

	// Reset sourceType
	sourceType = ""
}

func TestParseScanSelfManagedConnectorsOpts_InvalidSourceType(t *testing.T) {
	// Create temporary state file
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

	// Set flags with invalid source-type
	stateFile = tmpFile.Name()
	connectRestURL = "http://localhost:8083"
	clusterID = "test-cluster"
	sourceType = "invalid"
	useUnauthenticated = true

	_, err = parseScanSelfManagedConnectorsOpts()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid source-type: invalid")

	// Reset sourceType
	sourceType = ""
}

func TestParseScanSelfManagedConnectorsOpts_ExplicitSourceType_OverridesAutoDetection(t *testing.T) {
	// Create temporary state file with OSK cluster
	state := &types.State{
		OSKSources: &types.OSKSourcesState{
			Clusters: []types.OSKDiscoveredCluster{
				{
					ID: "arn:custom:id",
				},
			},
		},
	}
	tmpFile, err := os.CreateTemp("", "state-*.json")
	assert.NoError(t, err)
	defer func() { _ = os.Remove(tmpFile.Name()) }()

	err = state.PersistStateFile(tmpFile.Name())
	assert.NoError(t, err)

	// Set flags - cluster-id starts with "arn:" but explicit source-type says OSK
	stateFile = tmpFile.Name()
	connectRestURL = "http://localhost:8083"
	clusterID = "arn:custom:id"
	sourceType = "osk" // Explicit override
	useUnauthenticated = true

	opts, err := parseScanSelfManagedConnectorsOpts()
	assert.NoError(t, err)
	assert.Equal(t, types.SourceTypeOSK, opts.SourceType)
	assert.Equal(t, "", opts.ClusterArn)
	assert.Equal(t, "arn:custom:id", opts.ClusterID)

	// Reset sourceType
	sourceType = ""
}
