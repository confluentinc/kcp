package plan

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/confluentinc/kcp/internal/services/plan/engine"
)

// sameAnswer compares two answer values, treating a multi-valued answer as an
// unordered token set so `[a, b]` and `[b, a]` are equal.
func sameAnswer(a, b string, multi bool) bool {
	if !multi {
		return a == b
	}
	return canonicalTokens(a) == canonicalTokens(b)
}

func canonicalTokens(v string) string {
	if v == "" {
		return ""
	}
	toks := strings.Split(v, ", ")
	sort.Strings(toks)
	return strings.Join(toks, ", ")
}

// Fact is a scan-derived answer shown for reference (not a question).
type Fact struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// scanFactsOf extracts the scan-derived answers from a built profile.
func scanFactsOf(p engine.Profile) []Fact {
	partitions := "unknown (no topic scan)"
	if p.PartitionsExact != nil {
		partitions = strconv.FormatFloat(*p.PartitionsExact, 'f', -1, 64)
	}
	auth := "not detected"
	if len(p.SourceAuthTypes) > 0 {
		auth = strings.Join(p.SourceAuthTypes, ", ")
	}
	facts := []Fact{
		{"msk_cluster_type", p.MSKClusterType},
		{"kafka_version", p.KafkaVersion},
		{"source_auth_types", auth},
		{"partitions", partitions},
	}
	if p.StorageMode != nil {
		facts = append(facts, Fact{"tiered_storage", tieredLabel(*p.StorageMode)})
	}
	if p.MSKConnectPresent != nil {
		facts = append(facts, Fact{"msk_connect", *p.MSKConnectPresent})
	}
	if p.SelfManagedConnectors != nil {
		facts = append(facts, Fact{"self_managed_connectors", *p.SelfManagedConnectors})
	}
	return facts
}

func tieredLabel(v string) string {
	if v == "Yes" {
		return "yes"
	}
	return "no"
}

// openCounts returns the number of open required and open optional questions. An
// inert question (required in the catalog but provably changes nothing for this
// cluster) is counted as optional: it is answerable but does not block, so it must
// not inflate the required count that drives the cluster's Ready/needs-answers state.
func openCounts(qs []ResolvedQuestion) (required, optional int) {
	for _, q := range qs {
		switch q.Status {
		case statusOpenRequired:
			required++
		case statusOpenOptional, statusOpenInert:
			optional++
		}
	}
	return
}

func factValue(cp ClusterPlan, name string) string {
	for _, f := range cp.ScanFacts {
		if f.Name == name {
			return f.Value
		}
	}
	return ""
}

// commaNum inserts thousands separators into a plain integer string ("1500" ->
// "1,500"), matching the comma-formatted partition counts in plan.md. Non-integer
// input is returned unchanged.
func commaNum(s string) string {
	for _, r := range s {
		if r < '0' || r > '9' {
			return s
		}
	}
	n := len(s)
	if n <= 3 {
		return s
	}
	var b strings.Builder
	pre := n % 3
	if pre > 0 {
		b.WriteString(s[:pre])
		b.WriteByte(',')
	}
	for i := pre; i < n; i += 3 {
		b.WriteString(s[i : i+3])
		if i+3 < n {
			b.WriteByte(',')
		}
	}
	return b.String()
}

// writeInputsHeader writes the concise top-of-file header: what the file is, how
// to use it next, how many required answers are still pending, and (for a fleet)
// a one-line note on defaults vs per-cluster overrides.
func writeInputsHeader(b *strings.Builder, ep *EnginePlan, layered bool) {
	b.WriteString("# plan-inputs.yaml: your answers for `kcp report plan`.\n")
	b.WriteString("# Re-running reads this file and writes it back; your answers are preserved (it only adds any new follow-up questions).\n")
	b.WriteString("# Fill the lines marked `# not set`, then re-run (adjust the paths to where your files are):\n")
	b.WriteString("#   " + rerunCommand(ep) + "\n")
	if n := ep.Summary.OpenRequired; n > 0 {
		b.WriteString(fmt.Sprintf("# %d required question%s still open before a full plan.\n", n, plural(n)))
	} else {
		b.WriteString("# All required questions answered. The plan is ready.\n")
	}
	if layered {
		b.WriteString("#\n")
		b.WriteString("# all_clusters:  every question. Your answers here apply to every cluster.\n")
		b.WriteString("# clusters:      each cluster's scanned values; uncomment a line to override just that cluster.\n")
		b.WriteString("# Tags:  `# not set` = required, fill it   ·   `# default` = built-in default   ·   `# from scan` = detected (auto-updates).\n")
	}
	b.WriteString("\n")
}

// RenderPlanInputsYAML generates a round-trip plan-inputs.yaml. A single-cluster
// fleet renders flat: one section under `clusters:` carrying every question with
// its full wording inline. A multi-cluster fleet uses progressive disclosure —
// fleet-wide decisions live in a `defaults:` block (answered once, full wording),
// per-cluster questions are documented once in a reference catalog, and each
// cluster's section is terse: `key: token  # [tok | tok | …]` one-liners.
// Re-running after answering may reveal follow-ups.
func RenderPlanInputsYAML(ep *EnginePlan) string {
	if len(ep.Clusters) == 1 {
		return renderPlanInputsFlat(ep)
	}
	return renderPlanInputsLayered(ep)
}

// renderPlanInputsFlat renders a single cluster with all its questions inline —
// no `defaults:` block, since there is nothing to share across a fleet of one.
func renderPlanInputsFlat(ep *EnginePlan) string {
	var b strings.Builder
	writeInputsHeader(&b, ep, false)
	b.WriteString("clusters:\n")

	cp := ep.Clusters[0]
	req, opt := openCounts(cp.Questions)
	header := "region: " + cp.Region
	if pc := factValue(cp, "partitions"); pc != "" {
		header += " · partitions (scanned): " + commaNum(pc)
	}
	header += fmt.Sprintf(" · %d open required, %d open optional", req, opt)
	b.WriteString("  " + yamlKey(cp.Key) + ":   # " + header + "\n")

	const ind = "    "
	scanless := ep.Header.StateFilePath == ""
	answeredTitle := "ALREADY ANSWERED / FROM YOUR SCAN: edit to override"
	if scanless {
		answeredTitle = "ALREADY ANSWERED / PRESET: edit to override"
	}
	writeQuestionGroup(&b, ind, "REQUIRED: answer these", cp.Questions, scanless, func(q ResolvedQuestion) bool { return q.Status == "open_required" })
	writeQuestionGroup(&b, ind, "OPTIONAL: defaults pre-selected, change if needed", cp.Questions, scanless, func(q ResolvedQuestion) bool {
		return q.Status == statusOpenOptional || q.Status == statusOpenInert
	})
	writeQuestionGroup(&b, ind, answeredTitle, cp.Questions, scanless, func(q ResolvedQuestion) bool { return q.Status == "answered" || q.Status == "scan" })
	writeApps(&b, ind, cp)
	return b.String()
}

// renderPlanInputsLayered renders a `defaults:` block with the fleet-wide
// decisions (full wording, answered once) plus a one-time reference catalog for
// the per-cluster questions, followed by a terse per-cluster section — each line
// a `key: token  # [tok | tok | …]` one-liner, so a large fleet stays legible.
func renderPlanInputsLayered(ep *EnginePlan) string {
	var b strings.Builder
	writeInputsHeader(&b, ep, true)

	fleet := fleetWideQuestions(ep)
	union := allQuestionsUnion(ep)

	// all_clusters: every question, findable in one place, with full wording. The
	// only ACTIVE lines are the required `# not set` ones — defaults and scan facts
	// are commented (they already have a value); uncomment one to override it.
	b.WriteString("all_clusters:\n")
	const ind = "  "
	// Scan facts (even when required-because-undetected) belong in the Scan-facts
	// group, not here, so a required-when-missing scan fact isn't listed twice.
	writeAllClustersGroup(&b, ind, "Required: set these", union, ep, fleet, func(q ResolvedQuestion) bool { return q.Required && !q.Scan })
	writeAllClustersGroup(&b, ind, "Optional: a default applies; uncomment a line to change it", union, ep, fleet, func(q ResolvedQuestion) bool {
		return !q.Required && !q.Scan && q.Key != "target_auth"
	})
	writeAllClustersGroup(&b, ind, "Derived from your source auth per cluster; set one here to force it on ALL clusters", union, ep, fleet, func(q ResolvedQuestion) bool {
		return q.Key == "target_auth"
	})
	writeAllClustersGroup(&b, ind, "Scan facts: detected per cluster below; set one here to force it on ALL clusters", union, ep, fleet, func(q ResolvedQuestion) bool {
		// Read-only scan facts are authoritative per cluster, so no force-on-all knob.
		return q.Scan && !q.ReadOnly && q.Key != "target_auth"
	})

	// clusters: each cluster's scanned values (live, auto-updating) and the
	// per-cluster override knobs for the all_clusters answers above.
	b.WriteString("\nclusters:\n")
	for _, cp := range ep.Clusters {
		header := "region: " + cp.Region
		if pc := factValue(cp, "partitions"); pc != "" {
			header += " · partitions (scanned): " + commaNum(pc)
		}
		b.WriteString("  " + yamlKey(cp.Key) + ":   # " + header + "\n")
		const cind = "    "
		writeClusterScanLines(&b, cind, ep, fleet, cp)
		writeClusterOverrides(&b, cind, ep, fleet, cp)
		writeApps(&b, cind, cp)
	}
	return b.String()
}

// allQuestionsUnion returns every question across the fleet, deduped by key in
// first-seen (reading) order; wording and flags come from the first occurrence.
func allQuestionsUnion(ep *EnginePlan) []ResolvedQuestion {
	seen := map[string]bool{}
	var out []ResolvedQuestion
	for _, cp := range ep.Clusters {
		for _, q := range cp.Questions {
			if seen[q.Key] {
				continue
			}
			seen[q.Key] = true
			out = append(out, q)
		}
	}
	return out
}

// writeAllClustersGroup renders one titled group of the all_clusters block: each
// question's full wording and legend, then its value line.
func writeAllClustersGroup(b *strings.Builder, indent, title string, union []ResolvedQuestion, ep *EnginePlan, fleet []ResolvedQuestion, include func(ResolvedQuestion) bool) {
	any := false
	for _, q := range union {
		if include(q) {
			any = true
			break
		}
	}
	if !any {
		return
	}
	b.WriteString(indent + "# ── " + title + " ──\n")
	for _, q := range union {
		if !include(q) {
			continue
		}
		b.WriteString(indent + "# " + q.Prompt + "\n")
		if q.Hint != "" {
			b.WriteString(indent + "#   (" + q.Hint + ")\n")
		}
		for _, line := range q.Legend {
			b.WriteString(indent + "#   " + line + "\n")
		}
		b.WriteString(indent + allClustersValueLine(q, ep, fleet) + "\n")
	}
}

// allClustersValueLine renders the value line for a question in the all_clusters
// block, tagged by where its value comes from: `# not set` (required, you must
// set it — the only active line), `# default` (built-in default, commented),
// `# from scan` / `# derived from source auth` (per-cluster values, commented).
func allClustersValueLine(q ResolvedQuestion, ep *EnginePlan, fleet []ResolvedQuestion) string {
	tokHint := ""
	if len(q.Tokens) > 0 {
		tokHint = " · [" + strings.Join(q.Tokens, " | ") + "]"
	}
	switch {
	case q.Status == statusOpenInert:
		// Required in the catalog, but inert for this cluster (e.g. a size-forced tier
		// moots private-networking): a commented, answerable knob, never a blocker.
		return "# " + q.Key + ":   # optional here — doesn't change the plan" + tokHint
	case q.Key == "target_auth":
		// Its default is derived per cluster, so no fleet default — unless the
		// customer set it fleet-wide, in which case show that.
		if val, answered := fleetAnswer(ep, fleet, q); answered {
			return q.Key + ": " + fmtVal(val, q.Multi)
		}
		return "# " + q.Key + ":   # derived from source auth" + tokHint
	case q.Scan:
		return "# " + q.Key + ":   # from scan" + tokHint
	}
	val, answered := fleetAnswer(ep, fleet, q)
	// Preserve original scope: an answer the customer set under all_clusters is
	// written back under all_clusters even when the question no longer applies to
	// every cluster (so it isn't demoted to the one cluster that still consumes it).
	if !answered {
		if scv, ok := allClustersSourced(ep, q.Key); ok {
			val, answered = scv, true
		}
	}
	if q.Required {
		if answered {
			return q.Key + ": " + fmtVal(val, q.Multi)
		}
		// Answered per cluster (values differ, so no fleet-wide value): don't tag it
		// "# not set", which reads as unanswered when the answers live in the clusters.
		if answeredByAllClusters(ep, q.Key) {
			return "# " + q.Key + ":   # answered per cluster below" + tokHint
		}
		return q.Key + ":   # not set"
	}
	def := questionDefault(q.Key)
	if answered && !sameAnswer(val, def, q.Multi) {
		return q.Key + ": " + fmtVal(val, q.Multi) // set fleet-wide to a non-default value
	}
	return "# " + q.Key + ": " + fmtVal(def, q.Multi) + "   # default"
}

// answeredByAllClusters reports whether every cluster that NEEDS the question has a
// value for it. Specialist-routed clusters are excluded, and so are clusters where
// the question is inert (open_inert) — an inert cluster doesn't need the answer, so
// its empty value must not make a fleet-wide answer read as "still unset". Used to
// tell "answered per cluster" apart from "still unset" in the all_clusters block.
func answeredByAllClusters(ep *EnginePlan, key string) bool {
	seen := false
	for i := range ep.Clusters {
		cp := ep.Clusters[i]
		if cp.Plan.Withheld {
			continue
		}
		q := findResolved(cp.Questions, key)
		if q == nil {
			continue // doesn't apply to this cluster
		}
		if q.Status == statusOpenInert {
			continue // inert here: this cluster doesn't need it
		}
		if q.Value == "" {
			return false
		}
		seen = true
	}
	return seen
}

// fleetAnswer returns the value every cluster shares for a key (empty when the
// key isn't fleet-wide or clusters disagree), and whether such a shared value exists.
func fleetAnswer(ep *EnginePlan, fleet []ResolvedQuestion, q ResolvedQuestion) (string, bool) {
	if !fleetHas(fleet, q.Key) {
		return "", false
	}
	if cv, common := commonClusterValue(ep, q.Key); common && cv != "" {
		return cv, true
	}
	return "", false
}

// fmtVal formats a value for a YAML line — a multi renders as a [list].
func fmtVal(v string, multi bool) string {
	if multi {
		if v == "" {
			return "[]"
		}
		return "[" + v + "]"
	}
	return v
}

// writeClusterScanLines writes a cluster's scanned facts (commented, auto-updated
// on re-scan) plus the target_auth value derived from its source auth. A scan
// fact the scan couldn't capture is written as an active `# not set` — it's
// required here.
func writeClusterScanLines(b *strings.Builder, indent string, ep *EnginePlan, fleet []ResolvedQuestion, cp ClusterPlan) {
	var detected, missing, readonly []string
	for _, q := range cp.Questions {
		if !q.Scan {
			continue
		}
		if q.Status == "open_required" {
			// A scan-category fact the scan didn't capture — you must supply it.
			missing = append(missing, q.Key+":   # not set · ["+strings.Join(q.Tokens, " | ")+"]")
			continue
		}
		val := fmtVal(q.Value, q.Multi)
		switch {
		case q.Overridden:
			// Customer-supplied or changed: render live (active line). The scan may
			// never have detected this, so we don't claim "was from scan" — see
			// ResolvedQuestion.provenance.
			detected = append(detected, q.Key+": "+val+"   # "+q.provenance())
		case q.ReadOnly:
			// The scan is authoritative here, so we don't advertise an override; it's
			// listed for audit (the key still works if hand-added to override a bad scan).
			readonly = append(readonly, q.Key+" = "+val)
		default:
			detected = append(detected, "# "+q.Key+": "+val+"   # "+q.provenance())
		}
	}
	// target_auth's default is derived from this cluster's source auth. Show that
	// suggestion unless the customer pinned their own value here (or fleet-wide).
	if taq := findResolved(cp.Questions, "target_auth"); taq != nil {
		fleetVal, fleetSet := fleetAnswer(ep, fleet, *taq)
		switch {
		case fleetSet && sameAnswer(taq.Value, fleetVal, taq.Multi):
			// shown once in all_clusters — don't repeat per cluster
		case taq.Source == "cluster" && taq.Value != "":
			detected = append(detected, "target_auth: "+fmtVal(taq.Value, taq.Multi)+"   # your override")
		default:
			detected = append(detected, "# target_auth: ["+derivedTargetAuthToken(cp)+"]   # derived from source auth")
		}
	}
	// Actionable first: the facts you must supply, then the detected read-outs.
	if len(missing) > 0 {
		b.WriteString(indent + "# ── The scan couldn't detect these, so set them ──\n")
		for _, l := range missing {
			b.WriteString(indent + l + "\n")
		}
	}
	if len(detected) > 0 {
		b.WriteString(indent + "# ── From the scan: auto-updates on re-scan; uncomment a line to override just this cluster ──\n")
		for _, l := range detected {
			b.WriteString(indent + l + "\n")
		}
	}
	if len(readonly) > 0 {
		b.WriteString(indent + "# ── Detected from your scan (read-only; the scan is authoritative) ──\n")
		for _, l := range readonly {
			b.WriteString(indent + "# " + l + "\n")
		}
		// The escape hatch: the key still works if hand-added, so signpost how to
		// override a wrong value (or a planned change) without inviting routine edits.
		b.WriteString(indent + "# Scan wrong, or planning a change (e.g. a Kafka upgrade)? Add the key with a\n")
		b.WriteString(indent + "# value to override (e.g. `kafka_version: older`); options are in the Questions list.\n")
	}
}

// writeClusterOverrides writes the per-cluster override knobs for the declared
// all_clusters answers: a commented placeholder for each, or an active line when
// this cluster already diverges from the fleet answer.
func writeClusterOverrides(b *strings.Builder, indent string, ep *EnginePlan, fleet []ResolvedQuestion, cp ClusterPlan) {
	var lines []string
	for _, q := range cp.Questions {
		if q.Scan || q.Key == "target_auth" {
			continue
		}
		tokHint := ""
		if len(q.Tokens) > 0 {
			tokHint = " · [" + strings.Join(q.Tokens, " | ") + "]"
		}
		// Write an active line for any cluster whose answer differs from what the
		// all_clusters line hands back on re-parse — not only cluster-declared ones.
		// Otherwise, when one cluster's override de-promotes the shared line, the
		// answer the other clusters inherited from it would silently vanish on the
		// round-trip. `effective` is the value a reader gets if they don't override:
		// the promoted fleet value, else the built-in default (empty when required).
		effective, effAnswered := fleetAnswer(ep, fleet, q)
		if !effAnswered {
			// Mirror allClustersValueLine: an answer the customer set under all_clusters
			// is still emitted there, so this cluster inherits it and must not re-emit it
			// as a per-cluster override (which would demote it on the round-trip).
			if scv, ok := allClustersSourced(ep, q.Key); ok {
				effective = scv
			} else {
				effective = questionDefault(q.Key)
			}
		}
		if q.Value != "" && !sameAnswer(q.Value, effective, q.Multi) {
			lines = append(lines, q.Key+": "+fmtVal(q.Value, q.Multi)+"   # override for this cluster")
		} else {
			lines = append(lines, "# "+q.Key+":   # override for this cluster"+tokHint)
		}
	}
	if len(lines) == 0 {
		return
	}
	b.WriteString(indent + "# ── Override an all_clusters answer here (uncomment) ──\n")
	for _, l := range lines {
		b.WriteString(indent + l + "\n")
	}
}

// derivedTargetAuthToken picks the target auth kcp would default to from the
// scanned source auth: preserve mTLS if the source uses it, else API keys.
func derivedTargetAuthToken(cp ClusterPlan) string {
	for _, a := range cp.SourceAuths {
		if strings.Contains(strings.ToLower(a), "mtls") {
			return "mtls"
		}
	}
	return "api-keys"
}

// commonClusterValue returns the value shared by EVERY cluster for a key, and
// whether they all agree. A key absent from any cluster is treated as disagreement.
func commonClusterValue(ep *EnginePlan, key string) (string, bool) {
	first := true
	var val string
	for _, cp := range ep.Clusters {
		q := findResolved(cp.Questions, key)
		if q == nil {
			return "", false
		}
		if first {
			val = q.Value
			first = false
		} else if !sameAnswer(q.Value, val, q.Multi) {
			return "", false
		}
	}
	return val, !first
}

// allClustersSourced returns the value a key carries when at least one cluster
// answered it from the all_clusters block (Source == sourceAllClusters), and
// whether such a value exists. It lets the writer keep a fleet-wide answer under
// all_clusters on write-back even when the question no longer applies to every
// cluster — without it, the value would be demoted to the one cluster that still
// consumes it and re-open as required for the others on the next run. Clusters
// inheriting an all_clusters answer all share its value, so the first is
// representative; a cluster that overrode it carries Source == sourceCluster and is
// skipped.
func allClustersSourced(ep *EnginePlan, key string) (string, bool) {
	for i := range ep.Clusters {
		cp := ep.Clusters[i]
		if cp.Plan.Withheld {
			continue
		}
		if q := findResolved(cp.Questions, key); q != nil && q.Source == sourceAllClusters {
			return q.Value, true
		}
	}
	return "", false
}

func findResolved(qs []ResolvedQuestion, key string) *ResolvedQuestion {
	for i := range qs {
		if qs[i].Key == key {
			return &qs[i]
		}
	}
	return nil
}

// questionDefault returns a declared question's default token (empty for
// required questions and scan facts).
func questionDefault(key string) string {
	for _, q := range catalog {
		if q.Key == key {
			return q.Default
		}
	}
	return ""
}

// writeApps renders a cluster's optional `applications:` block. Apps are shown
// only when declared; per app, only the app-scoped answers that diverge from the
// cluster (its inherited default) are written — an app with no divergence is
// marked as inheriting. `cind` is the cluster body indent.
func writeApps(b *strings.Builder, cind string, cp ClusterPlan) {
	if len(cp.Apps) == 0 {
		return
	}
	clusterVal := map[string]string{}
	for _, q := range cp.Questions {
		if appQuestionKeys[q.Key] {
			clusterVal[q.Key] = q.Value
		}
	}
	b.WriteString(cind + "applications:   # per-app overrides; unset app answers inherit the cluster\n")
	for _, ap := range cp.Apps {
		b.WriteString(cind + "  " + yamlKey(ap.Name) + ":\n")
		wrote := false
		for _, q := range ap.Questions {
			if q.Value != "" && !sameAnswer(q.Value, clusterVal[q.Key], q.Multi) {
				b.WriteString(cind + "    " + terseLine(q) + "\n")
				wrote = true
			}
		}
		if !wrote {
			b.WriteString(cind + "    # (inherits the cluster's answers)\n")
		}
	}
}

// terseLine renders one per-cluster answer as a single line with an inline
// `# [tok | tok | …]` choice hint. Open questions render `# not set`. A
// scan-detected fact is rendered COMMENTED — a re-scan refreshes it; uncomment to
// pin it as an override.
func terseLine(q ResolvedQuestion) string {
	if q.Status == statusOpenInert && q.Value == "" {
		// Inert here and unset: a commented, answerable knob, never a `# not set` blocker.
		line := "# " + q.Key + ":   # optional here — doesn't change this cluster's plan"
		if len(q.Tokens) > 0 {
			line += " · [" + strings.Join(q.Tokens, " | ") + "]"
		}
		return line
	}
	if q.Status == "open_required" || (q.Scan && q.Value == "") {
		line := q.Key + ":   # not set"
		if len(q.Tokens) > 0 {
			line += " · [" + strings.Join(q.Tokens, " | ") + "]"
		}
		return line
	}
	valuePart := q.Value
	if q.Multi {
		if q.Value == "" {
			valuePart = "[]"
		} else {
			valuePart = "[" + q.Value + "]"
		}
	}
	// Scan facts render commented (a re-scan refreshes them) UNLESS the customer
	// overrode this one, in which case it stays live so the override persists.
	if q.Scan && !q.Overridden {
		return "# " + q.Key + ": " + valuePart + "   # from scan (uncomment to override)"
	}
	suffix := ""
	if len(q.Tokens) > 0 {
		suffix = "  # [" + strings.Join(q.Tokens, " | ") + "]"
	}
	if q.Scan {
		suffix += "  # override of scan"
	}
	return q.Key + ": " + valuePart + suffix
}

// fleetWideQuestions returns the declared (non-scan) questions common to every
// cluster, using the first cluster as the representative for wording and value.
func fleetWideQuestions(ep *EnginePlan) []ResolvedQuestion {
	if len(ep.Clusters) == 0 {
		return nil
	}
	// A key is fleet-wide if it appears as a declared question in every cluster.
	count := map[string]int{}
	for _, cp := range ep.Clusters {
		seen := map[string]bool{}
		for _, q := range cp.Questions {
			if q.Scan || seen[q.Key] {
				continue
			}
			seen[q.Key] = true
			count[q.Key]++
		}
	}
	var out []ResolvedQuestion
	for _, q := range ep.Clusters[0].Questions {
		if !q.Scan && count[q.Key] == len(ep.Clusters) {
			out = append(out, q)
		}
	}
	return out
}

func fleetHas(fleet []ResolvedQuestion, key string) bool {
	for _, q := range fleet {
		if q.Key == key {
			return true
		}
	}
	return false
}

func writeQuestionGroup(b *strings.Builder, indent, title string, qs []ResolvedQuestion, scanless bool, include func(ResolvedQuestion) bool) {
	any := false
	for _, q := range qs {
		if include(q) {
			any = true
			break
		}
	}
	if !any {
		return
	}
	b.WriteString(indent + "# ── " + title + " ──\n")
	for _, q := range qs {
		if !include(q) {
			continue
		}
		b.WriteString(indent + "# " + q.Prompt + "\n")
		if q.Hint != "" {
			b.WriteString(indent + "#   (" + q.Hint + ")\n")
		}
		for _, line := range q.Legend {
			b.WriteString(indent + "#   " + line + "\n")
		}
		b.WriteString(indent + yamlLine(q, scanless))
		b.WriteString("\n")
	}
}

// yamlLine renders one `key: value` line. Required-open questions are left blank
// with a marker; optionals carry their default; multi questions render a token
// list. A scan-detected fact is rendered COMMENTED — a re-scan refreshes it;
// uncomment to pin it as an override.
func yamlLine(q ResolvedQuestion, scanless bool) string {
	if q.Status == statusOpenInert {
		// Required in the catalog, but inert for this cluster: a commented, answerable
		// knob (uncomment to set), never an active `# not set` blocker.
		line := "# " + q.Key + ":   # optional here — doesn't change this cluster's plan"
		if len(q.Tokens) > 0 {
			line += " · [" + strings.Join(q.Tokens, " | ") + "]"
		}
		return line + "\n"
	}
	if q.Status == "open_required" || (q.Scan && q.Value == "") {
		return q.Key + ":   # not set\n"
	}
	valuePart := q.Value
	if q.Multi {
		if q.Value == "" {
			valuePart = "[]"
		} else {
			valuePart = "[" + q.Value + "]"
		}
	}
	// Scan facts render commented (a re-scan refreshes them) UNLESS the customer
	// overrode this one, in which case it stays live so the override persists. With no
	// scan, a scan-category fact is a preset default, not a scan finding.
	if q.Scan && !q.Overridden {
		origin := "from scan"
		if scanless {
			origin = "preset"
		}
		return "# " + q.Key + ": " + valuePart + "   # " + origin + " (uncomment to override)\n"
	}
	if q.Scan {
		// With no scan, a scan-category value the customer set is their answer, not an
		// override of a scan finding — say so.
		tag := "override of scan"
		if scanless {
			tag = "you set this"
		}
		return q.Key + ": " + valuePart + "   # " + tag + "\n"
	}
	return q.Key + ": " + valuePart + "\n"
}

// yamlKey quotes a cluster name if needed to be a valid YAML mapping key.
func yamlKey(s string) string {
	if s == "" {
		return `""`
	}
	for _, r := range s {
		if !(r == '-' || r == '_' || r == '.' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')) {
			return strconv.Quote(s)
		}
	}
	return s
}
