package client_inventory

// Tests for the CloudWatch Logs Insights source. Insights does the filtering and
// de-duplication server-side, so KCP downloads one representative line per distinct
// client instead of every TRACE event. These tests pin that the rows it returns
// feed the SAME gate + parser as the S3 and FilterLogEvents paths, that the query
// lifecycle (start, poll, terminal states) is handled, and that a result set the
// service truncated is reported instead of silently producing a partial inventory.

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	cwltypes "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"
)

// fakeInsights is a fake insightsAPI. statuses is returned one per GetQueryResults
// call (the last one repeats), so polling through Scheduled/Running is exercised.
type fakeInsights struct {
	statuses []cwltypes.QueryStatus
	lines    []string
	started  *cloudwatchlogs.StartQueryInput
	getCalls int
}

func (f *fakeInsights) StartQuery(_ context.Context, in *cloudwatchlogs.StartQueryInput, _ ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.StartQueryOutput, error) {
	f.started = in
	return &cloudwatchlogs.StartQueryOutput{QueryId: aws.String("q-1")}, nil
}

func (f *fakeInsights) GetQueryResults(_ context.Context, in *cloudwatchlogs.GetQueryResultsInput, _ ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.GetQueryResultsOutput, error) {
	idx := f.getCalls
	if idx >= len(f.statuses) {
		idx = len(f.statuses) - 1
	}
	f.getCalls++
	status := f.statuses[idx]
	out := &cloudwatchlogs.GetQueryResultsOutput{Status: status}
	if status == cwltypes.QueryStatusComplete {
		for _, l := range f.lines {
			out.Results = append(out.Results, []cwltypes.ResultField{
				{Field: aws.String("k_cid"), Value: aws.String("whatever")},
				{Field: aws.String("line"), Value: aws.String(l)},
			})
		}
	}
	return out, nil
}

func newTestInsightsSource(api insightsAPI) *cloudWatchInsightsLogSource {
	start, end := cwWindow()
	src := NewCloudWatchInsightsLogSource(api, "kcp", start, end)
	src.pollInterval = 0
	return src
}

func TestInsightsSource_RowsFeedExistingParser(t *testing.T) {
	fake := &fakeInsights{
		statuses: []cwltypes.QueryStatus{cwltypes.QueryStatusComplete},
		lines:    []string{realIAMProduceLine, realSCRAMFetchLine},
	}
	src := newTestInsightsSource(fake)

	content, err := src.ReadChunk(context.Background(), LogChunk{ID: "kcp"})
	if err != nil {
		t.Fatalf("ReadChunk: %v", err)
	}
	got := parseViaScanner(content)
	if len(got) != 2 {
		t.Fatalf("want 2 parsed requests from 2 rows, got %d", len(got))
	}
	ids := map[string]bool{}
	for _, md := range got {
		ids[md.ClientId] = true
	}
	if !ids["TESTING_PRODUCER-1"] || !ids["glenns_consumer_with_sasl_scram"] {
		t.Errorf("missing a client: %+v", got)
	}
}

func TestInsightsSource_StartQueryRequest(t *testing.T) {
	fake := &fakeInsights{statuses: []cwltypes.QueryStatus{cwltypes.QueryStatusComplete}}
	src := newTestInsightsSource(fake)

	if _, err := src.ReadChunk(context.Background(), LogChunk{ID: "kcp"}); err != nil {
		t.Fatalf("ReadChunk: %v", err)
	}
	in := fake.started
	if in == nil {
		t.Fatal("StartQuery was not called")
	}
	if aws.ToString(in.LogGroupName) != "kcp" {
		t.Errorf("LogGroupName = %q, want kcp", aws.ToString(in.LogGroupName))
	}
	// Insights takes epoch SECONDS, unlike FilterLogEvents' milliseconds.
	if aws.ToInt64(in.StartTime) != src.start.Unix() || aws.ToInt64(in.EndTime) != src.end.Unix() {
		t.Errorf("window = [%d,%d], want [%d,%d]", aws.ToInt64(in.StartTime), aws.ToInt64(in.EndTime), src.start.Unix(), src.end.Unix())
	}
	// The default Limit is 1000 rows; the service maximum must be requested.
	if aws.ToInt32(in.Limit) != insightsMaxRows {
		t.Errorf("Limit = %d, want %d", aws.ToInt32(in.Limit), insightsMaxRows)
	}
	if aws.ToString(in.QueryString) != kafkaApiInsightsQuery {
		t.Errorf("QueryString is not the package query")
	}
}

// The de-duplication key must never be coarser than KCP's composite key
// (clientId|topic|role|auth|principal), or distinct clients would collapse into one
// representative line and be lost. Pin every component the parser reads.
func TestInsightsQuery_GroupsByEveryCompositeKeyComponent(t *testing.T) {
	for _, want := range []string{
		"apiKey=",              // role
		"clientId=",            // clientId
		"FetchTopic",           // consumer topic
		"partitionSizes",       // producer topic
		`principal:\[IAM\]`,    // IAM principal
		"securityProtocol:SSL", // TLS principal
		"principal:(?<",        // SCRAM / anonymous principal
		"stats latest(@message)",
	} {
		if !strings.Contains(kafkaApiInsightsQuery, want) {
			t.Errorf("query is missing %q:\n%s", want, kafkaApiInsightsQuery)
		}
	}
}

func TestInsightsSource_PollsUntilComplete(t *testing.T) {
	fake := &fakeInsights{
		statuses: []cwltypes.QueryStatus{cwltypes.QueryStatusScheduled, cwltypes.QueryStatusRunning, cwltypes.QueryStatusComplete},
		lines:    []string{realIAMProduceLine},
	}
	src := newTestInsightsSource(fake)

	content, err := src.ReadChunk(context.Background(), LogChunk{ID: "kcp"})
	if err != nil {
		t.Fatalf("ReadChunk: %v", err)
	}
	if fake.getCalls != 3 {
		t.Errorf("want 3 GetQueryResults polls, got %d", fake.getCalls)
	}
	if got := parseViaScanner(content); len(got) != 1 {
		t.Fatalf("want 1 client once complete, got %d", len(got))
	}
}

func TestInsightsSource_TerminalFailureStatusesAreErrors(t *testing.T) {
	for _, status := range []cwltypes.QueryStatus{cwltypes.QueryStatusFailed, cwltypes.QueryStatusCancelled, cwltypes.QueryStatusTimeout} {
		fake := &fakeInsights{statuses: []cwltypes.QueryStatus{status}}
		src := newTestInsightsSource(fake)

		if _, err := src.ReadChunk(context.Background(), LogChunk{ID: "kcp"}); err == nil {
			t.Errorf("status %s: want an error, got nil", status)
		}
	}
}

func TestInsightsSource_EmptyResultIsNotAnError(t *testing.T) {
	fake := &fakeInsights{statuses: []cwltypes.QueryStatus{cwltypes.QueryStatusComplete}}
	src := newTestInsightsSource(fake)

	content, err := src.ReadChunk(context.Background(), LogChunk{ID: "kcp"})
	if err != nil {
		t.Fatalf("ReadChunk: %v", err)
	}
	if got := parseViaScanner(content); len(got) != 0 {
		t.Fatalf("want 0 clients, got %d", len(got))
	}
}

// Insights silently caps a result at the requested Limit. A capped result may be
// missing clients, so it must fail loudly and tell the user to narrow the window.
func TestInsightsSource_ResultAtLimitIsAnError(t *testing.T) {
	lines := make([]string, insightsMaxRows)
	for i := range lines {
		lines[i] = fmt.Sprintf("line-%d", i)
	}
	fake := &fakeInsights{statuses: []cwltypes.QueryStatus{cwltypes.QueryStatusComplete}, lines: lines}
	src := newTestInsightsSource(fake)

	_, err := src.ReadChunk(context.Background(), LogChunk{ID: "kcp"})
	if err == nil {
		t.Fatal("want an error when the result hit the row limit")
	}
	if !strings.Contains(err.Error(), "narrow") {
		t.Errorf("error should tell the user to narrow the window, got: %v", err)
	}
}

func TestInsightsSource_HonoursContextCancellationWhilePolling(t *testing.T) {
	fake := &fakeInsights{statuses: []cwltypes.QueryStatus{cwltypes.QueryStatusRunning}}
	src := newTestInsightsSource(fake)
	src.pollInterval = time.Millisecond

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	if _, err := src.ReadChunk(ctx, LogChunk{ID: "kcp"}); err == nil {
		t.Fatal("want an error when the context ends while the query is still running")
	}
}
