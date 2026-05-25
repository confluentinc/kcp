# Migration Plan — Amazon MSK → Confluent Cloud

_Generated 2026-05-15 15:00:00 UTC by KCP 0.7.2 from `./kcp-state.json` · plan schema `1-experimental`._

## Definitions

- **Enterprise / Dedicated** — Confluent Cloud cluster tiers. Enterprise has elastic billing per eCKU; Dedicated is fixed-provisioned per CKU. **Dedicated Multi-Zone (MZ)** is the default topology when Dedicated is selected; **Dedicated Single-Zone (SZ)** fires only when `requires_99_95_sla_within_a_single_zone: true` is set in `plan-inputs.yaml`.
- **eCKU** — Elastic Confluent Kafka Unit, the throughput unit on Enterprise clusters. 60 MB/s ingress + 180 MB/s egress per eCKU at the per-eCKU caps used below.
- **CKU** — Confluent Kafka Unit, the Dedicated-tier equivalent of eCKU. Sizing math is the same; only the unit name changes. Dedicated clusters always render with `CKU`.
- **P95** — the 95th-percentile sustained throughput observed in the metrics window. Sizing uses P95 (override with `sizing_percentile`) so a transient spike doesn't permanently inflate the recommended cluster size.
- **Final size** — the recommended eCKU (Enterprise) or CKU (Dedicated) count for the cluster. `(floor)` next to a value means the SLA minimum was binding (the math came in below the floor and was rounded up).
- **Peak burst** — short-window peak throughput observed in metrics, expressed as eCKU. Surfaces in the spiky-workload note when peak diverges from P95 by more than the configured ratio.
- **PNI** (Private Network Interface) — AWS-to-AWS private connectivity, up to 32 eCKU on Enterprise. The default for AWS Enterprise; **always required on Dedicated** (AWS).
- **PrivateLink** — capped at 10 eCKU on Enterprise. Fires when `target_cloud != "aws"` (PNI is AWS-only), when `cc_egress_required: true` (PNI lacks native CC→customer egress), or when `projected_pni_gateway_count >= 2`. Also the cross-cloud private path on Dedicated when `target_cloud` is Azure / GCP.
- **ACL cap (4000)** — Enterprise supports up to 4000 ACLs; exceeding the cap forces Dedicated. Source: cluster-types.html.

## 1. Source Environment

- **4** source clusters across **1** region · **15** brokers · **265** topics

| Cluster | Region | Brokers | Topics |
|---|---|---:|---:|
| heavy-acls | us-east-1 | 3 | 65 |
| mtls-to-azure | us-east-1 | 3 | 38 |
| small-orders | us-east-1 | 3 | 42 |
| steady-events | us-east-1 | 6 | 120 |

## 2. Sizing & Cluster Decisions

| Cluster | P95 in / out (MBps) | Partitions | Final size | Cluster Type | Networking |
|---|---|---:|---:|---|---|
| heavy-acls | 15.0 / 25.0 | 300 | 1 CKU | Dedicated Multi-Zone (MZ) | PNI |
| mtls-to-azure | 12.0 / 18.0 | 250 | 1 CKU | Dedicated Multi-Zone (MZ) | PNI |
| small-orders | 10.0 / 20.0 | 200 | 1 eCKU | Enterprise | PrivateLink |
| steady-events | 100.0 / 180.0 | 800 | 3 eCKU | Enterprise | PrivateLink |

### Why These Recommendations

- **heavy-acls** — Cluster type **Dedicated Multi-Zone (MZ)** — ACL count exceeds Enterprise cap (4001 ACLs > Enterprise cap 4000). Networking **PNI** — Dedicated cluster — PNI required (no TGW / VPC Peering override).
- **mtls-to-azure** — Cluster type **Dedicated Multi-Zone (MZ)** — Source uses mTLS, target is non-AWS (target_cloud="azure"). Networking **PNI** — Dedicated cluster — PNI required (no TGW / VPC Peering override).
- **small-orders** — Cluster type **Enterprise** — no hard-limit rule fired. Networking **PrivateLink** — target_cloud="azure" — PNI is AWS-to-AWS only, so cross-cloud lands on PrivateLink.
- **steady-events** — Cluster type **Enterprise** — no hard-limit rule fired. Networking **PrivateLink** — target_cloud="azure" — PNI is AWS-to-AWS only, so cross-cloud lands on PrivateLink.

## Appendix A1 — Sizing Math
<details><summary>Show sizing math per cluster</summary>

Each cluster is sized by taking the largest of its three throughput-vs-cap ratios (ingress, egress, partitions) and scaling that by `(1 + headroom)`, so the recommended size has spare capacity above the observed P95. Headroom for this run is `0.30` (override via `headroom_fraction` in `plan-inputs.yaml`). SLA floor binds when the math comes in below the minimum eCKU for the target SLA (1 eCKU for 99.9, 2 eCKU for 99.99 — published in the Confluent Cloud cluster-types SLA table). The `Sized` and `Final` columns are in eCKU on Enterprise and CKU on Dedicated.

Formula: `CEIL(max(P95In/60, P95Out/180, partitions/3000) * (1 + 0.30 headroom))`

| Cluster | Ingress ratio | Egress ratio | Partition ratio | Max (driver) | Sized | SLA floor | Final |
|---|---:|---:|---:|---|---:|---:|---:|
| heavy-acls | 0.2500 | 0.1389 | 0.1000 | **0.2500** (ingress) | 1 | 1 | 1 |
| mtls-to-azure | 0.2000 | 0.1000 | 0.0833 | **0.2000** (ingress) | 1 | 1 | 1 |
| small-orders | 0.1667 | 0.1111 | 0.0667 | **0.1667** (ingress) | 1 | 1 | 1 |
| steady-events | 1.6667 | 1.0000 | 0.2667 | **1.6667** (ingress) | 3 | 1 | 3 |

Max-ratio is multiplied by `(1 + headroom)` and rounded up to get `Sized`. `Final` is `max(Sized, SLA floor)`.

</details>
