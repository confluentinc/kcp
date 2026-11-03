# Source compatibility

KCP supports two source types - **AWS MSK** and **Apache Kafka®** - and not every command supports every source flavour today. `kcp migration resources` additionally accepts **Confluent Platform** as a source; see [Direct API migration](#direct-api-migration). This page is the authoritative quick-lookup for which `kcp` subcommands work against which source.

> [!TIP]
> Looking for _how_ a command works? See the [Command Reference](command-reference/index.md). This page covers _whether_ it works for your source.

## Source flavors

- **MSK Provisioned / Express** — AWS MSK provisioned clusters (including MSK Express brokers).
- **MSK Serverless** — AWS MSK Serverless clusters.
- **Apache Kafka** — Any Kafka API compatible source, including **Confluent Platform**, reached via the Kafka Admin API.

## Legend

| Marker       | Meaning                                                                                              |
| :----------- | :--------------------------------------------------------------------------------------------------- |
| **Yes**      | Fully supported.                                                                                     |
| **Limited**  | Partial support — see the inline note on the row for what's missing.                                 |
| **No**       | Not supported.                                                                                       |
| **Coming**   | Planned for an upcoming release.                                                                     |
| **AWS only** | Supported when the Apache Kafka source is hosted on AWS; the generated infrastructure assumes AWS networking. |
| **N/A**      | Command is source-agnostic; the source type does not apply.                                          |

## Compatibility matrix

<div class="matrix" markdown="1">

| Command                                                 | MSK Provisioned/Express | MSK Serverless                         | Apache Kafka                |
| :------------------------------------------------------ | :---------------------- | :------------------------------------- | :-------------------------- |
| `kcp discover`                                          | Yes                     | Limited (some resources not discoverable for Serverless) | No                          |
| `kcp scan client-inventory`                             | Yes                     | No                                     | No                          |
| `kcp scan clusters`                                     | Yes                     | No                                     | Yes                         |
| `kcp scan msk-connectors`                           | Yes (pass `--metrics-granularity` to also pull per-connector CloudWatch metrics) | Limited (only if MSK Connect connectors target the cluster) | No                          |
| `kcp scan schema-registry`                              | Yes                     | Yes                                    | Yes                         |
| `kcp iac bastion-host generate`                         | N/A                     | N/A                                    | N/A                         |
| `kcp iac acls iam generate`                             | Yes                     | Limited (manual IAM user/role mapping) | No                          |
| `kcp iac acls kafka generate`                           | Yes                     | No                                     | Yes                         |
| `kcp iac connectors connector-utility generate`         | Yes                     | Yes                                    | Yes                         |
| `kcp iac connectors msk generate`                       | Yes                     | Yes                                    | Yes                         |
| `kcp iac connectors self-managed generate`              | Yes                     | No                                     | Yes                         |
| `kcp iac schemas generate`                              | Yes                     | Yes                                    | Yes                         |
| `kcp iac topics generate`                               | Yes                     | No                                     | Yes                         |
| `kcp iac migration-infra generate` - Type 1             | Yes                     | N/A                                    | AWS only                    |
| `kcp iac migration-infra generate` - Type 2             | Yes                     | N/A                                    | AWS only                    |
| `kcp iac migration-infra generate` - Type 3             | Yes                     | N/A                                    | AWS only                    |
| `kcp iac migration-infra generate` - Type 4             | Yes                     | N/A                                    | AWS only                    |
| `kcp iac migration-infra generate` - Type 5             | Yes                     | Yes                                    | AWS only - Requires IAM JAR |
| `kcp iac target-infra generate`                         | N/A                     | N/A                                    | N/A                         |
| `kcp migration cutover lag-check`                               | Yes                     | No                                     | Yes                         |
| `kcp migration cutover execute`                                 | Yes                     | No                                     | Yes                         |
| `kcp migration resources create`                        | Yes                     | No                                     | Yes                         |
| `kcp migration resources validate`                      | Yes                     | No                                     | Yes                         |
| `kcp ui`                                                | Yes                     | No                                     | Yes                         |

</div>

## Direct API migration

`kcp migration resources create` and `validate` apply a cluster link, topics, and ACLs to the target through its APIs rather than generating Terraform, driven by a `Migration` manifest. Beyond the matrix above:

| Aspect                | Support                                                                                                  |
| :-------------------- | :------------------------------------------------------------------------------------------------------- |
| Sources               | MSK, Apache Kafka, Confluent Platform                                                                    |
| Targets               | Confluent Cloud, Confluent Platform                                                                      |
| Cluster link          | Destination-initiated, or source-initiated when the source is Confluent Platform                         |
| Topics                | Mirror topics or new topics                                                                              |
| Service accounts      | Confluent Cloud targets                                                                                  |
| ACLs                  | Kafka ACLs and MSK IAM policies, to Confluent Cloud targets only (`spec.acls` is rejected for Confluent Platform targets) |
| Schemas, connectors   | Not supported; use `kcp iac schemas generate` and `kcp iac connectors ... generate`                      |
| Jump cluster          | Not supported (migration types 4 and 5), so IAM-only sources such as MSK Serverless cannot use this path       |
| Target infrastructure | Not supported; use `kcp iac target-infra generate` and `kcp iac migration-infra generate`                |

## Consumer group discovery

`kcp scan clusters` discovers consumer groups by default (disable with `--skip-consumer-groups`). Support mirrors the `kcp scan clusters` row above: available for **MSK Provisioned/Express** and **Apache Kafka**, and **not** for **MSK Serverless** (no Kafka Admin API).

For every group KCP records its id, state, coordinator, and **type**. The type is the group's KIP-848 protocol, and which types a cluster can report depends on its Kafka version:

| Group type  | Introduced           | Notes                                                                  |
| :---------- | :------------------- | :--------------------------------------------------------------------- |
| `classic`   | all versions         | The original consumer-group protocol. Fully described.                 |
| `consumer`  | Kafka 4.0 (KIP-848)  | New consumer rebalance protocol.                                       |
| `share`     | Kafka 4.1 (KIP-932)  | Share groups; requires `group.share.enable` on the broker.            |
| `streams`   | Kafka 4.2 (KIP-1071) | Kafka Streams groups.                                                  |

> [!NOTE]
> Reading the group **type** requires a broker running **Kafka 3.8 or newer** (the `ListGroups` v5 API). Against older brokers the type is reported as blank and every group is treated as classic-protocol.

> [!IMPORTANT]
> Only `classic` groups are **fully described** — their members and per-member topic assignments are captured. `consumer`, `share`, and `streams` groups record their type, state, and coordinator but not member-level detail (that requires the newer `ConsumerGroupDescribe` API, which KCP does not yet call). Such groups are flagged with incomplete detail in `kcp-state.json` and marked "partial" in the UI's Consumer Groups tab.

## Confluent Cloud destination

The matrix above describes _source_ support. Independently, three `kcp iac` commands require a `--cc-type` declaration naming the Confluent Cloud _destination_ — `commercial` (Standard) or `government` (**Confluent Cloud for Government**):

- `kcp iac migration-infra generate`
- `kcp iac topics generate`
- `kcp iac schemas generate`

The declaration is **required** on these three commands (there is no default). It is not accepted on any other command.

**Confluent Cloud for Government** doesn't offer fully managed Cluster Linking or Schema Linking, so the linking-based paths are refused before any Terraform is generated when `--cc-type government` is declared:

| Path                                             | `commercial` (Standard) | `government` (Government)              |
| :----------------------------------------------- | :---------------------- | :-------------------------------------------- |
| `migration-infra generate` (all `--type` values) | Supported       | Refused — relies on Cluster Linking           |
| `topics generate --mode mirror`                  | Supported       | Refused — use `--mode new` instead            |
| `topics generate --mode new`                     | Supported       | Supported                                     |
| `schemas generate --url` (Schema Exporter)       | Supported       | Refused — use `--glue-registry` instead       |
| `schemas generate --glue-registry`               | Supported       | Supported                                     |

---

If a row here doesn't match what you're seeing in practice, please open an [issue](https://github.com/confluentinc/kcp/issues/new/choose).
