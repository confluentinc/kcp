# Source compatibility

KCP supports two source types - **AWS MSK** and **Open Source Kafka (OSK)** - and not every command supports every source flavour today. This page is the authoritative quick-lookup for which `kcp` subcommands work against which source.

> [!TIP]
> Looking for _how_ a command works? See the [Command Reference](command-reference/index.md). This page covers _whether_ it works for your source.

## Source flavors

- **MSK Provisioned / Express** — AWS MSK provisioned clusters (including MSK Express brokers).
- **MSK Serverless** — AWS MSK Serverless clusters.
- **OSK** — Any Kafka API compatible source, reached via the Kafka Admin API.

## Legend

| Marker       | Meaning                                                                                              |
| :----------- | :--------------------------------------------------------------------------------------------------- |
| **Yes**      | Fully supported.                                                                                     |
| **Limited**  | Partial support — see the inline note on the row for what's missing.                                 |
| **No**       | Not supported.                                                                                       |
| **Coming**   | Planned for an upcoming release.                                                                     |
| **AWS only** | Supported when the OSK source is hosted on AWS; the generated infrastructure assumes AWS networking. |
| **N/A**      | Command is source-agnostic; the source type does not apply.                                          |

## Compatibility matrix

<div class="matrix" markdown="1">

| Command                                                 | MSK Provisioned/Express | MSK Serverless                         | OSK                         |
| :------------------------------------------------------ | :---------------------- | :------------------------------------- | :-------------------------- |
| `kcp discover`                                          | Yes                     | Limited                                | No                          |
| `kcp scan client-inventory`                             | Yes                     | No                                     | No                          |
| `kcp scan clusters`                                     | Yes                     | No                                     | Yes                         |
| `kcp scan schema-registry`                              | Yes                     | Yes                                    | Yes                         |
| `kcp create-asset bastion-host`                         | N/A                     | N/A                                    | N/A                         |
| `kcp create-asset migrate-acls iam`                     | Yes                     | Limited (manual IAM user/role mapping) | No                          |
| `kcp create-asset migrate-acls kafka`                   | Yes                     | No                                     | Yes                         |
| `kcp create-asset migrate-connectors connector-utility` | Yes                     | Yes                                    | Yes                         |
| `kcp create-asset migrate-connectors msk`               | Yes                     | Yes                                    | Yes                         |
| `kcp create-asset migrate-connectors self-managed`      | Yes                     | No                                     | Yes                         |
| `kcp create-asset migrate-schemas`                      | Yes                     | Yes                                    | Yes                         |
| `kcp create-asset migrate-topics`                       | Yes                     | No                                     | Yes                         |
| `kcp create-asset migration-infra` - Type 1             | Yes                     | N/A                                    | AWS only                    |
| `kcp create-asset migration-infra` - Type 2             | Yes                     | N/A                                    | AWS only                    |
| `kcp create-asset migration-infra` - Type 3             | Yes                     | N/A                                    | AWS only                    |
| `kcp create-asset migration-infra` - Type 4             | Yes                     | N/A                                    | AWS only                    |
| `kcp create-asset migration-infra` - Type 5             | Yes                     | Yes                                    | AWS only - Requires IAM JAR |
| `kcp create-asset target-infra`                         | N/A                     | N/A                                    | N/A                         |
| `kcp migration init`                                    | Yes                     | No                                     | Yes                         |
| `kcp migration lag-check`                               | Yes                     | No                                     | Yes                         |
| `kcp migration execute`                                 | Yes                     | No                                     | Yes                         |
| `kcp migration list`                                    | Yes                     | No                                     | Yes                         |
| `kcp ui`                                                | Yes                     | No                                     | Yes                         |

</div>

## Confluent Cloud destination

The matrix above describes _source_ support. Independently, three `create-asset` commands require a `--cc-environment` declaration naming the Confluent Cloud _destination_ — `cc` (Standard) or `cc-gov` (**Confluent Cloud for Government**):

- `kcp create-asset migration-infra`
- `kcp create-asset migrate-topics`
- `kcp create-asset migrate-schemas`

The declaration is **required** on these three commands (there is no default). It is not accepted on any other command.

**Confluent Cloud for Government** does not provide Cluster Linking or Schema Linking, so the linking-based paths are refused before any Terraform is generated when `--cc-environment cc-gov` is declared:

| Path                                            | `cc` (Standard) | `cc-gov` (Government)                          |
| :---------------------------------------------- | :-------------- | :-------------------------------------------- |
| `migration-infra` (all `--type` values)         | Supported       | Refused — relies on Cluster Linking           |
| `migrate-topics --mode mirror`                  | Supported       | Refused — use `--mode new` instead            |
| `migrate-topics --mode new`                     | Supported       | Supported                                     |
| `migrate-schemas --url` (Schema Exporter)       | Supported       | Refused — use `--glue-registry` instead       |
| `migrate-schemas --glue-registry`               | Supported       | Supported                                     |

---

If a row here doesn't match what you're seeing in practice, please open an [issue](https://github.com/confluentinc/kcp/issues/new/choose).
