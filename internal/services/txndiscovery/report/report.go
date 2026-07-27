package report

import (
	"cmp"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/confluentinc/kcp/internal/services/txndiscovery/discovery"
	"github.com/confluentinc/kcp/internal/services/txndiscovery/grouping"
	"github.com/confluentinc/kcp/internal/services/txndiscovery/tail"
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

	// Tail is the fetch component's snapshot. It must be taken BEFORE the run's
	// context is cancelled: every partition loop clears its running flag as it
	// exits, so a snapshot taken after shutdown reports zero partitions live and
	// the health line would condemn a perfectly healthy run.
	Tail tail.Stats

	// TxnState is the __transaction_state reader's counters.
	TxnState discovery.TxnStateStats

	// Offsets is the __consumer_offsets tail's counters, including the consumed
	// input topics that phase recovered and whether it ran at all (R13).
	Offsets discovery.ConsumerOffsetsStats

	// AuditErrors is AuditWriter.Errors(): audit lines that were meant to reach disk
	// and did not. Zero when the audit log was disabled.
	AuditErrors int

	// EnrichmentActive reports whether consumer-group enrichment ran. The offsets
	// phase carries its own Unavailable flag; enrichment has no counters of its
	// own, so whether it ran has to be told to the report.
	EnrichmentActive bool
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

	// Health is the one keep-up indicator the summary carries; the full per-source
	// and per-partition detail lives in the stats document (KTD4).
	Health Health

	// AuditErrors is how many audit lines never reached disk.
	AuditErrors int
}

// Health is the three things that together say whether the window was actually
// observed. Any one of them alone can read clean on a run that missed data: a stalled
// partition reports zero lag, and a reader that decoded nothing reports no lag either.
type Health struct {
	// PartitionsAssigned and PartitionsRunning are counted at the moment the tail
	// snapshot was taken, which must be before shutdown.
	PartitionsAssigned int
	PartitionsRunning  int

	// Lag is the aggregate last-stable-offset lag (KTD9), not the high-watermark
	// gap: under ReadCommitted the broker serves nothing past the last stable
	// offset, so an open transaction is not something the reader can catch up on.
	Lag int64

	// DecodeErrors is every broker-supplied structure any source failed to parse.
	// It is the format-drift alarm.
	DecodeErrors int64
}

// healthOf collects the keep-up indicators from every source that produces them.
func healthOf(r Run) Health {
	return Health{
		PartitionsAssigned: r.Tail.PartitionsAssigned,
		PartitionsRunning:  r.Tail.PartitionsRunning,
		Lag:                r.Tail.Lag,
		DecodeErrors: r.Tail.DecodeErrors +
			r.TxnState.KeyDecodeErrors + r.TxnState.ValueDecodeErrors +
			r.Offsets.KeyDecodeErrors,
	}
}

// Summarize derives the render-ready summary from a completed run.
func Summarize(r Run) Summary {
	result := r.Result
	result.Groups = orderGroups(r.Result.Groups)
	return Summary{
		Duration:       r.Duration,
		Interval:       r.Interval,
		ActiveSources:  r.ActiveSources,
		TxnRecordsRead: r.TxnState.RecordsSeen,
		TxnCount:       len(r.Footprints),
		TxnCommitted:   r.TxnState.Committed,
		TxnAborted:     r.TxnState.Aborted,
		Result:         result,
		Health:         healthOf(r),
		AuditErrors:    r.AuditErrors,
	}
}

// orderGroups returns a copy of groups ordered largest first, ties broken by name.
//
// grouping.Build already emits this order, but the ordering is re-imposed here rather
// than assumed: it is what makes the artifacts diffable between runs, and inheriting it
// from an upstream package's incidental behaviour would let a change there silently
// reorder every report. The input slice is copied because it belongs to the caller.
func orderGroups(groups []grouping.Group) []grouping.Group {
	out := slices.Clone(groups)
	slices.SortStableFunc(out, func(a, b grouping.Group) int {
		if n := cmp.Compare(len(b.Topics), len(a.Topics)); n != 0 {
			return n
		}
		return cmp.Compare(a.Name, b.Name)
	})
	return out
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

	_, _ = fmt.Fprintf(w, "  keep-up                : %s\n", s.Health.Line())

	// A truncated audit log is silent by nature: the missing lines read downstream as
	// "no transaction coupled these topics", which is exactly what a clean run looks
	// like. The count is the only thing that tells the two apart, so it is surfaced
	// next to the other keep-up signal rather than left to the exit code alone.
	if s.AuditErrors > 0 {
		_, _ = fmt.Fprintf(w, "  WARNING: %d audit log %s failed to reach disk, so the trace is incomplete — a coupling it should record may be missing.\n",
			s.AuditErrors, plural(s.AuditErrors, "line", "lines"))
	}

	printGroupTable(w, groups)
}

// Line renders the single health indicator the summary carries: the status, the three
// numbers it is derived from, and — when it is not OK — why.
func (h Health) Line() string {
	status, reasons := "OK", h.concerns()
	if len(reasons) > 0 {
		status = "WARNING"
	}
	line := fmt.Sprintf("%s — %d/%d %s live, %d %s of lag, %d decode %s",
		status,
		h.PartitionsRunning, h.PartitionsAssigned, plural(h.PartitionsAssigned, "partition", "partitions"),
		h.Lag, plural64(h.Lag, "record", "records"),
		h.DecodeErrors, plural64(h.DecodeErrors, "failure", "failures"))
	if len(reasons) > 0 {
		line += "; " + strings.Join(reasons, "; ")
	}
	return line
}

// concerns lists what is wrong with the run, empty when nothing is.
func (h Health) concerns() []string {
	var out []string
	// Liveness first, because it is the concern that hides behind a clean-looking
	// line: a stopped partition contributes no lag and no decode errors, so without
	// this branch the worst outcome renders identically to the best one.
	if dead := h.PartitionsAssigned - h.PartitionsRunning; dead > 0 {
		out = append(out, fmt.Sprintf("%d %s stopped early, so part of the window went unobserved",
			dead, plural(dead, "partition", "partitions")))
	}
	if h.Lag > 0 {
		out = append(out, "the reader did not catch up to the last stable offset, so the window was not fully observed")
	}
	if h.DecodeErrors > 0 {
		out = append(out, "the internal record format may have drifted, so footprints may be missing")
	}
	return out
}

// printGroupTable writes one row per group.
//
// Counts only: no topic name and no transactional id reaches the terminal. Group names
// are synthetic ordinals produced by grouping.Build, so they carry no customer
// information; the names themselves live in the YAML.
func printGroupTable(w io.Writer, groups []grouping.Group) {
	_, _ = fmt.Fprintln(w)
	// A table header with nothing under it reads as "the report broke". Naming the
	// zero state says the run worked and found no coupling, which is a result.
	if len(groups) == 0 {
		_, _ = fmt.Fprintln(w, "Transaction groups: none — every observed topic can migrate individually.")
		return
	}
	_, _ = fmt.Fprintln(w, "Transaction groups (topics coupled by a shared transaction — migrate each group atomically):")
	for _, g := range groups {
		tag := ""
		if g.ReadProcessWrite {
			tag = "  [read-process-write]"
		}
		_, _ = fmt.Fprintf(w, "  %s: %d %s, %d transactional %s%s\n",
			g.Name,
			len(g.Topics), plural(len(g.Topics), "topic", "topics"),
			len(g.TxnIDs), plural(len(g.TxnIDs), "id", "ids"),
			tag)
	}
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func plural64(n int64, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
