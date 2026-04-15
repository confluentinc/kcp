package metrics

import (
	"strings"
	"testing"
	"time"

	"github.com/confluentinc/kcp/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// MockReportService is a mock implementation of ReportService
type MockReportService struct {
	mock.Mock
}

func (m *MockReportService) ProcessState(state types.State) types.ProcessedState {
	args := m.Called(state)
	return args.Get(0).(types.ProcessedState)
}

func (m *MockReportService) FilterClusterMetrics(processedState types.ProcessedState, clusterArn string, sourceType string, startTime, endTime *time.Time) (*types.ProcessedClusterMetrics, error) {
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
		assert.Equal(t, "OSK Metrics Report", title)
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
			QueryInfo: nil, // OSK clusters don't have CloudWatch query info
		},
	}

	md := reporter.generateReport(clusters)
	report := md.String()

	// Check title
	assert.Contains(t, report, "OSK Metrics Report")

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

// ptr is a helper function to create a pointer to a float64
func ptr(v float64) *float64 {
	return &v
}
