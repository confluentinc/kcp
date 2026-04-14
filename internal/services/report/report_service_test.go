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

func TestProcessState_OSKMetricsPreservation(t *testing.T) {
	rs := NewReportService()

	t.Run("OSK cluster with Jolokia metrics preserved", func(t *testing.T) {
		// Create a test state with OSK cluster containing metrics
		state := types.State{
			OSKSources: &types.OSKSourcesState{
				Clusters: []types.OSKDiscoveredCluster{
					{
						ID:               "test-cluster",
						BootstrapServers: []string{"broker1:9092", "broker2:9092"},
						KafkaAdminClientInformation: types.KafkaAdminClientInformation{
							SaslMechanism: "SCRAM-SHA-256",
						},
						ClusterMetrics: &types.ProcessedClusterMetrics{
							Metrics: []types.ProcessedMetric{
								{
									Start: "2025-01-01T00:00:00Z",
									End:   "2025-01-01T00:01:00Z",
									Label: "BytesInPerSec",
									Value: ptr(100.5),
								},
							},
							Metadata: types.MetricMetadata{
								Period: 60,
							},
						},
						DiscoveredClients: []types.DiscoveredClient{},
						Metadata: types.OSKClusterMetadata{
							Environment: "test",
						},
					},
				},
			},
			KcpBuildInfo: types.KcpBuildInfo{
				Version: "1.0.0",
			},
		}

		processedState := rs.ProcessState(state)

		// Verify the state was processed
		require.Len(t, processedState.Sources, 1)
		source := processedState.Sources[0]
		require.Equal(t, types.SourceTypeOSK, source.Type)
		require.NotNil(t, source.OSKData)
		require.Len(t, source.OSKData.Clusters, 1)

		cluster := source.OSKData.Clusters[0]
		assert.Equal(t, "test-cluster", cluster.ID)

		// Verify metrics are preserved
		require.NotNil(t, cluster.ClusterMetrics)
		require.Len(t, cluster.ClusterMetrics.Metrics, 1)
		assert.Equal(t, "BytesInPerSec", cluster.ClusterMetrics.Metrics[0].Label)
		require.NotNil(t, cluster.ClusterMetrics.Metrics[0].Value)
		assert.InDelta(t, 100.5, *cluster.ClusterMetrics.Metrics[0].Value, 0.01)
	})

	t.Run("OSK cluster with Prometheus metrics preserved", func(t *testing.T) {
		state := types.State{
			OSKSources: &types.OSKSourcesState{
				Clusters: []types.OSKDiscoveredCluster{
					{
						ID:               "prometheus-cluster",
						BootstrapServers: []string{"broker1:9092"},
						KafkaAdminClientInformation: types.KafkaAdminClientInformation{
							SaslMechanism: "SCRAM-SHA-512",
						},
						ClusterMetrics: &types.ProcessedClusterMetrics{
							Metrics: []types.ProcessedMetric{
								{
									Start: "2025-01-01T00:00:00Z",
									End:   "2025-01-01T01:00:00Z",
									Label: "MessagesInPerSec",
									Value: ptr(250.0),
								},
								{
									Start: "2025-01-01T01:00:00Z",
									End:   "2025-01-01T02:00:00Z",
									Label: "MessagesInPerSec",
									Value: ptr(300.0),
								},
							},
							Metadata: types.MetricMetadata{
								Period: 3600,
							},
						},
						DiscoveredClients: []types.DiscoveredClient{},
						Metadata: types.OSKClusterMetadata{
							Environment: "prod",
						},
					},
				},
			},
			KcpBuildInfo: types.KcpBuildInfo{
				Version: "1.0.0",
			},
		}

		processedState := rs.ProcessState(state)

		require.Len(t, processedState.Sources, 1)
		cluster := processedState.Sources[0].OSKData.Clusters[0]

		// Verify metrics are preserved
		require.NotNil(t, cluster.ClusterMetrics)
		require.Len(t, cluster.ClusterMetrics.Metrics, 2)
		assert.Equal(t, "MessagesInPerSec", cluster.ClusterMetrics.Metrics[0].Label)
		assert.Equal(t, "MessagesInPerSec", cluster.ClusterMetrics.Metrics[1].Label)
		require.NotNil(t, cluster.ClusterMetrics.Metrics[0].Value)
		require.NotNil(t, cluster.ClusterMetrics.Metrics[1].Value)
		assert.InDelta(t, 250.0, *cluster.ClusterMetrics.Metrics[0].Value, 0.01)
		assert.InDelta(t, 300.0, *cluster.ClusterMetrics.Metrics[1].Value, 0.01)
	})

	t.Run("OSK cluster without metrics handled correctly", func(t *testing.T) {
		state := types.State{
			OSKSources: &types.OSKSourcesState{
				Clusters: []types.OSKDiscoveredCluster{
					{
						ID:               "no-metrics-cluster",
						BootstrapServers: []string{"broker1:9092"},
						KafkaAdminClientInformation: types.KafkaAdminClientInformation{
							SaslMechanism: "SCRAM-SHA-256",
						},
						ClusterMetrics: nil,
						DiscoveredClients: []types.DiscoveredClient{},
						Metadata: types.OSKClusterMetadata{
							Environment: "test",
						},
					},
				},
			},
			KcpBuildInfo: types.KcpBuildInfo{
				Version: "1.0.0",
			},
		}

		processedState := rs.ProcessState(state)

		require.Len(t, processedState.Sources, 1)
		cluster := processedState.Sources[0].OSKData.Clusters[0]

		// Metrics should be nil or empty
		assert.Nil(t, cluster.ClusterMetrics)
	})

	t.Run("ProcessState with multiple OSK clusters", func(t *testing.T) {
		state := types.State{
			OSKSources: &types.OSKSourcesState{
				Clusters: []types.OSKDiscoveredCluster{
					{
						ID:               "osk-cluster-1",
						BootstrapServers: []string{"broker1:9092"},
						KafkaAdminClientInformation: types.KafkaAdminClientInformation{
							SaslMechanism: "SCRAM-SHA-256",
						},
						ClusterMetrics: &types.ProcessedClusterMetrics{
							Metrics: []types.ProcessedMetric{
								{
									Start: "2025-01-01T00:00:00Z",
									End:   "2025-01-01T00:01:00Z",
									Label: "BytesInPerSec",
									Value: ptr(100.0),
								},
							},
							Metadata: types.MetricMetadata{
								Period: 60,
							},
						},
						DiscoveredClients: []types.DiscoveredClient{},
						Metadata: types.OSKClusterMetadata{
							Environment: "prod",
						},
					},
					{
						ID:               "osk-cluster-2",
						BootstrapServers: []string{"broker1:9092"},
						KafkaAdminClientInformation: types.KafkaAdminClientInformation{
							SaslMechanism: "SCRAM-SHA-512",
						},
						ClusterMetrics: &types.ProcessedClusterMetrics{
							Metrics: []types.ProcessedMetric{
								{
									Start: "2025-01-01T00:00:00Z",
									End:   "2025-01-01T00:01:00Z",
									Label: "MessagesInPerSec",
									Value: ptr(500.0),
								},
							},
							Metadata: types.MetricMetadata{
								Period: 60,
							},
						},
						DiscoveredClients: []types.DiscoveredClient{},
						Metadata: types.OSKClusterMetadata{
							Environment: "staging",
						},
					},
				},
			},
			KcpBuildInfo: types.KcpBuildInfo{
				Version: "1.0.0",
			},
		}

		processedState := rs.ProcessState(state)

		// Should have OSK source
		require.Len(t, processedState.Sources, 1)
		require.Equal(t, types.SourceTypeOSK, processedState.Sources[0].Type)
		require.NotNil(t, processedState.Sources[0].OSKData)

		// Verify both OSK clusters have metrics preserved
		require.Len(t, processedState.Sources[0].OSKData.Clusters, 2)

		cluster1 := processedState.Sources[0].OSKData.Clusters[0]
		require.NotNil(t, cluster1.ClusterMetrics)
		require.Len(t, cluster1.ClusterMetrics.Metrics, 1)
		assert.Equal(t, "BytesInPerSec", cluster1.ClusterMetrics.Metrics[0].Label)
		require.NotNil(t, cluster1.ClusterMetrics.Metrics[0].Value)
		assert.InDelta(t, 100.0, *cluster1.ClusterMetrics.Metrics[0].Value, 0.01)

		cluster2 := processedState.Sources[0].OSKData.Clusters[1]
		require.NotNil(t, cluster2.ClusterMetrics)
		require.Len(t, cluster2.ClusterMetrics.Metrics, 1)
		assert.Equal(t, "MessagesInPerSec", cluster2.ClusterMetrics.Metrics[0].Label)
		require.NotNil(t, cluster2.ClusterMetrics.Metrics[0].Value)
		assert.InDelta(t, 500.0, *cluster2.ClusterMetrics.Metrics[0].Value, 0.01)
	})
}

// ptr is a helper function to convert a float64 value to a pointer
func ptr(v float64) *float64 {
	return &v
}

func TestFilterClusterMetrics_SourceAware(t *testing.T) {
	rs := NewReportService()

	// Create a test state with both MSK and OSK clusters
	processedState := types.ProcessedState{
		Sources: []types.ProcessedSource{
			// MSK source with cluster
			{
				Type: types.SourceTypeMSK,
				MSKData: &types.ProcessedMSKSource{
					Regions: []types.ProcessedRegion{
						{
							Name: "us-east-1",
							Clusters: []types.ProcessedCluster{
								{
									Name: "test-msk-cluster",
									Arn:  "arn:aws:kafka:us-east-1:123456789012:cluster/test-msk-cluster/abc-123",
									ClusterMetrics: types.ProcessedClusterMetrics{
										Metrics: []types.ProcessedMetric{
											{
												Start: "2025-01-01T00:00:00Z",
												End:   "2025-01-01T00:01:00Z",
												Label: "BytesInPerSec",
												Value: ptr(500.0),
											},
										},
										Metadata: types.MetricMetadata{Period: 60},
									},
								},
							},
						},
					},
				},
			},
			// OSK source with cluster
			{
				Type: types.SourceTypeOSK,
				OSKData: &types.ProcessedOSKSource{
					Clusters: []types.ProcessedOSKCluster{
						{
							ID:               "my-osk-cluster",
							BootstrapServers: []string{"broker1:9092"},
							ClusterMetrics: &types.ProcessedClusterMetrics{
								Metrics: []types.ProcessedMetric{
									{
										Start: "2025-01-01T00:00:00Z",
										End:   "2025-01-01T00:01:00Z",
										Label: "MessagesInPerSec",
										Value: ptr(1000.0),
									},
								},
								Metadata: types.MetricMetadata{Period: 60},
							},
						},
					},
				},
			},
		},
	}

	t.Run("sourceType=msk finds MSK cluster", func(t *testing.T) {
		result, err := rs.FilterClusterMetrics(
			processedState,
			"arn:aws:kafka:us-east-1:123456789012:cluster/test-msk-cluster/abc-123",
			"msk",
			nil,
			nil,
		)

		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Equal(t, "us-east-1", result.Region)
		assert.Equal(t, "arn:aws:kafka:us-east-1:123456789012:cluster/test-msk-cluster/abc-123", result.ClusterArn)
		require.Len(t, result.Metrics, 1)
		assert.Equal(t, "BytesInPerSec", result.Metrics[0].Label)
		require.NotNil(t, result.Metrics[0].Value)
		assert.InDelta(t, 500.0, *result.Metrics[0].Value, 0.01)
	})

	t.Run("sourceType=osk finds OSK cluster", func(t *testing.T) {
		result, err := rs.FilterClusterMetrics(
			processedState,
			"my-osk-cluster",
			"osk",
			nil,
			nil,
		)

		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Equal(t, "", result.Region) // OSK clusters don't have regions
		assert.Equal(t, "my-osk-cluster", result.ClusterArn)
		require.Len(t, result.Metrics, 1)
		assert.Equal(t, "MessagesInPerSec", result.Metrics[0].Label)
		require.NotNil(t, result.Metrics[0].Value)
		assert.InDelta(t, 1000.0, *result.Metrics[0].Value, 0.01)
	})

	t.Run("sourceType=auto detects ARN and searches MSK", func(t *testing.T) {
		result, err := rs.FilterClusterMetrics(
			processedState,
			"arn:aws:kafka:us-east-1:123456789012:cluster/test-msk-cluster/abc-123",
			"auto",
			nil,
			nil,
		)

		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Equal(t, "us-east-1", result.Region)
		assert.Equal(t, "arn:aws:kafka:us-east-1:123456789012:cluster/test-msk-cluster/abc-123", result.ClusterArn)
	})

	t.Run("sourceType=auto detects non-ARN and searches OSK", func(t *testing.T) {
		result, err := rs.FilterClusterMetrics(
			processedState,
			"my-osk-cluster",
			"auto",
			nil,
			nil,
		)

		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Equal(t, "", result.Region)
		assert.Equal(t, "my-osk-cluster", result.ClusterArn)
	})

	t.Run("cluster not found in MSK sources shows clear error", func(t *testing.T) {
		result, err := rs.FilterClusterMetrics(
			processedState,
			"arn:aws:kafka:us-west-2:999999999999:cluster/nonexistent/xyz-789",
			"msk",
			nil,
			nil,
		)

		require.Error(t, err)
		assert.Nil(t, result)
		assert.Contains(t, err.Error(), "not found in MSK sources")
		assert.Contains(t, err.Error(), "arn:aws:kafka:us-west-2:999999999999:cluster/nonexistent/xyz-789")
	})

	t.Run("cluster not found in OSK sources shows clear error", func(t *testing.T) {
		result, err := rs.FilterClusterMetrics(
			processedState,
			"nonexistent-cluster",
			"osk",
			nil,
			nil,
		)

		require.Error(t, err)
		assert.Nil(t, result)
		assert.Contains(t, err.Error(), "not found in OSK sources")
		assert.Contains(t, err.Error(), "nonexistent-cluster")
	})

	t.Run("OSK cluster without metrics returns cluster with nil metrics", func(t *testing.T) {
		// Create state with OSK cluster that has no metrics
		stateWithoutMetrics := types.ProcessedState{
			Sources: []types.ProcessedSource{
				{
					Type: types.SourceTypeOSK,
					OSKData: &types.ProcessedOSKSource{
						Clusters: []types.ProcessedOSKCluster{
							{
								ID:               "no-metrics-cluster",
								BootstrapServers: []string{"broker1:9092"},
								ClusterMetrics:   nil, // No metrics
							},
						},
					},
				},
			},
		}

		result, err := rs.FilterClusterMetrics(
			stateWithoutMetrics,
			"no-metrics-cluster",
			"osk",
			nil,
			nil,
		)

		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Equal(t, "no-metrics-cluster", result.ClusterArn)
		assert.Nil(t, result.Metrics)
		assert.Nil(t, result.Aggregates)
	})

	t.Run("invalid source type returns error", func(t *testing.T) {
		result, err := rs.FilterClusterMetrics(
			processedState,
			"some-cluster",
			"invalid",
			nil,
			nil,
		)

		require.Error(t, err)
		assert.Nil(t, result)
		assert.Contains(t, err.Error(), "invalid source type")
		assert.Contains(t, err.Error(), "invalid")
	})
}
