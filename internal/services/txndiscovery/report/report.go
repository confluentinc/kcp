package report

import (
	"cmp"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/confluentinc/kcp/internal/services/txndiscovery/discovery"
	"github.com/confluentinc/kcp/internal/services/txndiscovery/grouping"
	"github.com/confluentinc/kcp/internal/services/txndiscovery/tail"
	"github.com/goccy/go-yaml"
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

	// Recovery attributes recovered consumed inputs to the phase that found them.
	Recovery Recovery
}

// Recovery records which enrichment phase actually recovered each read-process-write
// transaction's consumed inputs.
//
// Attribution matters because the two phases have very different coverage: the Kafka
// Streams naming convention only correlates applications that follow it, while
// producer-id correlation works for any exactly-once application. Crediting a phase
// that merely ran would tell an operator their non-Streams applications were covered
// when nothing looked at them.
//
// The two phases are attributed at different granularities, and deliberately so. The
// __consumer_offsets tail records the exact set of input topics it recovered, so it is
// credited per topic. Consumer-group enrichment keeps no such record: the accumulator
// unions every source's topics into one set, so once an enrichment observation has
// merged there is no way to tell which topics it contributed. What survives is the
// per-transaction source list, so naming is credited per transaction. Inventing a topic
// count for it would mean guessing.
type Recovery struct {
	// ByOffsetsTopics are the consumed input topics producer-id correlation
	// recovered, exactly.
	ByOffsetsTopics []string

	// ByOffsetsTxns and ByNamingTxns count the transactions each phase enriched.
	// A transaction enriched by both is counted in each.
	ByOffsetsTxns int
	ByNamingTxns  int

	// Txns is how many distinct transactions had inputs recovered by either phase.
	Txns int

	// OffsetsActive and EnrichmentActive report whether each phase ran, so a phase
	// that was never enabled is not presented as having found nothing.
	OffsetsActive    bool
	EnrichmentActive bool

	// OffsetsUnavailableReason is why the offsets tail could not run (R13).
	OffsetsUnavailableReason string

	// byTxn maps a transactional id to the phases that enriched it, which is what
	// lets a group's warning name the mechanism that recovered ITS inputs.
	byTxn map[string]mechanisms
}

// mechanisms is the set of enrichment phases that reported one transaction.
type mechanisms struct {
	byOffsets bool
	byNaming  bool
}

func (m mechanisms) any() bool { return m.byOffsets || m.byNaming }

// describe names the phases in m, or the empty string when none apply.
func (m mechanisms) describe() string {
	switch {
	case m.byOffsets && m.byNaming:
		return "exact producer-id correlation via __consumer_offsets and the Kafka Streams naming convention"
	case m.byOffsets:
		return "exact producer-id correlation via __consumer_offsets"
	case m.byNaming:
		return "the Kafka Streams transactional.id<->group.id naming convention"
	default:
		return ""
	}
}

// recoveryOf derives the attribution from the per-transaction source lists.
func recoveryOf(r Run) Recovery {
	rec := Recovery{
		ByOffsetsTopics:          r.Offsets.RecoveredTopics,
		OffsetsActive:            slices.Contains(r.ActiveSources, discovery.SourceConsumerOffsets) && !r.Offsets.Unavailable,
		EnrichmentActive:         r.EnrichmentActive,
		OffsetsUnavailableReason: r.Offsets.UnavailableReason,
		byTxn:                    make(map[string]mechanisms, len(r.Footprints)),
	}
	for _, fp := range r.Footprints {
		var m mechanisms
		for _, src := range fp.Sources {
			switch src {
			case discovery.SourceConsumerOffsets:
				m.byOffsets = true
			case discovery.SourceConsumerGroups:
				m.byNaming = true
			}
		}
		if !m.any() {
			continue
		}
		rec.byTxn[fp.TxnID] = m
		rec.Txns++
		if m.byOffsets {
			rec.ByOffsetsTxns++
		}
		if m.byNaming {
			rec.ByNamingTxns++
		}
	}
	return rec
}

// forGroup collapses the mechanisms that enriched any of a group's transactions.
func (rc Recovery) forGroup(g grouping.Group) mechanisms {
	var out mechanisms
	for _, id := range g.TxnIDs {
		m := rc.byTxn[id]
		out.byOffsets = out.byOffsets || m.byOffsets
		out.byNaming = out.byNaming || m.byNaming
	}
	return out
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
		Recovery:       recoveryOf(r),
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
	_, _ = fmt.Fprintf(w, "  read-process-write     : %d %s; consumed inputs recovered for %d %s\n",
		rpwGroups, plural(rpwGroups, "group", "groups"),
		s.Recovery.Txns, plural(s.Recovery.Txns, "transaction", "transactions"))

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

	if len(s.Result.ReadProcessWriteTopics) > 0 {
		printRecovery(w, s)
	}
}

// printRecovery explains what happened to the consumed inputs of the exactly-once
// applications observed, naming only the phases that actually recovered something.
func printRecovery(w io.Writer, s Summary) {
	rc := s.Recovery
	_, _ = fmt.Fprintln(w)
	_, _ = fmt.Fprintln(w, "Read-process-write (exactly-once consume-transform-produce) apps detected.")
	_, _ = fmt.Fprintf(w, "  %d %s are coupled by consume-transform-produce transactions.\n",
		len(s.Result.ReadProcessWriteTopics), plural(len(s.Result.ReadProcessWriteTopics), "topic", "topics"))

	switch {
	case rc.Txns > 0:
		_, _ = fmt.Fprintln(w, "  Consumed input topics were recovered and folded into their groups by:")
		if rc.ByOffsetsTxns > 0 || len(rc.ByOffsetsTopics) > 0 {
			_, _ = fmt.Fprintf(w, "    - exact producer-id correlation via __consumer_offsets: %d %s, %d input %s\n",
				rc.ByOffsetsTxns, plural(rc.ByOffsetsTxns, "transaction", "transactions"),
				len(rc.ByOffsetsTopics), plural(len(rc.ByOffsetsTopics), "topic", "topics"))
		}
		if rc.ByNamingTxns > 0 {
			// No topic count: the accumulator unions every source's topics, so which
			// of a transaction's topics enrichment contributed is not recoverable.
			_, _ = fmt.Fprintf(w, "    - the Kafka Streams transactional.id<->group.id naming convention: %d %s\n",
				rc.ByNamingTxns, plural(rc.ByNamingTxns, "transaction", "transactions"))
		}
		if !rc.OffsetsActive {
			_, _ = fmt.Fprintln(w, "  NOTE: producer-id correlation did not run, so non-Streams exactly-once")
			_, _ = fmt.Fprintln(w, "        applications may have unrecovered inputs — verify coverage before cutover.")
		}
	case rc.EnrichmentActive || rc.OffsetsActive:
		_, _ = fmt.Fprintln(w, "  No consumed input topics were recovered: no correlatable exactly-once consumer")
		_, _ = fmt.Fprintln(w, "  group was observed in the window. Verify inputs before cutover.")
	default:
		_, _ = fmt.Fprintln(w, "  Their consumed input topics are not visible through the transaction footprint")
		_, _ = fmt.Fprintln(w, "  and may need to migrate with them. Verify inputs before cutover.")
	}

	if rc.OffsetsUnavailableReason != "" {
		_, _ = fmt.Fprintf(w, "  NOTE: the __consumer_offsets tail could not run (%s).\n", rc.OffsetsUnavailableReason)
	}
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

// --- txn-discovery.yaml ---

// discoveryDoc is the on-disk shape of txn-discovery.yaml.
//
// This is the artifact that carries the names: the terminal deliberately reports counts
// only, so everything an operator needs to act on is here.
type discoveryDoc struct {
	GeneratedBy       string `yaml:"generated_by"`
	GeneratedAt       string `yaml:"generated_at"`
	ObservationWindow string `yaml:"observation_window"`

	Groups []discoveryGroup `yaml:"groups"`

	IndividualTopicCount int `yaml:"individual_topic_count"`
	// IndividualTopics were only ever seen alone in a transaction, so they can
	// migrate one at a time. Listed rather than merely counted so the document holds
	// the complete set of observed topics.
	IndividualTopics []string `yaml:"individual_topics"`
}

type discoveryGroup struct {
	Name             string   `yaml:"name"`
	ReadProcessWrite bool     `yaml:"read_process_write"`
	Warning          string   `yaml:"warning,omitempty"`
	Topics           []string `yaml:"topics"`
	TransactionalIDs []string `yaml:"transactional_ids"`
}

// groupWarning explains what a read-process-write group's consumed inputs depend on,
// naming the mechanism that recovered THIS group's inputs rather than whichever phase
// happened to be enabled. A group with no read-process-write transaction has nothing to
// warn about and gets no warning.
func groupWarning(g grouping.Group, s Summary) string {
	if !g.ReadProcessWrite {
		return ""
	}
	m := s.Recovery.forGroup(g)
	if !m.any() {
		return "read-process-write group: consumed input topics are not captured; review before cutover."
	}
	warning := "read-process-write group: consumed input topics recovered and included via " + m.describe() + "."
	if !s.Recovery.OffsetsActive {
		warning += " Producer-id correlation did not run, so verify coverage for non-Streams exactly-once apps."
	}
	return warning
}

// WriteYAML writes the discovered groups to path.
func WriteYAML(path string, s Summary) error {
	doc := discoveryDoc{
		GeneratedBy:          "kcp migration txn-discovery",
		GeneratedAt:          time.Now().UTC().Format(time.RFC3339),
		ObservationWindow:    s.Duration.String(),
		IndividualTopicCount: len(s.Result.IndividualTopics),
		IndividualTopics:     s.Result.IndividualTopics,
	}
	for _, g := range s.Result.Groups {
		doc.Groups = append(doc.Groups, discoveryGroup{
			Name:             g.Name,
			ReadProcessWrite: g.ReadProcessWrite,
			Warning:          groupWarning(g, s),
			Topics:           g.Topics,
			TransactionalIDs: g.TxnIDs,
		})
	}

	body, err := yaml.Marshal(doc)
	if err != nil {
		return fmt.Errorf("failed to marshal the transaction discovery document: %w", err)
	}
	return os.WriteFile(path, body, 0644)
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
