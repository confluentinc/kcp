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

// TestChunkingConstantsDefined verifies the chunking constants are declared with
// the expected values. The constants are consumed by chunking logic added in
// later tasks; this test keeps them reachable so the linter does not flag them.
func TestChunkingConstantsDefined(t *testing.T) {
	const wantBudget = 100_000
	if datapointBudget != wantBudget {
		t.Errorf("datapointBudget = %d, want %d", datapointBudget, wantBudget)
	}
	const wantAuthTypes = 4
	if maxClientAuthTypes != wantAuthTypes {
		t.Errorf("maxClientAuthTypes = %d, want %d", maxClientAuthTypes, wantAuthTypes)
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
