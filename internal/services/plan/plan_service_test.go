package plan

import (
	"testing"
	"time"

	"github.com/confluentinc/kcp/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func fixedNow() time.Time {
	return time.Date(2026, 5, 12, 12, 0, 0, 0, time.UTC)
}

func twoClusterState() types.ProcessedState {
	// b-cluster intentionally listed first so the sort proves it works.
	return types.ProcessedState{
		Sources: []types.ProcessedSource{
			{
				Type: types.SourceTypeMSK,
				MSKData: &types.ProcessedMSKSource{
					Regions: []types.ProcessedRegion{
						{
							Name: "us-east-1",
							Clusters: []types.ProcessedCluster{
								fixtureCluster("b-cluster", 100, 5.0, 5.0, 6.0, 6.0),
								fixtureCluster("a-cluster", 50, 1.0, 1.0, 2.0, 2.0),
							},
						},
					},
				},
			},
		},
	}
}

func TestPlanServiceBuild_BasicShape(t *testing.T) {
	svc := NewPlanService(defaultCfg(t), fixedNow)
	p, err := svc.Build(twoClusterState(), defaultInputs(), "kcp-state.json")
	require.NoError(t, err)

	assert.Equal(t, "Amazon MSK", p.Header.Source)
	assert.Equal(t, "1-experimental", p.Header.PlanSchemaVersion)
	assert.Equal(t, "kcp-state.json", p.Header.StateFilePath)
	assert.Equal(t, 1, p.SourceEnvironment.TotalRegions)
	assert.Len(t, p.Sizing, 2)
	assert.Len(t, p.ClusterTypeDecision, 2)
	assert.Len(t, p.NetworkingDecision, 2)
	assert.Len(t, p.SizingAppendix, 2)
}

func TestPlanServiceBuild_DeterministicByteIdenticalJSON(t *testing.T) {
	// Build the same plan twice and ensure the rendered JSON is byte-equal.
	// This is the load-bearing reproducibility test.
	svc := NewPlanService(defaultCfg(t), fixedNow)
	state := twoClusterState()
	inputs := defaultInputs()

	p1, err := svc.Build(state, inputs, "x.json")
	require.NoError(t, err)
	p2, err := svc.Build(state, inputs, "x.json")
	require.NoError(t, err)

	j1, err := RenderJSON(p1)
	require.NoError(t, err)
	j2, err := RenderJSON(p2)
	require.NoError(t, err)
	assert.Equal(t, string(j1), string(j2), "rendered JSON must be byte-identical across builds")
}

func TestPlanServiceBuild_StableSortAcrossRegions(t *testing.T) {
	state := types.ProcessedState{
		Sources: []types.ProcessedSource{{
			Type: types.SourceTypeMSK,
			MSKData: &types.ProcessedMSKSource{
				Regions: []types.ProcessedRegion{
					// Same cluster name in two regions to prove (Region, Name) sort.
					{Name: "us-west-2", Clusters: []types.ProcessedCluster{
						withRegion(fixtureCluster("collide", 10, 1.0, 1.0, 1.0, 1.0), "us-west-2"),
					}},
					{Name: "us-east-1", Clusters: []types.ProcessedCluster{
						withRegion(fixtureCluster("collide", 10, 1.0, 1.0, 1.0, 1.0), "us-east-1"),
					}},
				},
			},
		}},
	}
	svc := NewPlanService(defaultCfg(t), fixedNow)
	p, err := svc.Build(state, defaultInputs(), "x.json")
	require.NoError(t, err)

	require.Len(t, p.SourceEnvironment.Clusters, 2)
	// us-east-1 sorts before us-west-2.
	assert.Equal(t, "us-east-1", p.SourceEnvironment.Clusters[0].Region)
	assert.Equal(t, "us-west-2", p.SourceEnvironment.Clusters[1].Region)
}

func TestPlanServiceBuild_EmptyState(t *testing.T) {
	svc := NewPlanService(defaultCfg(t), fixedNow)
	p, err := svc.Build(types.ProcessedState{}, defaultInputs(), "")
	require.NoError(t, err)
	assert.Empty(t, p.Sizing)
	assert.Empty(t, p.SourceEnvironment.Clusters)
	assert.Equal(t, 0, p.SourceEnvironment.TotalRegions)
}

func TestPlanServiceBuild_DegradedClusterStillRenders(t *testing.T) {
	state := types.ProcessedState{
		Sources: []types.ProcessedSource{{
			Type: types.SourceTypeMSK,
			MSKData: &types.ProcessedMSKSource{
				Regions: []types.ProcessedRegion{{
					Name: "us-east-1",
					Clusters: []types.ProcessedCluster{{
						Name:                        "no-metrics",
						Region:                      "us-east-1",
						KafkaAdminClientInformation: types.KafkaAdminClientInformation{Topics: &types.Topics{Summary: types.TopicSummary{TotalPartitions: 5}}},
					}},
				}},
			},
		}},
	}
	svc := NewPlanService(defaultCfg(t), fixedNow)
	p, err := svc.Build(state, defaultInputs(), "x.json")
	require.NoError(t, err)
	require.Len(t, p.Sizing, 1)
	assert.True(t, p.Sizing[0].Degraded)
	// No appendix entry for degraded clusters (nothing to show).
	assert.Empty(t, p.SizingAppendix)
}

func withRegion(c types.ProcessedCluster, region string) types.ProcessedCluster {
	c.Region = region
	return c
}
