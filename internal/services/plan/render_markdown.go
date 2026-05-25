package plan

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/confluentinc/kcp/internal/types"
)

// RenderMarkdown emits a human-readable Plan: Source Environment table,
// Sizing & Cluster Decisions table with full rationale, collapsed
// Appendix A1 with the sizing math expansion. MVP scope — no auth /
// switchover / red flag sections (each lands with its own follow-up).
//
// cfg is read for product-fact numbers in the Definitions block and for
// the partition cap rendered in the appendix; pass the same PlanConfig
// the PlanService used to build the plan.
func RenderMarkdown(p *types.Plan, cfg *PlanConfig) ([]byte, error) {
	var b bytes.Buffer

	fmt.Fprintf(&b, "# Migration Plan — %s → Confluent Cloud\n\n", p.Header.Source)
	schemaSuffix := ""
	if p.Header.PlanSchemaVersion != "" {
		schemaSuffix = fmt.Sprintf(" · plan schema `%s`", p.Header.PlanSchemaVersion)
	}
	fmt.Fprintf(&b, "_Generated %s by KCP %s from `%s`%s._\n\n", p.Header.GeneratedAt.Format("2006-01-02 15:04:05 UTC"), p.Header.KCPVersion, p.Header.StateFilePath, schemaSuffix)

	writeDefinitions(&b, cfg)
	writeSourceEnvironment(&b, p)
	writeSizingAndDecisions(&b, p)
	writeOpenQuestions(&b, p)
	writeSizingAppendix(&b, p, cfg)

	return b.Bytes(), nil
}

func writeOpenQuestions(b *bytes.Buffer, p *types.Plan) {
	if len(p.OpenQuestions) == 0 {
		return
	}
	b.WriteString("## 3. Actions Needed\n\n")
	b.WriteString("Each item below is a concrete action that tightens the recommendation in **Sizing & Cluster Decisions**. The current recommendation stands; doing these closes a state-file or scan gap.\n\n")

	// Group per-cluster OQs by (ID, Body, HowToClose) so only items with
	// identical rendered content collapse into a single Action with an
	// `Affects:` line. Two clusters in different regions, for example,
	// render different `--region <region>` commands in HowToClose — they
	// must NOT merge. Plan-level OQs (empty ClusterID) live in their own
	// group keyed by title. Group order follows the first-seen position
	// in p.OpenQuestions, which is already priority-sorted.
	type group struct {
		oq       types.OpenQuestion
		clusters []string
	}
	groups := make([]*group, 0, len(p.OpenQuestions))
	byKey := make(map[string]*group, len(p.OpenQuestions))
	for _, oq := range p.OpenQuestions {
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
		g, ok := byKey[key]
		if !ok {
			g = &group{oq: oq}
			byKey[key] = g
			groups = append(groups, g)
		}
		if oq.ClusterID != "" {
			g.clusters = append(g.clusters, oq.ClusterID)
		}
	}

	for i, g := range groups {
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
	b.WriteString("- **Enterprise / Dedicated** — Confluent Cloud cluster tiers. Enterprise has elastic billing per eCKU; Dedicated is fixed-provisioned per CKU. **Dedicated Multi-Zone (MZ)** is the default topology when Dedicated is selected; **Dedicated Single-Zone (SZ)** fires only when `requires_99_95_sla_within_a_single_zone: true` is set in `plan-inputs.yaml`.\n")
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
	regionWord := "region"
	if p.SourceEnvironment.TotalRegions != 1 {
		regionWord = "regions"
	}
	clusterWord := "cluster"
	if len(p.SourceEnvironment.Clusters) != 1 {
		clusterWord = "clusters"
	}
	serverlessNote := ""
	if serverlessCount > 0 {
		clusterWord := "cluster"
		if serverlessCount != 1 {
			clusterWord = "clusters"
		}
		serverlessNote = fmt.Sprintf(" (incl. %d MSK Serverless %s — no broker count by design)", serverlessCount, clusterWord)
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
		fmt.Fprintf(b, "| %s | %s | %s | %d |\n", c.ClusterID, c.Region, brokerCell, c.TopicCount)
	}
	b.WriteString("\n")
}

func writeSizingAndDecisions(b *bytes.Buffer, p *types.Plan) {
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
		if s.ScanIncomplete {
			// Don't ship a confident verdict against an incomplete scan —
			// render every downstream column as deferred.
			fmt.Fprintf(b, "| %s | _scan incomplete_ | _unknown_ | _deferred_ | _deferred_ | _deferred_ |\n", s.ClusterID)
			continue
		}
		if s.Degraded {
			fmt.Fprintf(b, "| %s | _metrics missing_ | %d | %s | %s | %s |\n",
				s.ClusterID, s.UserPartitions, sizeCell, ctLabel, net)
			continue
		}
		fmt.Fprintf(b, "| %s | %.1f / %.1f | %d | %s | %s | %s |\n",
			s.ClusterID, s.SizedInMBps, s.SizedOutMBps, s.UserPartitions, sizeCell, ctLabel, net)
	}
	b.WriteString("\n")

	// Per-cluster rationale: each line cites the cluster-type decision and
	// the networking decision. Reads cleanly even for 30+ clusters because
	// each entry is one or two lines.
	b.WriteString("### Why These Recommendations\n\n")
	srcByID := make(map[string]types.SourceClusterSummary, len(p.SourceEnvironment.Clusters))
	for _, sc := range p.SourceEnvironment.Clusters {
		srcByID[sc.ClusterID] = sc
	}
	for _, s := range p.Sizing {
		ct := ctByID[s.ClusterID]
		unit := finalSizeUnit(ct)
		src := srcByID[s.ClusterID]
		if s.ScanIncomplete {
			fmt.Fprintf(b, "- **%s** — sizing **deferred**: source scan didn't populate topics or ACLs, so the downstream cluster-type and networking verdicts aren't trustworthy yet. See Actions Needed below.\n", s.ClusterID)
			continue
		}
		if s.Degraded {
			// Symptom-only here; the action lives in Actions Needed
			// (so it isn't duplicated across two surfaces).
			if src.IsServerless {
				fmt.Fprintf(b, "- **%s** — **MSK Serverless source**; broker count is N/A by design and the CloudWatch metrics path differs from Provisioned. Final %s defaults to the SLA floor (%d) until throughput is supplied. See Actions Needed below.\n", s.ClusterID, unit, s.FinalECKU)
			} else {
				fmt.Fprintf(b, "- **%s** — metrics missing (%s). Final %s defaults to the SLA floor (%d). See Actions Needed below.\n", s.ClusterID, s.DegradedReason, unit, s.FinalECKU)
			}
			continue
		}
		var pieces []string
		var customerDeclaredTriggers []string
		if len(ct.Triggers) == 0 {
			pieces = append(pieces, fmt.Sprintf("Cluster type **%s** — no hard-limit rule fired", clusterTypeLabel(ct)))
		} else {
			rules := make([]string, 0, len(ct.Triggers))
			for _, t := range ct.Triggers {
				rules = append(rules, fmt.Sprintf("%s (%s)", t.Description, t.Evidence))
				if t.CustomerDeclared {
					customerDeclaredTriggers = append(customerDeclaredTriggers, t.RowID)
				}
			}
			pieces = append(pieces, fmt.Sprintf("Cluster type **%s** — %s", clusterTypeLabel(ct), strings.Join(rules, "; ")))
		}
		if n, ok := netByID[s.ClusterID]; ok {
			pieces = append(pieces, fmt.Sprintf("Networking **%s** — %s", n.Verdict, n.Reason))
		}
		fmt.Fprintf(b, "- **%s** — %s.\n", s.ClusterID, strings.Join(pieces, ". "))
		if len(customerDeclaredTriggers) > 0 {
			fmt.Fprintf(b, costCalloutTemplate, strings.Join(customerDeclaredTriggers, ", "))
		}
		// Single-Zone resilience tradeoff: the 99.95% SZ SLA verdict puts the
		// cluster in one AZ. Customers reading "99.95% SLA" can misread it as
		// "more available" without noticing the single-AZ failure-domain
		// cost, so surface that tradeoff inline.
		if ct.Verdict == types.ClusterTypeDedicated && ct.Topology == types.TopologySingleZone {
			b.WriteString("  - > ℹ **Single-Zone resilience tradeoff:** the 99.95% SLA selected here is a same-AZ failure-domain choice. An AZ-wide outage takes the cluster offline. Multi-Zone (99.99% SLA, ≥2 CKU) is the resilience-first alternative.\n")
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
	fmt.Fprintf(b, "Each cluster is sized by taking the largest of its three throughput-vs-cap ratios (ingress, egress, partitions) and scaling that by `(1 + headroom)`, so the recommended size has spare capacity above the observed %s. Headroom for this run is `%.2f` (override via `headroom_fraction` in `plan-inputs.yaml`). SLA floor binds when the math comes in below the minimum eCKU for the target SLA (1 eCKU for 99.9, 2 eCKU for 99.99 — published in the Confluent Cloud cluster-types SLA table). The `Sized` and `Final` columns are in eCKU on Enterprise and CKU on Dedicated.\n\n", percentileHeader(p.Inputs.SizingPercentile), p.Inputs.HeadroomFraction)
	// The formula is identical for every cluster (caps + headroom are
	// constants, not per-cluster). Print it once, then a single audit
	// table covers every cluster in a row.
	fmt.Fprintf(b, "Formula: `%s`\n\n", p.SizingAppendix[0].Formula)
	b.WriteString("| Cluster | Ingress ratio | Egress ratio | Partition ratio | Max (driver) | Sized | SLA floor | Final |\n")
	b.WriteString("|---|---:|---:|---:|---|---:|---:|---:|\n")
	for _, s := range p.Sizing {
		if s.ScanIncomplete {
			fmt.Fprintf(b, "| %s | _scan incomplete_ | _scan incomplete_ | _unknown_ | _n/a_ | _deferred_ | %d | _deferred_ |\n",
				s.ClusterID, s.SLAFloorECKU)
			continue
		}
		if s.Degraded {
			fmt.Fprintf(b, "| %s | _degraded_ | _degraded_ | %d / %d | _n/a_ | _n/a_ | %d | %d |\n",
				s.ClusterID, s.UserPartitions, partitionCap, s.SLAFloorECKU, s.FinalECKU)
			continue
		}
		fmt.Fprintf(b, "| %s | %.4f | %.4f | %.4f | **%.4f** (%s) | %d | %d | %d |\n",
			s.ClusterID, s.IngressRatio, s.EgressRatio, s.PartitionRatio, s.MaxRatio, s.MaxRatioDriver, s.SizedECKU, s.SLAFloorECKU, s.FinalECKU)
	}
	b.WriteString("\nMax-ratio is multiplied by `(1 + headroom)` and rounded up to get `Sized`. `Final` is `max(Sized, SLA floor)`.\n\n")
	b.WriteString("</details>\n\n")
}

// costCalloutTemplate surfaces a ⚠ warning when a customer-declared
// flag flipped the cluster to Dedicated — a wrong `true` costs 5–10×
// monthly, so the callout names the rules and the recovery path.
const costCalloutTemplate = "  - > ⚠ **Cost callout:** Dedicated was forced by customer-declared `plan-inputs.yaml` flag(s) — %s. Dedicated costs ~5–10× Enterprise per month for an equivalent footprint. If a flag was set in error, flip it to `false` and re-run; confirm with your Confluent account team if unsure.\n"

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
