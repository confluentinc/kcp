package metrics

import (
	"encoding/json"
	"strings"
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
		populateCLICommands(queryInfos, queries, startTime, endTime, "us-east-1")

		for _, info := range queryInfos {
			assert.NotEmpty(t, info.AWSCLICommand, "CLI command should be populated for %s", info.MetricName)
			assert.Contains(t, info.AWSCLICommand, "aws cloudwatch get-metric-data")
			assert.Contains(t, info.AWSCLICommand, "--region us-east-1")
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
		populateCLICommands(queryInfos, queries, startTime, endTime, "us-east-1")

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

// extractJSONFromCLICommand extracts the JSON array from the heredoc in the CLI command
func extractJSONFromCLICommand(t *testing.T, cmd string) string {
	t.Helper()
	// Find the JSON between <<'QUERY' and QUERY delimiters
	startMarker := "<<'QUERY'\n"
	start := strings.Index(cmd, startMarker)
	require.NotEqual(t, -1, start, "Should find <<'QUERY' in CLI command")
	start += len(startMarker)

	endMarker := "\nQUERY\n"
	end := strings.Index(cmd[start:], endMarker)
	require.NotEqual(t, -1, end, "Should find QUERY delimiter")

	return cmd[start : start+end]
}

func TestPopulateCLICommandsWithStorageQueries(t *testing.T) {
	ms := &MetricService{client: nil}
	startTime := time.Date(2025, 3, 10, 0, 0, 0, 0, time.UTC)
	endTime := time.Date(2026, 3, 10, 0, 0, 0, 0, time.UTC)

	// Local storage has 3 query entries (SEARCH + math + SUM), but the CLI command
	// should still match via the SEARCH expression
	queries, queryInfos := ms.buildLocalStorageUsageQuery("test-cluster", 86400, 500)
	populateCLICommands(queryInfos, queries, startTime, endTime, "us-east-1")

	info := queryInfos[0]
	assert.NotEmpty(t, info.AWSCLICommand)
	assert.Contains(t, info.AWSCLICommand, "aws cloudwatch get-metric-data")

	// Verify JSON is valid
	jsonStr := extractJSONFromCLICommand(t, info.AWSCLICommand)
	var entries []map[string]any
	err := json.Unmarshal([]byte(jsonStr), &entries)
	require.NoError(t, err)
	// Should contain all 3 queries: SEARCH + math conversion + SUM aggregation
	assert.Len(t, entries, 3)
	// The final SUM query should have ReturnData: true
	assert.Equal(t, true, entries[2]["ReturnData"])
}

func TestConsoleSourceJSON(t *testing.T) {
	ms := &MetricService{client: nil}
	startTime := time.Date(2025, 3, 10, 0, 0, 0, 0, time.UTC)
	endTime := time.Date(2026, 3, 10, 0, 0, 0, 0, time.UTC)

	t.Run("SEARCH-based metric produces valid console JSON", func(t *testing.T) {
		queries, queryInfos := ms.buildBrokerMetricQueries("test-cluster", 86400)
		populateCLICommands(queryInfos, queries, startTime, endTime, "us-east-1")

		for _, info := range queryInfos {
			assert.NotEmpty(t, info.ConsoleSourceJSON, "ConsoleSourceJSON should be populated for %s", info.MetricName)

			var source map[string]any
			err := json.Unmarshal([]byte(info.ConsoleSourceJSON), &source)
			require.NoError(t, err, "ConsoleSourceJSON should be valid JSON for %s", info.MetricName)

			assert.Equal(t, "timeSeries", source["view"])
			assert.Equal(t, false, source["stacked"])
			assert.Equal(t, "us-east-1", source["region"])
			assert.Contains(t, source, "metrics")

			// Verify metrics array contains the full query chain (SEARCH + SUM)
			metrics, ok := source["metrics"].([]any)
			require.True(t, ok)
			assert.Len(t, metrics, 2, "Should have SEARCH + SUM entries for %s", info.MetricName)
		}
	})

	t.Run("MetricStat-based metric produces valid console JSON", func(t *testing.T) {
		queries, queryInfos := ms.buildClusterMetricQueries("test-cluster", 86400)
		populateCLICommands(queryInfos, queries, startTime, endTime, "us-east-1")

		info := queryInfos[0]
		assert.NotEmpty(t, info.ConsoleSourceJSON)

		var source map[string]any
		err := json.Unmarshal([]byte(info.ConsoleSourceJSON), &source)
		require.NoError(t, err)
		assert.Equal(t, "us-east-1", source["region"])
	})
}

// Ensure MetricService with nil client doesn't panic on query building (only on execution)
func TestMetricServiceNilClient(t *testing.T) {
	ms := NewMetricService((*cloudwatch.Client)(nil))
	require.NotNil(t, ms)

	queries, infos := ms.buildBrokerMetricQueries("test", 3600)
	assert.NotEmpty(t, queries)
	assert.NotEmpty(t, infos)
}
