package plan

import (
	"testing"

	"github.com/confluentinc/kcp/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// parseMSKInstanceType extracts the `<family>.<size>` portion from
// the AWS Cost Explorer usage string and lowercases the family token
// to match BrokerNodeGroupInfo.InstanceType shape.
func TestParseMSKInstanceType(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"USE1-Kafka.m5.large", "kafka.m5.large"},
		{"APN1-Express.m7g.large", "express.m7g.large"},
		{"EU-Kafka.kraft.t3.small", "kafka.kraft.t3.small"},
		{"USE1-DataTransfer-Out-Bytes", ""}, // non-broker row
		{"", ""},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			assert.Equal(t, c.want, parseMSKInstanceType(c.in, mskUsageTypeRegex(defaultCfg(t))))
		})
	}
}

// Diff surfaces a cost-billed instance type that's NOT in inventory.
func TestDetectCostReconciliation_DiffSurfacesHiddenType(t *testing.T) {
	discovered := redFlagCluster("discovered-cluster", "3.5.0", "kafka.m5.large", "")
	state := wrapClusters(discovered)
	// Inject cost data: m5.large (present in inventory) + m7g.large (NOT in inventory)
	state.Sources[0].MSKData.Regions[0].Costs = types.ProcessedRegionCosts{
		Region: "us-east-1",
		Results: []types.ProcessedCost{
			{Start: "2026-04-01", UsageType: "USE1-Kafka.m5.large", Values: types.ProcessedCostBreakdown{UnblendedCost: 100.50}},
			{Start: "2026-04-01", UsageType: "USE1-Kafka.m7g.large", Values: types.ProcessedCostBreakdown{UnblendedCost: 250.75}},
		},
	}
	section := detectCostReconciliation(state, defaultCfg(t))
	require.NotNil(t, section)
	require.Len(t, section.Candidates, 1, "only the hidden type appears as a candidate")
	assert.Equal(t, "kafka.m7g.large", section.Candidates[0].InstanceType)
	assert.InDelta(t, 250.75, section.Candidates[0].TotalSpend, 0.01)
}

// Candidates are sorted by TotalSpend descending.
func TestDetectCostReconciliation_SortedByTotalSpendDesc(t *testing.T) {
	state := wrapClusters(redFlagCluster("only-known", "3.5.0", "kafka.t3.small", ""))
	state.Sources[0].MSKData.Regions[0].Costs = types.ProcessedRegionCosts{
		Region: "us-east-1",
		Results: []types.ProcessedCost{
			{Start: "2026-04-01", UsageType: "USE1-Kafka.m5.large", Values: types.ProcessedCostBreakdown{UnblendedCost: 100.0}},
			{Start: "2026-04-01", UsageType: "USE1-Kafka.m7g.large", Values: types.ProcessedCostBreakdown{UnblendedCost: 500.0}},
			{Start: "2026-04-01", UsageType: "USE1-Express.m7g.large", Values: types.ProcessedCostBreakdown{UnblendedCost: 250.0}},
		},
	}
	section := detectCostReconciliation(state, defaultCfg(t))
	require.NotNil(t, section)
	require.Len(t, section.Candidates, 3)
	assert.Equal(t, "kafka.m7g.large", section.Candidates[0].InstanceType, "highest-spend first")
	assert.Equal(t, "express.m7g.large", section.Candidates[1].InstanceType)
	assert.Equal(t, "kafka.m5.large", section.Candidates[2].InstanceType)
}

// Empty cost data → detectCostReconciliation returns nil AND the OQ
// detector fires `cost_data_not_collected`.
func TestDetectCostReconciliation_EmptyCostDataEmitsOQ(t *testing.T) {
	state := wrapClusters(redFlagCluster("only-known", "3.5.0", "kafka.t3.small", ""))
	// No cost data attached.
	assert.Nil(t, detectCostReconciliation(state, defaultCfg(t)))
	oqs := detectCostReconciliationOpenQuestions(state)
	require.Len(t, oqs, 1)
	assert.Equal(t, "cost_data_not_collected", oqs[0].ID)
}

// Non-broker usage strings (data transfer, EBS, etc.) are ignored.
func TestDetectCostReconciliation_NonBrokerRowsIgnored(t *testing.T) {
	state := wrapClusters(redFlagCluster("only-known", "3.5.0", "kafka.t3.small", ""))
	state.Sources[0].MSKData.Regions[0].Costs = types.ProcessedRegionCosts{
		Region: "us-east-1",
		Results: []types.ProcessedCost{
			{Start: "2026-04-01", UsageType: "USE1-DataTransfer-Out-Bytes", Values: types.ProcessedCostBreakdown{UnblendedCost: 500.0}},
			{Start: "2026-04-01", UsageType: "USE1-EBS:VolumeUsage.gp3", Values: types.ProcessedCostBreakdown{UnblendedCost: 200.0}},
		},
	}
	section := detectCostReconciliation(state, defaultCfg(t))
	assert.Nil(t, section, "non-broker rows must not generate candidates")
}

// Months / days observed roll up across distinct timestamps.
func TestDetectCostReconciliation_ObservationCounts(t *testing.T) {
	state := wrapClusters(redFlagCluster("only-known", "3.5.0", "kafka.t3.small", ""))
	state.Sources[0].MSKData.Regions[0].Costs = types.ProcessedRegionCosts{
		Region: "us-east-1",
		Results: []types.ProcessedCost{
			{Start: "2026-04-01", UsageType: "USE1-Kafka.m7g.large", Values: types.ProcessedCostBreakdown{UnblendedCost: 100.0}},
			{Start: "2026-04-02", UsageType: "USE1-Kafka.m7g.large", Values: types.ProcessedCostBreakdown{UnblendedCost: 100.0}},
			{Start: "2026-05-01", UsageType: "USE1-Kafka.m7g.large", Values: types.ProcessedCostBreakdown{UnblendedCost: 100.0}},
		},
	}
	section := detectCostReconciliation(state, defaultCfg(t))
	require.NotNil(t, section)
	require.Len(t, section.Candidates, 1)
	assert.Equal(t, 2, section.Candidates[0].MonthsObserved, "April + May")
	assert.Equal(t, 3, section.Candidates[0].DaysObserved)
	assert.InDelta(t, 300.0, section.Candidates[0].TotalSpend, 0.01)
}
