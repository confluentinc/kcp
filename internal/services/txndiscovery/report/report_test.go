package report

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/confluentinc/kcp/internal/services/txndiscovery/discovery"
	"github.com/confluentinc/kcp/internal/services/txndiscovery/grouping"
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
