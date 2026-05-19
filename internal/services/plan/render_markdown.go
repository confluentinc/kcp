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
	fmt.Fprintf(&b, "_Generated %s by KCP %s from `%s`._\n\n", p.Header.GeneratedAt.Format("2006-01-02 15:04:05 UTC"), p.Header.KCPVersion, p.Header.StateFilePath)

	writeDefinitions(&b, cfg)
	writeSourceEnvironment(&b, p)
	writeSizingAndDecisions(&b, p)
	writeSizingAppendix(&b, p, cfg)

	return b.Bytes(), nil
}

func writeDefinitions(b *bytes.Buffer, cfg *PlanConfig) {
	caps := cfg.EnterpriseCaps
	b.WriteString("## Definitions\n\n")
	fmt.Fprintf(b, "- **eCKU** — Elastic Confluent Kafka Unit, the throughput unit on Enterprise clusters. %d MB/s ingress + %d MB/s egress per eCKU at the per-eCKU caps used below.\n", caps.PerECKUIngressMBps, caps.PerECKUEgressMBps)
	b.WriteString("- **P95** — the 95th-percentile sustained throughput observed in the metrics window. Sizing uses P95 (override with `sizing_percentile`) so a transient spike doesn't permanently inflate the recommended cluster size.\n")
	fmt.Fprintf(b, "- **PrivateLink** — single-AZ private connectivity, up to %d eCKU. The default network when the cluster fits inside it with headroom.\n", caps.PrivateLinkMaxECKU)
	fmt.Fprintf(b, "- **PNI** (Private Network Interface) — multi-AZ private connectivity, up to %d eCKU. Used when PrivateLink's cap is too close to the cluster's peak burst.\n\n", caps.PNIMaxECKU)
}

func writeSourceEnvironment(b *bytes.Buffer, p *types.Plan) {
	b.WriteString("## 1. Source Environment\n\n")
	if len(p.SourceEnvironment.Clusters) == 0 {
		b.WriteString("_No clusters found in the state file. Re-run `kcp discover` / `kcp scan ...` and try again._\n\n")
		return
	}
	fmt.Fprintf(b, "- **%d** source clusters across **%d** region(s)\n\n", len(p.SourceEnvironment.Clusters), p.SourceEnvironment.TotalRegions)
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

	b.WriteString("| Cluster | P95 in / out (MBps) | Partitions | Final size | Cluster Type | Networking |\n")
	b.WriteString("|---|---|---:|---:|---|---|\n")
	for i, s := range p.Sizing {
		var ctDecision types.ClusterTypeDecision
		net := types.Networking("")
		if i < len(p.ClusterTypeDecision) {
			ctDecision = p.ClusterTypeDecision[i]
		}
		if i < len(p.NetworkingDecision) {
			net = p.NetworkingDecision[i].Verdict
		}
		ctLabel := clusterTypeLabel(ctDecision)
		sizeCell := formatSizeCell(s.FinalECKU, ctDecision, s.Degraded)
		if s.Degraded {
			fmt.Fprintf(b, "| %s | _sizing degraded_ | %d | %s | %s | %s |\n",
				s.ClusterID, s.UserPartitions, sizeCell, ctLabel, net)
			continue
		}
		fmt.Fprintf(b, "| %s | %.1f / %.1f | %d | %s | %s | %s |\n",
			s.ClusterID, s.P95InMBps, s.P95OutMBps, s.UserPartitions, sizeCell, ctLabel, net)
	}
	b.WriteString("\n")

	// Per-cluster rationale: each line cites the cluster-type decision and
	// the networking decision. Reads cleanly even for 30+ clusters because
	// each entry is one or two lines.
	b.WriteString("### Why these recommendations\n\n")
	for i, s := range p.Sizing {
		if s.Degraded {
			fmt.Fprintf(b, "- **%s** — %s. Final eCKU defaults to the SLA floor (%d). Re-run with metrics to lock in throughput-based sizing.\n", s.ClusterID, s.DegradedReason, s.FinalECKU)
			continue
		}
		var pieces []string
		var customerDeclaredTriggers []string
		if i < len(p.ClusterTypeDecision) {
			ct := p.ClusterTypeDecision[i]
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
		if i < len(p.NetworkingDecision) {
			n := p.NetworkingDecision[i]
			pieces = append(pieces, fmt.Sprintf("Networking **%s** — %s", n.Verdict, n.Reason))
		}
		fmt.Fprintf(b, "- **%s** — %s.\n", s.ClusterID, strings.Join(pieces, ". "))
		// Cost callout: a customer-declared plan-input flipped this cluster
		// to Dedicated. Surface the wrong-click cost (5–10× monthly) so the
		// customer reviews before committing.
		if len(customerDeclaredTriggers) > 0 {
			fmt.Fprintf(b, "  - > ⚠ **Cost callout:** Dedicated was forced by customer-declared `plan-inputs.yaml` flag(s) — %s. Dedicated costs ~5–10× Enterprise per month for an equivalent footprint. If a flag was set in error, flip it to `false` and re-run; confirm with your Confluent account team if unsure.\n", strings.Join(customerDeclaredTriggers, ", "))
		}
	}
	b.WriteString("\n")
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
