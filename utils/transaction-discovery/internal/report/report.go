// Package report renders a discovery run: a terminal summary in the style of the
// design doc, plus a migration.yaml the operator reviews and completes.
package report

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/confluentinc/kcp/utils/transaction-discovery/internal/discovery"
	"github.com/confluentinc/kcp/utils/transaction-discovery/internal/grouping"
)

// Summary is everything needed to render a run's result.
type Summary struct {
	Duration time.Duration
	Interval time.Duration

	ActiveSources   []string // sources that actually ran
	SourcesWithData []string // sources that produced at least one observation

	// EnrichmentActive reports whether Phase 3 consumer-group enrichment ran;
	// OffsetsTailActive reports whether Phase 4 (exact producer-id correlation via
	// __consumer_offsets) ran. RecoveredInputTopics are the consumed input topics
	// either phase folded back into groups (their union). RecoveredByNaming /
	// RecoveredByOffsets are the per-phase breakdown, so the report can attribute each
	// recovered topic to the mechanism that ACTUALLY found it rather than to whichever
	// phase happened to be enabled.
	EnrichmentActive     bool
	OffsetsTailActive    bool
	RecoveredInputTopics []string
	RecoveredByNaming    []string // Phase 3: Kafka Streams transactional.id<->group.id
	RecoveredByOffsets   []string // Phase 4: exact producer-id correlation

	TxnCount int

	// TxnCommitted / TxnAborted are the committed / aborted transaction completions the
	// __transaction_state tail observed during the window (0 when the tail was inactive).
	TxnCommitted int64
	TxnAborted   int64

	Result grouping.Result
}

// maxTopicsPerGroup caps how many topics are listed under each group so a group with
// hundreds of topics does not flood the terminal; the full set is always in the YAML.
const maxTopicsPerGroup = 20

// PrintTerminal writes a human-readable summary: a compact header of the run's totals,
// the discovered groups WITH their topics, the individual topics, and the
// read-process-write recovery breakdown. It is plain ASCII (no emoji) so it
// copy-pastes cleanly out of a terminal.
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
	totalTopics := groupedTopics + len(individual)

	// --- header: the at-a-glance totals ---
	const rule = "============================================================"
	fmt.Fprintln(w, rule)
	fmt.Fprintln(w, " Transaction discovery summary")
	fmt.Fprintln(w, rule)
	fmt.Fprintf(w, "  window                 : %s (enrichment interval %s)\n", s.Duration, s.Interval)
	fmt.Fprintf(w, "  sources                : %s\n", strings.Join(s.ActiveSources, ", "))
	if !contains(s.SourcesWithData, discovery.SourceTxnStateLog) {
		fmt.Fprintln(w, "                           (transaction-state log saw no transactions in the window)")
	}
	fmt.Fprintf(w, "  transactional producers: %d observed\n", s.TxnCount)
	fmt.Fprintf(w, "  transactions           : %d committed, %d aborted (completions seen on the transaction-state log this window)\n",
		s.TxnCommitted, s.TxnAborted)
	fmt.Fprintf(w, "  topics                 : %d total — %d in %d %s, %d individual\n",
		totalTopics, groupedTopics, len(groups), plural(len(groups), "group", "groups"), len(individual))
	fmt.Fprintf(w, "  read-process-write     : %d %s; %d consumed input topic(s) recovered\n",
		rpwGroups, plural(rpwGroups, "group", "groups"), len(s.RecoveredInputTopics))

	// --- groups: the topics coupled by transactions (must migrate atomically) ---
	fmt.Fprintln(w)
	if len(groups) == 0 {
		fmt.Fprintln(w, "Transaction groups: none — every observed topic can migrate individually.")
	} else {
		fmt.Fprintln(w, "Transaction groups (topics coupled by a shared transaction — migrate each group atomically):")
		recovered := sliceSet(s.RecoveredInputTopics)
		for _, g := range groups {
			tag := ""
			if g.ReadProcessWrite {
				tag = "  [read-process-write]"
			}
			fmt.Fprintf(w, "\n  %s (%d %s, %d %s)%s\n",
				g.Name,
				len(g.TxnIDs), plural(len(g.TxnIDs), "id", "ids"),
				len(g.Topics), plural(len(g.Topics), "topic", "topics"),
				tag)
			printTopicList(w, g.Topics, recovered)
		}
	}

	// --- individual topics ---
	fmt.Fprintln(w)
	fmt.Fprintf(w, "Individual topics (no cross-topic transaction — can migrate one at a time): %d\n", len(individual))
	if len(individual) > 0 {
		fmt.Fprintf(w, "    %s\n", previewList(individual, 12))
	}

	// --- read-process-write recovery, attributed to the mechanism that ACTUALLY found it ---
	if len(s.Result.ReadProcessWriteTopics) > 0 {
		printRecovery(w, s)
	}
}

// printTopicList writes a group's topics one per line, marking any that were recovered
// as an EOS consumed input, capped at maxTopicsPerGroup.
func printTopicList(w io.Writer, topics []string, recovered map[string]struct{}) {
	shown, extra := topics, 0
	if len(shown) > maxTopicsPerGroup {
		extra = len(shown) - maxTopicsPerGroup
		shown = shown[:maxTopicsPerGroup]
	}
	for _, t := range shown {
		if _, ok := recovered[t]; ok {
			fmt.Fprintf(w, "    %s   <- consumed input, recovered\n", t)
		} else {
			fmt.Fprintf(w, "    %s\n", t)
		}
	}
	if extra > 0 {
		fmt.Fprintf(w, "    ... (+%d more topic(s))\n", extra)
	}
}

// printRecovery writes the read-process-write recovery section, attributing each
// recovered input to the phase that actually found it (never crediting a phase that
// recovered nothing — the report used to name Phase 4 whenever it was merely enabled).
func printRecovery(w io.Writer, s Summary) {
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Read-process-write (exactly-once consume-transform-produce) apps detected.")
	fmt.Fprintf(w, "  %d topic(s) are produced inside consume-transform-produce transactions.\n",
		len(s.Result.ReadProcessWriteTopics))
	switch {
	case len(s.RecoveredInputTopics) > 0:
		fmt.Fprintln(w, "  Consumed input topic(s) recovered and folded into their group(s):")
		if len(s.RecoveredByOffsets) > 0 {
			fmt.Fprintf(w, "    - exact producer-id correlation via __consumer_offsets: %s\n",
				previewList(s.RecoveredByOffsets, 12))
		}
		if len(s.RecoveredByNaming) > 0 {
			fmt.Fprintf(w, "    - Kafka Streams transactional.id<->group.id naming:     %s\n",
				previewList(s.RecoveredByNaming, 12))
		}
		if !s.OffsetsTailActive {
			fmt.Fprintln(w, "  NOTE: the __consumer_offsets tail was inactive on this cluster, so")
			fmt.Fprintln(w, "        non-Streams consumer+producer EOS inputs may be unrecovered — verify coverage.")
		}
	case s.EnrichmentActive || s.OffsetsTailActive:
		fmt.Fprintln(w, "  No consumed input topics were recovered (no correlatable EOS consumer group")
		fmt.Fprintln(w, "  observed in the window). Verify inputs before cutover.")
	default:
		fmt.Fprintln(w, "  Their CONSUMED input topics are not visible through the transaction footprint")
		fmt.Fprintln(w, "  and may need to migrate together with these. Enrich before cutover (see roadmap).")
	}
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// previewList joins items with commas, capping at n and appending a "+N more" suffix.
func previewList(items []string, n int) string {
	if len(items) <= n {
		return strings.Join(items, ", ")
	}
	return strings.Join(items[:n], ", ") + fmt.Sprintf(", ... (+%d more)", len(items)-n)
}

func sliceSet(items []string) map[string]struct{} {
	s := make(map[string]struct{}, len(items))
	for _, it := range items {
		s[it] = struct{}{}
	}
	return s
}

// recoveryMechanismForGroup describes how THIS group's recovered input topics were
// found, naming only the mechanism(s) that actually recovered one of them.
func recoveryMechanismForGroup(groupTopics []string, s Summary) string {
	byOffsets := sharesAny(groupTopics, s.RecoveredByOffsets)
	byNaming := sharesAny(groupTopics, s.RecoveredByNaming)
	switch {
	case byOffsets && byNaming:
		return "exact producer-id correlation via __consumer_offsets and the Kafka Streams naming convention"
	case byOffsets:
		return "exact producer-id correlation via __consumer_offsets"
	case byNaming:
		return "the Kafka Streams transactional.id<->group.id naming convention"
	default:
		return "consumer-group enrichment"
	}
}

// sharesAny reports whether a and b have at least one element in common.
func sharesAny(a, b []string) bool {
	set := make(map[string]struct{}, len(b))
	for _, x := range b {
		set[x] = struct{}{}
	}
	for _, x := range a {
		if _, ok := set[x]; ok {
			return true
		}
	}
	return false
}

// --- migration.yaml ---

type migrationDoc struct {
	GeneratedBy          string           `yaml:"generated_by"`
	GeneratedAt          string           `yaml:"generated_at"`
	ObservationWindow    string           `yaml:"observation_window"`
	Groups               []migrationGroup `yaml:"groups"`
	IndividualTopicCount int              `yaml:"individual_topic_count"`
	// IndividualTopics are the topics seen in a transaction but not coupled to any
	// other topic — safe to migrate one at a time. Listed (not just counted) so the
	// full set of topics to migrate is captured for review/automation.
	IndividualTopics []string `yaml:"individual_topics"`
}

type migrationGroup struct {
	Name             string   `yaml:"name"`
	BootstrapURL     string   `yaml:"bootstrap_url"` // operator sets this before migrating
	ReadProcessWrite bool     `yaml:"read_process_write"`
	Warning          string   `yaml:"warning,omitempty"`
	Topics           []string `yaml:"topics"`
	TransactionalIDs []string `yaml:"transactional_ids"`
}

const yamlHeader = "# Generated by the kcp transaction-discovery utility (POC).\n" +
	"# Each group must migrate atomically. Review the topics and transactional ids,\n" +
	"# then set bootstrap_url on each group before migrating. NOTE: this is a discovery\n" +
	"# artifact for review — treat the schema as provisional and adapt it before using\n" +
	"# it to drive an automated migration.\n"

// WriteMigrationYAML writes the discovered groups to path.
func WriteMigrationYAML(path string, s Summary) error {
	doc := migrationDoc{
		GeneratedBy:          "kcp txn-discovery (POC)",
		GeneratedAt:          time.Now().UTC().Format(time.RFC3339),
		ObservationWindow:    s.Duration.String(),
		IndividualTopicCount: len(s.Result.IndividualTopics),
		IndividualTopics:     s.Result.IndividualTopics,
	}
	for _, g := range s.Result.Groups {
		mg := migrationGroup{
			Name:             g.Name,
			ReadProcessWrite: g.ReadProcessWrite,
			Topics:           g.Topics,
			TransactionalIDs: g.TxnIDs,
		}
		if g.ReadProcessWrite {
			switch {
			case sharesAny(g.Topics, s.RecoveredInputTopics):
				mg.Warning = "read-process-write group: consumed input topic(s) recovered and included via " +
					recoveryMechanismForGroup(g.Topics, s) + "."
				if !s.OffsetsTailActive {
					mg.Warning += " Verify coverage for non-Streams consumer+producer EOS apps."
				}
			default:
				mg.Warning = "read-process-write group: consumed input topics are not captured; " +
					"review before cutover."
			}
		}
		doc.Groups = append(doc.Groups, mg)
	}

	body, err := yaml.Marshal(doc)
	if err != nil {
		return fmt.Errorf("marshal migration doc: %w", err)
	}
	return os.WriteFile(path, append([]byte(yamlHeader), body...), 0o644)
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
