package plan

import (
	"testing"

	"github.com/confluentinc/kcp/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fixtureCluster builds a ProcessedCluster with the metric aggregates that
// drive the sizing formula. Caller passes in P95/peak values in MBps.
func fixtureCluster(name string, partitions int, p95InMBps, p95OutMBps, peakInMBps, peakOutMBps float64) types.ProcessedCluster {
	p95In := p95InMBps * bytesPerMBps
	p95Out := p95OutMBps * bytesPerMBps
	peakIn := peakInMBps * bytesPerMBps
	peakOut := peakOutMBps * bytesPerMBps
	return types.ProcessedCluster{
		Name:   name,
		Region: "us-east-1",
		ClusterMetrics: types.ProcessedClusterMetrics{
			Aggregates: map[string]types.MetricAggregate{
				"BytesInPerSec":  {P95: &p95In, Maximum: &peakIn},
				"BytesOutPerSec": {P95: &p95Out, Maximum: &peakOut},
			},
		},
		KafkaAdminClientInformation: types.KafkaAdminClientInformation{
			Topics: &types.Topics{Summary: types.TopicSummary{TotalPartitions: partitions}},
		},
	}
}

func defaultInputs() types.PlanInputsResolved {
	return types.PlanInputsResolved{
		SLATarget:                  "99.9",
		SizingPercentile:           "P95",
		HeadroomFraction:           0.30,
		PrivateLinkSafetyThreshold: 0.80,
		SpikyWorkloadRatio:         2.0,
	}
}

func defaultCfg(t *testing.T) *PlanConfig {
	t.Helper()
	cfg, err := LoadPlanConfig("")
	require.NoError(t, err)
	return cfg
}

func TestComputeClusterSizing_EgressDominant(t *testing.T) {
	// Read-heavy fan-out workload: egress at ~995 MBps drives the max ratio
	// well above ingress and partitions, so the egress dimension wins and
	// sizing snaps to 8 eCKU.
	c := fixtureCluster("read-heavy", 2058, 88.7, 994.8, 99.0, 1200.0)
	s := ComputeClusterSizing(c, defaultCfg(t), defaultInputs())

	assert.False(t, s.Degraded)
	assert.InDelta(t, 88.7, s.P95InMBps, 0.1)
	assert.InDelta(t, 994.8, s.P95OutMBps, 0.1)
	// egress ratio = 994.8 / 180 = 5.5267 dominates; CEIL(5.5267 * 1.30) = 8
	assert.Equal(t, 8, s.SizedECKU)
	assert.Equal(t, 8, s.FinalECKU)
}

func TestComputeClusterSizing_SLAFloorBinds(t *testing.T) {
	// Tiny workload: 99.99 SLA target forces FinalECKU >= 2 even though the
	// math says 1.
	c := fixtureCluster("small", 10, 0.1, 0.1, 0.2, 0.2)
	inputs := defaultInputs()
	inputs.SLATarget = "99.99"
	s := ComputeClusterSizing(c, defaultCfg(t), inputs)
	assert.Equal(t, 1, s.SizedECKU)
	assert.Equal(t, 2, s.SLAFloorECKU)
	assert.Equal(t, 2, s.FinalECKU)
}

func TestComputeClusterSizing_DegradedOnMissingP95(t *testing.T) {
	// State file that has zero metric aggregates (kcp discover ran without
	// kcp scan metrics) should not abort — surface a degraded sizing.
	c := types.ProcessedCluster{
		Name:                        "no-metrics",
		ClusterMetrics:              types.ProcessedClusterMetrics{Aggregates: map[string]types.MetricAggregate{}},
		KafkaAdminClientInformation: types.KafkaAdminClientInformation{Topics: &types.Topics{Summary: types.TopicSummary{TotalPartitions: 50}}},
	}
	s := ComputeClusterSizing(c, defaultCfg(t), defaultInputs())
	require.True(t, s.Degraded)
	assert.Contains(t, s.DegradedReason, "BytesInPerSec")
	assert.Equal(t, 50, s.UserPartitions)
	assert.Equal(t, 1, s.FinalECKU)
	assert.Equal(t, 1, s.SLAFloorECKU)
}

func TestComputeClusterSizing_SpikyDetection(t *testing.T) {
	// Peak 5× P95 → spiky flag fires.
	c := fixtureCluster("spike", 100, 10.0, 10.0, 50.0, 50.0)
	s := ComputeClusterSizing(c, defaultCfg(t), defaultInputs())
	assert.True(t, s.SpikyIngress)
	assert.True(t, s.SpikyEgress)
}

func TestComputeClusterSizing_PartitionWinner(t *testing.T) {
	// 100k partitions dominate throughput in the max-ratio.
	c := fixtureCluster("part", 100_000, 1.0, 1.0, 1.0, 1.0)
	s := ComputeClusterSizing(c, defaultCfg(t), defaultInputs())
	// partition ratio = 100000 / 3000 = 33.33; CEIL(33.33 * 1.30) = 44
	assert.Equal(t, 44, s.SizedECKU)
}
