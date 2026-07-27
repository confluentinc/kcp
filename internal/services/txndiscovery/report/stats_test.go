package report

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/confluentinc/kcp/internal/services/txndiscovery/discovery"
	"github.com/confluentinc/kcp/internal/services/txndiscovery/tail"
)

// writeStats writes the stats document for r into a fresh temp dir and returns its path
// and its parsed body.
func writeStats(t *testing.T, r Run) (string, map[string]any) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "txn-discovery-stats.json")
	if err := WriteStatsJSON(path, r); err != nil {
		t.Fatalf("write stats: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read stats: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("stats is not valid JSON: %v\n--- stats ---\n%s", err, raw)
	}
	return path, doc
}

// at walks a JSON document by key, failing the test when a step is missing.
func at(t *testing.T, doc map[string]any, path ...string) any {
	t.Helper()
	var cur any = doc
	for i, key := range path {
		m, ok := cur.(map[string]any)
		if !ok {
			t.Fatalf("%v is not an object at step %q", path[:i], key)
		}
		cur, ok = m[key]
		if !ok {
			t.Fatalf("stats document has no %v (keys at that level: %v)", path[:i+1], keysOf(m))
		}
	}
	return cur
}

func keysOf(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// num reads a JSON number as a float, failing on anything else.
func num(t *testing.T, v any) float64 {
	t.Helper()
	f, ok := v.(float64)
	if !ok {
		t.Fatalf("expected a number, got %T (%v)", v, v)
	}
	return f
}

// statsRun exercises every counter the keep-up signal is built from, with distinct
// values so a document that wires the wrong field is caught rather than passing on a
// coincidence.
func statsRun() Run {
	return Run{
		Duration:      2 * time.Minute,
		Interval:      20 * time.Second,
		ActiveSources: []string{discovery.SourceTxnStateLog, discovery.SourceConsumerOffsets},
		Tail: tail.Stats{
			PartitionsAssigned: 2,
			PartitionsRunning:  1,
			RecordsRead:        1300,
			Lag:                77,
			OpenTxnBacklog:     11,
			AbortedBatches:     5,
			DecodeErrors:       3,
			LeadershipErrors:   2,
			UnclassifiedErrors: 1,
			OffsetResets:       4,
			TransportErrors:    6,
			Partitions: []tail.PartitionStats{
				{
					Topic: "__transaction_state", Partition: 7,
					NextOffset: 900, LastStableOffset: 977, HighWaterMark: 988,
					Lag: 77, OpenTxnBacklog: 11, Running: true,
					RecordsRead: 1000, AbortedBatches: 5, DecodeErrors: 3,
				},
				{
					Topic: "__consumer_offsets", Partition: 12,
					NextOffset: 300, LastStableOffset: 300, HighWaterMark: 300,
					Lag: 0, OpenTxnBacklog: 0, Running: false,
					RecordsRead: 300, LastError: "kafka server: Not the leader",
				},
			},
		},
		TxnState: discovery.TxnStateStats{
			RecordsSeen: 1000, Footprints: 40, Empty: 3, Committed: 30, Aborted: 2, Tombstones: 1,
			KeyDecodeErrors: 8, ValueDecodeErrors: 9,
		},
		Offsets: discovery.ConsumerOffsetsStats{
			RecordsSeen: 300, TxnRecords: 25, KeyDecodeErrors: 6,
			PendingProducers: 2, PendingEvicted: 14,
			GroupsLinked: 3, Correlations: 4,
			RecoveredTopics: []string{"in-a", "in-b"},
		},
	}
}

func TestStatsJSONCarriesRecordsReadLagAndPerSourceDecodeFailures(t *testing.T) {
	_, doc := statsRun2Doc(t)

	// Records read, per source and in aggregate.
	if got := num(t, at(t, doc, "tail", "records_read")); got != 1300 {
		t.Errorf("tail.records_read = %v, want 1300", got)
	}
	if got := num(t, at(t, doc, "transaction_state_log", "records_seen")); got != 1000 {
		t.Errorf("transaction_state_log.records_seen = %v, want 1000", got)
	}
	if got := num(t, at(t, doc, "consumer_offsets_log", "records_seen")); got != 300 {
		t.Errorf("consumer_offsets_log.records_seen = %v, want 300", got)
	}

	// Lag: aggregate, and per partition (KTD9 — measured to the last stable offset,
	// with the high-watermark gap reported separately).
	if got := num(t, at(t, doc, "tail", "lag")); got != 77 {
		t.Errorf("tail.lag = %v, want 77", got)
	}
	if got := num(t, at(t, doc, "tail", "open_txn_backlog")); got != 11 {
		t.Errorf("tail.open_txn_backlog = %v, want 11", got)
	}
	parts, ok := at(t, doc, "tail", "partitions").([]any)
	if !ok || len(parts) != 2 {
		t.Fatalf("tail.partitions = %v, want 2 entries", at(t, doc, "tail", "partitions"))
	}
	p0, _ := parts[0].(map[string]any)
	if got := num(t, p0["lag"]); got != 77 {
		t.Errorf("partitions[0].lag = %v, want 77", got)
	}
	if got := num(t, p0["last_stable_offset"]); got != 977 {
		t.Errorf("partitions[0].last_stable_offset = %v, want 977", got)
	}
	if got := num(t, p0["next_offset"]); got != 900 {
		t.Errorf("partitions[0].next_offset = %v, want 900", got)
	}
	if running, _ := p0["running"].(bool); !running {
		t.Errorf("partitions[0].running = %v, want true", p0["running"])
	}
	p1, _ := parts[1].(map[string]any)
	if running, _ := p1["running"].(bool); running {
		t.Errorf("partitions[1].running = %v, want false", p1["running"])
	}

	// Decode failures, kept per source: the summary aggregates them, but which half
	// of which record drifted is what makes the alarm actionable.
	if got := num(t, at(t, doc, "tail", "decode_errors")); got != 3 {
		t.Errorf("tail.decode_errors = %v, want 3", got)
	}
	if got := num(t, at(t, doc, "transaction_state_log", "key_decode_errors")); got != 8 {
		t.Errorf("transaction_state_log.key_decode_errors = %v, want 8", got)
	}
	if got := num(t, at(t, doc, "transaction_state_log", "value_decode_errors")); got != 9 {
		t.Errorf("transaction_state_log.value_decode_errors = %v, want 9", got)
	}
	if got := num(t, at(t, doc, "consumer_offsets_log", "key_decode_errors")); got != 6 {
		t.Errorf("consumer_offsets_log.key_decode_errors = %v, want 6", got)
	}
}

func TestWriteStatsJSONFailsActionablyWhenTheOutputDirectoryDoesNotExist(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "no-such-dir")

	err := WriteStatsJSON(filepath.Join(missing, "txn-discovery-stats.json"), statsRun())
	if err == nil {
		t.Fatal("writing into a nonexistent directory succeeded")
	}
	for _, want := range []string{missing, "does not exist"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
	entries, readErr := os.ReadDir(dir)
	if readErr != nil {
		t.Fatalf("read temp dir: %v", readErr)
	}
	if len(entries) != 0 {
		t.Errorf("failed write left %d entries behind: %v", len(entries), entries)
	}
}

func TestKeepUpDetailBreaksDownEverySourceAndEveryPartition(t *testing.T) {
	var buf bytes.Buffer
	PrintKeepUp(&buf, statsRun())
	out := buf.String()

	for _, want := range []string{
		// Aggregate fetch state, including the error taxonomy the health line reduces
		// to one number.
		"fetch: 1/2 partitions live, 1300 records read, lag 77, open-txn backlog 11",
		"3 decode, 2 leadership, 1 unclassified, 4 offset reset, 6 transport",
		// Per partition, so a stalled one is identifiable rather than merely counted.
		"__transaction_state[7]: next 900, LSO 977, HWM 988, lag 77, backlog 11, 1000 records, live",
		"__consumer_offsets[12]: next 300, LSO 300, HWM 300, lag 0, backlog 0, 300 records, STOPPED",
		"last error: kafka server: Not the leader",
		// Per source.
		"transaction-state reader: 1000 records, 40 footprints, 30 committed / 2 aborted, 1 tombstone",
		"decode failures: 8 key, 9 value",
		"consumer-offsets tail: 300 records, 25 txn offset-commits, 3 groups linked, 2 input topics recovered",
		"14 pending evictions",
		"6 key decode failures",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("keep-up detail is missing %q\n--- keep-up ---\n%s", want, out)
		}
	}
}

func statsRun2Doc(t *testing.T) (string, map[string]any) {
	t.Helper()
	return writeStats(t, statsRun())
}
