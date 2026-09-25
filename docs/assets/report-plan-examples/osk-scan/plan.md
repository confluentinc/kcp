# Migration Plan

Source: Apache Kafka · Generated <example> · kcp 0.0.0-localdev · scanned 2026-06-26 09:46 UTC

## Summary

**1 cluster**. Each cluster gets one migration plan, split into an infrastructure half and an application half. Where a cluster runs multiple apps with different needs, the application half is per app.

| Migration plan | Region | Infrastructure plan | Application plan |
| --- | --- | --- | --- |
| [osk-playground](#cluster-osk-playground) | — | Needs answers | Needs answers |

9 required + 7 optional questions open across all plans.

**Next:** answer the required questions in `plan-inputs.yaml` (see [Answers by cluster](#answers-by-cluster) for what's open), then re-run `kcp report plan --state-file demo-osk-scan.json --plan-inputs plan-inputs.yaml`.

**Prefer a hand?** [Talk to a person](#talk-to-a-person). A Confluent migration specialist can help you build the plan, for free.

## Cluster: osk-playground

**Source:** Apache Kafka · 67 topics · auth: SASL/PLAIN

### Infrastructure plan

> [!IMPORTANT]
> **Needs answers.** The `Pending` rows below unlock once you answer this plan's [Required questions (Action needed)](#cluster-osk-playground-answers).

| Category | Recommendation | Why |
| --- | --- | --- |
| Cluster type | Pending | Answer `private_networking_required`, `source_cloud` (they decide this). |
| Sizing | Pending | Answer `source_cloud` (it decides this). |
| Networking | Pending | Answer `kafka_version`, `move_existing_data`, `private_networking_required`, `source_cloud` (they decide this). |
| Authentication | API keys (SASL/PLAIN) | Default baseline you can change with `target_auth`; Confluent Cloud issues new credentials after cutover. [docs](https://docs.confluent.io/cloud/current/security/authenticate/overview.html) |

**Sized from your scan:** 743 partitions, 45 megabytes/sec peak ingress, 135 megabytes/sec peak egress. Not measured: request rate.

#### Why these recommendations

- **Cluster type**: Pending, decided by `private_networking_required`, `source_cloud`. Answer them and re-run to see this.
- **Sizing**: Pending, decided by `source_cloud`. Answer it and re-run to see this.
- **Networking**: Pending, decided by `kafka_version`, `move_existing_data`, `private_networking_required`, `source_cloud`. Answer them and re-run to see this.
- **Authentication**: After cutover, your clients authenticate to Confluent Cloud with API keys (SASL/PLAIN). This is the default baseline; OAuth and mTLS are also available if you need them. All credentials land as Confluent Cloud service accounts.

### Application plan

> [!IMPORTANT]
> **Needs answers.** The `Pending` rows below unlock once you answer this plan's [Required questions (Action needed)](#cluster-osk-playground-answers).

| Category | Recommendation | Why |
| --- | --- | --- |
| Data migration | Pending | Answer `downtime_tolerance`, `kafka_version`, `move_existing_data`, `private_networking_required` (they decide this). |
| Schema | Pending | Answer `schema_registry_edition`, `schema_strategy` (they decide this). |
| Connectors | Pending | Answer `self_managed_connectors` (it decides this). |
| Topics | Set matching topic settings on Confluent Cloud | Your topics carry over as they are. [docs](https://docs.confluent.io/cloud/current/client-apps/topics/manage.html) |
| Historical data | No separate backfill to plan | Your retained data is small enough to come across with your data migration, so there's no separate historical backfill to plan whichever cutover you choose. [docs](https://docs.confluent.io/cloud/current/multi-cloud/cluster-linking/migrate-cc.html) |

#### Why these recommendations

- **Data migration**: Pending, decided by `downtime_tolerance`, `kafka_version`, `move_existing_data`, `private_networking_required`. Answer them and re-run to see this.
  - **Note:** Found 2 Kafka Streams internal topic(s) (changelog/repartition). Streams apps carry local state. Confirm the exactly-once / Streams answer for the affected applications.
- **Schema**: Pending, decided by `schema_registry_edition`, `schema_strategy`. Answer them and re-run to see this.
- **Connectors**: Pending, decided by `self_managed_connectors`. Answer it and re-run to see this.
- **Topics**: Based on your scan (topics with non-default settings), your topics carry over as they are. Confluent Cloud fixes replication factor at 3, so any topic with a different replication factor is created with a replication factor of 3. Retention and cleanup policy are fully configurable. Set them on your Confluent Cloud topics to match your source, or keep Confluent Cloud's defaults: 7-day retention and a delete cleanup policy. Max message size is also configurable, but capped by cluster type: up to 8 MB on Basic or Standard, up to 20 MB on Enterprise or Dedicated. If any source topic carries messages larger than 8 MB, confirm your target cluster type supports that message size before you migrate. Otherwise, nothing needs changing on your source cluster.
- **Historical data**: Based on your scan (about 50 GB retained), your retained data is small enough to come across with your data migration, so there's no separate historical backfill to plan whichever cutover you choose.

### Migration steps

> [!NOTE]
> Pending: available once the infrastructure plan above is settled.

---

## Questions

Every question, its wording, and its options, listed once. Set answers per cluster (or fleet-wide in `all_clusters:`) in `plan-inputs.yaml`; see **Answers by cluster** for each cluster's current answers and what's still needed.

| Key | Question | Options |
| --- | --- | --- |
| `source_platform` | What is your source platform? | `msk` → Amazon MSK<br>`apache-kafka` → Apache Kafka<br>`confluent-platform` → Confluent Platform |
| `source_cloud` | Which cloud does your source run in?<br>_Where your Kafka runs today. It sets the default target cloud (you can still choose a different one below) and shapes the networking plan._ | `aws` → AWS<br>`azure` → Azure<br>`gcp` → GCP<br>`on-prem` → On-premises or other |
| `use_case_breadth` | How widely is this cluster used? | `one-app` → One team, one application<br>`few-teams` → A few teams or applications<br>`shared-fabric` → Shared fabric across many apps and teams |
| `move_existing_data` | Do you need to move your existing data? | `true` → Yes, move my existing data: Brings your history along. Availability depends on your source setup.<br>`false` → No, we can start fresh: Points producers and consumers at the new cluster and lets the old one retire |
| `private_networking_required` | Is private networking a hard requirement?<br>_Answer based on your requirement, not your current setup. Public endpoints on Confluent Cloud are authenticated and encrypted._ | `true` → Yes, private networking is required<br>`false` → No, public endpoints are fine |
| `cc_egress_required` | Will any of Confluent Cloud's managed connectors or consumers need to connect into your private network?<br>_For example, a connector writing to a private database, or a managed Confluent component calling a private API inside your network._ | `true` → Yes<br>`false` → No |
| `schema_registry_edition` | Which Confluent Schema Registry edition does your source run?<br>_Your scan detected a Confluent Schema Registry. Schema Linking needs Enterprise 7.1 or later._ | `cp-enterprise-7.1` → Enterprise, 7.1 or later: Confluent Platform Enterprise, version 7.1 or later<br>`cp-community` → Community, or below 7.1: Community edition, or Confluent Platform below version 7.1<br>`other` → Other, not sure |
| `schema_strategy` | What do you want to do with your schemas on Confluent Cloud? | `migrate` → Migrate my existing schemas<br>`fresh` → Start fresh on Confluent Cloud<br>`schemaless` → Stay schemaless |
| `downtime_tolerance` | What is your target downtime window at cutover? | `window-all` → A scheduled window, all at once<br>`window-sequential` → A scheduled window, one service at a time<br>`minutes` → Minutes per service<br>`seconds` → Seconds per service<br>`zero` → Zero downtime |
| `exceeds_standard_limits` | Does your workload exceed any of the following: 250 megabytes/sec ingress, 750 megabytes/sec egress, or 15,000 requests/sec?<br>_If you exceed any of these, we'll plan for an Enterprise cluster instead of Standard. Enterprise runs on private networking._ | `true` → Yes<br>`false` → No  (default) |
| `target_cloud` | Which cloud should your new Confluent Cloud cluster run on?<br>_This is independent of your source cloud. Confluent supports cross-cloud migrations._ | `aws` → AWS  (default)<br>`azure` → Azure<br>`gcp` → GCP |
| `target_auth` | Which authentication methods should your Confluent Cloud cluster support? Select all that apply.<br>_A cluster can support several at once. On AWS we keep your existing mTLS as-is; on Azure and GCP, mTLS needs a Dedicated cluster, which we plan with you. OAuth and API keys are always set up new._ | `api-keys` → API keys (SASL/PLAIN)<br>`oauth` → OAuth<br>`mtls` → mTLS<br>_Select all that apply, as a list, for example `[api-keys, oauth]`._ |
| `client_coordination` | How much coordination will it take to cut over all your clients and apps at the same time? | `easy` → Low (few clients, one team)<br>`moderate` → Moderate  (default)<br>`hard` → High (many clients and teams) |
| `eos_streams` | Do any applications use exactly-once transactions and/or Kafka Streams? | `eos` → Exactly-once or transactions<br>`kstreams` → Kafka Streams<br>_Select all that apply, as a list, for example `[eos, kstreams]`._ |
| `kafka_version` | What Kafka version does your source cluster run?<br>_Below Kafka 2.4, Cluster Linking isn't available, so we use Confluent Replicator instead._ | `3.0-plus` → 3.0 or newer<br>`2.4-2.9` → 2.4-2.9<br>`older` → Older than 2.4 |
| `source_auth` | How your Kafka clients authenticate today | `scram` → SASL/SCRAM<br>`sasl-plain` → SASL/PLAIN<br>`mtls` → TLS client certificates (mTLS)<br>`unauth` → None / plaintext<br>_Select all that apply, as a list, for example `[scram, sasl-plain]`._ |
| `tiered_storage` | Do your topics use tiered storage?<br>_Tiered data is stored separately from your active topics, in object storage, so retrieving it during cutover takes extra time and can add cost._ | `true` → Yes<br>`false` → No |
| `topics_have_custom_settings` | Do any of your topics use non-default settings?<br>_This includes retention over 7 days, a replication factor other than 3, a max message size over 2 MB, or a cleanup policy that combines compact and delete._ | `true` → Yes<br>`false` → No |
| `self_managed_connectors` | Do you run self-managed Kafka Connect? | `true` → Yes<br>`false` → No |

## Answers by cluster

Every question and its current answer, per cluster. The **Required questions (Action needed)** rows are the ones still to answer; the rest are already filled in (optional defaults, or facts from your scan and earlier answers). Set answers in `plan-inputs.yaml` (wording and options are in [Questions](#questions) above), then re-run `kcp report plan --state-file demo-osk-scan.json --plan-inputs plan-inputs.yaml` (adjust the paths to where your files are). Answering some may reveal follow-up questions.

### Cluster: osk-playground (answers)

_9 required, 7 optional open. Edit `plan-inputs.yaml` under `osk-playground` to answer._

#### Required questions (Action needed)

> [!IMPORTANT]
> **Action needed.** These required questions are still open. Answer them in `plan-inputs.yaml`; the plan can't complete until you do.

| Answer these (key) |
| --- |
| `source_cloud` |
| `use_case_breadth` |
| `move_existing_data` |
| `private_networking_required` |
| `schema_registry_edition` |
| `schema_strategy` |
| `downtime_tolerance` |
| `kafka_version` |
| `self_managed_connectors` |

#### Optional questions (defaults pre-selected)

| Default (key: value) |
| --- |
| `source_platform`: `(optional here — doesn't change this cluster's plan)` |
| `cc_egress_required`: `(optional here — doesn't change this cluster's plan)` |
| `exceeds_standard_limits`: `false` |
| `target_cloud`: `aws` |
| `target_auth`: `[]` |
| `client_coordination`: `moderate` |
| `eos_streams`: `[]` |

#### Already answered / from your scan (edit to override)

| Current (key: value) |
| --- |
| `source_auth`: `sasl-plain` _(from scan)_ |
| `tiered_storage`: `false` _(from scan)_ |
| `topics_have_custom_settings`: `true` _(from scan)_ |

## Talk to a person

Migrating to Confluent is easier with a hand. Reach a Confluent migration specialist in the Confluent Cloud Migration Hub: https://confluent.cloud/migration-hub

