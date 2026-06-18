package metrics

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	cloudwatchtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
)

// fakeCWClient is an in-memory cloudWatchGetMetricDataAPI for tests.
type fakeCWClient struct {
	calls   []cloudwatch.GetMetricDataInput
	respond func(call int, in *cloudwatch.GetMetricDataInput) (*cloudwatch.GetMetricDataOutput, error)
}

func (f *fakeCWClient) GetMetricData(_ context.Context, in *cloudwatch.GetMetricDataInput, _ ...func(*cloudwatch.Options)) (*cloudwatch.GetMetricDataOutput, error) {
	idx := len(f.calls)
	f.calls = append(f.calls, *in)
	return f.respond(idx, in)
}

func TestFakeClientSatisfiesInterface(t *testing.T) {
	var _ cloudWatchGetMetricDataAPI = &fakeCWClient{}
	ms := &MetricService{client: &fakeCWClient{}}
	if ms.client == nil {
		t.Fatal("expected client to be set")
	}
}

func TestChunkSeconds(t *testing.T) {
	// 100_000 budget / 12 series = 8333 pts; * 60s period = per-chunk seconds.
	if got := chunkSeconds(60, 12); got != int64(100_000/12)*60 {
		t.Errorf("got %d, want %d", got, int64(100_000/12)*60)
	}
	// Unknown estimate or non-positive period => 0 (caller uses full window).
	if got := chunkSeconds(60, 0); got != 0 {
		t.Errorf("seriesEstimate 0 => 0, got %d", got)
	}
	if got := chunkSeconds(0, 12); got != 0 {
		t.Errorf("period 0 => 0, got %d", got)
	}
	// Huge series count clamps to >= 1 point per series (one period).
	if got := chunkSeconds(60, 1_000_000); got != 60 {
		t.Errorf("expected floor of one period (60), got %d", got)
	}
}

func TestSeriesEstimates(t *testing.T) {
	if got := brokerSeriesEstimate(3); got != 4*(3+1) {
		t.Errorf("broker: got %d, want %d", got, 4*(3+1))
	}
	if got := clientConnSeriesEstimate(3); got != 2*(3*maxClientAuthTypes+1) {
		t.Errorf("clientconn: got %d, want %d", got, 2*(3*maxClientAuthTypes+1))
	}
	if got := storageSeriesEstimate(3); got != 3+2 {
		t.Errorf("storage: got %d, want %d", got, 3+2)
	}
}

func TestExecuteWindow_SetsAscendingScanAndPaginates(t *testing.T) {
	fake := &fakeCWClient{
		respond: func(call int, in *cloudwatch.GetMetricDataInput) (*cloudwatch.GetMetricDataOutput, error) {
			if call == 0 {
				return &cloudwatch.GetMetricDataOutput{
					MetricDataResults: []cloudwatchtypes.MetricDataResult{{Id: aws.String("sum_x"), Values: []float64{1}}},
					NextToken:         aws.String("page2"),
				}, nil
			}
			return &cloudwatch.GetMetricDataOutput{
				MetricDataResults: []cloudwatchtypes.MetricDataResult{{Id: aws.String("sum_y"), Values: []float64{2}}},
			}, nil
		},
	}
	ms := &MetricService{client: fake}
	out, err := ms.executeWindow(context.Background(), nil, time.Unix(0, 0), time.Unix(3600, 0))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(fake.calls) != 2 {
		t.Fatalf("expected 2 paginated calls, got %d", len(fake.calls))
	}
	if fake.calls[0].ScanBy != cloudwatchtypes.ScanByTimestampAscending {
		t.Errorf("expected ScanBy ascending, got %v", fake.calls[0].ScanBy)
	}
	if len(out.MetricDataResults) != 2 {
		t.Errorf("expected 2 stitched page results, got %d", len(out.MetricDataResults))
	}
}

func TestResultStitcher_ConcatenatesPerIdPreservingOrder(t *testing.T) {
	s := newResultStitcher()
	t0, t1, t2 := time.Unix(0, 0), time.Unix(60, 0), time.Unix(120, 0)

	s.add([]cloudwatchtypes.MetricDataResult{
		{Id: aws.String("sum_a"), Label: aws.String("A"), Timestamps: []time.Time{t0}, Values: []float64{1}},
		{Id: aws.String("sum_b"), Label: aws.String("B"), Timestamps: []time.Time{t0}, Values: []float64{10}},
	})
	s.add([]cloudwatchtypes.MetricDataResult{
		{Id: aws.String("sum_a"), Timestamps: []time.Time{t1, t2}, Values: []float64{2, 3}},
	})

	out := s.output()
	if len(out.MetricDataResults) != 2 {
		t.Fatalf("expected 2 ids, got %d", len(out.MetricDataResults))
	}
	if aws.ToString(out.MetricDataResults[0].Id) != "sum_a" {
		t.Errorf("expected first-seen order sum_a first, got %s", aws.ToString(out.MetricDataResults[0].Id))
	}
	a := out.MetricDataResults[0]
	if len(a.Values) != 3 || a.Values[0] != 1 || a.Values[2] != 3 {
		t.Errorf("sum_a values not concatenated in order: %v", a.Values)
	}
	if aws.ToString(a.Label) != "A" {
		t.Errorf("expected label preserved, got %s", aws.ToString(a.Label))
	}
}

func TestResultStitcher_MarkPartial(t *testing.T) {
	s := newResultStitcher()
	if s.partial {
		t.Fatal("expected not partial initially")
	}
	s.markPartial()
	if !s.partial {
		t.Error("expected partial after markPartial")
	}
}

// dummyQueries returns a minimal non-empty queries slice for tests where the
// fake client ignores the query content entirely.
func dummyQueries() []cloudwatchtypes.MetricDataQuery {
	return []cloudwatchtypes.MetricDataQuery{{Id: aws.String("q")}}
}

// completeResult returns a single complete series with n points for an Id.
func completeResult(id string, n int) []cloudwatchtypes.MetricDataResult {
	vals := make([]float64, n)
	ts := make([]time.Time, n)
	for i := 0; i < n; i++ {
		vals[i] = float64(i)
		ts[i] = time.Unix(int64(i*60), 0)
	}
	return []cloudwatchtypes.MetricDataResult{{
		Id: aws.String(id), Values: vals, Timestamps: ts, StatusCode: cloudwatchtypes.StatusCodeComplete,
	}}
}

func TestExecuteChunkedQuery_SingleCallWhenUnderBudget(t *testing.T) {
	fake := &fakeCWClient{respond: func(_ int, _ *cloudwatch.GetMetricDataInput) (*cloudwatch.GetMetricDataOutput, error) {
		return &cloudwatch.GetMetricDataOutput{MetricDataResults: completeResult("sum_x", 10)}, nil
	}}
	ms := &MetricService{client: fake}
	start, end := time.Unix(0, 0), time.Unix(600, 0) // 10 points at 60s
	out, err := ms.executeChunkedQuery(context.Background(), dummyQueries(), start, end, 60, 4, "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(fake.calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(fake.calls))
	}
	if len(out.MetricDataResults[0].Values) != 10 {
		t.Errorf("expected 10 stitched values, got %d", len(out.MetricDataResults[0].Values))
	}
}

func TestExecuteChunkedQuery_ChunksAndStaysUnderCap(t *testing.T) {
	// period 60s, seriesEstimate 250 => chunkSeconds = (100000/250)*60 = 24000s (400 pts).
	// total window 96000s (1600 pts) => ceil(96000/24000) = 4 chunks.
	period := int32(60)
	seriesEst := 250
	start := time.Unix(0, 0)
	end := time.Unix(96000, 0)
	cs := chunkSeconds(period, seriesEst)

	fake := &fakeCWClient{respond: func(_ int, in *cloudwatch.GetMetricDataInput) (*cloudwatch.GetMetricDataOutput, error) {
		win := int64(in.EndTime.Sub(*in.StartTime).Seconds())
		if win > cs {
			t.Errorf("chunk window %d exceeds chunkSeconds %d", win, cs)
		}
		return &cloudwatch.GetMetricDataOutput{MetricDataResults: completeResult("sum_x", int(win/int64(period)))}, nil
	}}
	ms := &MetricService{client: fake}
	out, err := ms.executeChunkedQuery(context.Background(), dummyQueries(), start, end, period, seriesEst, "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(fake.calls) != 4 {
		t.Fatalf("expected 4 chunks, got %d", len(fake.calls))
	}
	if got := len(out.MetricDataResults[0].Values); got != 1600 {
		t.Errorf("expected 1600 stitched values, got %d", got)
	}
}

func TestExecuteChunkedQuery_BisectsOnPartialData(t *testing.T) {
	// seriesEstimate 0 => no deterministic chunking; full window returns PartialData,
	// each half returns Complete. Bisection should recover full data.
	start, end := time.Unix(0, 0), time.Unix(120, 0) // 2 periods at 60s
	fake := &fakeCWClient{respond: func(_ int, in *cloudwatch.GetMetricDataInput) (*cloudwatch.GetMetricDataOutput, error) {
		win := int64(in.EndTime.Sub(*in.StartTime).Seconds())
		if win > 60 {
			return &cloudwatch.GetMetricDataOutput{MetricDataResults: []cloudwatchtypes.MetricDataResult{
				{Id: aws.String("sum_x"), StatusCode: cloudwatchtypes.StatusCodePartialData, Values: []float64{0}, Timestamps: []time.Time{time.Unix(60, 0)}},
			}}, nil
		}
		return &cloudwatch.GetMetricDataOutput{MetricDataResults: completeResult("sum_x", 1)}, nil
	}}
	ms := &MetricService{client: fake}
	out, err := ms.executeChunkedQuery(context.Background(), dummyQueries(), start, end, 60, 0, "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(fake.calls) != 3 {
		t.Fatalf("expected 3 calls (1 partial + 2 halves), got %d", len(fake.calls))
	}
	if got := len(out.MetricDataResults[0].Values); got != 2 {
		t.Errorf("expected 2 recovered values, got %d", got)
	}
}

func TestExecuteChunkedQuery_WarnsAtFloor(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
	defer slog.SetDefault(prev)

	fake := &fakeCWClient{respond: func(_ int, _ *cloudwatch.GetMetricDataInput) (*cloudwatch.GetMetricDataOutput, error) {
		return &cloudwatch.GetMetricDataOutput{MetricDataResults: []cloudwatchtypes.MetricDataResult{
			{Id: aws.String("sum_x"), StatusCode: cloudwatchtypes.StatusCodePartialData, Values: []float64{0}, Timestamps: []time.Time{time.Unix(0, 0)}},
		}}, nil
	}}
	ms := &MetricService{client: fake}
	_, err := ms.executeChunkedQuery(context.Background(), dummyQueries(), time.Unix(0, 0), time.Unix(60, 0), 60, 0, "broker metrics for cluster c1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "metrics may be incomplete") || !strings.Contains(buf.String(), "broker metrics for cluster c1") {
		t.Errorf("expected one incomplete-metrics warning mentioning the label, got: %q", buf.String())
	}
}

func TestExecuteChunkedQuery_EmptyQueriesReturnsEmpty(t *testing.T) {
	fake := &fakeCWClient{respond: func(_ int, _ *cloudwatch.GetMetricDataInput) (*cloudwatch.GetMetricDataOutput, error) {
		t.Fatal("client should not be called for empty queries")
		return nil, nil
	}}
	ms := &MetricService{client: fake}
	out, err := ms.executeChunkedQuery(context.Background(), nil, time.Unix(0, 0), time.Unix(600, 0), 60, 4, "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out.MetricDataResults) != 0 || len(fake.calls) != 0 {
		t.Errorf("expected no calls and empty results, got %d calls / %d results", len(fake.calls), len(out.MetricDataResults))
	}
}
