package report

import (
	"testing"

	"github.com/confluentinc/kcp/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCalculateCostAggregates(t *testing.T) {
	rs := NewReportService()

	t.Run("empty costs returns empty aggregates", func(t *testing.T) {
		aggregates := rs.calculateCostAggregates(nil)
		assert.Empty(t, aggregates.AmazonManagedStreamingForApacheKafka.UnblendedCost)
		assert.Empty(t, aggregates.ElasticLoadBalancing.UnblendedCost)
		assert.Empty(t, aggregates.AmazonVPC.UnblendedCost)
	})

	t.Run("routes costs to correct service aggregates", func(t *testing.T) {
		costs := []types.ProcessedCost{
			{
				Start: "2025-01-01", End: "2025-01-02",
				Service: types.ServiceMSK, UsageType: "USE1-Kafka.m5.large",
				Values: types.ProcessedCostBreakdown{UnblendedCost: 10.0, BlendedCost: 10.0},
			},
			{
				Start: "2025-01-01", End: "2025-01-02",
				Service: types.ServiceELB, UsageType: "USE1-LoadBalancerUsage",
				Values: types.ProcessedCostBreakdown{UnblendedCost: 5.0, BlendedCost: 5.0},
			},
			{
				Start: "2025-01-01", End: "2025-01-02",
				Service: types.ServiceVPC, UsageType: "USE1-VpcEndpoint-Hours",
				Values: types.ProcessedCostBreakdown{UnblendedCost: 3.0, BlendedCost: 3.0},
			},
			{
				Start: "2025-01-01", End: "2025-01-02",
				Service: types.ServiceEC2Other, UsageType: "USE1-NatGateway-Hours",
				Values: types.ProcessedCostBreakdown{UnblendedCost: 2.0, BlendedCost: 2.0},
			},
			{
				Start: "2025-01-01", End: "2025-01-02",
				Service: types.ServiceAWSCertificateManager, UsageType: "USE1-FreePrivateCA",
				Values: types.ProcessedCostBreakdown{UnblendedCost: 1.0, BlendedCost: 1.0},
			},
		}

		aggregates := rs.calculateCostAggregates(costs)

		// Verify each service has the right usage type
		assertHasUsageType(t, aggregates.AmazonManagedStreamingForApacheKafka, "USE1-Kafka.m5.large")
		assertHasUsageType(t, aggregates.ElasticLoadBalancing, "USE1-LoadBalancerUsage")
		assertHasUsageType(t, aggregates.AmazonVPC, "USE1-VpcEndpoint-Hours")
		assertHasUsageType(t, aggregates.EC2Other, "USE1-NatGateway-Hours")
		assertHasUsageType(t, aggregates.AWSCertificateManager, "USE1-FreePrivateCA")

		// Verify totals
		assertServiceTotal(t, aggregates.AmazonManagedStreamingForApacheKafka, 10.0)
		assertServiceTotal(t, aggregates.ElasticLoadBalancing, 5.0)
		assertServiceTotal(t, aggregates.AmazonVPC, 3.0)
		assertServiceTotal(t, aggregates.EC2Other, 2.0)
		assertServiceTotal(t, aggregates.AWSCertificateManager, 1.0)
	})

	t.Run("aggregates multiple entries for same service and usage type", func(t *testing.T) {
		costs := []types.ProcessedCost{
			{
				Start: "2025-01-01", End: "2025-01-02",
				Service: types.ServiceELB, UsageType: "USE1-LoadBalancerUsage",
				Values: types.ProcessedCostBreakdown{UnblendedCost: 5.0},
			},
			{
				Start: "2025-01-02", End: "2025-01-03",
				Service: types.ServiceELB, UsageType: "USE1-LoadBalancerUsage",
				Values: types.ProcessedCostBreakdown{UnblendedCost: 7.0},
			},
		}

		aggregates := rs.calculateCostAggregates(costs)

		// Sum should be 12.0
		agg, ok := aggregates.ElasticLoadBalancing.UnblendedCost["USE1-LoadBalancerUsage"].(types.CostAggregate)
		require.True(t, ok)
		require.NotNil(t, agg.Sum)
		assert.InDelta(t, 12.0, *agg.Sum, 0.001)
		assert.InDelta(t, 6.0, *agg.Average, 0.001)
		assert.InDelta(t, 5.0, *agg.Minimum, 0.001)
		assert.InDelta(t, 7.0, *agg.Maximum, 0.001)
	})
}

func TestForService(t *testing.T) {
	aggregates := types.NewProcessedAggregates()

	assert.Equal(t, &aggregates.AmazonManagedStreamingForApacheKafka, aggregates.ForService(types.ServiceMSK))
	assert.Equal(t, &aggregates.ElasticLoadBalancing, aggregates.ForService(types.ServiceELB))
	assert.Equal(t, &aggregates.AmazonVPC, aggregates.ForService(types.ServiceVPC))
	assert.Equal(t, &aggregates.EC2Other, aggregates.ForService(types.ServiceEC2Other))
	assert.Equal(t, &aggregates.AWSCertificateManager, aggregates.ForService(types.ServiceAWSCertificateManager))
	assert.Nil(t, aggregates.ForService("Unknown Service"))
}

func assertHasUsageType(t *testing.T, svc types.ServiceCostAggregates, usageType string) {
	t.Helper()
	_, ok := svc.UnblendedCost[usageType]
	assert.True(t, ok, "expected usage type %q in UnblendedCost", usageType)
}

func assertServiceTotal(t *testing.T, svc types.ServiceCostAggregates, expectedTotal float64) {
	t.Helper()
	total, ok := svc.UnblendedCost["total"].(float64)
	require.True(t, ok, "expected 'total' key in UnblendedCost")
	assert.InDelta(t, expectedTotal, total, 0.001)
}
