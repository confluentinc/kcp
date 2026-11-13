# Source Compatibility for kcp Commands

kcp supports two source types, Amazon MSK and Apache Kafka®, and this page shows which kcp commands work with each source flavor: MSK Provisioned and Express, MSK Serverless, and Apache Kafka. Use it to check whether a command supports your source before you plan a migration.

The page covers whether a command works with your source. To learn how a command works, see the [command reference](command-reference/index.md).

## Source flavors

- **MSK Provisioned and Express.** MSK provisioned clusters, including clusters with MSK Express brokers.
- **MSK Serverless.** MSK Serverless clusters.
- **Apache Kafka.** Any source compatible with the Kafka API, including Confluent Platform, that kcp reaches through the Kafka Admin API.

In addition, `kcp migration resources` accepts Confluent Platform as a source. For details, see [Direct API migration](#direct-api-migration).

## Matrix legend

| Marker   | Meaning                                                                                                        |
| :------- | :------------------------------------------------------------------------------------------------------------- |
| Yes      | Fully supported.                                                                                               |
| Limited  | Partially supported. The note in the row says what's missing.                                                  |
| No       | Not supported.                                                                                                 |
| AWS only | Supported when the Apache Kafka source is hosted on AWS. The generated infrastructure assumes AWS networking. |
| N/A      | The command is source-agnostic, so the source type doesn't apply.                                              |

## Compatibility matrix

<div class="matrix" markdown="1">

| Command                                         | MSK Provisioned and Express                                                       | MSK Serverless                                               | Apache Kafka                  |
| :---------------------------------------------- | :-------------------------------------------------------------------------------- | :----------------------------------------------------------- | :---------------------------- |
| `kcp discover`                                  | Yes                                                                               | Limited (some resources aren't discoverable for Serverless)  | No                            |
| `kcp scan client-inventory`                     | Yes                                                                               | No                                                           | No                            |
| `kcp scan clusters`                             | Yes                                                                               | No                                                           | Yes                           |
| `kcp scan msk-connectors`                       | Yes (pass `--metrics-granularity` to also pull per-connector CloudWatch metrics) | Limited (only if MSK Connect connectors target the cluster)  | No                            |
| `kcp scan schema-registry`                      | Yes                                                                               | Yes                                                          | Yes                           |
| `kcp iac bastion-host generate`                 | N/A                                                                               | N/A                                                          | N/A                           |
| `kcp iac acls iam generate`                     | Yes                                                                               | Limited (manual mapping of IAM users or roles)               | No                            |
| `kcp iac acls kafka generate`                   | Yes                                                                               | No                                                           | Yes                           |
| `kcp iac connectors connector-utility generate` | Yes                                                                               | Yes                                                          | Yes                           |
| `kcp iac connectors msk generate`               | Yes                                                                               | Yes                                                          | Yes                           |
| `kcp iac connectors self-managed generate`      | Yes                                                                               | No                                                           | Yes                           |
| `kcp iac schemas generate`                      | Yes                                                                               | Yes                                                          | Yes                           |
| `kcp iac topics generate`                       | Yes                                                                               | No                                                           | Yes                           |
| `kcp iac migration-infra generate --type 1`     | Yes                                                                               | N/A                                                          | AWS only                      |
| `kcp iac migration-infra generate --type 2`     | Yes                                                                               | N/A                                                          | AWS only                      |
| `kcp iac migration-infra generate --type 3`     | Yes                                                                               | N/A                                                          | AWS only                      |
| `kcp iac migration-infra generate --type 4`     | Yes                                                                               | N/A                                                          | AWS only                      |
| `kcp iac migration-infra generate --type 5`     | Yes                                                                               | Yes                                                          | AWS only, requires the IAM JAR |
| `kcp iac target-infra generate`                 | N/A                                                                               | N/A                                                          | N/A                           |
| `kcp migration cutover lag-check`               | Yes                                                                               | No                                                           | Yes                           |
| `kcp migration cutover execute`                 | Yes                                                                               | No                                                           | Yes                           |
| `kcp migration resources create`                | Yes                                                                               | No                                                           | Yes                           |
| `kcp migration resources validate`              | Yes                                                                               | No                                                           | Yes                           |
| `kcp ui`                                        | Yes                                                                               | No                                                           | Yes                           |

</div>

## Direct API migration

The `kcp migration resources` commands, `validate` and `create`, work from a `Migration` manifest. They apply a cluster link, topics, and access control lists (ACLs) to the target through its APIs instead of generating Terraform. Beyond the compatibility matrix, they support the following:

| Aspect                | Support                                                                                                     |
| :-------------------- | :---------------------------------------------------------------------------------------------------------- |
| Sources               | MSK, Apache Kafka, and Confluent Platform                                                                   |
| Targets               | Confluent Cloud and Confluent Platform                                                                      |
| Cluster link          | Destination-initiated, or source-initiated when the source is Confluent Platform                            |
| Topics                | Mirror topics or new topics                                                                                 |
| Service accounts      | Confluent Cloud targets                                                                                     |
| ACLs                  | Kafka ACLs and MSK IAM policies, to Confluent Cloud targets only. kcp rejects `spec.acls` for Confluent Platform targets. |
| Schemas, connectors   | Not supported. Use `kcp iac schemas generate` and `kcp iac connectors ... generate`.                        |
| Jump cluster          | Not supported (migration types 4 and 5). IAM-only sources, such as MSK Serverless, can't use this path.     |
| Target infrastructure | Not supported. Use `kcp iac target-infra generate` and `kcp iac migration-infra generate`.                  |

## Consumer group discovery

`kcp scan clusters` discovers consumer groups by default. To turn discovery off, pass `--skip-consumer-groups`. Support follows the `kcp scan clusters` row of the matrix: discovery works for MSK Provisioned and Express and for Apache Kafka, but not for MSK Serverless, which has no Kafka Admin API.

For every group, kcp records its ID, state, coordinator, and type. The type is the group's KIP-848 protocol, and the types a cluster can report depend on its Kafka version:

| Group type | Introduced           | Notes                                                    |
| :--------- | :------------------- | :------------------------------------------------------- |
| `classic`  | All versions         | The original consumer group protocol. Fully described.   |
| `consumer` | Kafka 4.0 (KIP-848)  | The new consumer rebalance protocol.                     |
| `share`    | Kafka 4.1 (KIP-932)  | Share groups. Requires `group.share.enable` on the broker. |
| `streams`  | Kafka 4.2 (KIP-1071) | Kafka Streams groups.                                    |

Reading the group type requires brokers running Kafka 3.8 and later, which provide version 5 of the `ListGroups` API. On older brokers, kcp reports the type as blank and treats every group as a classic group.

!!! note
    kcp describes only `classic` groups in full, including their members and each member's topic assignments. For `consumer`, `share`, and `streams` groups, kcp records the type, state, and coordinator but not member-level detail, which comes from the newer `ConsumerGroupDescribe` API that kcp doesn't call. kcp flags these groups as having incomplete detail in `kcp-state.json`, and the UI's Consumer Groups tab marks them as partial.

## Confluent Cloud destination

The compatibility matrix covers source support. Separately, three `kcp iac` commands require you to declare the Confluent Cloud destination with `--cc-type`: `commercial` (Standard) or `government` (Confluent Cloud for Government). The three commands are:

- `kcp iac migration-infra generate`
- `kcp iac topics generate`
- `kcp iac schemas generate`

These three commands require `--cc-type`, and it has no default. No other command accepts it.

Confluent Cloud for Government doesn't offer fully managed Cluster Linking or Schema Linking. If you declare `--cc-type government`, kcp refuses the paths that rely on linking before it generates any Terraform:

| Path                                             | `commercial` (Standard) | `government` (Government)               |
| :----------------------------------------------- | :---------------------- | :-------------------------------------- |
| `migration-infra generate` (all `--type` values) | Supported               | Refused. Relies on Cluster Linking.     |
| `topics generate --mode mirror`                  | Supported               | Refused. Use `--mode new` instead.      |
| `topics generate --mode new`                     | Supported               | Supported                               |
| `schemas generate --url` (Schema Exporter)       | Supported               | Refused. Use `--glue-registry` instead. |
| `schemas generate --glue-registry`               | Supported               | Supported                               |

## Related content

- [kcp command reference](command-reference/index.md)
- [Gateway migration limitations](technical-guides/migrations/gateway/limitations.md)
- [Migration assets](migration-assets/README.md)
- [Apache Kafka credentials](apache-kafka-configuration/credentials.md)
- GitHub: [Report a compatibility mismatch](https://github.com/confluentinc/kcp/issues/new/choose)
