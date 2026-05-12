# Migration Plan — Amazon MSK → Confluent Cloud

_Generated 2026-05-12 12:00:00 UTC by KCP 0.7.2 from `./kcp-state.json`._

## Definitions

- **eCKU** — Elastic Confluent Kafka Unit, the throughput unit on Enterprise clusters. ~30 MB/s ingress + 90 MB/s egress per eCKU at the per-eCKU caps used below.
- **P95** — the 95th-percentile sustained throughput observed in the metrics window. Sizing uses P95 so a transient spike doesn't permanently inflate the recommended cluster size.
- **PrivateLink** — single-AZ private connectivity, up to 10 eCKU. The default network when the cluster fits inside it with headroom.
- **PNI** (Private Network Interface) — multi-AZ private connectivity, up to 32 eCKU. Used when PrivateLink's cap is too close to the cluster's peak burst.

## 1. Source Environment

- **2** source clusters across **1** region(s)

| Cluster | Region | Brokers | Topics |
|---|---|---:|---:|
| cluster-a | us-east-1 | 3 | 250 |
| cluster-b | us-east-1 | 9 | 400 |

## 2. Sizing & Cluster Decisions

| Cluster | P95 in / out (MBps) | Partitions | Final eCKU | Cluster Type | Networking |
|---|---|---:|---:|---|---|
| cluster-a | 12.0 / 18.0 | 600 | 1 | Enterprise | PrivateLink |
| cluster-b | 90.0 / 900.0 | 1500 | 7 | Enterprise | PNI |

### Why these recommendations

- **cluster-a** — Cluster type **Enterprise** — no hard-limit rule fired. Networking **PrivateLink** — Peak burst 1 eCKU below 80% of 10 eCKU PrivateLink cap.
- **cluster-b** — Cluster type **Enterprise** — no hard-limit rule fired. Networking **PNI** — Peak burst 9 eCKU at 90% of PrivateLink cap — exceeds 80% safety threshold.

## Appendix A1 — Sizing Math

<details><summary>Show sizing math per cluster</summary>

Each cluster is sized by taking the largest of its three throughput-vs-cap ratios (ingress, egress, partitions) and scaling that by `(1 + headroom)`, so the recommended eCKU has spare capacity above the observed P95. SLA floor binds when the math comes in below the minimum eCKU for the target SLA.

Formula: `CEIL(max(P95In/60, P95Out/180, partitions/3000) * (1 + 0.30 headroom))`

| Cluster | Ingress ratio | Egress ratio | Partition ratio | Max (driver) | Sized eCKU | SLA floor | Final eCKU |
|---|---:|---:|---:|---|---:|---:|---:|
| cluster-a | 0.2000 | 0.1000 | 0.2000 | **0.2000** (partitions) | 1 | 1 | 1 |
| cluster-b | 1.5000 | 5.0000 | 0.5000 | **5.0000** (egress) | 7 | 1 | 7 |

Max-ratio is multiplied by `(1 + headroom)` and rounded up to get `Sized eCKU`. `Final eCKU` is `max(Sized, SLA floor)`.

</details>
