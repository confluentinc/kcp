package types

import (
	"testing"

	"github.com/confluentinc/kcp/internal/redact"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func connectorSummary(name string, config map[string]string) ConnectorSummary {
	return ConnectorSummary{
		ConnectorArn:           "arn:aws:kafkaconnect:us-east-1:123:connector/" + name,
		ConnectorName:          name,
		ConnectorConfiguration: config,
	}
}

func connectorNames(conns []ConnectorSummary) []string {
	names := make([]string, 0, len(conns))
	for _, c := range conns {
		names = append(names, c.ConnectorName)
	}
	return names
}

// A denied or empty MSK Connect re-run produces a cluster with no connectors;
// merging at the seam must preserve the connectors already in state rather than
// wiping them.
func TestMergeClusterPreservingAdminInfo_PreservesConnectorsWhenRerunEmpty(t *testing.T) {
	existing := DiscoveredCluster{
		Arn: "arn:cluster",
		AWSClientInformation: AWSClientInformation{
			Connectors: []ConnectorSummary{
				connectorSummary("conn-a", map[string]string{"tasks.max": "3"}),
				connectorSummary("conn-b", map[string]string{"tasks.max": "1"}),
			},
		},
	}
	newCluster := DiscoveredCluster{Arn: "arn:cluster"} // re-run found no connectors

	merged := mergeClusterPreservingAdminInfo(existing, newCluster)

	require.Len(t, merged.AWSClientInformation.Connectors, 2, "denied/empty re-run must not wipe prior connectors")
	assert.ElementsMatch(t, []string{"conn-a", "conn-b"}, connectorNames(merged.AWSClientInformation.Connectors))
}

// Security: a preserved prior connector keeps its <kcp-redacted> placeholder.
// The merge must never synthesize a raw secret — it only moves already-redacted
// state across a re-run.
func TestMergeClusterPreservingAdminInfo_PreservedConnectorKeepsRedaction(t *testing.T) {
	existing := DiscoveredCluster{
		Arn: "arn:cluster",
		AWSClientInformation: AWSClientInformation{
			Connectors: []ConnectorSummary{
				connectorSummary("conn-secret", map[string]string{
					"database.password": redact.Placeholder,
					"tasks.max":         "3",
				}),
			},
		},
	}
	newCluster := DiscoveredCluster{Arn: "arn:cluster"}

	merged := mergeClusterPreservingAdminInfo(existing, newCluster)

	require.Len(t, merged.AWSClientInformation.Connectors, 1)
	cfg := merged.AWSClientInformation.Connectors[0].ConnectorConfiguration
	assert.Equal(t, redact.Placeholder, cfg["database.password"], "merge must preserve the redacted placeholder")
	assert.NotEqual(t, "hunter2", cfg["database.password"], "merge must never resurrect a raw secret")
}

// Invariant guard: only Connectors gains merge-preserve semantics. Other
// AWSClientInformation fields remain wholesale-replace, so a re-run's value wins.
func TestMergeClusterPreservingAdminInfo_NonConnectorFieldsStillReplaced(t *testing.T) {
	existing := DiscoveredCluster{
		Arn:                  "arn:cluster",
		AWSClientInformation: AWSClientInformation{ScramSecrets: []string{"old-secret-arn"}},
	}
	newCluster := DiscoveredCluster{
		Arn:                  "arn:cluster",
		AWSClientInformation: AWSClientInformation{ScramSecrets: []string{"new-secret-arn"}},
	}

	merged := mergeClusterPreservingAdminInfo(existing, newCluster)

	assert.Equal(t, []string{"new-secret-arn"}, merged.AWSClientInformation.ScramSecrets, "non-connector fields must still replace wholesale")
}

// A successful overlapping re-run unions old and new by ConnectorName, with the
// new connector winning on a name collision. Result order is not stable (built
// from a Go map), so assert by set membership, never by slice position.
func TestMergeConnectors_OverlapNewWins(t *testing.T) {
	old := []ConnectorSummary{
		connectorSummary("conn-a", map[string]string{"tasks.max": "1"}),
		connectorSummary("conn-b", map[string]string{"tasks.max": "1"}),
	}
	newConns := []ConnectorSummary{
		connectorSummary("conn-b", map[string]string{"tasks.max": "9"}), // updated by same name
		connectorSummary("conn-c", map[string]string{"tasks.max": "3"}),
	}

	merged := mergeConnectors(newConns, old)

	require.Len(t, merged, 3)
	byName := make(map[string]ConnectorSummary, len(merged))
	for _, c := range merged {
		byName[c.ConnectorName] = c
	}
	assert.ElementsMatch(t, []string{"conn-a", "conn-b", "conn-c"}, connectorNames(merged))
	assert.Equal(t, "9", byName["conn-b"].ConnectorConfiguration["tasks.max"], "new connector must win on a name collision")
}

func TestMergeConnectors_EdgeCases(t *testing.T) {
	a := connectorSummary("conn-a", map[string]string{"tasks.max": "1"})

	// old empty, new has → new returned
	assert.ElementsMatch(t, []string{"conn-a"}, connectorNames(mergeConnectors([]ConnectorSummary{a}, nil)))
	// new empty, old has → old preserved
	assert.ElementsMatch(t, []string{"conn-a"}, connectorNames(mergeConnectors(nil, []ConnectorSummary{a})))
	// both empty → empty
	assert.Empty(t, mergeConnectors(nil, nil))
}

func TestRefreshClusters_PreservesConnectorsOnDeniedRerun(t *testing.T) {
	dr := &DiscoveredRegion{
		Clusters: []DiscoveredCluster{
			{
				Arn: "arn:cluster",
				AWSClientInformation: AWSClientInformation{
					Connectors: []ConnectorSummary{
						connectorSummary("conn-a", map[string]string{"tasks.max": "3"}),
						connectorSummary("conn-b", map[string]string{"tasks.max": "1"}),
					},
				},
			},
		},
	}

	// Re-discovery returns the same cluster but ListConnectors was denied → no connectors.
	dr.RefreshClusters([]DiscoveredCluster{{Arn: "arn:cluster"}})

	require.Len(t, dr.Clusters, 1)
	require.Len(t, dr.Clusters[0].AWSClientInformation.Connectors, 2, "RefreshClusters must preserve prior connectors on a denied re-run")
}

func TestUpsertCluster_PreservesConnectorsOnDeniedRerun(t *testing.T) {
	dr := &DiscoveredRegion{
		Clusters: []DiscoveredCluster{
			{
				Arn: "arn:cluster",
				AWSClientInformation: AWSClientInformation{
					Connectors: []ConnectorSummary{
						connectorSummary("conn-a", map[string]string{"tasks.max": "3"}),
					},
				},
			},
		},
	}

	dr.UpsertCluster(DiscoveredCluster{Arn: "arn:cluster"}) // denied re-run, no connectors

	require.Len(t, dr.Clusters, 1)
	require.Len(t, dr.Clusters[0].AWSClientInformation.Connectors, 1, "UpsertCluster must preserve prior connectors on a denied re-run")
}
