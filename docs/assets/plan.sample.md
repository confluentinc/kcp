# Migration Plan — Amazon MSK → Confluent Cloud

_Generated 2026-05-15 15:00:00 UTC by KCP 0.0.0-localdev from `sample-state.json` · plan schema `1-experimental`._

## Definitions

- **Enterprise / Dedicated** — Confluent Cloud cluster tiers. Enterprise has elastic billing per eCKU; Dedicated is fixed-provisioned per CKU. **MZ** (Multi-Zone) is the default Dedicated topology; **SZ** (Single-Zone) fires only when `requires_99_95_sla_within_a_single_zone: true` is set in `plan-inputs.yaml`.
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
| heavy-acls | 15.0 / 25.0 | 300 | 1 CKU | Dedicated Multi-Zone (MZ) | PrivateLink |
| mtls-to-azure | 12.0 / 18.0 | 250 | 1 CKU | Dedicated Multi-Zone (MZ) | PrivateLink |
| small-orders | 10.0 / 20.0 | 200 | 1 eCKU | Enterprise | PrivateLink |
| steady-events | 100.0 / 180.0 | 800 | 3 eCKU | Enterprise | PrivateLink |

### Why These Recommendations

- **heavy-acls** — Cluster type **Dedicated Multi-Zone (MZ)** — ACL count exceeds Enterprise cap (4001 ACLs > Enterprise cap 4000). Networking **PrivateLink** — target_cloud="azure" — PNI / TGW / VPC Peering are AWS-only, so cross-cloud Dedicated lands on PrivateLink (set `target_cloud: aws` in `plan-inputs.yaml` to undo).
- **mtls-to-azure** — Cluster type **Dedicated Multi-Zone (MZ)** — Source uses mTLS, target is non-AWS (mTLS source + target_cloud="azure" (mTLS on non-AWS requires Dedicated)). Networking **PrivateLink** — target_cloud="azure" — PNI / TGW / VPC Peering are AWS-only, so cross-cloud Dedicated lands on PrivateLink (set `target_cloud: aws` in `plan-inputs.yaml` to undo).
- **small-orders** — Cluster type **Enterprise** — no hard-limit rule fired. Networking **PrivateLink** — target_cloud="azure" — PNI is AWS-to-AWS only, so cross-cloud lands on PrivateLink (set `target_cloud: aws` in `plan-inputs.yaml` to undo).
- **steady-events** — Cluster type **Enterprise** — no hard-limit rule fired. Networking **PrivateLink** — target_cloud="azure" — PNI is AWS-to-AWS only, so cross-cloud lands on PrivateLink (set `target_cloud: aws` in `plan-inputs.yaml` to undo).

## Appendix A1 — Sizing Math
<details><summary>Show sizing math per cluster</summary>

Each cluster is sized by taking the largest of its three throughput-vs-cap ratios (ingress, egress, partitions) and scaling that by `(1 + headroom)`, so the recommended size has spare capacity above the observed P95. Headroom for this run is `0.30` (override via `headroom_fraction` in `plan-inputs.yaml`). SLA floor binds when the math comes in below the minimum eCKU for the target SLA (1 eCKU for 99.9, 1 eCKU for 99.95, 2 eCKU for 99.99 — published in the Confluent Cloud cluster-types SLA table). The `Sized` and `Final` columns are in eCKU on Enterprise and CKU on Dedicated.

Formula: `CEIL(max(P95In/60, P95Out/180, partitions/3000) * (1 + 0.30 headroom))`

| Cluster | Ingress ratio | Egress ratio | Partition ratio | Max (driver) | Sized | SLA floor | Final |
|---|---:|---:|---:|---|---:|---:|---:|
| heavy-acls | 0.2500 | 0.1389 | 0.1000 | **0.2500** (ingress) | 1 | 1 | 1 |
| mtls-to-azure | 0.2000 | 0.1000 | 0.0833 | **0.2000** (ingress) | 1 | 1 | 1 |
| small-orders | 0.1667 | 0.1111 | 0.0667 | **0.1667** (ingress) | 1 | 1 | 1 |
| steady-events | 1.6667 | 1.0000 | 0.2667 | **1.6667** (ingress) | 3 | 1 | 3 |

Max-ratio is multiplied by `(1 + headroom)` and rounded up to get `Sized`. `Final` is `max(Sized, SLA floor)`.

</details>

## Appendix A2 — Hard-Limit Rules Evaluated
<details><summary>Show per-cluster rule-evaluation trace</summary>

Every cluster runs the same hard-limit catalog; this table records each rule's outcome so a reviewer can confirm the verdict and see negative evidence (e.g. "47 ACLs ≤ 4000 cap"). `skipped` rows mean the rule couldn't be evaluated — the cluster's verdict resolves on the other rules.

**heavy-acls**

| Rule | Outcome | Detail |
|---|---|---|
| `eCKU_exceeds_pni_cap` (Sized eCKU exceeds Enterprise PNI cap) | `not_fired` | sized 1 eCKU ≤ PNI cap 32 eCKU |
| `acl_count_exceeds_cap` (ACL count exceeds Enterprise cap) | `fired` | 4001 ACLs > Enterprise cap 4000 |
| `broker_side_schema_validation_required` (Broker-side schema ID validation required) | `not_fired` | `enforce_schemas_at_the_broker: false` |
| `rest_produce_api_high_throughput` (High-throughput Kafka REST Produce v3 required) | `not_fired` | `requires_high_throughput_rest_produce_api: false` |
| `sla_99_95_single_zone` (99.95% single-zone SLA required) | `not_fired` | `requires_99_95_sla_within_a_single_zone: false` |
| `mtls_on_non_aws_target` (Source uses mTLS, target is non-AWS) | `not_fired` | no mTLS source + target_cloud="azure" |

**mtls-to-azure**

| Rule | Outcome | Detail |
|---|---|---|
| `eCKU_exceeds_pni_cap` (Sized eCKU exceeds Enterprise PNI cap) | `not_fired` | sized 1 eCKU ≤ PNI cap 32 eCKU |
| `acl_count_exceeds_cap` (ACL count exceeds Enterprise cap) | `not_fired` | 0 ACLs ≤ Enterprise cap 4000 |
| `broker_side_schema_validation_required` (Broker-side schema ID validation required) | `not_fired` | `enforce_schemas_at_the_broker: false` |
| `rest_produce_api_high_throughput` (High-throughput Kafka REST Produce v3 required) | `not_fired` | `requires_high_throughput_rest_produce_api: false` |
| `sla_99_95_single_zone` (99.95% single-zone SLA required) | `not_fired` | `requires_99_95_sla_within_a_single_zone: false` |
| `mtls_on_non_aws_target` (Source uses mTLS, target is non-AWS) | `fired` | mTLS source + target_cloud="azure" (mTLS on non-AWS requires Dedicated) |

**small-orders**

| Rule | Outcome | Detail |
|---|---|---|
| `eCKU_exceeds_pni_cap` (Sized eCKU exceeds Enterprise PNI cap) | `not_fired` | sized 1 eCKU ≤ PNI cap 32 eCKU |
| `acl_count_exceeds_cap` (ACL count exceeds Enterprise cap) | `not_fired` | 0 ACLs ≤ Enterprise cap 4000 |
| `broker_side_schema_validation_required` (Broker-side schema ID validation required) | `not_fired` | `enforce_schemas_at_the_broker: false` |
| `rest_produce_api_high_throughput` (High-throughput Kafka REST Produce v3 required) | `not_fired` | `requires_high_throughput_rest_produce_api: false` |
| `sla_99_95_single_zone` (99.95% single-zone SLA required) | `not_fired` | `requires_99_95_sla_within_a_single_zone: false` |
| `mtls_on_non_aws_target` (Source uses mTLS, target is non-AWS) | `not_fired` | no mTLS source + target_cloud="azure" |

**steady-events**

| Rule | Outcome | Detail |
|---|---|---|
| `eCKU_exceeds_pni_cap` (Sized eCKU exceeds Enterprise PNI cap) | `not_fired` | sized 3 eCKU ≤ PNI cap 32 eCKU |
| `acl_count_exceeds_cap` (ACL count exceeds Enterprise cap) | `not_fired` | 0 ACLs ≤ Enterprise cap 4000 |
| `broker_side_schema_validation_required` (Broker-side schema ID validation required) | `not_fired` | `enforce_schemas_at_the_broker: false` |
| `rest_produce_api_high_throughput` (High-throughput Kafka REST Produce v3 required) | `not_fired` | `requires_high_throughput_rest_produce_api: false` |
| `sla_99_95_single_zone` (99.95% single-zone SLA required) | `not_fired` | `requires_99_95_sla_within_a_single_zone: false` |
| `mtls_on_non_aws_target` (Source uses mTLS, target is non-AWS) | `not_fired` | no mTLS source + target_cloud="azure" |

</details>

