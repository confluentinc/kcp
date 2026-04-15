package metrics

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/confluentinc/kcp/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Removed: TestParseMetricReporterOpts_SourceTypeAuto - "auto" is no longer supported

func TestParseMetricReporterOpts_SourceTypeMSK(t *testing.T) {
	// Setup: create a state file with both MSK and OSK sources
	tmpDir := t.TempDir()
	stateFilePath := filepath.Join(tmpDir, "state.json")

	state := &types.State{
		MSKSources: &types.MSKSourcesState{
			Regions: []types.DiscoveredRegion{
				{
					Name: "us-east-1",
					Clusters: []types.DiscoveredCluster{
						{Arn: "arn:aws:kafka:us-east-1:123456789012:cluster/test-msk/uuid1"},
					},
				},
			},
		},
		OSKSources: &types.OSKSourcesState{
			Clusters: []types.OSKDiscoveredCluster{
				{ID: "osk-cluster-1"},
			},
		},
		Timestamp: time.Now(),
	}

	err := state.PersistStateFile(stateFilePath)
	require.NoError(t, err)

	// Set module-level variables
	stateFile = stateFilePath
	sourceType = "msk"
	clusterIds = []string{}
	start = ""
	end = ""

	// Test
	opts, err := parseMetricReporterOpts()

	// Verify
	require.NoError(t, err)
	assert.NotNil(t, opts)
	assert.Equal(t, "msk", opts.SourceType)
	// Should only include MSK clusters
	assert.Len(t, opts.ClusterIds, 1)
	assert.Contains(t, opts.ClusterIds[0], "arn:aws:kafka")
}

func TestParseMetricReporterOpts_SourceTypeOSK(t *testing.T) {
	// Setup: create a state file with both MSK and OSK sources
	tmpDir := t.TempDir()
	stateFilePath := filepath.Join(tmpDir, "state.json")

	state := &types.State{
		MSKSources: &types.MSKSourcesState{
			Regions: []types.DiscoveredRegion{
				{
					Name: "us-east-1",
					Clusters: []types.DiscoveredCluster{
						{Arn: "arn:aws:kafka:us-east-1:123456789012:cluster/test-msk/uuid1"},
					},
				},
			},
		},
		OSKSources: &types.OSKSourcesState{
			Clusters: []types.OSKDiscoveredCluster{
				{ID: "osk-cluster-1"},
			},
		},
		Timestamp: time.Now(),
	}

	err := state.PersistStateFile(stateFilePath)
	require.NoError(t, err)

	// Set module-level variables
	stateFile = stateFilePath
	sourceType = "osk"
	clusterIds = []string{}
	start = ""
	end = ""

	// Test
	opts, err := parseMetricReporterOpts()

	// Verify
	require.NoError(t, err)
	assert.NotNil(t, opts)
	assert.Equal(t, "osk", opts.SourceType)
	// Should only include OSK clusters
	assert.Len(t, opts.ClusterIds, 1)
	assert.Equal(t, "osk-cluster-1", opts.ClusterIds[0])
}

// Removed: TestParseMetricReporterOpts_ClusterArnFlag - --cluster-arn flag no longer exists

func TestParseMetricReporterOpts_ClusterIdFlag(t *testing.T) {
	// Setup: create a state file
	tmpDir := t.TempDir()
	stateFilePath := filepath.Join(tmpDir, "state.json")

	state := &types.State{
		OSKSources: &types.OSKSourcesState{
			Clusters: []types.OSKDiscoveredCluster{
				{ID: "osk-cluster-1"},
			},
		},
		Timestamp: time.Now(),
	}

	err := state.PersistStateFile(stateFilePath)
	require.NoError(t, err)

	// Set module-level variables
	stateFile = stateFilePath
	sourceType = ""
	clusterIds = []string{"osk-cluster-1"}
	start = ""
	end = ""

	// Test
	opts, err := parseMetricReporterOpts()

	// Verify
	require.NoError(t, err)
	assert.NotNil(t, opts)
	assert.Len(t, opts.ClusterIds, 1)
	assert.Equal(t, "osk-cluster-1", opts.ClusterIds[0])
}

func TestParseMetricReporterOpts_MultipleClusterIds(t *testing.T) {
	// Setup: create a state file
	tmpDir := t.TempDir()
	stateFilePath := filepath.Join(tmpDir, "state.json")

	state := &types.State{
		MSKSources: &types.MSKSourcesState{
			Regions: []types.DiscoveredRegion{
				{
					Name: "us-east-1",
					Clusters: []types.DiscoveredCluster{
						{Arn: "arn:aws:kafka:us-east-1:123456789012:cluster/test-msk/uuid1"},
					},
				},
			},
		},
		OSKSources: &types.OSKSourcesState{
			Clusters: []types.OSKDiscoveredCluster{
				{ID: "osk-cluster-1"},
			},
		},
		Timestamp: time.Now(),
	}

	err := state.PersistStateFile(stateFilePath)
	require.NoError(t, err)

	// Set module-level variables
	stateFile = stateFilePath
	sourceType = ""
	clusterIds = []string{"arn:aws:kafka:us-east-1:123456789012:cluster/test-msk/uuid1", "osk-cluster-1"}
	start = ""
	end = ""

	// Test
	opts, err := parseMetricReporterOpts()

	// Verify
	require.NoError(t, err)
	assert.NotNil(t, opts)
	// Should include both clusters (MSK ARN and OSK ID)
	assert.Len(t, opts.ClusterIds, 2)
}

func TestParseMetricReporterOpts_InvalidSourceType(t *testing.T) {
	// Setup: create a state file
	tmpDir := t.TempDir()
	stateFilePath := filepath.Join(tmpDir, "state.json")

	state := &types.State{
		MSKSources: &types.MSKSourcesState{
			Regions: []types.DiscoveredRegion{
				{
					Name: "us-east-1",
					Clusters: []types.DiscoveredCluster{
						{Arn: "arn:aws:kafka:us-east-1:123456789012:cluster/test-msk/uuid1"},
					},
				},
			},
		},
		Timestamp: time.Now(),
	}

	err := state.PersistStateFile(stateFilePath)
	require.NoError(t, err)

	// Set module-level variables
	stateFile = stateFilePath
	sourceType = "invalid"
	clusterIds = []string{}
	start = ""
	end = ""

	// Test
	_, err = parseMetricReporterOpts()

	// Verify
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid source-type")
}

func TestParseMetricReporterOpts_StateFileNotExist(t *testing.T) {
	// Set module-level variables
	stateFile = "/nonexistent/path/state.json"
	sourceType = "msk"
	clusterIds = []string{}
	start = ""
	end = ""

	// Test
	_, err := parseMetricReporterOpts()

	// Verify
	require.Error(t, err)
	assert.Contains(t, err.Error(), "state file does not exist")
}

func TestParseMetricReporterOpts_NoClustersInState_MSK(t *testing.T) {
	// Setup: create a state file with no clusters
	tmpDir := t.TempDir()
	stateFilePath := filepath.Join(tmpDir, "state.json")

	state := &types.State{
		MSKSources: &types.MSKSourcesState{
			Regions: []types.DiscoveredRegion{},
		},
		OSKSources: &types.OSKSourcesState{
			Clusters: []types.OSKDiscoveredCluster{},
		},
		Timestamp: time.Now(),
	}

	err := state.PersistStateFile(stateFilePath)
	require.NoError(t, err)

	// Set module-level variables - source-type msk with no MSK clusters
	stateFile = stateFilePath
	sourceType = "msk"
	clusterIds = []string{}
	start = ""
	end = ""

	// Test
	_, err = parseMetricReporterOpts()

	// Verify
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no msk clusters found in state file")
}

func TestParseMetricReporterOpts_NoClustersInState_Neither(t *testing.T) {
	// Setup: create a state file with no clusters
	tmpDir := t.TempDir()
	stateFilePath := filepath.Join(tmpDir, "state.json")

	state := &types.State{
		MSKSources: &types.MSKSourcesState{
			Regions: []types.DiscoveredRegion{},
		},
		OSKSources: &types.OSKSourcesState{
			Clusters: []types.OSKDiscoveredCluster{},
		},
		Timestamp: time.Now(),
	}

	err := state.PersistStateFile(stateFilePath)
	require.NoError(t, err)

	// Set module-level variables - neither flag provided
	stateFile = stateFilePath
	sourceType = ""
	clusterIds = []string{}
	start = ""
	end = ""

	// Test
	_, err = parseMetricReporterOpts()

	// Verify
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no clusters found in state file")
}

func TestPreRunReportMetrics_InvalidSourceType(t *testing.T) {
	// Create a minimal command to test PreRunE
	cmd := NewReportMetricsCmd()

	// Set invalid source-type
	sourceType = "kafka"

	// Test
	err := preRunReportMetrics(cmd, []string{})

	// Verify
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid source-type")
}

func TestPreRunReportMetrics_ValidSourceTypes(t *testing.T) {
	testCases := []struct {
		name       string
		sourceType string
		clusterIds []string
	}{
		{"msk", "msk", []string{}},
		{"osk", "osk", []string{}},
		{"with cluster id", "", []string{"cluster-1"}},
		{"neither flag", "", []string{}},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create a minimal command to test PreRunE
			cmd := NewReportMetricsCmd()

			// Set valid source-type and cluster IDs
			sourceType = tc.sourceType
			clusterIds = tc.clusterIds

			// Test - should pass validation (may fail on env binding, but that's OK for this test)
			err := preRunReportMetrics(cmd, []string{})

			// Verify - should not complain about invalid source-type
			if err != nil {
				assert.NotContains(t, err.Error(), "invalid source-type")
			}
		})
	}
}

func TestNewReportMetricsCmd_FlagsRegistered(t *testing.T) {
	cmd := NewReportMetricsCmd()

	// Verify all required flags exist
	assert.NotNil(t, cmd.Flags().Lookup("state-file"))
	assert.NotNil(t, cmd.Flags().Lookup("source-type"))
	assert.NotNil(t, cmd.Flags().Lookup("cluster-id"))
	assert.NotNil(t, cmd.Flags().Lookup("start"))
	assert.NotNil(t, cmd.Flags().Lookup("end"))

	// Verify deprecated flag is removed
	assert.Nil(t, cmd.Flags().Lookup("cluster-arn"))
}

func TestNewReportMetricsCmd_MutuallyExclusiveFlags(t *testing.T) {
	// Create a temporary state file
	tmpDir := t.TempDir()
	stateFilePath := filepath.Join(tmpDir, "state.json")

	state := &types.State{
		MSKSources: &types.MSKSourcesState{
			Regions: []types.DiscoveredRegion{
				{
					Name: "us-east-1",
					Clusters: []types.DiscoveredCluster{
						{Arn: "arn:aws:kafka:us-east-1:123456789012:cluster/test-msk/uuid1"},
					},
				},
			},
		},
		Timestamp: time.Now(),
	}

	err := state.PersistStateFile(stateFilePath)
	require.NoError(t, err)

	// Test that using both --cluster-id and --source-type together fails
	cmd := NewReportMetricsCmd()
	cmd.SetArgs([]string{
		"--state-file", stateFilePath,
		"--cluster-id", "arn:aws:kafka:us-east-1:123456789012:cluster/test-msk/uuid1",
		"--source-type", "msk",
	})

	err = cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "if any flags in the group [cluster-id source-type] are set none of the others can be")
}

func TestParseMetricReporterOpts_NeitherFlagProvided(t *testing.T) {
	// Setup: create a state file with both MSK and OSK sources
	tmpDir := t.TempDir()
	stateFilePath := filepath.Join(tmpDir, "state.json")

	state := &types.State{
		MSKSources: &types.MSKSourcesState{
			Regions: []types.DiscoveredRegion{
				{
					Name: "us-east-1",
					Clusters: []types.DiscoveredCluster{
						{Arn: "arn:aws:kafka:us-east-1:123456789012:cluster/test-msk/uuid1"},
					},
				},
			},
		},
		OSKSources: &types.OSKSourcesState{
			Clusters: []types.OSKDiscoveredCluster{
				{ID: "osk-cluster-1"},
			},
		},
		Timestamp: time.Now(),
	}

	err := state.PersistStateFile(stateFilePath)
	require.NoError(t, err)

	// Set module-level variables - neither flag provided
	stateFile = stateFilePath
	sourceType = ""
	clusterIds = []string{}
	start = ""
	end = ""

	// Test
	opts, err := parseMetricReporterOpts()

	// Verify
	require.NoError(t, err)
	assert.NotNil(t, opts)
	// Should include clusters from both sources
	assert.Len(t, opts.ClusterIds, 2)
	// Should have both MSK ARN and OSK ID
	assert.Contains(t, opts.ClusterIds, "arn:aws:kafka:us-east-1:123456789012:cluster/test-msk/uuid1")
	assert.Contains(t, opts.ClusterIds, "osk-cluster-1")
}

// Helper to clean up state after tests
func TestMain(m *testing.M) {
	code := m.Run()
	os.Exit(code)
}
