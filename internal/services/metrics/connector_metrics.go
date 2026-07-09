package metrics

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	cloudwatchtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
	"github.com/confluentinc/kcp/internal/types"
)

// connectorMetricStats maps each AWS/KafkaConnect CloudWatch metric to the display
// Label used for the stored series and the CloudWatch statistic. Labels are aligned
// with the self-managed Connect metric names (internal/services/prometheus
// ConnectQueryDefinitions / jmx ConnectMetricDefinitions) so the two Connect metrics
// paths render identically in the UI. NOTE: metric names are AWS-docs facts — verify
// against `aws cloudwatch list-metrics --namespace AWS/KafkaConnect`.
var connectorMetricStats = []struct {
	AWSName string // AWS/KafkaConnect metric name (used in the CloudWatch query)
	Label   string // display label (aligned with self-managed Connect labels)
	Stat    string
}{
	{"BytesInPerSec", "incoming-byte-rate", "Average"},
	{"BytesOutPerSec", "outgoing-byte-rate", "Average"},
	{"SourceRecordPollRate", "source-record-poll-rate", "Average"},
	{"SourceRecordWriteRate", "source-record-write-rate", "Average"},
	{"SinkRecordReadRate", "sink-record-read-rate", "Average"},
	{"SinkRecordSendRate", "sink-record-send-rate", "Average"},
	{"RunningTaskCount", "task-count", "Maximum"},
}

// buildConnectorMetricQueries builds one MetricStat query per (connector, metric),
// dimensioned by "ConnectorName". Direct MetricStat (not SEARCH) is used because the
// connector names are known, giving deterministic per-connector labels ("<metric> (<connector>)").
func buildConnectorMetricQueries(connectorNames []string, period int32) ([]cloudwatchtypes.MetricDataQuery, []types.MetricQueryInfo) {
	var queries []cloudwatchtypes.MetricDataQuery
	var infos []types.MetricQueryInfo
	id := 0
	for _, name := range connectorNames {
		for _, m := range connectorMetricStats {
			label := fmt.Sprintf("%s (%s)", m.Label, name)
			queries = append(queries, cloudwatchtypes.MetricDataQuery{
				Id:    aws.String(fmt.Sprintf("q%d", id)),
				Label: aws.String(label),
				MetricStat: &cloudwatchtypes.MetricStat{
					Metric: &cloudwatchtypes.Metric{
						Namespace:  aws.String("AWS/KafkaConnect"),
						MetricName: aws.String(m.AWSName),
						Dimensions: []cloudwatchtypes.Dimension{
							{Name: aws.String("ConnectorName"), Value: aws.String(name)},
						},
					},
					Period: aws.Int32(period),
					Stat:   aws.String(m.Stat),
				},
				ReturnData: aws.Bool(true),
			})
			infos = append(infos, types.MetricQueryInfo{
				MetricName:      label,
				SourceType:      types.MetricBackendCloudWatch,
				Namespace:       "AWS/KafkaConnect",
				Dimensions:      "ConnectorName",
				Statistic:       m.Stat,
				Period:          period,
				AggregationNote: fmt.Sprintf("Direct MetricStat for MSK Connect metric %q (shown as %q) on connector %q (dimension \"ConnectorName\").", m.AWSName, m.Label, name),
			})
			id++
		}
	}
	return queries, infos
}

// CollectConnectorMetrics fetches AWS/KafkaConnect metrics for the given connector
// names over the time window and returns the RAW envelope (mirroring
// ProcessProvisionedCluster). Flattening/aggregation into ConnectClusterMetrics is
// done by the caller (the managed-connectors scanner) so this package stays free of
// the report package. With no connectors it returns an empty (non-nil) envelope.
func (ms *MetricService) CollectConnectorMetrics(ctx context.Context, connectorNames []string, tw types.CloudWatchTimeWindow, region string) (*types.ClusterMetrics, error) {
	meta := types.MetricMetadata{
		StartDate: tw.StartTime,
		EndDate:   tw.EndTime,
		Period:    tw.Period,
	}
	if len(connectorNames) == 0 {
		return &types.ClusterMetrics{MetricMetadata: meta}, nil
	}

	queries, infos := buildConnectorMetricQueries(connectorNames, tw.Period)
	// Attach a reproducible `aws cloudwatch get-metric-data` command + console
	// source JSON to each query info — the CloudWatch analog of the self-managed
	// jolokia/prometheus curl (parity with the broker-metrics path).
	populateConnectorCLICommands(infos, queries, tw.StartTime, tw.EndTime, region)

	// seriesEstimate: one series per query (no fan-out), so the count of queries.
	out, err := ms.executeChunkedQuery(ctx, queries, tw.StartTime, tw.EndTime, tw.Period, len(queries), "connector-metrics")
	if err != nil {
		return nil, fmt.Errorf("failed to fetch connector metrics: %v", err)
	}

	return &types.ClusterMetrics{
		MetricMetadata: meta,
		Results:        out.MetricDataResults,
		QueryInfo:      infos,
	}, nil
}

// populateConnectorCLICommands sets AWSCLICommand + ConsoleSourceJSON on each query
// info from its index-paired MetricStat query (infos[i] and queries[i] are built
// together by buildConnectorMetricQueries). It can't reuse populateCLICommands
// because that matches MetricStat queries by MetricName, whereas connector infos
// carry the display label (and multiple connectors share an AWS metric name).
// Reuses the shared cli/console helpers so output matches the broker path.
func populateConnectorCLICommands(infos []types.MetricQueryInfo, queries []cloudwatchtypes.MetricDataQuery, startTime, endTime time.Time, region string) {
	for i := range infos {
		if i >= len(queries) {
			break
		}
		q := queries[i]
		if q.MetricStat == nil || q.MetricStat.Metric == nil {
			continue
		}
		var dims []cliDimension
		for _, d := range q.MetricStat.Metric.Dimensions {
			dims = append(dims, cliDimension{Name: aws.ToString(d.Name), Value: aws.ToString(d.Value)})
		}
		stat := &cliMetricStat{Period: aws.ToInt32(q.MetricStat.Period), Stat: aws.ToString(q.MetricStat.Stat)}
		stat.Metric.Namespace = aws.ToString(q.MetricStat.Metric.Namespace)
		stat.Metric.MetricName = aws.ToString(q.MetricStat.Metric.MetricName)
		stat.Metric.Dimensions = dims
		entry := cliQueryEntry{ID: aws.ToString(q.Id), MetricStat: stat, ReturnData: aws.ToBool(q.ReturnData)}
		if q.Label != nil {
			entry.Label = *q.Label
		}
		entries := []cliQueryEntry{entry}
		if queriesJSON, err := json.MarshalIndent(entries, "    ", "  "); err == nil {
			infos[i].AWSCLICommand = fmt.Sprintf("aws cloudwatch get-metric-data \\\n  --region %s \\\n  --start-time %s \\\n  --end-time %s \\\n  --metric-data-queries \"$(cat <<'QUERY'\n%s\nQUERY\n)\"",
				region, startTime.Format(time.RFC3339), endTime.Format(time.RFC3339), string(queriesJSON))
		}
		infos[i].ConsoleSourceJSON = buildConsoleSourceJSON(entries, region)
	}
}
