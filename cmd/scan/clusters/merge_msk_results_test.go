package clusters

import (
	"testing"

	"github.com/confluentinc/kcp/internal/sources"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMergeMSKResults_ErrorsWhenNoMSKSourcesInState(t *testing.T) {
	state := &types.State{}
	result := &sources.ScanResult{SourceType: types.SourceTypeMSK}

	err := mergeMSKResults(state, result)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "run 'kcp discover'")
}

func TestMergeMSKResults_RescanPreservesOldDataOnEmpty(t *testing.T) {
	// Simulates a re-scan where the new admin info is empty/nil. Old topics,
	// ACLs, and (most importantly) self-managed connectors must be preserved.
	const arn = "arn:aws:kafka:us-east-1:123:cluster/test/abc-1"

	state := &types.State{
		MSKSources: &types.MSKSourcesState{
			Regions: []types.DiscoveredRegion{
				{
					Name: "us-east-1",
					Clusters: []types.DiscoveredCluster{
						{
							Arn: arn,
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
			},
		},
	}

	result := &sources.ScanResult{
		SourceType: types.SourceTypeMSK,
		Clusters: []sources.ClusterScanResult{
			{
				Identifier: sources.ClusterIdentifier{UniqueID: arn},
				// Re-scan returned empty topics/ACLs (e.g. transient permission failure)
				KafkaAdminInfo: &types.KafkaAdminClientInformation{
					ClusterID: "new-id",
					Topics:    nil,
					Acls:      nil,
				},
			},
		},
	}

	err := mergeMSKResults(state, result)
	require.NoError(t, err)

	merged := state.MSKSources.Regions[0].Clusters[0].KafkaAdminClientInformation
	assert.Equal(t, "new-id", merged.ClusterID, "new ClusterID takes precedence")
	require.NotNil(t, merged.Topics, "old topics should be preserved when new is nil")
	assert.Len(t, merged.Topics.Details, 1)
	assert.Equal(t, "important-topic", merged.Topics.Details[0].Name)
	require.Len(t, merged.Acls, 1, "old ACLs should be preserved when new is nil")
	assert.Equal(t, "important-acl", merged.Acls[0].ResourceName)
}

func TestMergeMSKResults_RescanPreservesSelfManagedConnectors(t *testing.T) {
	// Locks in the connector-preservation guarantee: `kcp scan clusters`
	// returns SelfManagedConnectors=nil, so a re-scan must NOT wipe connectors
	// that already exist in state. Sequence under test:
	//   1. scan clusters   (writes nil)
	//   2. connectors populated in state by other means
	//   3. scan clusters   (returns nil — must preserve step 2)
	const arn = "arn:aws:kafka:us-east-1:123:cluster/test/abc-1"

	state := &types.State{
		MSKSources: &types.MSKSourcesState{
			Regions: []types.DiscoveredRegion{
				{
					Name: "us-east-1",
					Clusters: []types.DiscoveredCluster{
						{
							Arn: arn,
							KafkaAdminClientInformation: types.KafkaAdminClientInformation{
								SelfManagedConnectors: &types.SelfManagedConnectors{
									Connectors: []types.SelfManagedConnector{
										{Name: "rest-discovered", State: "RUNNING", ConnectHost: "worker-1:8083"},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	result := &sources.ScanResult{
		SourceType: types.SourceTypeMSK,
		Clusters: []sources.ClusterScanResult{
			{
				Identifier: sources.ClusterIdentifier{UniqueID: arn},
				// Post-refactor scan-clusters shape: SelfManagedConnectors is nil.
				KafkaAdminInfo: &types.KafkaAdminClientInformation{
					SelfManagedConnectors: nil,
				},
			},
		},
	}

	err := mergeMSKResults(state, result)
	require.NoError(t, err)

	merged := state.MSKSources.Regions[0].Clusters[0].KafkaAdminClientInformation
	require.NotNil(t, merged.SelfManagedConnectors, "REST-discovered connectors must survive a scan-clusters re-run")
	require.Len(t, merged.SelfManagedConnectors.Connectors, 1)
	assert.Equal(t, "rest-discovered", merged.SelfManagedConnectors.Connectors[0].Name)
	assert.Equal(t, "RUNNING", merged.SelfManagedConnectors.Connectors[0].State)
	assert.Equal(t, "worker-1:8083", merged.SelfManagedConnectors.Connectors[0].ConnectHost)
}

func TestMergeMSKResults_NewDataTakesPrecedence(t *testing.T) {
	// When the new scan returns non-empty topics/ACLs they win over the old
	// ones for matching keys, but old entries not in the new result are kept
	// (this is the standard MergeFrom semantics for collections — see the
	// merge helpers in internal/types/state.go).
	const arn = "arn:aws:kafka:us-east-1:123:cluster/test/abc-1"

	state := &types.State{
		MSKSources: &types.MSKSourcesState{
			Regions: []types.DiscoveredRegion{
				{
					Name: "us-east-1",
					Clusters: []types.DiscoveredCluster{
						{
							Arn: arn,
							KafkaAdminClientInformation: types.KafkaAdminClientInformation{
								Topics: &types.Topics{
									Details: []types.TopicDetails{
										{Name: "topic-a", Partitions: 1},
										{Name: "topic-b", Partitions: 2},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	result := &sources.ScanResult{
		SourceType: types.SourceTypeMSK,
		Clusters: []sources.ClusterScanResult{
			{
				Identifier: sources.ClusterIdentifier{UniqueID: arn},
				KafkaAdminInfo: &types.KafkaAdminClientInformation{
					Topics: &types.Topics{
						Details: []types.TopicDetails{
							{Name: "topic-a", Partitions: 5}, // updated partitions
							{Name: "topic-c", Partitions: 3}, // new
						},
					},
				},
			},
		},
	}

	err := mergeMSKResults(state, result)
	require.NoError(t, err)

	merged := state.MSKSources.Regions[0].Clusters[0].KafkaAdminClientInformation
	require.NotNil(t, merged.Topics)

	byName := make(map[string]int)
	for _, td := range merged.Topics.Details {
		byName[td.Name] = td.Partitions
	}
	assert.Equal(t, 5, byName["topic-a"], "new partition count wins for topic-a")
	assert.Equal(t, 2, byName["topic-b"], "topic-b preserved from old data")
	assert.Equal(t, 3, byName["topic-c"], "topic-c added from new scan")
}

func TestMergeMSKResults_UnscannedClustersUntouched(t *testing.T) {
	// Clusters in state that were not part of the scan result must be
	// completely untouched.
	const scannedArn = "arn:aws:kafka:us-east-1:123:cluster/scanned/abc-1"
	const skippedArn = "arn:aws:kafka:us-east-1:123:cluster/skipped/def-2"

	state := &types.State{
		MSKSources: &types.MSKSourcesState{
			Regions: []types.DiscoveredRegion{
				{
					Name: "us-east-1",
					Clusters: []types.DiscoveredCluster{
						{
							Arn: scannedArn,
							KafkaAdminClientInformation: types.KafkaAdminClientInformation{
								ClusterID: "scanned-old",
							},
						},
						{
							Arn: skippedArn,
							KafkaAdminClientInformation: types.KafkaAdminClientInformation{
								ClusterID: "skipped-id",
								SelfManagedConnectors: &types.SelfManagedConnectors{
									Connectors: []types.SelfManagedConnector{{Name: "untouched"}},
								},
							},
						},
					},
				},
			},
		},
	}

	result := &sources.ScanResult{
		SourceType: types.SourceTypeMSK,
		Clusters: []sources.ClusterScanResult{
			{
				Identifier: sources.ClusterIdentifier{UniqueID: scannedArn},
				KafkaAdminInfo: &types.KafkaAdminClientInformation{
					ClusterID: "scanned-new",
				},
			},
		},
	}

	err := mergeMSKResults(state, result)
	require.NoError(t, err)

	clusters := state.MSKSources.Regions[0].Clusters
	require.Len(t, clusters, 2)

	scanned := clusters[0]
	assert.Equal(t, "scanned-new", scanned.KafkaAdminClientInformation.ClusterID)

	skipped := clusters[1]
	assert.Equal(t, "skipped-id", skipped.KafkaAdminClientInformation.ClusterID)
	require.NotNil(t, skipped.KafkaAdminClientInformation.SelfManagedConnectors)
	assert.Equal(t, "untouched", skipped.KafkaAdminClientInformation.SelfManagedConnectors.Connectors[0].Name)
}
