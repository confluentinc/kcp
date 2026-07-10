package metrics

import (
	"context"
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
