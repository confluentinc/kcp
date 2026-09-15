# Migration Plan

Source: Questionnaire (no scan file) · Generated <example> · kcp 0.0.0-localdev

## Summary

**1 cluster** across **1 region**. Each cluster gets one migration plan, split into an infrastructure half and an application half. Where a cluster runs multiple apps with different needs, the application half is per app.

| Migration plan | Region | Infrastructure plan | Application plan |
| --- | --- | --- | --- |
| [your-cluster](#cluster-your-cluster) | — | Needs answers | Needs answers |

No throughput metrics are available (no scan), so all sizing below is a lower bound. Run `kcp scan metrics` to size from real ingress/egress before you commit.

14 required + 6 optional questions open across all plans.

**Next:** answer the required questions in `plan-inputs.yaml` (see [Answers by cluster](#answers-by-cluster) for what's open), then re-run `kcp report plan --plan-inputs plan-inputs.yaml`.

**Prefer a hand?** [Talk to a person](#talk-to-a-person). A Confluent migration specialist can help you build the plan, for free.

> [!NOTE]
> This plan was generated from your answers alone (no scan), which is a complete way to plan. For sharper sizing and ready-to-run commands, run `kcp discover` (MSK) or `kcp scan` to produce a state file, then re-run this with `--state-file <file>`.

## Cluster: your-cluster

**Source:** No scan, answers only. Every fact is taken from `plan-inputs.yaml`.

### Infrastructure plan

> [!IMPORTANT]
> **Needs answers.** The `Pending` rows below unlock once you answer this plan's [Required questions (Action needed)](#cluster-your-cluster-answers).

| Category | Recommendation | Why |
| --- | --- | --- |
| Cluster type | Pending | Answer `partition_band`, `private_networking_required` (they decide this). |
| Sizing | Pending | Answer `partition_band` (it decides this). |
| Networking | Pending | Answer `kafka_version`, `move_existing_data`, `partition_band`, `private_networking_required` (they decide this). |
| Authentication | Pending | Answer `source_auth` (it decides this). |

**Sizing signals.** Not measured: partition count, ingress throughput, egress throughput, request rate.

#### Why these recommendations

- **Cluster type**: Pending, decided by `partition_band`, `private_networking_required`. Answer them and re-run to see this.
- **Sizing**: Pending, decided by `partition_band`. Answer it and re-run to see this.
  - **Heads up:** Sizing is a lower bound; see the note at the top of the plan.
- **Networking**: Pending, decided by `kafka_version`, `move_existing_data`, `partition_band`, `private_networking_required`. Answer them and re-run to see this.
- **Authentication**: Pending, decided by `source_auth`. Answer it and re-run to see this.

### Application plan

> [!IMPORTANT]
> **Needs answers.** The `Pending` rows below unlock once you answer this plan's [Required questions (Action needed)](#cluster-your-cluster-answers).

| Category | Recommendation | Why |
| --- | --- | --- |
| Data migration | Pending | Answer `downtime_tolerance`, `kafka_version`, `move_existing_data`, `private_networking_required` (they decide this). |
| Schema | Pending | Answer `schema_registry`, `schema_strategy` (they decide this). |
| Connectors | Pending | Answer `msk_connect_present`, `self_managed_connectors` (they decide this). |
| Topics | Pending | Answer `topics_have_custom_settings` (it decides this). |
| Historical data | Pending | Answer `move_existing_data`, `tiered_storage` (they decide this). |

#### Why these recommendations

- **Data migration**: Pending, decided by `downtime_tolerance`, `kafka_version`, `move_existing_data`, `private_networking_required`. Answer them and re-run to see this.
- **Schema**: Pending, decided by `schema_registry`, `schema_strategy`. Answer them and re-run to see this.
- **Connectors**: Pending, decided by `msk_connect_present`, `self_managed_connectors`. Answer them and re-run to see this.
- **Topics**: Pending, decided by `topics_have_custom_settings`. Answer it and re-run to see this.
- **Historical data**: Pending, decided by `move_existing_data`, `tiered_storage`. Answer them and re-run to see this.

### Migration steps

> [!NOTE]
> Pending: available once the infrastructure plan above is settled.

---

## Questions

Every question, its wording, and its options, listed once. Set answers per cluster (or fleet-wide in `all_clusters:`) in `plan-inputs.yaml`; see **Answers by cluster** for each cluster's current answers and what's still needed.

| Key | Question | Options |
| --- | --- | --- |
| `use_case_breadth` | How widely is this cluster used? | `one-app` → One team, one application<br>`few-teams` → A few teams or applications<br>`shared-fabric` → Shared fabric across many apps and teams |
| `move_existing_data` | Do you need to move your existing data? | `true` → Yes, move my existing data: Brings your history along. Availability depends on your source setup.<br>`false` → No, we can start fresh: Points producers and consumers at the new cluster and lets the old one retire |
| `private_networking_required` | Is private networking a hard requirement?<br>_Answer based on your requirement, not your current setup. Public endpoints on Confluent Cloud are authenticated and encrypted._ | `true` → Yes, private networking is required<br>`false` → No, public endpoints are fine |
| `connects_today` | How do you connect to your cluster today? | `same-vpc` → Same VPC<br>`peered` → Peered<br>`privatelink` → PrivateLink<br>`other` → Other or more than one |
| `cc_egress_required` | Will any of Confluent Cloud's managed connectors or consumers need to connect into your private network?<br>_For example, a connector writing to a private database, or a managed Confluent component calling a private API inside your network._ | `true` → Yes<br>`false` → No |
| `schema_registry` | What Schema Registry does your source environment use? | `none` → None: Schemaless, or schemas kept in app code<br>`glue` → AWS Glue Schema Registry<br>`cp-enterprise-7.1` → Confluent Schema Registry: Enterprise, 7.1 or later: Confluent Platform Enterprise, version 7.1 or later<br>`cp-community` → Confluent Schema Registry: Community, or below 7.1: Community edition, or Confluent Platform below version 7.1<br>`other` → Other, not sure |
| `schema_strategy` | What do you want to do with your schemas on Confluent Cloud? | `migrate` → Migrate my existing schemas<br>`fresh` → Start fresh on Confluent Cloud<br>`schemaless` → Stay schemaless |
| `downtime_tolerance` | What is your target downtime window at cutover? | `window-all` → A scheduled window, all at once<br>`window-sequential` → A scheduled window, one service at a time<br>`minutes` → Minutes per service<br>`seconds` → Seconds per service<br>`zero` → Zero downtime |
| `exceeds_standard_limits` | Does your workload exceed any of the following: 250 megabytes/sec ingress, 750 megabytes/sec egress, or 15,000 requests/sec?<br>_If you exceed any of these, we'll plan for an Enterprise cluster instead of Standard. Enterprise runs on private networking._ | `true` → Yes<br>`false` → No  (default) |
| `target_cloud` | Which cloud should your new Confluent Cloud cluster run on?<br>_This is independent of your source cloud. Confluent supports cross-cloud migrations._ | `aws` → AWS  (default)<br>`azure` → Azure<br>`gcp` → GCP |
| `target_auth` | Which authentication methods should your Confluent Cloud cluster support? Select all that apply.<br>_A cluster can support several at once. On AWS we keep your existing mTLS as-is; on Azure and GCP, mTLS needs a Dedicated cluster, which we plan with you. OAuth and API keys are always set up new._ | `api-keys` → API keys (SASL/PLAIN)<br>`oauth` → OAuth<br>`mtls` → mTLS<br>_Select all that apply, as a list, for example `[api-keys, oauth]`._ |
| `client_coordination` | How much coordination will it take to cut over all your clients and apps at the same time? | `easy` → Low (few clients, one team)<br>`moderate` → Moderate  (default)<br>`hard` → High (many clients and teams) |
| `eos_streams` | Do any applications use exactly-once transactions and/or Kafka Streams? | `eos` → Exactly-once or transactions<br>`kstreams` → Kafka Streams<br>_Select all that apply, as a list, for example `[eos, kstreams]`._ |
| `source_cluster_type` | Source cluster type | `provisioned` → Provisioned<br>`serverless` → Serverless |
| `kafka_version` | What Kafka version does your source cluster run?<br>_Below Kafka 2.4, Cluster Linking isn't available, so we use Confluent Replicator instead._ | `3.0-plus` → 3.0 or newer<br>`2.4-2.9` → 2.4-2.9<br>`older` → Older than 2.4 |
| `partition_band` | Roughly how many partitions do you have, not counting replicas? | `under-2500` → Under 2,500<br>`2500-30000` → 2,500 to 30,000<br>`over-30000` → Over 30,000 |
| `source_auth` | How your Kafka clients authenticate today | `iam` → AWS IAM<br>`scram` → SASL/SCRAM<br>`mtls` → TLS client certificates (mTLS)<br>`unauth` → None / plaintext<br>_Select all that apply, as a list, for example `[iam, scram]`._ |
| `tiered_storage` | Do your topics use tiered storage?<br>_Tiered data is stored separately from your active topics, in Amazon S3, so retrieving it during cutover takes extra time and can add cost._ | `true` → Yes<br>`false` → No |
| `topics_have_custom_settings` | Do any of your topics use non-default settings?<br>_This includes retention over 7 days, a replication factor other than 3, a max message size over 2 MB, or a cleanup policy that combines compact and delete._ | `true` → Yes<br>`false` → No |
| `msk_connect_present` | Do you use MSK Connect? | `true` → Yes<br>`false` → No |
| `self_managed_connectors` | Do you run self-managed Kafka Connect? | `true` → Yes<br>`false` → No |

## Answers by cluster

Every question and its current answer, per cluster. The **Required questions (Action needed)** rows are the ones still to answer; the rest are already filled in (optional defaults, presets, and any earlier answers). Set answers in `plan-inputs.yaml` (wording and options are in [Questions](#questions) above), then re-run `kcp report plan --plan-inputs plan-inputs.yaml` (adjust the paths to where your files are). Answering some may reveal follow-up questions.

### Cluster: your-cluster (answers)

_14 required, 6 optional open. Edit `plan-inputs.yaml` under `your-cluster` to answer._

#### Required questions (Action needed)

> [!IMPORTANT]
> **Action needed.** These required questions are still open. Answer them in `plan-inputs.yaml`; the plan can't complete until you do.

| Answer these (key) |
| --- |
| `use_case_breadth` |
| `move_existing_data` |
| `private_networking_required` |
| `connects_today` |
| `schema_registry` |
| `schema_strategy` |
| `downtime_tolerance` |
| `kafka_version` |
| `partition_band` |
| `source_auth` |
| `tiered_storage` |
| `topics_have_custom_settings` |
| `msk_connect_present` |
| `self_managed_connectors` |

#### Optional questions (defaults pre-selected)

| Default (key: value) |
| --- |
| `cc_egress_required`: `(optional here — doesn't change this cluster's plan)` |
| `exceeds_standard_limits`: `false` |
| `target_cloud`: `aws` |
| `target_auth`: `[]` |
| `client_coordination`: `moderate` |
| `eos_streams`: `[]` |

#### Already answered / preset (edit to override)

| Current (key: value) |
| --- |
| `source_cluster_type`: `provisioned` _(preset)_ |

## Talk to a person

Migrating to Confluent is easier with a hand. Reach a Confluent migration specialist in the Confluent Cloud Migration Hub: https://confluent.cloud/migration-hub

