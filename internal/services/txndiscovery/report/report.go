package report

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/confluentinc/kcp/internal/services/txndiscovery/discovery"
	"github.com/confluentinc/kcp/internal/services/txndiscovery/grouping"
)

// Run is the raw material of a completed discovery run: what the sources
// accumulated plus the parameters they ran under. It is the single input the
// command hands to this package, so the derivation of every artifact lives here
// rather than being duplicated at the call site.
type Run struct {
	// Duration is the observation window and Interval the enrichment period.
	Duration time.Duration
	Interval time.Duration

	// ActiveSources names the phases that ran, in the order they are reported.
	ActiveSources []string

	// Footprints is the accumulator's snapshot: one entry per transactional id
	// observed, carrying the phases that reported it.
	Footprints []discovery.TxnFootprint

	// Result is the grouping computed from those footprints.
	Result grouping.Result

	// TxnState is the __transaction_state reader's counters.
	TxnState discovery.TxnStateStats
}

// Summary is the derived, render-ready view of a run.
type Summary struct {
	Duration time.Duration
	Interval time.Duration

	ActiveSources []string

	// TxnRecordsRead is how many __transaction_state records were read and
	// TxnCount how many distinct transactional ids those yielded.
	TxnRecordsRead int64
	TxnCount       int
	TxnCommitted   int64
	TxnAborted     int64

	Result grouping.Result
}

// Summarize derives the render-ready summary from a completed run.
func Summarize(r Run) Summary {
	return Summary{
		Duration:       r.Duration,
		Interval:       r.Interval,
		ActiveSources:  r.ActiveSources,
		TxnRecordsRead: r.TxnState.RecordsSeen,
		TxnCount:       len(r.Footprints),
		TxnCommitted:   r.TxnState.Committed,
		TxnAborted:     r.TxnState.Aborted,
		Result:         r.Result,
	}
}

// PrintTerminal writes the operator-facing summary.
func PrintTerminal(w io.Writer, s Summary) {
	groups := s.Result.Groups
	individual := s.Result.IndividualTopics

	groupedTopics, rpwGroups := 0, 0
	for _, g := range groups {
		groupedTopics += len(g.Topics)
		if g.ReadProcessWrite {
			rpwGroups++
		}
	}

	const rule = "============================================================"
	_, _ = fmt.Fprintln(w, rule)
	_, _ = fmt.Fprintln(w, " Transaction discovery summary")
	_, _ = fmt.Fprintln(w, rule)
	_, _ = fmt.Fprintf(w, "  window                 : %s (enrichment interval %s)\n", s.Duration, s.Interval)
	_, _ = fmt.Fprintf(w, "  sources                : %s\n", strings.Join(s.ActiveSources, ", "))
	_, _ = fmt.Fprintf(w, "  transactions read      : %d record(s) on the transaction-state log, %d transactional id(s) observed\n",
		s.TxnRecordsRead, s.TxnCount)
	_, _ = fmt.Fprintf(w, "  transactions           : %d committed, %d aborted\n", s.TxnCommitted, s.TxnAborted)
	_, _ = fmt.Fprintf(w, "  topics                 : %d total — %d in %d %s, %d individual\n",
		groupedTopics+len(individual), groupedTopics, len(groups), plural(len(groups), "group", "groups"), len(individual))
	_, _ = fmt.Fprintf(w, "  read-process-write     : %d %s; %d consumed input topic(s) recovered\n",
		rpwGroups, plural(rpwGroups, "group", "groups"), 0)
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
