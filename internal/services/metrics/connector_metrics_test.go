package metrics

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	cloudwatchtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildConnectorMetricQueries_TargetsKafkaConnectNamespace(t *testing.T) {
	queries, infos := buildConnectorMetricQueries([]string{"conn-a", "conn-b"}, 300)

	// 7 metrics x 2 connectors = 14 MetricStat queries, all ReturnData=true.
	require.Len(t, queries, 14)
	for _, q := range queries {
		require.NotNil(t, q.MetricStat)
		assert.Equal(t, "AWS/KafkaConnect", aws.ToString(q.MetricStat.Metric.Namespace))
		require.Len(t, q.MetricStat.Metric.Dimensions, 1)
		assert.Equal(t, "ConnectorName", aws.ToString(q.MetricStat.Metric.Dimensions[0].Name))
		assert.True(t, aws.ToBool(q.ReturnData))
		assert.Equal(t, int32(300), aws.ToInt32(q.MetricStat.Period))
	}
	// QueryInfo carries the AWS/KafkaConnect namespace and cloudwatch source.
	require.NotEmpty(t, infos)
	assert.Equal(t, "AWS/KafkaConnect", infos[0].Namespace)
	assert.Equal(t, types.MetricBackendCloudWatch, infos[0].SourceType)

	// Labels are aligned with the self-managed Connect metric names (kebab-case),
	// not the raw AWS metric names, so both Connect metrics paths render alike.
	labels := map[string]bool{}
	for _, in := range infos {
		labels[in.MetricName] = true
	}
	for _, want := range []string{
		"incoming-byte-rate (conn-a)", "outgoing-byte-rate (conn-a)",
		"source-record-poll-rate (conn-a)", "source-record-write-rate (conn-a)",
		"sink-record-read-rate (conn-a)", "sink-record-send-rate (conn-a)",
		"task-count (conn-a)",
	} {
		assert.True(t, labels[want], "expected self-managed-aligned label %q", want)
	}
}

// spyGetMetricData is a mock of the cloudWatchGetMetricDataAPI seam.
type spyGetMetricData struct {
	gotInputs []*cloudwatch.GetMetricDataInput
	out       *cloudwatch.GetMetricDataOutput
}

func (s *spyGetMetricData) GetMetricData(ctx context.Context, in *cloudwatch.GetMetricDataInput, _ ...func(*cloudwatch.Options)) (*cloudwatch.GetMetricDataOutput, error) {
	s.gotInputs = append(s.gotInputs, in)
	return s.out, nil
}

func TestCollectConnectorMetrics_ReturnsRawResults(t *testing.T) {
	ts := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	val := 12.5
	spy := &spyGetMetricData{
		out: &cloudwatch.GetMetricDataOutput{
			MetricDataResults: []cloudwatchtypes.MetricDataResult{{
				Id:         aws.String("q0"),
				Label:      aws.String("BytesInPerSec (conn-a)"),
				Timestamps: []time.Time{ts},
				Values:     []float64{val},
				StatusCode: cloudwatchtypes.StatusCodeComplete,
			}},
		},
	}
	ms := &MetricService{client: spy}
	tw := types.CloudWatchTimeWindow{StartTime: ts, EndTime: ts.Add(time.Hour), Period: 300}

	got, err := ms.CollectConnectorMetrics(context.Background(), []string{"conn-a"}, tw, "us-east-1")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, int32(300), got.MetricMetadata.Period)
	assert.Equal(t, tw.StartTime, got.MetricMetadata.StartDate)
	require.NotEmpty(t, got.Results)
	assert.Equal(t, "BytesInPerSec (conn-a)", aws.ToString(got.Results[0].Label))
	assert.NotEmpty(t, spy.gotInputs) // GetMetricData actually called

	// Each query info carries a reproducible AWS CLI command + console source JSON
	// (the CloudWatch analog of the self-managed curl).
	require.NotEmpty(t, got.QueryInfo)
	assert.Contains(t, got.QueryInfo[0].AWSCLICommand, "aws cloudwatch get-metric-data")
	assert.Contains(t, got.QueryInfo[0].AWSCLICommand, "us-east-1")
	assert.NotEmpty(t, got.QueryInfo[0].ConsoleSourceJSON)
}

func TestCollectConnectorMetrics_NoConnectorsReturnsEmpty(t *testing.T) {
	ms := &MetricService{client: &spyGetMetricData{out: &cloudwatch.GetMetricDataOutput{}}}
	got, err := ms.CollectConnectorMetrics(context.Background(), nil, types.CloudWatchTimeWindow{Period: 300}, "us-east-1")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Empty(t, got.Results)
}

// echoGetMetricData records how many queries each request carried and returns one
// complete result per input query (echoing its Id), so a caller that batches its
// query slice can be checked for both the per-request cap and result stitching.
type echoGetMetricData struct {
	queriesPerRequest []int
}

func (e *echoGetMetricData) GetMetricData(ctx context.Context, in *cloudwatch.GetMetricDataInput, _ ...func(*cloudwatch.Options)) (*cloudwatch.GetMetricDataOutput, error) {
	e.queriesPerRequest = append(e.queriesPerRequest, len(in.MetricDataQueries))
	results := make([]cloudwatchtypes.MetricDataResult, 0, len(in.MetricDataQueries))
	for _, q := range in.MetricDataQueries {
		results = append(results, cloudwatchtypes.MetricDataResult{
			Id:         q.Id,
			Label:      q.Label,
			Timestamps: []time.Time{aws.ToTime(in.StartTime)},
			Values:     []float64{1},
			StatusCode: cloudwatchtypes.StatusCodeComplete,
		})
	}
	return &cloudwatch.GetMetricDataOutput{MetricDataResults: results}, nil
}

func TestCollectConnectorMetrics_BatchesQueriesUnderCloudWatchCap(t *testing.T) {
	// 72 connectors x 7 metrics = 504 queries, which exceeds CloudWatch's 500-query
	// per-request cap. Collection must split into batches of <=500 and stitch results.
	connectors := make([]string, 72)
	for i := range connectors {
		connectors[i] = fmt.Sprintf("conn-%02d", i)
	}
	totalQueries := len(connectors) * len(connectorMetricStats)
	require.Greater(t, totalQueries, maxQueriesPerRequest) // guards the premise

	spy := &echoGetMetricData{}
	ms := &MetricService{client: spy}
	ts := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	tw := types.CloudWatchTimeWindow{StartTime: ts, EndTime: ts.Add(time.Hour), Period: 300}

	got, err := ms.CollectConnectorMetrics(context.Background(), connectors, tw, "us-east-1")
	require.NoError(t, err)
	require.NotNil(t, got)

	// No single request may exceed the cap, and every query must have been sent.
	require.NotEmpty(t, spy.queriesPerRequest)
	sent := 0
	for _, n := range spy.queriesPerRequest {
		assert.LessOrEqual(t, n, maxQueriesPerRequest, "a request exceeded the CloudWatch query cap")
		sent += n
	}
	assert.Equal(t, totalQueries, sent, "every query must be sent across batches")

	// Results from all batches are stitched together — one per query.
	assert.Len(t, got.Results, totalQueries)
	assert.Len(t, got.QueryInfo, totalQueries)
}
