package client_inventory

// Tests for the CloudWatch Logs source. They prove the bytes it produces feed
// the SAME gate + parser the S3 path uses (parseViaScanner, defined in
// logsource_test.go) and yield identical client identities from REAL broker
// TRACE fixture lines — plus pagination and empty-window behaviour.

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	cwltypes "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"
)

// fakeCW is a fake cwLogsAPI. Each entry in pages is one FilterLogEvents page;
// it threads a NextToken ("page-N") so multi-page reads are exercised. calls
// counts invocations so pagination can be asserted.
type fakeCW struct {
	pages [][]string
	calls int
}

func (f *fakeCW) FilterLogEvents(_ context.Context, in *cloudwatchlogs.FilterLogEventsInput, _ ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.FilterLogEventsOutput, error) {
	f.calls++
	idx := 0
	if in.NextToken != nil {
		_, _ = fmt.Sscanf(*in.NextToken, "page-%d", &idx)
	}
	if idx >= len(f.pages) {
		return &cloudwatchlogs.FilterLogEventsOutput{}, nil
	}
	events := make([]cwltypes.FilteredLogEvent, 0, len(f.pages[idx]))
	for _, m := range f.pages[idx] {
		m := m
		events = append(events, cwltypes.FilteredLogEvent{Message: aws.String(m)})
	}
	var next *string
	if idx+1 < len(f.pages) {
		tok := fmt.Sprintf("page-%d", idx+1)
		next = &tok
	}
	return &cloudwatchlogs.FilterLogEventsOutput{Events: events, NextToken: next}, nil
}

// REAL SCRAM fetch fixture (verbatim from kafka_trace_line_parser_test.go).
const realSCRAMFetchLine = `[2025-08-08 07:30:42,834] TRACE [KafkaApi-1] Handling request:RequestHeader(apiKey=FETCH, apiVersion=11, clientId=glenns_consumer_with_sasl_scram, correlationId=2010, headerVersion=1) -- FetchRequestData(clusterId=null, replicaId=-1, replicaState=ReplicaState(replicaId=-1, replicaEpoch=-1), maxWaitMs=500, minBytes=1, maxBytes=104857600, isolationLevel=0, sessionId=0, sessionEpoch=-1, topics=[FetchTopic(topic='test-topic-1', topicId=AAAAAAAAAAAAAAAAAAAAAA, partitions=[FetchPartition(partition=2, currentLeaderEpoch=0, fetchOffset=20419, lastFetchedEpoch=-1, logStartOffset=0, partitionMaxBytes=1048576), FetchPartition(partition=5, currentLeaderEpoch=0, fetchOffset=20258, lastFetchedEpoch=-1, logStartOffset=0, partitionMaxBytes=1048576)])], forgottenTopicsData=[], rackId='') from connection INTERNAL_IP-INTERNAL_IP-11-5;securityProtocol:SASL_SSL,principal:User:kafka-user-2 (kafka.server.KafkaApis)`

// A config-dump line like the ones observed flowing through CloudWatch BEFORE
// TRACE was (re)enabled — must be skipped by the gate, exactly as on S3.
const cloudWatchConfigDumpLine = `fetch.max.bytes = 52428800`

func cwWindow() (time.Time, time.Time) {
	end := time.Now()
	return end.Add(-time.Hour), end
}

func TestCloudWatchLogSource_FeedsExistingParser_IAMProduce(t *testing.T) {
	start, end := cwWindow()
	fake := &fakeCW{pages: [][]string{{realIAMProduceLine}}}
	src := NewCloudWatchLogSource(fake, "kcp", start, end)

	chunks, err := src.ListChunks(context.Background())
	if err != nil {
		t.Fatalf("ListChunks: %v", err)
	}
	if len(chunks) != 1 {
		t.Fatalf("want 1 chunk, got %d", len(chunks))
	}

	content, err := src.ReadChunk(context.Background(), chunks[0])
	if err != nil {
		t.Fatalf("ReadChunk: %v", err)
	}

	got := parseViaScanner(content)
	if len(got) != 1 {
		t.Fatalf("want 1 parsed request, got %d", len(got))
	}
	md := got[0]
	if md.ClientId != "TESTING_PRODUCER-1" {
		t.Errorf("ClientId = %q, want TESTING_PRODUCER-1", md.ClientId)
	}
	if md.ApiKey != "PRODUCE" {
		t.Errorf("ApiKey = %q, want PRODUCE", md.ApiKey)
	}
	if md.Role != "Producer" {
		t.Errorf("Role = %q, want Producer", md.Role)
	}
	if md.Topic != "customers1" {
		t.Errorf("Topic = %q, want customers1", md.Topic)
	}
	if md.Auth != AuthTypeIAM {
		t.Errorf("Auth = %q, want %q", md.Auth, AuthTypeIAM)
	}
	wantPrincipal := "arn:aws:sts::635910096382:assumed-role/AWSReservedSSO_nonprod-administrator_b3955bd58a347b7b/me@confluent.io"
	if md.Principal != wantPrincipal {
		t.Errorf("Principal = %q, want %q", md.Principal, wantPrincipal)
	}
}

func TestCloudWatchLogSource_FeedsExistingParser_SCRAMFetch(t *testing.T) {
	start, end := cwWindow()
	fake := &fakeCW{pages: [][]string{{realSCRAMFetchLine}}}
	src := NewCloudWatchLogSource(fake, "kcp", start, end)

	content, err := src.ReadChunk(context.Background(), LogChunk{ID: "kcp"})
	if err != nil {
		t.Fatalf("ReadChunk: %v", err)
	}
	got := parseViaScanner(content)
	if len(got) != 1 {
		t.Fatalf("want 1 parsed request, got %d", len(got))
	}
	md := got[0]
	if md.ClientId != "glenns_consumer_with_sasl_scram" {
		t.Errorf("ClientId = %q", md.ClientId)
	}
	if md.Role != "Consumer" {
		t.Errorf("Role = %q, want Consumer", md.Role)
	}
	if md.Topic != "test-topic-1" {
		t.Errorf("Topic = %q, want test-topic-1", md.Topic)
	}
	if md.Auth != AuthTypeSASL_SCRAM {
		t.Errorf("Auth = %q, want %q", md.Auth, AuthTypeSASL_SCRAM)
	}
	if md.Principal != "User:kafka-user-2" {
		t.Errorf("Principal = %q, want User:kafka-user-2", md.Principal)
	}
}

// Proves the non-trace lines actually seen in CloudWatch are skipped, identically
// to S3 — only real TRACE request lines become clients.
func TestCloudWatchLogSource_SkipsNonTraceLines(t *testing.T) {
	start, end := cwWindow()
	fake := &fakeCW{pages: [][]string{{
		cloudWatchConfigDumpLine,
		realIAMProduceLine,
		"fetch.max.wait.ms = 500",
	}}}
	src := NewCloudWatchLogSource(fake, "kcp", start, end)

	content, err := src.ReadChunk(context.Background(), LogChunk{ID: "kcp"})
	if err != nil {
		t.Fatalf("ReadChunk: %v", err)
	}
	got := parseViaScanner(content)
	if len(got) != 1 {
		t.Fatalf("want exactly 1 real client (junk skipped), got %d", len(got))
	}
	if got[0].ClientId != "TESTING_PRODUCER-1" {
		t.Errorf("ClientId = %q", got[0].ClientId)
	}
}

// Review Focus #1: a window whose events span multiple FilterLogEvents pages
// must read ALL events (follow NextToken), not just page one.
func TestCloudWatchLogSource_PagesAllEvents(t *testing.T) {
	start, end := cwWindow()
	fake := &fakeCW{pages: [][]string{{realIAMProduceLine}, {realSCRAMFetchLine}}}
	src := NewCloudWatchLogSource(fake, "kcp", start, end)

	content, err := src.ReadChunk(context.Background(), LogChunk{ID: "kcp"})
	if err != nil {
		t.Fatalf("ReadChunk: %v", err)
	}
	if fake.calls != 2 {
		t.Errorf("expected 2 FilterLogEvents calls (pagination), got %d", fake.calls)
	}
	got := parseViaScanner(content)
	if len(got) != 2 {
		t.Fatalf("want 2 clients across 2 pages, got %d", len(got))
	}
	ids := map[string]bool{}
	for _, md := range got {
		ids[md.ClientId] = true
	}
	if !ids["TESTING_PRODUCER-1"] || !ids["glenns_consumer_with_sasl_scram"] {
		t.Errorf("missing a client across pages: %+v", got)
	}
}

// Review Focus #4: an empty window (no matching events) yields no clients and no
// error — the scanner then treats it like S3's "no log files found" skip.
func TestCloudWatchLogSource_EmptyWindow(t *testing.T) {
	start, end := cwWindow()
	fake := &fakeCW{pages: [][]string{{}}}
	src := NewCloudWatchLogSource(fake, "kcp", start, end)

	content, err := src.ReadChunk(context.Background(), LogChunk{ID: "kcp"})
	if err != nil {
		t.Fatalf("ReadChunk: %v", err)
	}
	got := parseViaScanner(content)
	if len(got) != 0 {
		t.Fatalf("want 0 clients for empty window, got %d", len(got))
	}
}
