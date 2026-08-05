package costs

import (
	"testing"

	"github.com/confluentinc/kcp/internal/services/report"
	"github.com/stretchr/testify/assert"
)

func TestCalculateRegionTotalsAllTypes_IncludesMSKConnect(t *testing.T) {
	rc := &CostReporter{}

	sum := 42.0
	aggregates := report.NewProcessedAggregates()
	aggregates.MSKConnect.UnblendedCost["USE1-Kafka.mcu.general"] = report.CostAggregate{Sum: &sum}

	regionData := report.ProcessedRegionCosts{Aggregates: aggregates}

	totals := rc.calculateRegionTotalsAllTypes(regionData)

	assert.InDelta(t, 42.0, totals[0], 0.001, "MSK Connect cost should count toward the region's Unblended total")
}
