package plan

import (
	"bytes"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/confluentinc/kcp/internal/types"
)

// RenderMarkdown emits the human-readable Plan: Source Environment,
// Sizing & Cluster Decisions, Actions Needed, and collapsed appendices.
// `cfg` must be the same PlanConfig the PlanService used to build the
// plan (product-fact numbers in the Definitions block and the partition
// cap in the appendix read from it).
func RenderMarkdown(p *types.Plan, cfg *PlanConfig) ([]byte, error) {
	var b bytes.Buffer

	fmt.Fprintf(&b, "# Migration Plan — %s → Confluent Cloud\n\n", p.Header.Source)
	// `from <path>` clause is omitted when the path is empty — library
	// callers (pkg/lib) pass bytes, not a file path; only the CLI
	// `kcp report plan --state-file ...` populates this field.
	fromClause := ""
	if p.Header.StateFilePath != "" {
		fromClause = fmt.Sprintf(" from `%s`", p.Header.StateFilePath)
	}
	schemaSuffix := ""
	if p.Header.PlanSchemaVersion != "" {
		schemaSuffix = fmt.Sprintf(" · plan schema `%s`", p.Header.PlanSchemaVersion)
	}
	fmt.Fprintf(&b, "_Generated %s by KCP %s%s%s._\n\n", p.Header.GeneratedAt.Format("2006-01-02 15:04:05 UTC"), p.Header.KCPVersion, fromClause, schemaSuffix)

	writeDefinitions(&b, cfg)
	writeSourceEnvironment(&b, p)
	writeSizingAndDecisions(&b, p, cfg)
	writeOpenQuestions(&b, p)
	writeSizingAppendix(&b, p, cfg)
	writeRulesAppendix(&b, p)

	return b.Bytes(), nil
}

func writeOpenQuestions(b *bytes.Buffer, p *types.Plan) {
	if len(p.OpenQuestions) == 0 {
		return
	}
	b.WriteString("## 3. Actions Needed\n\n")
	b.WriteString("Each item below is a concrete action that tightens the recommendation in **Sizing & Cluster Decisions**. The current recommendation stands; doing these closes a state-file or scan gap.\n\n")
	for i, g := range groupOpenQuestions(p.OpenQuestions) {
		fmt.Fprintf(b, "%d. **%s**\n", i+1, g.oq.Title)
		if len(g.clusters) > 0 {
			fmt.Fprintf(b, "   - _Affects:_ %s\n", formatClusterList(g.clusters))
		}
		if g.oq.Body != "" {
			fmt.Fprintf(b, "   - %s\n", g.oq.Body)
		}
		if g.oq.HowToClose != "" {
			fmt.Fprintf(b, "   - _How to close:_ %s\n", g.oq.HowToClose)
		}
	}
	b.WriteString("\n")
}

// oqGroup collapses OQs that render identically (same ID + Body +
// HowToClose) into one entry with an `Affects:` line. Plan-level OQs
// (empty ClusterID) never merge with per-cluster OQs; OQs whose
// HowToClose differs (e.g. per-region commands) stay distinct.
type oqGroup struct {
	oq       types.OpenQuestion
	clusters []string
}

// groupOpenQuestions returns first-seen-order groups; callers must pass
// a priority-sorted input slice if they want priority order preserved.
func groupOpenQuestions(oqs []types.OpenQuestion) []oqGroup {
	groups := make([]oqGroup, 0, len(oqs))
	byKey := make(map[string]int, len(oqs))
	for _, oq := range oqs {
		var key string
		switch {
		case oq.ClusterID == "":
			// Plan-level — never merge with per-cluster OQs.
			key = "plan-level\x00" + oq.ID + "\x00" + oq.Title
		case oq.ID != "":
			// Per-cluster grouping: include Body + HowToClose so two
			// clusters whose rendered text differs (e.g. per-region
			// commands) stay as separate items.
			key = "cluster\x00" + oq.ID + "\x00" + oq.Body + "\x00" + oq.HowToClose
		default:
			// No ID and a ClusterID — can't safely group; keep distinct.
			key = "cluster\x00\x00" + oq.Title + "\x00" + oq.ClusterID
		}
		idx, ok := byKey[key]
		if !ok {
			groups = append(groups, oqGroup{oq: oq})
			idx = len(groups) - 1
			byKey[key] = idx
		}
		if oq.ClusterID != "" {
			groups[idx].clusters = append(groups[idx].clusters, oq.ClusterID)
		}
	}
	return groups
}

// formatClusterList renders a list of cluster IDs as “ `a` “, “ `b` “,
// “ `c` “ for inline display.
func formatClusterList(ids []string) string {
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = "`" + id + "`"
	}
	return strings.Join(parts, ", ")
}

func writeDefinitions(b *bytes.Buffer, cfg *PlanConfig) {
	caps := cfg.EnterpriseCaps
	b.WriteString("## Definitions\n\n")
	b.WriteString("- **Enterprise / Dedicated** — Confluent Cloud cluster tiers. Enterprise has elastic billing per eCKU; Dedicated is fixed-provisioned per CKU. **MZ** (Multi-Zone) is the default Dedicated topology; **SZ** (Single-Zone) fires only when `requires_99_95_sla_within_a_single_zone: true` is set in `plan-inputs.yaml`.\n")
	fmt.Fprintf(b, "- **eCKU** — Elastic Confluent Kafka Unit, the throughput unit on Enterprise clusters. %d MB/s ingress + %d MB/s egress per eCKU at the per-eCKU caps used below.\n", caps.PerECKUIngressMBps, caps.PerECKUEgressMBps)
	b.WriteString("- **CKU** — Confluent Kafka Unit, the Dedicated-tier equivalent of eCKU. Sizing math is the same; only the unit name changes. Dedicated clusters always render with `CKU`.\n")
	b.WriteString("- **P95** — the 95th-percentile sustained throughput observed in the metrics window. Sizing uses P95 (override with `sizing_percentile`) so a transient spike doesn't permanently inflate the recommended cluster size.\n")
	b.WriteString("- **Final size** — the recommended eCKU (Enterprise) or CKU (Dedicated) count for the cluster. `(floor)` next to a value means the SLA minimum was binding (the math came in below the floor and was rounded up).\n")
	b.WriteString("- **Peak burst** — short-window peak throughput observed in metrics, expressed as eCKU. Surfaces in the spiky-workload note when peak diverges from P95 by more than the configured ratio.\n")
	fmt.Fprintf(b, "- **PNI** (Private Network Interface) — AWS-to-AWS private connectivity, up to %d eCKU on Enterprise. The default for AWS Enterprise; **always required on Dedicated** (AWS).\n", caps.PNIMaxECKU)
	fmt.Fprintf(b, "- **PrivateLink** — capped at %d eCKU on Enterprise. Fires when `target_cloud != \"aws\"` (PNI is AWS-only), when `cc_egress_required: true` (PNI lacks native CC→customer egress), or when `projected_pni_gateway_count >= 2`. Also the cross-cloud private path on Dedicated when `target_cloud` is Azure / GCP.\n", caps.PrivateLinkMaxECKU)
	fmt.Fprintf(b, "- **ACL cap (%d)** — Enterprise supports up to %d ACLs; exceeding the cap forces Dedicated. Source: cluster-types.html.\n\n", caps.ACLCountCap, caps.ACLCountCap)
}

func writeSourceEnvironment(b *bytes.Buffer, p *types.Plan) {
	b.WriteString("## 1. Source Environment\n\n")
	if len(p.SourceEnvironment.Clusters) == 0 {
		b.WriteString("_No clusters found in the state file. Re-run `kcp discover` / `kcp scan ...` and try again._\n\n")
		return
	}
	totalBrokers := 0
	totalTopics := 0
	serverlessCount := 0
	for _, c := range p.SourceEnvironment.Clusters {
		// Serverless clusters have no broker count by design — exclude
		// from the total so the headline number reflects Provisioned
		// brokers only.
		if !c.IsServerless {
			totalBrokers += c.BrokerCount
		} else {
			serverlessCount++
		}
		totalTopics += c.TopicCount
	}
	regionWord := pluralize("region", p.SourceEnvironment.TotalRegions)
	clusterWord := pluralize("cluster", len(p.SourceEnvironment.Clusters))
	serverlessNote := ""
	if serverlessCount > 0 {
		serverlessNote = fmt.Sprintf(" (incl. %d MSK Serverless %s — no broker count by design)", serverlessCount, pluralize("cluster", serverlessCount))
	}
	fmt.Fprintf(b, "- **%d** source %s across **%d** %s · **%d** brokers · **%d** topics%s\n\n",
		len(p.SourceEnvironment.Clusters), clusterWord, p.SourceEnvironment.TotalRegions, regionWord, totalBrokers, totalTopics, serverlessNote)
	b.WriteString("| Cluster | Region | Brokers | Topics |\n")
	b.WriteString("|---|---|---:|---:|\n")
	for _, c := range p.SourceEnvironment.Clusters {
		brokerCell := fmt.Sprintf("%d", c.BrokerCount)
		if c.IsServerless {
			brokerCell = "_N/A (serverless)_"
		}
		fmt.Fprintf(b, "| %s | %s | %s | %d |\n", escapeMarkdownTableCell(c.ClusterID), escapeMarkdownTableCell(c.Region), brokerCell, c.TopicCount)
	}
	b.WriteString("\n")
}

func writeSizingAndDecisions(b *bytes.Buffer, p *types.Plan, cfg *PlanConfig) {
	b.WriteString("## 2. Sizing & Cluster Decisions\n\n")
	if len(p.Sizing) == 0 {
		b.WriteString("_No clusters to size._\n\n")
		return
	}

	// Build cluster_id-keyed lookups so the rendered table can't silently
	// mis-pair a cluster's sizing with another cluster's verdict — earlier
	// the loop indexed all three slices by `i`, which was correct for plans
	// built by PlanService.Build() but fragile under any other construction.
	ctByID := make(map[string]types.ClusterTypeDecision, len(p.ClusterTypeDecision))
	for _, d := range p.ClusterTypeDecision {
		ctByID[d.ClusterID] = d
	}
	netByID := make(map[string]types.NetworkingDecision, len(p.NetworkingDecision))
	for _, d := range p.NetworkingDecision {
		netByID[d.ClusterID] = d
	}

	percentileLabel := percentileHeader(p.Inputs.SizingPercentile)
	fmt.Fprintf(b, "| Cluster | %s in / out (MBps) | Partitions | Final size | Cluster Type | Networking |\n", percentileLabel)
	b.WriteString("|---|---|---:|---:|---|---|\n")
	for _, s := range p.Sizing {
		ctDecision := ctByID[s.ClusterID]
		net := netByID[s.ClusterID].Verdict
		ctLabel := clusterTypeLabel(ctDecision)
		sizeCell := formatSizeCell(s.FinalECKU, ctDecision, s.Degraded)
		throughputCell := fmt.Sprintf("%.1f / %.1f", s.SizedInMBps, s.SizedOutMBps)
		partitionsCell := fmt.Sprintf("%d", s.UserPartitions)
		// Per-column provisional markers: any input in InputsMissing
		// flips the columns it actually drives. The verdict columns
		// (Cluster Type / Networking) still render because they're
		// deterministic given the inputs that ARE present (customer
		// flags, target_cloud) — only the columns whose inputs are
		// genuinely missing get the provisional marker. A trailing
		// note appears in the Why line so the reader sees what's
		// missing.
		if s.Degraded {
			throughputCell = "_metrics missing_"
			sizeCell = formatSizeCell(s.FinalECKU, ctDecision, true)
		}
		// Partitions column reflects topics specifically (the only
		// signal that drives partition count). The `*` marker is
		// broader — ANY missing input flips the sizing cell so a
		// table-only reader sees every cluster with a provisional
		// verdict, not just the topics-missing ones.
		if slices.Contains(s.InputsMissing, "topics") {
			partitionsCell = "_unknown_"
		}
		if len(s.InputsMissing) > 0 {
			sizeCell += " *"
		}
		fmt.Fprintf(b, "| %s | %s | %s | %s | %s | %s |\n",
			escapeMarkdownTableCell(s.ClusterID), throughputCell, partitionsCell, sizeCell, ctLabel, net)
	}
	b.WriteString("\n")
	if hasProvisional(p.Sizing) {
		b.WriteString("`*` = sizing is provisional — some scan inputs were missing; see each cluster's Why line for the specifics.\n\n")
	}

	// Per-cluster rationale: each line cites the cluster-type decision and
	// the networking decision. Reads cleanly even for 30+ clusters because
	// each entry is one or two lines.
	b.WriteString("### Why These Recommendations\n\n")
	srcByID := make(map[string]types.SourceClusterSummary, len(p.SourceEnvironment.Clusters))
	for _, sc := range p.SourceEnvironment.Clusters {
		srcByID[sc.ClusterID] = sc
	}
	// Triggers that fire on every cluster (e.g. a global flag in plan-
	// inputs.yaml) get one top-of-section cost callout instead of the
	// same warning repeated per cluster. Per-cluster overrides where
	// only some clusters fire keep the inline cost callout — the noise
	// signal there is real.
	globalCustomerTriggers := detectGlobalCustomerTriggers(p.ClusterTypeDecision)
	if len(globalCustomerTriggers) > 0 {
		writeCostCallout(b, globalCustomerTriggers, calloutGlobal)
		if szIsGlobal(globalCustomerTriggers) {
			writeSZTradeoff(b, cfg, calloutGlobal)
		}
	}
	for _, s := range p.Sizing {
		ct := ctByID[s.ClusterID]
		unit := finalSizeUnit(ct)
		src := srcByID[s.ClusterID]
		if s.Degraded {
			// Metrics-degraded clusters get a symptom-only line here;
			// the action lives in Actions Needed (so the symptom isn't
			// duplicated across two surfaces).
			if src.IsServerless {
				fmt.Fprintf(b, "- **%s** — **MSK Serverless source**; broker count is N/A by design and the CloudWatch metrics path differs from Provisioned. Final %s defaults to the SLA floor (%d) until throughput is supplied. See Actions Needed below.\n", s.ClusterID, unit, s.FinalECKU)
			} else {
				fmt.Fprintf(b, "- **%s** — metrics missing (%s). Final %s defaults to the SLA floor (%d). See Actions Needed below.\n", s.ClusterID, s.DegradedReason, unit, s.FinalECKU)
			}
			continue
		}
		var pieces []string
		var customerDeclaredTriggers []types.HardLimitTrigger
		skippedRuleCount := countSkippedRules(ct.EvaluatedRules)
		if len(ct.Triggers) == 0 {
			noFire := fmt.Sprintf("Cluster type **%s** — no hard-limit rule fired", clusterTypeLabel(ct))
			if skippedRuleCount > 0 {
				noFire += fmt.Sprintf(" (%d %s skipped pending missing inputs — see Appendix A2)", skippedRuleCount, pluralize("rule", skippedRuleCount))
			}
			pieces = append(pieces, noFire)
		} else {
			rules := make([]string, 0, len(ct.Triggers))
			for _, t := range ct.Triggers {
				rules = append(rules, fmt.Sprintf("%s (%s)", t.Description, t.Evidence))
				if t.CustomerDeclared {
					customerDeclaredTriggers = append(customerDeclaredTriggers, t)
				}
			}
			pieces = append(pieces, fmt.Sprintf("Cluster type **%s** — %s", clusterTypeLabel(ct), strings.Join(rules, "; ")))
		}
		if n, ok := netByID[s.ClusterID]; ok {
			pieces = append(pieces, fmt.Sprintf("Networking **%s** — %s", n.Verdict, n.Reason))
		}
		fmt.Fprintf(b, "- **%s** — %s.\n", s.ClusterID, strings.Join(pieces, ". "))
		if len(s.InputsMissing) > 0 {
			fmt.Fprintf(b, "  - _Inputs missing: %s — sizing math is provisional until the scan is re-run; the verdict above resolves on the rules that could still evaluate. See Actions Needed below for the next command._\n", strings.Join(s.InputsMissing, ", "))
		}
		// Per-cluster cost callout: only render triggers that didn't
		// already fire as global (those got the one-time banner up top).
		perClusterTriggers := filterNonGlobalTriggers(customerDeclaredTriggers, globalCustomerTriggers)
		if len(perClusterTriggers) > 0 {
			writeCostCallout(b, perClusterTriggers, calloutPerCluster)
		}
		// Single-Zone resilience tradeoff: skipped when the SZ trigger
		// fired globally (banner already covered it); otherwise emit
		// inline so the per-cluster reader sees the failure-domain
		// note next to the verdict.
		if ct.Verdict == types.ClusterTypeDedicated && ct.Topology == types.TopologySingleZone && !szIsGlobal(globalCustomerTriggers) {
			writeSZTradeoff(b, cfg, calloutPerCluster)
		}
		// Spiky-workload note (FYI only — sizing already absorbs the spike).
		// Suppress when sizing landed on the SLA floor: the spike couldn't
		// have pushed the cluster larger anyway (floor binds first), so
		// the hint to flip `sizing_percentile: p99` would be misleading.
		if (s.SpikyIngress || s.SpikyEgress) && s.FinalECKU > s.SLAFloorECKU {
			absorbed := "Enterprise elasticity absorbs"
			if ct.Verdict == types.ClusterTypeDedicated {
				absorbed = "the sized Dedicated capacity absorbs"
			}
			fmt.Fprintf(b, "  - _Note: %s. Sizing is P95-based and %s the spike; set `sizing_percentile: p99` in `plan-inputs.yaml` if you'd rather size to the peak._\n", spikyDescription(s), absorbed)
		}
	}
	b.WriteString("\n")
}

// spikyDescription returns the inline "peak X vs P95 Y (Zx)" phrasing
// for the FYI note on spiky clusters. Guards against P95==0 (the spiky
// flag fires for any positive peak when P95 is zero — `peak > 2.0 * 0`)
// so the ratio doesn't render as +Inf.
func spikyDescription(s types.ClusterSizing) string {
	var parts []string
	if s.SpikyIngress {
		parts = append(parts, formatSpikeRatio("ingress", s.PeakInMBps, s.SizedInMBps))
	}
	if s.SpikyEgress {
		parts = append(parts, formatSpikeRatio("egress", s.PeakOutMBps, s.SizedOutMBps))
	}
	return strings.Join(parts, "; ")
}

func formatSpikeRatio(dir string, peak, p95 float64) string {
	if p95 <= 0 {
		return fmt.Sprintf("%s peak %s MB/s (no P95 baseline)", dir, formatMBps(peak))
	}
	return fmt.Sprintf("%s peak %s MB/s vs P95 %s MB/s (%.1fx)", dir, formatMBps(peak), formatMBps(p95), peak/p95)
}

// formatMBps renders a MB/s value with precision scaled to the value's
// magnitude, so a 0.27 MB/s peak vs 0.002 MB/s P95 reads as "0.27 vs
// 0.002" rather than "0 vs 0" (which makes a 136x ratio look like
// nonsense).
func formatMBps(v float64) string {
	switch {
	case v >= 10:
		return fmt.Sprintf("%.0f", v)
	case v >= 1:
		return fmt.Sprintf("%.1f", v)
	case v >= 0.01:
		return fmt.Sprintf("%.3f", v)
	default:
		return fmt.Sprintf("%.4f", v)
	}
}

// escapeMarkdownTableCell escapes the column-separator character so an
// evidence string like "auth ⊥ a|b" doesn't break the table layout.
// Backslash is also escaped to keep the result unambiguous on re-render.
func escapeMarkdownTableCell(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, "|", `\|`)
	return s
}

// pluralize returns `singular` when n == 1, else `singular + "s"`.
// Trivial but used in multiple places ("cluster"/"clusters",
// "rule"/"rules") so the call sites read naturally without inline
// if-statements or programmatic `(s)` hacks.
func pluralize(singular string, n int) string {
	if n == 1 {
		return singular
	}
	return singular + "s"
}

// hasProvisional reports whether any cluster has at least one
// load-bearing scan signal missing — used to decide whether to emit
// the `*` legend after the Sizing & Cluster Decisions table.
func hasProvisional(sizings []types.ClusterSizing) bool {
	for _, s := range sizings {
		if len(s.InputsMissing) > 0 {
			return true
		}
	}
	return false
}

// countSkippedRules returns how many evaluated rules were `skipped`
// (i.e. could not be evaluated because their inputs were missing). The
// renderer uses this to append a one-liner note to "no hard-limit rule
// fired" verdicts so the reader doesn't mistake the silence for
// certainty.
func countSkippedRules(rules []types.RuleEvaluation) int {
	n := 0
	for _, r := range rules {
		if r.Outcome == types.RuleSkipped {
			n++
		}
	}
	return n
}

// slaFloorList renders the SLA-floor table as an inline phrase, e.g.
// "1 eCKU for 99.9, 1 eCKU for 99.95, 2 eCKU for 99.99". Sorted by SLA
// key for determinism. Lexicographic sort coincides with numeric
// ascending for the current tier shape; revisit if a tier like "100"
// is ever added.
func slaFloorList(cfg *PlanConfig) string {
	keys := make([]string, 0, len(cfg.PlanInputDefaults.SLAFloorECKU))
	for k := range cfg.PlanInputDefaults.SLAFloorECKU {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = fmt.Sprintf("%d eCKU for %s", cfg.PlanInputDefaults.SLAFloorECKU[k], k)
	}
	return strings.Join(parts, ", ")
}

// finalSizeUnit returns "eCKU" or "CKU" for the given cluster-type
// verdict so rendered prose matches the unit in the Sizing & Cluster
// Decisions table.
func finalSizeUnit(ct types.ClusterTypeDecision) string {
	if ct.Verdict == types.ClusterTypeDedicated {
		return "CKU"
	}
	return "eCKU"
}

func writeSizingAppendix(b *bytes.Buffer, p *types.Plan, cfg *PlanConfig) {
	if len(p.SizingAppendix) == 0 {
		return
	}
	partitionCap := cfg.EnterpriseCaps.PerECKUPartitionRate
	b.WriteString("## Appendix A1 — Sizing Math\n")
	b.WriteString("<details><summary>Show sizing math per cluster</summary>\n\n")
	fmt.Fprintf(b, "Each cluster is sized by taking the largest of its three throughput-vs-cap ratios (ingress, egress, partitions) and scaling that by `(1 + headroom)`, so the recommended size has spare capacity above the observed %s. Headroom for this run is `%.2f` (override via `headroom_fraction` in `plan-inputs.yaml`). SLA floor binds when the math comes in below the minimum eCKU for the target SLA (%s — published in the Confluent Cloud cluster-types SLA table). The `Sized` and `Final` columns are in eCKU on Enterprise and CKU on Dedicated.\n\n", percentileHeader(p.Inputs.SizingPercentile), p.Inputs.HeadroomFraction, slaFloorList(cfg))
	// The formula is identical for every cluster (caps + headroom are
	// constants, not per-cluster). Print it once, then a single audit
	// table covers every cluster in a row.
	fmt.Fprintf(b, "Formula: `%s`\n\n", p.SizingAppendix[0].Formula)
	b.WriteString("| Cluster | Ingress ratio | Egress ratio | Partition ratio | Max (driver) | Sized | SLA floor | Final |\n")
	b.WriteString("|---|---:|---:|---:|---|---:|---:|---:|\n")
	for _, s := range p.Sizing {
		// Topics missing → partition input is `_unknown_`, sizing math
		// can't be computed; show the SLA-floor fallback alongside the
		// note. Other "missing inputs" don't affect sizing math itself.
		if slices.Contains(s.InputsMissing, "topics") {
			fmt.Fprintf(b, "| %s | _unknown_ | _unknown_ | _unknown_ / %d | _n/a_ | _n/a_ | %d | %d (floor) |\n",
				escapeMarkdownTableCell(s.ClusterID), partitionCap, s.SLAFloorECKU, s.FinalECKU)
			continue
		}
		if s.Degraded {
			fmt.Fprintf(b, "| %s | _degraded_ | _degraded_ | %d / %d | _n/a_ | _n/a_ | %d | %d |\n",
				escapeMarkdownTableCell(s.ClusterID), s.UserPartitions, partitionCap, s.SLAFloorECKU, s.FinalECKU)
			continue
		}
		fmt.Fprintf(b, "| %s | %.4f | %.4f | %.4f | **%.4f** (%s) | %d | %d | %d |\n",
			escapeMarkdownTableCell(s.ClusterID), s.IngressRatio, s.EgressRatio, s.PartitionRatio, s.MaxRatio, s.MaxRatioDriver, s.SizedECKU, s.SLAFloorECKU, s.FinalECKU)
	}
	b.WriteString("\nMax-ratio is multiplied by `(1 + headroom)` and rounded up to get `Sized`. `Final` is `max(Sized, SLA floor)`.\n\n")
	b.WriteString("</details>\n\n")
}

// writeRulesAppendix surfaces the full hard-limit-rule evaluation trace
// per cluster: every rule's outcome (fired / not_fired / skipped) with
// evidence or skip-reason. Lets a reviewer audit "what would the rules
// engine have said given this state" without re-running the tool.
// Collapsed by default since most readers don't need it.
func writeRulesAppendix(b *bytes.Buffer, p *types.Plan) {
	hasAny := false
	for _, ct := range p.ClusterTypeDecision {
		if len(ct.EvaluatedRules) > 0 {
			hasAny = true
			break
		}
	}
	if !hasAny {
		return
	}
	b.WriteString("## Appendix A2 — Hard-Limit Rules Evaluated\n")
	b.WriteString("<details><summary>Show per-cluster rule-evaluation trace</summary>\n\n")
	b.WriteString("Every cluster runs the same hard-limit catalog; this table records each rule's outcome so a reviewer can confirm the verdict and see negative evidence (e.g. \"47 ACLs ≤ 4000 cap\"). `skipped` rows mean the rule couldn't be evaluated — the cluster's verdict resolves on the other rules.\n\n")
	for _, ct := range p.ClusterTypeDecision {
		if len(ct.EvaluatedRules) == 0 {
			continue
		}
		fmt.Fprintf(b, "**%s**\n\n", escapeMarkdownTableCell(ct.ClusterID))
		b.WriteString("| Rule | Outcome | Detail |\n|---|---|---|\n")
		for _, r := range ct.EvaluatedRules {
			detail := r.Evidence
			if r.Outcome == types.RuleSkipped {
				detail = r.SkipReason
			}
			fmt.Fprintf(b, "| `%s` (%s) | `%s` | %s |\n",
				escapeMarkdownTableCell(r.RowID),
				escapeMarkdownTableCell(r.Description),
				r.Outcome,
				escapeMarkdownTableCell(detail))
		}
		b.WriteString("\n")
	}
	b.WriteString("</details>\n\n")
}

// --- cost callout & Single-Zone resilience tradeoff -----------------
//
// When a customer-declared trigger fires on every cluster (a global
// plan-input flag), the N inline callouts collapse into one banner
// above the Why list. Partial fires keep the per-cluster callout. The
// SZ resilience tradeoff piggybacks on the same scope rule.

// szTriggerRowID is the RowID of the SZ-SLA customer trigger that, in
// addition to the cost callout, gets a paired Single-Zone resilience
// tradeoff banner. Constant-named here so the two emit sites (global
// banner + per-cluster suppression) can't drift.
const szTriggerRowID = "sla_99_95_single_zone"

// calloutScope selects between the one-time banner at the top of the
// Why section (calloutGlobal) and the inline nested-list-item attached
// to one cluster's bullet (calloutPerCluster). The body text is the
// same; only the lead phrase and indent differ.
type calloutScope int

const (
	calloutGlobal calloutScope = iota
	calloutPerCluster
)

const costCalloutBody = "Dedicated was forced by customer-declared `plan-inputs.yaml` flag(s) — %s. Dedicated has a higher monthly cost than Enterprise. If a flag was set in error, flip it to `false` and re-run; confirm with your Confluent account team if unsure."

const szTradeoffBody = "**Single-Zone resilience tradeoff:** the 99.95%% SLA selected here is a same-AZ failure-domain choice. An AZ-wide outage takes the cluster offline. Multi-Zone (99.99%% SLA, ≥%d CKU) is the resilience-first alternative."

// writeCostCallout emits the cost-callout block. Global scope renders
// the top-of-section banner once; per-cluster scope renders the inline
// nested-list-item version attached to one cluster's bullet.
func writeCostCallout(b *bytes.Buffer, triggers []types.HardLimitTrigger, scope calloutScope) {
	labels := formatCustomerTriggerLabels(triggers)
	switch scope {
	case calloutGlobal:
		fmt.Fprintf(b, "> ⚠ **Cost callout (applies to every cluster below):** "+costCalloutBody+"\n\n", labels)
	case calloutPerCluster:
		fmt.Fprintf(b, "  - > ⚠ **Cost callout:** "+costCalloutBody+"\n", labels)
	}
}

// writeSZTradeoff emits the Single-Zone resilience-tradeoff block. The
// MZ floor comes from PlanConfig so renaming a tier in YAML doesn't
// desync from prose.
func writeSZTradeoff(b *bytes.Buffer, cfg *PlanConfig, scope calloutScope) {
	mzFloor := cfg.PlanInputDefaults.SLAFloorECKU["99.99"]
	switch scope {
	case calloutGlobal:
		fmt.Fprintf(b, "> ℹ "+szTradeoffBody+"\n\n", mzFloor)
	case calloutPerCluster:
		fmt.Fprintf(b, "  - > ℹ "+szTradeoffBody+"\n", mzFloor)
	}
}

// szIsGlobal reports whether the SZ-SLA customer trigger appears in
// the global-banner set — if it does, per-cluster sites must suppress
// their inline SZ tradeoff to avoid duplicating the message.
func szIsGlobal(global []types.HardLimitTrigger) bool {
	return slices.ContainsFunc(global, func(t types.HardLimitTrigger) bool { return t.RowID == szTriggerRowID })
}

// detectGlobalCustomerTriggers finds customer-declared triggers that
// fire on *every* cluster — those are global flags (one plan-input
// flipped, no per-cluster scoping) and deserve one top-of-section
// banner instead of N repeated per-cluster callouts. Per-cluster
// overrides (where the same trigger fires on a subset) keep their
// inline cost callout because the noise signal there is real.
func detectGlobalCustomerTriggers(decisions []types.ClusterTypeDecision) []types.HardLimitTrigger {
	if len(decisions) == 0 {
		return nil
	}
	// Count occurrences per customer-declared RowID across all clusters.
	// Use a per-cluster `seen` set so a duplicate RowID inside one
	// cluster's Triggers (defensive — DecideClusterType today emits at
	// most once) doesn't inflate the count past `len(decisions)` and
	// fool the "fires on every cluster" check.
	count := make(map[string]int)
	firstSeen := make(map[string]types.HardLimitTrigger)
	for _, d := range decisions {
		seen := make(map[string]bool, len(d.Triggers))
		for _, t := range d.Triggers {
			if !t.CustomerDeclared || seen[t.RowID] {
				continue
			}
			seen[t.RowID] = true
			count[t.RowID]++
			if _, ok := firstSeen[t.RowID]; !ok {
				firstSeen[t.RowID] = t
			}
		}
	}
	var global []types.HardLimitTrigger
	for rowID, n := range count {
		if n == len(decisions) {
			global = append(global, firstSeen[rowID])
		}
	}
	// Stable order by RowID so the rendered banner doesn't reshuffle
	// across runs (determinism contract).
	sort.Slice(global, func(i, j int) bool { return global[i].RowID < global[j].RowID })
	return global
}

// filterNonGlobalTriggers returns the subset of `cluster` that does
// NOT appear in `global`. Used at the per-cluster cost-callout site
// so a trigger that already got the top-of-section banner isn't
// repeated inline.
func filterNonGlobalTriggers(cluster, global []types.HardLimitTrigger) []types.HardLimitTrigger {
	if len(global) == 0 {
		return cluster
	}
	globalIDs := make(map[string]struct{}, len(global))
	for _, t := range global {
		globalIDs[t.RowID] = struct{}{}
	}
	var out []types.HardLimitTrigger
	for _, t := range cluster {
		if _, isGlobal := globalIDs[t.RowID]; !isGlobal {
			out = append(out, t)
		}
	}
	return out
}

// formatCustomerTriggerLabels renders the comma-separated trigger labels
// used inside the cost callout. Uses the human-readable Description
// (e.g. "99.95% single-zone SLA required") rather than the raw RowID
// (e.g. "sla_99_95_single_zone") so a customer sees something they
// recognise from the YAML comments.
func formatCustomerTriggerLabels(triggers []types.HardLimitTrigger) string {
	labels := make([]string, len(triggers))
	for i, t := range triggers {
		labels[i] = fmt.Sprintf("%s (`%s`)", t.Description, t.RowID)
	}
	return strings.Join(labels, "; ")
}

// percentileHeader returns the uppercase label rendered in the throughput
// column header so the customer can see which percentile produced the
// numbers (P95 / P99 / Max).
func percentileHeader(percentile string) string {
	switch percentile {
	case "p99":
		return "P99"
	case "max":
		return "Max"
	default:
		return "P95"
	}
}

// clusterTypeLabel returns the customer-facing label for a cluster-type
// decision. Dedicated clusters carry a topology suffix (MZ vs SZ) so the
// reader can see at a glance whether the verdict is Multi-Zone or the
// 99.95%-SLA-driven Single-Zone variant. Enterprise renders as-is.
func clusterTypeLabel(ct types.ClusterTypeDecision) string {
	if ct.Verdict != types.ClusterTypeDedicated {
		return string(ct.Verdict)
	}
	switch ct.Topology {
	case types.TopologySingleZone:
		return "Dedicated Single-Zone (SZ)"
	case types.TopologyMultiZone:
		return "Dedicated Multi-Zone (MZ)"
	default:
		return "Dedicated"
	}
}

// formatSizeCell renders the "Final size" column. Enterprise clusters
// are sized in eCKU; Dedicated clusters are sized in CKU (same integer,
// different unit per the Confluent product taxonomy).
func formatSizeCell(finalECKU int, ct types.ClusterTypeDecision, degraded bool) string {
	suffix := "eCKU"
	value := finalECKU
	if ct.Verdict == types.ClusterTypeDedicated && ct.FinalCKU != nil {
		suffix = "CKU"
		value = *ct.FinalCKU
	}
	if degraded {
		return fmt.Sprintf("%d %s (floor)", value, suffix)
	}
	return fmt.Sprintf("%d %s", value, suffix)
}
