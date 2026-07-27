package report

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/confluentinc/kcp/internal/services/txndiscovery/discovery"
)

// readLines returns the raw newline-delimited records currently on disk at path.
func readLines(t *testing.T, path string) []string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open audit log: %v", err)
	}
	defer func() { _ = f.Close() }()

	var out []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		out = append(out, sc.Text())
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scan audit log: %v", err)
	}
	return out
}

// decodeLines parses every line on disk into an AuditLine, failing the test if any
// line is not independently valid JSON.
func decodeLines(t *testing.T, path string) []AuditLine {
	t.Helper()
	raw := readLines(t, path)
	out := make([]AuditLine, 0, len(raw))
	for i, line := range raw {
		var al AuditLine
		if err := json.Unmarshal([]byte(line), &al); err != nil {
			t.Fatalf("line %d is not valid JSON: %v\nline: %q", i, err, line)
		}
		out = append(out, al)
	}
	return out
}

func TestRecordWritesOneLineNamingTheAddedTopicsAndTheResultingSet(t *testing.T) {
	path := filepath.Join(t.TempDir(), "txn-discovery-audit.jsonl")

	w, err := NewAuditWriter(path)
	if err != nil {
		t.Fatalf("NewAuditWriter: %v", err)
	}
	defer func() { _ = w.Close() }()

	obs := discovery.Observation{
		TxnID:      "orders-tx-0",
		ProducerID: 42,
		Topics:     []string{"orders", "payments"},
		Source:     discovery.SourceTxnStateLog,
		ObservedAt: time.Date(2026, 7, 27, 10, 0, 0, 0, time.UTC),
	}
	ch := discovery.Change{
		Added:  []string{"orders", "payments"},
		Topics: []string{"orders", "payments"},
	}
	w.Record(obs, ch)

	lines := decodeLines(t, path)
	if len(lines) != 1 {
		t.Fatalf("want 1 audit line, got %d", len(lines))
	}
	got := lines[0]
	if got.TxnID != "orders-tx-0" {
		t.Errorf("transactional_id: want %q, got %q", "orders-tx-0", got.TxnID)
	}
	if want := []string{"orders", "payments"}; !equal(got.Added, want) {
		t.Errorf("added: want %v, got %v", want, got.Added)
	}
	if want := []string{"orders", "payments"}; !equal(got.Topics, want) {
		t.Errorf("topics: want %v, got %v", want, got.Topics)
	}
	if !got.Timestamp.Equal(obs.ObservedAt) {
		t.Errorf("timestamp: want %v, got %v", obs.ObservedAt, got.Timestamp)
	}
}

func TestRecordWritesNoLineWhenTheObservationAddedNothing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "txn-discovery-audit.jsonl")

	w, err := NewAuditWriter(path)
	if err != nil {
		t.Fatalf("NewAuditWriter: %v", err)
	}
	defer func() { _ = w.Close() }()

	obs := discovery.Observation{
		TxnID:      "orders-tx-0",
		Topics:     []string{"orders", "payments"},
		Source:     discovery.SourceTxnStateLog,
		ObservedAt: time.Date(2026, 7, 27, 10, 0, 1, 0, time.UTC),
	}
	// The transaction was already seen touching both topics, so nothing was added.
	ch := discovery.Change{Added: nil, Topics: []string{"orders", "payments"}}
	w.Record(obs, ch)

	if lines := readLines(t, path); len(lines) != 0 {
		t.Fatalf("want no audit lines for a non-growing observation, got %d: %q", len(lines), lines)
	}
}

// feed mirrors the wiring U11 uses: every observation goes through the accumulator,
// and the change it reports drives the audit line.
func feed(w *AuditWriter, acc *discovery.Accumulator, obs discovery.Observation) {
	w.Record(obs, acc.Add(obs))
}

func TestRecordWritesOnlyTheNewlyAddedTopicAcrossASequence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "txn-discovery-audit.jsonl")

	w, err := NewAuditWriter(path)
	if err != nil {
		t.Fatalf("NewAuditWriter: %v", err)
	}
	defer func() { _ = w.Close() }()

	acc := discovery.NewAccumulator()
	base := time.Date(2026, 7, 27, 10, 0, 0, 0, time.UTC)

	// 1. Introduces two topics.
	feed(w, acc, discovery.Observation{
		TxnID: "orders-tx-0", Topics: []string{"orders", "payments"},
		Source: discovery.SourceTxnStateLog, ObservedAt: base,
	})
	// 2. Re-reports the same footprint: no growth, no line.
	feed(w, acc, discovery.Observation{
		TxnID: "orders-tx-0", Topics: []string{"payments", "orders"},
		Source: discovery.SourceTxnStateLog, ObservedAt: base.Add(time.Second),
	})
	// 3. Introduces one more.
	feed(w, acc, discovery.Observation{
		TxnID: "orders-tx-0", Topics: []string{"orders", "payments", "shipments"},
		Source: discovery.SourceTxnStateLog, ObservedAt: base.Add(2 * time.Second),
	})

	lines := decodeLines(t, path)
	if len(lines) != 2 {
		t.Fatalf("want 2 audit lines across the sequence, got %d", len(lines))
	}
	if want := []string{"orders", "payments"}; !equal(lines[0].Added, want) {
		t.Errorf("line 0 added: want %v, got %v", want, lines[0].Added)
	}
	if want := []string{"shipments"}; !equal(lines[1].Added, want) {
		t.Errorf("line 1 added: want %v, got %v", want, lines[1].Added)
	}
	if want := []string{"orders", "payments", "shipments"}; !equal(lines[1].Topics, want) {
		t.Errorf("line 1 topics: want %v, got %v", want, lines[1].Topics)
	}
}

// failingWriter stands in for a full disk or an unmounted volume: the descriptor is
// open, but every write fails.
type failingWriter struct {
	err   error
	calls int
}

func (f *failingWriter) Write(p []byte) (int, error) {
	f.calls++
	return 0, f.err
}

func TestRecordCountsWriteFailuresRatherThanDroppingThemSilently(t *testing.T) {
	captureLogs(t) // the writer warns on the first failure; keep it out of the test output
	path := filepath.Join(t.TempDir(), "txn-discovery-audit.jsonl")

	w, err := NewAuditWriter(path)
	if err != nil {
		t.Fatalf("NewAuditWriter: %v", err)
	}
	defer func() { _ = w.Close() }()

	if got := w.Errors(); got != 0 {
		t.Fatalf("want 0 errors on a fresh writer, got %d", got)
	}

	// The volume goes away mid-run.
	w.sink = &failingWriter{err: errors.New("no space left on device")}

	for i := range 3 {
		w.Record(discovery.Observation{
			TxnID:      "orders-tx-0",
			Source:     discovery.SourceTxnStateLog,
			ObservedAt: time.Date(2026, 7, 27, 10, 0, i, 0, time.UTC),
		}, discovery.Change{Added: []string{"t"}, Topics: []string{"t"}})
	}

	// A dropped line reads downstream as "no transaction coupled these topics", which
	// is indistinguishable from a clean run. The count is what makes it distinguishable.
	if got := w.Errors(); got != 3 {
		t.Fatalf("want 3 counted write failures, got %d", got)
	}
}

func TestCloseCountsAndReturnsAFailedClose(t *testing.T) {
	path := filepath.Join(t.TempDir(), "txn-discovery-audit.jsonl")

	w, err := NewAuditWriter(path)
	if err != nil {
		t.Fatalf("NewAuditWriter: %v", err)
	}

	// Pull the descriptor out from under the writer. On buffered and network
	// filesystems the deferred write error surfaces at close, not at write, so a close
	// that fails means lines are missing just as surely as a failed write does.
	if err := w.f.Close(); err != nil {
		t.Fatalf("pre-close: %v", err)
	}

	if err := w.Close(); err == nil {
		t.Errorf("want Close to return the failure, got nil")
	}
	if got := w.Errors(); got != 1 {
		t.Fatalf("want the failed close counted, got %d errors", got)
	}
}

// countingWriter records how many Write calls each line takes.
type countingWriter struct {
	writes int
	buf    bytes.Buffer
}

func (c *countingWriter) Write(p []byte) (int, error) {
	c.writes++
	return c.buf.Write(p)
}

func TestEachLineIsEmittedWithASingleWriteCall(t *testing.T) {
	path := filepath.Join(t.TempDir(), "txn-discovery-audit.jsonl")

	w, err := NewAuditWriter(path)
	if err != nil {
		t.Fatalf("NewAuditWriter: %v", err)
	}
	defer func() { _ = w.Close() }()

	cw := &countingWriter{}
	w.sink = cw

	const records = 3
	for i := range records {
		w.Record(discovery.Observation{
			TxnID:      "orders-tx-0",
			Source:     discovery.SourceTxnStateLog,
			ObservedAt: time.Date(2026, 7, 27, 10, 0, i, 0, time.UTC),
		}, discovery.Change{Added: []string{"t"}, Topics: []string{"t"}})
	}

	// The record and its newline go out together. If they were separate writes a crash
	// could land between them, leaving a line with no terminator — which glues two
	// records into one unparseable line rather than losing only the trailing one.
	if cw.writes != records {
		t.Fatalf("want %d writes for %d records, got %d", records, records, cw.writes)
	}
}

func TestLinesAreOnDiskBeforeTheWriterIsClosed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "txn-discovery-audit.jsonl")

	w, err := NewAuditWriter(path)
	if err != nil {
		t.Fatalf("NewAuditWriter: %v", err)
	}
	defer func() { _ = w.Close() }()

	w.Record(discovery.Observation{
		TxnID: "orders-tx-0", Source: discovery.SourceTxnStateLog,
		ObservedAt: time.Date(2026, 7, 27, 10, 0, 0, 0, time.UTC),
	}, discovery.Change{Added: []string{"orders"}, Topics: []string{"orders"}})

	// Read through a separate handle, with the writer still open. The audit log is a
	// live trace: an operator tails it during a run that lasts hours, and a run killed
	// by SIGKILL must still leave everything observed up to that point on disk.
	// Buffering would make both false.
	lines := decodeLines(t, path)
	if len(lines) != 1 {
		t.Fatalf("want the line readable before Close, got %d lines — output is buffered", len(lines))
	}
	if lines[0].TxnID != "orders-tx-0" {
		t.Errorf("transactional_id: want %q, got %q", "orders-tx-0", lines[0].TxnID)
	}
}

// recoverLines re-reads the audit log the way a consumer of a crashed run's file must:
// every line that parses is kept, and a damaged one is skipped rather than aborting the
// read. It returns the recovered records and the number of unparseable lines.
func recoverLines(t *testing.T, path string) ([]AuditLine, int) {
	t.Helper()
	var out []AuditLine
	damaged := 0
	for _, line := range readLines(t, path) {
		var al AuditLine
		if err := json.Unmarshal([]byte(line), &al); err != nil {
			damaged++
			continue
		}
		out = append(out, al)
	}
	return out, damaged
}

func TestATruncatedTrailingLineDoesNotInvalidateThePrecedingLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "txn-discovery-audit.jsonl")

	w, err := NewAuditWriter(path)
	if err != nil {
		t.Fatalf("NewAuditWriter: %v", err)
	}
	acc := discovery.NewAccumulator()
	base := time.Date(2026, 7, 27, 10, 0, 0, 0, time.UTC)
	for i, topic := range []string{"orders", "payments", "shipments"} {
		feed(w, acc, discovery.Observation{
			TxnID: "orders-tx-0", Topics: []string{topic},
			Source: discovery.SourceTxnStateLog, ObservedAt: base.Add(time.Duration(i) * time.Second),
		})
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// Chop the file ten bytes into the third record, as a SIGKILL or a full disk during
	// the third write would.
	full, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read audit log: %v", err)
	}
	thirdRecordStart := 0
	seen := 0
	for i, b := range full {
		if b == '\n' {
			seen++
			if seen == 2 {
				thirdRecordStart = i + 1
				break
			}
		}
	}
	cut := thirdRecordStart + 10
	if cut >= len(full) {
		t.Fatalf("test setup: cut %d is not inside the third record (file is %d bytes)", cut, len(full))
	}
	if err := os.Truncate(path, int64(cut)); err != nil {
		t.Fatalf("truncate: %v", err)
	}

	recovered, damaged := recoverLines(t, path)
	if damaged != 1 {
		t.Errorf("want exactly the trailing record damaged, got %d damaged lines", damaged)
	}
	if len(recovered) != 2 {
		t.Fatalf("want the 2 complete records still readable after truncation, got %d", len(recovered))
	}
	if want := []string{"orders"}; !equal(recovered[0].Added, want) {
		t.Errorf("record 0 added: want %v, got %v", want, recovered[0].Added)
	}
	if want := []string{"payments"}; !equal(recovered[1].Added, want) {
		t.Errorf("record 1 added: want %v, got %v", want, recovered[1].Added)
	}
}

func TestTheSourceFieldDistinguishesWhichPhaseProducedTheEdge(t *testing.T) {
	path := filepath.Join(t.TempDir(), "txn-discovery-audit.jsonl")

	w, err := NewAuditWriter(path)
	if err != nil {
		t.Fatalf("NewAuditWriter: %v", err)
	}
	defer func() { _ = w.Close() }()

	acc := discovery.NewAccumulator()
	base := time.Date(2026, 7, 27, 10, 0, 0, 0, time.UTC)

	// The transaction log sees only what the transaction produced to.
	feed(w, acc, discovery.Observation{
		TxnID: "orders-tx-0", Topics: []string{"orders-out"},
		Source: discovery.SourceTxnStateLog, ObservedAt: base,
	})
	// The consumer-group enrichment phase recovers the input it consumed from. That is
	// a weaker inference than the log, so an operator questioning why these two topics
	// were coupled has to be able to tell which phase asserted the edge.
	feed(w, acc, discovery.Observation{
		TxnID: "orders-tx-0", Topics: []string{"orders-in"}, ReadProcessWrite: true,
		Source: discovery.SourceConsumerGroups, ObservedAt: base.Add(time.Second),
	})

	lines := decodeLines(t, path)
	if len(lines) != 2 {
		t.Fatalf("want 2 audit lines, got %d", len(lines))
	}
	if lines[0].Source != discovery.SourceTxnStateLog {
		t.Errorf("line 0 source: want %q, got %q", discovery.SourceTxnStateLog, lines[0].Source)
	}
	if lines[1].Source != discovery.SourceConsumerGroups {
		t.Errorf("line 1 source: want %q, got %q", discovery.SourceConsumerGroups, lines[1].Source)
	}
}

// TestHostileTopicNamesStayOnOneParseableLine is the audit-log-injection case. A topic
// name is attacker-influenced data as far as this file is concerned: whoever can create
// a topic on the cluster chooses the bytes that end up in it.
//
// The critical property is that one record occupies exactly one physical line. A raw
// newline inside a name would end the record early and let the remainder of the name be
// read as a second, forged audit record — a way to plant a coupling that was never
// observed, or to hide a real one by making the surrounding lines unparseable.
func TestHostileTopicNamesStayOnOneParseableLine(t *testing.T) {
	// A name carrying a complete, well-formed audit record after a newline: if
	// newlines were not escaped, this single topic would forge a second record.
	const forgery = "orders\n" +
		`{"timestamp":"2000-01-01T00:00:00Z","transactional_id":"forged","source":"transaction-state-log","added":["never-observed"],"topics":["never-observed"]}`

	tests := []struct {
		name  string
		topic string
		want  string // what must come back out; differs from topic only for lossy input
	}{
		{"double quote", `orders"; DROP`, `orders"; DROP`},
		{"backslash", `orders\payments`, `orders\payments`},
		{"newline forging a second record", forgery, forgery},
		{"carriage return", "orders\rpayments", "orders\rpayments"},
		{"nul byte", "orders\x00payments", "orders\x00payments"},
		// encoding/json substitutes U+FFFD for invalid UTF-8 rather than failing. See
		// the package comment on why that is the right trade for this artifact.
		{"invalid utf-8", "orders\xff\xfepayments", "orders��payments"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "txn-discovery-audit.jsonl")
			w, err := NewAuditWriter(path)
			if err != nil {
				t.Fatalf("NewAuditWriter: %v", err)
			}
			defer func() { _ = w.Close() }()

			w.Record(discovery.Observation{
				TxnID:      "orders-tx-0",
				Source:     discovery.SourceTxnStateLog,
				ObservedAt: time.Date(2026, 7, 27, 10, 0, 0, 0, time.UTC),
			}, discovery.Change{Added: []string{tc.topic}, Topics: []string{tc.topic}})

			if got := w.Errors(); got != 0 {
				t.Fatalf("the line must be written, not dropped; got %d errors", got)
			}

			// Exactly one physical line: no forged second record, no split.
			raw := readLines(t, path)
			if len(raw) != 1 {
				t.Fatalf("want 1 physical line, got %d — a name broke out of its record: %q", len(raw), raw)
			}

			lines := decodeLines(t, path)
			if got := lines[0].Added[0]; got != tc.want {
				t.Errorf("added[0]: want %q, got %q", tc.want, got)
			}
			if lines[0].TxnID != "orders-tx-0" {
				t.Errorf("transactional_id was displaced by the payload: got %q", lines[0].TxnID)
			}
		})
	}
}

// alwaysFailWriter fails every write and holds no state of its own, so it adds no race
// of its own to the one under test.
type alwaysFailWriter struct{}

func (alwaysFailWriter) Write(p []byte) (int, error) {
	return 0, errors.New("no space left on device")
}

// The three observation sources run concurrently, and Accumulator.Add releases its lock
// before returning the Change, so Record is genuinely called from several goroutines at
// once. These two run under -race.

func TestConcurrentRecordsEachProduceOneIntactLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "txn-discovery-audit.jsonl")
	w, err := NewAuditWriter(path)
	if err != nil {
		t.Fatalf("NewAuditWriter: %v", err)
	}
	defer func() { _ = w.Close() }()

	const sources, perSource = 8, 60
	var wg sync.WaitGroup
	for s := range sources {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range perSource {
				w.Record(discovery.Observation{
					TxnID:      fmt.Sprintf("txn-%d", s),
					Source:     discovery.SourceTxnStateLog,
					ObservedAt: time.Date(2026, 7, 27, 10, 0, 0, 0, time.UTC),
				}, discovery.Change{
					Added:  []string{fmt.Sprintf("topic-%d-%d", s, i)},
					Topics: []string{fmt.Sprintf("topic-%d-%d", s, i)},
				})
			}
		}()
	}
	wg.Wait()

	// decodeLines fails the test on any line that is not independently valid JSON, so
	// an interleaved or torn write shows up here.
	lines := decodeLines(t, path)
	if want := sources * perSource; len(lines) != want {
		t.Fatalf("want %d intact lines, got %d", want, len(lines))
	}
}

func TestConcurrentWriteFailuresAreAllCounted(t *testing.T) {
	captureLogs(t) // the writer warns on the first failure; keep it out of the test output
	path := filepath.Join(t.TempDir(), "txn-discovery-audit.jsonl")
	w, err := NewAuditWriter(path)
	if err != nil {
		t.Fatalf("NewAuditWriter: %v", err)
	}
	defer func() { _ = w.Close() }()

	w.sink = alwaysFailWriter{}

	const sources, perSource = 8, 60
	var wg sync.WaitGroup
	for s := range sources {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range perSource {
				w.Record(discovery.Observation{
					TxnID:      fmt.Sprintf("txn-%d", s),
					Source:     discovery.SourceTxnStateLog,
					ObservedAt: time.Date(2026, 7, 27, 10, 0, i%60, 0, time.UTC),
				}, discovery.Change{Added: []string{"t"}, Topics: []string{"t"}})
			}
		}()
	}
	wg.Wait()

	// A lost increment here means the summary under-reports how much of the trace is
	// missing, which is the failure this count exists to prevent.
	if want, got := sources*perSource, w.Errors(); got != want {
		t.Fatalf("want %d counted failures, got %d", want, got)
	}
}

// R18: --no-audit-log is expressed as the absence of a writer. Every method tolerating
// a nil receiver means the caller wires one branch, not a nil check at each call site
// where a forgotten one would panic mid-run.
func TestANilWriterIsASafeNoOp(t *testing.T) {
	var w *AuditWriter

	w.Record(discovery.Observation{
		TxnID: "orders-tx-0", Source: discovery.SourceTxnStateLog,
		ObservedAt: time.Date(2026, 7, 27, 10, 0, 0, 0, time.UTC),
	}, discovery.Change{Added: []string{"orders"}, Topics: []string{"orders"}})

	if got := w.Errors(); got != 0 {
		t.Errorf("Errors on a disabled writer: want 0, got %d", got)
	}
	if got := w.Path(); got != "" {
		t.Errorf("Path on a disabled writer: want %q, got %q", "", got)
	}
	if err := w.Close(); err != nil {
		t.Errorf("Close on a disabled writer: want nil, got %v", err)
	}
}

// captureLogs redirects the default slog logger at Debug+ for the duration of the test
// and returns the accumulated output. Debug+ because kcp.log's file leg has no level
// control: every level lands in the file operators attach to support tickets.
func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	buf := &bytes.Buffer{}
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return buf
}

func TestAWriteFailureIsWarnedAboutWithoutLeakingNamesIntoTheLog(t *testing.T) {
	const (
		secretTopic = "acme-super-secret-ledger"
		secretTxnID = "acme-payments-tx-7"
	)
	buf := captureLogs(t)

	path := filepath.Join(t.TempDir(), "txn-discovery-audit.jsonl")
	w, err := NewAuditWriter(path)
	if err != nil {
		t.Fatalf("NewAuditWriter: %v", err)
	}
	defer func() { _ = w.Close() }()

	w.sink = alwaysFailWriter{}
	for i := range 3 {
		w.Record(discovery.Observation{
			TxnID:      secretTxnID,
			Source:     discovery.SourceTxnStateLog,
			ObservedAt: time.Date(2026, 7, 27, 10, 0, i, 0, time.UTC),
		}, discovery.Change{Added: []string{secretTopic}, Topics: []string{secretTopic}})
	}

	out := buf.String()
	if out == "" {
		t.Fatalf("a failed audit write must reach kcp.log; nothing was logged")
	}
	if !strings.Contains(out, "WARN") {
		t.Errorf("the failure must be logged at Warn so it reaches the console too; got:\n%s", out)
	}
	if !strings.Contains(out, path) {
		t.Errorf("the warning should name the path so it is actionable; got:\n%s", out)
	}
	// The audit file carries names by design. kcp.log must not: it is unconditionally
	// Debug+ and is what operators attach to support tickets.
	if strings.Contains(out, secretTopic) {
		t.Errorf("topic name leaked into the log:\n%s", out)
	}
	if strings.Contains(out, secretTxnID) {
		t.Errorf("transactional id leaked into the log:\n%s", out)
	}

	// Warn once, not once per line: a full disk fails every write, and thousands of
	// identical warnings would bury the rest of the run's log.
	if got := strings.Count(out, "WARN"); got != 1 {
		t.Errorf("want exactly 1 warning across 3 failures, got %d:\n%s", got, out)
	}
}

// TestFilteringTheAuditLogToATopicYieldsTheTransactionThatCoupledIt is the unit's
// acceptance case: the whole point of the file is that, months later, an operator who
// asks "why is this topic in that migration batch?" can answer it from the trace.
func TestFilteringTheAuditLogToATopicYieldsTheTransactionThatCoupledIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "txn-discovery-audit.jsonl")
	w, err := NewAuditWriter(path)
	if err != nil {
		t.Fatalf("NewAuditWriter: %v", err)
	}

	acc := discovery.NewAccumulator()
	base := time.Date(2026, 7, 27, 10, 0, 0, 0, time.UTC)
	at := func(n int) time.Time { return base.Add(time.Duration(n) * time.Second) }

	// A realistic run: three transactions, several sources, repeated sightings.
	feed(w, acc, discovery.Observation{TxnID: "orders-tx", ProducerID: 1001,
		Topics: []string{"orders-out"}, Source: discovery.SourceTxnStateLog, ObservedAt: at(0)})
	feed(w, acc, discovery.Observation{TxnID: "billing-tx", ProducerID: 1002,
		Topics: []string{"ledger", "__consumer_offsets"}, ReadProcessWrite: true,
		Source: discovery.SourceTxnStateLog, ObservedAt: at(1)})
	// Repeated sighting: no growth, no line.
	feed(w, acc, discovery.Observation{TxnID: "billing-tx", ProducerID: 1002,
		Topics: []string{"ledger"}, Source: discovery.SourceTxnStateLog, ObservedAt: at(2)})
	// The enrichment phase recovers what billing-tx consumed — this is the edge that
	// pulls "invoices" into the same migration batch as "ledger".
	feed(w, acc, discovery.Observation{TxnID: "billing-tx",
		Topics: []string{"invoices"}, ReadProcessWrite: true,
		Source: discovery.SourceConsumerGroups, ObservedAt: at(3)})
	feed(w, acc, discovery.Observation{TxnID: "audit-tx", ProducerID: 1003,
		Topics: []string{"audit-events"}, Source: discovery.SourceTxnStateLog, ObservedAt: at(4)})

	if err := w.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if got := w.Errors(); got != 0 {
		t.Fatalf("want a clean run, got %d errors", got)
	}

	// The operator's question: "why is `invoices` grouped with `ledger`?"
	// The filter: every line whose topic set mentions `invoices`.
	const query = "invoices"
	var hits []AuditLine
	for _, al := range decodeLines(t, path) {
		for _, topic := range al.Topics {
			if topic == query {
				hits = append(hits, al)
				break
			}
		}
	}

	if len(hits) != 1 {
		t.Fatalf("want exactly 1 audit line mentioning %q, got %d", query, len(hits))
	}
	hit := hits[0]
	if hit.TxnID != "billing-tx" {
		t.Errorf("the coupling transaction: want %q, got %q", "billing-tx", hit.TxnID)
	}
	// The line names the phase that asserted the edge...
	if hit.Source != discovery.SourceConsumerGroups {
		t.Errorf("coupling source: want %q, got %q", discovery.SourceConsumerGroups, hit.Source)
	}
	// ...what that phase contributed...
	if want := []string{"invoices"}; !equal(hit.Added, want) {
		t.Errorf("added: want %v, got %v", want, hit.Added)
	}
	// ...and the resulting union, which is the group `invoices` landed in.
	if want := []string{"__consumer_offsets", "invoices", "ledger"}; !equal(hit.Topics, want) {
		t.Errorf("resulting set: want %v, got %v", want, hit.Topics)
	}
	// The unrelated transactions are not implicated.
	if hit.TxnID == "orders-tx" || hit.TxnID == "audit-tx" {
		t.Errorf("filter matched an unrelated transaction: %q", hit.TxnID)
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
