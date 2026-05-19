# Migration Plan — Amazon MSK → Confluent Cloud

_Generated 2026-05-15 15:00:00 UTC by KCP 0.7.2 from `./kcp-state.json`._

## Definitions

- **eCKU** — Elastic Confluent Kafka Unit, the throughput unit on Enterprise clusters. 60 MB/s ingress + 180 MB/s egress per eCKU at the per-eCKU caps used below.
- **P95** — the 95th-percentile sustained throughput observed in the metrics window. Sizing uses P95 (override with `sizing_percentile`) so a transient spike doesn't permanently inflate the recommended cluster size.
- **PrivateLink** — single-AZ private connectivity, up to 10 eCKU. The default network when the cluster fits inside it with headroom.
- **PNI** (Private Network Interface) — multi-AZ private connectivity, up to 32 eCKU. Used when PrivateLink's cap is too close to the cluster's peak burst.

## 1. Source Environment

- **5** source clusters across **1** region(s)

| Cluster | Region | Brokers | Topics |
|---|---|---:|---:|
| bursty-events | us-east-1 | 6 | 120 |
| heavy-acls | us-east-1 | 3 | 65 |
| mtls-to-azure | us-east-1 | 3 | 38 |
| small-orders | us-east-1 | 3 | 42 |
| stale-no-metrics | us-east-1 | 3 | 28 |

## 2. Sizing & Cluster Decisions

| Cluster | P95 in / out (MBps) | Partitions | Final size | Cluster Type | Networking |
|---|---|---:|---:|---|---|
| bursty-events | 100.0 / 180.0 | 800 | 3 eCKU | Enterprise | PNI |
| heavy-acls | 15.0 / 25.0 | 300 | 1 CKU | Dedicated Multi-Zone (MZ) | PNI |
| mtls-to-azure | 12.0 / 18.0 | 250 | 1 CKU | Dedicated Multi-Zone (MZ) | PNI |
| small-orders | 10.0 / 20.0 | 200 | 1 eCKU | Enterprise | PrivateLink |
| stale-no-metrics | _sizing degraded_ | 150 | 1 eCKU (floor) | Enterprise | PrivateLink |

### Why these recommendations

- **bursty-events** — Cluster type **Enterprise** — no hard-limit rule fired. Networking **PNI** — Peak burst 8 eCKU at 80% of PrivateLink cap — exceeds 80% safety threshold.
- **heavy-acls** — Cluster type **Dedicated Multi-Zone (MZ)** — ACL count exceeds Enterprise cap (4001 ACLs > Enterprise cap 4000). Networking **PNI** — Dedicated cluster — PNI required (no TGW / VPC Peering override).
- **mtls-to-azure** — Cluster type **Dedicated Multi-Zone (MZ)** — Source uses mTLS AND target cloud is not AWS (only Dedicated has mTLS off AWS) (source uses mTLS AND target_cloud="azure" (mTLS off AWS requires Dedicated)). Networking **PNI** — Dedicated cluster — PNI required (no TGW / VPC Peering override).
- **small-orders** — Cluster type **Enterprise** — no hard-limit rule fired. Networking **PrivateLink** — Peak burst 1 eCKU below 80% of 10 eCKU PrivateLink cap.
- **stale-no-metrics** — no BytesInPerSec or BytesOutPerSec p95; re-run kcp scan metrics. Final eCKU defaults to the SLA floor (1). Re-run with metrics to lock in throughput-based sizing.

## 3. Open Questions

Each item below is a gap the Plan flagged while building. The recommendation in §2 stands, but answering these items tightens the result.

1. **`bursty-events` — Spiky workload detected (peak materially exceeds P95)**
   - Sized eCKU is based on P95; the observed peak is the spike threshold above. Current sizing relies on Enterprise's elastic scaling to absorb the spike — if the spike is steady-state rather than seasonal, consider `sizing_percentile: p99`.
   - _How to close:_ Confirm with the owning team whether the spike is seasonal (current sizing is fine) or steady-state (override `sizing_percentile`).
2. **`stale-no-metrics` — No P95 throughput metrics — sizing fell back to SLA floor**
   - no BytesInPerSec or BytesOutPerSec p95; re-run kcp scan metrics
   - _How to close:_ Re-run `kcp scan metrics` against this cluster.

## Appendix A1 — Sizing Math
<details><summary>Show sizing math per cluster</summary>

Each cluster is sized by taking the largest of its three throughput-vs-cap ratios (ingress, egress, partitions) and scaling that by `(1 + headroom)`, so the recommended eCKU has spare capacity above the observed P95. SLA floor binds when the math comes in below the minimum eCKU for the target SLA.

Formula: `CEIL(max(P95In/60, P95Out/180, partitions/3000) * (1 + 0.30 headroom))`

| Cluster | Ingress ratio | Egress ratio | Partition ratio | Max (driver) | Sized eCKU | SLA floor | Final eCKU |
|---|---:|---:|---:|---|---:|---:|---:|
| bursty-events | 1.6667 | 1.0000 | 0.2667 | **1.6667** (ingress) | 3 | 1 | 3 |
| heavy-acls | 0.2500 | 0.1389 | 0.1000 | **0.2500** (ingress) | 1 | 1 | 1 |
| mtls-to-azure | 0.2000 | 0.1000 | 0.0833 | **0.2000** (ingress) | 1 | 1 | 1 |
| small-orders | 0.1667 | 0.1111 | 0.0667 | **0.1667** (ingress) | 1 | 1 | 1 |
| stale-no-metrics | _degraded_ | _degraded_ | 150 / 3000 | _n/a_ | _n/a_ | 1 | 1 |

Max-ratio is multiplied by `(1 + headroom)` and rounded up to get `Sized eCKU`. `Final eCKU` is `max(Sized, SLA floor)`.

</details>

