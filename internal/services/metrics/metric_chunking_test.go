package metrics

import (
	"context"
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
