package clusters

import (
	"testing"

	"github.com/confluentinc/kcp/internal/sources"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMergeOSKResults_InitializesNilOSKSources(t *testing.T) {
	state := &types.State{}
	result := &sources.ScanResult{}

	err := mergeOSKResults(state, result)
	require.NoError(t, err)
	require.NotNil(t, state.OSKSources)
	assert.Empty(t, state.OSKSources.Clusters)
}

func TestMergeOSKResults_AppendsNewCluster(t *testing.T) {
	state := &types.State{
		OSKSources: &types.OSKSourcesState{
			Clusters: []types.OSKDiscoveredCluster{},
		},
	}

	result := &sources.ScanResult{
		Clusters: []sources.ClusterScanResult{
			{
				Identifier: sources.ClusterIdentifier{
					UniqueID:         "cluster-1",
					BootstrapServers: []string{"broker1:9092"},
				},
				KafkaAdminInfo:     &types.KafkaAdminClientInformation{},
				SourceSpecificData: types.OSKClusterMetadata{},
			},
		},
	}

	err := mergeOSKResults(state, result)
	require.NoError(t, err)
	require.Len(t, state.OSKSources.Clusters, 1)
	assert.Equal(t, "cluster-1", state.OSKSources.Clusters[0].ID)
}

func TestMergeOSKResults_UpdateExistingPreservesMetricsAndClients(t *testing.T) {
	existingMetrics := &types.ProcessedClusterMetrics{
		Region: "test",
	}
	existingClients := []types.DiscoveredClient{
		{ClientId: "app-1"},
	}

	state := &types.State{
		OSKSources: &types.OSKSourcesState{
			Clusters: []types.OSKDiscoveredCluster{
				{
					ID:                "cluster-1",
					BootstrapServers:  []string{"broker1:9092"},
					ClusterMetrics:    existingMetrics,
					DiscoveredClients: existingClients,
				},
			},
		},
	}

	result := &sources.ScanResult{
		Clusters: []sources.ClusterScanResult{
			{
				Identifier: sources.ClusterIdentifier{
					UniqueID:         "cluster-1",
					BootstrapServers: []string{"broker1:9092", "broker2:9092"},
				},
				KafkaAdminInfo:     &types.KafkaAdminClientInformation{},
				SourceSpecificData: types.OSKClusterMetadata{},
			},
		},
	}

	err := mergeOSKResults(state, result)
	require.NoError(t, err)
	require.Len(t, state.OSKSources.Clusters, 1)

	merged := state.OSKSources.Clusters[0]
	assert.Equal(t, []string{"broker1:9092", "broker2:9092"}, merged.BootstrapServers)
	assert.Equal(t, existingMetrics, merged.ClusterMetrics, "metrics should be preserved")
	assert.Equal(t, existingClients, merged.DiscoveredClients, "clients should be preserved")
}

func TestMergeOSKResults_RescanPreservesOldTopicsOnEmpty(t *testing.T) {
	// Simulates a re-scan where topics come back empty (e.g. transient permission failure).
	// Old topics should be preserved via MergeFrom, matching MSK behavior.
	state := &types.State{
		OSKSources: &types.OSKSourcesState{
			Clusters: []types.OSKDiscoveredCluster{
				{
					ID:               "cluster-1",
					BootstrapServers: []string{"broker1:9092"},
					KafkaAdminClientInformation: types.KafkaAdminClientInformation{
						ClusterID: "old-id",
						Topics: &types.Topics{
							Details: []types.TopicDetails{
								{Name: "important-topic", Partitions: 12},
							},
							Summary: types.TopicSummary{Topics: 1, TotalPartitions: 12},
						},
						Acls: []types.Acls{
							{ResourceName: "important-acl"},
						},
					},
				},
			},
		},
	}

	result := &sources.ScanResult{
		Clusters: []sources.ClusterScanResult{
			{
				Identifier: sources.ClusterIdentifier{
					UniqueID:         "cluster-1",
					BootstrapServers: []string{"broker1:9092"},
				},
				// Re-scan returned empty topics and ACLs (permission failure)
				KafkaAdminInfo: &types.KafkaAdminClientInformation{
					ClusterID: "new-id",
					Topics:    nil,
					Acls:      nil,
				},
				SourceSpecificData: types.OSKClusterMetadata{},
			},
		},
	}

	err := mergeOSKResults(state, result)
	require.NoError(t, err)

	merged := state.OSKSources.Clusters[0]
	assert.Equal(t, "new-id", merged.KafkaAdminClientInformation.ClusterID, "new ClusterID takes precedence")
	require.NotNil(t, merged.KafkaAdminClientInformation.Topics, "old topics should be preserved")
	assert.Len(t, merged.KafkaAdminClientInformation.Topics.Details, 1)
	assert.Equal(t, "important-topic", merged.KafkaAdminClientInformation.Topics.Details[0].Name)
	assert.Len(t, merged.KafkaAdminClientInformation.Acls, 1, "old ACLs should be preserved")
	assert.Equal(t, "important-acl", merged.KafkaAdminClientInformation.Acls[0].ResourceName)
}

func TestMergeOSKResults_MixedUpdateAndAppend(t *testing.T) {
	// This test verifies the fix for the pointer invalidation bug:
	// updating existing clusters and appending new ones in the same call
	// must not corrupt data due to slice reallocation.
	state := &types.State{
		OSKSources: &types.OSKSourcesState{
			// Start with capacity=1 so append is likely to reallocate
			Clusters: make([]types.OSKDiscoveredCluster, 1),
		},
	}
	state.OSKSources.Clusters[0] = types.OSKDiscoveredCluster{
		ID:               "existing-cluster",
		BootstrapServers: []string{"old-broker:9092"},
		ClusterMetrics:   &types.ProcessedClusterMetrics{Region: "preserved"},
	}

	result := &sources.ScanResult{
		Clusters: []sources.ClusterScanResult{
			{
				// Update existing
				Identifier: sources.ClusterIdentifier{
					UniqueID:         "existing-cluster",
					BootstrapServers: []string{"new-broker:9092"},
				},
				KafkaAdminInfo:     &types.KafkaAdminClientInformation{},
				SourceSpecificData: types.OSKClusterMetadata{},
			},
			{
				// Append new
				Identifier: sources.ClusterIdentifier{
					UniqueID:         "new-cluster",
					BootstrapServers: []string{"broker3:9092"},
				},
				KafkaAdminInfo:     &types.KafkaAdminClientInformation{},
				SourceSpecificData: types.OSKClusterMetadata{},
			},
		},
	}

	err := mergeOSKResults(state, result)
	require.NoError(t, err)
	require.Len(t, state.OSKSources.Clusters, 2)

	// Verify existing cluster was updated correctly
	assert.Equal(t, "existing-cluster", state.OSKSources.Clusters[0].ID)
	assert.Equal(t, []string{"new-broker:9092"}, state.OSKSources.Clusters[0].BootstrapServers)
	assert.Equal(t, "preserved", state.OSKSources.Clusters[0].ClusterMetrics.Region, "metrics should be preserved on update")

	// Verify new cluster was appended
	assert.Equal(t, "new-cluster", state.OSKSources.Clusters[1].ID)
	assert.Equal(t, []string{"broker3:9092"}, state.OSKSources.Clusters[1].BootstrapServers)
}

func TestMergeOSKResults_InvalidSourceSpecificData(t *testing.T) {
	state := &types.State{
		OSKSources: &types.OSKSourcesState{},
	}

	result := &sources.ScanResult{
		Clusters: []sources.ClusterScanResult{
			{
				Identifier: sources.ClusterIdentifier{
					UniqueID: "cluster-1",
				},
				KafkaAdminInfo:     &types.KafkaAdminClientInformation{},
				SourceSpecificData: "not-osk-metadata",
			},
		},
	}

	err := mergeOSKResults(state, result)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid source-specific data")
}
