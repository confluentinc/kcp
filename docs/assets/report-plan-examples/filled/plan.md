# Migration Plan

Source: Amazon MSK · Generated <example> · kcp 0.0.0-localdev · scanned 2026-09-04 12:00 UTC

## Summary

**3 clusters** across **2 regions**. Each cluster gets one migration plan, split into an infrastructure half and an application half. Where a cluster runs multiple apps with different needs, the application half is per app. 1 routed to a specialist.

| Migration plan | Region | Infrastructure plan | Application plan |
| --- | --- | --- | --- |
| [clickstream](#cluster-clickstream-us-east-1) | us-east-1 | Ready | Ready |
| [orders-prod](#cluster-orders-prod-us-east-1) | us-east-1 | Needs a specialist | (same handoff) |
| [logs-ingest](#cluster-logs-ingest-us-west-2) | us-west-2 | Ready | Ready |

No throughput metrics were scanned, so all sizing below is a lower bound. Run `kcp scan metrics` to size from real ingress/egress before you commit.

0 required + 10 optional questions open across all plans (specialist-routed clusters excluded).

**Prefer a hand?** [Talk to a person](#talk-to-a-person). A Confluent migration specialist can walk through this plan with you, for free.

## Cluster: clickstream (us-east-1)

**Source:** MSK Provisioned · 180 topics · 6 brokers · auth: AWS IAM, SASL/SCRAM

### Infrastructure plan

**Ready**

| Category | Recommendation | Why |
| --- | --- | --- |
| Cluster type | Enterprise | Private networking is a hard requirement you set, and that starts at Enterprise. [docs](https://docs.confluent.io/cloud/current/clusters/cluster-types.html) |
| Sizing | Autoscales, nothing to provision | We size from your partition count (the only signal we have); measured ingress/egress can raise this. [docs](https://docs.confluent.io/cloud/current/clusters/cluster-types.html) |
| Networking | PNI + Egress PrivateLink Endpoint | PNI (Private Network Interface) scales to the full 32 eCKU (elastic Confluent Unit for Kafka), so it grows with you. The Egress PrivateLink Endpoint is added so the migration cluster link can reach your source cluster, which PNI doesn't carry. [docs](https://docs.confluent.io/cloud/current/networking/aws-egress-privatelink-esku.html) |
| Authentication | API keys (SASL/PLAIN) | Default baseline you can change with `target_auth`; Confluent Cloud issues new credentials after cutover. [docs](https://docs.confluent.io/cloud/current/security/authenticate/overview.html) |

**Sized from your scan:** 1,500 partitions. Not measured: ingress throughput, egress throughput, request rate.

#### Why these recommendations

- **Cluster type**: Based on your answer (private networking required), an Enterprise cluster fits: private networking starts at Enterprise, and nothing else you told us pushes it up to Dedicated. Zones: Spread across 3 Availability Zones, with a published uptime SLA up to 99.99% for this cluster type once you provision 2 or more eCKU (elastic Confluent Unit for Kafka). See the cluster-type docs for the SLA terms.
- **Sizing**: Based on your scan (1,500 partitions), we size from your partition count (the only signal we have); measured ingress/egress can raise this. This tier scales with your workload, so there is no capacity for you to pick.
  - **Heads up:** Sizing is a lower bound; see the note at the top of the plan.
- **Networking**: Based on your answer (private networking required), on AWS, we recommend PNI (Private Network Interface) for your private connection. It scales to the full 32 eCKU (elastic Confluent Unit for Kafka), so it grows with you. Confluent Cloud pulls your data over the cluster link, reaching out to your source cluster. PNI doesn't carry Cluster Linking traffic, so the link needs an Egress PrivateLink Endpoint alongside PNI.
- **Authentication**: After cutover, your clients authenticate to Confluent Cloud with API keys (SASL/PLAIN). This is the default baseline; OAuth and mTLS are also available if you need them. All credentials land as Confluent Cloud service accounts.

### Application plan

**Ready**

| Category | Recommendation | Why |
| --- | --- | --- |
| Data migration | Cluster Linking, all at once | We recommend a Cluster Linking cutover. [docs](https://docs.confluent.io/cloud/current/multi-cloud/cluster-linking/migrate-cc.html) |
| Schema | Schema Linking | Your registry can reach Confluent Cloud, so we recommend Schema Linking. [docs](https://docs.confluent.io/cloud/current/sr/schema-linking.html) |
| Connectors | None to move | There is no connector work in this plan. |
| Topics | Topics carry over as they are | Your topics carry over as they are. [docs](https://docs.confluent.io/cloud/current/client-apps/topics/manage.html) |
| Historical data | No separate backfill to plan | The small local window comes across with your data migration, so there's no separate backfill to plan. [docs](https://docs.confluent.io/cloud/current/multi-cloud/cluster-linking/migrate-cc.html) |

#### Why these recommendations

- **Data migration**: Based on your answer (a scheduled window, all at once), we recommend a Cluster Linking cutover. At cutover you stop your producers, let the link finish, then restart them all against Confluent Cloud together, in your scheduled window.
- **Schema**: Based on your answer (Confluent Platform Enterprise 7.1+, migrate your schemas), your registry can reach Confluent Cloud, so we recommend Schema Linking. It preserves your schema IDs, and no client change is needed for the schema move.
- **Connectors**: Based on your answer (no MSK Connect or self-managed Connect), there is no connector work in this plan.
- **Topics**: Based on your answer (topic settings match the defaults), your topics carry over as they are.
- **Historical data**: Based on your scan (no tiered or long-retention history), the small local window comes across with your data migration, so there's no separate backfill to plan. Run `kcp scan metrics` to size the retained data if you want to confirm.

### Migration steps

Build the infrastructure this plan recommends, then move your data over it. `kcp` generates the Terraform for each step; you review it and `terraform apply`.

**Step 1: create the target cluster.** Provision an **Enterprise** cluster in Confluent Cloud. `kcp create-asset target-infra` ([docs](https://confluentinc.github.io/kcp/latest/command-reference/create-asset/target-infra/)) generates the environment, the cluster, and its private networking, or set it up in the [Confluent Cloud Console](https://docs.confluent.io/cloud/current/clusters/create-cluster.html):

```bash
kcp create-asset target-infra \
  --state-file demo-scan.json \
  --source-cluster-id arn:aws:kafka:us-east-1:123456789012:cluster/clickstream/b2 \
  --needs-environment --env-name <your-env-name> \
  --needs-cluster --cluster-name <your-cluster-name> --cluster-type enterprise \
  --needs-private-link --subnet-cidrs <your-subnet-cidrs>
```

Note the new cluster's **environment ID**, **cluster ID**, **bootstrap endpoint**, and **REST endpoint** for the later steps. `--needs-private-link` (with your VPC subnet CIDRs) provisions the private networking. The migration link's Egress PrivateLink Endpoint is added in the link step below. Set up **API keys (SASL/PLAIN)** client authentication on the cluster separately.

**Step 2: build the migration link.** Your data migration runs over the recommended link (**Private source with external outbound cluster link (SASL/SCRAM)**, [`--type 2`](https://confluentinc.github.io/kcp/latest/command-reference/create-asset/migration-infra/)). Your source is private and its brokers use SASL/SCRAM, so the cluster link reaches it privately, over an external outbound endpoint, using your source's SASL/SCRAM credentials. Fill in the placeholders (the cluster you created plus a link name), then `terraform apply`:

```bash
kcp create-asset migration-infra \
  --type 2 \
  --source-type msk \
  --state-file demo-scan.json \
  --cluster-id arn:aws:kafka:us-east-1:123456789012:cluster/clickstream/b2 \
  --cc-type commercial \
  --target-cluster-type enterprise \
  --cluster-link-name <your-link-name> \
  --target-cluster-id <cc-cluster-id> \
  --target-rest-endpoint <cc-rest-endpoint> \
  --target-environment-id <cc-env-id>
```

- `<your-link-name>`: a name you choose for the cluster link, for example `msk-to-cc`.
- `<cc-env-id>`, `<cc-cluster-id>`, `<cc-rest-endpoint>`: from the cluster you created.

Alternative: a jump cluster in your VPC (SASL/SCRAM), if Confluent Cloud can't reach your brokers over an egress endpoint. That is `--type 4`, which needs extra jump-cluster inputs (a bootstrap endpoint, an existing PrivateLink endpoint, and subnet CIDRs). See the [docs](https://confluentinc.github.io/kcp/latest/command-reference/create-asset/migration-infra/).

**Step 3: prepare your source credentials.** What the link needs to sign in to your source, by method:

- **AWS IAM**: Nothing to do for IAM here. The cluster link uses your existing SASL/SCRAM path (above), so your MSK cluster is unchanged for the migration; your IAM clients get new Confluent Cloud credentials when you point them over. Your AWS IAM policies don't carry over automatically. Confluent Cloud uses role-based access control (RBAC) role bindings, which you set up separately (we can help scope this).
- **SASL/SCRAM**: No change. Cluster Linking uses your SCRAM credentials as-is.

**Step 4: create your topics on the target.** Recreate your source topics as mirror topics that the cluster link forwards your data into, with their partition counts and configs, using `kcp create-asset migrate-topics` ([docs](https://confluentinc.github.io/kcp/latest/command-reference/create-asset/migrate-topics/)):

```bash
kcp create-asset migrate-topics \
  --mode mirror \
  --source-type msk \
  --state-file demo-scan.json \
  --cluster-id arn:aws:kafka:us-east-1:123456789012:cluster/clickstream/b2 \
  --cc-type commercial \
  --target-cluster-id <cc-cluster-id> \
  --target-rest-endpoint <cc-rest-endpoint> \
  --cluster-link-name <your-link-name>
```

**Step 5: migrate your schemas.** Copy the source schemas into Confluent Cloud Schema Registry; the Schema recommendation above covers the approach. Run `kcp create-asset migrate-schemas` ([docs](https://confluentinc.github.io/kcp/latest/command-reference/create-asset/migrate-schemas/)):

```bash
kcp create-asset migrate-schemas \
  --url <source-sr-url> \
  --state-file demo-scan.json \
  --cc-type commercial \
  --cc-sr-rest-endpoint <cc-sr-rest-endpoint>
```

**Step 6: run the cutover.** With the link live and the mirror caught up, cut your clients over with `kcp migration` ([docs](https://confluentinc.github.io/kcp/latest/command-reference/migration/)). Cluster Linking syncs your consumer offsets across as it mirrors, so your consumers resume where they left off on Confluent Cloud — no timestamp interceptor needed. Run it in three stages:

1. `kcp migration init` — set up the cutover.
2. `kcp migration lagcheck` — confirm the mirror has caught up to the source (lag is zero).
3. `kcp migration execute` — promote the mirror topics and move your clients to Confluent Cloud.

What changes for each client app at cutover: point it at the new Confluent Cloud **bootstrap endpoint**; switch its security config to **API keys (SASL/PLAIN)** with the new Confluent Cloud credentials; and, for any app that uses Schema Registry, point it at the new Confluent Cloud **Schema Registry URL**.

**Backing out:** until you run `execute`, nothing is committed: the mirror isn't promoted until lag is zero, and your source keeps taking writes until you cut clients over. To roll back, point your clients at the still-running source instead of running `execute`.

**Access control (set up separately).** Your source's authorization rules don't carry over. Map your AWS IAM policies and the Kafka ACLs your SASL/SCRAM principals use to Confluent Cloud RBAC role bindings (or Confluent Cloud ACLs) before cutover, granting each application and service account only the access it needs. This is independent of moving your data — plan it alongside the steps above, not after.

---

## Cluster: orders-prod (us-east-1)

**Source:** MSK Provisioned · 520 topics · 6 brokers · auth: SASL/SCRAM

### Let's size this one together

> [!NOTE]
> This cluster is a better fit for a quick conversation with a Confluent migration specialist than an automated plan, so we've held the recommendation. [Talk to a person](#talk-to-a-person) and we'll size it with you, for free.

#### Why we'd loop in a specialist

- Based on your answer that this cluster is a shared fabric across many apps and teams: shared clusters like yours usually involve more moving parts than an automated plan can account for, so we'd like to talk through the right approach for your footprint. Talk to a specialist and they'll work through it with you.

**Sized from your scan:** 6,000 partitions. Not measured: ingress throughput, egress throughput, request rate.

#### What else the scan showed

- **Tiered storage in use:** Historical data sits in S3. Backfilling it during cutover takes time proportional to the tiered volume.
- **No throughput metrics scanned:** Sizing is based on the partition count alone and is a lower bound. Run `kcp scan metrics` to size from real ingress/egress before you commit: a higher measured throughput can raise the recommended cluster type, and a move to Dedicated changes the private-networking design (PNI is not available on Dedicated).

---

## Cluster: logs-ingest (us-west-2)

**Source:** MSK Provisioned · 45 topics · 6 brokers · auth: SASL/SCRAM

### Infrastructure plan

**Ready**

| Category | Recommendation | Why |
| --- | --- | --- |
| Cluster type | Standard | Public networking works for your workload size. [docs](https://docs.confluent.io/cloud/current/clusters/cluster-types.html) |
| Sizing | Autoscales, nothing to provision | We size from your partition count (the only signal we have); measured ingress/egress can raise this. [docs](https://docs.confluent.io/cloud/current/clusters/cluster-types.html) |
| Networking | Public endpoint | No private networking to set up. [docs](https://docs.confluent.io/cloud/current/networking/overview.html) |
| Authentication | API keys (SASL/PLAIN) | Default baseline you can change with `target_auth`; Confluent Cloud issues new credentials after cutover. [docs](https://docs.confluent.io/cloud/current/security/authenticate/overview.html) |

**Sized from your scan:** 400 partitions. Not measured: ingress throughput, egress throughput, request rate.

#### Why these recommendations

- **Cluster type**: Based on your scan (workload fits Standard) and your answer (private networking not required), a Standard cluster holds your workload and is the simplest fully managed option for it. Moving existing data into a Standard cluster runs on self-managed Confluent Replicator (a Confluent Platform Enterprise license); an Enterprise cluster would migrate over managed Cluster Linking instead — no self-managed worker, no license. Zones: Spread across 3 Availability Zones, with a published uptime SLA up to 99.99% for this cluster type once you provision 2 or more eCKU (elastic Confluent Unit for Kafka). See the cluster-type docs for the SLA terms.
- **Sizing**: Based on your scan (400 partitions), we size from your partition count (the only signal we have); measured ingress/egress can raise this. This tier scales with your workload, so there is no capacity for you to pick.
  - **Heads up:** Sizing is a lower bound; see the note at the top of the plan.
- **Networking**: Based on your answer (private networking not required), a public endpoint is the simplest way in. Moving to private networking later means moving to a different cluster type.
- **Authentication**: After cutover, your clients authenticate to Confluent Cloud with API keys (SASL/PLAIN). This is the default baseline; OAuth and mTLS are also available if you need them. All credentials land as Confluent Cloud service accounts.

### Application plan: analytics-worker

**Ready**

| Category | Recommendation | Why |
| --- | --- | --- |
| Data migration | Confluent Replicator | Cluster Linking needs an Enterprise or Dedicated destination, so it is not available into a Standard cluster. [docs](https://docs.confluent.io/platform/current/multi-dc-deployments/replicator/index.html) |
| Schema | Glue bulk re-registration | Glue schemas use a different wire format and cannot be linked, so we bulk re-register them instead. [docs](https://docs.confluent.io/cloud/current/sr/index.html) |
| Connectors | None to move | There is no connector work in this plan. |
| Topics | Topics carry over as they are | Your topics carry over as they are. [docs](https://docs.confluent.io/cloud/current/client-apps/topics/manage.html) |
| Historical data | No separate backfill to plan | The small local window comes across with your data migration, so there's no separate backfill to plan. [docs](https://docs.confluent.io/platform/current/multi-dc-deployments/replicator/index.html) |

#### Why these recommendations

- **Data migration**: Cluster Linking needs an Enterprise or Dedicated destination, so it is not available into a Standard cluster. We recommend Confluent Replicator to move your existing data. You run the Connect worker yourself for the migration. It needs a license (Confluent Platform Enterprise). Contact us and we'll help you get one. [Talk to a person](#talk-to-a-person) if you'd like a hand.
- **Schema**: Based on your answer (AWS Glue Schema Registry, migrate your schemas), Glue schemas use a different wire format and cannot be linked, so we bulk re-register them instead. This does not preserve schema IDs, so plan a phased client cutover.
- **Connectors**: Based on your answer (no MSK Connect or self-managed Connect), there is no connector work in this plan.
- **Topics**: Based on your answer (topic settings match the defaults), your topics carry over as they are.
- **Historical data**: Based on your scan (no tiered or long-retention history), the small local window comes across with your data migration, so there's no separate backfill to plan. Run `kcp scan metrics` to size the retained data if you want to confirm.

### Application plan: ingest-api

**Ready**

| Category | Recommendation | Why |
| --- | --- | --- |
| Data migration | Confluent Replicator | Cluster Linking needs an Enterprise or Dedicated destination, so it is not available into a Standard cluster. [docs](https://docs.confluent.io/platform/current/multi-dc-deployments/replicator/index.html) |
| Schema | Glue bulk re-registration | Glue schemas use a different wire format and cannot be linked, so we bulk re-register them instead. [docs](https://docs.confluent.io/cloud/current/sr/index.html) |
| Connectors | None to move | There is no connector work in this plan. |
| Topics | Topics carry over as they are | Your topics carry over as they are. [docs](https://docs.confluent.io/cloud/current/client-apps/topics/manage.html) |
| Historical data | No separate backfill to plan | The small local window comes across with your data migration, so there's no separate backfill to plan. [docs](https://docs.confluent.io/platform/current/multi-dc-deployments/replicator/index.html) |

#### Why these recommendations

- **Data migration**: Cluster Linking needs an Enterprise or Dedicated destination, so it is not available into a Standard cluster. We recommend Confluent Replicator to move your existing data. You run the Connect worker yourself for the migration. It needs a license (Confluent Platform Enterprise). Contact us and we'll help you get one. [Talk to a person](#talk-to-a-person) if you'd like a hand.
- **Schema**: Based on your answer (AWS Glue Schema Registry, migrate your schemas), Glue schemas use a different wire format and cannot be linked, so we bulk re-register them instead. This does not preserve schema IDs, so plan a phased client cutover.
- **Connectors**: Based on your answer (no MSK Connect or self-managed Connect), there is no connector work in this plan.
- **Topics**: Based on your answer (topic settings match the defaults), your topics carry over as they are.
- **Historical data**: Based on your scan (no tiered or long-retention history), the small local window comes across with your data migration, so there's no separate backfill to plan. Run `kcp scan metrics` to size the retained data if you want to confirm.

### Migration steps

Your data moves with Confluent Replicator, because Cluster Linking needs an Enterprise or Dedicated cluster. Create the cluster and its structure, run Replicator, then cut over.

**Step 1: create the target cluster.** Create a **Standard** cluster with **Public endpoint** networking in the [Confluent Cloud Console](https://docs.confluent.io/cloud/current/clusters/create-cluster.html). (`kcp create-asset target-infra` provisions Enterprise and Dedicated clusters only.) Set up **API keys (SASL/PLAIN)** client authentication on it, and note its **cluster ID**, **bootstrap endpoint**, and **REST endpoint** for the later steps.

**Step 2: create your topics on the target.** Recreate your source topics as plain topics on the new cluster (no data yet), with their partition counts and configs, using `kcp create-asset migrate-topics` ([docs](https://confluentinc.github.io/kcp/latest/command-reference/create-asset/migrate-topics/)):

```bash
kcp create-asset migrate-topics \
  --mode new \
  --source-type msk \
  --state-file demo-scan.json \
  --cluster-id arn:aws:kafka:us-west-2:123456789012:cluster/logs-ingest/c3 \
  --cc-type commercial \
  --target-cluster-id <cc-cluster-id> \
  --target-rest-endpoint <cc-rest-endpoint>
```

**Step 3: migrate your schemas.** Copy the source schemas into Confluent Cloud Schema Registry; the Schema recommendation above covers the approach. Run `kcp create-asset migrate-schemas` ([docs](https://confluentinc.github.io/kcp/latest/command-reference/create-asset/migrate-schemas/)):

```bash
kcp create-asset migrate-schemas \
  --glue-registry <glue-registry-name> \
  --state-file demo-scan.json \
  --cc-type commercial \
  --cc-sr-rest-endpoint <cc-sr-rest-endpoint>
```

**Step 4: copy your data with Confluent Replicator.** Run Confluent Replicator on a Kafka Connect worker in your own account to copy data from your source into the new cluster ([docs](https://docs.confluent.io/cloud/current/get-started/tutorials/copy-data-cloud.html)). It reads your source with your existing credentials and writes into Confluent Cloud with the target credentials (the client authentication) you set up in Step 1, and it needs a Confluent Platform Enterprise license.

**Step 5: cut over.** Once Replicator has caught up, move your clients to Confluent Cloud. Consumer offsets don't carry over on their own: translate them with the Confluent timestamp interceptor, added to every consumer before you start (Java clients only) ([docs](https://docs.confluent.io/cloud/current/get-started/tutorials/copy-data-cloud.html)).

What changes for each client app at cutover: point it at the new Confluent Cloud **bootstrap endpoint**; switch its security config to **API keys (SASL/PLAIN)** with the new Confluent Cloud credentials; and, for any app that uses Schema Registry, point it at the new Confluent Cloud **Schema Registry URL**.

**Backing out:** your source keeps taking writes and serving your applications until you move the clients, so nothing is committed before cutover. To roll back, leave your clients pointed at the still-running source (or point them back to it) instead of completing the move.

**Access control (set up separately).** Your source's authorization rules don't carry over. Map your source ACLs to Confluent Cloud role-based access control (RBAC) role bindings (or Confluent Cloud ACLs) before cutover, granting each application and service account only the access it needs. This is independent of moving your data — plan it alongside the steps above, not after.

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
| `schema_reachable_to_cc` | Can your Schema Registry reach Confluent Cloud to sync schemas (outbound, port 443)? | `yes` → Yes<br>`no` → No<br>`unsure` → Not sure |
| `downtime_tolerance` | What is your target downtime window at cutover? | `window-all` → A scheduled window, all at once<br>`window-sequential` → A scheduled window, one service at a time<br>`minutes` → Minutes per service<br>`seconds` → Seconds per service<br>`zero` → Zero downtime |
| `exceeds_standard_limits` | Does your workload exceed any of the following: 250 megabytes/sec ingress, 750 megabytes/sec egress, or 15,000 requests/sec?<br>_If you exceed any of these, we'll plan for an Enterprise cluster instead of Standard. Enterprise runs on private networking._ | `true` → Yes<br>`false` → No  (default) |
| `exceeds_enterprise_limits` | Does your workload exceed any of the following: 1,920 megabytes/sec ingress, 5,760 megabytes/sec egress, or 240,000 requests/sec? | `true` → Yes<br>`false` → No  (default) |
| `target_cloud` | Which cloud should your new Confluent Cloud cluster run on?<br>_This is independent of your source cloud. Confluent supports cross-cloud migrations._ | `aws` → AWS  (default)<br>`azure` → Azure<br>`gcp` → GCP |
| `target_auth` | Which authentication methods should your Confluent Cloud cluster support? Select all that apply.<br>_A cluster can support several at once. On AWS we keep your existing mTLS as-is; on Azure and GCP, mTLS needs a Dedicated cluster, which we plan with you. OAuth and API keys are always set up new._ | `api-keys` → API keys (SASL/PLAIN)<br>`oauth` → OAuth<br>`mtls` → mTLS<br>_Select all that apply, as a list, for example `[api-keys, oauth]`._ |
| `client_coordination` | How much coordination will it take to cut over all your clients and apps at the same time? | `easy` → Low (few clients, one team)<br>`moderate` → Moderate  (default)<br>`hard` → High (many clients and teams) |
| `eos_streams` | Do any applications use exactly-once transactions and/or Kafka Streams? | `eos` → Exactly-once or transactions<br>`kstreams` → Kafka Streams<br>_Select all that apply, as a list, for example `[eos, kstreams]`._ |
| `consumer_history_requirement` | Do your consumers need historical data available after migration?<br>_Only relevant when there's history to carry (tiered storage or long retention) and you're moving existing data._ | `true` → Yes  (default)<br>`false` → No |
| `source_cluster_type` | Source cluster type | `provisioned` → Provisioned<br>`serverless` → Serverless |
| `kafka_version` | What Kafka version does your source cluster run?<br>_Below Kafka 2.4, Cluster Linking isn't available, so we use Confluent Replicator instead._ | `3.0-plus` → 3.0 or newer<br>`2.4-2.9` → 2.4-2.9<br>`older` → Older than 2.4 |
| `source_auth` | How your Kafka clients authenticate today | `iam` → AWS IAM<br>`scram` → SASL/SCRAM<br>`mtls` → TLS client certificates (mTLS)<br>`unauth` → None / plaintext<br>_Select all that apply, as a list, for example `[iam, scram]`._ |
| `tiered_storage` | Do your topics use tiered storage?<br>_Tiered data is stored separately from your active topics, in Amazon S3, so retrieving it during cutover takes extra time and can add cost._ | `true` → Yes<br>`false` → No |
| `topics_have_custom_settings` | Do any of your topics use non-default settings?<br>_This includes retention over 7 days, a replication factor other than 3, a max message size over 2 MB, or a cleanup policy that combines compact and delete._ | `true` → Yes<br>`false` → No |
| `msk_connect_present` | Do you use MSK Connect? | `true` → Yes<br>`false` → No |
| `self_managed_connectors` | Do you run self-managed Kafka Connect? | `true` → Yes<br>`false` → No |

## Answers by cluster

Every question and its current answer, per cluster. The **Required questions (Action needed)** rows are the ones still to answer; the rest are already filled in (optional defaults, or facts from your scan and earlier answers). Set answers in `plan-inputs.yaml` (wording and options are in [Questions](#questions) above), then re-run `kcp report plan --state-file demo-scan.json --plan-inputs plan-inputs.yaml` (adjust the paths to where your files are). Answering some may reveal follow-up questions.

### Cluster: clickstream

_0 required, 5 optional open. Edit `plan-inputs.yaml` under `clickstream` to answer._

#### Optional questions (defaults pre-selected)

| Default (key: value) |
| --- |
| `exceeds_standard_limits`: `false` |
| `target_cloud`: `aws` |
| `target_auth`: `[]` |
| `client_coordination`: `moderate` |
| `eos_streams`: `[]` |

#### Already answered / from your scan (edit to override)

| Current (key: value) |
| --- |
| `use_case_breadth`: `few-teams` _(from cluster)_ |
| `move_existing_data`: `true` _(from all clusters)_ |
| `private_networking_required`: `true` _(from cluster)_ |
| `connects_today`: `same-vpc` _(from cluster)_ |
| `cc_egress_required`: `false` _(from cluster)_ |
| `schema_registry`: `cp-enterprise-7.1` _(from cluster)_ |
| `schema_strategy`: `migrate` _(from all clusters)_ |
| `schema_reachable_to_cc`: `yes` _(from cluster)_ |
| `downtime_tolerance`: `window-all` _(from cluster)_ |
| `source_cluster_type`: `provisioned` _(from scan)_ |
| `kafka_version`: `3.0-plus` _(from scan)_ |
| `source_auth`: `iam, scram` _(from scan)_ |
| `tiered_storage`: `false` _(from scan)_ |
| `topics_have_custom_settings`: `false` _(you set this)_ |
| `msk_connect_present`: `false` _(you set this)_ |
| `self_managed_connectors`: `false` _(you set this)_ |

### Cluster: orders-prod

_This cluster is going to a specialist. The answers below are context for that conversation, not blockers to an automated plan._

#### Optional questions (defaults pre-selected)

| Default (key: value) |
| --- |
| `exceeds_enterprise_limits`: `false` |
| `target_cloud`: `aws` |
| `target_auth`: `[]` |
| `client_coordination`: `moderate` |
| `eos_streams`: `[]` |
| `consumer_history_requirement`: `true` |

#### Already answered / from your scan (edit to override)

| Current (key: value) |
| --- |
| `use_case_breadth`: `shared-fabric` _(from cluster)_ |
| `move_existing_data`: `true` _(from all clusters)_ |
| `private_networking_required`: `true` _(from cluster)_ |
| `connects_today`: `same-vpc` _(from cluster)_ |
| `cc_egress_required`: `false` _(from cluster)_ |
| `schema_registry`: `cp-enterprise-7.1` _(from cluster)_ |
| `schema_strategy`: `migrate` _(from all clusters)_ |
| `schema_reachable_to_cc`: `yes` _(from cluster)_ |
| `downtime_tolerance`: `window-all` _(from cluster)_ |
| `source_cluster_type`: `provisioned` _(from scan)_ |
| `kafka_version`: `3.0-plus` _(from scan)_ |
| `source_auth`: `scram` _(from scan)_ |
| `tiered_storage`: `true` _(from scan)_ |
| `topics_have_custom_settings`: `false` _(you set this)_ |
| `msk_connect_present`: `false` _(you set this)_ |
| `self_managed_connectors`: `false` _(you set this)_ |

### Cluster: logs-ingest

_0 required, 5 optional open. Edit `plan-inputs.yaml` under `logs-ingest` to answer._

#### Optional questions (defaults pre-selected)

| Default (key: value) |
| --- |
| `exceeds_standard_limits`: `false` |
| `target_cloud`: `aws` |
| `target_auth`: `[]` |
| `client_coordination`: `moderate` |
| `eos_streams`: `[]` |

#### Already answered / from your scan (edit to override)

| Current (key: value) |
| --- |
| `use_case_breadth`: `few-teams` _(from cluster)_ |
| `move_existing_data`: `true` _(from all clusters)_ |
| `private_networking_required`: `false` _(from cluster)_ |
| `schema_registry`: `glue` _(from cluster)_ |
| `schema_strategy`: `migrate` _(from all clusters)_ |
| `source_cluster_type`: `provisioned` _(from scan)_ |
| `kafka_version`: `2.4-2.9` _(from scan)_ |
| `source_auth`: `scram` _(from scan)_ |
| `tiered_storage`: `false` _(from scan)_ |
| `topics_have_custom_settings`: `false` _(you set this)_ |
| `msk_connect_present`: `false` _(you set this)_ |
| `self_managed_connectors`: `false` _(you set this)_ |

#### Application: analytics-worker

_Effective answers for this app (source shown); edit under `applications:` in plan-inputs.yaml to override._

##### Optional questions (defaults pre-selected)

| Default (key: value) |
| --- |
| `client_coordination`: `moderate` |
| `eos_streams`: `[]` |

##### Already answered / from your scan (edit to override)

| Current (key: value) |
| --- |
| `move_existing_data`: `true` _(from all clusters)_ |
| `schema_registry`: `glue` _(from cluster)_ |
| `schema_strategy`: `migrate` _(from all clusters)_ |

#### Application: ingest-api

_Effective answers for this app (source shown); edit under `applications:` in plan-inputs.yaml to override._

##### Optional questions (defaults pre-selected)

| Default (key: value) |
| --- |
| `client_coordination`: `moderate` |
| `eos_streams`: `[]` |

##### Already answered / from your scan (edit to override)

| Current (key: value) |
| --- |
| `move_existing_data`: `true` _(from all clusters)_ |
| `schema_registry`: `glue` _(from cluster)_ |
| `schema_strategy`: `migrate` _(from all clusters)_ |

## Talk to a person

Migrating to Confluent is easier with a hand. Reach a Confluent migration specialist in the Confluent Cloud Migration Hub: https://confluent.cloud/migration-hub

