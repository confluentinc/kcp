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
func RenderMarkdown(p *types.Plan) ([]byte, error) {
	var b bytes.Buffer

	fmt.Fprintf(&b, "# Migration Plan — %s → Confluent Cloud\n\n", p.Header.Source)
	fmt.Fprintf(&b, "_Generated %s by KCP %s from `%s`._\n\n", p.Header.GeneratedAt.Format("2006-01-02 15:04:05 UTC"), p.Header.KCPVersion, p.Header.StateFilePath)

	writeDefinitions(&b)
	writeSourceEnvironment(&b, p)
	writeSizingAndDecisions(&b, p)
	writeSizingAppendix(&b, p)

	return b.Bytes(), nil
}

func writeDefinitions(b *bytes.Buffer) {
	b.WriteString("## Definitions\n\n")
	b.WriteString("- **eCKU** — Elastic Confluent Kafka Unit, the throughput unit on Enterprise clusters. ~30 MB/s ingress + 90 MB/s egress per eCKU at the per-eCKU caps used below.\n")
	b.WriteString("- **P95** — the 95th-percentile sustained throughput observed in the metrics window. Sizing uses P95 so a transient spike doesn't permanently inflate the recommended cluster size.\n")
	b.WriteString("- **PrivateLink** — single-AZ private connectivity, up to 10 eCKU. The default network when the cluster fits inside it with headroom.\n")
	b.WriteString("- **PNI** (Private Network Interface) — multi-AZ private connectivity, up to 32 eCKU. Used when PrivateLink's cap is too close to the cluster's peak burst.\n\n")
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

	b.WriteString("| Cluster | P95 in / out (MBps) | Partitions | Final eCKU | Cluster Type | Networking |\n")
	b.WriteString("|---|---|---:|---:|---|---|\n")
	for i, s := range p.Sizing {
		ct := types.ClusterType("")
		net := types.Networking("")
		if i < len(p.ClusterTypeDecision) {
			ct = p.ClusterTypeDecision[i].Verdict
		}
		if i < len(p.NetworkingDecision) {
			net = p.NetworkingDecision[i].Verdict
		}
		if s.Degraded {
			fmt.Fprintf(b, "| %s | _sizing degraded_ | %d | %d (floor) | %s | %s |\n",
				s.ClusterID, s.UserPartitions, s.FinalECKU, ct, net)
			continue
		}
		fmt.Fprintf(b, "| %s | %.1f / %.1f | %d | %d | %s | %s |\n",
			s.ClusterID, s.P95InMBps, s.P95OutMBps, s.UserPartitions, s.FinalECKU, ct, net)
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
		if i < len(p.ClusterTypeDecision) {
			ct := p.ClusterTypeDecision[i]
			if len(ct.Triggers) == 0 {
				pieces = append(pieces, fmt.Sprintf("Cluster type **%s** — no hard-limit rule fired", ct.Verdict))
			} else {
				rules := make([]string, 0, len(ct.Triggers))
				for _, t := range ct.Triggers {
					rules = append(rules, fmt.Sprintf("%s (%s)", t.Description, t.Evidence))
				}
				pieces = append(pieces, fmt.Sprintf("Cluster type **%s** — %s", ct.Verdict, strings.Join(rules, "; ")))
			}
		}
		if i < len(p.NetworkingDecision) {
			n := p.NetworkingDecision[i]
			pieces = append(pieces, fmt.Sprintf("Networking **%s** — %s", n.Verdict, n.Reason))
		}
		fmt.Fprintf(b, "- **%s** — %s.\n", s.ClusterID, strings.Join(pieces, ". "))
	}
	b.WriteString("\n")
}

func writeSizingAppendix(b *bytes.Buffer, p *types.Plan) {
	if len(p.SizingAppendix) == 0 {
		return
	}
	b.WriteString("## Appendix A1 — Sizing Math\n")
	b.WriteString("<details><summary>Show sizing math per cluster</summary>\n\n")
	b.WriteString("Each cluster is sized by taking the largest of its three throughput-vs-cap ratios (ingress, egress, partitions) and scaling that by `(1 + headroom)`, so the recommended eCKU has spare capacity above the observed P95. SLA floor binds when the math comes in below the minimum eCKU for the target SLA.\n\n")
	// The formula is identical for every cluster (caps + headroom are
	// constants, not per-cluster). Print it once, then a single audit
	// table covers every cluster in a row.
	fmt.Fprintf(b, "Formula: `%s`\n\n", p.SizingAppendix[0].Formula)
	b.WriteString("| Cluster | Ingress ratio | Egress ratio | Partition ratio | Max (driver) | Sized eCKU | SLA floor | Final eCKU |\n")
	b.WriteString("|---|---:|---:|---:|---|---:|---:|---:|\n")
	for i, s := range p.Sizing {
		_ = i
		if s.Degraded {
			fmt.Fprintf(b, "| %s | _degraded_ | _degraded_ | %d / 3000 | _n/a_ | _n/a_ | %d | %d |\n",
				s.ClusterID, s.UserPartitions, s.SLAFloorECKU, s.FinalECKU)
			continue
		}
		driver := sizingDriver(s)
		fmt.Fprintf(b, "| %s | %.4f | %.4f | %.4f | **%.4f** (%s) | %d | %d | %d |\n",
			s.ClusterID, s.IngressRatio, s.EgressRatio, s.PartitionRatio, s.MaxRatio, driver, s.SizedECKU, s.SLAFloorECKU, s.FinalECKU)
	}
	b.WriteString("\nMax-ratio is multiplied by `(1 + headroom)` and rounded up to get `Sized eCKU`. `Final eCKU` is `max(Sized, SLA floor)`.\n\n")
	b.WriteString("</details>\n\n")
}

// sizingDriver returns the label of the ratio that won max() for a cluster.
// Used in the appendix table so the reader can see at a glance which
// dimension (ingress, egress, or partitions) drove the eCKU number.
func sizingDriver(s types.ClusterSizing) string {
	switch {
	case s.MaxRatio == s.EgressRatio:
		return "egress"
	case s.MaxRatio == s.IngressRatio:
		return "ingress"
	default:
		return "partitions"
	}
}
