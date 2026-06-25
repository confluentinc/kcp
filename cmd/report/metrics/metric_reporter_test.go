package metrics

import (
	"strings"
	"testing"
	"time"

	"github.com/confluentinc/kcp/internal/services/report"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// MockReportService is a mock implementation of ReportService
type MockReportService struct {
	mock.Mock
}

func (m *MockReportService) ProcessState(state types.State) report.ProcessedState {
	args := m.Called(state)
	return args.Get(0).(report.ProcessedState)
}

func (m *MockReportService) FilterClusterMetrics(processedState report.ProcessedState, clusterArn string, sourceType string, startTime, endTime *time.Time) (*types.ProcessedClusterMetrics, error) {
	args := m.Called(processedState, clusterArn, sourceType, startTime, endTime)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*types.ProcessedClusterMetrics), args.Error(1)
}

func TestDetermineReportTitle(t *testing.T) {
	startTime := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	endTime := time.Date(2025, 1, 31, 0, 0, 0, 0, time.UTC)

	t.Run("Only MSK clusters", func(t *testing.T) {
		reporter := NewMetricReporter(nil, MetricReporterOpts{
			ClusterIds: []string{
				"arn:aws:kafka:us-east-1:123456789012:cluster/test-msk-cluster/abc-123",
			},
			State:     &types.State{},
			StartDate: &startTime,
			EndDate:   &endTime,
		})

		clusters := []types.ProcessedClusterMetrics{
			{
				ClusterArn: "arn:aws:kafka:us-east-1:123456789012:cluster/test-msk-cluster/abc-123",
			},
		}

		title := reporter.determineReportTitle(clusters)
		assert.Equal(t, "AWS MSK Metrics Report", title)
	})

	t.Run("Only OSK clusters", func(t *testing.T) {
		reporter := NewMetricReporter(nil, MetricReporterOpts{
			ClusterIds: []string{"osk-cluster-1"},
			State:      &types.State{},
			StartDate:  &startTime,
			EndDate:    &endTime,
		})

		clusters := []types.ProcessedClusterMetrics{
			{
				ClusterArn: "osk-cluster-1",
			},
		}

		title := reporter.determineReportTitle(clusters)
		assert.Equal(t, "Apache Kafka Metrics Report", title)
	})

	t.Run("Mixed MSK and OSK clusters", func(t *testing.T) {
		reporter := NewMetricReporter(nil, MetricReporterOpts{
			ClusterIds: []string{
				"arn:aws:kafka:us-east-1:123456789012:cluster/msk-cluster/abc-123",
				"osk-cluster-1",
			},
			State:     &types.State{},
			StartDate: &startTime,
			EndDate:   &endTime,
		})

		clusters := []types.ProcessedClusterMetrics{
			{
				ClusterArn: "arn:aws:kafka:us-east-1:123456789012:cluster/msk-cluster/abc-123",
			},
			{
				ClusterArn: "osk-cluster-1",
			},
		}

		title := reporter.determineReportTitle(clusters)
		assert.Equal(t, "Kafka Metrics Report", title)
	})
}

func TestIsMSKCluster(t *testing.T) {
	reporter := NewMetricReporter(nil, MetricReporterOpts{})

	t.Run("MSK ARN is detected as MSK", func(t *testing.T) {
		assert.True(t, reporter.isMSKCluster("arn:aws:kafka:us-east-1:123456789012:cluster/test/abc"))
		assert.True(t, reporter.isMSKCluster("arn:aws:kafka:eu-west-1:999999999999:cluster/prod/xyz"))
	})

	t.Run("OSK ID is not detected as MSK", func(t *testing.T) {
		assert.False(t, reporter.isMSKCluster("osk-cluster-1"))
		assert.False(t, reporter.isMSKCluster("my-kafka-cluster"))
		assert.False(t, reporter.isMSKCluster("prod-kafka"))
	})

	t.Run("Short strings are not MSK", func(t *testing.T) {
		assert.False(t, reporter.isMSKCluster("arn"))
		assert.False(t, reporter.isMSKCluster("ar"))
		assert.False(t, reporter.isMSKCluster(""))
	})
}

func TestGenerateReport_MSKCluster(t *testing.T) {
	startTime := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	endTime := time.Date(2025, 1, 31, 0, 0, 0, 0, time.UTC)

	reporter := NewMetricReporter(nil, MetricReporterOpts{
		ClusterIds: []string{"arn:aws:kafka:us-east-1:123456789012:cluster/test-msk/abc-123"},
		State:      &types.State{},
		StartDate:  &startTime,
		EndDate:    &endTime,
	})

	clusters := []types.ProcessedClusterMetrics{
		{
			ClusterArn: "arn:aws:kafka:us-east-1:123456789012:cluster/test-msk/abc-123",
			Region:     "us-east-1",
			Metadata: types.MetricMetadata{
				ClusterType:         "PROVISIONED",
				NumberOfBrokerNodes: 3,
				KafkaVersion:        "3.5.1",
				EnhancedMonitoring:  "PER_BROKER",
				Period:              300,
				InstanceType:        "kafka.m5.large",
				TieredStorage:       false,
				FollowerFetching:    true,
			},
			Aggregates: map[string]types.MetricAggregate{
				"BytesInPerSec": {
					Average: ptr(500.0),
					Maximum: ptr(1000.0),
					Minimum: ptr(100.0),
				},
			},
			QueryInfo: []types.MetricQueryInfo{
				{
					MetricName: "BytesInPerSec",
					Namespace:  "AWS/Kafka",
					Statistic:  "Average",
				},
			},
		},
	}

	md := reporter.generateReport(clusters)
	report := md.String()

	// Check title
	assert.Contains(t, report, "AWS MSK Metrics Report")

	// Check MSK-specific fields are present
	assert.Contains(t, report, "Cluster ARN")
	assert.Contains(t, report, "Region")
	assert.Contains(t, report, "Cluster Type")
	assert.Contains(t, report, "Enhanced Monitoring")
	assert.Contains(t, report, "Instance Type")
	assert.Contains(t, report, "Tiered Storage")
	assert.Contains(t, report, "Follower Fetching")

	// Check query details section is present
	assert.Contains(t, report, "Query Details")

	// Check OSK-specific fields are NOT present
	assert.NotContains(t, report, "Cluster ID:")
	assert.NotContains(t, report, "Environment:")
	assert.NotContains(t, report, "Location:")
}

func TestGenerateReport_OSKCluster(t *testing.T) {
	startTime := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	endTime := time.Date(2025, 1, 31, 0, 0, 0, 0, time.UTC)

	reporter := NewMetricReporter(nil, MetricReporterOpts{
		ClusterIds: []string{"my-osk-cluster"},
		State:      &types.State{},
		StartDate:  &startTime,
		EndDate:    &endTime,
	})

	clusters := []types.ProcessedClusterMetrics{
		{
			ClusterArn:  "my-osk-cluster",
			Region:      "", // OSK clusters don't have regions
			Environment: "production",
			Location:    "datacenter-1",
			Metadata: types.MetricMetadata{
				NumberOfBrokerNodes: 5,
				KafkaVersion:        "3.6.0",
				Period:              60,
			},
			Aggregates: map[string]types.MetricAggregate{
				"MessagesInPerSec": {
					Average: ptr(250.0),
					Maximum: ptr(500.0),
					Minimum: ptr(50.0),
				},
			},
			QueryInfo: nil, // No metrics backend was used for this cluster
		},
	}

	md := reporter.generateReport(clusters)
	report := md.String()

	// Check title
	assert.Contains(t, report, "Apache Kafka Metrics Report")

	// Check OSK-specific fields are present
	assert.Contains(t, report, "Cluster ID")
	assert.Contains(t, report, "my-osk-cluster")
	assert.Contains(t, report, "Environment")
	assert.Contains(t, report, "production")
	assert.Contains(t, report, "Location")
	assert.Contains(t, report, "datacenter-1")

	// Check MSK-specific fields are NOT present
	assert.NotContains(t, report, "Cluster ARN:")
	assert.NotContains(t, report, "Region:")
	assert.NotContains(t, report, "Cluster Type:")
	assert.NotContains(t, report, "Enhanced Monitoring:")
	assert.NotContains(t, report, "Instance Type:")
	assert.NotContains(t, report, "Tiered Storage:")
	assert.NotContains(t, report, "Follower Fetching:")

	// Check query details section is NOT present
	assert.NotContains(t, report, "Query Details")
}

func TestGenerateReport_OSKCluster_MissingMetadata(t *testing.T) {
	startTime := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	endTime := time.Date(2025, 1, 31, 0, 0, 0, 0, time.UTC)

	reporter := NewMetricReporter(nil, MetricReporterOpts{
		ClusterIds: []string{"osk-no-metadata"},
		State:      &types.State{},
		StartDate:  &startTime,
		EndDate:    &endTime,
	})

	clusters := []types.ProcessedClusterMetrics{
		{
			ClusterArn:  "osk-no-metadata",
			Region:      "",
			Environment: "", // Empty environment
			Location:    "", // Empty location
			Metadata: types.MetricMetadata{
				NumberOfBrokerNodes: 3,
				KafkaVersion:        "3.5.0",
				Period:              300,
			},
		},
	}

	md := reporter.generateReport(clusters)
	report := md.String()

	// Check that "Not specified" is shown for missing metadata
	assert.Contains(t, report, "Environment")
	assert.Contains(t, report, "Not specified")
	assert.Contains(t, report, "Location")

	// Count occurrences of "Not specified" - should be at least 2 (environment and location)
	count := strings.Count(report, "Not specified")
	assert.GreaterOrEqual(t, count, 2, "Expected 'Not specified' to appear at least twice for missing environment and location")
}

func TestGenerateReport_MixedClusters(t *testing.T) {
	startTime := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	endTime := time.Date(2025, 1, 31, 0, 0, 0, 0, time.UTC)

	reporter := NewMetricReporter(nil, MetricReporterOpts{
		ClusterIds: []string{
			"arn:aws:kafka:us-east-1:123456789012:cluster/msk/abc",
			"osk-cluster",
		},
		State:     &types.State{},
		StartDate: &startTime,
		EndDate:   &endTime,
	})

	clusters := []types.ProcessedClusterMetrics{
		{
			ClusterArn: "arn:aws:kafka:us-east-1:123456789012:cluster/msk/abc",
			Region:     "us-east-1",
			Metadata: types.MetricMetadata{
				ClusterType:         "PROVISIONED",
				NumberOfBrokerNodes: 3,
				KafkaVersion:        "3.5.1",
				EnhancedMonitoring:  "PER_BROKER",
				InstanceType:        "kafka.m5.large",
			},
		},
		{
			ClusterArn:  "osk-cluster",
			Region:      "",
			Environment: "staging",
			Location:    "us-datacenter",
			Metadata: types.MetricMetadata{
				NumberOfBrokerNodes: 5,
				KafkaVersion:        "3.6.0",
			},
		},
	}

	md := reporter.generateReport(clusters)
	report := md.String()

	// Check title for mixed clusters
	assert.Contains(t, report, "Kafka Metrics Report")

	// Check both MSK and OSK fields are present
	assert.Contains(t, report, "Cluster ARN")
	assert.Contains(t, report, "Cluster ID")
	assert.Contains(t, report, "Enhanced Monitoring")
	assert.Contains(t, report, "Environment")
}

func TestBackwardCompatibility_MSKReport(t *testing.T) {
	startTime := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	endTime := time.Date(2025, 1, 31, 0, 0, 0, 0, time.UTC)

	reporter := NewMetricReporter(nil, MetricReporterOpts{
		ClusterIds: []string{"arn:aws:kafka:us-east-1:123456789012:cluster/legacy/xyz"},
		State:      &types.State{},
		StartDate:  &startTime,
		EndDate:    &endTime,
	})

	// MSK cluster without OSK fields (backward compatibility test)
	clusters := []types.ProcessedClusterMetrics{
		{
			ClusterArn: "arn:aws:kafka:us-east-1:123456789012:cluster/legacy/xyz",
			Region:     "us-east-1",
			// Environment and Location are empty (omitted in JSON)
			Environment: "",
			Location:    "",
			Metadata: types.MetricMetadata{
				ClusterType:         "PROVISIONED",
				NumberOfBrokerNodes: 3,
				KafkaVersion:        "2.8.1",
				EnhancedMonitoring:  "DEFAULT",
				InstanceType:        "kafka.m5.large",
				Period:              300,
			},
		},
	}

	md := reporter.generateReport(clusters)
	report := md.String()

	// Verify MSK report format is unchanged
	assert.Contains(t, report, "AWS MSK Metrics Report")
	assert.Contains(t, report, "Cluster ARN")
	assert.Contains(t, report, "Region")
	assert.Contains(t, report, "us-east-1")
	assert.Contains(t, report, "Cluster Type")
	assert.Contains(t, report, "Enhanced Monitoring")
	assert.Contains(t, report, "Instance Type")

	// OSK-specific fields should not appear in MSK report
	assert.NotContains(t, report, "Cluster ID:")
	assert.NotContains(t, report, "Environment:")
	assert.NotContains(t, report, "Location:")
}

func TestBackwardCompatibility_QueryInfoWithoutSourceType(t *testing.T) {
	startTime := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	endTime := time.Date(2025, 1, 31, 0, 0, 0, 0, time.UTC)

	reporter := NewMetricReporter(nil, MetricReporterOpts{
		ClusterIds: []string{"arn:aws:kafka:us-east-1:123456789012:cluster/old-cluster/abc"},
		State:      &types.State{},
		StartDate:  &startTime,
		EndDate:    &endTime,
	})

	// Simulate an old state file where SourceType was not set on QueryInfo
	clusters := []types.ProcessedClusterMetrics{
		{
			ClusterArn: "arn:aws:kafka:us-east-1:123456789012:cluster/old-cluster/abc",
			Region:     "us-east-1",
			Metadata: types.MetricMetadata{
				ClusterType:         "PROVISIONED",
				NumberOfBrokerNodes: 3,
				KafkaVersion:        "3.5.1",
				Period:              300,
			},
			QueryInfo: []types.MetricQueryInfo{
				{
					MetricName:       "BytesInPerSec",
					SourceType:       "", // Empty — old state file without source_type
					Namespace:        "AWS/Kafka",
					Statistic:        "Average",
					Dimensions:       "Cluster Name, Broker ID",
					Period:           300,
					SearchExpression: "SEARCH('{AWS/Kafka,Cluster Name,Broker ID}', 'Average', 300)",
					AggregationNote:  "Uses SEARCH to find BytesInPerSec across all brokers.",
				},
			},
		},
	}

	md := reporter.generateReport(clusters)
	report := md.String()

	// Should render as CloudWatch via the default branch
	assert.Contains(t, report, "Query Details")
	assert.Contains(t, report, "Namespace")
	assert.Contains(t, report, "AWS/Kafka")
	assert.Contains(t, report, "Statistic")
	assert.Contains(t, report, "Average")
	assert.Contains(t, report, "Dimensions")
	assert.Contains(t, report, "SEARCH Expression")
	assert.Contains(t, report, "300 seconds")

	// Should NOT contain OSK-specific fields
	assert.NotContains(t, report, "Jolokia")
	assert.NotContains(t, report, "MBean Path")
	assert.NotContains(t, report, "Prometheus")
	assert.NotContains(t, report, "PromQL")
}

func TestGenerateReport_OSKCluster_JolokiaQueryDetails(t *testing.T) {
	startTime := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	endTime := time.Date(2025, 1, 31, 0, 0, 0, 0, time.UTC)

	reporter := NewMetricReporter(nil, MetricReporterOpts{
		ClusterIds: []string{"jolokia-cluster"},
		State:      &types.State{},
		StartDate:  &startTime,
		EndDate:    &endTime,
	})

	clusters := []types.ProcessedClusterMetrics{
		{
			ClusterArn:  "jolokia-cluster",
			Environment: "production",
			Location:    "dc-1",
			Metadata: types.MetricMetadata{
				NumberOfBrokerNodes: 3,
				KafkaVersion:        "3.6.0",
				Period:              10,
			},
			Aggregates: map[string]types.MetricAggregate{
				"BytesInPerSec": {Average: ptr(1024.0), Maximum: ptr(2048.0), Minimum: ptr(512.0)},
			},
			QueryInfo: []types.MetricQueryInfo{
				{
					MetricName:      "BytesInPerSec",
					SourceType:      types.MetricBackendJolokia,
					Statistic:       "Rate (delta/sec, summed across brokers)",
					Period:          10,
					QueryDuration:   "5m",
					MBeanPath:       "kafka.server:type=BrokerTopicMetrics,name=BytesInPerSec",
					JolokiaURL:      "http://broker1:8778/jolokia",
					CurlCommand:     "curl 'http://broker1:8778/jolokia/read/kafka.server:type=BrokerTopicMetrics,name=BytesInPerSec'",
					AggregationNote: "Rate computed from the monotonic Count attribute.",
				},
				{
					MetricName:      "PartitionCount",
					SourceType:      types.MetricBackendJolokia,
					Statistic:       "Sum across brokers",
					Period:          10,
					QueryDuration:   "5m",
					MBeanPath:       "kafka.server:type=ReplicaManager,name=PartitionCount",
					JolokiaURL:      "http://broker1:8778/jolokia",
					CurlCommand:     "curl 'http://broker1:8778/jolokia/read/kafka.server:type=ReplicaManager,name=PartitionCount'",
					AggregationNote: "Gauge value read from the Value attribute.",
				},
			},
		},
	}

	md := reporter.generateReport(clusters)
	report := md.String()

	// Query Details section should be present
	assert.Contains(t, report, "Query Details")

	// Jolokia-specific fields
	assert.Contains(t, report, "Jolokia (JMX)")
	assert.Contains(t, report, "Jolokia Endpoint")
	assert.Contains(t, report, "http://broker1:8778/jolokia")
	assert.Contains(t, report, "MBean Path")
	assert.Contains(t, report, "kafka.server:type=BrokerTopicMetrics,name=BytesInPerSec")

	// Statistic, poll interval, and query duration
	assert.Contains(t, report, "Rate (delta/sec, summed across brokers)")
	assert.Contains(t, report, "Sum across brokers")
	assert.Contains(t, report, "Poll Interval")
	assert.Contains(t, report, "10 seconds")
	assert.Contains(t, report, "Query Duration")
	assert.Contains(t, report, "5m")

	// Curl command
	assert.Contains(t, report, "Curl Command")
	assert.Contains(t, report, "curl 'http://broker1:8778/jolokia/read/")

	// Aggregation note
	assert.Contains(t, report, "Rate computed from the monotonic Count attribute.")

	// Should NOT contain CloudWatch-specific fields
	assert.NotContains(t, report, "Namespace")
	assert.NotContains(t, report, "Dimensions")
	assert.NotContains(t, report, "SEARCH Expression")
	assert.NotContains(t, report, "AWS CLI Command")
}

func TestGenerateReport_OSKCluster_PrometheusQueryDetails(t *testing.T) {
	startTime := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	endTime := time.Date(2025, 1, 31, 0, 0, 0, 0, time.UTC)

	reporter := NewMetricReporter(nil, MetricReporterOpts{
		ClusterIds: []string{"prometheus-cluster"},
		State:      &types.State{},
		StartDate:  &startTime,
		EndDate:    &endTime,
	})

	clusters := []types.ProcessedClusterMetrics{
		{
			ClusterArn:  "prometheus-cluster",
			Environment: "staging",
			Location:    "us-east",
			Metadata: types.MetricMetadata{
				NumberOfBrokerNodes: 5,
				KafkaVersion:        "3.5.1",
				Period:              60,
			},
			Aggregates: map[string]types.MetricAggregate{
				"BytesInPerSec":          {Average: ptr(2048.0), Maximum: ptr(4096.0), Minimum: ptr(1024.0)},
				"TotalLocalStorageUsage": {Average: ptr(50.5), Maximum: ptr(55.0), Minimum: ptr(45.0)},
			},
			QueryInfo: []types.MetricQueryInfo{
				{
					MetricName:           "BytesInPerSec",
					SourceType:           types.MetricBackendPrometheus,
					Statistic:            "Rate (sum of rate() over 5m window)",
					Period:               60,
					QueryDuration:        "1d",
					PromQLQuery:          "sum(rate(kafka_server_brokertopicmetrics_bytesinpersec_total[5m]))",
					PrometheusURL:        "http://prometheus:9090",
					PrometheusMetricName: "kafka_server_brokertopicmetrics_bytesinpersec_total",
					CurlCommand:          "curl -G 'http://prometheus:9090/api/v1/query_range' --data-urlencode 'query=sum(rate(kafka_server_brokertopicmetrics_bytesinpersec_total[5m]))' --data-urlencode 'start=2025-01-01T00:00:00Z' --data-urlencode 'end=2025-01-02T00:00:00Z' --data-urlencode 'step=60s'",
					AggregationNote:      "Computes rate() over a 5m window, then sums across all instances.",
				},
				{
					MetricName:           "TotalLocalStorageUsage",
					SourceType:           types.MetricBackendPrometheus,
					Statistic:            "Sum (bytes converted to GiB)",
					Period:               60,
					QueryDuration:        "1d",
					PromQLQuery:          "sum(kafka_log_log_size) / (1024*1024*1024)",
					PrometheusURL:        "http://prometheus:9090",
					PrometheusMetricName: "kafka_log_log_size",
					CurlCommand:          "curl -G 'http://prometheus:9090/api/v1/query_range' --data-urlencode 'query=sum(kafka_log_log_size) / (1024*1024*1024)' --data-urlencode 'start=2025-01-01T00:00:00Z' --data-urlencode 'end=2025-01-02T00:00:00Z' --data-urlencode 'step=60s'",
					AggregationNote:      "Sums raw byte values across all instances and converts to GiB.",
				},
			},
		},
	}

	md := reporter.generateReport(clusters)
	report := md.String()

	// Query Details section should be present
	assert.Contains(t, report, "Query Details")

	// Prometheus-specific fields
	assert.Contains(t, report, "Prometheus")
	assert.Contains(t, report, "Prometheus URL")
	assert.Contains(t, report, "http://prometheus:9090")
	assert.Contains(t, report, "Prometheus Metric")
	assert.Contains(t, report, "kafka_server_brokertopicmetrics_bytesinpersec_total")

	// PromQL query
	assert.Contains(t, report, "PromQL Query")
	assert.Contains(t, report, "sum(rate(kafka_server_brokertopicmetrics_bytesinpersec_total[5m]))")
	assert.Contains(t, report, "sum(kafka_log_log_size) / (1024*1024*1024)")

	// Statistic, query step, and query duration
	assert.Contains(t, report, "Rate (sum of rate() over 5m window)")
	assert.Contains(t, report, "Sum (bytes converted to GiB)")
	assert.Contains(t, report, "Query Step")
	assert.Contains(t, report, "60 seconds")
	assert.Contains(t, report, "Query Duration")
	assert.Contains(t, report, "1d")

	// Curl command with real timestamps
	assert.Contains(t, report, "Curl Command")
	assert.Contains(t, report, "query_range")
	assert.Contains(t, report, "start=2025-01-01")
	assert.Contains(t, report, "step=60s")

	// Aggregation notes
	assert.Contains(t, report, "Computes rate() over a 5m window")
	assert.Contains(t, report, "Sums raw byte values across all instances and converts to GiB.")

	// Should NOT contain CloudWatch or Jolokia-specific fields
	assert.NotContains(t, report, "Namespace")
	assert.NotContains(t, report, "Dimensions")
	assert.NotContains(t, report, "SEARCH Expression")
	assert.NotContains(t, report, "AWS CLI Command")
	assert.NotContains(t, report, "MBean Path")
	assert.NotContains(t, report, "Jolokia")
}

// ptr is a helper function to create a pointer to a float64
func ptr(v float64) *float64 {
	return &v
}
