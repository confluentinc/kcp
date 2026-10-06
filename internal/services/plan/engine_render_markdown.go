package plan

import (
	"fmt"
	"sort"
	"strings"

	"github.com/confluentinc/kcp/internal/services/markdown"
	"github.com/confluentinc/kcp/internal/services/plan/engine"
)

// docMigrationInfra is the KCP command reference for `create-asset migration-infra`
// (the migration-type matrix and flags).
const docMigrationInfra = "https://confluentinc.github.io/kcp/latest/command-reference/create-asset/migration-infra/"
const docLinkConfigUpdate = "https://docs.confluent.io/confluent-cli/current/command-reference/kafka/link/configuration/confluent_kafka_link_configuration_update.html"
const docMigration = "https://confluentinc.github.io/kcp/latest/command-reference/migration/"
const docTargetInfra = "https://confluentinc.github.io/kcp/latest/command-reference/create-asset/target-infra/"
const docPrivateLinkAttachment = "https://docs.confluent.io/cloud/current/networking/aws-platt.html"
const docCPClusterLinkCommands = "https://docs.confluent.io/platform/current/multi-dc-deployments/cluster-linking/commands.html"
const docMirrorTopics = "https://docs.confluent.io/cloud/current/multi-cloud/cluster-linking/mirror-topics-cc.html"
const docMigrateTopics = "https://confluentinc.github.io/kcp/latest/command-reference/create-asset/migrate-topics/"
const docMigrateSchemas = "https://confluentinc.github.io/kcp/latest/command-reference/create-asset/migrate-schemas/"

// Replicator connector offset sentences, worded as the engine's Connectors reason says them.
const (
	replicatorSinkOffsets  = "At cutover, let each old sink connector reach zero lag, stop it, and note the time in UTC, a few seconds before it stopped, to allow for producer clock skew. Once Replicator has caught up, print each partition's offset for that time with `kafka-consumer-groups --bootstrap-server <confluent-cloud-bootstrap> --command-config <client.properties> --group <any-unused-group-name> --topic <topic> --reset-offsets --to-datetime <yyyy-MM-ddTHH:mm:ss.SSS, in UTC> --dry-run`, then create the connector with those offsets; see [Create connectors with offsets](" + docConnectorOffsets + ")."
	selfManagedSinkOffsets = "At cutover, let each old sink connector reach zero lag, stop it, and note the time in UTC, a few seconds before it stopped, to allow for producer clock skew. Once Replicator has caught up, set the new sink's consumer group to that time, then create the sink connector: `kafka-consumer-groups --bootstrap-server <confluent-cloud-bootstrap> --command-config <client.properties> --group connect-<connector-name> --topic <topic> --reset-offsets --to-datetime <yyyy-MM-ddTHH:mm:ss.SSS, in UTC> --execute`. Replicator keeps each record's timestamp, so the sink resumes where the old one stopped; a few records around that time may be delivered twice."
	sourceConnectorOffsets = "Source connectors don't carry offsets over, so check where each one should start before you apply it."
	// sourceConnectorOffsetsAfterSinks follows a sentence that already ends "before you apply it".
	sourceConnectorOffsetsAfterSinks = "Source connectors don't carry offsets over, so check where each one should start."
	utilityVersion                   = "This mode needs Kafka Connect on Apache Kafka 3.6 or Confluent Platform 7.6 or later, which has the offsets REST API."
)

const docConnectorOffsets = "https://docs.confluent.io/cloud/current/connectors/offsets.html"
const docMigrateConnectors = "https://confluentinc.github.io/kcp/latest/command-reference/create-asset/migrate-connectors/"
const docCreateCluster = "https://docs.confluent.io/cloud/current/clusters/create-cluster.html"
const docReplicator = "https://docs.confluent.io/platform/current/multi-dc-deployments/replicator/replicator-failover.html"
const docConnectMigrationUtility = "https://docs.confluent.io/cloud/current/connectors/migrate-self-managed-connectors.html"

// RenderEnginePlanMarkdown renders the engine-driven plan as Markdown. A withheld
// cluster shows only why it needs a specialist (no automated recommendation, in
// plan.md or plan.json); every other cluster shows the full recommendation set.
// The "Talk to a person" section renders once at the end.
func RenderEnginePlanMarkdown(ep *EnginePlan) string {
	md := markdown.New()
	md.AddHeading("Migration Plan", 1)

	h := ep.Header
	meta := fmt.Sprintf("Source: %s · Generated %s · kcp %s",
		h.Source, h.GeneratedAt.Format("2006-01-02 15:04 MST"), h.KCPVersion)
	if !h.StateGeneratedAt.IsZero() {
		meta += " · scanned " + h.StateGeneratedAt.Format("2006-01-02 15:04 MST")
	}
	md.AddParagraph(meta)

	// Plan-inputs that could not be applied (unknown keys, invalid values,
	// unsupported sources). Surfaced near the top so a reader notices mis-typed
	// inputs rather than trusting a plan that silently dropped them.
	if len(ep.Warnings) > 0 {
		body := "Some plan-inputs were not applied:"
		for _, w := range ep.Warnings {
			body += "\n- " + w
		}
		md.AddAlert(markdown.AlertWarning, body)
	}

	if len(ep.Clusters) == 0 {
		md.AddParagraph("No clusters found in the source state file.")
		return md.String()
	}

	renderSummary(md, ep)

	// No state-file path means the plan ran as a questionnaire (no scan), so copy
	// that would otherwise reference the scan is softened per cluster.
	scanless := ep.Header.StateFilePath == ""
	if scanless {
		// A questionnaire-only plan is a valid mode; frame the scan as an upgrade for
		// sharper sizing and ready-to-run commands, not a prerequisite you skipped.
		// `kcp discover` is MSK-only, so a declared non-MSK source names only
		// `kcp scan clusters`; otherwise the source is open or MSK and both are named.
		scanTool := "run `kcp discover` (MSK) or `kcp scan clusters`"
		if anyNonMSKSource(ep) {
			scanTool = "run `kcp scan clusters`"
		}
		md.AddAlert(markdown.AlertNote, "This plan was generated from your answers alone (no scan), which is a complete way to plan. For sharper sizing and ready-to-run commands, "+scanTool+" to produce a state file, then re-run this with `--state-file <file>`.")
	}
	// When the whole fleet lacks throughput metrics, the sizing lower-bound caveat
	// is stated once at the top (in renderSummary), so per-cluster it shortens to a
	// pointer instead of repeating the full remediation.
	shortSizingNote := fleetSizingIsLowerBound(ep)
	for _, cp := range ep.Clusters {
		renderCluster(md, cp, ep.Header.StateFilePath, scanless, shortSizingNote)
		md.AddHorizontalRule()
	}

	renderQuestions(md, ep)
	renderHandoffCTA(md)
	return md.String()
}

// renderSummary renders the fleet roll-up at the top of the plan: one row per
// migration plan (kcp models one plan per cluster) with its state and what's
// blocking it, then the fleet-wide open-question tally.
func renderSummary(md *markdown.Markdown, ep *EnginePlan) {
	s := ep.Summary
	md.AddHeading("Summary", 2)
	// Only name a region count when there is one (MSK scans have regions; OSK/CP and
	// scanless plans have none, where "across 0 regions" reads oddly).
	scope := fmt.Sprintf("**%d cluster%s**", s.Clusters, plural(s.Clusters))
	if s.Regions > 0 {
		scope += fmt.Sprintf(" across **%d region%s**", s.Regions, plural(s.Regions))
	}
	lead := scope + ". Each cluster gets one migration plan, split into an infrastructure half and an application half. Where a cluster runs multiple apps with different needs, the application half is per app."
	if s.NeedsSpecialist > 0 {
		lead += fmt.Sprintf(" %d routed to a specialist.", s.NeedsSpecialist)
	}
	md.AddParagraph(lead)

	rows := make([][]string, 0, len(ep.Clusters))
	for _, cp := range ep.Clusters {
		region := cp.Region
		if region == "" {
			// A cluster with no region (the scanless placeholder) reads better as a
			// dash than the literal "unknown", which looks like a rendering glitch.
			region = "—"
		}
		name := fmt.Sprintf("[%s](#%s)", cp.ClusterID, headingAnchor(clusterHeadingTitle(cp)))
		hold := openRequiredUnheld(cp)
		infra := planGroupState(cp.Plan.Withheld, cp.Contingent, infraVerdicts, hold)
		app := planGroupState(cp.Plan.Withheld, cp.Contingent, appVerdicts, hold)
		if cp.Plan.Withheld {
			// A withheld cluster routes to one specialist conversation, not an
			// independent infrastructure half plus application half (the section below is
			// a single blended block, not the two-plan split a Ready cluster gets). So
			// state the routing once and mark the application cell as the same handoff,
			// rather than repeating "Needs a specialist" as if the halves were held
			// separately.
			app = "(same handoff)"
		}
		rows = append(rows, []string{name, region, infra, app})
	}
	md.AddTable([]string{"Migration plan", "Region", "Infrastructure plan", "Application plan"}, rows)

	// When no cluster in the fleet has scanned throughput, the sizing lower-bound
	// caveat applies to every plan, so state it once here and shorten the per-cluster
	// pointers rather than repeating the full remediation under each cluster.
	if fleetSizingIsLowerBound(ep) {
		metricsCmd := fleetMetricsCommand(ep)
		caveat := "No throughput metrics were scanned, so all sizing below is a lower bound. Run " + metricsCmd + " to size from real ingress/egress before you commit."
		if ep.Header.StateFilePath == "" {
			// No scan ran at all: "were scanned" would imply a scan that came up empty.
			caveat = "No throughput metrics are available (no scan), so all sizing below is a lower bound. Run " + metricsCmd + " to size from real ingress/egress before you commit."
		}
		md.AddParagraph(caveat)
	}

	if s.OpenRequired > 0 || s.OpenOptional > 0 {
		// A specialist-routed cluster's questions are excluded from this tally, so
		// note that only when at least one cluster is actually withheld; otherwise the
		// parenthetical is noise.
		tail := "."
		for _, cp := range ep.Clusters {
			if cp.Plan.Withheld {
				tail = " (specialist-routed clusters excluded)."
				break
			}
		}
		// This is a per-cluster tally, so a fleet-wide answer is counted once per cluster
		// it applies to; with more than one cluster, say so, since answering under
		// `all_clusters` clears it everywhere at once.
		if len(ep.Clusters) > 1 && s.OpenRequired > 0 {
			tail = strings.TrimSuffix(tail, ".") + " — many are fleet-wide; answer them once under `all_clusters` and they clear for every cluster."
		}
		md.AddParagraph(fmt.Sprintf("%d required + %d optional question%s open across all plans%s",
			s.OpenRequired, s.OpenOptional, plural(s.OpenRequired+s.OpenOptional), tail))
	} else {
		md.AddParagraph("All required questions are answered.")
	}
	// The primary next action, when answers are still open: edit and re-run.
	if s.OpenRequired > 0 {
		md.AddParagraph("**Next:** answer the required questions in `plan-inputs.yaml` (see [Answers by cluster](#answers-by-cluster) for what's open), then re-run `" + rerunCommand(ep) + "`.")
	}
	// Before the plan is complete, the specialist helps build it; once every plan is
	// settled, they walk through the finished plan.
	handoff := "A Confluent migration specialist can walk through this plan with you, for free."
	if s.OpenRequired > 0 {
		handoff = "A Confluent migration specialist can help you build the plan, for free."
	}
	md.AddParagraph("**Prefer a hand?** [Talk to a person](#talk-to-a-person). " + handoff)
}

// A migration plan splits into an infrastructure half (the shared cluster
// shape) and an application half (how the data and its dependents move). Each
// half's state is derived from whether any of its verdicts is still held pending
// an answer.
var (
	infraVerdicts = []string{nodeClusterType, nodeSizing, nodeNetworking, nodeAuth}
	appVerdicts   = []string{nodeDataMigration, nodeSchema, nodeConnectors, nodeTopics, nodeHistorical}
)

// planGroupState reports a plan half's readiness as a display label, sourced from
// the canonical state vocabulary (stateLabel): "Needs a specialist" when the whole
// plan is withheld, "Needs answers" when any verdict in the group is held pending a
// required answer, otherwise "Ready". hold keeps a half from reading "Ready" while the
// cluster still has an open required question that no verdict is waiting on.
func planGroupState(withheld bool, contingent map[string][]string, verdicts []string, hold bool) string {
	return stateLabel(planGroupStateCode(withheld, contingent, verdicts, hold))
}

// planGroupStateCode is the canonical state code for a plan half (for plan.json),
// so the machine record and the plan.md badge derive from one place.
func planGroupStateCode(withheld bool, contingent map[string][]string, verdicts []string, hold bool) string {
	if withheld {
		return StateNeedsSpecialist
	}
	for _, v := range verdicts {
		if _, held := contingent[v]; held {
			return StateNeedsAnswers
		}
	}
	if hold {
		return StateNeedsAnswers
	}
	return StateReady
}

// openRequiredUnheld reports whether the cluster has an open required question that
// no verdict is waiting on (for example source_platform, which sets the whole source
// axis rather than gating one recommendation). Every half of the plan rests on it, so
// none can read "Ready" until it is answered.
func openRequiredUnheld(cp ClusterPlan) bool {
	waitedOn := map[string]bool{}
	for _, keys := range cp.Contingent {
		for _, k := range keys {
			waitedOn[k] = true
		}
	}
	for _, q := range cp.Questions {
		if q.Status == statusOpenRequired && !waitedOn[q.Key] {
			return true
		}
	}
	return false
}

// renderSectionStatus writes a plan half's readiness line under its heading, so
// each half carries its own state (matching the summary table). "Needs answers" is
// an action for the reader, so it renders as an IMPORTANT alert deep-linked to this
// plan's Q&A subsection; a ready half is a plain line.
func renderSectionStatus(md *markdown.Markdown, state, qaAnchor string) {
	if state == "Needs answers" {
		md.AddAlert(markdown.AlertImportant, "**Needs answers.** The `Pending` rows below unlock once you answer this plan's [Required questions (Action needed)](#"+qaAnchor+").")
		return
	}
	md.AddParagraph("**" + state + "**")
}

// answersHeadingTitle is the "### Cluster: …" heading for a cluster's Q&A
// subsection under "Answers by cluster". It must slug to a different anchor than
// the top "## Cluster: …" heading so a plan half can deep-link to the Q&A block
// rather than the top section. When the cluster carries a region (or a distinct
// plan-inputs key), the top heading already differs; but a region-less cluster
// whose key equals its name (the scanless placeholder, or an Apache Kafka source
// with no region) would slug identically to this one, so we add an "(answers)"
// suffix in exactly that case to keep the two anchors distinct.
func answersHeadingTitle(cp ClusterPlan) string {
	title := "Cluster: " + cp.Key
	if cp.Region == "" && cp.Key == cp.ClusterID {
		title += " (answers)"
	}
	return title
}

// qaAnchor is the fragment of a cluster's Q&A subsection under the per-cluster
// answers section, so a plan half can deep-link to it. Derived from
// answersHeadingTitle so the anchor and the heading it points at stay in sync and
// never collide with the top "## Cluster: …" heading.
func qaAnchor(cp ClusterPlan) string { return headingAnchor(answersHeadingTitle(cp)) }

// clusterHeadingTitle is the "## Cluster: …" heading text for a plan. Built in
// one place so the heading and any in-document link to it stay in sync.
func clusterHeadingTitle(cp ClusterPlan) string {
	title := "Cluster: " + cp.ClusterID
	if cp.Region != "" {
		title += " (" + cp.Region + ")"
	}
	// Disambiguate when the plan-inputs key isn't just the name (collision case),
	// so the heading maps unambiguously to the plan-inputs.yaml section.
	if cp.Key != cp.ClusterID {
		title += " (key `" + cp.Key + "`)"
	}
	return title
}

// headingAnchor slugifies a heading to the fragment a GitHub-flavored-markdown
// viewer generates for it: lower-case, spaces to hyphens, every other character
// (punctuation, parens, backticks, em dashes) dropped.
func headingAnchor(title string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(title) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		case r == ' ':
			b.WriteRune('-')
		}
	}
	return b.String()
}

// rerunCommand is the `kcp report plan …` command to re-run after editing inputs,
// shown by file name only (no absolute path) since it lands in shareable files;
// the exact paths already print in the terminal.
func rerunCommand(ep *EnginePlan) string {
	cmd := "kcp report plan"
	if ep.Header.StateFilePath != "" {
		cmd += " --state-file " + ep.Header.StateFilePath
	}
	return cmd + " --plan-inputs plan-inputs.yaml"
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// renderQuestions renders the one-time question catalog (wording + options,
// listed once for the whole fleet) followed by a terse per-cluster "Actions
// needed" section that lists each cluster's open and answered questions by key
// and value, referencing the catalog rather than repeating the wording.
func renderQuestions(md *markdown.Markdown, ep *EnginePlan) {
	renderQuestionCatalog(md, ep)

	scanless := ep.Header.StateFilePath == ""
	filledIn := "optional defaults, or facts from your scan and earlier answers"
	if scanless {
		filledIn = "optional defaults, presets, and any earlier answers"
	}
	md.AddHeading("Answers by cluster", 2)
	md.AddParagraph("Every question and its current answer, per cluster. The **Required questions (Action needed)** rows are the ones still to answer; the rest are already filled in (" + filledIn + "). Set answers in `plan-inputs.yaml` (wording and options are in [Questions](#questions) above), then re-run `" + rerunCommand(ep) + "` (adjust the paths to where your files are). Answering some may reveal follow-up questions.")

	for _, cp := range ep.Clusters {
		req, opt := openCounts(cp.Questions)
		md.AddHeading(answersHeadingTitle(cp), 3)
		if cp.Plan.Withheld {
			md.AddParagraph("_This cluster is going to a specialist. The answers below are context for that conversation, not blockers to an automated plan._")
		} else {
			md.AddParagraph(fmt.Sprintf("_%d required, %d optional open. Edit `plan-inputs.yaml` under `%s` to answer._", req, opt, cp.Key))
		}
		renderQuestionTables(md, cp.Questions, 4, cp.Plan.Withheld, scanless)

		// Per-app overrides, when the cluster declares applications.
		for _, ap := range cp.Apps {
			md.AddHeading("Application: "+ap.Name, 4)
			md.AddParagraph("_Effective answers for this app (source shown); edit under `applications:` in plan-inputs.yaml to override._")
			renderQuestionTables(md, ap.Questions, 5, ap.Plan.Withheld, scanless)
		}
	}
}

// renderQuestionTables renders one question set as three tables: the required
// questions still to answer (headed "Action needed"), the optional questions with
// their pre-selected defaults, and the answers already derived from the scan or an
// earlier run. Key and value only — the wording lives in the catalog above.
func renderQuestionTables(md *markdown.Markdown, qs []ResolvedQuestion, level int, withheld, scanless bool) {
	var required, optional, answered [][]string
	for _, q := range qs {
		key := "`" + q.Key + "`"
		tag := ""
		if prov := q.provenance(); prov != "" {
			// With no scan, a scanCatalog fact is a preset default, not a scan finding,
			// so "(from scan)" would assert a scan that never ran.
			if scanless && prov == "from scan" {
				prov = "preset"
			}
			tag = " _(" + prov + ")_"
		}
		switch {
		case q.Status == "open_required":
			// Anything still required to answer — including a scan fact the scan
			// didn't capture — belongs here.
			required = append(required, []string{key})
		case q.Status == statusOpenInert:
			// Required in the catalog, but answering it changes nothing for this
			// cluster, so it doesn't block: shown as an optional, answerable knob.
			optional = append(optional, []string{key + ": `(optional here — doesn't change this cluster's plan)`"})
		case q.Scan:
			// Scan-detected facts (or a customer-supplied one), regardless of value.
			val := "`" + q.Value + "`"
			if q.Value == "" {
				val = "_(not detected)_"
			}
			answered = append(answered, []string{key + ": " + val + tag})
		case q.Status == "answered":
			// A question you've answered (in all_clusters or on the cluster), required or
			// optional. Answered optionals belong here, "edit to override", not in the
			// "Optional (defaults pre-selected)" table — otherwise that table overcounts
			// versus the summary's "N optional open", which counts only still-default ones.
			answered = append(answered, []string{key + ": `" + q.Value + "`" + tag})
		default:
			// Every still-open optional question (at its default) — one section, and its
			// count matches the summary's "N optional questions open".
			switch {
			case q.Value != "":
				optional = append(optional, []string{key + ": `" + q.Value + "`"})
			case q.Multi:
				// A list question with nothing selected renders as [], matching
				// plan-inputs.yaml (for target_auth, empty means kcp picks the method).
				optional = append(optional, []string{key + ": `[]`"})
			default:
				optional = append(optional, []string{key + ": `(unset)`"})
			}
		}
	}
	if len(required) > 0 {
		heading := "Required questions (Action needed)"
		col := "Answer these (key)"
		if withheld {
			// Not a blocker for a specialist-routed cluster: these are context.
			heading = "Questions your specialist will use"
			col = "Question (key)"
		}
		md.AddHeading(heading, level)
		// Give the open required questions a coloured callout so they stand out from the
		// plain optional/answered tables (matching how the plan flags "Needs answers"
		// elsewhere). A specialist-routed cluster's questions are context, not blockers,
		// so they stay plain.
		if !withheld {
			md.AddAlert(markdown.AlertImportant, "**Action needed.** These required questions are still open. Answer them in `plan-inputs.yaml`; the plan can't complete until you do.")
		}
		md.AddTable([]string{col}, required)
	}
	if len(optional) > 0 {
		md.AddHeading("Optional questions (defaults pre-selected)", level)
		md.AddTable([]string{"Default (key: value)"}, optional)
	}
	if len(answered) > 0 {
		heading := "Already answered / from your scan (edit to override)"
		if scanless {
			heading = "Already answered / preset (edit to override)"
		}
		md.AddHeading(heading, level)
		md.AddTable([]string{"Current (key: value)"}, answered)
	}
}

// backtickLegendToken wraps the leading option token of a "token → label" legend
// line in backticks, so the value the customer writes stands out as code.
func backtickLegendToken(line string) string {
	if i := strings.Index(line, " → "); i > 0 {
		return "`" + line[:i] + "`" + line[i:]
	}
	return line
}

// renderQuestionCatalog lists every question once — key, wording (prompt + hint),
// and options — so the per-cluster tables can stay terse and just reference it.
func renderQuestionCatalog(md *markdown.Markdown, ep *EnginePlan) {
	seen := map[string]bool{}
	type catRow struct {
		key  string
		cols []string
	}
	var rows []catRow
	for _, cp := range ep.Clusters {
		for _, q := range cp.Questions {
			if seen[q.Key] {
				continue
			}
			seen[q.Key] = true
			question := q.Prompt
			if q.Hint != "" {
				question += "<br>_" + q.Hint + "_"
			}
			var options string
			if len(q.Legend) > 0 {
				lines := make([]string, len(q.Legend))
				for i, line := range q.Legend {
					lines[i] = backtickLegendToken(line)
				}
				options = strings.Join(lines, "<br>")
			} else {
				toks := make([]string, len(q.Tokens))
				for i, t := range q.Tokens {
					toks[i] = "`" + t + "`"
				}
				options = strings.Join(toks, " · ")
			}
			// A multi-select answer is a YAML list; show that so it doesn't read as
			// pick-one (the `a | b` legend looks like a choice of one).
			if q.Multi && len(q.Tokens) >= 2 {
				options += "<br>_Select all that apply, as a list, for example `[" + q.Tokens[0] + ", " + q.Tokens[1] + "]`._"
			}
			rows = append(rows, catRow{key: q.Key, cols: []string{"`" + q.Key + "`", question, options}})
		}
	}
	if len(rows) == 0 {
		return
	}
	// Order the catalog by its canonical position (the intake `catalog` block, then
	// the `scanCatalog` block), not by which cluster first surfaced each question,
	// so the table reads in the same order as the questionnaire. Keys not in either
	// slice sort last (stable).
	rank := map[string]int{}
	for i, q := range catalog {
		rank[q.Key] = i
	}
	for i, q := range scanCatalog {
		rank[q.Key] = len(catalog) + i
	}
	rankOf := func(key string) int {
		if r, ok := rank[key]; ok {
			return r
		}
		return len(catalog) + len(scanCatalog) + 1
	}
	sort.SliceStable(rows, func(i, j int) bool {
		return rankOf(rows[i].key) < rankOf(rows[j].key)
	})
	out := make([][]string, len(rows))
	for i, r := range rows {
		out[i] = r.cols
	}
	md.AddHeading("Questions", 2)
	md.AddParagraph("Every question, its wording, and its options, listed once. Set answers per cluster (or fleet-wide in `all_clusters:`) in `plan-inputs.yaml`; see **Answers by cluster** for each cluster's current answers and what's still needed.")
	md.AddTable([]string{"Key", "Question", "Options"}, out)
}

func renderCluster(md *markdown.Markdown, cp ClusterPlan, stateFilePath string, scanless, shortSizingNote bool) {
	md.AddHeading(clusterHeadingTitle(cp), 2)

	if scanless {
		md.AddParagraph("**Source:** No scan, answers only. Every fact is taken from `plan-inputs.yaml`.")
	} else {
		kind := "Provisioned"
		if cp.IsServerless {
			kind = "Serverless"
		}
		auths := "not detected"
		// The effective auth (answers folded in), so a source_auth override shows here.
		shown := cp.effectiveSourceAuths
		if len(shown) == 0 {
			shown = cp.SourceAuths
		}
		if len(shown) > 0 {
			friendly := make([]string, len(shown))
			for i, a := range shown {
				friendly[i] = friendlyAuthLabel(a)
			}
			auths = strings.Join(friendly, ", ")
		}
		// A non-MSK (Apache Kafka / Confluent Platform) source has no Provisioned vs
		// Serverless axis and no broker-node count in the scan, so its header names the
		// platform alone; MSK keeps its exact "MSK <kind>" wording.
		var src string
		if cp.SourcePlatform != "" {
			src = "**Source:** " + cp.SourcePlatform
		} else {
			src = "**Source:** MSK " + kind
		}
		// Only show counts the scan actually captured — "0 topics · 0 brokers" reads
		// as broken when the scan simply didn't collect them.
		if cp.TopicCount > 0 {
			src += fmt.Sprintf(" · %d topics", cp.TopicCount)
		}
		if cp.BrokerCount > 0 {
			src += fmt.Sprintf(" · %d brokers", cp.BrokerCount)
		}
		md.AddParagraph(src + " · auth: " + auths)
	}

	p := cp.Plan
	// Scan-derived caveats are folded into the relevant recommendation's "Why"
	// (keyed by verdict), not shown as a separate Observations section.
	caveats := caveatBullets(cp.Observations, shortSizingNote)
	switch {
	case p.Withheld:
		// Routed to a specialist: we do not put an automated recommendation in front
		// of the customer, so nothing but the "why" is shown (and plan.json redacts
		// the verdicts too, see PlanResult.MarshalJSON). Caveats ride with the hidden
		// verdicts, so none are shown either.
		renderWithheld(md, cp, scanless)
	case len(cp.Apps) == 0:
		// One infrastructure plan, then the single application plan — same split as
		// the multi-application case, so the shape is consistent and multi-app is a
		// natural extension. Both halves deep-link to this cluster's Q&A subsection.
		frag := qaAnchor(cp)
		renderInfraRecommendation(md, p, cp.Contingent, frag, caveats, openRequiredUnheld(cp))
		md.AddHeading("Application plan", 3)
		renderAppRecommendation(md, p, cp.Contingent, frag, caveats, openRequiredUnheld(cp))
		// Shared, per-cluster: one cluster link serves every application, so the
		// migration infrastructure is its own peer section after both plans, not
		// nested under either.
		renderMigrationInfra(md, cp, stateFilePath)
	default:
		// One infrastructure plan shared across applications, then one application
		// plan per declared application, then the shared migration infrastructure.
		frag := qaAnchor(cp)
		renderInfraRecommendation(md, p, cp.Contingent, frag, caveats, openRequiredUnheld(cp))
		for _, ap := range cp.Apps {
			md.AddHeading("Application plan: "+ap.Name, 3)
			if ap.Plan.Withheld {
				md.AddAlert(markdown.AlertWarning, "This application is routed to a Confluent migration specialist instead of an automated plan. [Talk to a person](#talk-to-a-person) to size it with a specialist.")
				continue
			}
			renderAppRecommendation(md, ap.Plan, ap.Contingent, frag, caveats, openRequiredUnheld(cp))
		}
		renderMigrationInfra(md, cp, stateFilePath)
	}
}

// renderHandoffCTA renders the fleet-wide "talk to an expert" call to action
// once, at the end of the plan.
func renderHandoffCTA(md *markdown.Markdown) {
	md.AddHeading("Talk to a person", 2)
	md.AddParagraph("Migrating to Confluent is easier with a hand. Reach a Confluent migration specialist in the Confluent Cloud Migration Hub: https://confluent.cloud/migration-hub")
}

func renderWithheld(md *markdown.Markdown, cp ClusterPlan, scanless bool) {
	p := cp.Plan
	md.AddHeading("Let's size this one together", 3)
	md.AddAlert(markdown.AlertNote, "This cluster is a better fit for a quick conversation with a Confluent migration specialist than an automated plan, so we've held the recommendation. [Talk to a person](#talk-to-a-person) and we'll size it with you, for free.")
	// Lead each trigger with what set it off (provenance), matching the
	// "Based on your answer (…)" voice of the recommendation whys. The size-driven
	// triggers already carry the measured value that crossed the line.
	if len(p.HumanAssist.Triggers) > 0 {
		md.AddHeading("Why we'd loop in a specialist", 4)
		// Each trigger Why is already in the one normalized shape ("Based on <src>:
		// <reason> <CTA>"), so render them verbatim.
		var items []string
		for _, t := range p.HumanAssist.Triggers {
			items = append(items, t.Why)
		}
		md.AddList(items)
	}
	// The verdicts are redacted for a specialist-routed cluster, but its scanned
	// sizing facts are exactly what the specialist needs, so surface them here rather
	// than leaving them only in plan.json. Render them with the same helper a Ready
	// cluster uses, so identical numbers read the same way ("Sized from your scan: ….
	// Not measured: …") regardless of whether the cluster was routed to a specialist.
	renderSizingSignals(md, p.Sizing.Signals)
	// A Ready cluster folds its scan observations (tiered storage, unmeasured
	// throughput and the like) into the "Why" table; a withheld cluster has no such
	// table, so its observations follow as the specialist's supporting context.
	var ctx []string
	for _, o := range cp.Observations {
		title := o.Title
		// Scanless: these facts came from answers, not a scan, so the no-throughput
		// note reads as "measured" rather than "scanned".
		if scanless && title == "No throughput metrics scanned" {
			title = "No throughput measured"
		}
		ctx = append(ctx, "**"+title+":** "+o.Detail)
	}
	if len(ctx) > 0 {
		heading := "What else the scan showed"
		if scanless {
			heading = "What else your answers showed"
		}
		md.AddHeading(heading, 4)
		md.AddList(ctx)
	}
}

// isContingent reports whether a verdict is held pending an unanswered question.
func isContingent(name string, c map[string][]string) bool { return len(c[name]) > 0 }

// friendlyAuthLabel maps a scanned source-auth token to its display label, so the
// cluster header reads "AWS IAM, SASL/SCRAM" rather than the raw "iam, scram".
func friendlyAuthLabel(token string) string {
	switch token {
	case SourceAuthIAM:
		return "AWS IAM"
	case SourceAuthSCRAM:
		return "SASL/SCRAM"
	case SourceAuthSASLPlain:
		return "SASL/PLAIN"
	case SourceAuthMTLS:
		return "mTLS"
	case SourceAuthUnauth:
		return "unauthenticated"
	case SourceAuthKerberos:
		return "Kerberos"
	}
	return token
}

// article returns "a" or "an" for a word, by its leading sound (vowel-letter
// approximation, which is right for the cluster-type names here).
func article(word string) string {
	if word == "" {
		return "a"
	}
	if strings.ContainsRune("AEIOUaeiou", rune(word[0])) {
		return "an"
	}
	return "a"
}

// firstSentence returns the first sentence of a reason — the short "why" for the
// table cell; the full reason still appears in "Why these recommendations".
func firstSentence(s string) string {
	if i := strings.Index(s, ". "); i >= 0 {
		return s[:i+1]
	}
	return s
}

// shortWhy is the brief table "Why" for a verdict that carries no authored short
// field: the first sentence of its reason with the "Based on …," provenance
// lead-in stripped, so the cell stays a terse clause and does not repeat the full
// reason (with its provenance) shown in "Why these recommendations".
func shortWhy(reason string) string {
	s := reason
	if strings.HasPrefix(s, "Based on ") {
		// The lead-in closes at the first "), " (triggers never contain it), after
		// which the clause continues lower-case.
		if i := strings.Index(s, "), "); i >= 0 {
			s = s[i+3:]
		}
	}
	s = firstSentence(s)
	if s != "" {
		r := []rune(s)
		r[0] = []rune(strings.ToUpper(string(r[0])))[0]
		s = string(r)
	}
	return s
}

func backticked(keys []string) string {
	q := make([]string, len(keys))
	for i, k := range keys {
		q[i] = "`" + k + "`"
	}
	return strings.Join(q, ", ")
}

// vrow builds a recommendation table row, replacing the value with "Pending" and
// naming the deciding questions when the verdict is contingent on an unanswered
// required question. id is a plan-node ID (contingency is keyed by ID); the row
// title shows its display label.
func vrow(id, value, why string, c map[string][]string) []string {
	label := nodeLabel(id)
	if isContingent(id, c) {
		// Data migration held only on the cutover-style question: the approach
		// (Cluster Linking) is decided and its steps are shown below; only the style
		// is pending, so don't blank the whole row to "Pending".
		if id == nodeDataMigration && onlyDriver(c[id], "downtime_tolerance") {
			return []string{label, "Cluster Linking (cutover style pending)", "Answer `downtime_tolerance` to set the cutover style; the migration approach is decided (see the migration steps below)."}
		}
		verb := "it decides"
		if len(c[id]) > 1 {
			verb = "they decide"
		}
		return []string{label, "Pending", "Answer " + backticked(c[id]) + " (" + verb + " this)."}
	}
	return []string{label, value, why}
}

// onlyDriver reports whether a contingent verdict's sole deciding question is key.
func onlyDriver(drivers []string, key string) bool {
	return len(drivers) == 1 && drivers[0] == key
}

// whyLine builds a "why" bullet, or a pending note when the verdict is contingent.
// id is a plan-node ID (contingency is keyed by ID); the bullet heading shows its
// display label.
func whyLine(id, reason string, c map[string][]string) string {
	label := nodeLabel(id)
	if isContingent(id, c) {
		if id == nodeDataMigration && onlyDriver(c[id], "downtime_tolerance") {
			return "**Data migration**: the approach (Cluster Linking) is decided and its steps are below; answer `downtime_tolerance` to set your cutover style."
		}
		answer := "Answer it and re-run to see this."
		if len(c[id]) >= 2 {
			answer = "Answer them and re-run to see this."
		}
		return "**" + label + "**: Pending, decided by " + backticked(c[id]) + ". " + answer
	}
	return "**" + label + "**: " + reason
}

// whyListCaveats builds the "Why these recommendations" bullets from (name,
// reason) pairs, with any scan-derived caveats nested as sub-bullets
// under each verdict's bullet, so a caveat reads as belonging to that decision
// rather than as a sibling recommendation. When the verdict's own reason is
// skipped (already shown in the table) but a caveat exists, a minimal labeled
// parent carries it.
func whyListCaveats(c map[string][]string, caveats map[string][]string, pairs ...string) []string {
	var out []string
	for i := 0; i+1 < len(pairs); i += 2 {
		id, reason := pairs[i], pairs[i+1]
		var bullet string
		switch {
		case isContingent(id, c):
			bullet = whyLine(id, reason, c)
		case reason != "":
			// The table cell now shows only a short clause (shortWhy), so the prose
			// carries the full reason for every verdict, not just the multi-sentence ones.
			bullet = whyLine(id, reason, c)
		case len(caveats[id]) > 0:
			bullet = "**" + nodeLabel(id) + "**" // parent for the caveat only
		default:
			continue
		}
		out = append(out, nestCaveats(bullet, caveats[id]))
	}
	return out
}

// nestCaveats appends each caveat as an indented sub-bullet of the given "Why"
// bullet (GitHub-flavored markdown renders "\n  - " as a nested list item).
func nestCaveats(bullet string, caveats []string) string {
	for _, cv := range caveats {
		bullet += "\n  - " + cv
	}
	return bullet
}

// renderMigrationInfra renders the end-to-end steps to build and run the migration
// for one cluster, adapted to the mechanism: a Cluster Link, Confluent Replicator
// (Standard/Basic targets), or start-fresh. Each step gives a kcp command where one
// exists, or Console guidance plus a docs link where it doesn't.
func renderMigrationInfra(md *markdown.Markdown, cp ClusterPlan, stateFilePath string) {
	mi := cp.MigrationInfra
	md.AddHeading("Migration steps", 3)
	// The target cluster (and everything after it) needs the infrastructure plan
	// settled, so hold the whole section only while that is pending.
	for _, v := range []string{nodeClusterType, nodeNetworking, nodeAuth} {
		if isContingent(v, cp.Contingent) {
			md.AddAlert(markdown.AlertNote, "Pending: available once the infrastructure plan above is settled.")
			return
		}
	}

	// A scanless run has no state file, so the create-asset commands below carry
	// placeholders (source type, cluster ID) rather than values wired to a real
	// cluster. Say so up front so a reader doesn't run them as-is.
	if stateFilePath == "" {
		// `kcp discover` is MSK-only. For a non-MSK source the source type is already
		// known (apache-kafka), so lead with `kcp scan clusters` and name the resolved type; MSK
		// keeps the original discover-first wording and the msk|apache-kafka placeholder.
		switch {
		case cp.SourcePlatform != "":
			md.AddAlert(markdown.AlertNote, "The `kcp create-asset …` commands below are a reference. They need a scanned `kcp-state.json` — with your real cluster ID — to run against your cluster: run `kcp scan clusters` first, then re-run this with `--state-file`, or hand this plan to your specialist. Until then, `--source-type apache-kafka` is set for your source and the cluster ID is a placeholder to fill in.")
		case cp.sourceDeclaredMSK:
			md.AddAlert(markdown.AlertNote, "The `kcp create-asset …` commands below are a reference. They need a scanned `kcp-state.json` — with your real cluster ID — to run against your cluster: run `kcp discover` first, then re-run this with `--state-file`, or hand this plan to your specialist. Until then, `--source-type msk` is set for your source and the cluster ID is a placeholder to fill in.")
		default:
			md.AddAlert(markdown.AlertNote, "The `kcp create-asset …` commands below are a reference. They need a scanned `kcp-state.json` — with your real cluster ID and source type — to run against your cluster: run `kcp discover` (MSK) or `kcp scan clusters` first, then re-run this with `--state-file`, or hand this plan to your specialist. Until then, `--source-type <msk|apache-kafka>` and the cluster ID are placeholders to fill in.")
		}
	}

	if mechanismSettled(cp) && (cp.Plan.Switchover.StartFresh || cp.Plan.Switchover.Replicator || mi.Type >= 1 || mi.SpecialistClusterLinkable) {
		beforeYouStart(md, cp, stateFilePath)
	}

	n := 0
	step := func(title string) string { n++; return fmt.Sprintf("**Step %d: %s**", n, title) }

	// The mechanism (start fresh / Replicator / cluster link) is fixed by the infra
	// tier and whether data moves; only the cutover style depends on downtime. If a
	// mechanism-changing answer is still pending, we can build the target cluster now
	// but can't lay out how the data moves yet — show the cluster step plus a note.
	if !mechanismSettled(cp) {
		md.AddParagraph("Your infrastructure plan is settled, so you can stand up the target cluster now. How your data moves and the cutover follow the Data migration recommendation, still pending above.")
		targetClusterStep(md, cp, stateFilePath, step)
		md.AddAlert(markdown.AlertNote, "The data-movement and cutover steps become available once you answer this cluster's data-migration questions.")
		return
	}

	sw := cp.Plan.Switchover
	switch {
	case sw.StartFresh:
		md.AddParagraph("You're starting fresh, so no existing data is copied. Create the new cluster, recreate its structure, then point your applications at it.")
		targetClusterStep(md, cp, stateFilePath, step)
		topicsStep(md, cp, stateFilePath, migrateTopicsModeNew, step)
		perAppDataOps(md, cp, stateFilePath, step)
		md.AddParagraph(step("cut over.") + " Move your producers to the new cluster first, let your consumers finish reading the old cluster (lag zero), then move them, starting from the earliest offset. The old cluster then ages out over its retention window.")
		clientCutoverChanges(md, cp)
		schemaCutoverNote(md, cp)
		md.AddParagraph("**Backing out:** your old cluster keeps running and serving your applications over its retention window, so nothing is committed by standing up the new one. To roll back, leave your clients pointed at the old cluster (or point them back to it).")
		decommissionStep(md, cp, step)
		pendingDataOpsNote(md, cp)

	case sw.Replicator:
		// Replicator is selected for two different reasons: a Standard/Basic
		// destination (below the Cluster Linking tier floor), or an Enterprise/Dedicated
		// destination whose SOURCE is below the Cluster Linking version floor (Kafka
		// <2.4 / IBP <2.8). Name the one that actually applies rather than always
		// claiming the tier reason.
		replicatorReason := "Cluster Linking needs an Enterprise or Dedicated cluster"
		if cp.Plan.ClusterType.Value != engine.TierStandard {
			floorType := engine.SourceMSK
			switch cp.SourcePlatform {
			case enginePlatformOSK:
				floorType = engine.SourceApacheKafka
			case enginePlatformCP:
				floorType = engine.SourceConfluentPlatform
			}
			replicatorReason = "your source is below the Cluster Linking floor (" + engine.ClusterLinkingFloor(floorType) + ")"
		}
		md.AddParagraph("Your data moves with Confluent Replicator, because " + replicatorReason + ". Create the cluster and its structure, run Replicator, then cut over.")
		targetClusterStep(md, cp, stateFilePath, step)
		topicsStep(md, cp, stateFilePath, migrateTopicsModeNew, step)
		perAppDataOps(md, cp, stateFilePath, step)
		md.AddParagraph(step("copy your data with Confluent Replicator.") + " Run Confluent Replicator on a Kafka Connect worker in your own account to copy data from your source into the new cluster ([docs](" + docReplicator + ")). It reads your source with your existing credentials and writes into Confluent Cloud with the target credentials (the client authentication) you set up in Step 1, and it needs a Confluent Platform Enterprise license.")
		md.AddParagraph(step("cut over.") + " Once Replicator has caught up, move your clients to Confluent Cloud. Consumer offsets don't carry over on their own: translate them with the Confluent timestamp interceptor, added to every consumer before you start (Java clients only) ([docs](" + docReplicator + ")). Non-Java consumers have no automatic offset-translation path here — they resume according to each consumer's `auto.offset.reset` (reprocessing from earliest, or skipping to latest), so plan their cutover accordingly.")
		if replicatorKeepsSelfManagedSinks(cp) {
			md.AddParagraph(selfManagedSinkOffsets)
		}
		clientCutoverChanges(md, cp)
		schemaCutoverNote(md, cp)
		md.AddParagraph("**Backing out:** your source keeps taking writes and serving your applications until you move the clients, so nothing is committed before cutover. To roll back, leave your clients pointed at the still-running source (or point them back to it) instead of completing the move.")
		decommissionStep(md, cp, step)
		pendingDataOpsNote(md, cp)

	case mi.Type >= 1:
		md.AddParagraph("Build the infrastructure this plan recommends, then move your data over it. `kcp` generates the Terraform for each step; you review it and `terraform apply`.")
		// A "zero downtime" reader must see this caveat before following steps that
		// include a producer stop, so it leads the section rather than trailing it.
		gatewayCutover := sw.GatewayMediated
		if gatewayCutover {
			md.AddAlert(markdown.AlertNote, "The Gateway-mediated cutover your Data migration recommendation calls for runs through the Confluent Gateway with `kcp migration`, which needs Confluent for Kubernetes and a Gateway license. [Talk to a person](#talk-to-a-person) to set up the Gateway, and deploy it and route your clients through it, before you run the cutover step below. The Gateway also needs the backend secrets for its credential swap in your secret store before the cutover.")
		}
		targetClusterStep(md, cp, stateFilePath, step)
		// The source credentials (and any listener the link signs in with) must be in
		// place before the link is applied, so that step comes first; a jump cluster
		// also needs its PrivateLink endpoint before the link step takes its ID.
		refs := linkRefs{}
		next := n + 1
		if len(sw.SourceCredentialHandling) > 0 {
			refs.credentials = next
			next++
		}
		if mi.JumpCluster {
			refs.privateLink = next
			next++
		}
		credentialsStep(md, cp, step, next)
		if mi.JumpCluster {
			jumpPrivateLinkStep(md, next, step)
		}
		linkStep(md, cp, stateFilePath, step, refs)
		topicsStep(md, cp, stateFilePath, migrateTopicsModeMirror, step)
		perAppDataOps(md, cp, stateFilePath, step)
		clusterLinkCutoverStep(md, cp, step)
		decommissionStep(md, cp, step)
		pendingDataOpsNote(md, cp)

	case mechanismSettled(cp) && mi.SpecialistClusterLinkable:
		// A cluster-linking migration whose infra type couldn't be auto-mapped — mTLS
		// (or undetected) source auth on an otherwise supported tier. The cutover is the
		// standard Cluster Linking flow; only the link's authentication is designed with
		// a specialist, so we render the standard steps and call that piece out (the same
		// steps-plus-NOTE shape the Gateway cutover uses). Government targets are excluded:
		// Confluent Cloud for Government has no Cluster Linking at all.
		md.AddParagraph("Your data moves over a Cluster Link. The cutover below is the standard Cluster Linking flow; the one piece we design with you is the link's setup.")
		md.AddAlert(markdown.AlertNote, "**"+mi.Label+".** "+mi.Rationale+" See the [migration-infra docs]("+docMigrationInfra+"), or [Talk to a person](#talk-to-a-person) to wire up the migration link; the steps below are otherwise the standard flow.")
		targetClusterStep(md, cp, stateFilePath, step)
		// The topic steps below name the link (`<your-link-name>`), so a step has to create
		// it first; kcp generates no link here, so a specialist sets it up.
		md.AddParagraph(step("set up the cluster link with a specialist.") + " A specialist sets up the cluster link between your source and the new cluster ([Talk to a person](#talk-to-a-person)). Use the link's name for `<your-link-name>` in the steps below.")
		topicsStep(md, cp, stateFilePath, migrateTopicsModeMirror, step)
		perAppDataOps(md, cp, stateFilePath, step)
		clusterLinkCutoverStep(md, cp, step)
		decommissionStep(md, cp, step)
		pendingDataOpsNote(md, cp)

	default:
		// government / other route with no standard type and no settled cluster-link
		// mechanism: a specialist designs it.
		md.AddParagraph("**" + mi.Label + "**: " + mi.Rationale)
	}

	// Access control applies to every target that actually gets built (not the
	// government/other specialist route, which a specialist designs end to end). It's
	// set up separately from moving data, so call it out once here.
	if sw.StartFresh || sw.Replicator || mi.Type >= 1 || mi.SpecialistClusterLinkable {
		accessControlNote(md, cp)
	}
}

// beforeYouStart lists the scans the steps below read from the state file and that
// it doesn't hold yet, so a reader runs them first instead of hitting a missing-data
// error mid-migration. It names only what this plan's steps use: topics when the
// state has none, the source Schema Registry when a schema migration runs, and the
// connector runtime(s) when connectors are rebuilt. Nothing is printed when the
// state already covers it all.
func beforeYouStart(md *markdown.Markdown, cp ClusterPlan, stateFilePath string) {
	state := "kcp-state.json"
	if stateFilePath != "" {
		state = stateFilePath
	}
	var items []string

	// Topics: migrate-topics reads them from the state file. A scan-based run that
	// captured none (topic discovery skipped) needs a topic scan; a scanless run has
	// no state file at all, which the reference note above already explains.
	if stateFilePath != "" && !cp.topicsScanned && !isContingent(nodeTopics, cp.Contingent) {
		if cp.SourcePlatform != "" {
			items = append(items, "Topics: `kcp scan clusters --source-type apache-kafka --state-file "+state+" --credentials-file apache-kafka-credentials.yaml`")
		} else {
			items = append(items, "Topics: `kcp scan clusters --source-type msk --state-file "+state+" --credentials-file msk-credentials.yaml`, or re-run `kcp discover` without `--skip-topics`")
		}
	}

	var schemaKinds = map[engine.SchemaKind]bool{}
	var src ConnectorSource
	for _, av := range appViews(cp) {
		if !isContingent(nodeSchema, av.contingent) && schemaNeedsMigration(av.plan) {
			schemaKinds[av.plan.Schema.Kind] = true
		}
		if !isContingent(nodeConnectors, av.contingent) && connectorsNeedRebuild(av.plan) {
			src.MSKConnect = src.MSKConnect || av.connectorSrc.MSKConnect
			src.SelfManaged = src.SelfManaged || av.connectorSrc.SelfManaged
		}
	}

	// Schemas: migrate-schemas reads the source registry from the state file.
	if len(schemaKinds) > 0 && !cp.schemaRegistryScanned {
		if schemaKinds[engine.SchemaKindGlueBulk] {
			reg, region := "<glue-registry-name>", "<glue-region>"
			if cp.SchemaSource != nil && cp.SchemaSource.GlueRegistry != "" {
				reg = cp.SchemaSource.GlueRegistry
			}
			if cp.SchemaSource != nil && cp.SchemaSource.GlueRegion != "" {
				region = cp.SchemaSource.GlueRegion
			}
			items = append(items, "Schemas: `kcp scan schema-registry --state-file "+state+" --sr-type glue --registry-name "+reg+" --region "+region+"`")
		}
		if schemaKinds[engine.SchemaKindLinking] {
			url := "<source-sr-url>"
			if cp.SchemaSource != nil && cp.SchemaSource.ConfluentURL != "" {
				url = cp.SchemaSource.ConfluentURL
			}
			items = append(items, "Schemas: `kcp scan schema-registry --state-file "+state+" --sr-type confluent --url "+url+" --use-unauthenticated` (or `--use-basic-auth` / `--use-mtls` with its credentials)")
		}
	}

	// Connectors: migrate-connectors reads the connector configs from the state file.
	clusterID := cp.Arn
	if clusterID == "" {
		clusterID = cp.ClusterID
	}
	if src.MSKConnect {
		arn := cp.Arn
		if arn == "" {
			arn = "<your-msk-cluster-arn>"
		}
		items = append(items, "MSK Connect connectors: `kcp scan msk-connectors --state-file "+state+" --cluster-arn "+arn+"`")
	}
	if src.SelfManaged {
		items = append(items, "Self-managed Kafka Connect connectors: `kcp scan self-managed-connectors --state-file "+state+" --cluster-id "+clusterID+" --connect-rest-url <your-connect-rest-url> --use-unauthenticated` (or `--use-basic-auth` / `--use-tls` with its credentials)")
	}

	if len(items) == 0 {
		return
	}
	md.AddParagraph("**Before you start.** The steps below read these from your state file. Run any scan you haven't already, so the state file has them:")
	md.AddList(items)
}

// accessControlNote points at how to recreate the source's authorization rules,
// set up separately from data movement. The Authentication recommendation above
// (see renderInfraRecommendation) already carries the specifics — which command
// to run and what it does — via authDecision's access-control notes, so this
// only adds the timing and, for a source with no authorization to carry over,
// the guidance those notes don't cover.
func accessControlNote(md *markdown.Markdown, cp ClusterPlan) {
	// Start-fresh has no cutover/mirror, so frame the timing against pointing clients
	// at the new cluster rather than a data cutover (5a).
	when := "before cutover"
	tail := " This is independent of moving your data — plan it alongside the steps above, not after."
	if cp.Plan.Switchover.StartFresh {
		when = "before you point your clients at the new cluster"
		tail = " Plan it alongside the steps above, not after."
	}
	var body string
	switch {
	case sourceAuthHas(cp, SourceAuthIAM):
		cmd := "`kcp create-asset migrate-acls iam`"
		if sourceAuthHasACLBearing(cp) {
			cmd += " and `kcp create-asset migrate-acls kafka`"
		}
		body = "**Access control (set up separately).** Your source's authorization rules don't carry over. Recreate them with " + cmd + ". The Authentication recommendation above covers what it reads. Do this " + when + "." + tail
	case sourceAuthUnauthOnly(cp):
		// Unauthenticated sources have no authorization to carry over, but the new cluster
		// still shouldn't run open.
		body = "**Access control (set up separately).** Your source has no authorization to carry over — still, don't run the new cluster open. Grant each application and service account least-privilege access with Confluent Cloud role-based access control (RBAC) role bindings (or Confluent Cloud ACLs) " + when + "." + tail
	default:
		body = "**Access control (set up separately).** Your source's authorization rules don't carry over. Recreate them with `kcp create-asset migrate-acls kafka`. The Authentication recommendation above covers what it reads. Do this " + when + "." + tail
	}
	md.AddParagraph(body)
}

// sourceAuthHas reports whether the cluster's effective source auth (after answers)
// includes the given kcp auth token.
func sourceAuthHas(cp ClusterPlan, token string) bool {
	for _, a := range cp.effectiveSourceAuths {
		if a == token {
			return true
		}
	}
	return false
}

// sourceAuthHasACLBearing reports whether the effective source auth includes a
// method whose Kafka ACLs need recreating with `migrate-acls kafka` (SASL/SCRAM,
// mTLS, SASL/PLAIN or Kerberos), as opposed to AWS IAM or plaintext.
func sourceAuthHasACLBearing(cp ClusterPlan) bool {
	for _, a := range cp.effectiveSourceAuths {
		switch a {
		case SourceAuthSCRAM, SourceAuthMTLS, SourceAuthSASLPlain, SourceAuthKerberos:
			return true
		}
	}
	return false
}

// sourceAuthUnauthOnly reports whether the only effective source auth is
// unauthenticated (so there is genuinely no authorization to carry over).
func sourceAuthUnauthOnly(cp ClusterPlan) bool {
	if len(cp.effectiveSourceAuths) == 0 {
		return false
	}
	for _, a := range cp.effectiveSourceAuths {
		if a != SourceAuthUnauth {
			return false
		}
	}
	return true
}

// clusterLinkCutoverStep renders the shared final Cluster Linking cutover step and
// its backing-out note, used by both the auto-mapped and the specialist-wired
// cluster-link paths so they read identically. A Gateway-mediated cutover is driven
// by `kcp migration`, the CPC Gateway cutover tool; a plain Cluster Linking cutover
// has no such tool and is walked through by hand.
func clusterLinkCutoverStep(md *markdown.Markdown, cp ClusterPlan, step func(string) string) {
	sw := cp.Plan.Switchover
	if sw.GatewayMediated {
		md.AddParagraph(step("run the cutover.") + " With the link live and the mirror caught up, cut over through the Confluent Gateway with `kcp migration` ([docs](" + docMigration + ")). How consumer offsets come across depends on the route type in your `GatewayMigration` manifest. For a static route, enable consumer offset sync on the cluster link before you cut over: set `consumer.offset.sync.enable=true` and list the consumer groups to migrate in `consumer.offset.group.filters` (it defaults to none, and its value is JSON such as `" + engine.OffsetSyncFiltersExample + "`), for example by putting both in a properties file and running `confluent kafka link configuration update <your-link-name> --config <your-link-config-file>` ([docs](" + docLinkConfigUpdate + ")). For a topic-based (dynamic) route, keep consumer offset sync disabled on the link: `kcp migration` refuses the run if it is enabled. Then:")
		items := []string{
			gatewaySecretsItem(cp),
		}
		if gatewayServesMTLS(cp) {
			items = append(items, gatewayMTLSSourceACLs)
		}
		const routeClients = "point each client at the Gateway instead of the source. The Gateway keeps forwarding to your source until `execute` switches it."
		if sourceAuthHas(cp, SourceAuthSCRAM) && !gatewayServesMTLS(cp) {
			// SCRAM credentials are registered through the deployed Gateway, before clients route through it.
			items = append(items,
				"Deploy the Confluent Gateway with Confluent for Kubernetes.",
				scramRegistrationItem(cp),
				"Route your clients through the Gateway: "+routeClients,
			)
		} else {
			items = append(items, "Deploy the Confluent Gateway with Confluent for Kubernetes and route your clients through it: "+routeClients)
		}
		items = append(items,
			"Write the `GatewayMigration` manifest that describes the migration (source, cluster link, Gateway, and topics) ([docs]("+docMigration+")).",
			"`kcp migration lag-check --migration-yaml <your-gateway-migration.yaml>` — confirm the mirror has caught up to the source (lag is zero).",
			"`kcp migration execute --migration-yaml <your-gateway-migration.yaml>` — every run reads the manifest and the live state of the Gateway and cluster link, validates them, and resumes from wherever the live state says work remains (add `--dry-run` to validate only and change nothing), then fences the Gateway, promotes the mirror topics, and switches the Gateway to route to Confluent Cloud.",
		)
		md.AddOrderedList(items)
		clientCutoverChanges(md, cp)
		schemaCutoverNote(md, cp)
		selfManagedSinksAfterPromote(md, cp)
		md.AddParagraph("**Backing out:** before `execute`, you can back out by leaving clients on the source. Once `execute` promotes the mirror topics, rolling back needs a new cluster link in the other direction.")
		return
	}
	md.AddParagraph(step("run the cutover.") + " " + engine.ClCutoverStepsMarkdown(sw.CutoverStyle, cp.MigrationInfra.JumpCluster, "confluent kafka link configuration update <your-link-name> --config <your-link-config-file>", docLinkConfigUpdate, jumpFirstHopSyncCommand(cp.MigrationInfra.Type), docCPClusterLinkCommands))
	clientCutoverChanges(md, cp)
	schemaCutoverNote(md, cp)
	selfManagedSinksAfterPromote(md, cp)
	if sw.CutoverStyle != "" && !engine.IsAllAtOnceStyle(sw.CutoverStyle) {
		md.AddParagraph("**Backing out:** until you promote a service's mirror topics, nothing is committed for that service: your source keeps taking writes until you stop its producers and cut over. To roll back a service, restart its producers against the still-running source instead of promoting the mirror.")
		return
	}
	md.AddParagraph("**Backing out:** until you promote the mirror topics, nothing is committed: your source keeps taking writes until you stop your producers and cut over. To roll back, restart your producers against the still-running source instead of promoting the mirror.")
}

// selfManagedSinksAfterPromote closes the cutover with the self-managed sink connectors,
// which start only once their topics are promoted.
func selfManagedSinksAfterPromote(md *markdown.Markdown, cp ClusterPlan) {
	if linkKeepsSelfManagedConnectors(cp) {
		if cp.Plan.Switchover.GatewayMediated {
			md.AddParagraph("Once `kcp migration execute` promotes the mirror topics, create your self-managed sink connectors on your Connect cluster. On a static route, each resumes from the offsets synced for its consumer group (`connect-<connector-name>`).")
			return
		}
		md.AddParagraph("Once the mirror topics are promoted, create your self-managed sink connectors on your Connect cluster. Each resumes from the offsets synced for its consumer group (`connect-<connector-name>`).")
	}
}

// gatewayServesMTLS reports whether a Gateway plan has clients that use mTLS. The
// Gateway terminates TLS, so identity passthrough can't carry a certificate to
// Confluent Cloud: it swaps (`auth: swap`) to the API key it signs in with, and
// Confluent Cloud sees that key rather than each client's certificate.
func gatewayServesMTLS(cp ClusterPlan) bool {
	return cp.Plan.Switchover.GatewayMediated && sourceAuthHas(cp, SourceAuthMTLS)
}

const gatewayMTLSSourceACLs = "On the source, clients now arrive as the Gateway's certificate, so grant that certificate's principal ACLs that cover every client."

// scramMechanismForRegistration names the SCRAM mechanism for the registration command:
// the one the scan recorded for the source, or the choice to make when it recorded none.
func scramMechanismForRegistration(cp ClusterPlan) string {
	if cp.scanScramMechanism != "" {
		return cp.scanScramMechanism
	}
	return "<SCRAM-SHA-256|SCRAM-SHA-512, whichever your source uses>"
}

// gatewaySecretsItem is the "load the Gateway's secrets" step of a Gateway cutover.
// The source route is a passthrough, so the Gateway holds no source credentials of
// its own for non-mTLS clients; only mTLS clients reach the source as the Gateway's
// certificate. A SCRAM swap is the exception: clients' SCRAM credentials are registered
// through the Gateway, which keeps them in the secret store. SASL/PLAIN clients are
// checked by the Gateway itself, against the route's JAAS file.
func gatewaySecretsItem(cp ClusterPlan) string {
	const store = "Load the Gateway's secrets into your secret store (HashiCorp Vault, AWS Secrets Manager, or Azure Key Vault): "
	const scramStore = "Load the Gateway's secrets into your secret store: "
	if gatewayServesMTLS(cp) {
		return store + "its TLS material, the certificate it presents to your source cluster, and one API key per client certificate CN, which is how it swaps each client certificate (`auth: swap`) at cutover. Set ACLs on Confluent Cloud for each key's service account."
	}
	if sourceAuthHas(cp, SourceAuthSCRAM) {
		return scramStore + "the credentials it uses to sign in to Confluent Cloud on each client's behalf, which is how it swaps credentials (`auth: swap`) at cutover, and a SCRAM admin credential it uses to register client SCRAM users. Store the SCRAM credentials in HashiCorp Vault, AWS Secrets Manager, or CyberArk Conjur (Azure Key Vault doesn't support SCRAM), and give the Gateway write access to that store. The SCRAM admin user that does the registration needs its own swap entry in the secret store, mapping it to a Confluent Cloud API key, or provisioning fails. Give each client its own credential."
	}
	if sourceAuthHas(cp, SourceAuthUnauth) {
		return store + "the API key it signs in to Confluent Cloud with, which is how it maps your clients (`auth: swap`) at cutover. " + engine.GatewayAnonymousACLSentence
	}
	if sourceAuthHas(cp, SourceAuthSASLPlain) {
		return store + "the credentials it uses to sign in to Confluent Cloud on each client's behalf, which is how it swaps credentials (`auth: swap`) at cutover. The Gateway checks each client's SASL/PLAIN login itself, so add every client's username and password to the route's JAAS file before you route clients through it. Give each client its own credential."
	}
	return store + "the credentials it uses to sign in to Confluent Cloud on each client's behalf, which is how it swaps credentials (`auth: swap`) at cutover. Give each client its own credential."
}

// scramRegistrationItem registers each client's SCRAM credentials through the deployed
// Gateway, so it follows the deploy and precedes `kcp migration execute`.
func scramRegistrationItem(cp ClusterPlan) string {
	return "Register each client's SCRAM credentials through the Gateway's registration route (`scram-registration-route`, port 9599) with `kafka-configs --bootstrap-server <gateway-host>:9599 --command-config <admin.properties> --alter --add-config '" + scramMechanismForRegistration(cp) + "=[password=<password>]' --entity-type users --entity-name <client-user>`, and the Gateway stores them in the secret store for you. SCRAM passwords can't contain `[` or `]`, which conflict with the `kafka-configs` bracket syntax."
}

// jumpFirstHopSyncCommand is the Confluent Platform command, run against the jump
// cluster, that enables consumer offset sync on its link from the source. The link name
// is the one kcp's jump-cluster setup gives it: your link name plus a per-type suffix.
func jumpFirstHopSyncCommand(miType int) string {
	suffix := "-msk-cp"
	if miType == 4 {
		suffix = "-source-cp"
	}
	return "kafka-configs --bootstrap-server <jump-cluster-broker>:9092 --alter --cluster-link <your-link-name>" + suffix + " --add-config-file <your-link-config-file>"
}

// clientCutoverChanges enumerates the concrete config changes each client app needs
// at cutover — the new bootstrap endpoint, the plan's chosen client authentication,
// and (for schema users) the new Schema Registry URL — so "move your clients" spells
// out what actually changes rather than leaving it implied.
func clientCutoverChanges(md *markdown.Markdown, cp ClusterPlan) {
	auth := cp.Plan.Auth.Value
	if auth == "" {
		auth = "the plan's chosen client authentication"
	}
	// Preserved auth (mTLS carried over as-is) keeps the client's existing
	// credentials — it must not tell the reader to switch to "new" credentials.
	credsClause := "with the new Confluent Cloud credentials"
	if strings.Contains(auth, "preserved") {
		credsClause = "keeping the credentials it already uses"
	}
	bootstrap := "point it at the new Confluent Cloud **bootstrap endpoint**"
	if cp.Plan.Switchover.GatewayMediated {
		// Clients already point at the Gateway; the cutover moves where it routes.
		bootstrap = "point it at the **Confluent Gateway** (once, before `execute`; the Gateway then routes it to Confluent Cloud, so the bootstrap address does not change again)"
	}
	security := "switch its security config to **" + auth + "** " + credsClause
	if cp.Plan.Switchover.GatewayMediated {
		// The Gateway swaps credentials (`auth: swap`), so clients keep their source
		// credentials and need no security change.
		security = "leave its security config as it is: it keeps its source credentials, and the Gateway swaps them for Confluent Cloud credentials (`auth: swap`)"
		switch {
		case gatewayServesMTLS(cp):
			security = "leave its security config as it is: it keeps its client certificate to the Gateway, and the Gateway swaps each client's certificate for that client's own API key (`auth: swap`), so Confluent Cloud sees that key's service account, not the certificate"
		case sourceAuthHas(cp, SourceAuthUnauth):
			security = "leave its security config as it is: it keeps connecting without credentials, and the Gateway signs it in to Confluent Cloud with the one mapped API key (`auth: swap`)"
		}
	}
	change := "What changes for each client app at cutover: " + bootstrap + "; " + security
	// A schemaless plan has no Schema Registry to repoint, so omit that clause.
	if cp.Plan.Schema.Kind != engine.SchemaKindSchemaless {
		change += "; and, for any app that uses Schema Registry, point it at the new Confluent Cloud **Schema Registry URL**"
	}
	md.AddParagraph(change + ".")
}

// schemaCutoverNote closes out a schema migration at cutover. A Glue migration needs
// clients moved to Confluent serializers. Schema Linking (the
// schema exporter) and Replicator both leave the Confluent Cloud Schema Registry in
// IMPORT mode while they copy, so producers can't register new schemas until the copy
// is stopped and the registry is set back to READWRITE.
func schemaCutoverNote(md *markdown.Markdown, cp ClusterPlan) {
	var linking, replicator, glue bool
	for _, av := range appViews(cp) {
		if isContingent(nodeSchema, av.contingent) {
			continue
		}
		switch av.plan.Schema.Kind {
		case engine.SchemaKindGlueBulk:
			glue = true
		case engine.SchemaKindLinking:
			linking = true
		case engine.SchemaKindReplicator:
			replicator = true
		}
	}
	const tail = " and set the Confluent Cloud Schema Registry back to READWRITE, so producers can register new schemas."
	if linking {
		md.AddParagraph("At cutover, stop the schema exporter" + tail)
	}
	if replicator {
		md.AddParagraph("At cutover, stop Replicator" + tail)
	}
	if glue {
		note := "At cutover, switch your clients from the AWS Glue Schema Registry serializers to Confluent Schema Registry serializers."
		// A start-fresh plan mirrors no messages, so there is no Glue-format history to read.
		if !cp.Plan.Switchover.StartFresh {
			note += " Messages already mirrored keep their Glue format, so consumers that read that history still need the Glue deserializer."
		}
		md.AddParagraph(note)
	}
}

// decommissionStep is the final step of a self-serve plan: tear down what the
// migration stood up, then retire the source. A plan whose link is wired up with a
// specialist (the default case) gets no step: it has no settled mechanism to tear down.
func decommissionStep(md *markdown.Markdown, cp ClusterPlan, step func(string) string) {
	sw, mi := cp.Plan.Switchover, cp.MigrationInfra
	var body string
	switch {
	case sw.StartFresh:
		body = "retire your source cluster once your consumers have finished reading it."
	case sw.Replicator:
		body = "stop Replicator and its Connect worker, then retire your source cluster."
	case mi.Type >= 1 && mi.JumpCluster:
		body = "delete both cluster links, the jump cluster, and its PrivateLink endpoint, then retire your source cluster."
	case mi.Type >= 1 || mi.SpecialistClusterLinkable:
		parts := "the cluster link"
		switch net := cp.Plan.Networking; {
		case net.HasEgressEndpoint && net.EgressForConnectors:
			parts += " and the Egress PrivateLink Endpoint (keep it if your connectors still use it)"
		case net.HasEgressEndpoint:
			parts += " and the Egress PrivateLink Endpoint"
		}
		body = "delete " + parts + ", then retire your source cluster."
	default:
		return
	}
	md.AddParagraph(step("decommission.") + " " + strings.ToUpper(body[:1]) + body[1:])
}

// mechanismSettled reports whether the migration mechanism (start fresh /
// Replicator / cluster link) is determined. The mechanism is fixed by the infra
// tier (gated separately) and whether existing data moves; only the cutover style
// turns on downtime tolerance. So a Data-migration verdict held solely on
// downtime_tolerance still has a known mechanism, while any other pending driver
// (for example whether to move data) could change it, so we hold.
func mechanismSettled(cp ClusterPlan) bool {
	for _, q := range cp.Contingent[nodeDataMigration] {
		if q != "downtime_tolerance" {
			return false
		}
	}
	return true
}

// anyNonMSKSource reports whether any cluster is a non-MSK source (Apache Kafka /
// Confluent Platform), for source-aware fleet-level copy. `kcp discover` is MSK-only,
// so a plan with a non-MSK source leads with `kcp scan clusters`.
func anyNonMSKSource(ep *EnginePlan) bool {
	for i := range ep.Clusters {
		if ep.Clusters[i].SourcePlatform != "" {
			return true
		}
	}
	return false
}

// privateNetworkNoun is the cloud-correct word for the customer's private network,
// for prose about subnet CIDRs. AWS (and the empty/MSK default) = "VPC" so MSK copy
// is unchanged; Azure = "VNet"; GCP = "VPC network".
func privateNetworkNoun(targetCloud string) string {
	switch targetCloud {
	case "Azure":
		return "VNet"
	case "GCP":
		return "VPC network"
	default:
		return "VPC"
	}
}

// targetInfraUnsupported explains why `kcp create-asset target-infra` can't provision
// this plan's target cluster, or "" when it can. target-infra builds AWS networking
// only (a VPC-attached PrivateLink, which it requires for Enterprise), so it can't
// stand up an Azure or GCP cluster, nor an AWS cluster whose recommended networking
// is PNI. Emitting its command there would build different networking from the one
// the plan recommends.
func targetInfraUnsupported(cp ClusterPlan) string {
	switch cp.targetCloud {
	case "Azure", "GCP":
		return "builds AWS networking only, so it can't provision a cluster on " + cp.targetCloud
	}
	if strings.HasPrefix(cp.Plan.Networking.Value, "PNI") {
		return "builds PrivateLink networking, not PNI, so it can't provision the networking this plan recommends"
	}
	return ""
}

// targetClusterStep provisions the target cluster: a target-infra command for
// Enterprise/Dedicated clusters it can build, or Console guidance for Standard/Basic
// (which target-infra does not create) and for networking target-infra doesn't build.
func targetClusterStep(md *markdown.Markdown, cp ClusterPlan, stateFilePath string, step func(string) string) {
	ct := string(cp.Plan.ClusterType.Value)
	net := cp.Plan.Networking.Value
	auth := cp.Plan.Auth.Value
	if targetClusterTypeFlag(cp) == "" {
		md.AddParagraph(step("create the target cluster.") + " Create " + article(ct) + " **" + ct + "** cluster with **" + net + "** networking in the [Confluent Cloud Console](" + docCreateCluster + "). (`kcp create-asset target-infra` provisions Enterprise and Dedicated clusters only.) Set up **" + auth + "** client authentication on it, and note its **cluster ID**, **bootstrap endpoint**, and **REST endpoint** for the later steps.")
		return
	}
	if why := targetInfraUnsupported(cp); why != "" {
		note := "Set up **" + net + "** networking on it and **" + auth + "** client authentication, and note its **environment ID**, **cluster ID**, **bootstrap endpoint**, and **REST endpoint** for the later steps."
		note += linkEgressNote(cp)
		md.AddParagraph(step("create the target cluster.") + " Create " + article(ct) + " **" + ct + "** cluster with **" + net + "** networking in the [Confluent Cloud Console](" + docCreateCluster + "), or [Talk to a person](#talk-to-a-person) to set it up with a specialist. `kcp create-asset target-infra` isn't used here: it " + why + ". " + note)
		if cp.targetCloud != "Azure" && cp.targetCloud != "GCP" {
			// PNI plan on AWS: target-infra builds PrivateLink, so offer it only as the
			// explicit alternative to the PNI this plan recommends.
			md.AddParagraph("If you would rather use PrivateLink than PNI, `kcp create-asset target-infra` ([docs](" + docTargetInfra + ")) builds the environment, the cluster, and PrivateLink networking:")
			md.AddCodeBlock(targetInfraCommand(cp, stateFilePath), "bash")
		}
		return
	}
	md.AddParagraph(step("create the target cluster.") + " Provision " + article(ct) + " **" + ct + "** cluster in Confluent Cloud. `kcp create-asset target-infra` ([docs](" + docTargetInfra + ")) generates the environment, the cluster, and its private networking, or set it up in the [Confluent Cloud Console](" + docCreateCluster + "):")
	md.AddCodeBlock(targetInfraCommand(cp, stateFilePath), "bash")
	note := "Note the new cluster's **environment ID**, **cluster ID**, **bootstrap endpoint**, and **REST endpoint** for the later steps. `--needs-private-link` (with your " + privateNetworkNoun(cp.targetCloud) + " subnet CIDRs) provisions the private networking."
	if cp.SourcePlatform != "" {
		note += " `--aws-region` and `--vpc-id` name the AWS region and VPC for that networking: your source isn't an MSK cluster, so there is no scanned MSK cluster to read them from."
	}
	note += linkEgressNote(cp)
	note += " Set up **" + auth + "** client authentication on the cluster separately."
	md.AddParagraph(note)
}

// linkEgressNote forward-references the migration link's Egress PrivateLink Endpoint,
// only when a Cluster Linking link step actually follows and the plan carries that
// egress endpoint; on Replicator/start-fresh paths there is no link step to
// reference. Returns "" otherwise, or a sentence with a leading space.
func linkEgressNote(cp ClusterPlan) string {
	if cp.MigrationInfra.Kind != "cluster_link" || !cp.Plan.Networking.HasEgressEndpoint {
		return ""
	}
	if cp.Plan.Networking.EgressForConnectors {
		// The endpoint was added for the runtime cc_egress need (see the Networking
		// "Why"), and the same one carries the migration link — reconcile the two
		// rationales so the single endpoint isn't justified two different ways.
		return " The Egress PrivateLink Endpoint your Confluent-managed connectors use (from the Networking plan above) also carries the migration link, wired up in the link step below."
	}
	return " The migration link's Egress PrivateLink Endpoint is added in the link step below."
}

// linkStep prints the migration-infra command that builds the Cluster Link.
// linkRefs are the step numbers the link step points back at: the source-credentials
// step and (for a jump cluster) the PrivateLink endpoint step. Zero means no such step.
type linkRefs struct {
	credentials int
	privateLink int
}

func linkStep(md *markdown.Markdown, cp ClusterPlan, stateFilePath string, step func(string) string, refs linkRefs) {
	mi := cp.MigrationInfra
	title := "build the migration link."
	if mi.JumpCluster {
		title = "set up the jump cluster and its cluster links."
	}
	md.AddParagraph(step(title) + fmt.Sprintf(" Your data migration runs over the recommended link (**%s**, [`--type %d`](%s)). %s Fill in the placeholders (the cluster you created plus a link name), then `terraform apply`:", mi.Label, mi.Type, docMigrationInfra, firstSentence(mi.Rationale)))
	md.AddCodeBlock(migrationInfraCommand(cp, stateFilePath), "bash")
	// The example link name stays MSK-flavored for MSK sources (SourcePlatform is
	// empty there) and generic for Apache Kafka / Confluent Platform sources.
	linkNameExample := "msk-to-cc"
	if cp.SourcePlatform != "" {
		linkNameExample = "kafka-to-cc"
	}
	placeholders := []string{
		"`<your-link-name>`: a name you choose for the cluster link, for example `" + linkNameExample + "`.",
		"`<cc-env-id>`, `<cc-cluster-id>`, `<cc-rest-endpoint>`: from the cluster you created.",
	}
	// Apache Kafka (OSK/CP) migration-infra provisions the outbound link from an AWS
	// VPC, so the command carries the source-side AWS networking flags that MSK derives
	// from state. Known non-AWS sources are routed to a specialist upstream (no command
	// here), so this documents the AWS-resident case with a safety note for unknowns.
	if cp.SourcePlatform != "" {
		placeholders = append(placeholders, "`<your-source-vpc-id>`, `<your-source-aws-region>`: the AWS VPC and region where your source's brokers run — kcp's Apache Kafka migration-infra provisions the outbound link from an AWS VPC. (If your source isn't in AWS, set the link up with a specialist instead — [Talk to a person](#talk-to-a-person).)")
		if mi.Type == 2 {
			placeholders = append(placeholders, "`<your-source-subnet-id>`, `<your-source-security-group-id>`: a subnet and security group in that VPC for the host that provisions the link.")
		}
		if (mi.Type == 1 || mi.Type == 2 || mi.Type == 4) && !scramMechanismKnown(cp) {
			if sourceHasSCRAM(cp) {
				mechanism := "the mechanism your source's SASL/SCRAM listener uses"
				// Only a scanned run can say its scan recorded none; a scanless plan has no scan.
				if stateFilePath != "" {
					mechanism += " (your scan didn't record one)"
				}
				placeholders = append(placeholders, "`<SCRAM-SHA-256|SCRAM-SHA-512>`: "+mechanism+".")
			} else {
				listener := "the mechanism of the SASL/SCRAM listener you add to your source for the link"
				if refs.credentials > 0 {
					listener += fmt.Sprintf(" (see Step %d above)", refs.credentials)
				}
				placeholders = append(placeholders, "`<SCRAM-SHA-256|SCRAM-SHA-512>`: "+listener+".")
			}
		}
	}
	if mi.JumpCluster {
		inputs := "the PrivateLink (access point) bootstrap endpoint (not the PNI one), an existing PrivateLink endpoint, and VPC subnets for the jump cluster"
		if refs.privateLink > 0 {
			inputs = fmt.Sprintf("the PrivateLink (access point) bootstrap endpoint (not the PNI one) and the VPC endpoint from Step %d, and VPC subnets for the jump cluster", refs.privateLink)
		}
		placeholders = append(placeholders, "the jump-cluster inputs (`<cc-bootstrap-endpoint>`, `<your-vpce-id>`, and the subnet CIDRs): "+inputs+" ([docs]("+docMigrationInfra+")).")
		if cp.SourcePlatform != "" {
			placeholders = append(placeholders, "`<instance-type>`, `<gb>`: the EC2 instance type and broker storage for the jump cluster (no source broker config to default them from off MSK).")
		}
	}
	md.AddList(placeholders)
	if mi.Alternative != "" {
		alt := "Alternative: " + mi.Alternative + "."
		if mi.AlternativeType > 0 {
			if engine.IsJumpClusterType(mi.AlternativeType) {
				alt += fmt.Sprintf(" That is `--type %d`, which needs extra jump-cluster inputs (a bootstrap endpoint, an existing PrivateLink endpoint, and subnet CIDRs). See the [docs](%s).", mi.AlternativeType, docMigrationInfra)
			} else {
				// A direct-link alternative (e.g. a SASL/SCRAM listener + type 2) needs no
				// jump-cluster inputs; it connects to the source directly.
				alt += fmt.Sprintf(" That is `--type %d`; the link connects to your source directly, with no jump cluster to run. See the [docs](%s).", mi.AlternativeType, docMigrationInfra)
			}
		}
		md.AddParagraph(alt)
	}
}

// credentialsStep lists what the link needs to sign in to the source, per method. It
// runs before the link step, so the notes' reference to the migration-link step is
// swapped for that step's number (linkStepNum).
func credentialsStep(md *markdown.Markdown, cp ClusterPlan, step func(string) string, linkStepNum int) {
	handling := cp.Plan.Switchover.SourceCredentialHandling
	if len(handling) == 0 {
		return
	}
	md.AddParagraph(step("prepare your source credentials.") + " What the link needs to sign in to your source, by method:")
	notes := make([]string, len(handling))
	for i, h := range handling {
		note := strings.ReplaceAll(h.Note, engine.MigrationLinkStepRef, fmt.Sprintf("Step %d, the migration link", linkStepNum))
		notes[i] = "**" + h.Method + "**: " + note
	}
	md.AddList(notes)
}

// jumpPrivateLinkStep sets up the PrivateLink the jump cluster uses to reach Confluent
// Cloud. The link step takes its VPC endpoint ID (`--existing-private-link-vpce-id`) and
// the PrivateLink bootstrap endpoint (`--target-bootstrap-endpoint`); the target-cluster
// step builds PNI only, so nothing else creates them. kcp create-asset target-infra is
// not used: it can't wire PrivateLink onto an existing cluster and doesn't output the
// bootstrap endpoint. linkStepNum is the number of the link step that follows.
func jumpPrivateLinkStep(md *markdown.Markdown, linkStepNum int, step func(string) string) {
	md.AddParagraph(step("set up a PrivateLink endpoint for the jump cluster.") + " Create an ingress PrivateLink Gateway and a PrivateLink Access Point for your Confluent Cloud environment, with a VPC endpoint in the jump cluster's VPC, then use that endpoint's bootstrap address for the jump cluster's link ([docs](" + docPrivateLinkAttachment + ")). " + fmt.Sprintf("Note the **VPC endpoint ID** (`vpce-…`) for `<your-vpce-id>` and the PrivateLink **bootstrap endpoint** for `<cc-bootstrap-endpoint>` in Step %d.", linkStepNum))
}

// topicsStep recreates the source topics on the target. Shown only once the
// Topics verdict is settled; an undecided verdict can't produce a concrete step.
// mode is the migrate-topics mode for this mechanism: "mirror" (cluster link
// forwards data) or "new" (empty topics for Replicator/start-fresh).
func topicsStep(md *markdown.Markdown, cp ClusterPlan, stateFilePath, mode string, step func(string) string) {
	if isContingent(nodeTopics, cp.Contingent) {
		return
	}
	if cp.IsServerless && mode == migrateTopicsModeMirror {
		// MSK Serverless has no Kafka Admin API scan, so migrate-topics finds no topics to mirror.
		md.AddParagraph(step("create your mirror topics on the target.") + " `kcp create-asset migrate-topics` can't list topics on MSK Serverless, so it would create none. Create the mirror topics by hand with `confluent kafka mirror create <topic> --link <your-link-name>` for each topic, or enable auto-create mirror topics on the Confluent Cloud link ([docs](" + docMirrorTopics + ")).")
		return
	}
	if cp.IsServerless && mode == migrateTopicsModeNew {
		md.AddParagraph(step("create your topics on the target.") + " `kcp create-asset migrate-topics` can't list topics on MSK Serverless, so it would create none. Create each topic on Confluent Cloud yourself with `confluent kafka topic create <topic> --partitions <n>`, using your source topics' partition counts and configs.")
		return
	}
	how := "as plain topics on the new cluster (no data yet)"
	if mode == migrateTopicsModeMirror {
		how = "as mirror topics that the cluster link forwards your data into"
	}
	md.AddParagraph(step("create your topics on the target.") + " Recreate your source topics " + how + ", with their partition counts and configs, using `kcp create-asset migrate-topics` ([docs](" + docMigrateTopics + ")):")
	md.AddCodeBlock(migrateTopicsCommand(cp, mode, stateFilePath), "bash")
}

// appView is one application's plan for the per-application steps: its declared
// name (empty when the cluster has a single implicit app) and its verdicts and
// contingent map.
type appView struct {
	name         string
	plan         engine.PlanResult
	contingent   map[string][]string
	connectorSrc ConnectorSource
}

// appViews returns one view per application the steps plan for: each declared app
// (skipping any routed to a specialist, which gets no automated step), or the
// cluster's own implicit single app when none are declared.
func appViews(cp ClusterPlan) []appView {
	if len(cp.Apps) == 0 {
		return []appView{{plan: cp.Plan, contingent: cp.Contingent, connectorSrc: derefConnectorSource(cp.ConnectorSource)}}
	}
	var vs []appView
	for _, ap := range cp.Apps {
		if ap.Plan.Withheld {
			continue
		}
		vs = append(vs, appView{name: ap.Name, plan: ap.Plan, contingent: ap.Contingent, connectorSrc: derefConnectorSource(ap.ConnectorSource)})
	}
	return vs
}

// derefConnectorSource returns the connector runtimes for a plan, or the empty set
// (no runtimes) when nil.
func derefConnectorSource(cs *ConnectorSource) ConnectorSource {
	if cs == nil {
		return ConnectorSource{}
	}
	return *cs
}

// perAppDataOps renders the schema and connector steps. Both verdicts are
// per-application, so a multi-application cluster can have applications with
// different strategies (one migrates schemas, another starts fresh). Each step is
// scoped to the applications it applies to, and is emitted only for those whose
// verdict is settled as a migration.
func perAppDataOps(md *markdown.Markdown, cp ClusterPlan, stateFilePath string, step func(string) string) {
	schemaStep(md, cp, stateFilePath, step)
	schemaTechAssistStep(md, cp, step)
	connectorsStep(md, cp, stateFilePath, step)
}

// schemaTechAssistStep renders a schema STEP for the tech-assist schema paths that
// schemaStep skips (they have no self-serve migrate-schemas command): Replicator (a
// community/below-7.1 registry, or an Enterprise registry that can't reach Confluent
// Cloud) and "Special handling" (a third-party or unidentified registry). Without it,
// a reader following the numbered steps would move data, topics, and connectors but
// never their schemas. The move is specialist-assisted, so the step names the
// approach and hands off, mirroring the keep-self-managed connectors step.
func schemaTechAssistStep(md *markdown.Markdown, cp ClusterPlan, step func(string) string) {
	views := appViews(cp)
	var replicator, special []string
	for _, av := range views {
		// Held apps are surfaced by pendingDataOpsNote; here we only render settled ones.
		if isContingent(nodeSchema, av.contingent) {
			continue
		}
		switch av.plan.Schema.Kind {
		case engine.SchemaKindReplicator:
			replicator = append(replicator, av.name)
		case engine.SchemaKindSpecial:
			special = append(special, av.name)
		}
	}
	if len(replicator) > 0 {
		md.AddParagraph(step("migrate your schemas"+appScope(replicator, len(views))+".") + " Your source registry moves with Confluent Replicator, which replicates your `_schemas` topic into Confluent Cloud Schema Registry in IMPORT mode, preserving your schema IDs (it needs a Confluent Platform Enterprise license) — see the Schema recommendation above. [Talk to a person](#talk-to-a-person) for a hand.")
	}
	if len(special) > 0 {
		md.AddParagraph(step("migrate your schemas"+appScope(special, len(views))+".") + " Your source uses a third-party or unidentified Schema Registry, so getting your schemas into Confluent Cloud Schema Registry is something you set up on your side — see the Schema recommendation above. [Talk to a person](#talk-to-a-person) for a hand.")
	}
}

// pendingDataOpsNote names the per-app steps (schema, connectors, topics) held back
// because their verdict is still pending, and the questions that unblock them, so a
// reader knows the plan will add them rather than the step simply being absent.
func pendingDataOpsNote(md *markdown.Markdown, cp ClusterPlan) {
	total := len(appViews(cp))
	var items []string
	add := func(verb, verdict string) {
		apps, qs := heldDataOp(cp, verdict)
		if len(qs) == 0 {
			return
		}
		items = append(items, verb+appScope(apps, total)+", once you answer "+keyList(qs))
	}
	add("migrate your schemas", nodeSchema)
	add("rebuild your connectors", nodeConnectors)
	if qs := cp.Contingent[nodeTopics]; len(qs) > 0 {
		items = append(items, "create your topics, once you answer "+keyList(qs))
	}
	if len(items) == 0 {
		return
	}
	// Fold the intro and the list into one NOTE alert (each line prefixed inside the
	// blockquote), so the whole callout reads as a single informational block.
	body := "**Still to come.** Once you answer this cluster's [pending questions](#" + qaAnchor(cp) + "), the plan adds:\n"
	for _, it := range items {
		body += "- " + it + "\n"
	}
	md.AddAlert(markdown.AlertNote, strings.TrimRight(body, "\n"))
}

// heldDataOp collects, for one per-app verdict, the applications whose verdict is
// held and the union of questions that would settle it.
func heldDataOp(cp ClusterPlan, verdict string) (apps, questions []string) {
	seen := map[string]bool{}
	for _, av := range appViews(cp) {
		qs := av.contingent[verdict]
		if len(qs) == 0 {
			continue
		}
		apps = append(apps, av.name)
		for _, q := range qs {
			if !seen[q] {
				seen[q] = true
				questions = append(questions, q)
			}
		}
	}
	return apps, questions
}

// keyList renders question keys as a backtick-wrapped, comma-separated list.
func keyList(keys []string) string {
	out := make([]string, len(keys))
	for i, k := range keys {
		out[i] = "`" + k + "`"
	}
	return strings.Join(out, ", ")
}

// schemaStep migrates schemas, per application, and prints the ready-to-fill
// migrate-schemas command. It names the applications only when it's a strict
// subset (some apps don't migrate schemas), so a step shared by every app stays
// unqualified; a pending app is noted, not asserted.
func schemaStep(md *markdown.Markdown, cp ClusterPlan, stateFilePath string, step func(string) string) {
	views := appViews(cp)
	var migrate []string
	methods := map[engine.SchemaKind]bool{} // distinct schema methods among migrators, for the command
	for _, av := range views {
		// Held apps are surfaced by pendingDataOpsNote; here we only render settled ones.
		if !isContingent(nodeSchema, av.contingent) && schemaNeedsMigration(av.plan) {
			migrate = append(migrate, av.name)
			methods[av.plan.Schema.Kind] = true
		}
	}
	if len(migrate) == 0 {
		return // nothing settled needs a migrate-schemas command; a pending-only step would assert too much
	}
	// The migrate-schemas invocation depends on the method (Glue re-registration vs
	// Schema Linking). The source registry is environment-level, so every migrating
	// app shares one method; only then is a single command unambiguous.
	var cmd string
	if len(methods) == 1 {
		var kind engine.SchemaKind
		for k := range methods {
			kind = k
		}
		cmd = migrateSchemasCommand(cp, kind, stateFilePath)
	}
	lead := " Copy the source schemas into Confluent Cloud Schema Registry; the Schema recommendation above covers the approach."
	if cmd != "" {
		lead += " Run `kcp create-asset migrate-schemas` ([docs](" + docMigrateSchemas + ")):"
	} else {
		lead += " Use `kcp create-asset migrate-schemas` ([docs](" + docMigrateSchemas + "))."
	}
	md.AddParagraph(step("migrate your schemas"+appScope(migrate, len(views))+".") + lead)
	if cmd != "" {
		md.AddCodeBlock(cmd, "bash")
	}
}

// connectorsStep rebuilds connectors as Confluent Cloud fully-managed connectors,
// per application, and prints the migrate-connectors command(s). The subcommand
// follows the source runtime (MSK Connect -> `msk`, self-managed -> `self-managed`),
// taken as the union across rebuilding apps since the command migrates by cluster.
func connectorsStep(md *markdown.Markdown, cp ClusterPlan, stateFilePath string, step func(string) string) {
	views := appViews(cp)
	var rebuild, keepSelf []string
	var src ConnectorSource
	for _, av := range views {
		// Held apps are surfaced by pendingDataOpsNote; here we only render settled ones.
		if isContingent(nodeConnectors, av.contingent) {
			continue
		}
		switch {
		case connectorsNeedRebuild(av.plan):
			rebuild = append(rebuild, av.name)
			src.MSKConnect = src.MSKConnect || av.connectorSrc.MSKConnect
			src.SelfManaged = src.SelfManaged || av.connectorSrc.SelfManaged
		case connectorsKeepSelfManaged(av.plan):
			keepSelf = append(keepSelf, av.name)
		}
	}
	if len(rebuild) > 0 {
		cmds := migrateConnectorsCommands(cp, src, stateFilePath)
		lead := " Recreate your connectors as Confluent Cloud fully-managed connectors; the Connectors recommendation above covers the approach."
		if len(cmds) > 0 {
			lead += " Run `kcp create-asset migrate-connectors` ([docs](" + docMigrateConnectors + ")):"
		} else {
			lead += " Use `kcp create-asset migrate-connectors` ([docs](" + docMigrateConnectors + "))."
		}
		md.AddParagraph(step("rebuild your connectors"+appScope(rebuild, len(views))+".") + lead)
		for _, cmd := range cmds {
			md.AddCodeBlock(cmd, "bash")
		}
		// Sequencing: generate the definitions before cutover. Where a cluster link moves the
		// data, apply them only once the mirror topics are promoted. The generated Terraform
		// has no paused state, so applying a connector starts it; applying early double-delivers
		// from a sink or writes into a read-only mirror topic.
		sw := cp.Plan.Switchover
		byLink := !sw.StartFresh && !sw.Replicator
		applyCue := "once you cut your clients over"
		if byLink {
			if sw.GatewayMediated {
				applyCue = "only after `kcp migration execute` promotes the mirror topics"
			} else {
				applyCue = "only after you promote the mirror topics"
			}
		}
		cutoverStop := "At cutover, stop the source-side connectors first, so you don't double-deliver from a sink or write into a read-only mirror topic."
		switch {
		case sw.StartFresh:
			// Old sinks must keep draining the old cluster, so they stop last.
			cutoverStop = "At cutover, stop your source connectors first, then stop each sink connector only after its consumer lag on the old cluster reaches zero, so it finishes delivering what is already there."
		case sw.Replicator:
			cutoverStop = "At cutover, stop the source-side connectors first, so you don't double-deliver from a sink."
		}
		md.AddParagraph("Generate these Confluent-managed connector definitions now, but apply them " + applyCue + ", so they don't start early: the generated Terraform starts a connector as soon as you apply it. " + cutoverStop)
		// Only self-managed Kafka Connect gets the Connect Migration Utility: its
		// stop_create_latest_offset mode needs a Connect REST endpoint (worker URLs), which
		// MSK Connect doesn't expose. MSK Connect connectors use the generated definitions.
		if src.MSKConnect {
			sinks := ""
			if sw.Replicator {
				sinks = replicatorSinkOffsets + " Add an offsets block to each sink connector's generated definition before you apply it. "
			}
			if byLink {
				sinks = "Confluent-managed sink connectors read with their own consumer group, so create each one with the offsets of its MSK Connect consumer group (`connect-<connector-name>`); see [Create connectors with offsets](" + docConnectorOffsets + "). Add an offsets block to each sink connector's generated definition before you apply it. "
			}
			if sinks != "" {
				md.AddParagraph(sinks + sourceConnectorOffsetsAfterSinks)
			} else {
				md.AddParagraph(sourceConnectorOffsets)
			}
		}
		if src.SelfManaged {
			creation := "the utility or generated definitions, not both."
			if src.MSKConnect {
				creation = "the utility for self-managed Connect, the generated definitions for MSK Connect, not both for the same connector."
			}
			var utility string
			if byLink {
				utility = "For connectors on self-managed Kafka Connect, use the Connect Migration Utility with `stop_create_latest_offset` so a connector resumes from its last offsets instead of re-reading or re-snapshotting ([docs](" + docConnectMigrationUtility + ")). " + utilityVersion + " "
				utility += "Run the utility only after you promote the mirror topics, since it creates the Confluent-managed connectors right away. "
			} else {
				// Replicator changes offsets and start-fresh topics are empty, so sinks cannot reuse their old offsets.
				utility = "Use the Connect Migration Utility with `stop_create_latest_offset` for source connectors only ([docs](" + docConnectMigrationUtility + ")). " + utilityVersion + " Create sink connectors without carried-over offsets, since the topics on Confluent Cloud have new offsets. "
				if sw.Replicator {
					utility = "Use the Connect Migration Utility with `stop_create_latest_offset` for source connectors only ([docs](" + docConnectMigrationUtility + ")). " + utilityVersion + " "
					if !src.MSKConnect {
						// The utility carries source offsets, and the sinks are created from generated definitions.
						utility += replicatorSinkOffsets + " Add an offsets block to each sink connector's generated definition before you apply it. "
					}
				}
			}
			md.AddParagraph(utility + "Use one creation path per connector: " + creation)
		}
	}
	// Keeping connectors self-managed still means recreating each one on a Connect
	// cluster you run against Confluent Cloud; migrate-connectors doesn't do that, so
	// point to the Connect Migration Utility rather than leaving the work step-less.
	if len(keepSelf) > 0 {
		offsets := ""
		recreate := "recreate each connector there."
		if cp.Plan.Switchover.Replicator {
			offsets = " " + sourceConnectorOffsets
			recreate = "recreate each source connector there. Create each sink connector only after you reset its offsets at cutover (below)."
		} else if sw := cp.Plan.Switchover; !sw.StartFresh {
			recreate = "recreate each source connector there. " + selfManagedLinkSinks
			if sw.GatewayMediated {
				recreate = "recreate each source connector there. " + selfManagedGatewaySinks
			}
		}
		md.AddParagraph(step("port your connectors to a self-managed Connect cluster"+appScope(keepSelf, len(views))+".") + " Stand up your own Kafka Connect cluster against Confluent Cloud and " + recreate + " The connectors stay self-managed, so `migrate-connectors` and the Connect Migration Utility, which create Confluent-managed connectors, don't apply." + offsets)
	}
}

// selfManagedLinkSinks is the sink-connector instruction for self-managed Connect on a
// Cluster Linking plan: the sinks start only once their topics are writable.
const selfManagedLinkSinks = "Stop each old sink connector with its service, before you promote its topics, so its last offsets sync. Create sink connectors only after you promote their topics, and list each sink's consumer group (connect-<connector-name>) in consumer.offset.group.filters so it resumes from its synced offsets."

// selfManagedGatewaySinks is the same instruction for a Gateway plan, whose route type
// (static or dynamic, set in the GatewayMigration manifest) decides whether offset sync is on:
// a dynamic route keeps it disabled on the link, so its sink groups stay out of the filters.
const selfManagedGatewaySinks = "Create sink connectors only after you promote their topics. On a static route, stop each old sink connector with its service, before you promote its topics, so its last offsets sync, and list each sink's consumer group (connect-<connector-name>) in consumer.offset.group.filters so it resumes from its synced offsets. On a dynamic route, consumer offset sync stays disabled on the link, so don't list the sink groups in consumer.offset.group.filters."

// linkKeepsSelfManagedConnectors reports whether any settled app keeps its connectors
// self-managed on a Cluster Linking plan, so the cutover step is followed by the sinks.
func linkKeepsSelfManagedConnectors(cp ClusterPlan) bool {
	sw := cp.Plan.Switchover
	if sw.StartFresh || sw.Replicator {
		return false
	}
	for _, av := range appViews(cp) {
		if !isContingent(nodeConnectors, av.contingent) && connectorsKeepSelfManaged(av.plan) {
			return true
		}
	}
	return false
}

// replicatorKeepsSelfManagedSinks reports whether any settled app keeps its connectors
// self-managed on a Replicator plan, so the cutover step carries the sink offset reset.
func replicatorKeepsSelfManagedSinks(cp ClusterPlan) bool {
	if !cp.Plan.Switchover.Replicator {
		return false
	}
	for _, av := range appViews(cp) {
		if !isContingent(nodeConnectors, av.contingent) && connectorsKeepSelfManaged(av.plan) {
			return true
		}
	}
	return false
}

// schemaNeedsMigration reports whether a settled Schema verdict maps to a
// self-serve migrate-schemas command. Only Glue re-registration and Schema Linking
// do; schemaless, a fresh registry, pending, and the tech-assist paths (Replicator,
// "Special handling") have no such command and so produce no step.
func schemaNeedsMigration(plan engine.PlanResult) bool {
	switch plan.Schema.Kind {
	case engine.SchemaKindGlueBulk, engine.SchemaKindLinking:
		return true
	}
	return false
}

// connectorsNeedRebuild reports whether a settled Connectors verdict maps to a
// migrate-connectors command. Only "Rebuild as Confluent-managed connectors" does;
// none-to-move, not-assessed, the destination-undecided (pending) value, and
// keeping connectors self-managed (running your own Connect cluster, which
// migrate-connectors does not do) produce no step.
func connectorsNeedRebuild(plan engine.PlanResult) bool {
	return plan.Connectors.Kind == engine.ConnectorsKindManaged
}

// connectorsKeepSelfManaged reports whether a settled Connectors verdict keeps the
// connectors self-managed (running your own Connect cluster). That work has no
// migrate-connectors command, so it gets its own step pointing at the Connect
// Migration Utility instead.
func connectorsKeepSelfManaged(plan engine.PlanResult) bool {
	return plan.Connectors.Kind == engine.ConnectorsKindSelfManaged
}

// appScope renders " for `a`, `b`" when the step applies to a named subset of
// declared apps (fewer than all views), and "" when it's every app or the single
// implicit app (whose name is empty and needs no qualifier).
func appScope(names []string, total int) string {
	if len(names) >= total {
		return ""
	}
	if named := namedList(names); named != "" {
		return " for " + named
	}
	return ""
}

// namedList backtick-joins declared app names, dropping the empty name of a single
// implicit app.
func namedList(names []string) string {
	var out []string
	for _, n := range names {
		if n != "" {
			out = append(out, "`"+n+"`")
		}
	}
	return strings.Join(out, ", ")
}

// renderInfraRecommendation renders the infrastructure verdicts (cluster type,
// sizing, networking, auth) — the shared half of the plan that every application
// on the cluster builds on.
func renderInfraRecommendation(md *markdown.Markdown, p engine.PlanResult, c map[string][]string, qaFragment string, caveats map[string][]string, hold bool) {
	md.AddHeading("Infrastructure plan", 3)
	renderSectionStatus(md, planGroupState(p.Withheld, c, infraVerdicts, hold), qaFragment)
	netAltV, netAltR := "", ""
	if a := p.Networking.Alternative; a != nil {
		netAltV, netAltR = a.Value, a.Reason
	}
	rows := [][]string{
		vrow(nodeClusterType, string(p.ClusterType.Value), withLink(p.ClusterType.Why, p.ClusterType.Source), c),
		vrow(nodeSizing, p.Sizing.Value, withLink(shortWhy(p.Sizing.Reason), p.Sizing.Source), c),
		vrow(nodeNetworking, valueWithAlt(p.Networking.Value, netAltV), withLink(p.Networking.Why, p.Networking.Source), c),
		vrow(nodeAuth, p.Auth.Value, withLink(p.Auth.Why, p.Auth.Source), c),
	}
	md.AddTable([]string{"Category", "Recommendation", "Why"}, rows)
	renderSizingSignals(md, p.Sizing.Signals)

	netReason := withAlternative(p.Networking.Reason, netAltV, netAltR)
	md.AddHeading("Why these recommendations", 4)
	var why []string
	for _, kv := range [][2]string{
		{nodeClusterType, p.ClusterType.Reason + " Zones: " + p.ClusterType.ZoneReason},
		{nodeSizing, p.Sizing.Reason},
		{nodeNetworking, netReason},
		{nodeAuth, p.Auth.Reason},
	} {
		why = append(why, nestCaveats(whyLine(kv[0], kv[1], c), caveats[kv[0]]))
	}
	md.AddList(why)
}

// renderSizingSignals shows, read-only, the measured capacity signals the sizing
// read (partition count, peak throughput) as its audit trail, and names anything
// the scan didn't measure with the command to fill it in.
func renderSizingSignals(md *markdown.Markdown, sigs []engine.SizingSignal) {
	var scanned, answered, missing []string
	for _, s := range sigs {
		switch {
		case s.Measured && s.Answered:
			answered = append(answered, s.Value)
		case s.Measured:
			scanned = append(scanned, s.Value)
		default:
			missing = append(missing, s.Name)
		}
	}
	if len(scanned) == 0 && len(answered) == 0 && len(missing) == 0 {
		return
	}
	// State the sizing facts only; the Sizing "Heads up" caveat owns the
	// run-kcp-scan-metrics remediation, so we don't repeat it here. A scanned figure
	// is a measurement ("Sized from your scan"); a band is an answer ("From your
	// answer"), which isn't a measurement so it reads differently.
	var parts []string
	if len(scanned) > 0 {
		parts = append(parts, "**Sized from your scan:** "+strings.Join(scanned, ", ")+".")
	}
	if len(answered) > 0 {
		parts = append(parts, "**From your answer:** "+strings.Join(answered, ", ")+".")
	}
	if len(missing) > 0 {
		note := "Not measured: " + strings.Join(missing, ", ") + "."
		if len(parts) == 0 {
			note = "**Sizing signals.** " + note
		}
		parts = append(parts, note)
	}
	md.AddParagraph(strings.Join(parts, " "))
}

// observationCategory maps a scan-derived observation to the recommendation whose
// "Why" it belongs under, so each caveat surfaces beside the decision it affects
// rather than in a separate section.
var observationCategory = map[string]string{
	"Tiered storage in use":                nodeHistorical,
	"No throughput metrics scanned":        nodeSizing,
	"Migration may already be in progress": nodeDataMigration,
	"MirrorMaker 2 checkpoints detected":   nodeDataMigration,
	"Kafka Streams in use":                 nodeDataMigration,
}

// caveatBullets groups a cluster's observations into "Why"-list bullets keyed by
// recommendation category, leading each with its severity so it reads as a caveat
// rather than part of the rationale. When shortSizingNote is set, the no-throughput
// sizing caveat is reduced to a one-line pointer, because the full remediation is
// already stated once at the top of the plan.
func caveatBullets(obs []Observation, shortSizingNote bool) map[string][]string {
	out := map[string][]string{}
	for _, o := range obs {
		cat, ok := observationCategory[o.Title]
		if !ok {
			continue
		}
		lead := "Note"
		if o.Severity == "warn" {
			lead = "Heads up"
		}
		detail := o.Detail
		if shortSizingNote && o.Title == "No throughput metrics scanned" {
			detail = "Sizing is a lower bound; see the note at the top of the plan."
		}
		out[cat] = append(out[cat], "**"+lead+":** "+detail)
	}
	return out
}

// hasObservation reports whether a cluster carries an observation with the given
// title.
func hasObservation(obs []Observation, title string) bool {
	for _, o := range obs {
		if o.Title == title {
			return true
		}
	}
	return false
}

// fleetMetricsCommand names the metrics-collection command for the fleet: the
// source-specific one when every cluster shares a source kind, else the generic one
// naming both (a mixed fleet, or a questionnaire run that never named its source).
func fleetMetricsCommand(ep *EnginePlan) string {
	nonMSK, msk := 0, 0
	for _, cp := range ep.Clusters {
		if cp.SourcePlatform != "" {
			nonMSK++
		} else {
			msk++
		}
	}
	p := engine.Profile{StateFile: ep.Header.StateFilePath, Scanless: ep.Header.StateFilePath == ""}
	switch {
	case nonMSK > 0 && msk == 0:
		p.SourceType = engine.SourceApacheKafka
	case msk > 0 && nonMSK == 0 && (!p.Scanless || allDeclaredMSK(ep)):
		// Amazon MSK, scanned or named by an answer: the discover command.
		p.Scanless = false
	default:
		p.Scanless = true // mixed fleet (or unnamed source): name both
	}
	return engine.MetricsScanCommand(p)
}

// allDeclaredMSK reports whether every cluster's source was answered as Amazon MSK
// (a scanless run that named its source).
func allDeclaredMSK(ep *EnginePlan) bool {
	for _, cp := range ep.Clusters {
		if !cp.sourceDeclaredMSK {
			return false
		}
	}
	return len(ep.Clusters) > 0
}

// fleetSizingIsLowerBound reports whether every planned (non-withheld) cluster
// lacks scanned throughput metrics, so the sizing lower-bound caveat applies
// fleet-wide and is stated once at the top rather than under every cluster.
func fleetSizingIsLowerBound(ep *EnginePlan) bool {
	planned := 0
	for _, cp := range ep.Clusters {
		if cp.Plan.Withheld {
			continue
		}
		planned++
		if !hasObservation(cp.Observations, "No throughput metrics scanned") {
			return false
		}
	}
	return planned > 0
}

// valueWithAlt shows a verdict's alternative alongside its recommendation in the
// table cell, e.g. "PNI + Egress PrivateLink Endpoint (or PrivateLink)".
func valueWithAlt(value, altValue string) string {
	if altValue == "" {
		return value
	}
	return value + " (or " + altValue + ")"
}

// withAlternative folds a verdict's distinct alternative option into its reason,
// so the "why" carries it inline instead of as a separate section.
func withAlternative(reason, altValue, altReason string) string {
	if altValue == "" {
		return reason
	}
	return reason + " An alternative is " + altPhrase(altValue) + ". " + altReason
}

// altPhrase reads an alternative's label after "An alternative is": a label that is
// an imperative ("Mirror your data across first") becomes an infinitive ("to mirror
// your data across first"); a name ("PrivateLink", "Confluent Replicator") is
// unchanged.
func altPhrase(v string) string {
	first, rest, _ := strings.Cut(v, " ")
	switch first {
	case "Mirror", "Use", "Run", "Add", "Move", "Start", "Keep", "Copy", "Create", "Migrate":
		return "to " + strings.ToLower(first[:1]) + first[1:] + " " + rest
	}
	return v
}

// renderAppRecommendation renders one application's migration verdicts (data
// migration, schema, connectors, topics, historical) — the per-app half of the
// plan, on top of the shared infrastructure above.
func renderAppRecommendation(md *markdown.Markdown, p engine.PlanResult, c map[string][]string, qaFragment string, caveats map[string][]string, hold bool) {
	renderSectionStatus(md, planGroupState(p.Withheld, c, appVerdicts, hold), qaFragment)
	dmAltV, dmAltR := "", ""
	if a := p.Switchover.Alternative; a != nil {
		dmAltV, dmAltR = a.Value, a.Reason
	}
	rows := [][]string{
		vrow(nodeDataMigration, valueWithAlt(p.Switchover.Value, dmAltV), withLink(shortWhy(p.Switchover.Reason), p.Switchover.Source), c),
		vrow(nodeSchema, p.Schema.Value, withLink(shortWhy(p.Schema.Reason), p.Schema.Source), c),
		vrow(nodeConnectors, p.Connectors.Value, withLink(shortWhy(p.Connectors.Reason), p.Connectors.Source), c),
		vrow(nodeTopics, p.Topics.Value, withLink(shortWhy(p.Topics.Reason), p.Topics.Source), c),
		vrow(nodeHistorical, p.HistoricalData.Value, withLink(shortWhy(p.HistoricalData.Reason), p.HistoricalData.Source), c),
	}
	md.AddTable([]string{"Category", "Recommendation", "Why"}, rows)

	dmReason := withAlternative(p.Switchover.Reason, dmAltV, dmAltR)
	md.AddHeading("Why these recommendations", 4)
	md.AddList(whyListCaveats(c, caveats,
		nodeDataMigration, withTechAssistCTA(dmReason, p.Switchover.TechAssist),
		nodeSchema, withTechAssistCTA(p.Schema.Reason, p.Schema.TechAssist),
		nodeConnectors, p.Connectors.Reason,
		nodeTopics, p.Topics.Reason,
		nodeHistorical, p.HistoricalData.Reason,
	))
}

// withTechAssistCTA appends the shared "Talk to a person" call to action to a
// verdict's why-text when it needs a specialist (TechAssist). It is the single CTA
// for both schema and data-migration handoffs, linking the Talk to a person section.
func withTechAssistCTA(reason string, techAssist bool) string {
	if !techAssist {
		return reason
	}
	return reason + " [Talk to a person](#talk-to-a-person) if you'd like a hand."
}

// withLink appends a "(docs)" link to text when a source URL is present.
func withLink(text, source string) string {
	if source == "" {
		return text
	}
	if text == "" {
		return "[docs](" + source + ")"
	}
	return text + " [docs](" + source + ")"
}
