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
- **ACL cap (4000)** — Enterprise supports up to 4000 ACLs; exceeding the cap forces Dedicated. Source: [https://docs.confluent.io/cloud/current/clusters/cluster-types.html](https://docs.confluent.io/cloud/current/clusters/cluster-types.html).

<details><summary>Cutover-related terms (Stop-Restart-Repeat, Blue/Green, CC Gateway-mediated, etc.)</summary>

- **Stop-Restart-Repeat** — phased per-service cutover. Each application (or topic) is stopped on MSK, mirrored to CC, and resumed at the CC endpoint, one at a time. Recoverable per step; longer elapsed time.
- **Stop-Wait-Restart** — single coordinated maintenance window. Producers stop, the mirror catches up, services resume in sequence inside the window.
- **Restart-All-At-Once** — single window where every client reconfigures and reconnects at the same instant. Largest blast radius; one rollback point for the whole fleet.
- **Blue/Green** — parallel run on both sides via Cluster Linking. Zero downtime, highest operational complexity; customer-designed orchestration.
- **CC Gateway-mediated** — a sidecar component that absorbs the cutover with a 30–90 s `BROKER_NOT_AVAILABLE` window per service, after which clients auto-retry against CC. Removes the per-service producer restart; requires Confluent for Kubernetes + a Gateway Add-On license.
- **Plain Cluster Linking** — Cluster Linking without the CC Gateway; the simpler op model. Each service stops, mirror drains, restarts against the CC endpoint (minutes per service). Fully supported; chosen via `prefer_gateway: false`.

</details>

## 1. Source Environment

- **4** source clusters across **1** region · **15** brokers · **265** topics

| Cluster | Region | Brokers | Topics | Source auth |
|---|---|---:|---:|---|
| heavy-acls | us-east-1 | 3 | 65 | `scram` |
| mtls-cluster | us-east-1 | 3 | 38 | `mtls` |
| small-orders | us-east-1 | 3 | 42 | `scram` |
| steady-events | us-east-1 | 6 | 120 | `scram` |

## 2. Sizing & Cluster Decisions

This section combines three per-cluster decisions: the **sizing** (how many eCKU each cluster needs to absorb its workload at the chosen percentile + headroom), the **cluster type** (Enterprise, Dedicated, or Freight — driven by hard limits like ACL count and customer-declared flags), and the **networking** topology (PNI, PrivateLink, Transit Gateway, or VPC Peering — driven by `target_cloud`, egress requirements, and projected PNI gateway count). They render together because each row's verdict in one column constrains the others: e.g. a Dedicated cluster opens up PrivateLink networking patterns that Enterprise caps.

| Cluster | P95 in / out (MBps) | Partitions | Final size | Cluster Type | Networking |
|---|---|---:|---:|---|---|
| heavy-acls | 15.0 / 25.0 | 300 | 1 CKU | Dedicated Multi-Zone (MZ) | PNI |
| mtls-cluster | 12.0 / 18.0 | 250 | 1 eCKU | Enterprise | PNI |
| small-orders | 10.0 / 20.0 | 200 | 1 eCKU | Enterprise | PNI |
| steady-events | 100.0 / 180.0 | 800 | 3 eCKU | Enterprise | PNI |

### Why These Recommendations

- **heavy-acls** — Cluster type **Dedicated Multi-Zone (MZ)** — ACL count exceeds Enterprise cap (4001 ACLs > Enterprise cap 4000). Networking **PNI** — Dedicated AWS cluster — PNI required (TGW / VPC Peering are alternatives only when the customer's existing MSK topology already uses them; see `existing_vpc_connectivity` in plan-inputs.yaml).
  - ℹ **Cost direction:** Dedicated has a higher monthly cost than Enterprise. This verdict is state-derived (a hard-limit rule fired on the cluster as scanned), so the escalation isn't recoverable by editing `plan-inputs.yaml`. Confirm with your Confluent account team that the cluster's capacity reflects your actual workload before committing.
- **mtls-cluster** — Cluster type **Enterprise** — no hard-limit rule fired. Networking **PNI** — default for AWS Enterprise (scales to 32 eCKU vs PrivateLink's 10-eCKU cap).
- **small-orders** — Cluster type **Enterprise** — no hard-limit rule fired. Networking **PNI** — default for AWS Enterprise (scales to 32 eCKU vs PrivateLink's 10-eCKU cap).
- **steady-events** — Cluster type **Enterprise** — no hard-limit rule fired. Networking **PNI** — default for AWS Enterprise (scales to 32 eCKU vs PrivateLink's 10-eCKU cap).

## 3. Cutover Approach

_The cutover style below applies to the **entire fleet**. Per-cluster cutover overrides aren't supported in this kcp version (planned for a follow-up). Heterogeneous fleets can run kcp against a state-file subset that contains only the clusters sharing a style — re-run for each subset and combine the recommendations manually._

- **Style:** Stop-Restart-Repeat (app-by-app)
- **Gateway mediation:** Plain Cluster Linking
  - ℹ **Awaiting gateway intent** — no preference declared yet; the Plan uses plain Cluster Linking until you confirm. See **Actions Needed** for how to choose.
- **Alternatives considered:**
  - **Stop-Wait-Restart** — single coordinated window; needs the window to be long enough for re-mirroring + validation. Pick via `downtime_tolerance: scheduled_window_sequential`.
  - **Restart-All-At-Once** — single window; every client reconfigures at the same instant. Highest blast radius. Pick via `downtime_tolerance: scheduled_window_all_at_once`.
  - **Blue/Green** — parallel run via Cluster Linking; zero downtime, highest operational complexity. Pick via `downtime_tolerance: zero`.
- **Prerequisites:**

  | Prereq | Status |
  |---|---|
  | Confluent for Kubernetes (CFK) cluster | ⛔ not started |
  | Confluent Cloud Gateway Add-On license | ⛔ not started |

  _IAM pre-migration prereq omitted — no IAM source detected in this fleet._


## 4. Client Auth Migration

Per-cluster source→target mapping. Each source-auth method on every cluster maps to a recommended Confluent Cloud auth — the recommendation can be overridden globally via `target_auth_method` or per-cluster via `clusters[<name>].target_auth_method`. The **Works via CC Gateway** column describes whether this auth method *could* flow through the CC Gateway when the gateway path is in use — it's a property of the auth mapping, not a statement about which path §3 picked.

| Cluster | Source auth | Target on Confluent Cloud | Works via CC Gateway | Notes |
|---|---|---|---|---|
| heavy-acls | `scram` | `confluent_cloud_api_keys` | ✅ yes (transparent swap) | — |
| mtls-cluster | `mtls` | `mtls` | ✅ yes (auth-swap mode) | Gateway terminates TLS and re-issues an ACM-PCA cert source-side (auth-swap mode). |
| small-orders | `scram` | `confluent_cloud_api_keys` | ✅ yes (transparent swap) | — |
| steady-events | `scram` | `confluent_cloud_api_keys` | ✅ yes (transparent swap) | — |

_Mapping provenance:_
- `mtls` mapping → https://docs.confluent.io/cloud/current/security/authenticate/workload-identities/identity-providers/mtls/configure.html (last verified 2026-05-20)
- `scram` mapping → https://docs.confluent.io/cloud/current/security/authenticate/overview.html (last verified 2026-05-20)

## 5. Actions Needed

Each item below is a concrete action that tightens the recommendation. The current recommendation stands; these close state-file gaps, fix invalid inputs, or resolve preference questions. **Severity prefix:** 🟢 preference (pick one).

1. 🟢 **Gateway intent — pick CC Gateway or plain Cluster Linking**
   - `prefer_gateway: true` (default) AND all three gateway prereqs (`confluent_for_kubernetes_status`, `cc_gateway_license_status`, `iam_pre_migration_status`) are at `not_started`. Both paths are fully supported — the Plan just needs you to pick. Plain Cluster Linking applies while this is open.
   - _How to close:_ In `plan-inputs.yaml`, either (a) set `prefer_gateway: false` to commit to plain Cluster Linking, OR (b) move at least one gateway prereq to `in_progress` to commit to the gateway path. Re-run `kcp report plan` to clear the OQ.

## Appendix A1 — Sizing Math
<details><summary>Show sizing math per cluster</summary>

Each cluster is sized by taking the largest of its three throughput-vs-cap ratios (ingress, egress, partitions) and scaling that by `(1 + headroom)`, so the recommended size has spare capacity above the observed P95. Headroom for this run is `0.30` (override via `headroom_fraction` in `plan-inputs.yaml`). SLA floor binds when the math comes in below the minimum eCKU for the target SLA (1 eCKU for 99.9, 1 eCKU for 99.95, 2 eCKU for 99.99 — published in the Confluent Cloud cluster-types SLA table). The `Sized` and `Final` columns are in eCKU on Enterprise and CKU on Dedicated.

Formula: `CEIL(max(P95In/60, P95Out/180, partitions/3000) * (1 + 0.30 headroom))`

| Cluster | Ingress ratio | Egress ratio | Partition ratio | Max (driver) | Sized | SLA floor | Final |
|---|---:|---:|---:|---|---:|---:|---:|
| heavy-acls | 0.2500 | 0.1389 | 0.1000 | **0.2500** (ingress) | 1 | 1 | 1 |
| mtls-cluster | 0.2000 | 0.1000 | 0.0833 | **0.2000** (ingress) | 1 | 1 | 1 |
| small-orders | 0.1667 | 0.1111 | 0.0667 | **0.1667** (ingress) | 1 | 1 | 1 |
| steady-events | 1.6667 | 1.0000 | 0.2667 | **1.6667** (ingress) | 3 | 1 | 3 |

Max-ratio is multiplied by `(1 + headroom)` and rounded up to get `Sized`. `Final` is `max(Sized, SLA floor)`.

</details>

## Appendix A2 — Hard-Limit Rules Evaluated
<details><summary>Show per-cluster rule-evaluation trace</summary>

Every cluster runs the same hard-limit catalog; this table records each rule's outcome so a reviewer can confirm the verdict and see negative evidence (e.g. "47 ACLs ≤ 4000 cap"). `skipped` rows mean the rule couldn't be evaluated — the cluster's verdict resolves on the other rules.

_Evidence below reflects the source state file as of 2026-05-15 15:00:00 UTC. Re-run `kcp discover` / `kcp scan ...` if the source environment has changed materially since._

**Cluster** `heavy-acls`

| Rule | Outcome | Detail |
|---|---|---|
| `eCKU_exceeds_pni_cap` (Sized eCKU exceeds Enterprise PNI cap) | `not_fired` | sized 1 eCKU ≤ PNI cap 32 eCKU |
| `acl_count_exceeds_cap` (ACL count exceeds Enterprise cap) | `fired` | 4001 ACLs > Enterprise cap 4000 |
| `broker_side_schema_validation_required` (Broker-side schema ID validation required) | `not_fired` | `enforce_schemas_at_the_broker: false` |
| `rest_produce_api_high_throughput` (High-throughput Kafka REST Produce v3 required) | `not_fired` | `requires_high_throughput_rest_produce_api: false` |
| `sla_99_95_single_zone` (99.95% single-zone SLA required) | `not_fired` | `requires_99_95_sla_within_a_single_zone: false` |
| `mtls_on_non_aws_target` (Source uses mTLS, target is non-AWS) | `not_fired` | no mTLS source + target_cloud="aws" |

**Cluster** `mtls-cluster`

| Rule | Outcome | Detail |
|---|---|---|
| `eCKU_exceeds_pni_cap` (Sized eCKU exceeds Enterprise PNI cap) | `not_fired` | sized 1 eCKU ≤ PNI cap 32 eCKU |
| `acl_count_exceeds_cap` (ACL count exceeds Enterprise cap) | `not_fired` | 0 ACLs ≤ Enterprise cap 4000 |
| `broker_side_schema_validation_required` (Broker-side schema ID validation required) | `not_fired` | `enforce_schemas_at_the_broker: false` |
| `rest_produce_api_high_throughput` (High-throughput Kafka REST Produce v3 required) | `not_fired` | `requires_high_throughput_rest_produce_api: false` |
| `sla_99_95_single_zone` (99.95% single-zone SLA required) | `not_fired` | `requires_99_95_sla_within_a_single_zone: false` |
| `mtls_on_non_aws_target` (Source uses mTLS, target is non-AWS) | `not_fired` | mTLS source + target_cloud="aws" (AWS supports mTLS on Enterprise/Freight) |

**Cluster** `small-orders`

| Rule | Outcome | Detail |
|---|---|---|
| `eCKU_exceeds_pni_cap` (Sized eCKU exceeds Enterprise PNI cap) | `not_fired` | sized 1 eCKU ≤ PNI cap 32 eCKU |
| `acl_count_exceeds_cap` (ACL count exceeds Enterprise cap) | `not_fired` | 0 ACLs ≤ Enterprise cap 4000 |
| `broker_side_schema_validation_required` (Broker-side schema ID validation required) | `not_fired` | `enforce_schemas_at_the_broker: false` |
| `rest_produce_api_high_throughput` (High-throughput Kafka REST Produce v3 required) | `not_fired` | `requires_high_throughput_rest_produce_api: false` |
| `sla_99_95_single_zone` (99.95% single-zone SLA required) | `not_fired` | `requires_99_95_sla_within_a_single_zone: false` |
| `mtls_on_non_aws_target` (Source uses mTLS, target is non-AWS) | `not_fired` | no mTLS source + target_cloud="aws" |

**Cluster** `steady-events`

| Rule | Outcome | Detail |
|---|---|---|
| `eCKU_exceeds_pni_cap` (Sized eCKU exceeds Enterprise PNI cap) | `not_fired` | sized 3 eCKU ≤ PNI cap 32 eCKU |
| `acl_count_exceeds_cap` (ACL count exceeds Enterprise cap) | `not_fired` | 0 ACLs ≤ Enterprise cap 4000 |
| `broker_side_schema_validation_required` (Broker-side schema ID validation required) | `not_fired` | `enforce_schemas_at_the_broker: false` |
| `rest_produce_api_high_throughput` (High-throughput Kafka REST Produce v3 required) | `not_fired` | `requires_high_throughput_rest_produce_api: false` |
| `sla_99_95_single_zone` (99.95% single-zone SLA required) | `not_fired` | `requires_99_95_sla_within_a_single_zone: false` |
| `mtls_on_non_aws_target` (Source uses mTLS, target is non-AWS) | `not_fired` | no mTLS source + target_cloud="aws" |

</details>

