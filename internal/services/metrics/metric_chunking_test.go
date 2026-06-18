package metrics

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
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
