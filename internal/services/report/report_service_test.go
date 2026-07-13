package report

import (
	"errors"
	"testing"
	"time"

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
		costs := []ProcessedCost{
			{
				Start: "2025-01-01", End: "2025-01-02",
				Service: types.ServiceMSK, UsageType: "USE1-Kafka.m5.large",
				Values: ProcessedCostBreakdown{UnblendedCost: 10.0, BlendedCost: 10.0},
			},
			{
				Start: "2025-01-01", End: "2025-01-02",
				Service: types.ServiceELB, UsageType: "USE1-LoadBalancerUsage",
				Values: ProcessedCostBreakdown{UnblendedCost: 5.0, BlendedCost: 5.0},
			},
			{
				Start: "2025-01-01", End: "2025-01-02",
				Service: types.ServiceVPC, UsageType: "USE1-VpcEndpoint-Hours",
				Values: ProcessedCostBreakdown{UnblendedCost: 3.0, BlendedCost: 3.0},
			},
			{
				Start: "2025-01-01", End: "2025-01-02",
				Service: types.ServiceEC2Other, UsageType: "USE1-NatGateway-Hours",
				Values: ProcessedCostBreakdown{UnblendedCost: 2.0, BlendedCost: 2.0},
			},
			{
				Start: "2025-01-01", End: "2025-01-02",
				Service: types.ServiceAWSCertificateManager, UsageType: "USE1-FreePrivateCA",
				Values: ProcessedCostBreakdown{UnblendedCost: 1.0, BlendedCost: 1.0},
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
		costs := []ProcessedCost{
			{
				Start: "2025-01-01", End: "2025-01-02",
				Service: types.ServiceELB, UsageType: "USE1-LoadBalancerUsage",
				Values: ProcessedCostBreakdown{UnblendedCost: 5.0},
			},
			{
				Start: "2025-01-02", End: "2025-01-03",
				Service: types.ServiceELB, UsageType: "USE1-LoadBalancerUsage",
				Values: ProcessedCostBreakdown{UnblendedCost: 7.0},
			},
		}

		aggregates := rs.calculateCostAggregates(costs)

		// Sum should be 12.0
		agg, ok := aggregates.ElasticLoadBalancing.UnblendedCost["USE1-LoadBalancerUsage"].(CostAggregate)
		require.True(t, ok)
		require.NotNil(t, agg.Sum)
		assert.InDelta(t, 12.0, *agg.Sum, 0.001)
		assert.InDelta(t, 6.0, *agg.Average, 0.001)
		assert.InDelta(t, 5.0, *agg.Minimum, 0.001)
		assert.InDelta(t, 7.0, *agg.Maximum, 0.001)
	})
}

func TestForService(t *testing.T) {
	aggregates := NewProcessedAggregates()

	assert.Equal(t, &aggregates.AmazonManagedStreamingForApacheKafka, aggregates.ForService(types.ServiceMSK))
	assert.Equal(t, &aggregates.ElasticLoadBalancing, aggregates.ForService(types.ServiceELB))
	assert.Equal(t, &aggregates.AmazonVPC, aggregates.ForService(types.ServiceVPC))
	assert.Equal(t, &aggregates.EC2Other, aggregates.ForService(types.ServiceEC2Other))
	assert.Equal(t, &aggregates.AWSCertificateManager, aggregates.ForService(types.ServiceAWSCertificateManager))
	assert.Nil(t, aggregates.ForService("Unknown Service"))
}

func assertHasUsageType(t *testing.T, svc ServiceCostAggregates, usageType string) {
	t.Helper()
	_, ok := svc.UnblendedCost[usageType]
	assert.True(t, ok, "expected usage type %q in UnblendedCost", usageType)
}

func assertServiceTotal(t *testing.T, svc ServiceCostAggregates, expectedTotal float64) {
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
						ClusterMetrics:    nil,
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
	processedState := ProcessedState{
		Sources: []ProcessedSource{
			// MSK source with cluster
			{
				Type: types.SourceTypeMSK,
				MSKData: &ProcessedMSKSource{
					Regions: []ProcessedRegion{
						{
							Name: "us-east-1",
							Clusters: []ProcessedCluster{
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
				OSKData: &ProcessedOSKSource{
					Clusters: []ProcessedOSKCluster{
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

	t.Run("empty sourceType detects ARN and searches MSK", func(t *testing.T) {
		result, err := rs.FilterClusterMetrics(
			processedState,
			"arn:aws:kafka:us-east-1:123456789012:cluster/test-msk-cluster/abc-123",
			"",
			nil,
			nil,
		)

		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Equal(t, "us-east-1", result.Region)
		assert.Equal(t, "arn:aws:kafka:us-east-1:123456789012:cluster/test-msk-cluster/abc-123", result.ClusterArn)
	})

	t.Run("empty sourceType detects non-ARN and searches OSK", func(t *testing.T) {
		result, err := rs.FilterClusterMetrics(
			processedState,
			"my-osk-cluster",
			"",
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
		assert.Contains(t, err.Error(), "not found in Apache Kafka sources")
		assert.Contains(t, err.Error(), "nonexistent-cluster")
	})

	t.Run("OSK cluster without metrics returns cluster with nil metrics", func(t *testing.T) {
		// Create state with OSK cluster that has no metrics
		stateWithoutMetrics := ProcessedState{
			Sources: []ProcessedSource{
				{
					Type: types.SourceTypeOSK,
					OSKData: &ProcessedOSKSource{
						Clusters: []ProcessedOSKCluster{
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

func TestFilterOSKClusterMetrics_PopulatesMetadata(t *testing.T) {
	rs := NewReportService()

	t.Run("OSK cluster with metadata populates Environment and Location", func(t *testing.T) {
		processedState := ProcessedState{
			Sources: []ProcessedSource{
				{
					Type: types.SourceTypeOSK,
					OSKData: &ProcessedOSKSource{
						Clusters: []ProcessedOSKCluster{
							{
								ID:               "prod-cluster",
								BootstrapServers: []string{"broker1:9092"},
								ClusterMetrics: &types.ProcessedClusterMetrics{
									Metrics: []types.ProcessedMetric{
										{
											Start: "2025-01-01T00:00:00Z",
											End:   "2025-01-01T00:01:00Z",
											Label: "BytesInPerSec",
											Value: ptr(100.0),
										},
									},
									Metadata: types.MetricMetadata{Period: 60},
								},
								Metadata: types.OSKClusterMetadata{
									Environment: "production",
									Location:    "datacenter-1",
								},
							},
						},
					},
				},
			},
		}

		result, err := rs.FilterClusterMetrics(processedState, "prod-cluster", "osk", nil, nil)

		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Equal(t, "production", result.Environment)
		assert.Equal(t, "datacenter-1", result.Location)
	})

	t.Run("OSK cluster without metadata fields has empty Environment and Location", func(t *testing.T) {
		processedState := ProcessedState{
			Sources: []ProcessedSource{
				{
					Type: types.SourceTypeOSK,
					OSKData: &ProcessedOSKSource{
						Clusters: []ProcessedOSKCluster{
							{
								ID:               "no-metadata-cluster",
								BootstrapServers: []string{"broker1:9092"},
								ClusterMetrics: &types.ProcessedClusterMetrics{
									Metrics:  []types.ProcessedMetric{},
									Metadata: types.MetricMetadata{Period: 60},
								},
								Metadata: types.OSKClusterMetadata{
									// Environment and Location not set
								},
							},
						},
					},
				},
			},
		}

		result, err := rs.FilterClusterMetrics(processedState, "no-metadata-cluster", "osk", nil, nil)

		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Equal(t, "", result.Environment)
		assert.Equal(t, "", result.Location)
	})

	t.Run("OSK cluster with nil metrics still populates metadata", func(t *testing.T) {
		processedState := ProcessedState{
			Sources: []ProcessedSource{
				{
					Type: types.SourceTypeOSK,
					OSKData: &ProcessedOSKSource{
						Clusters: []ProcessedOSKCluster{
							{
								ID:               "no-metrics-cluster",
								BootstrapServers: []string{"broker1:9092"},
								ClusterMetrics:   nil,
								Metadata: types.OSKClusterMetadata{
									Environment: "staging",
									Location:    "datacenter-2",
								},
							},
						},
					},
				},
			},
		}

		result, err := rs.FilterClusterMetrics(processedState, "no-metrics-cluster", "osk", nil, nil)

		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Equal(t, "staging", result.Environment)
		assert.Equal(t, "datacenter-2", result.Location)
		assert.Nil(t, result.Metrics)
		assert.Nil(t, result.Aggregates)
	})

	t.Run("MSK cluster has empty Environment and Location", func(t *testing.T) {
		processedState := ProcessedState{
			Sources: []ProcessedSource{
				{
					Type: types.SourceTypeMSK,
					MSKData: &ProcessedMSKSource{
						Regions: []ProcessedRegion{
							{
								Name: "us-east-1",
								Clusters: []ProcessedCluster{
									{
										Name: "msk-cluster",
										Arn:  "arn:aws:kafka:us-east-1:123456789012:cluster/msk-cluster/abc",
										ClusterMetrics: types.ProcessedClusterMetrics{
											Metrics:  []types.ProcessedMetric{},
											Metadata: types.MetricMetadata{Period: 300},
										},
									},
								},
							},
						},
					},
				},
			},
		}

		result, err := rs.FilterClusterMetrics(
			processedState,
			"arn:aws:kafka:us-east-1:123456789012:cluster/msk-cluster/abc",
			"msk",
			nil,
			nil,
		)

		require.NoError(t, err)
		require.NotNil(t, result)
		// MSK clusters should have empty OSK fields
		assert.Equal(t, "", result.Environment)
		assert.Equal(t, "", result.Location)
	})
}

func TestFilterClusterMetrics_DateFiltering(t *testing.T) {
	rs := NewReportService()

	makeTime := func(s string) *time.Time {
		t, _ := time.Parse(time.RFC3339, s)
		return &t
	}

	oskState := ProcessedState{
		Sources: []ProcessedSource{
			{
				Type: types.SourceTypeOSK,
				OSKData: &ProcessedOSKSource{
					Clusters: []ProcessedOSKCluster{
						{
							ID:               "date-test-cluster",
							BootstrapServers: []string{"broker1:9092"},
							ClusterMetrics: &types.ProcessedClusterMetrics{
								Metrics: []types.ProcessedMetric{
									{Start: "2025-01-01T00:00:00Z", End: "2025-01-01T00:01:00Z", Label: "BytesInPerSec", Value: ptr(100.0)},
									{Start: "2025-01-02T00:00:00Z", End: "2025-01-02T00:01:00Z", Label: "BytesInPerSec", Value: ptr(200.0)},
									{Start: "2025-01-03T00:00:00Z", End: "2025-01-03T00:01:00Z", Label: "BytesInPerSec", Value: ptr(300.0)},
									{Start: "2025-01-04T00:00:00Z", End: "2025-01-04T00:01:00Z", Label: "BytesInPerSec", Value: ptr(400.0)},
									{Start: "2025-01-05T00:00:00Z", End: "2025-01-05T00:01:00Z", Label: "BytesInPerSec", Value: ptr(500.0)},
									// RFC3339 with timezone offset (Jolokia/Prometheus output)
									{Start: "2025-01-06T01:00:00+01:00", End: "2025-01-06T01:01:00+01:00", Label: "BytesInPerSec", Value: ptr(600.0)},
									// Unparseable timestamp — should be silently skipped
									{Start: "not-a-date", End: "not-a-date", Label: "BytesInPerSec", Value: ptr(999.0)},
								},
								Metadata: types.MetricMetadata{Period: 60},
							},
						},
					},
				},
			},
		},
	}

	t.Run("nil start and end returns all parseable metrics", func(t *testing.T) {
		result, err := rs.FilterClusterMetrics(oskState, "date-test-cluster", "osk", nil, nil)
		require.NoError(t, err)
		assert.Len(t, result.Metrics, 7)
	})

	t.Run("start filter excludes earlier metrics", func(t *testing.T) {
		result, err := rs.FilterClusterMetrics(oskState, "date-test-cluster", "osk", makeTime("2025-01-03T00:00:00Z"), nil)
		require.NoError(t, err)
		assert.Len(t, result.Metrics, 4)
		assert.InDelta(t, 300.0, *result.Metrics[0].Value, 0.01)
	})

	t.Run("end filter excludes later metrics", func(t *testing.T) {
		result, err := rs.FilterClusterMetrics(oskState, "date-test-cluster", "osk", nil, makeTime("2025-01-03T00:00:00Z"))
		require.NoError(t, err)
		assert.Len(t, result.Metrics, 3)
		assert.InDelta(t, 300.0, *result.Metrics[2].Value, 0.01)
	})

	t.Run("start and end produce a subset", func(t *testing.T) {
		result, err := rs.FilterClusterMetrics(
			oskState, "date-test-cluster", "osk",
			makeTime("2025-01-02T00:00:00Z"),
			makeTime("2025-01-04T00:00:00Z"),
		)
		require.NoError(t, err)
		assert.Len(t, result.Metrics, 3)
		assert.InDelta(t, 200.0, *result.Metrics[0].Value, 0.01)
		assert.InDelta(t, 400.0, *result.Metrics[2].Value, 0.01)
	})

	t.Run("inclusive start boundary", func(t *testing.T) {
		result, err := rs.FilterClusterMetrics(
			oskState, "date-test-cluster", "osk",
			makeTime("2025-01-03T00:00:00Z"),
			makeTime("2025-01-03T00:00:00Z"),
		)
		require.NoError(t, err)
		assert.Len(t, result.Metrics, 1)
		assert.InDelta(t, 300.0, *result.Metrics[0].Value, 0.01)
	})

	t.Run("RFC3339 with timezone offset is parsed correctly", func(t *testing.T) {
		result, err := rs.FilterClusterMetrics(
			oskState, "date-test-cluster", "osk",
			makeTime("2025-01-06T00:00:00Z"),
			makeTime("2025-01-06T01:00:00Z"),
		)
		require.NoError(t, err)
		assert.Len(t, result.Metrics, 1)
		assert.InDelta(t, 600.0, *result.Metrics[0].Value, 0.01)
	})

	t.Run("unparseable timestamps are silently skipped", func(t *testing.T) {
		result, err := rs.FilterClusterMetrics(
			oskState, "date-test-cluster", "osk",
			makeTime("2025-01-01T00:00:00Z"),
			makeTime("2025-01-06T02:00:00Z"),
		)
		require.NoError(t, err)
		// 6 valid metrics (the "not-a-date" one is skipped)
		assert.Len(t, result.Metrics, 6)
	})

	t.Run("aggregates are recalculated from filtered subset", func(t *testing.T) {
		result, err := rs.FilterClusterMetrics(
			oskState, "date-test-cluster", "osk",
			makeTime("2025-01-02T00:00:00Z"),
			makeTime("2025-01-04T00:00:00Z"),
		)
		require.NoError(t, err)
		require.NotNil(t, result.Aggregates)
		agg, ok := result.Aggregates["BytesInPerSec"]
		require.True(t, ok)
		require.NotNil(t, agg.Minimum)
		require.NotNil(t, agg.Average)
		require.NotNil(t, agg.Maximum)
		assert.InDelta(t, 200.0, *agg.Minimum, 0.01)
		assert.InDelta(t, 300.0, *agg.Average, 0.01)
		assert.InDelta(t, 400.0, *agg.Maximum, 0.01)
	})
}

func TestProcessState_CarriesStateMetadata(t *testing.T) {
	rs := NewReportService()
	state := types.State{
		SchemaVersion: 1,
		KcpBuildInfo:  types.KcpBuildInfo{Version: "0.8.5", Commit: "abc1234", Date: "2026-06-17"},
		Timestamp:     time.Date(2026, 5, 14, 0, 0, 0, 0, time.UTC),
		UpdatedAt:     time.Date(2026, 6, 26, 10, 30, 0, 0, time.UTC),
		UpgradedFrom:  "kcp_build_info.version=0.7.3",
	}

	got := rs.ProcessState(state)

	if got.SchemaVersion != 1 {
		t.Errorf("SchemaVersion = %d, want 1", got.SchemaVersion)
	}
	if got.UpdatedAt == nil || !got.UpdatedAt.Equal(state.UpdatedAt) {
		t.Errorf("UpdatedAt = %v, want %v", got.UpdatedAt, state.UpdatedAt)
	}
	if got.UpgradedFrom != "kcp_build_info.version=0.7.3" {
		t.Errorf("UpgradedFrom = %q, want kcp_build_info.version=0.7.3", got.UpgradedFrom)
	}
}

func TestProcessState_LegacyZeroMetadataOmitsUpdatedAt(t *testing.T) {
	rs := NewReportService()

	// Create an empty state (legacy state file with zero metadata)
	state := types.State{}

	got := rs.ProcessState(state)

	// Verify schema version is zero (legacy)
	if got.SchemaVersion != 0 {
		t.Errorf("SchemaVersion = %d, want 0 (legacy)", got.SchemaVersion)
	}

	// Verify updated_at is nil so it will be omitted from JSON
	if got.UpdatedAt != nil {
		t.Errorf("UpdatedAt = %v, want nil for zero/legacy state", got.UpdatedAt)
	}

	// Verify upgraded_from is empty (legacy)
	if got.UpgradedFrom != "" {
		t.Errorf("UpgradedFrom = %q, want empty string (legacy)", got.UpgradedFrom)
	}
}

func TestCalculateMetricsAggregates(t *testing.T) {
	makeMetrics := func(label string, values []float64) []types.ProcessedMetric {
		out := make([]types.ProcessedMetric, len(values))
		for i, v := range values {
			v := v
			out[i] = types.ProcessedMetric{Label: label, Value: &v}
		}
		return out
	}

	t.Run("computes P95 P99 against fixed 1..100 vector (nearest rank)", func(t *testing.T) {
		// unsorted on input to prove sort happens
		values := make([]float64, 100)
		for i := 0; i < 100; i++ {
			values[i] = float64(100 - i)
		}
		aggs := CalculateMetricsAggregates(makeMetrics("BytesInPerSec", values))
		agg, ok := aggs["BytesInPerSec"]
		require.True(t, ok)
		// ceil(100*0.95)-1 = 94 -> sorted[94] = 95
		// ceil(100*0.99)-1 = 98 -> sorted[98] = 99
		assert.InDelta(t, 95.0, *agg.P95, 0.0001)
		assert.InDelta(t, 99.0, *agg.P99, 0.0001)
		assert.Equal(t, 100, agg.Count)
	})

	t.Run("P95 and P99 are distinct at N=20", func(t *testing.T) {
		// Regression: the previous int(N*p) implementation collapsed
		// P95 == P99 at N=20 (both indexed [19]). Nearest-rank gives
		// ceil(20*0.95)-1=18 (=19) and ceil(20*0.99)-1=19 (=20).
		values := make([]float64, 20)
		for i := 0; i < 20; i++ {
			values[i] = float64(i + 1)
		}
		aggs := CalculateMetricsAggregates(makeMetrics("M", values))
		assert.InDelta(t, 19.0, *aggs["M"].P95, 0.0001)
		assert.InDelta(t, 20.0, *aggs["M"].P99, 0.0001)
	})

	t.Run("missing value at the rank doesn't crash", func(t *testing.T) {
		// 1..100 minus 95 → N=99, ceil(99*0.95)-1=94, sorted[94]=96
		// (indexes 0..93 hold 1..94; index 94 holds 96 because 95 is absent).
		values := []float64{}
		for i := 1; i <= 100; i++ {
			if i == 95 {
				continue
			}
			values = append(values, float64(i))
		}
		aggs := CalculateMetricsAggregates(makeMetrics("M", values))
		require.Equal(t, 99, aggs["M"].Count)
		assert.InDelta(t, 96.0, *aggs["M"].P95, 0.0001)
	})
}

func TestFilterConnectMetrics(t *testing.T) {
	rs := NewReportService()

	oskConnectMetrics := &types.ConnectClusterMetrics{
		Metrics: []types.ProcessedMetric{
			{Start: "2025-01-01T00:00:00Z", End: "2025-01-01T00:01:00Z", Label: "connector-count", Value: ptr(2.0)},
			{Start: "2025-01-01T00:01:00Z", End: "2025-01-01T00:02:00Z", Label: "connector-count", Value: ptr(2.0)},
			{Start: "2025-01-02T00:00:00Z", End: "2025-01-02T00:01:00Z", Label: "connector-count", Value: ptr(3.0)},
		},
		Metadata: types.ConnectMetricMetadata{Period: 60, MetricsSource: types.MetricBackendJolokia},
		QueryInfo: []types.MetricQueryInfo{
			{MetricName: "connector-count", Statistic: "Point-in-time value (per worker)"},
		},
	}

	mskConnectMetrics := &types.ConnectClusterMetrics{
		Metrics: []types.ProcessedMetric{
			{Start: "2025-01-01T00:00:00Z", End: "2025-01-01T00:01:00Z", Label: "connector-count", Value: ptr(5.0)},
			{Start: "2025-01-02T00:00:00Z", End: "2025-01-02T00:01:00Z", Label: "connector-count", Value: ptr(7.0)},
		},
		Metadata: types.ConnectMetricMetadata{Period: 60, MetricsSource: types.MetricBackendPrometheus},
		QueryInfo: []types.MetricQueryInfo{
			{MetricName: "connector-count", Statistic: "Point-in-time value (per worker)"},
		},
	}

	const mskArn = "arn:aws:kafka:us-east-1:123456789012:cluster/msk-kafka/def-456"

	// State carries BOTH an OSK and an MSK cluster, each with Connect metrics, so the
	// source-type branches and cross-source isolation can be exercised against one fixture.
	stateWithConnect := ProcessedState{
		Sources: []ProcessedSource{
			{
				Type: types.SourceTypeOSK,
				OSKData: &ProcessedOSKSource{
					Clusters: []ProcessedOSKCluster{
						{
							ID: "osk-kafka",
							KafkaAdminClientInformation: types.KafkaAdminClientInformation{
								SelfManagedConnectors: &types.SelfManagedConnectors{
									Connectors: []types.SelfManagedConnector{
										{Name: "test-connector"},
									},
									Metrics: oskConnectMetrics,
								},
							},
							Metadata: types.OSKClusterMetadata{
								Environment: "test",
								Location:    "local",
							},
						},
					},
				},
			},
			{
				Type: types.SourceTypeMSK,
				MSKData: &ProcessedMSKSource{
					Regions: []ProcessedRegion{
						{
							Name: "us-east-1",
							Clusters: []ProcessedCluster{
								{
									Name: "msk-kafka",
									Arn:  mskArn,
									KafkaAdminClientInformation: types.KafkaAdminClientInformation{
										SelfManagedConnectors: &types.SelfManagedConnectors{
											Connectors: []types.SelfManagedConnector{
												{Name: "msk-connector"},
											},
											Metrics: mskConnectMetrics,
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	stateNoConnect := ProcessedState{
		Sources: []ProcessedSource{
			{
				Type: types.SourceTypeOSK,
				OSKData: &ProcessedOSKSource{
					Clusters: []ProcessedOSKCluster{
						{
							ID: "osk-kafka-no-connect",
						},
					},
				},
			},
		},
	}

	t.Run("returns Connect metrics for existing OSK cluster", func(t *testing.T) {
		result, err := rs.FilterConnectMetrics(stateWithConnect, "osk-kafka", "osk", "self-managed", nil, nil)
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Len(t, result.Metrics, 3)
		// The clean Connect shape carries the metadata through; broker-only
		// ClusterArn/Environment/Location are intentionally absent from the type.
		assert.Equal(t, int32(60), result.Metadata.Period, "period carried through")
		assert.Equal(t, types.MetricBackendJolokia, result.Metadata.MetricsSource, "metrics_source carried through")
		assert.Len(t, result.QueryInfo, 1)
		assert.Equal(t, "connector-count", result.QueryInfo[0].MetricName)
	})

	t.Run("returns Connect metrics for existing MSK cluster", func(t *testing.T) {
		result, err := rs.FilterConnectMetrics(stateWithConnect, mskArn, "msk", "self-managed", nil, nil)
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Len(t, result.Metrics, 2)
		assert.Equal(t, types.MetricBackendPrometheus, result.Metadata.MetricsSource, "MSK metrics_source carried through")
	})

	t.Run("filters by date range", func(t *testing.T) {
		start := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
		end := time.Date(2025, 1, 1, 23, 59, 59, 0, time.UTC)
		result, err := rs.FilterConnectMetrics(stateWithConnect, "osk-kafka", "osk", "self-managed", &start, &end)
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Len(t, result.Metrics, 2) // only Jan 1 metrics
	})

	t.Run("cluster not found returns error", func(t *testing.T) {
		_, err := rs.FilterConnectMetrics(stateWithConnect, "nonexistent", "osk", "self-managed", nil, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})

	t.Run("cluster without self-managed connectors signals never-collected", func(t *testing.T) {
		_, err := rs.FilterConnectMetrics(stateNoConnect, "osk-kafka-no-connect", "osk", "self-managed", nil, nil)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrNoConnectMetricsCollected)
	})

	t.Run("collected metrics outside the date range return empty without error", func(t *testing.T) {
		// Distinct from "never collected": the cluster HAS Connect metrics, the
		// selected window just excludes them all. This must NOT surface as the
		// never-collected sentinel — it is a valid empty result the API serves as a
		// 200, so the user sees an empty chart rather than a "run a scan" message.
		start := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
		end := time.Date(2030, 1, 2, 0, 0, 0, 0, time.UTC)
		result, err := rs.FilterConnectMetrics(stateWithConnect, "osk-kafka", "osk", "self-managed", &start, &end)
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Empty(t, result.Metrics)
		assert.NotErrorIs(t, err, ErrNoConnectMetricsCollected)
	})

	t.Run("case-insensitive cluster ID match", func(t *testing.T) {
		result, err := rs.FilterConnectMetrics(stateWithConnect, "OSK-KAFKA", "osk", "self-managed", nil, nil)
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Len(t, result.Metrics, 3)
	})

	// Abuse case: cross-source-type bleed. A cluster identifier that exists under one
	// source type must never resolve when queried under the other source type.
	t.Run("OSK cluster id requested as msk does not bleed", func(t *testing.T) {
		_, err := rs.FilterConnectMetrics(stateWithConnect, "osk-kafka", "msk", "self-managed", nil, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})

	t.Run("MSK cluster arn requested as osk does not bleed", func(t *testing.T) {
		_, err := rs.FilterConnectMetrics(stateWithConnect, mskArn, "osk", "self-managed", nil, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})

	// Abuse case: an unknown source type is rejected, not silently defaulted to a source.
	t.Run("unknown source type returns error", func(t *testing.T) {
		_, err := rs.FilterConnectMetrics(stateWithConnect, "osk-kafka", "bogus", "self-managed", nil, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "source type")
	})

	// MSK-branch coverage symmetric to the OSK cases above: not-found, no-connectors,
	// and date filtering must each be exercised through the MSK lookup path.
	t.Run("MSK cluster not found returns error", func(t *testing.T) {
		_, err := rs.FilterConnectMetrics(stateWithConnect, "arn:aws:kafka:us-east-1:000000000000:cluster/nope/zzz", "msk", "self-managed", nil, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})

	t.Run("MSK cluster without self-managed connectors signals never-collected", func(t *testing.T) {
		const arn = "arn:aws:kafka:us-east-1:123456789012:cluster/msk-bare/ghi-789"
		stateMSKNoConnect := ProcessedState{
			Sources: []ProcessedSource{
				{
					Type: types.SourceTypeMSK,
					MSKData: &ProcessedMSKSource{
						Regions: []ProcessedRegion{
							{
								Name:     "us-east-1",
								Clusters: []ProcessedCluster{{Name: "msk-bare", Arn: arn}},
							},
						},
					},
				},
			},
		}
		_, err := rs.FilterConnectMetrics(stateMSKNoConnect, arn, "msk", "self-managed", nil, nil)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrNoConnectMetricsCollected)
	})

	t.Run("filters by date range on the MSK path", func(t *testing.T) {
		start := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
		end := time.Date(2025, 1, 1, 23, 59, 59, 0, time.UTC)
		result, err := rs.FilterConnectMetrics(stateWithConnect, mskArn, "msk", "self-managed", &start, &end)
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Len(t, result.Metrics, 1) // only the Jan 1 MSK metric
	})
}

func TestFilterConnectMetrics_Managed_MSK(t *testing.T) {
	rs := &ReportService{}
	arn := "arn:aws:kafka:us-east-1:000000000000:cluster/c/uuid"
	val := 2.0
	state := types.State{MSKSources: &types.MSKSourcesState{Regions: []types.DiscoveredRegion{{
		Name: "us-east-1",
		Clusters: []types.DiscoveredCluster{{
			Arn: arn,
			AWSClientInformation: types.AWSClientInformation{
				ConnectorMetrics: &types.ConnectClusterMetrics{
					Metadata: types.ConnectMetricMetadata{MetricsSource: types.MetricBackendCloudWatch},
					Metrics:  []types.ProcessedMetric{{Label: "BytesInPerSec (x)", Value: &val, Start: "2026-01-01T00:00:00Z", End: "2026-01-01T00:04:59Z"}},
				},
			},
		}},
	}}}}
	ps := rs.ProcessState(state)

	got, err := rs.FilterConnectMetrics(ps, arn, "msk", "managed", nil, nil)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, types.MetricBackendCloudWatch, got.Metadata.MetricsSource)
	require.Len(t, got.Metrics, 1)
}

func TestFilterConnectMetrics_Managed_ExistingClusterNilMetrics_SignalsNeverCollected(t *testing.T) {
	// The MSK cluster exists (present in state by ARN) but ConnectorMetrics was
	// never populated (e.g. scanned without --metrics-granularity). This must
	// return the never-collected sentinel, not a not-found error, so the API
	// layer shows the "run a scan" hint.
	rs := &ReportService{}
	arn := "arn:aws:kafka:us-east-1:000000000000:cluster/c/uuid"
	state := types.State{MSKSources: &types.MSKSourcesState{Regions: []types.DiscoveredRegion{{
		Name: "us-east-1",
		Clusters: []types.DiscoveredCluster{{
			Arn:                  arn,
			AWSClientInformation: types.AWSClientInformation{ConnectorMetrics: nil},
		}},
	}}}}
	ps := rs.ProcessState(state)

	_, err := rs.FilterConnectMetrics(ps, arn, "msk", "managed", nil, nil)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrNoConnectMetricsCollected))
}

func TestFilterConnectMetrics_Managed_NonexistentCluster_ReturnsNotFound(t *testing.T) {
	// A nonexistent MSK ARN must return a plain not-found error, distinct from
	// the never-collected sentinel, so the "run a scan" hint isn't shown for a
	// cluster that was never discovered in the first place.
	rs := &ReportService{}
	state := types.State{MSKSources: &types.MSKSourcesState{Regions: []types.DiscoveredRegion{{
		Name: "us-east-1",
		Clusters: []types.DiscoveredCluster{{
			Arn: "arn:aws:kafka:us-east-1:000000000000:cluster/other/uuid",
			AWSClientInformation: types.AWSClientInformation{
				ConnectorMetrics: &types.ConnectClusterMetrics{},
			},
		}},
	}}}}
	ps := rs.ProcessState(state)

	_, err := rs.FilterConnectMetrics(ps, "arn:aws:kafka:us-east-1:000000000000:cluster/nonexistent/uuid", "msk", "managed", nil, nil)
	require.Error(t, err)
	assert.False(t, errors.Is(err, ErrNoConnectMetricsCollected))
	assert.Contains(t, err.Error(), "not found")
}

func TestFilterConnectMetrics_Managed_OSKIsError(t *testing.T) {
	rs := &ReportService{}
	state := types.State{OSKSources: &types.OSKSourcesState{Clusters: []types.OSKDiscoveredCluster{{ID: "osk-1"}}}}
	ps := rs.ProcessState(state)
	_, err := rs.FilterConnectMetrics(ps, "osk-1", "osk", "managed", nil, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "managed")
}

func TestFilterConnectMetrics_SelfManaged_DefaultUnchanged(t *testing.T) {
	// Existing self-managed behavior still works when kind == "self-managed".
	rs := &ReportService{}
	arn := "arn:aws:kafka:us-east-1:000000000000:cluster/c/uuid"
	val := 1.0
	state := types.State{MSKSources: &types.MSKSourcesState{Regions: []types.DiscoveredRegion{{
		Name: "us-east-1",
		Clusters: []types.DiscoveredCluster{{
			Arn: arn,
			KafkaAdminClientInformation: types.KafkaAdminClientInformation{
				SelfManagedConnectors: &types.SelfManagedConnectors{
					Metrics: &types.ConnectClusterMetrics{Metrics: []types.ProcessedMetric{{Label: "task-count", Value: &val}}},
				},
			},
		}},
	}}}}
	ps := rs.ProcessState(state)
	got, err := rs.FilterConnectMetrics(ps, arn, "msk", "self-managed", nil, nil)
	require.NoError(t, err)
	require.Len(t, got.Metrics, 1)
}
