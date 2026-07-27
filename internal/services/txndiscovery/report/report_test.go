package report

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/confluentinc/kcp/internal/services/txndiscovery/discovery"
	"github.com/confluentinc/kcp/internal/services/txndiscovery/grouping"
	"github.com/confluentinc/kcp/internal/services/txndiscovery/tail"
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
	requireContains(t, out, "read-process-write     : 0 groups; 0 consumed input topic(s) recovered")
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
