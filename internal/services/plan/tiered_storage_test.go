package plan

import (
	"testing"

	"github.com/confluentinc/kcp/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	kafkatypes "github.com/aws/aws-sdk-go-v2/service/kafka/types"
)

// Tiered storage detection picks up StorageMode == "TIERED" and
// surfaces the cluster in §Tiered Storage.
func TestDetectTieredStorage_TieredClusterSurfaces(t *testing.T) {
	tiered := redFlagCluster("tiered-cluster", "3.5.0", "", string(kafkatypes.StorageModeTiered))
	local := redFlagCluster("local-cluster", "3.5.0", "", string(kafkatypes.StorageModeLocal))
	state := wrapClusters(tiered, local)

	section := detectTieredStorage(state, defaultInputs())
	require.NotNil(t, section)
	require.Len(t, section.Clusters, 1)
	assert.Equal(t, "tiered-cluster", section.Clusters[0].ClusterID)
}

// No tiered storage anywhere → section nils so the renderer omits.
func TestDetectTieredStorage_NoTieredFleetReturnsNil(t *testing.T) {
	local := redFlagCluster("local-cluster", "3.5.0", "", string(kafkatypes.StorageModeLocal))
	state := wrapClusters(local)
	assert.Nil(t, detectTieredStorage(state, defaultInputs()))
}

// RemoteLogSizeBytes from CloudWatch aggregates is plumbed through;
// falls back to Average when Maximum is absent.
func TestDetectTieredStorage_RemoteLogSizeBytes(t *testing.T) {
	tiered := redFlagCluster("tiered-cluster", "3.5.0", "", string(kafkatypes.StorageModeTiered))
	v := 1024.0 * 1024 * 1024 * 50 // 50 GB
	tiered.ClusterMetrics.Aggregates = map[string]types.MetricAggregate{
		"RemoteLogSizeBytes": {Average: &v},
	}
	state := wrapClusters(tiered)
	section := detectTieredStorage(state, defaultInputs())
	require.NotNil(t, section)
	require.Len(t, section.Clusters, 1)
	assert.InDelta(t, v, section.Clusters[0].RemoteLogSizeBytes, 0.1)
}

// When CloudWatch publishes both Maximum and Average for
// RemoteLogSizeBytes, the renderer reads Maximum (peak observed
// footprint) rather than Average — Average over a 30-day window
// for a fixed 7-day retention would report ~half the real current
// footprint, which is misleading for the trade-off framing.
func TestDetectTieredStorage_RemoteLogSizeBytes_MaximumWinsOverAverage(t *testing.T) {
	tiered := redFlagCluster("tiered-cluster", "3.5.0", "", string(kafkatypes.StorageModeTiered))
	avg := 1024.0 * 1024 * 1024 * 20 // 20 GB avg
	max := 1024.0 * 1024 * 1024 * 50 // 50 GB peak
	tiered.ClusterMetrics.Aggregates = map[string]types.MetricAggregate{
		"RemoteLogSizeBytes": {Average: &avg, Maximum: &max},
	}
	state := wrapClusters(tiered)
	section := detectTieredStorage(state, defaultInputs())
	require.NotNil(t, section)
	require.Len(t, section.Clusters, 1)
	assert.InDelta(t, max, section.Clusters[0].RemoteLogSizeBytes, 0.1, "Maximum must win when both aggregates are present")
}

// consumer_history_requirement defaults to `required` when undeclared.
// historical_data_strategy stays empty unless the customer declares
// it, EXCEPT when consumer_history_requirement == not_required (then
// the cascade defaults strategy to defer_to_account_team).
func TestDetectTieredStorage_StrategyCascade(t *testing.T) {
	tiered := redFlagCluster("tiered-cluster", "3.5.0", "", string(kafkatypes.StorageModeTiered))
	state := wrapClusters(tiered)

	// Default — required + undeclared strategy.
	section := detectTieredStorage(state, defaultInputs())
	require.NotNil(t, section)
	assert.Equal(t, ConsumerHistoryRequired, section.ConsumerHistoryRequirement)
	assert.Equal(t, "", section.HistoricalDataStrategy, "default empty strategy when history is required")

	// not_required → defer_to_account_team cascade.
	inputs := defaultInputs()
	inputs.ConsumerHistoryRequirement = ConsumerHistoryNotRequired
	section = detectTieredStorage(state, inputs)
	require.NotNil(t, section)
	assert.Equal(t, HistoricalDeferToAccount, section.HistoricalDataStrategy, "cascade defaults to defer when history not required")

	// Explicit strategy declared → respected.
	inputs.HistoricalDataStrategy = HistoricalBulkLoadExtern
	section = detectTieredStorage(state, inputs)
	require.NotNil(t, section)
	assert.Equal(t, HistoricalBulkLoadExtern, section.HistoricalDataStrategy)
}

// `tiered_strategy_undeclared` OQ fires when tiered storage is
// detected, consumer history is required (default), and the strategy
// hasn't been declared.
func TestDetectTieredStorageOQ_StrategyUndeclared(t *testing.T) {
	tiered := redFlagCluster("tiered-cluster", "3.5.0", "", string(kafkatypes.StorageModeTiered))
	state := wrapClusters(tiered)
	section := detectTieredStorage(state, defaultInputs())

	oqs := detectTieredStorageOpenQuestions(section, defaultInputs())
	require.Len(t, oqs, 1)
	assert.Equal(t, "tiered_strategy_undeclared", oqs[0].ID)
}

// Typo in consumer_history_requirement → tiered_consumer_history_invalid OQ.
func TestDetectTieredStorageOQ_ConsumerHistoryTypo(t *testing.T) {
	tiered := redFlagCluster("tiered-cluster", "3.5.0", "", string(kafkatypes.StorageModeTiered))
	state := wrapClusters(tiered)
	inputs := defaultInputs()
	inputs.ConsumerHistoryRequirement = "requiredd" // typo
	section := detectTieredStorage(state, inputs)

	oqs := detectTieredStorageOpenQuestions(section, inputs)
	found := false
	for _, oq := range oqs {
		if oq.ID == "tiered_consumer_history_invalid" {
			found = true
		}
	}
	assert.True(t, found, "expected tiered_consumer_history_invalid OQ for typo")
}
