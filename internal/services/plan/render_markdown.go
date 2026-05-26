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
	// `from <path>` clause is omitted when the path is empty — library
	// callers (pkg/lib) pass bytes, not a file path; only the CLI
	// `kcp report plan --state-file ...` populates this field.
	fromClause := ""
	if p.Header.StateFilePath != "" {
		fromClause = fmt.Sprintf(" from `%s`", p.Header.StateFilePath)
	}
	fmt.Fprintf(&b, "_Generated %s by KCP %s%s._\n\n", p.Header.GeneratedAt.Format("2006-01-02 15:04:05 UTC"), p.Header.KCPVersion, fromClause)

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
	for i, oq := range p.OpenQuestions {
		title := oq.Title
		if oq.ClusterID != "" {
			title = fmt.Sprintf("`%s` — %s", oq.ClusterID, title)
		}
		fmt.Fprintf(b, "%d. **%s**\n", i+1, title)
		if oq.Body != "" {
			fmt.Fprintf(b, "   - %s\n", oq.Body)
		}
		if oq.HowToClose != "" {
			fmt.Fprintf(b, "   - _How to close:_ %s\n", oq.HowToClose)
		}
	}
	b.WriteString("\n")
}

func writeDefinitions(b *bytes.Buffer, cfg *PlanConfig) {
	caps := cfg.EnterpriseCaps
	b.WriteString("## Definitions\n\n")
	fmt.Fprintf(b, "- **eCKU** — Elastic Confluent Kafka Unit, the throughput unit on Enterprise clusters. %d MB/s ingress + %d MB/s egress per eCKU at the per-eCKU caps used below.\n", caps.PerECKUIngressMBps, caps.PerECKUEgressMBps)
	b.WriteString("- **CKU** — Confluent Kafka Unit, the Dedicated-tier equivalent of eCKU. Sizing math is the same; only the unit name changes. Dedicated clusters always render with `CKU`.\n")
	b.WriteString("- **P95** — the 95th-percentile sustained throughput observed in the metrics window. Sizing uses P95 (override with `sizing_percentile`) so a transient spike doesn't permanently inflate the recommended cluster size.\n")
	b.WriteString("- **Final size** — the recommended eCKU (Enterprise) or CKU (Dedicated) count for the cluster. `(floor)` next to a value means the SLA minimum was binding (the math came in below the floor and was rounded up).\n")
	fmt.Fprintf(b, "- **PrivateLink** — single-AZ private connectivity, up to %d eCKU. The default network when the cluster fits inside it with headroom. Enterprise only.\n", caps.PrivateLinkMaxECKU)
	fmt.Fprintf(b, "- **PNI** (Private Network Interface) — multi-AZ private connectivity, up to %d eCKU. Used when PrivateLink's cap is too close to the cluster's peak burst; **always required on Dedicated**.\n\n", caps.PNIMaxECKU)
}

func writeSourceEnvironment(b *bytes.Buffer, p *types.Plan) {
	b.WriteString("## 1. Source Environment\n\n")
	if len(p.SourceEnvironment.Clusters) == 0 {
		b.WriteString("_No clusters found in the state file. Re-run `kcp discover` / `kcp scan ...` and try again._\n\n")
		return
	}
	totalBrokers := 0
	totalTopics := 0
	for _, c := range p.SourceEnvironment.Clusters {
		totalBrokers += c.BrokerCount
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
	fmt.Fprintf(b, "- **%d** source %s across **%d** %s · **%d** brokers · **%d** topics\n\n",
		len(p.SourceEnvironment.Clusters), clusterWord, p.SourceEnvironment.TotalRegions, regionWord, totalBrokers, totalTopics)
	b.WriteString("| Cluster | Region | Brokers | Topics |\n")
	b.WriteString("|---|---|---:|---:|\n")
	for _, c := range p.SourceEnvironment.Clusters {
		fmt.Fprintf(b, "| %s | %s | %d | %d |\n", c.ClusterID, c.Region, c.BrokerCount, c.TopicCount)
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
	for _, s := range p.Sizing {
		if s.Degraded {
			// Symptom-only here; the action lives in Actions Needed
			// (so it isn't duplicated across two surfaces).
			fmt.Fprintf(b, "- **%s** — metrics missing (%s). Final eCKU defaults to the SLA floor (%d). See Actions Needed below.\n", s.ClusterID, s.DegradedReason, s.FinalECKU)
			continue
		}
		var pieces []string
		var customerDeclaredTriggers []string
		if ct, ok := ctByID[s.ClusterID]; ok {
			ctLabel := clusterTypeLabel(ct)
			if len(ct.Triggers) == 0 {
				pieces = append(pieces, fmt.Sprintf("Cluster type **%s** — no hard-limit rule fired", ctLabel))
			} else {
				rules := make([]string, 0, len(ct.Triggers))
				for _, t := range ct.Triggers {
					rules = append(rules, fmt.Sprintf("%s (%s)", t.Description, t.Evidence))
					if t.CustomerDeclared {
						customerDeclaredTriggers = append(customerDeclaredTriggers, t.RowID)
					}
				}
				pieces = append(pieces, fmt.Sprintf("Cluster type **%s** — %s", ctLabel, strings.Join(rules, "; ")))
			}
		}
		if n, ok := netByID[s.ClusterID]; ok {
			pieces = append(pieces, fmt.Sprintf("Networking **%s** — %s", n.Verdict, n.Reason))
		}
		fmt.Fprintf(b, "- **%s** — %s.\n", s.ClusterID, strings.Join(pieces, ". "))
		if len(customerDeclaredTriggers) > 0 {
			fmt.Fprintf(b, costCalloutTemplate, strings.Join(customerDeclaredTriggers, ", "))
		}
		// Spiky-workload note (FYI only — sizing already absorbs the spike).
		if s.SpikyIngress || s.SpikyEgress {
			fmt.Fprintf(b, "  - _Note: %s. Sizing is P95-based and Enterprise elasticity absorbs the spike; set `sizing_percentile: p99` in `plan-inputs.yaml` if you'd rather size to the peak._\n", spikyDescription(s))
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
		return fmt.Sprintf("%s peak %.0f MB/s (no P95 baseline)", dir, peak)
	}
	return fmt.Sprintf("%s peak %.0f MB/s vs P95 %.0f MB/s (%.1fx)", dir, peak, p95, peak/p95)
}

func writeSizingAppendix(b *bytes.Buffer, p *types.Plan, cfg *PlanConfig) {
	if len(p.SizingAppendix) == 0 {
		return
	}
	partitionCap := cfg.EnterpriseCaps.PerECKUPartitionRate
	b.WriteString("## Appendix A1 — Sizing Math\n")
	b.WriteString("<details><summary>Show sizing math per cluster</summary>\n\n")
	b.WriteString("Each cluster is sized by taking the largest of its three throughput-vs-cap ratios (ingress, egress, partitions) and scaling that by `(1 + headroom)`, so the recommended eCKU has spare capacity above the observed P95. SLA floor binds when the math comes in below the minimum eCKU for the target SLA.\n\n")
	// The formula is identical for every cluster (caps + headroom are
	// constants, not per-cluster). Print it once, then a single audit
	// table covers every cluster in a row.
	fmt.Fprintf(b, "Formula: `%s`\n\n", p.SizingAppendix[0].Formula)
	b.WriteString("| Cluster | Ingress ratio | Egress ratio | Partition ratio | Max (driver) | Sized eCKU | SLA floor | Final eCKU |\n")
	b.WriteString("|---|---:|---:|---:|---|---:|---:|---:|\n")
	for _, s := range p.Sizing {
		if s.Degraded {
			fmt.Fprintf(b, "| %s | _degraded_ | _degraded_ | %d / %d | _n/a_ | _n/a_ | %d | %d |\n",
				s.ClusterID, s.UserPartitions, partitionCap, s.SLAFloorECKU, s.FinalECKU)
			continue
		}
		fmt.Fprintf(b, "| %s | %.4f | %.4f | %.4f | **%.4f** (%s) | %d | %d | %d |\n",
			s.ClusterID, s.IngressRatio, s.EgressRatio, s.PartitionRatio, s.MaxRatio, s.MaxRatioDriver, s.SizedECKU, s.SLAFloorECKU, s.FinalECKU)
	}
	b.WriteString("\nMax-ratio is multiplied by `(1 + headroom)` and rounded up to get `Sized eCKU`. `Final eCKU` is `max(Sized, SLA floor)`.\n\n")
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
