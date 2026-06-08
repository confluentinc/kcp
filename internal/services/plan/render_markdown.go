package plan

import (
	"bytes"
	"fmt"
	"math"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/confluentinc/kcp/internal/types"
)

// regionFlagToken matches the inline `--region <value>` flag that
// per-cluster HowToClose strings embed. Used by `groupOpenQuestions`
// to normalize the flag for grouping so OQs identical except for
// region collapse to one entry, and by the renderer to swap the
// concrete region for a `<region>` placeholder when the collapsed
// group spans multiple regions. Restricted to the AWS region-name
// character class (`[a-z0-9-]`) so a literal `--region <region>`
// placeholder embedded in a template never accidentally matches.
var regionFlagToken = regexp.MustCompile(`--region\s+[a-zA-Z0-9-]+`)

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
	// Section numbering is dynamic: empty Cutover or empty Auth slices
	// drop their section, and Actions Needed claims the next available
	// number. Avoids "jumps from §2 to §5" when an empty fleet skips §3/§4.
	section := 1
	writeSourceEnvironment(&b, p, section)
	section++
	writeSizingAndDecisions(&b, p, cfg, section)
	section++
	if p.Cutover != nil {
		writeCutover(&b, p.Cutover, p.CutoverOverrides, cfg, section)
		section++
	}
	if len(p.Auth) > 0 {
		writeAuth(&b, p.Auth, p.Cutover, p.Inputs, section)
		section++
	}
	if p.Schema != nil {
		writeSchema(&b, p.Schema, p.Inputs, cfg, section)
		section++
	}
	if p.RedFlags != nil && len(p.RedFlags.Rows) > 0 {
		writeRedFlags(&b, p.RedFlags, section)
		section++
	}
	if p.EffortSignals != nil && len(p.EffortSignals.Signals) > 0 {
		writeEffortSignals(&b, p.EffortSignals, section)
		section++
	}
	if p.TieredStorage != nil && len(p.TieredStorage.Clusters) > 0 {
		writeTieredStorage(&b, p.TieredStorage, section)
		section++
	}
	if p.CostReconciliation != nil && len(p.CostReconciliation.Candidates) > 0 {
		writeCostReconciliation(&b, p.CostReconciliation, section)
		section++
	}
	writeOpenQuestions(&b, p, section)
	writeSizingAppendix(&b, p, cfg)
	writeRulesAppendix(&b, p)

	return b.Bytes(), nil
}

func writeOpenQuestions(b *bytes.Buffer, p *types.Plan, section int) {
	if len(p.OpenQuestions) == 0 {
		fmt.Fprintf(b, "## %d. Actions Needed\n\n", section)
		b.WriteString("_No actions outstanding — the Plan above renders without unresolved questions._\n\n")
		return
	}
	fmt.Fprintf(b, "## %d. Actions Needed\n\n", section)
	groups := groupOpenQuestions(p.OpenQuestions)
	siblingIDs := oqIDSet(p.OpenQuestions)
	// Build per-group rendered severity + title up front so the legend
	// can mention only severities that actually appear in this Plan.
	rendered := make([]struct {
		severity string
		title    string
	}, len(groups))
	present := map[string]bool{}
	for i, g := range groups {
		meta := oqMetaFor(g.oq.ID)
		sev, title := promoteSeverity(meta, g.oq.Title, siblingIDs)
		rendered[i].severity = sev
		rendered[i].title = title
		present[sev] = true
	}
	b.WriteString("Each item below is a concrete action that tightens the recommendation. The current recommendation stands; these close state-file gaps, fix invalid inputs, or resolve preference questions.")
	if legend := severityLegend(present); legend != "" {
		fmt.Fprintf(b, " %s", legend)
	}
	b.WriteString("\n\n")
	for i, g := range groups {
		fmt.Fprintf(b, "%d. %s **%s**\n", i+1, rendered[i].severity, rendered[i].title)
		if len(g.clusters) > 0 {
			fmt.Fprintf(b, "   - **Affects:** %s\n", formatClusterList(g.clusters))
		}
		if g.oq.Body != "" {
			fmt.Fprintf(b, "   - %s\n", indentMultiline(g.oq.Body, "     "))
		}
		howToClose := g.oq.HowToClose
		// When a group spans multiple source regions, swap the inline
		// `--region <X>` from the first-seen OQ for a `<region>`
		// placeholder so the rendered command reads generically and
		// the reader knows to map it per cluster from §1 inventory.
		if len(g.regions) > 1 {
			howToClose = regionFlagToken.ReplaceAllString(howToClose, "--region <region>")
		}
		if howToClose != "" {
			fmt.Fprintf(b, "   - _How to close:_ %s\n", indentMultiline(howToClose, "     "))
		}
	}
	b.WriteString("\n")
}

// severityLegend renders only the severities that appear in this Plan.
// Suppresses the legend entirely when nothing is present (no OQs path
// short-circuits earlier; this guards an all-promoted edge case).
func severityLegend(present map[string]bool) string {
	if len(present) == 0 {
		return ""
	}
	parts := []string{}
	if present["🔴"] {
		parts = append(parts, "🔴 blocker (fix before cutover)")
	}
	if present["🟡"] {
		parts = append(parts, "🟡 affects accuracy (fix before relying on the plan)")
	}
	if present["🟢"] {
		parts = append(parts, "🟢 preference (pick one)")
	}
	if len(parts) == 0 {
		return ""
	}
	return "**Severity prefix:** " + strings.Join(parts, " · ") + "."
}

// oqIDSet returns a lookup of OQ IDs present in this Plan. Used to
// drive sibling-aware severity promotions through the OQ registry
// (see oq_registry.go — `promoteSeverity`).
func oqIDSet(oqs []types.OpenQuestion) map[string]bool {
	out := make(map[string]bool, len(oqs))
	for _, oq := range oqs {
		out[oq.ID] = true
	}
	return out
}

// oqGroup collapses OQs that render identically (same ID + Body +
// normalized HowToClose) into one entry with an `Affects:` line.
// Plan-level OQs (empty ClusterID) never merge with per-cluster OQs.
// Per-cluster OQs whose only HowToClose difference is the embedded
// `--region <X>` flag DO merge — the renderer swaps the concrete
// region for a `<region>` placeholder when the collapsed group spans
// multiple regions, since each cluster lives in exactly one region.
type oqGroup struct {
	oq       types.OpenQuestion
	clusters []string
	regions  map[string]struct{}
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
			// Per-cluster grouping: include Body + region-normalized
			// HowToClose so two OQs identical except for `--region <X>`
			// collapse to one entry. Other HowToClose differences keep
			// the items distinct.
			normalized := regionFlagToken.ReplaceAllString(oq.HowToClose, "--region <region>")
			key = "cluster\x00" + oq.ID + "\x00" + oq.Body + "\x00" + normalized
		default:
			// No ID and a ClusterID — can't safely group; keep distinct.
			key = "cluster\x00\x00" + oq.Title + "\x00" + oq.ClusterID
		}
		idx, ok := byKey[key]
		if !ok {
			groups = append(groups, oqGroup{oq: oq, regions: map[string]struct{}{}})
			idx = len(groups) - 1
			byKey[key] = idx
		}
		if oq.ClusterID != "" {
			groups[idx].clusters = append(groups[idx].clusters, oq.ClusterID)
		}
		// Track distinct regions seen across collapsed OQs so the
		// renderer can swap to a `<region>` placeholder when >1.
		if m := regionFlagToken.FindString(oq.HowToClose); m != "" {
			groups[idx].regions[m] = struct{}{}
		}
	}
	return groups
}

// indentMultiline prefixes every newline-separated continuation line
// in `s` with `indent` so the line stays nested under its parent
// ordered-list / bullet item. Without this, an embedded fenced code
// block in an OQ Body or HowToClose terminates the enclosing list
// item because the ``` lands at column 0.
func indentMultiline(s, indent string) string {
	if !strings.Contains(s, "\n") {
		return s
	}
	return strings.ReplaceAll(s, "\n", "\n"+indent)
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
	fmt.Fprintf(b, "- **ACL cap (%d)** — Enterprise supports up to %d ACLs; exceeding the cap forces Dedicated. Source: [%s](%s).\n\n", caps.ACLCountCap, caps.ACLCountCap, caps.Source, caps.Source)
	// Cutover-style + plain-CL definitions live in a collapsible block —
	// they're load-bearing context for §3 but expanding inline triples
	// the Definitions wall before the reader has any context.
	b.WriteString("<details><summary>Cutover-related terms (Stop-Restart-Repeat, Blue/Green, CC Gateway-mediated, etc.)</summary>\n\n")
	b.WriteString("- **Stop-Restart-Repeat** — phased per-service cutover. Each application (or topic) is stopped on MSK, mirrored to CC, and resumed at the CC endpoint, one at a time. Recoverable per step; longer elapsed time.\n")
	b.WriteString("- **Stop-Wait-Restart** — single coordinated maintenance window. Producers stop, the mirror catches up, services resume in sequence inside the window.\n")
	b.WriteString("- **Restart-All-At-Once** — single window where every client reconfigures and reconnects at the same instant. Largest blast radius; one rollback point for the whole fleet.\n")
	b.WriteString("- **Blue/Green** — parallel run on both sides via Cluster Linking. Zero downtime, highest operational complexity; customer-designed orchestration.\n")
	b.WriteString("- **CC Gateway-mediated** — a sidecar component that absorbs the cutover with a 30–90 s `BROKER_NOT_AVAILABLE` window per service, after which clients auto-retry against CC. Removes the per-service producer restart; requires Confluent for Kubernetes + a Gateway Add-On license.\n")
	b.WriteString("- **Plain Cluster Linking** — Cluster Linking without the CC Gateway; the simpler op model. Each service stops, mirror drains, restarts against the CC endpoint (minutes per service). Fully supported; chosen via `prefer_gateway: false`.\n")
	b.WriteString("\n</details>\n\n")
}

func writeSourceEnvironment(b *bytes.Buffer, p *types.Plan, section int) {
	fmt.Fprintf(b, "## %d. Source Environment\n\n", section)
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
	b.WriteString("| Cluster | Region | Brokers | Topics | Source auth |\n")
	b.WriteString("|---|---|---:|---:|---|\n")
	for _, c := range p.SourceEnvironment.Clusters {
		brokerCell := fmt.Sprintf("%d", c.BrokerCount)
		if c.IsServerless {
			brokerCell = "_N/A (serverless)_"
		}
		fmt.Fprintf(b, "| %s | %s | %s | %d | %s |\n",
			escapeMarkdownTableCell(c.ClusterID),
			escapeMarkdownTableCell(c.Region),
			brokerCell,
			c.TopicCount,
			sourceAuthCell(c.SourceAuths),
		)
	}
	b.WriteString("\n")
	if serverlessCount > 0 {
		b.WriteString("**MSK Serverless caveats** — the fleet includes one or more Serverless source clusters. These differ from Provisioned MSK in ways that affect the migration plan:\n")
		b.WriteString("- **No broker inventory.** Serverless is a pay-per-throughput managed service; the broker count column reads `N/A`.\n")
		b.WriteString("- **ACL semantics differ.** Serverless does not expose ACLs via the admin API path kcp scans; the `acl_count_exceeds_cap` rule cannot evaluate on Serverless clusters.\n")
		b.WriteString("- **Auth surface is smaller.** Only SASL/IAM is supported on Serverless; SCRAM / mTLS / unauthenticated paths don't apply.\n")
		b.WriteString("- **Workload shape changes at the migration boundary.** Serverless billing is throughput-based; CC destination clusters are eCKU / CKU sized. Confirm sizing against actual rate metrics with your Confluent account team.\n\n")
	}
}

// sourceAuthCell renders the Source-auth column: comma-separated
// `code`-formatted tokens, or "_none detected_" italic when empty.
// Multiple auths on one cluster (e.g. SCRAM + mTLS both enabled on
// MSK) are surfaced together; the plan never picks one.
func sourceAuthCell(auths []string) string {
	if len(auths) == 0 {
		return "_none detected_"
	}
	parts := make([]string, len(auths))
	for i, a := range auths {
		parts[i] = "`" + a + "`"
	}
	return strings.Join(parts, ", ")
}

func writeSizingAndDecisions(b *bytes.Buffer, p *types.Plan, cfg *PlanConfig, section int) {
	fmt.Fprintf(b, "## %d. Sizing & Cluster Decisions\n\n", section)
	if len(p.Sizing) == 0 {
		b.WriteString("_No clusters to size._\n\n")
		return
	}
	b.WriteString("This section combines three per-cluster decisions: the **sizing** (how many eCKU each cluster needs to absorb its workload at the chosen percentile + headroom), the **cluster type** (Enterprise, Dedicated, or Freight — driven by hard limits like ACL count and customer-declared flags), and the **networking** topology (PNI, PrivateLink, Transit Gateway, or VPC Peering — driven by `target_cloud`, egress requirements, and projected PNI gateway count). They render together because each row's verdict in one column constrains the others: e.g. a Dedicated cluster opens up PrivateLink networking patterns that Enterprise caps.\n\n")

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
			// sizeCell already carries the (floor) suffix via the
			// earlier formatSizeCell call (degraded flag was already
			// true). No re-format needed.
		}
		// Partitions column reflects topics specifically (the only
		// signal that drives partition count). The `*` marker is
		// broader — ANY missing input flips the sizing cell so a
		// table-only reader sees every cluster with a provisional
		// verdict, not just the topics-missing ones.
		if slices.Contains(s.InputsMissing, "topics") {
			partitionsCell = "_unknown_"
		}
		// Provisional `*` marker fires for ANY signal that makes the
		// size cell less than fully trusted: missing scan inputs OR a
		// degraded (metrics-missing) sizing fallback to the SLA floor.
		// Without the second branch, a metrics-degraded cluster shows
		// `1 eCKU (floor)` without the asterisk that explicitly means
		// "provisional" — three notations for the same condition.
		if len(s.InputsMissing) > 0 || s.Degraded {
			sizeCell += " *"
		}
		fmt.Fprintf(b, "| %s | %s | %s | %s | %s | %s |\n",
			escapeMarkdownTableCell(s.ClusterID), throughputCell, partitionsCell, sizeCell, ctLabel, net)
	}
	b.WriteString("\n")
	if hasProvisional(p.Sizing) {
		b.WriteString("`*` = sizing is provisional — some scan inputs were missing or metrics were degraded; see each cluster's Why line. `(floor)` next to a size means the SLA minimum bound it; both can apply to the same cluster.\n\n")
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
	// Fleet-level state-derived Dedicated banner: emit once at the top
	// of the section when ≥ 2 clusters land on Dedicated because of a
	// state-driven rule (no customer-declared trigger). Below the
	// threshold the per-cluster note is still readable; above it we
	// were repeating the same paragraph 30 times.
	stateDerivedDedicatedCount := countStateDerivedDedicated(p.ClusterTypeDecision)
	hoistStateDerivedBanner := stateDerivedDedicatedCount >= 2
	if hoistStateDerivedBanner {
		fmt.Fprintf(b, "> ℹ **Cost direction (fleet):** %d clusters land on Dedicated because of state-derived hard-limit rules — i.e. the cluster as scanned hit a cap that Enterprise can't carry. The escalation isn't recoverable by editing `plan-inputs.yaml`; confirm with your Confluent account team that the scanned capacity reflects each cluster's actual workload before committing. Dedicated has a higher monthly cost than Enterprise.\n\n",
			stateDerivedDedicatedCount)
	}
	// First pass: compute each cluster's rationale prose + per-cluster
	// extras (InputsMissing, customer-declared cost callout, SZ
	// tradeoff, spiky note, state-derived cost note). Bucket clusters
	// by (rationale + extras) — clusters sharing BOTH the same verdict
	// and the same extras paragraph collapse into one bullet with a
	// cluster-list affix. Spiky FYI notes embed per-cluster numeric
	// values so those clusters bucket alone naturally; structural
	// extras like "Inputs missing: acls" collapse across the fleet (the
	// prior version only collapsed when extras were empty, so real
	// scans missing ACLs spammed the paragraph per cluster). Degraded
	// rows always stay standalone.
	type rationaleBucket struct {
		rationale string
		extras    string
		clusters  []string
		order     int
	}
	bucketByKey := map[string]*rationaleBucket{}
	var bucketOrder []*rationaleBucket
	standaloneRows := []string{} // pre-formatted bullets for degraded rows only

	for _, s := range p.Sizing {
		ct := ctByID[s.ClusterID]
		unit := finalSizeUnit(ct)
		src := srcByID[s.ClusterID]
		if s.Degraded {
			// Degraded rows always render standalone — the reason
			// string is per-cluster.
			var line string
			if src.IsServerless {
				line = fmt.Sprintf("- **%s** — **MSK Serverless source**; broker count is N/A by design and the CloudWatch metrics path differs from Provisioned. Final %s defaults to the SLA floor (%d) until throughput is supplied. See Actions Needed below.", s.ClusterID, unit, s.FinalECKU)
			} else {
				line = fmt.Sprintf("- **%s** — metrics missing (%s). Final %s defaults to the SLA floor (%d). See Actions Needed below.", s.ClusterID, s.DegradedReason, unit, s.FinalECKU)
			}
			standaloneRows = append(standaloneRows, line+"\n")
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
		rationale := strings.Join(pieces, ". ")

		// Compute per-cluster extras (rendered below the bullet).
		var extras bytes.Buffer
		if len(s.InputsMissing) > 0 {
			fmt.Fprintf(&extras, "  - _Inputs missing: %s — sizing math is provisional until the scan is re-run; the verdict above resolves on the rules that could still evaluate. See Actions Needed below for the next command._\n", strings.Join(s.InputsMissing, ", "))
		}
		perClusterTriggers := filterNonGlobalTriggers(customerDeclaredTriggers, globalCustomerTriggers)
		if len(perClusterTriggers) > 0 {
			writeCostCallout(&extras, perClusterTriggers, calloutPerCluster)
		}
		if !hoistStateDerivedBanner && ct.Verdict == types.ClusterTypeDedicated && len(customerDeclaredTriggers) == 0 && len(ct.Triggers) > 0 {
			extras.WriteString("  - ℹ **Cost direction:** Dedicated has a higher monthly cost than Enterprise. This verdict is state-derived (a hard-limit rule fired on the cluster as scanned), so the escalation isn't recoverable by editing `plan-inputs.yaml`. Confirm with your Confluent account team that the cluster's capacity reflects your actual workload before committing.\n")
		}
		if ct.Verdict == types.ClusterTypeDedicated && ct.Topology == types.TopologySingleZone && !szIsGlobal(globalCustomerTriggers) {
			writeSZTradeoff(&extras, cfg, calloutPerCluster)
		}
		if (s.SpikyIngress || s.SpikyEgress) && s.FinalECKU > s.SLAFloorECKU {
			absorbed := "Enterprise elasticity absorbs"
			if ct.Verdict == types.ClusterTypeDedicated {
				absorbed = "the sized Dedicated capacity absorbs"
			}
			fmt.Fprintf(&extras, "  - _Note: %s. Sizing is P95-based and %s the spike; set `sizing_percentile: p99` in `plan-inputs.yaml` if you'd rather size to the peak._\n", spikyDescription(s), absorbed)
		}

		// Bucket by (rationale + extras). Identical extras paragraphs
		// collapse to one entry with a cluster-list affix; differing
		// extras keep clusters in separate buckets.
		extrasStr := extras.String()
		key := rationale + "\x00" + extrasStr
		bucket, ok := bucketByKey[key]
		if !ok {
			bucket = &rationaleBucket{rationale: rationale, extras: extrasStr, order: len(bucketOrder)}
			bucketByKey[key] = bucket
			bucketOrder = append(bucketOrder, bucket)
		}
		bucket.clusters = append(bucket.clusters, s.ClusterID)
	}

	// Render in two passes: (1) standalone rows (in original Sizing
	// order they appeared), (2) buckets — each bucket rendered once
	// with its cluster-list lead. Order across (1) + (2) follows the
	// first-appearance order in Sizing, but for simplicity we render
	// standalones first then buckets — at fleet scale this preserves
	// scannability (the special cases lead, then "everything else").
	for _, row := range standaloneRows {
		b.WriteString(row)
	}
	for _, bucket := range bucketOrder {
		if len(bucket.clusters) == 1 {
			fmt.Fprintf(b, "- **%s** — %s.\n", bucket.clusters[0], bucket.rationale)
		} else {
			fmt.Fprintf(b, "- **%s** (%d clusters) — %s.\n", strings.Join(bucket.clusters, ", "), len(bucket.clusters), bucket.rationale)
		}
		if bucket.extras != "" {
			b.WriteString(bucket.extras)
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

// pluralize returns `singular` when n == 1, else the plural form.
// Handles the regular `+ s` rule by default; pass a `y → ies` word
// (e.g. "registry") and the helper rewrites the suffix accordingly.
// Trivial but used in multiple places ("cluster"/"clusters",
// "rule"/"rules", "registry"/"registries") so the call sites read
// naturally without inline if-statements or programmatic `(s)` hacks.
func pluralize(singular string, n int) string {
	if n == 1 {
		return singular
	}
	if strings.HasSuffix(singular, "y") {
		return strings.TrimSuffix(singular, "y") + "ies"
	}
	return singular + "s"
}

// hasProvisional reports whether any cluster has at least one
// load-bearing scan signal missing — used to decide whether to emit
// the `*` legend after the Sizing & Cluster Decisions table.
func hasProvisional(sizings []types.ClusterSizing) bool {
	for _, s := range sizings {
		if len(s.InputsMissing) > 0 || s.Degraded {
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
	if !p.Header.StateGeneratedAt.IsZero() {
		fmt.Fprintf(b, "_Evidence below reflects the source state file as of %s. Re-run `kcp discover` / `kcp scan ...` if the source environment has changed materially since._\n\n", p.Header.StateGeneratedAt.Format("2006-01-02 15:04:05 UTC"))
	}
	for _, ct := range p.ClusterTypeDecision {
		if len(ct.EvaluatedRules) == 0 {
			continue
		}
		fmt.Fprintf(b, "**Cluster** `%s`\n\n", escapeMarkdownTableCell(ct.ClusterID))
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
		fmt.Fprintf(b, "  - ⚠ **Cost callout:** "+costCalloutBody+"\n", labels)
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
		fmt.Fprintf(b, "  - ℹ "+szTradeoffBody+"\n", mzFloor)
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
// countStateDerivedDedicated returns the number of clusters whose
// verdict is Dedicated AND every firing trigger was state-derived
// (no customer-declared flag in the trigger set). Drives the
// fleet-level "Cost direction" banner when many clusters share the
// state-forced escalation — at small N the per-cluster inline note
// is fine; at fleet scale it becomes noise.
func countStateDerivedDedicated(decisions []types.ClusterTypeDecision) int {
	n := 0
	for _, ct := range decisions {
		if ct.Verdict != types.ClusterTypeDedicated || len(ct.Triggers) == 0 {
			continue
		}
		anyCustomer := false
		for _, t := range ct.Triggers {
			if t.CustomerDeclared {
				anyCustomer = true
				break
			}
		}
		if !anyCustomer {
			n++
		}
	}
	return n
}

func detectGlobalCustomerTriggers(decisions []types.ClusterTypeDecision) []types.HardLimitTrigger {
	if len(decisions) == 0 {
		return nil
	}
	// Count occurrences per customer-declared RowID across all clusters.
	// Use a per-cluster `seen` set so a duplicate RowID inside one
	// cluster's Triggers (defensive — decideClusterType today emits at
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

// ----- §3 cutover -----

// writeCutover renders the fleet-wide cutover recommendation: chosen
// style, gateway mediation status with degraded markers, alternatives
// shown for trust, and the gateway prereq table. `overrides` carries
// per-cluster exceptions — clusters whose resolved style differs from
// the fleet default. Skips the section entirely when there's no
// cutover decision (empty fleet).
func writeCutover(b *bytes.Buffer, c *types.CutoverDecision, overrides []types.ClusterCutoverOverride, cfg *PlanConfig, section int) {
	if c == nil {
		return
	}
	fmt.Fprintf(b, "## %d. Cutover Approach\n\n", section)
	b.WriteString("_The style below is the **fleet default** — applied to every cluster that doesn't carry a per-cluster override (`clusters[<name>].downtime_tolerance`). Any cluster-specific overrides land in the **Per-cluster overrides** sub-list below the fleet block._\n\n")
	fmt.Fprintf(b, "- **Style:** %s\n", cutoverStyleLabel(c.Style, c.SubPattern))
	fmt.Fprintf(b, "- **Gateway mediation:** %s\n", cutoverGatewayLabel(c.GatewayMediated, c.RecommendationStatus))
	if marker := recommendationStatusMarker(c.RecommendationStatus); marker != "" {
		fmt.Fprintf(b, "  - %s\n", marker)
	}
	writeCutoverAlternatives(b, c.AlternativesShown)
	writeCutoverPrereqs(b, c.Prereqs)
	writeCutoverOverrides(b, overrides)
	// (writeCutoverPrereqs already emits a trailing blank line; do
	// NOT add another one here — the prior extra `\n` produced a
	// double blank before §4 across every plan.)
	// Blue/Green is customer-designed — kcp doesn't generate the
	// orchestration. Promote the runbook hint to its own sub-heading
	// so the multi-sentence content reads as a separate concern, not
	// a comically heavy list item.
	if c.Style == types.CutoverBlueGreen {
		linkingURL := "https://docs.confluent.io/cloud/current/multi-cloud/cluster-linking/index.html"
		if cfg != nil && cfg.ClusterLinking.Source != "" {
			linkingURL = cfg.ClusterLinking.Source
		}
		b.WriteString("### Runbook (Blue/Green)\n\n")
		fmt.Fprintf(b, "kcp does not generate Blue/Green orchestration. See [Confluent Cloud Cluster Linking guide](%s) for the parallel-run pattern; the customer designs cutover gating, consumer-offset handoff, and rollback.\n\n", linkingURL)
		b.WriteString("**Scope check** — three items to confirm with your account team before committing:\n")
		b.WriteString("- Dual-ingest cost on the source side\n")
		b.WriteString("- Consumer-coordinator change window\n")
		b.WriteString("- Consumer-offset translation strategy\n\n")
	}
}

// cutoverStyleLabel renders the style + sub-pattern label. Stop-Restart-Repeat
// carries the sub-pattern suffix (app-by-app vs topic-by-topic); other
// styles render as-is.
func cutoverStyleLabel(style types.CutoverStyle, sub types.CutoverSubPattern) string {
	base := cutoverStyleName(style)
	if style == types.CutoverStopRestartRepeat && sub != "" {
		return fmt.Sprintf("%s (%s)", base, sub)
	}
	return base
}

func cutoverStyleName(style types.CutoverStyle) string {
	switch style {
	case types.CutoverStopRestartRepeat:
		return "Stop-Restart-Repeat"
	case types.CutoverStopWaitRestart:
		return "Stop-Wait-Restart"
	case types.CutoverRestartAllAtOnce:
		return "Restart-All-At-Once"
	case types.CutoverBlueGreen:
		return "Blue/Green"
	default:
		return string(style)
	}
}

// cutoverGatewayLabel returns the "CC Gateway / plain Cluster Linking /
// not applicable" phrasing for the rendered Gateway mediation line.
// Plain Cluster Linking gets a tradeoff hint so it doesn't read as
// second-class — it's a fully supported path that some customers
// deliberately pick.
func cutoverGatewayLabel(m types.GatewayMediated, status types.RecommendationStatus) string {
	switch m {
	case types.GatewayMediatedTrue:
		return "CC Gateway-mediated (transparent per-service cutover, 30–90 s `BROKER_NOT_AVAILABLE` window)"
	case types.GatewayMediatedFalse:
		if status == types.RecommendationCustomerChoice {
			return "Plain Cluster Linking (gateway opted out via `prefer_gateway: false`). Simpler ops; requires per-service producer restart at cutover. Pick this when the CFK + Gateway Add-On license aren't justifiable for the fleet's downtime budget."
		}
		return "Plain Cluster Linking"
	case types.GatewayMediatedNotApplicable:
		return "Not applicable — Blue/Green doesn't sit on a single cutover step"
	default:
		return string(m)
	}
}

// recommendationStatusMarker returns the inline ℹ note shown beneath
// the Gateway mediation line for unresolved-decision paths, or empty
// for the canonical / customer-choice paths (those don't need a callout).
// "Awaiting" framing, not "Degraded" — plain Cluster Linking is a fully
// supported path; the marker just signals an open decision, not a
// broken state.
func recommendationStatusMarker(status types.RecommendationStatus) string {
	switch status {
	case types.RecommendationDegradedAwaitingOQ:
		return "ℹ **Awaiting gateway intent** — no preference declared yet; the Plan uses plain Cluster Linking until you confirm. See **Actions Needed** for how to choose."
	case types.RecommendationDegradedPrereqsPending:
		return "ℹ **Awaiting gateway prereqs** — gateway path requested but one or more prereqs are still at `not_started`. See **Actions Needed** for the list. Plain Cluster Linking applies in the meantime."
	default:
		return ""
	}
}

// writeCutoverAlternatives renders a short bullet list explaining the
// styles the plan considered and didn't pick. Keeps the reader's trust
// without forcing them to walk the full decision tree.
func writeCutoverAlternatives(b *bytes.Buffer, alts []types.CutoverStyle) {
	if len(alts) == 0 {
		return
	}
	b.WriteString("- **Alternatives considered:**\n")
	for _, s := range alts {
		fmt.Fprintf(b, "  - **%s** — %s\n", cutoverStyleName(s), cutoverAlternativeWhy(s))
	}
}

// cutoverAlternativeWhy returns the one-line "why this isn't the
// recommendation" string for each style.
func cutoverAlternativeWhy(s types.CutoverStyle) string {
	switch s {
	case types.CutoverStopRestartRepeat:
		return "phased per-service rollout; recoverable steps, longer elapsed time. Pick via `downtime_tolerance: seconds_per_service | minutes_per_service | let_confluent_choose`."
	case types.CutoverStopWaitRestart:
		return "single coordinated window; needs the window to be long enough for re-mirroring + validation. Pick via `downtime_tolerance: scheduled_window_sequential`."
	case types.CutoverRestartAllAtOnce:
		return "single window; every client reconfigures at the same instant. Highest blast radius. Pick via `downtime_tolerance: scheduled_window_all_at_once`."
	case types.CutoverBlueGreen:
		return "parallel run via Cluster Linking; zero downtime, highest operational complexity. Pick via `downtime_tolerance: zero`."
	default:
		return ""
	}
}

// writeCutoverPrereqs renders the prereq status table. When the
// prereq list is empty (Blue/Green OR customer opted out), emits a
// symmetric "no prereqs required" stub so both paths visually
// balance — without it, a reader scanning multiple plans wonders
// whether a row went missing. When IAM prereq is absent from a
// non-empty list, adds a one-line note clarifying it was suppressed
// because no IAM source was detected.
func writeCutoverPrereqs(b *bytes.Buffer, prereqs []types.Prereq) {
	if len(prereqs) == 0 {
		b.WriteString("- **Prerequisites:** _none required for this path._\n")
		return
	}
	b.WriteString("- **Prerequisites:**\n\n")
	b.WriteString("  | Prereq | Status |\n")
	b.WriteString("  |---|---|\n")
	var hasIAM bool
	for _, p := range prereqs {
		fmt.Fprintf(b, "  | %s | %s |\n", escapeMarkdownTableCell(p.Description), prereqStatusLabel(p.Status))
		if strings.Contains(p.Description, "IAM") {
			hasIAM = true
		}
	}
	if !hasIAM {
		b.WriteString("\n  _IAM pre-migration prereq omitted — no IAM source detected in this fleet._\n")
	}
	b.WriteString("\n")
}

// writeCutoverOverrides renders one bullet per cluster that resolves
// to a different cutover style than the fleet default. Empty slice =
// homogeneous fleet → nothing emitted. Rejected overrides carry a `*`
// marker + footnote (mirrors §4 Auth's OverrideRejected handling).
func writeCutoverOverrides(b *bytes.Buffer, overrides []types.ClusterCutoverOverride) {
	if len(overrides) == 0 {
		return
	}
	b.WriteString("- **Per-cluster overrides:**\n")
	anyRejected := false
	for _, o := range overrides {
		marker := ""
		if o.OverrideRejected {
			marker = " *"
			anyRejected = true
		}
		fmt.Fprintf(b, "  - `%s` → %s%s", o.ClusterID, cutoverStyleLabel(o.Style, o.SubPattern), marker)
		switch o.GatewayMediated {
		case types.GatewayMediatedTrue:
			b.WriteString(" (gateway-mediated)")
		case types.GatewayMediatedNotApplicable:
			b.WriteString(" (gateway N/A for this style)")
		}
		b.WriteString("\n")
	}
	if anyRejected {
		b.WriteString("\n  `*` = the per-cluster `downtime_tolerance` / `sub_pattern` override wasn't a recognised value, so the cluster fell through to a default. See Actions Needed for the exact typo.\n")
	}
	b.WriteString("\n")
}

func prereqStatusLabel(s types.PrereqStatus) string {
	switch s {
	case types.PrereqMet:
		return "✅ met"
	case types.PrereqInProgress:
		return "🚧 in progress"
	case types.PrereqBlocked:
		return "⛔ not started"
	case types.PrereqUnconfirmed:
		return "❓ unconfirmed"
	default:
		return string(s)
	}
}

// ----- §4 auth -----

// writeAuth renders the per-cluster source→target auth mapping table.
// One row per source auth detected on each cluster.
//
// `cutover` and `inputs` are read for the IAM-transition footnote
// when source-detected auth is IAM AND `iam_pre_migration_status:
// complete` AND the gateway is mediated, the §4 source row is a
// pre-migration snapshot and the prereq says clients have already
// moved off IAM — surface that so §3 and §4 don't read as contradictory.
func writeAuth(b *bytes.Buffer, auths []types.AuthDecision, cutover *types.CutoverDecision, inputs types.PlanInputsResolved, section int) {
	if len(auths) == 0 {
		return
	}
	fmt.Fprintf(b, "## %d. Client Auth Migration\n\n", section)
	b.WriteString("Per-cluster source→target mapping. Each source-auth method on every cluster maps to a recommended Confluent Cloud auth — the recommendation can be overridden globally via `target_auth_method` or per-cluster via `clusters[<name>].target_auth_method`. The **Works via CC Gateway** column describes whether this auth method *could* flow through the CC Gateway when the gateway path is in use — it's a property of the auth mapping, not a statement about which path §3 picked.\n\n")
	// Notes column always renders (`—` placeholder for empty cells)
	// so the §Auth table has identical column structure across all
	// plans. Earlier optimization that dropped the column when no
	// row had a note caused 4-col vs 5-col drift between scenarios
	// — a customer comparing two plans saw inconsistent layouts.
	b.WriteString("| Cluster | Source auth | Target on Confluent Cloud | Works via CC Gateway | Notes |\n")
	b.WriteString("|---|---|---|---|---|\n")
	anyOverrideRejected := false
	for _, a := range auths {
		if len(a.TargetMappings) == 0 {
			fmt.Fprintf(b, "| %s | _none detected_ | _n/a_ | _n/a_ | _Source auth posture unknown — see Actions Needed._ |\n",
				escapeMarkdownTableCell(a.ClusterID))
			continue
		}
		if a.OverrideRejected {
			anyOverrideRejected = true
		}
		for i, row := range a.TargetMappings {
			cluster := escapeMarkdownTableCell(a.ClusterID)
			if i > 0 {
				cluster = ""
			}
			target := fmt.Sprintf("`%s`", row.EffectiveTarget)
			if a.OverrideRejected {
				target += " *"
			}
			noteCell := strings.TrimSpace(row.Note)
			if noteCell == "" {
				noteCell = "—"
			}
			fmt.Fprintf(b, "| %s | `%s` | %s | %s | %s |\n",
				cluster, row.SourceAuth, target,
				gatewayCompatibleLabel(row.GatewayCompatible, row.TransparentSwap),
				escapeMarkdownTableCell(noteCell))
		}
	}
	if anyOverrideRejected {
		b.WriteString("\n`*` = a `target_auth_method` override was supplied but didn't match a recognised value, so the per-source default applies for that row; see Actions Needed for the exact typo.\n")
	}
	// IAM-transition footnote — when prereq is `complete` and
	// gateway is in play, the IAM rows above are a pre-migration
	// snapshot, not the post-migration auth.
	if cutover != nil && inputs.IAMPreMigrationStatus == PrereqStatusCompleteInput && cutover.GatewayMediated == types.GatewayMediatedTrue {
		anyIAM := false
		for _, a := range auths {
			for _, row := range a.TargetMappings {
				if row.SourceAuth == SourceAuthIAM {
					anyIAM = true
					break
				}
			}
		}
		if anyIAM {
			b.WriteString("\n_The IAM rows above reflect the **pre-migration** auth recorded by `kcp discover`. With `iam_pre_migration_status: complete`, clients have moved off IAM (to SCRAM or mTLS) — re-run `kcp discover` / `kcp scan clusters` to refresh §4 against post-migration state._\n")
		}
	}
	writeAuthMappingProvenance(b, auths)
	b.WriteString("\n")
}

// writeAuthMappingProvenance surfaces the per-row source URL +
// last_verified date so a reviewer can audit where each auth-mapping
// recommendation came from. Walks the unique (SourceAuth, Source,
// LastVerified) tuples and emits one bullet per source row.
func writeAuthMappingProvenance(b *bytes.Buffer, auths []types.AuthDecision) {
	type provKey struct{ source, lastVerified, sourceAuth string }
	seen := map[provKey]bool{}
	var rows []provKey
	for _, a := range auths {
		for _, r := range a.TargetMappings {
			if r.Source == "" && r.LastVerified == "" {
				continue
			}
			k := provKey{source: r.Source, lastVerified: r.LastVerified, sourceAuth: r.SourceAuth}
			if seen[k] {
				continue
			}
			seen[k] = true
			rows = append(rows, k)
		}
	}
	if len(rows) == 0 {
		return
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].sourceAuth < rows[j].sourceAuth })
	b.WriteString("\n_Mapping provenance:_\n")
	for _, r := range rows {
		fmt.Fprintf(b, "- `%s` mapping → %s (last verified %s)\n",
			r.sourceAuth, r.source, r.lastVerified)
	}
}

// gatewayCompatibleLabel renders the Gateway-compatible cell.
// Transparent swap means clients don't restart at cutover (gateway
// handles credential exchange in flight); non-transparent gateway
// paths still work but require auth-side coordination (e.g. mTLS cert
// re-issue). Both are spelled out so readers don't infer "transparent"
// means "the only good option".
func gatewayCompatibleLabel(compatible, transparent bool) string {
	switch {
	case compatible && transparent:
		return "✅ yes (transparent swap)"
	case compatible:
		return "✅ yes (auth-swap mode)"
	default:
		return "❌ no"
	}
}

// ----- §schema -----

// writeSchema renders the fleet-wide Schema Migration section. Skips
// rendering entirely on the schemaless path (caller already nils
// p.Schema in that branch). For mixed Confluent+Glue deployments
// both verdicts render — Glue Terraform command and a Schema-Linking
// eligibility table.
func writeSchema(b *bytes.Buffer, dec *types.SchemaDecision, inputs types.PlanInputsResolved, cfg *PlanConfig, section int) {
	if dec == nil {
		return
	}
	fmt.Fprintf(b, "## %d. Schema Migration\n\n", section)
	b.WriteString("Schemas are a fleet-wide concern (one Schema Registry per source, not one per cluster).\n\n")
	// The Schema-Linking glossary only applies to paths that use it
	// (Confluent SR — solo or dual). Pure-Glue paths don't, so the
	// definition would just confuse a Glue-only customer.
	if sourceTouchesConfluent(dec.Source) {
		b.WriteString("_**Schema Linking** is Confluent's source-pushes, CC-receives mirror — the source SR runs a one-directional exporter that streams schema updates to your CC SR endpoint, no manual export/import needed._\n\n")
	}
	b.WriteString("The verdict below combines:\n")
	b.WriteString("- what `kcp scan schema-registry` / `kcp scan glue-schema-registry` found on the source, and\n")
	b.WriteString("- the customer-declared `schema_strategy` / CP version / edition / network-reachability inputs in `plan-inputs.yaml`.\n\n")

	fmt.Fprintf(b, "- **Source detected:** %s\n", schemaSourceLabel(dec.Source, dec.ConfluentSRURLs, dec.GlueRegistries))
	fmt.Fprintf(b, "- **Recommended path%s:** %s\n", pluralize("", len(dec.Paths)), schemaPathsLabelForContext(dec.Paths, inputs.SchemaStrategy, dec.Source))
	fmt.Fprintf(b, "- **Strategy declared:** %s\n\n", schemaStrategyDeclaredLabel(inputs.SchemaStrategy, dec))

	// The eligibility table renders only when the customer's strategy
	// actually engages Schema Linking. Suppress on the `no_schemas`
	// strategy even if a Confluent SR was scanned — the table would
	// otherwise show all-❔ unknown to a reader who explicitly said
	// they're not migrating schemas. The mismatch OQ carries the
	// reconciliation prompt. Normalize "" → unknown here so the
	// comparison matches decideSchema's own normalization (schema.go).
	strategy := inputs.SchemaStrategy
	if strategy == "" {
		strategy = SchemaStrategyUnknown
	}
	suppressEligibilityTable := strategy == SchemaStrategyNoSchemas

	switch dec.Source {
	case types.SchemaSourceGlue:
		writeGluePathCommand(b, dec.GlueRegistries)
	case types.SchemaSourceConfluent:
		if !suppressEligibilityTable {
			writeSchemaLinkingEligibility(b, dec, cfg)
		}
	case types.SchemaSourceConfluentAndGlue:
		// Both registries present: render each path side-by-side so the
		// customer sees both recommendations without having to re-run
		// the Plan against a narrowed scan.
		writeGluePathCommand(b, dec.GlueRegistries)
		if !suppressEligibilityTable {
			writeSchemaLinkingEligibility(b, dec, cfg)
		}
	}

	writeSchemaProvenance(b, dec, cfg)
}

// writeSchemaProvenance emits a citation line whose content depends on
// the source. Glue-only paths cite the `kcp create-asset migrate-schemas`
// docs (the active recommendation); Confluent paths cite the Schema
// Linking docs (the eligibility floor); dual-source cites both.
func writeSchemaProvenance(b *bytes.Buffer, dec *types.SchemaDecision, cfg *PlanConfig) {
	const glueRef = "https://confluentinc.github.io/kcp/command-reference/create-asset/migrate-schemas/"
	switch dec.Source {
	case types.SchemaSourceGlue:
		fmt.Fprintf(b, "\n_Mapping provenance: Glue migration command → %s._\n\n", glueRef)
	case types.SchemaSourceConfluent:
		fmt.Fprintf(b, "\n_Mapping provenance: Schema Linking eligibility (CP %s+ %s + outbound reachable) → %s (last verified %s)._\n\n",
			cfg.SchemaLinking.MinCPVersion, cfg.SchemaLinking.RequiresCPEdition, cfg.SchemaLinking.Source, cfg.SchemaLinking.LastVerified)
	case types.SchemaSourceConfluentAndGlue:
		fmt.Fprintf(b, "\n_Mapping provenance: Glue migration → %s; Schema Linking eligibility (CP %s+ %s + outbound reachable) → %s (last verified %s)._\n\n",
			glueRef, cfg.SchemaLinking.MinCPVersion, cfg.SchemaLinking.RequiresCPEdition, cfg.SchemaLinking.Source, cfg.SchemaLinking.LastVerified)
	default:
		// Source==None case: no provenance citation applies — the
		// recommendation is purely strategy-driven and the OQ
		// explains the next step.
	}
}

// schemaPathsLabel formats one or more paths in rendering order. Used
// by the "Recommended path(s)" bullet to handle both single- and
// dual-source decisions uniformly. Joiner is " — **also:** " to keep
// the inline-bold labels readable (each label already ends with `**`,
// so a `**also**` joiner would collide asterisks).
func schemaPathsLabel(paths []types.SchemaPath) string {
	if len(paths) == 0 {
		return schemaPathLabel(types.SchemaPathUnknown)
	}
	if len(paths) == 1 {
		return schemaPathLabel(paths[0])
	}
	labels := make([]string, 0, len(paths))
	for _, p := range paths {
		labels = append(labels, schemaPathLabel(p))
	}
	return strings.Join(labels, " — **also:** ")
}

// schemaPathsLabelForContext is the entrypoint used by writeSchema.
// Overrides the generic schemaPathsLabel rendering for context-specific
// cases where the path enum's default text would mislead — currently:
//   - `no_schemas` declared while a SR was scanned: the verdict path
//     is Unknown but the underlying issue is the mismatch, not a
//     missing input. Surface "Conflict" so the reader's eye lands on
//     the contradiction rather than chasing a phantom missing field.
//   - `migrate_existing_schema_registry` declared but the scan found
//     nothing: the customer asserts they have an SR, kcp didn't see
//     one. Don't render "Pending — declare the missing input" (the
//     declaration is already complete); instead point the reader at
//     re-running the scan or correcting the strategy.
func schemaPathsLabelForContext(paths []types.SchemaPath, strategy string, source types.SchemaSource) string {
	if strategy == SchemaStrategyNoSchemas && source != types.SchemaSourceNone {
		return "**Conflict** — strategy declares `no_schemas` but the scan found a Schema Registry; see §Actions Needed for reconciliation"
	}
	if strategy == SchemaStrategyMigrateExistingSchemaRegistry && source == types.SchemaSourceNone {
		return "**Scan gap** — strategy declares `migrate_existing_schema_registry` but no SR was found in the state file; re-run `kcp scan schema-registry` / `kcp scan glue-schema-registry` against the source, OR correct `schema_strategy` if the source genuinely has no registry"
	}
	return schemaPathsLabel(paths)
}

// schemaStrategyDeclaredLabel renders the strategy bullet with a
// status glyph when the declared value conflicts with what the scan
// found, so a reader doesn't have to cross-reference §Schema with
// §Actions Needed to spot the contradiction.
func schemaStrategyDeclaredLabel(strategy string, dec *types.SchemaDecision) string {
	if strategy == "" {
		strategy = SchemaStrategyUnknown
	}
	if strategy == SchemaStrategyNoSchemas && dec.Source != types.SchemaSourceNone {
		return fmt.Sprintf("`%s` ⚠ (scan found a Schema Registry)", strategy)
	}
	if strategy == SchemaStrategyMigrateExistingSchemaRegistry && dec.Source == types.SchemaSourceNone {
		return fmt.Sprintf("`%s` ⚠ (scan found no Schema Registry — re-scan or correct the strategy)", strategy)
	}
	if !knownSchemaStrategy(strategy) && strategy != SchemaStrategyUnknown {
		return fmt.Sprintf("`%s` ⚠ (unrecognised value — see Actions Needed)", strategy)
	}
	return fmt.Sprintf("`%s`", strategy)
}

func schemaSourceLabel(src types.SchemaSource, confluentURLs, glueNames []string) string {
	switch src {
	case types.SchemaSourceConfluent:
		return fmt.Sprintf("Confluent Schema Registry (%d URL%s: %s)",
			len(confluentURLs), pluralize("", len(confluentURLs)), strings.Join(quoteAll(confluentURLs), ", "))
	case types.SchemaSourceGlue:
		return fmt.Sprintf("AWS Glue Schema Registry (%d %s: %s)",
			len(glueNames), pluralize("registry", len(glueNames)), strings.Join(quoteAll(glueNames), ", "))
	case types.SchemaSourceConfluentAndGlue:
		return "Confluent Schema Registry AND AWS Glue Schema Registry (each handled independently below)"
	default:
		return "_none detected_ — neither `kcp scan schema-registry` nor `kcp scan glue-schema-registry` found a registry on the source"
	}
}

func schemaPathLabel(p types.SchemaPath) string {
	switch p {
	case types.SchemaPathSchemaLinking:
		return "**Schema Linking** — source SR pushes schema updates to CC SR over TCP; alternative is manual REST export/import"
	case types.SchemaPathMigrateGlue:
		return "**`kcp create-asset migrate-schemas --glue-registry`** — one Terraform apply imports every schema in the Glue registry into CC SR"
	case types.SchemaPathDeferToAccount:
		return "**Defer to your Confluent account team** — REST API export/import is possible but not deterministic; see §Actions Needed for the failing constraint"
	case types.SchemaPathSchemaless:
		return "**Schemaless** — `schema_strategy: no_schemas` and no source SR was scanned; this section will not render"
	default:
		return "**Pending** — declare the missing input in `plan-inputs.yaml`; see §Actions Needed for the specific field"
	}
}

// writeGluePathCommand emits one canonical `kcp create-asset
// migrate-schemas` invocation with placeholder substitution hints,
// then lists the detected registries (one apply per registry, run
// the same command N times with `--glue-registry` varied).
func writeGluePathCommand(b *bytes.Buffer, glueNames []string) {
	b.WriteString("\n**Glue path — generates Terraform that imports every schema in a registry into CC SR as a `confluent_schema` resource. Run the command once per registry:**\n\n")
	b.WriteString("```bash\nkcp create-asset migrate-schemas \\\n  --glue-registry <registry-name> \\\n  --region <aws-region> \\\n  --cc-sr-rest-endpoint <cc-sr-url>\n```\n\n")
	b.WriteString("Find each placeholder:\n")
	b.WriteString("- `<registry-name>` — one of the detected registries below.\n")
	b.WriteString("- `<aws-region>` — the AWS region the Glue registry lives in (e.g. `us-east-1`).\n")
	b.WriteString("- `<cc-sr-url>` — your CC Schema Registry REST endpoint, e.g. `https://psrc-xxxxx.us-east-1.aws.confluent.cloud`; find on CC Console → Environment → Schema Registry.\n\n")
	fmt.Fprintf(b, "Detected %s: %s.\n\n", pluralize("registry", len(glueNames)), strings.Join(quoteAll(glueNames), ", "))
	b.WriteString("Applying the generated Terraform imports every schema in that registry into CC SR in one apply. **Client-side work remains yours:** swap `AWSKafkaAvroSerializer` for `KafkaAvroSerializer` in producers / consumers, and reconcile any subject-name overlaps with topics that already exist on CC.\n")
}

// writeSchemaLinkingEligibility renders the three-row eligibility
// table for the Confluent SR path. Verdict comes first so the
// reader's eye lands on ✅/❌/❔ before the constraint text — at a
// glance you see whether to read further or fix YAML.
func writeSchemaLinkingEligibility(b *bytes.Buffer, dec *types.SchemaDecision, cfg *PlanConfig) {
	b.WriteString("\n**Schema Linking eligibility (Confluent SR path):** all three rows must be **✅ yes** for the Schema Linking path to apply. Any **❌ no** falls to `defer_to_account_team`; any **❔ unknown** keeps the path at `unknown` until you declare the missing input.\n\n")
	b.WriteString("| Verdict | Constraint | Input |\n")
	b.WriteString("|---|---|---|\n")
	fmt.Fprintf(b, "| %s | Source CP version ≥ %s | `plan-inputs.confluent_sr_cp_version` |\n",
		eligibilityCell(dec.MeetsCPVersionFloor), cfg.SchemaLinking.MinCPVersion)
	fmt.Fprintf(b, "| %s | Source CP edition is `%s` | `plan-inputs.confluent_sr_cp_edition` |\n",
		eligibilityCell(dec.MeetsCPEditionRequirement), cfg.SchemaLinking.RequiresCPEdition)
	fmt.Fprintf(b, "| %s | Source SR can reach CC SR outbound | `plan-inputs.source_sr_outbound_reachable_to_cc` |\n",
		eligibilityCell(dec.SourceSROutboundReachable))
}

// eligibilityCell renders the verdict cell for a tri-state flag (nil
// = unknown, *true = yes, *false = no).
func eligibilityCell(flag *bool) string {
	if flag == nil {
		return "❔ unknown"
	}
	if *flag {
		return "✅ yes"
	}
	return "❌ no"
}

// quoteAll wraps each string in backticks for inline code rendering.
func quoteAll(values []string) []string {
	out := make([]string, 0, len(values))
	for _, v := range values {
		out = append(out, "`"+v+"`")
	}
	return out
}

// ----- §red flags -----

// writeRedFlags renders the fleet-wide §Red Flags section.
// Triggered rows lead the section with their evidence; NotTriggered
// and Unknown rows collapse into a tail summary (count + comma list)
// so the customer can scan triggered items in one screenful even on
// a 15-row table.
func writeRedFlags(b *bytes.Buffer, rf *types.RedFlagsSection, section int) {
	if rf == nil || len(rf.Rows) == 0 {
		return
	}
	fmt.Fprintf(b, "## %d. Red Flags\n\n", section)
	b.WriteString("Items below aren't blockers — they're things to discuss with your Confluent SE. Each row's evidence is the field path + value from the scan so the conversation grounds in scan facts, not inference.\n\n")
	var triggered, unknown, notTriggered []types.RedFlag
	for _, r := range rf.Rows {
		switch r.Status {
		case types.RedFlagTriggered:
			triggered = append(triggered, r)
		case types.RedFlagUnknown:
			unknown = append(unknown, r)
		default:
			notTriggered = append(notTriggered, r)
		}
	}
	if len(triggered) > 0 {
		b.WriteString("### Triggered\n\n")
		for _, r := range triggered {
			fmt.Fprintf(b, "- 🔴 **%s**\n", r.Title)
			if r.Evidence != "" {
				fmt.Fprintf(b, "  - _Evidence:_ %s\n", r.Evidence)
			}
		}
		b.WriteString("\n")
	}
	if len(unknown) > 0 {
		b.WriteString("### Not scanned (declare in `plan-inputs.yaml` or re-run the scanner)\n\n")
		for _, r := range unknown {
			fmt.Fprintf(b, "- ❔ **%s**\n", r.Title)
			if r.Evidence != "" {
				fmt.Fprintf(b, "  - _Evidence:_ %s\n", r.Evidence)
			}
		}
		b.WriteString("\n")
	}
	if len(notTriggered) > 0 {
		labels := make([]string, 0, len(notTriggered))
		for _, r := range notTriggered {
			labels = append(labels, r.Title)
		}
		// Inline comma list reads fine for short tails; once the
		// list outgrows the screen (≥ 6 items) hide it behind
		// <details> so the rendered Plan stays scannable.
		const inlineThreshold = 5
		if len(labels) <= inlineThreshold {
			fmt.Fprintf(b, "_Not triggered (%d): %s._\n\n", len(labels), strings.Join(labels, "; "))
		} else {
			fmt.Fprintf(b, "<details><summary><em>Not triggered (%d) — expand</em></summary>\n\n", len(labels))
			for _, l := range labels {
				fmt.Fprintf(b, "- %s\n", l)
			}
			b.WriteString("\n</details>\n\n")
		}
	}
}

// ----- §effort signals -----

// writeEffortSignals renders the fleet-wide list of quantitative
// effort inputs. The customer's PM uses these counts (combined with
// their team's velocity) to scope days of work — kcp doesn't ship a
// day-count estimate itself.
//
// Rendering rules:
//   - When every signal is zero, collapse to a one-liner. The full
//     table-of-zeros + caveat block was noise — most scenarios hit
//     that case and the reader's eyes glaze.
//   - Non-zero counts render in a table; caveats sit inline as
//     superscript-referenced footnotes only for rows that have them.
//     Verbose label-repeating bullets dropped.
//   - Structurally-unobservable zero (e.g. IAM client count when the
//     client-inventory scan didn't run) renders as `_unknown_`
//     instead of `0` so the customer doesn't read it as "no work".
func writeEffortSignals(b *bytes.Buffer, es *types.EffortSignalsSection, section int) {
	if es == nil || len(es.Signals) == 0 {
		return
	}
	fmt.Fprintf(b, "## %d. Effort Signals\n\n", section)
	b.WriteString("Quantitative inputs your PM consumes to scope migration effort. kcp doesn't ship a day-count — these counts plus your team's velocity produce the estimate.\n\n")
	// "All-zero" carve-out: only collapse when EVERY signal has a
	// concrete Count of 0 (the scan ran AND returned zero). A `nil`
	// Count means "scan didn't run / unobservable" — that's a
	// surface-worthy state, not a collapse trigger. Without this
	// carve-out the IAM-unknown signal silently disappeared.
	allConcreteZero := true
	for _, s := range es.Signals {
		if s.Count == nil || *s.Count > 0 {
			allConcreteZero = false
			break
		}
	}
	if allConcreteZero {
		b.WriteString("_No effort signals triggered — every signal returned zero. (Pattern-free topic names can also produce zero counts; if you have IAM clients, Connect fleets, or Glue schemas you'd expect to see, re-run the matching `kcp scan ...` subcommand.)_\n\n")
		return
	}
	// Footnote markers are assigned by ROW POSITION, not by "has a
	// note today". This keeps marker numbers stable across plans
	// even when some rows are unknown vs zero — a reader comparing
	// two plans sees the same signal referenced by the same `¹` /
	// `²` regardless of state. Rows without a note get no marker.
	footnoteFor := make([]int, len(es.Signals))
	footnoteForIdx := 0
	for i, s := range es.Signals {
		footnoteForIdx++
		if s.Note == "" {
			footnoteFor[i] = 0
			continue
		}
		footnoteFor[i] = footnoteForIdx
	}
	b.WriteString("| Count | Signal |\n")
	b.WriteString("|---:|---|\n")
	for i, s := range es.Signals {
		label := s.Label
		if footnoteFor[i] > 0 {
			label += effortSignalSuperscript(footnoteFor[i])
		}
		fmt.Fprintf(b, "| %s | %s |\n", effortSignalCountCell(s), label)
	}
	b.WriteString("\n")
	hasAnyNote := false
	for _, s := range es.Signals {
		if s.Note != "" {
			hasAnyNote = true
			break
		}
	}
	if hasAnyNote {
		b.WriteString("**Notes:**\n\n")
		for i, s := range es.Signals {
			if s.Note == "" {
				continue
			}
			fmt.Fprintf(b, "%s %s\n", effortSignalSuperscript(footnoteFor[i]), s.Note)
		}
		b.WriteString("\n")
	}
}

// effortSignalCountCell renders the count column. `nil` Count is the
// signal's structurally-unobservable state (the upstream scan didn't
// run); render it as `_unknown_` so the customer doesn't read it as
// "no work". Concrete 0 stays as `0` — the scan ran and returned
// zero hits.
func effortSignalCountCell(s types.EffortSignal) string {
	if s.Count == nil {
		return "_unknown_"
	}
	return fmt.Sprintf("%d", *s.Count)
}

// effortSignalSuperscript returns the Unicode superscript glyph for
// n in 1..9. Falls back to `^n` for n > 9.
func effortSignalSuperscript(n int) string {
	if n < 1 || n > 9 {
		return fmt.Sprintf("^%d", n)
	}
	return []string{"¹", "²", "³", "⁴", "⁵", "⁶", "⁷", "⁸", "⁹"}[n-1]
}

// ----- §tiered storage -----

// writeTieredStorage renders the per-cluster tiered-storage view +
// the three-dimension trade-off table. Customer-decision shaped: no
// dollar estimate, no recommendation — kcp surfaces the mechanism /
// duration / cost-direction so the customer (and account team) can
// decide whether the cold data is worth re-fetching.
func writeTieredStorage(b *bytes.Buffer, ts *types.TieredStorageSection, section int) {
	if ts == nil || len(ts.Clusters) == 0 {
		return
	}
	fmt.Fprintf(b, "## %d. Tiered Storage\n\n", section)
	b.WriteString("Cluster Linking does **not** carry historical tiered data forward. This section names the three dimensions of the trade-off (mechanism / duration / cost direction) so you can decide whether the cold data is worth re-fetching.\n\n")

	// Per-cluster header
	b.WriteString("**Clusters with tiered storage enabled:**\n\n")
	b.WriteString("| Cluster | Storage mode | Remote log size (peak) |\n")
	b.WriteString("|---|---|---:|\n")
	missingMetric := false
	for _, c := range ts.Clusters {
		if c.RemoteLogSizeBytes <= 0 {
			missingMetric = true
		}
		fmt.Fprintf(b, "| %s | `%s` | %s |\n", c.ClusterID, c.StorageMode, formatBytesHuman(c.RemoteLogSizeBytes))
	}
	if missingMetric {
		b.WriteString("\n_`_not collected_` means CloudWatch's `RemoteLogSizeBytes` aggregate is absent from the state file's metric window — re-run `kcp discover` with metrics enabled to populate it. The Trade-off framing below applies regardless._\n")
	}
	b.WriteString("\n")

	// Three dimensions as a bullet list — matches §Cutover's rhythm.
	// A 3-row "Dimension / What it is" table was visually heavier
	// than the content warranted (the rows are a glossary, not a
	// comparison).
	b.WriteString("**Three dimensions of the trade-off:**\n\n")
	b.WriteString("- **Mechanism — S3 re-fetch.** Backfilling means re-reading from MSK tiered storage (S3) and re-publishing into CC. Cluster Linking does not carry historical tiered data forward; some external tool (or extended dual-run) re-fetches it.\n")
	b.WriteString("- **Duration — backfill time.** Time-to-complete is a function of GB volume and ingest rate. Large historical volumes can take days or weeks to backfill.\n")
	b.WriteString("- **Cost direction — backfill $.** S3 GET / data-transfer-out charges on MSK, plus extra CC ingest. kcp does not estimate dollars; pull the AWS unit prices from your account team.\n")
	b.WriteString("\n")

	// Customer declarations
	fmt.Fprintf(b, "- **Consumer history requirement (declared):** `%s`\n", ts.ConsumerHistoryRequirement)
	strategy := ts.HistoricalDataStrategy
	if strategy == "" {
		fmt.Fprintf(b, "- **Historical data strategy:** _undeclared — see §Actions Needed_\n")
	} else {
		fmt.Fprintf(b, "- **Historical data strategy:** `%s`\n", strategy)
	}
	b.WriteString("\n")
}

// formatBytesHuman renders a byte count as KB / MB / GB / TB with a
// single decimal. Returns `_not collected_` when v is zero — the
// CloudWatch metric was not present in the state file.
func formatBytesHuman(v float64) string {
	if v <= 0 {
		return "_not collected_"
	}
	const (
		kb = 1024.0
		mb = kb * 1024
		gb = mb * 1024
		tb = gb * 1024
	)
	switch {
	case v >= tb:
		return fmt.Sprintf("%.1f TB", v/tb)
	case v >= gb:
		return fmt.Sprintf("%.1f GB", v/gb)
	case v >= mb:
		return fmt.Sprintf("%.1f MB", v/mb)
	default:
		return fmt.Sprintf("%.1f KB", v/kb)
	}
}

// ----- §cost reconciliation -----

// writeCostReconciliation renders the per-region table of MSK
// instance types billed by AWS but NOT discovered by `kcp discover`.
// No materiality threshold — the customer (FinOps / cloud lead /
// platform engineer) decides which candidates are "real" hidden
// clusters vs. decommissioned-but-still-billed ones.
func writeCostReconciliation(b *bytes.Buffer, cr *types.CostReconciliationSection, section int) {
	if cr == nil || len(cr.Candidates) == 0 {
		return
	}
	fmt.Fprintf(b, "## %d. Cost vs Inventory Reconciliation\n\n", section)
	b.WriteString("MSK instance types that show up in the AWS cost report but were NOT discovered by `kcp discover`. Sorted by spend descending — no materiality threshold; the customer (FinOps / cloud lead) decides what's real vs. decommissioned-but-still-billed.\n\n")
	b.WriteString("| Region | Instance type | Total spend (USD) | Billing window (months observed / days observed) |\n")
	b.WriteString("|---|---|---:|---:|\n")
	for _, c := range cr.Candidates {
		fmt.Fprintf(b, "| %s | `%s` | $%s | %d / %d |\n",
			c.Region, c.InstanceType, formatUSDWithCommas(c.TotalSpend), c.MonthsObserved, c.DaysObserved)
	}
	b.WriteString("\n")
	b.WriteString("_Cross-reference each candidate with your AWS console; common causes: a cluster intentionally excluded from `kcp discover` scope, a decommissioned cluster still showing up on the bill, or a cross-account cluster the scanner's IAM role can't see._\n\n")
}

// formatUSDWithCommas renders a dollar amount with thousands
// separators and 2 decimal places (`1234567.89` → `1,234,567.89`).
// Keeps amounts in §Cost Reconciliation scannable at a glance.
//
// Two non-obvious cases the old implementation got wrong:
//   - **Negative values in (-1, 0)** (e.g. AWS credit of $-0.99):
//     `int64(-0.99) == 0` and `"0"` has len ≤ 3, so the
//     neg-prefix branch was skipped. Tracking `neg` from the float
//     itself fixes this.
//   - **Fractional carry** (e.g. `v = 1.999`): rounding the fraction
//     separately to `100` cents produced `"1.100"`. Rounding the
//     whole value with `math.Round(v*100)` first, then splitting
//     into whole + cents, avoids the carry hole.
func formatUSDWithCommas(v float64) string {
	neg := v < 0
	abs := v
	if neg {
		abs = -abs
	}
	cents := int64(math.Round(abs * 100))
	whole := cents / 100
	frac := cents % 100
	wholeStr := fmt.Sprintf("%d", whole)
	// Insert commas every 3 digits from the right when the whole part
	// is large enough to need them.
	if len(wholeStr) > 3 {
		var grouped []string
		for i := len(wholeStr); i > 0; i -= 3 {
			lo := i - 3
			if lo < 0 {
				lo = 0
			}
			grouped = append([]string{wholeStr[lo:i]}, grouped...)
		}
		wholeStr = strings.Join(grouped, ",")
	}
	if neg {
		wholeStr = "-" + wholeStr
	}
	return fmt.Sprintf("%s.%02d", wholeStr, frac)
}
