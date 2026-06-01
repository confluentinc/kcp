package types

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPlanJSONRoundTrip(t *testing.T) {
	t.Run("zero-value plan round-trips", func(t *testing.T) {
		var p Plan
		data, err := json.Marshal(p)
		require.NoError(t, err)
		var decoded Plan
		require.NoError(t, json.Unmarshal(data, &decoded))
		assert.Equal(t, p, decoded)
	})

	t.Run("populated plan round-trips deeply equal", func(t *testing.T) {
		p := Plan{
			Header: PlanHeader{
				Source:            "Amazon MSK",
				StateFilePath:     "kcp-state.json",
				KCPVersion:        "0.7.2",
				GeneratedAt:       time.Date(2026, 5, 11, 12, 0, 0, 0, time.UTC),
				PlanSchemaVersion: "1",
			},
			SourceEnvironment: SourceEnvironment{TotalRegions: 1, Clusters: []SourceClusterSummary{{ClusterID: "x", Region: "us-east-1"}}},
			Sizing: []ClusterSizing{
				{ClusterID: "x", SizedInMBps: 1.0, FinalECKU: 1, Citations: []FieldCitation{{Path: "a", Value: 1.0}}},
			},
			ClusterTypeDecision: []ClusterTypeDecision{{ClusterID: "x", Verdict: ClusterTypeEnterprise}},
			NetworkingDecision:  []NetworkingDecision{{ClusterID: "x", Verdict: NetworkingPrivateLink}},
		}
		data, err := json.Marshal(p)
		require.NoError(t, err)
		var decoded Plan
		require.NoError(t, json.Unmarshal(data, &decoded))
		assert.Equal(t, p, decoded)
	})
}

func TestEnumIsValid(t *testing.T) {
	assert.True(t, ClusterTypeEnterprise.IsValid())
	assert.False(t, ClusterType("Bogus").IsValid())
	assert.True(t, NetworkingPrivateLink.IsValid())
	assert.False(t, Networking("None").IsValid())
}
