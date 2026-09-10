# Migration Plan

Source: Amazon MSK · Generated 2026-09-01 12:00 UTC · kcp 0.0.0-localdev · scanned 2026-09-01 12:00 UTC

## Cluster: orders-prod (us-east-1)

**Source:** MSK Provisioned · 143 topics · 6 brokers · auth: scram

### Recommendation

| Category | Recommendation | Why |
| --- | --- | --- |
| Cluster type | Enterprise | Private networking is required for your workload. [docs](https://docs.confluent.io/cloud/current/clusters/cluster-types.html) |
| Sizing | Autoscales, nothing to provision (Band 2) |  |
| Networking | PNI + Egress PrivateLink Endpoint | It scales to the full 32 eCKU, so it grows with you. The Egress PrivateLink Endpoint is added so the migration Cluster Link can reach your source cluster, which PNI doesn’t carry. [docs](https://docs.confluent.io/cloud/current/networking/aws-egress-privatelink-esku.html) |
| Authentication | API keys (SASL/PLAIN) | Confluent Cloud issues new credentials for this method after cutover. [docs](https://docs.confluent.io/cloud/current/security/authenticate/overview.html) |
| Data migration | Cluster Linking, service by service (Stop-Restart-Repeat) | [docs](https://docs.confluent.io/cloud/current/multi-cloud/cluster-linking/migrate-cc.html) |
| Schema | Schema Linking | [docs](https://docs.confluent.io/cloud/current/sr/schema-linking.html) |
| Connectors | Rebuild as Confluent-managed connectors | [docs](https://docs.confluent.io/cloud/current/connectors/index.html) |
| Topics | Topics should mirror as they are |  |
| Historical data | Cluster Linking backfills all history |  |

#### Why these recommendations

- **Cluster type** — An Enterprise cluster fits your needs. You need private networking, which starts at Enterprise, and nothing else you told us pushes it up to Dedicated. Zones: Spread across three Availability Zones, with an uptime SLA of up to 99.99% (at 2 or more eCKU). The cluster keeps serving through the loss of a zone.
- **Sizing** — We size from the most demanding of the numbers you gave us. Here that’s your partition count. This tier scales with your workload, so there is no capacity for you to pick.
- **Networking** — On AWS, we recommend PNI (Private Network Interface) for your private connection. It scales to the full 32 eCKU, so it grows with you. Confluent Cloud pulls your data over the Cluster Link, reaching out to your source cluster. PNI doesn’t carry Cluster Linking traffic, so the link needs an Egress PrivateLink Endpoint alongside PNI.
- **Authentication** — After cutover, your clients authenticate to Confluent Cloud with API keys (SASL/PLAIN). All credentials land as Confluent Cloud service accounts.
- **Data migration** — We recommend a Cluster Linking cutover. At cutover you stop your producers, let the link finish, then restart them against Confluent Cloud, when you are ready rather than all at once. Your source credentials: No change. Cluster Linking uses your SCRAM credentials as-is.

#### Networking trade-offs

**PNI + Egress PrivateLink Endpoint** — AWS deployments that want the most room to grow

- + Scales to the full 32 eCKU, the top of this tier
- + Traffic stays inside the Availability Zone when your clients are zone-aligned
- + Private, one-way access

- − Available on AWS only
- − Not available on Dedicated clusters
- − More complex to set up

#### Data migration

We recommend a Cluster Linking cutover. At cutover you stop your producers, let the link finish, then restart them against Confluent Cloud, when you are ready rather than all at once. Your source credentials: No change. Cluster Linking uses your SCRAM credentials as-is.

**Action:** Create cluster link

**KCP migration infrastructure:** https://confluentinc.github.io/kcp/latest/command-reference/create-asset/migration-infra/

#### Schema

Your Confluent Platform Enterprise 7.1+ registry can reach Confluent Cloud, so we recommend Schema Linking. It preserves your schema IDs, and no client change is needed for the schema move.

#### Connectors

Connectors do not travel with your topics. Whichever way you go, you set each one up again on the other side. You are on MSK Connect today, so each connector is recreated as a Confluent-managed connector. Confluent’s Connect Migration Utility copies each connector’s configuration across, so you are not retyping them.

### Observations

- ℹ️ **Tiered storage in use** — Historical data sits in S3. Backfilling it during cutover takes time proportional to the tiered volume and can add S3 re-fetch cost.
- ℹ️ **No throughput metrics scanned** — Sizing is based on the partition count alone and is a lower bound. Run `kcp scan metrics` to size from real ingress/egress.

### Talk to a person

Your answers land inside what we can plan. This plan is yours to run. Talk to us if you want a second opinion.

---

## Actions needed — answer in plan-inputs.yaml

Answers are per cluster. Fill in the open questions below, then re-run `kcp report plan --plan-inputs plan-output/plan-inputs.yaml`. Answering some may reveal follow-up questions.

### Cluster: orders-prod

_0 required, 5 optional open._

#### Optional — defaults pre-selected

| Default (key: token) | Question | Options (token → meaning) |
| --- | --- | --- |
| 🟡 exceeds_enterprise_limits: false | Does your workload exceed any of the following: 1,920 megabytes/sec ingress, 5,760 megabytes/sec egress, or 240,000 requests per second? | true → Yes<br>false → No  (default) |
| 🟡 target_cloud: aws | Which cloud should your new Confluent Cloud cluster run on?<br>_This is independent of your source cloud. Confluent supports cross-cloud migrations._ | aws → AWS  (default)<br>azure → Azure<br>gcp → GCP |
| 🟡 target_auth:  | Which authentication methods should your Confluent Cloud cluster support? Select all that apply.<br>_A cluster can support several at once. On AWS we keep your existing mTLS as-is; on Azure and GCP, mTLS needs a Dedicated cluster, which we plan with you. OAuth and API keys are always set up new._ | api-keys → API keys (SASL/PLAIN)<br>oauth → OAuth<br>mtls → mTLS |
| 🟡 client_coordination: moderate | How much coordination will it take to cut over all your clients and apps at the same time? | easy → Low (few clients, one team)<br>moderate → Moderate  (default)<br>hard → High (many clients and teams) |
| 🟡 eos_streams:  | Do any applications use exactly-once transactions and/or Kafka Streams? | eos → Exactly-once or transactions<br>kstreams → Kafka Streams |

#### Already answered / from your scan — edit to override

| Current (key: token) | Question | Options (token → meaning) |
| --- | --- | --- |
| ✅ private_networking_required: true | Is private networking a hard requirement?<br>_Answer based on your requirement, not your current setup. Public endpoints on Confluent Cloud are authenticated and encrypted._ | true → Yes, private networking is required<br>false → No, public endpoints are fine |
| ✅ use_case_breadth: few-teams | How widely is this cluster used? | one-app → One team, one application<br>few-teams → A few teams or applications<br>shared-fabric → Shared fabric across many apps and teams |
| ✅ move_existing_data: true | Do you need to move your existing data? | true → Yes, move my existing data — Brings your history along. Availability depends on your source setup.<br>false → No, we can start fresh — Points producers and consumers at the new cluster and lets the old one retire |
| ✅ connects_today: same-vpc | How do you connect to your cluster today? | same-vpc → Same VPC<br>peered → Peered<br>privatelink → PrivateLink<br>other → Other or more than one |
| ✅ cc_egress_required: false | Will any of Confluent Cloud’s managed connectors or consumers need to connect into your private network?<br>_For example, a connector writing to a private database, or a managed Confluent component calling a private API inside your network._ | true → Yes<br>false → No |
| ✅ downtime_tolerance: minutes | What is your target downtime window at cutover? | window-all → A scheduled window, all at once<br>window-sequential → A scheduled window, one service at a time<br>minutes → Minutes per service<br>seconds → Seconds per service<br>zero → Zero downtime |
| ✅ schema_registry: cp-enterprise-7.1 | What Schema Registry does your source environment use? | none → None — Schemaless, or schemas kept in app code<br>glue → AWS Glue Schema Registry<br>cp-enterprise-7.1 → Confluent Schema Registry: Enterprise, 7.1 or later — Confluent Platform Enterprise, version 7.1 or later<br>cp-community → Confluent Schema Registry: Community, or below 7.1 — Community edition, or Confluent Platform below version 7.1<br>other → Other, not sure |
| ✅ schema_strategy: migrate | What do you want to do with your schemas on Confluent Cloud? | migrate → Migrate my existing schemas<br>fresh → Start fresh on Confluent Cloud<br>schemaless → Stay schemaless |
| ✅ schema_reachable_to_cc: yes | Can your Schema Registry reach Confluent Cloud to sync schemas (outbound, port 443)? | yes → Yes<br>no → No<br>unsure → Not sure |
| ✅ topics_have_custom_settings: false | Do any of your topics use non-default settings?<br>_This includes retention over 7 days, a replication factor other than 3, a max message size over 2 MB, or a cleanup policy that combines compact and delete._ | true → Yes<br>false → No |
| ✅ connector_destination: confluent-managed | Do you want to keep your connectors self-managed, or move them to Confluent-managed? | self-managed → Keep self-managed — Continue to run your own Connect cluster, which is new infrastructure to stand up if you’re moving off MSK Connect<br>confluent-managed → Move to Confluent-managed — Confluent runs the connector for you — no Connect cluster to stand up, scale, or patch  (default) |
| ✅ consumer_history_requirement: required | Do your consumers need historical data available after migration? | required → Yes  (default)<br>not-required → No |
| 🔎 source_cluster_type: provisioned | Source cluster type (from your scan) | provisioned → Provisioned<br>serverless → Serverless |
| 🔎 kafka_version: 3.0-plus | Source Kafka version (from your scan) | 3.0-plus → 3.0 or newer<br>2.4-2.9 → 2.4–2.9<br>older → Older than 2.4 |
| 🔎 source_auth: scram | How your Kafka clients authenticate today (from your scan) | iam → AWS IAM<br>scram → SASL/SCRAM<br>mtls → TLS client certificates (mTLS)<br>unauth → None / plaintext |
| 🔎 tiered_storage: true | Does this cluster use tiered storage? (from your scan) | true → Yes<br>false → No |
| 🔎 msk_connect_present: true | Do you use MSK Connect? (from your scan) | true → Yes<br>false → No |
| 🔎 self_managed_connectors: false | Do you run self-managed Kafka Connect? (from your scan) | true → Yes<br>false → No |

