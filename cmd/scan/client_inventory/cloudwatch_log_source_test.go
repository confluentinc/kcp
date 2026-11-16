package client_inventory

// Tests for the CloudWatch Logs source, which reads broker logs through a Logs
// Insights query: CloudWatch filters and de-duplicates server-side so KCP downloads
// one representative TRACE line per distinct client. They prove the rows feed the
// SAME gate + parser the S3 path uses (parseViaScanner, defined in logsource_test.go),
// that the query lifecycle (start, poll, terminal states) is handled, and that a
// result the service truncated is reported instead of yielding a partial inventory.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	cwltypes "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"
	"github.com/confluentinc/kcp/internal/types"
)

// fakeInsights is a fake cwLogsAPI. statuses is returned one per GetQueryResults
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

func (f *fakeInsights) GetQueryResults(_ context.Context, _ *cloudwatchlogs.GetQueryResultsInput, _ ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.GetQueryResultsOutput, error) {
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

// completeWith returns a fake whose query completes immediately with the given lines.
func completeWith(lines ...string) *fakeInsights {
	return &fakeInsights{statuses: []cwltypes.QueryStatus{cwltypes.QueryStatusComplete}, lines: lines}
}

// REAL SCRAM fetch fixture (verbatim from kafka_trace_line_parser_test.go).
const realSCRAMFetchLine = `[2025-08-08 07:30:42,834] TRACE [KafkaApi-1] Handling request:RequestHeader(apiKey=FETCH, apiVersion=11, clientId=glenns_consumer_with_sasl_scram, correlationId=2010, headerVersion=1) -- FetchRequestData(clusterId=null, replicaId=-1, replicaState=ReplicaState(replicaId=-1, replicaEpoch=-1), maxWaitMs=500, minBytes=1, maxBytes=104857600, isolationLevel=0, sessionId=0, sessionEpoch=-1, topics=[FetchTopic(topic='test-topic-1', topicId=AAAAAAAAAAAAAAAAAAAAAA, partitions=[FetchPartition(partition=2, currentLeaderEpoch=0, fetchOffset=20419, lastFetchedEpoch=-1, logStartOffset=0, partitionMaxBytes=1048576), FetchPartition(partition=5, currentLeaderEpoch=0, fetchOffset=20258, lastFetchedEpoch=-1, logStartOffset=0, partitionMaxBytes=1048576)])], forgottenTopicsData=[], rackId='') from connection INTERNAL_IP-INTERNAL_IP-11-5;securityProtocol:SASL_SSL,principal:User:kafka-user-2 (kafka.server.KafkaApis)`

// A config-dump line of the kind seen in CloudWatch before TRACE was enabled. The
// query filters these out server-side, but the gate must still skip them if one
// ever reaches KCP.
const cloudWatchConfigDumpLine = `fetch.max.bytes = 52428800`

func cwWindow() (time.Time, time.Time) {
	end := time.Now()
	return end.Add(-time.Hour), end
}

func newTestSource(api cwLogsAPI) *cloudWatchLogSource {
	start, end := cwWindow()
	src := NewCloudWatchLogSource(api, "kcp", start, end)
	src.pollInterval = 0
	return src
}

func TestCloudWatchLogSource_NameAndSingleChunk(t *testing.T) {
	src := newTestSource(completeWith())
	if src.Name() != "kcp" {
		t.Errorf("Name = %q, want kcp", src.Name())
	}
	chunks, err := src.ListChunks(context.Background())
	if err != nil {
		t.Fatalf("ListChunks: %v", err)
	}
	if len(chunks) != 1 {
		t.Fatalf("want 1 chunk, got %d", len(chunks))
	}
}

func TestCloudWatchLogSource_FeedsExistingParser_IAMProduce(t *testing.T) {
	src := newTestSource(completeWith(realIAMProduceLine))

	content, err := src.ReadChunk(context.Background(), LogChunk{ID: "kcp"})
	if err != nil {
		t.Fatalf("ReadChunk: %v", err)
	}
	got := parseViaScanner(content)
	if len(got) != 1 {
		t.Fatalf("want 1 parsed request, got %d", len(got))
	}
	md := got[0]
	if md.ClientId != "TESTING_PRODUCER-1" || md.ApiKey != "PRODUCE" || md.Role != "Producer" || md.Topic != "customers1" || md.Auth != AuthTypeIAM {
		t.Errorf("unexpected IAM produce metadata: %+v", md)
	}
	wantPrincipal := "arn:aws:sts::635910096382:assumed-role/AWSReservedSSO_nonprod-administrator_b3955bd58a347b7b/me@confluent.io"
	if md.Principal != wantPrincipal {
		t.Errorf("Principal = %q, want %q", md.Principal, wantPrincipal)
	}
}

func TestCloudWatchLogSource_FeedsExistingParser_SCRAMFetch(t *testing.T) {
	src := newTestSource(completeWith(realSCRAMFetchLine))

	content, err := src.ReadChunk(context.Background(), LogChunk{ID: "kcp"})
	if err != nil {
		t.Fatalf("ReadChunk: %v", err)
	}
	got := parseViaScanner(content)
	if len(got) != 1 {
		t.Fatalf("want 1 parsed request, got %d", len(got))
	}
	md := got[0]
	if md.ClientId != "glenns_consumer_with_sasl_scram" || md.Role != "Consumer" || md.Topic != "test-topic-1" || md.Auth != AuthTypeSASL_SCRAM || md.Principal != "User:kafka-user-2" {
		t.Errorf("unexpected SCRAM fetch metadata: %+v", md)
	}
}

func TestCloudWatchLogSource_MultipleRows(t *testing.T) {
	src := newTestSource(completeWith(realIAMProduceLine, realSCRAMFetchLine))

	content, err := src.ReadChunk(context.Background(), LogChunk{ID: "kcp"})
	if err != nil {
		t.Fatalf("ReadChunk: %v", err)
	}
	got := parseViaScanner(content)
	ids := map[string]bool{}
	for _, md := range got {
		ids[md.ClientId] = true
	}
	if len(got) != 2 || !ids["TESTING_PRODUCER-1"] || !ids["glenns_consumer_with_sasl_scram"] {
		t.Errorf("want both clients, got %+v", got)
	}
}

// Non-TRACE lines are skipped by the gate, identically to S3, even if one slips
// past the server-side filter.
func TestCloudWatchLogSource_SkipsNonTraceLines(t *testing.T) {
	src := newTestSource(completeWith(cloudWatchConfigDumpLine, realIAMProduceLine, "fetch.max.wait.ms = 500"))

	content, err := src.ReadChunk(context.Background(), LogChunk{ID: "kcp"})
	if err != nil {
		t.Fatalf("ReadChunk: %v", err)
	}
	got := parseViaScanner(content)
	if len(got) != 1 || got[0].ClientId != "TESTING_PRODUCER-1" {
		t.Fatalf("want exactly the 1 real client, got %+v", got)
	}
}

func TestCloudWatchLogSource_StartQueryRequest(t *testing.T) {
	fake := completeWith()
	src := newTestSource(fake)

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
	// Insights takes epoch SECONDS (FilterLogEvents took milliseconds).
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

func TestCloudWatchLogSource_PollsUntilComplete(t *testing.T) {
	fake := &fakeInsights{
		statuses: []cwltypes.QueryStatus{cwltypes.QueryStatusScheduled, cwltypes.QueryStatusRunning, cwltypes.QueryStatusComplete},
		lines:    []string{realIAMProduceLine},
	}
	src := newTestSource(fake)

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

func TestCloudWatchLogSource_TerminalFailureStatusesAreErrors(t *testing.T) {
	for _, status := range []cwltypes.QueryStatus{cwltypes.QueryStatusFailed, cwltypes.QueryStatusCancelled, cwltypes.QueryStatusTimeout} {
		src := newTestSource(&fakeInsights{statuses: []cwltypes.QueryStatus{status}})

		if _, err := src.ReadChunk(context.Background(), LogChunk{ID: "kcp"}); err == nil {
			t.Errorf("status %s: want an error, got nil", status)
		}
	}
}

func TestCloudWatchLogSource_EmptyResultIsNotAnError(t *testing.T) {
	src := newTestSource(completeWith())

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
func TestCloudWatchLogSource_ResultAtLimitIsAnError(t *testing.T) {
	lines := make([]string, insightsMaxRows)
	for i := range lines {
		lines[i] = fmt.Sprintf("line-%d", i)
	}
	src := newTestSource(completeWith(lines...))

	_, err := src.ReadChunk(context.Background(), LogChunk{ID: "kcp"})
	if err == nil {
		t.Fatal("want an error when the result hit the row limit")
	}
	if !strings.Contains(err.Error(), "narrow") {
		t.Errorf("error should tell the user to narrow the window, got: %v", err)
	}
}

func TestCloudWatchLogSource_HonoursContextCancellationWhilePolling(t *testing.T) {
	src := newTestSource(&fakeInsights{statuses: []cwltypes.QueryStatus{cwltypes.QueryStatusRunning}})
	src.pollInterval = time.Millisecond

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	if _, err := src.ReadChunk(ctx, LogChunk{ID: "kcp"}); err == nil {
		t.Fatal("want an error when the context ends while the query is still running")
	}
}

// An empty window must be a clean skip: Run returns nil, no "cluster not found"
// error, and no state file is written — even when the cluster is not yet in state.
// (CloudWatch always lists one chunk, so this only holds if Run short-circuits on
// zero discovered clients.)
func TestRun_CloudWatchEmptyWindow_CleanSkipNoUpsert(t *testing.T) {
	src := newTestSource(completeWith())

	stateFilePath := filepath.Join(t.TempDir(), "kcp-state.json")
	scanner, err := NewClientInventoryScanner(src, types.State{}, ClientInventoryScannerOpts{
		Region:       "us-east-1",
		ClusterName:  "not-yet-in-state",
		StateFile:    stateFilePath,
		LogGroupName: "kcp",
	})
	if err != nil {
		t.Fatalf("NewClientInventoryScanner: %v", err)
	}

	if err := scanner.Run(); err != nil {
		t.Fatalf("empty CloudWatch window should be a clean skip, got error: %v", err)
	}
	if _, statErr := os.Stat(stateFilePath); !os.IsNotExist(statErr) {
		t.Errorf("expected no state file written on empty-window skip, but one exists")
	}
}

// Regression from a live run: MSK/CloudWatch truncate very large TRACE response
// lines ("Sending topic metadata ...") at exactly 64 KiB. Once a "\n" is appended
// that is 65537 bytes, which exceeded bufio.Scanner's default token limit and
// aborted the WHOLE chunk, losing every client. An over-long line must be skipped,
// not poison the lines around it.
func TestRun_OverlongLineDoesNotAbortChunk(t *testing.T) {
	prefix := "[2026-11-04 08:42:49,956] TRACE [KafkaApi-3] Sending topic metadata MetadataResponseTopic(errorCode=0, name='x'"
	for _, size := range []int{65536, 1 << 20} { // the observed AWS cap, and well beyond it
		overlong := prefix + strings.Repeat("a", size-len(prefix))
		src := newTestSource(completeWith(overlong, realIAMProduceLine, overlong, realSCRAMFetchLine))

		state := types.State{MSKSources: &types.MSKSourcesState{Regions: []types.DiscoveredRegion{{
			Name:     "us-east-1",
			Clusters: []types.DiscoveredCluster{{Name: "my-cluster"}},
		}}}}
		scanner, err := NewClientInventoryScanner(src, state, ClientInventoryScannerOpts{
			Region: "us-east-1", ClusterName: "my-cluster", LogGroupName: "kcp",
			StateFile: filepath.Join(t.TempDir(), "kcp-state.json"),
		})
		if err != nil {
			t.Fatalf("NewClientInventoryScanner: %v", err)
		}
		if err := scanner.Run(); err != nil {
			t.Fatalf("size %d: Run returned error: %v", size, err)
		}
		got := state.MSKSources.Regions[0].Clusters[0].DiscoveredClients
		if len(got) != 2 {
			t.Fatalf("size %d: want the 2 real clients around the over-long lines, got %d", size, len(got))
		}
	}
}
