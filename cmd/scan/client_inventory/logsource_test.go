package client_inventory

// Tests for the source-neutral LogSource abstraction. They prove:
//  1. The existing S3 path still yields identical client identities behind the
//     shared LogSource interface (this file).
//  2. (cloudwatch_log_source tests, same package) the CloudWatch source feeds
//     the SAME gate + parser and yields identical identities.
//
// parseViaScanner and the REAL fixture lines below are shared by both.

import (
	"bufio"
	"bytes"
	"context"
	"testing"
)

// parseViaScanner runs bytes through the SAME gate + parser the real scanner
// uses in handleLogFile (KafkaApiTracePattern, then KafkaApiTraceLineParser.Parse).
// A source is correct iff the bytes it produces parse to the same clients S3 would.
func parseViaScanner(content []byte) []RequestMetadata {
	parser := &KafkaApiTraceLineParser{}
	var out []RequestMetadata
	sc := bufio.NewScanner(bytes.NewReader(content))
	for sc.Scan() {
		line := sc.Text()
		if !KafkaApiTracePattern.MatchString(line) {
			continue
		}
		md, err := parser.Parse(line, 0, "test-source")
		if err != nil {
			continue
		}
		out = append(out, *md)
	}
	return out
}

// REAL fixture lines (verbatim from kafka_trace_line_parser_test.go), reused so
// the tests prove behaviour against lines we have actually observed from brokers.
const realIAMProduceLine = `[2025-07-25 14:45:53,662] TRACE [KafkaApi-1] Handling request:RequestHeader(apiKey=PRODUCE, apiVersion=7, clientId=TESTING_PRODUCER-1, correlationId=2, headerVersion=1) -- {acks=1,timeout=10000,partitionSizes=[customers1-0=107]} from connection INTERNAL_IP-INTERNAL_IP-11-5;securityProtocol:SASL_SSL,principal:[IAM]:[arn:aws:sts::635910096382:assumed-role/AWSReservedSSO_nonprod-administrator_b3955bd58a347b7b/me@confluent.io]:[INTERNAL_IP-INTERNAL_IP-11-5]:[00079d61-baba-497e-87c2-80c46608f1da] (kafka.server.KafkaApis)`

// --- S3 fake (satisfies the existing S3Service interface) --------------------

type fakeS3Svc struct {
	keys    []string
	content map[string][]byte
}

func (f *fakeS3Svc) ParseS3URI(string) (string, string, error) { return "bucket", "prefix/", nil }
func (f *fakeS3Svc) ListLogFiles(context.Context, string, string) ([]string, error) {
	return f.keys, nil
}
func (f *fakeS3Svc) DownloadAndDecompressLogFile(_ context.Context, _, key string) ([]byte, error) {
	return f.content[key], nil
}

// Proves the EXISTING S3 path still works, now behind the shared LogSource
// interface: list chunks, read a chunk, parse it to the same client.
func TestS3LogSource_StillWorks_SameInterface(t *testing.T) {
	fake := &fakeS3Svc{
		keys:    []string{"a.log.gz"},
		content: map[string][]byte{"a.log.gz": []byte(realIAMProduceLine + "\n")},
	}

	var src LogSource
	src, err := NewS3LogSource(fake, "s3://bucket/prefix/")
	if err != nil {
		t.Fatalf("NewS3LogSource: %v", err)
	}

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
	if len(got) != 1 || got[0].ClientId != "TESTING_PRODUCER-1" {
		t.Fatalf("S3 path broken: %+v", got)
	}
}
