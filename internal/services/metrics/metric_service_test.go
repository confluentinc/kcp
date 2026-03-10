package metrics

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewSearchMetricQueryInfo(t *testing.T) {
	info := newSearchMetricQueryInfo(
		"BytesInPerSec",
		"SEARCH('{AWS/Kafka,...}', 'Average', 86400)",
		"SUM(m_bytesinpersec)",
		"Average",
		86400,
		"Cluster Name, Broker ID",
	)

	assert.Equal(t, "BytesInPerSec", info.MetricName)
	assert.Equal(t, "AWS/Kafka", info.Namespace)
	assert.Equal(t, "Cluster Name, Broker ID", info.Dimensions)
	assert.Equal(t, "Average", info.Statistic)
	assert.Equal(t, int32(86400), info.Period)
	assert.Equal(t, "SEARCH('{AWS/Kafka,...}', 'Average', 86400)", info.SearchExpression)
	assert.Equal(t, "SUM(m_bytesinpersec)", info.MathExpression)
	assert.Contains(t, info.AggregationNote, "BytesInPerSec")
	assert.Contains(t, info.AggregationNote, "SUM(m_bytesinpersec)")
}

func TestBuildBrokerMetricQueries(t *testing.T) {
	ms := &MetricService{client: nil}
	queries, queryInfos := ms.buildBrokerMetricQueries("test-cluster", 86400)

	// 4 metrics * 2 queries each (SEARCH + SUM)
	assert.Len(t, queries, 8)
	assert.Len(t, queryInfos, 4)

	// Verify all expected metrics are present
	metricNames := map[string]bool{}
	for _, info := range queryInfos {
		metricNames[info.MetricName] = true
		assert.Equal(t, "AWS/Kafka", info.Namespace)
		assert.Equal(t, "Cluster Name, Broker ID", info.Dimensions)
		assert.Equal(t, int32(86400), info.Period)
		assert.NotEmpty(t, info.SearchExpression)
		assert.NotEmpty(t, info.MathExpression)
		assert.Contains(t, info.SearchExpression, "test-cluster")
	}

	assert.True(t, metricNames["BytesInPerSec"])
	assert.True(t, metricNames["BytesOutPerSec"])
	assert.True(t, metricNames["MessagesInPerSec"])
	assert.True(t, metricNames["PartitionCount"])
}

func TestBuildClientConnectionQueries(t *testing.T) {
	ms := &MetricService{client: nil}
	queries, queryInfos := ms.buildClientConnectionQueries("test-cluster", 3600)

	assert.Len(t, queries, 4) // 2 stats * 2 queries each
	assert.Len(t, queryInfos, 2)

	assert.Equal(t, "ClientConnectionCount (Maximum)", queryInfos[0].MetricName)
	assert.Equal(t, "Maximum", queryInfos[0].Statistic)
	assert.Equal(t, "ClientConnectionCount (Average)", queryInfos[1].MetricName)
	assert.Equal(t, "Average", queryInfos[1].Statistic)
	assert.Equal(t, "Cluster Name, Broker ID, Client Authentication", queryInfos[0].Dimensions)
}

func TestBuildClusterMetricQueries(t *testing.T) {
	ms := &MetricService{client: nil}
	queries, queryInfos := ms.buildClusterMetricQueries("test-cluster", 86400)

	assert.Len(t, queries, 1)
	assert.Len(t, queryInfos, 1)

	info := queryInfos[0]
	assert.Equal(t, "GlobalPartitionCount", info.MetricName)
	assert.Equal(t, "Maximum", info.Statistic)
	assert.Equal(t, "Cluster Name", info.Dimensions)
	assert.Empty(t, info.SearchExpression) // Uses MetricStat, not SEARCH
	assert.Empty(t, info.MathExpression)
	assert.Contains(t, info.AggregationNote, "MetricStat")
}

func TestBuildStorageQueries(t *testing.T) {
	ms := &MetricService{client: nil}

	t.Run("local storage", func(t *testing.T) {
		queries, queryInfos := ms.buildLocalStorageUsageQuery("test-cluster", 86400, 1000)

		assert.Len(t, queries, 3) // SEARCH + math + SUM
		assert.Len(t, queryInfos, 1)

		info := queryInfos[0]
		assert.Equal(t, "TotalLocalStorageUsage(GB)", info.MetricName)
		assert.Equal(t, "Maximum", info.Statistic)
		assert.Contains(t, info.SearchExpression, "KafkaDataLogsDiskUsed")
		assert.Contains(t, info.MathExpression, "1000")
	})

	t.Run("remote storage", func(t *testing.T) {
		queries, queryInfos := ms.buildRemoteStorageUsageQuery("test-cluster", 86400)

		assert.Len(t, queries, 3) // SEARCH + math + SUM
		assert.Len(t, queryInfos, 1)

		info := queryInfos[0]
		assert.Equal(t, "TotalRemoteStorageUsage(GB)", info.MetricName)
		assert.Equal(t, "Maximum", info.Statistic)
		assert.Contains(t, info.SearchExpression, "RemoteLogSizeBytes")
	})
}

func TestBuildServerlessMetricQueries(t *testing.T) {
	ms := &MetricService{client: nil}
	queries, queryInfos := ms.buildServerlessMetricQueries("test-cluster", 3600)

	assert.Len(t, queries, 6) // 3 metrics * 2 queries each
	assert.Len(t, queryInfos, 3)

	for _, info := range queryInfos {
		assert.Equal(t, "Cluster Name, Topic", info.Dimensions)
		assert.Equal(t, "Average", info.Statistic)
		assert.Contains(t, info.SearchExpression, "Topic")
	}

	metricNames := map[string]bool{}
	for _, info := range queryInfos {
		metricNames[info.MetricName] = true
	}
	assert.True(t, metricNames["BytesInPerSec"])
	assert.True(t, metricNames["BytesOutPerSec"])
	assert.True(t, metricNames["MessagesInPerSec"])
}

func TestPopulateCLICommands(t *testing.T) {
	ms := &MetricService{client: nil}
	startTime := time.Date(2025, 3, 10, 0, 0, 0, 0, time.UTC)
	endTime := time.Date(2026, 3, 10, 0, 0, 0, 0, time.UTC)

	t.Run("SEARCH-based metrics", func(t *testing.T) {
		queries, queryInfos := ms.buildBrokerMetricQueries("test-cluster", 86400)
		populateCLICommands(queryInfos, queries, startTime, endTime)

		for _, info := range queryInfos {
			assert.NotEmpty(t, info.AWSCLICommand, "CLI command should be populated for %s", info.MetricName)
			assert.Contains(t, info.AWSCLICommand, "aws cloudwatch get-metric-data")
			assert.Contains(t, info.AWSCLICommand, "--start-time 2025-03-10T00:00:00Z")
			assert.Contains(t, info.AWSCLICommand, "--end-time 2026-03-10T00:00:00Z")
			assert.Contains(t, info.AWSCLICommand, "--metric-data-queries")

			// Verify the JSON in the CLI command is valid
			jsonStr := extractJSONFromCLICommand(t, info.AWSCLICommand)
			var entries []map[string]any
			err := json.Unmarshal([]byte(jsonStr), &entries)
			require.NoError(t, err, "CLI command JSON should be valid for %s", info.MetricName)
			assert.Len(t, entries, 2, "Should have SEARCH + SUM query entries")
		}
	})

	t.Run("MetricStat-based metrics", func(t *testing.T) {
		queries, queryInfos := ms.buildClusterMetricQueries("test-cluster", 86400)
		populateCLICommands(queryInfos, queries, startTime, endTime)

		info := queryInfos[0]
		assert.NotEmpty(t, info.AWSCLICommand)
		assert.Contains(t, info.AWSCLICommand, "aws cloudwatch get-metric-data")

		jsonStr := extractJSONFromCLICommand(t, info.AWSCLICommand)
		var entries []map[string]any
		err := json.Unmarshal([]byte(jsonStr), &entries)
		require.NoError(t, err, "CLI command JSON should be valid for MetricStat query")
		assert.Len(t, entries, 1)
		assert.Contains(t, entries[0], "MetricStat")
	})
}

// extractJSONFromCLICommand extracts the JSON array from the --metric-data-queries parameter
func extractJSONFromCLICommand(t *testing.T, cmd string) string {
	t.Helper()
	// Find the JSON between single quotes after --metric-data-queries
	start := -1
	for i := 0; i < len(cmd); i++ {
		if i+len("--metric-data-queries '") <= len(cmd) && cmd[i:i+len("--metric-data-queries '")] == "--metric-data-queries '" {
			start = i + len("--metric-data-queries '")
			break
		}
	}
	require.NotEqual(t, -1, start, "Should find --metric-data-queries in CLI command")

	// Find the closing single quote
	end := len(cmd) - 1
	for end > start && cmd[end] != '\'' {
		end--
	}
	require.Greater(t, end, start, "Should find closing quote")

	return cmd[start:end]
}

func TestPopulateCLICommandsWithStorageQueries(t *testing.T) {
	ms := &MetricService{client: nil}
	startTime := time.Date(2025, 3, 10, 0, 0, 0, 0, time.UTC)
	endTime := time.Date(2026, 3, 10, 0, 0, 0, 0, time.UTC)

	// Local storage has 3 query entries (SEARCH + math + SUM), but the CLI command
	// should still match via the SEARCH expression
	queries, queryInfos := ms.buildLocalStorageUsageQuery("test-cluster", 86400, 500)
	populateCLICommands(queryInfos, queries, startTime, endTime)

	info := queryInfos[0]
	assert.NotEmpty(t, info.AWSCLICommand)
	assert.Contains(t, info.AWSCLICommand, "aws cloudwatch get-metric-data")

	// Verify JSON is valid
	jsonStr := extractJSONFromCLICommand(t, info.AWSCLICommand)
	var entries []map[string]any
	err := json.Unmarshal([]byte(jsonStr), &entries)
	require.NoError(t, err)
	// Should contain the SEARCH query and its dependent math queries
	assert.GreaterOrEqual(t, len(entries), 2)
}

// Ensure MetricService with nil client doesn't panic on query building (only on execution)
func TestMetricServiceNilClient(t *testing.T) {
	ms := NewMetricService((*cloudwatch.Client)(nil))
	require.NotNil(t, ms)

	queries, infos := ms.buildBrokerMetricQueries("test", 3600)
	assert.NotEmpty(t, queries)
	assert.NotEmpty(t, infos)
}
