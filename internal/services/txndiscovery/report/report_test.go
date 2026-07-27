package report

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/confluentinc/kcp/internal/services/txndiscovery/discovery"
	"github.com/confluentinc/kcp/internal/services/txndiscovery/grouping"
	"github.com/confluentinc/kcp/internal/services/txndiscovery/tail"
	"github.com/goccy/go-yaml"
)

// render runs the terminal summary for r and returns what an operator would see.
func render(t *testing.T, r Run) string {
	t.Helper()
	var buf bytes.Buffer
	PrintTerminal(&buf, Summarize(r))
	return buf.String()
}

// requireContains fails with the whole rendered summary attached, because a missing
// line is only diagnosable next to the output that was produced instead.
func requireContains(t *testing.T, out, want string) {
	t.Helper()
	if !strings.Contains(out, want) {
		t.Errorf("summary is missing %q\n--- summary ---\n%s", want, out)
	}
}

func TestSummaryRendersTheRunCounts(t *testing.T) {
	out := render(t, Run{
		Duration:      30 * time.Second,
		Interval:      10 * time.Second,
		ActiveSources: []string{discovery.SourceTxnStateLog, discovery.SourceConsumerGroups},
		Footprints: []discovery.TxnFootprint{
			{TxnID: "txn-a"}, {TxnID: "txn-b"}, {TxnID: "txn-c"}, {TxnID: "txn-d"},
		},
		TxnState: discovery.TxnStateStats{RecordsSeen: 12, Committed: 3, Aborted: 1},
		Result: grouping.Result{
			Groups: []grouping.Group{
				{Name: "group-1", Topics: []string{"alpha", "beta", "gamma"}, TxnIDs: []string{"txn-a", "txn-b"}},
				{Name: "group-2", Topics: []string{"delta", "epsilon"}, TxnIDs: []string{"txn-c"}},
			},
			IndividualTopics: []string{"zeta"},
		},
	})

	requireContains(t, out, "window                 : 30s (enrichment interval 10s)")
	requireContains(t, out, "transactions read      : 12 record(s) on the transaction-state log, 4 transactional id(s) observed")
	requireContains(t, out, "transactions           : 3 committed, 1 aborted")
	requireContains(t, out, "topics                 : 6 total — 5 in 2 groups, 1 individual")
	requireContains(t, out, "read-process-write     : 0 groups; consumed inputs recovered for 0 transactions")
}

// distinctiveRun is a run whose every topic name and transactional id is a token that
// cannot occur in the summary's prose by accident, so a leak is unambiguous.
func distinctiveRun() Run {
	return Run{
		Duration:      90 * time.Second,
		Interval:      15 * time.Second,
		ActiveSources: []string{discovery.SourceTxnStateLog, discovery.SourceConsumerGroups, discovery.SourceConsumerOffsets},
		Footprints: []discovery.TxnFootprint{
			{
				TxnID:            "zqx-txnid-ledger-77",
				ProducerID:       4001,
				Topics:           []string{"zqx-topic-payments", "zqx-topic-ledger"},
				ReadProcessWrite: true,
				Sources:          []string{discovery.SourceTxnStateLog, discovery.SourceConsumerOffsets},
			},
			{
				TxnID:      "zqx-txnid-audit-12",
				ProducerID: 4002,
				Topics:     []string{"zqx-topic-audit"},
				Sources:    []string{discovery.SourceTxnStateLog},
			},
		},
		TxnState: discovery.TxnStateStats{RecordsSeen: 40, Committed: 9, Aborted: 2},
		Offsets: discovery.ConsumerOffsetsStats{
			RecordsSeen:     120,
			TxnRecords:      6,
			GroupsLinked:    1,
			Correlations:    1,
			RecoveredTopics: []string{"zqx-topic-orders-in"},
		},
		EnrichmentActive: true,
		Result: grouping.Result{
			Groups: []grouping.Group{
				{
					Name:             "group-1",
					Topics:           []string{"zqx-topic-ledger", "zqx-topic-orders-in", "zqx-topic-payments"},
					TxnIDs:           []string{"zqx-txnid-ledger-77"},
					ReadProcessWrite: true,
				},
			},
			IndividualTopics:       []string{"zqx-topic-audit"},
			ReadProcessWriteTopics: []string{"zqx-topic-ledger", "zqx-topic-payments"},
		},
	}
}

// customerIdentifiers are every topic name and transactional id distinctiveRun carries.
func customerIdentifiers(r Run) []string {
	seen := map[string]struct{}{}
	add := func(vs ...string) {
		for _, v := range vs {
			seen[v] = struct{}{}
		}
	}
	for _, fp := range r.Footprints {
		add(fp.TxnID)
		add(fp.Topics...)
	}
	for _, g := range r.Result.Groups {
		add(g.Topics...)
		add(g.TxnIDs...)
	}
	add(r.Result.IndividualTopics...)
	add(r.Result.ReadProcessWriteTopics...)
	add(r.Offsets.RecoveredTopics...)

	out := make([]string, 0, len(seen))
	for v := range seen {
		out = append(out, v)
	}
	return out
}

func TestSummaryLeaksNoTopicNameOrTransactionalID(t *testing.T) {
	r := distinctiveRun()
	out := render(t, r)

	ids := customerIdentifiers(r)
	if len(ids) == 0 {
		t.Fatal("fixture carries no identifiers, so this test would pass vacuously")
	}
	for _, id := range ids {
		if strings.Contains(out, id) {
			t.Errorf("summary leaked the customer identifier %q\n--- summary ---\n%s", id, out)
		}
	}
}

func TestHealthLineIsOKWhenCaughtUpCleanAndEveryPartitionLive(t *testing.T) {
	out := render(t, Run{
		ActiveSources: []string{discovery.SourceTxnStateLog},
		Tail: tail.Stats{
			PartitionsAssigned: 3,
			PartitionsRunning:  3,
			RecordsRead:        500,
			// An open transaction holding the high watermark above the last stable
			// offset is the normal state on an internal topic and is not reader lag
			// (KTD9), so it must not spoil the health line.
			OpenTxnBacklog: 42,
		},
	})

	requireContains(t, out, "keep-up                : OK — 3/3 partitions live, 0 records of lag, 0 decode failures")
}

func TestHealthLineWarnsWhenAnySourceFailedToDecode(t *testing.T) {
	// One failure from each source that counts them, so the aggregate cannot pass by
	// watching only the tail.
	out := render(t, Run{
		ActiveSources: []string{discovery.SourceTxnStateLog},
		Tail: tail.Stats{
			PartitionsAssigned: 2,
			PartitionsRunning:  2,
			DecodeErrors:       1,
		},
		TxnState: discovery.TxnStateStats{KeyDecodeErrors: 2, ValueDecodeErrors: 3},
		Offsets:  discovery.ConsumerOffsetsStats{KeyDecodeErrors: 4},
	})

	requireContains(t, out, "keep-up                : WARNING — 2/2 partitions live, 0 records of lag, 10 decode failures")
	requireContains(t, out, "the internal record format may have drifted, so footprints may be missing")
}

func TestHealthLineIsNotOKWhenAPartitionStoppedEvenWithZeroLag(t *testing.T) {
	// The stall failure mode: a partition loop that exited reports zero lag and no
	// decode errors, so liveness is the only thing that distinguishes it from a
	// caught-up run.
	out := render(t, Run{
		ActiveSources: []string{discovery.SourceTxnStateLog},
		Tail: tail.Stats{
			PartitionsAssigned: 4,
			PartitionsRunning:  3,
			RecordsRead:        900,
		},
	})

	requireContains(t, out, "keep-up                : WARNING — 3/4 partitions live, 0 records of lag, 0 decode failures")
	requireContains(t, out, "1 partition stopped early, so part of the window went unobserved")
}

func TestHealthLineWarnsOnLastStableOffsetLagButNotOnOpenTransactionBacklog(t *testing.T) {
	out := render(t, Run{
		ActiveSources: []string{discovery.SourceTxnStateLog},
		Tail: tail.Stats{
			PartitionsAssigned: 2,
			PartitionsRunning:  2,
			Lag:                750,
			OpenTxnBacklog:     9000,
		},
	})

	requireContains(t, out, "keep-up                : WARNING — 2/2 partitions live, 750 records of lag, 0 decode failures")
	requireContains(t, out, "the reader did not catch up to the last stable offset, so the window was not fully observed")
	// KTD9: the high-watermark-to-LSO gap is not something the reader can catch up
	// on, so it must not appear as lag on the health line.
	if strings.Contains(out, "9000") {
		t.Errorf("open-transaction backlog was reported as reader lag\n--- summary ---\n%s", out)
	}
}

// rpwRun is a read-process-write run in which each enrichment phase recovered the
// inputs of exactly one transaction, so an attribution that credits whichever phase
// merely ran is distinguishable from one that credits the phase that found the input.
func rpwRun() Run {
	return Run{
		ActiveSources: []string{discovery.SourceTxnStateLog, discovery.SourceConsumerGroups, discovery.SourceConsumerOffsets},
		Footprints: []discovery.TxnFootprint{
			{TxnID: "by-offsets", ReadProcessWrite: true, Topics: []string{"out-a", "in-a"},
				Sources: []string{discovery.SourceTxnStateLog, discovery.SourceConsumerOffsets}},
			{TxnID: "by-naming", ReadProcessWrite: true, Topics: []string{"out-b", "in-b"},
				Sources: []string{discovery.SourceConsumerGroups, discovery.SourceTxnStateLog}},
			{TxnID: "unenriched", ReadProcessWrite: true, Topics: []string{"out-c"},
				Sources: []string{discovery.SourceTxnStateLog}},
		},
		EnrichmentActive: true,
		Offsets:          discovery.ConsumerOffsetsStats{RecoveredTopics: []string{"in-a"}},
		Result: grouping.Result{
			Groups: []grouping.Group{
				{Name: "group-1", Topics: []string{"in-a", "out-a"}, TxnIDs: []string{"by-offsets"}, ReadProcessWrite: true},
				{Name: "group-2", Topics: []string{"in-b", "out-b"}, TxnIDs: []string{"by-naming"}, ReadProcessWrite: true},
			},
			IndividualTopics:       []string{"out-c"},
			ReadProcessWriteTopics: []string{"in-a", "in-b", "out-a", "out-b", "out-c"},
		},
	}
}

func TestSummaryCreditsOnlyThePhaseThatActuallyRecoveredEachInput(t *testing.T) {
	out := render(t, rpwRun())

	requireContains(t, out, "read-process-write     : 2 groups; consumed inputs recovered for 2 transactions")
	requireContains(t, out, "exact producer-id correlation via __consumer_offsets: 1 transaction, 1 input topic")
	requireContains(t, out, "the Kafka Streams transactional.id<->group.id naming convention: 1 transaction")
}

func TestSummaryDoesNotCreditAnEnrichmentPhaseThatRecoveredNothing(t *testing.T) {
	r := rpwRun()
	// The offsets tail ran but correlated nothing: every recovery came from naming.
	r.Footprints[0].Sources = []string{discovery.SourceTxnStateLog}
	r.Offsets.RecoveredTopics = nil

	out := render(t, r)

	requireContains(t, out, "the Kafka Streams transactional.id<->group.id naming convention: 1 transaction")
	if strings.Contains(out, "exact producer-id correlation via __consumer_offsets:") {
		t.Errorf("credited the producer-id correlation phase, which recovered nothing\n--- summary ---\n%s", out)
	}
}

func TestSummaryWarnsWhenAuditLinesFailedToReachDisk(t *testing.T) {
	// Driven from a real AuditWriter rather than a hand-set number, because the
	// contract being proved is that the writer's error count reaches the summary at
	// all: a truncated trace reads downstream as "no transaction coupled these
	// topics", which is indistinguishable from a clean run.
	w, err := NewAuditWriter(filepath.Join(t.TempDir(), "audit.jsonl"))
	if err != nil {
		t.Fatalf("open audit log: %v", err)
	}
	defer func() { _ = w.Close() }()
	w.sink = &failingWriter{err: errors.New("no space left on device")}
	for i := 0; i < 3; i++ {
		w.Record(
			discovery.Observation{TxnID: fmt.Sprintf("t-%d", i), Source: discovery.SourceTxnStateLog},
			discovery.Change{Added: []string{fmt.Sprintf("topic-%d", i)}},
		)
	}
	if w.Errors() != 3 {
		t.Fatalf("fixture did not produce audit errors: got %d", w.Errors())
	}

	out := render(t, Run{ActiveSources: []string{discovery.SourceTxnStateLog}, AuditErrors: w.Errors()})
	requireContains(t, out, "WARNING: 3 audit log lines failed to reach disk")
	requireContains(t, out, "the trace is incomplete")
}

func TestSummaryDoesNotWarnAboutTheAuditLogWhenEveryLineReachedDisk(t *testing.T) {
	out := render(t, Run{ActiveSources: []string{discovery.SourceTxnStateLog}, AuditErrors: 0})
	if strings.Contains(out, "audit log") {
		t.Errorf("a clean run warned about the audit log\n--- summary ---\n%s", out)
	}
}

func TestSummaryRendersAZeroStateWhenNoGroupsWereFound(t *testing.T) {
	out := render(t, Run{
		Duration:      time.Minute,
		Interval:      10 * time.Second,
		ActiveSources: []string{discovery.SourceTxnStateLog},
		TxnState:      discovery.TxnStateStats{RecordsSeen: 4},
		Result:        grouping.Result{IndividualTopics: []string{"solo"}},
	})

	requireContains(t, out, "Transaction groups: none — every observed topic can migrate individually.")
	if strings.Contains(out, "migrate each group atomically") {
		t.Errorf("a run with no groups rendered the group table header\n--- summary ---\n%s", out)
	}
}

// resultIdentifiers are every topic name and transactional id the grouping result
// carries — the set the YAML exists to record.
func resultIdentifiers(res grouping.Result) []string {
	var out []string
	for _, g := range res.Groups {
		out = append(out, g.Topics...)
		out = append(out, g.TxnIDs...)
	}
	out = append(out, res.IndividualTopics...)
	return out
}

// writeYAML writes the YAML for r into a fresh temp dir and returns its path and body.
func writeYAML(t *testing.T, r Run) (string, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "txn-discovery.yaml")
	if err := WriteYAML(path, Summarize(r)); err != nil {
		t.Fatalf("write yaml: %v", err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read yaml: %v", err)
	}
	return path, string(body)
}

func TestYAMLCarriesEveryTopicNameAndTransactionalID(t *testing.T) {
	r := distinctiveRun()
	_, body := writeYAML(t, r)

	ids := resultIdentifiers(r.Result)
	if len(ids) == 0 {
		t.Fatal("fixture carries no identifiers, so this test would pass vacuously")
	}
	for _, id := range ids {
		if !strings.Contains(body, id) {
			t.Errorf("yaml is missing %q\n--- yaml ---\n%s", id, body)
		}
	}
}

func TestYAMLClaimsNeitherPOCStatusNorThatItDrivesMigration(t *testing.T) {
	_, body := writeYAML(t, distinctiveRun())

	// Nothing in kcp reads this document; presenting it as an input to an automated
	// migration, or leaving a field for the operator to complete before migrating,
	// promises a workflow that does not exist.
	for _, banned := range []string{
		"POC",
		"bootstrap_url",
		"before migrating",
		"drive an automated migration",
		"provisional",
		"roadmap",
	} {
		if strings.Contains(body, banned) {
			t.Errorf("yaml contains %q\n--- yaml ---\n%s", banned, body)
		}
	}
}

// yamlDoc mirrors the on-disk document by its YAML keys rather than by reusing the
// production struct, so a renamed key is a test failure instead of an invisible change.
type yamlDoc struct {
	GeneratedBy       string `yaml:"generated_by"`
	ObservationWindow string `yaml:"observation_window"`
	Groups            []struct {
		Name             string   `yaml:"name"`
		ReadProcessWrite bool     `yaml:"read_process_write"`
		Warning          string   `yaml:"warning"`
		Topics           []string `yaml:"topics"`
		TransactionalIDs []string `yaml:"transactional_ids"`
	} `yaml:"groups"`
	IndividualTopicCount int      `yaml:"individual_topic_count"`
	IndividualTopics     []string `yaml:"individual_topics"`
}

func parseYAML(t *testing.T, body string) yamlDoc {
	t.Helper()
	var doc yamlDoc
	if err := yaml.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("yaml did not parse: %v\n--- yaml ---\n%s", err, body)
	}
	return doc
}

func TestYAMLWarningNamesTheMechanismThatRecoveredThatGroupsInputs(t *testing.T) {
	r := rpwRun()
	// A third group that is read-process-write but had nothing recovered, so a
	// warning that merely names whichever phase ran is distinguishable from one that
	// names the phase that found THIS group's inputs.
	r.Result.Groups = append(r.Result.Groups, grouping.Group{
		Name: "group-3", Topics: []string{"out-d", "out-e"}, TxnIDs: []string{"unenriched"}, ReadProcessWrite: true,
	})
	r.Result.Groups = append(r.Result.Groups, grouping.Group{
		Name: "group-4", Topics: []string{"plain-1", "plain-2"}, TxnIDs: []string{"plain"},
	})

	_, body := writeYAML(t, r)
	doc := parseYAML(t, body)

	want := map[string]string{
		"group-1": "exact producer-id correlation via __consumer_offsets",
		"group-2": "the Kafka Streams transactional.id<->group.id naming convention",
		"group-3": "consumed input topics are not captured",
		"group-4": "",
	}
	if len(doc.Groups) != len(want) {
		t.Fatalf("expected %d groups on disk, got %d\n--- yaml ---\n%s", len(want), len(doc.Groups), body)
	}
	for _, g := range doc.Groups {
		w, ok := want[g.Name]
		if !ok {
			t.Fatalf("unexpected group %q on disk", g.Name)
		}
		if w == "" {
			if g.Warning != "" {
				t.Errorf("group %q is not read-process-write but carries warning %q", g.Name, g.Warning)
			}
			continue
		}
		if !strings.Contains(g.Warning, w) {
			t.Errorf("group %q warning = %q, want it to name %q", g.Name, g.Warning, w)
		}
	}
	// group-1's inputs came from producer-id correlation alone, so naming must not be
	// credited for it, and vice versa.
	for _, g := range doc.Groups {
		if g.Name == "group-1" && strings.Contains(g.Warning, "Kafka Streams") {
			t.Errorf("group-1 credited the naming convention, which recovered nothing for it: %q", g.Warning)
		}
		if g.Name == "group-2" && strings.Contains(g.Warning, "producer-id correlation") {
			t.Errorf("group-2 credited producer-id correlation, which recovered nothing for it: %q", g.Warning)
		}
	}
}

func TestWriteYAMLFailsActionablyWhenTheOutputDirectoryDoesNotExist(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "no-such-dir")
	path := filepath.Join(missing, "txn-discovery.yaml")

	err := WriteYAML(path, Summarize(distinctiveRun()))
	if err == nil {
		t.Fatal("writing into a nonexistent directory succeeded")
	}
	for _, want := range []string{missing, "does not exist"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
	// A partial artifact is worse than none: a truncated document parses as a shorter
	// list of groups, which reads as "these topics are not coupled".
	if _, statErr := os.Stat(missing); !errors.Is(statErr, fs.ErrNotExist) {
		t.Errorf("the missing directory was created: %v", statErr)
	}
	entries, readErr := os.ReadDir(dir)
	if readErr != nil {
		t.Fatalf("read temp dir: %v", readErr)
	}
	if len(entries) != 0 {
		t.Errorf("failed write left %d entries behind: %v", len(entries), entries)
	}
}

// hostileNames are topic names chosen to break a document assembled by string
// formatting rather than by an encoder. Whoever can create a topic on the observed
// cluster chooses these bytes, and they additionally arrive through decoders for
// broker-internal binary formats that are not part of the stable client protocol.
var hostileNames = []string{
	"-leading-dash",
	"key: value",
	"trailing-hash # comment",
	"!!str tagged",
	"yes",
	"no",
	"on",
	"true",
	"null",
	"~",
	"1.0",
	"\"double-quoted\"",
	"'single-quoted'",
	"&anchor",
	"*alias",
	"{inline: map}",
	"[inline, seq]",
	"---",
	"...",
	"\ttab-indented",
	" leading-space",
	"trailing-space ",
	"line-one\nline-two",
	// The forgery probe: if the document were assembled with string formatting, this
	// name would close its own list item and open a second, complete group.
	"forge\n  - name: forged-group\n    read_process_write: false\n    topics:\n      - pwned\n    transactional_ids: []",
}

func TestYAMLRoundTripsTopicNamesContainingYAMLMetacharacters(t *testing.T) {
	r := Run{
		Duration: time.Minute,
		Result: grouping.Result{
			Groups: []grouping.Group{{
				Name:   "group-1",
				Topics: hostileNames,
				TxnIDs: hostileNames,
			}},
			IndividualTopics: hostileNames,
		},
	}

	_, body := writeYAML(t, r)
	doc := parseYAML(t, body)

	// Exactly one group: a name that forged a second list item would show up here.
	if len(doc.Groups) != 1 {
		t.Fatalf("expected 1 group after round-trip, got %d\n--- yaml ---\n%s", len(doc.Groups), body)
	}
	for _, tc := range []struct {
		what string
		got  []string
	}{
		{"topics", doc.Groups[0].Topics},
		{"transactional_ids", doc.Groups[0].TransactionalIDs},
		{"individual_topics", doc.IndividualTopics},
	} {
		if len(tc.got) != len(hostileNames) {
			t.Errorf("%s round-tripped to %d entries, want %d\n--- yaml ---\n%s", tc.what, len(tc.got), len(hostileNames), body)
			continue
		}
		for i, want := range hostileNames {
			if tc.got[i] != want {
				t.Errorf("%s[%d] round-tripped to %q, want %q", tc.what, i, tc.got[i], want)
			}
		}
	}
}

// groupLines returns the rendered group rows in the order they appear, so ordering can
// be asserted without pinning the surrounding prose.
func groupLines(out string) []string {
	var rows []string
	for _, line := range strings.Split(out, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "group-") {
			rows = append(rows, trimmed)
		}
	}
	return rows
}

func TestGroupTableListsLargestGroupsFirstWithStableOrdering(t *testing.T) {
	// Deliberately supplied smallest-first and with the two three-topic groups in
	// reverse name order: the table must impose its own ordering rather than echoing
	// whatever order it was handed.
	out := render(t, Run{
		Result: grouping.Result{
			Groups: []grouping.Group{
				{Name: "group-3", Topics: []string{"m", "n"}, TxnIDs: []string{"t3"}},
				{Name: "group-2", Topics: []string{"d", "e", "f"}, TxnIDs: []string{"t2a", "t2b"}},
				{Name: "group-1", Topics: []string{"a", "b", "c"}, TxnIDs: []string{"t1"}},
				{Name: "group-0", Topics: []string{"p", "q", "r", "s"}, TxnIDs: []string{"t0"}},
			},
		},
	})

	want := []string{
		"group-0: 4 topics, 1 transactional id",
		"group-1: 3 topics, 1 transactional id",
		"group-2: 3 topics, 2 transactional ids",
		"group-3: 2 topics, 1 transactional id",
	}
	got := groupLines(out)
	if len(got) != len(want) {
		t.Fatalf("expected %d group rows, got %d\n--- summary ---\n%s", len(want), len(got), out)
	}
	for i := range want {
		if !strings.HasPrefix(got[i], want[i]) {
			t.Errorf("group row %d = %q, want prefix %q\n--- summary ---\n%s", i, got[i], want[i], out)
		}
	}
}
