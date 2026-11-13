# Example: Migrate Native ACLs

This example migrates a source cluster's native Apache Kafka® access control lists (ACLs) to Confluent Cloud, with no AWS IAM authorization plane involved.

`kcp migration resources` reads the source's ACLs live through `DescribeAcls`, resolves each distinct principal to a Confluent Cloud service account (`serviceAccounts.autoCreate`), and writes the matching ACLs on the target.

`spec.acls` alone is enough for `create`: neither `spec.clusterLink` nor `spec.topics` is required.

kcp always skips `User:ANONYMOUS` and `User:*`, with a warning, unless you map them explicitly under `spec.serviceAccounts.mapping`. For clarity, this example also excludes `User:ANONYMOUS` from `spec.acls.include`.

## Credential slots

| Slot | Family | This example | Catalog file |
|---|---|---|---|
| `spec.source.credentials` | Kafka | SASL/SCRAM | [`kafka-sasl-scram.yaml`](../../credentials/kafka-sasl-scram.yaml) |
| `spec.target.clusterCredentials` | REST | API key | [`rest-api-key.yaml`](../../credentials/rest-api-key.yaml) |
| `spec.target.cloudCredentials` | REST | API key (a separate Cloud or Global key, same file format) | [`rest-api-key.yaml`](../../credentials/rest-api-key.yaml) |

Any Kafka-family credential works for the source, such as IAM, mTLS, or SASL/PLAIN. `clusterCredentials` and `cloudCredentials` are two separate Confluent Cloud API keys, a Kafka cluster key and a Cloud or Global key, even though this example uses the same credential file format for both. For every format, see the [credential file catalog](../../credentials/README.md).

## Run the example

```bash
kcp migration resources validate -f migration.yaml   # structural check
kcp migration resources create -f migration.yaml --dry-run   # preview, change nothing
kcp migration resources create -f migration.yaml             # migrate the ACLs
```

The credential paths in this example point at the shared catalog (`../../credentials/…`) to avoid duplication. For a real migration, copy the credential files next to your manifest and update the paths.

## Related content

- [Example: migrate native and IAM ACLs together](../acls-native-and-iam/README.md)
- [`spec.acls` in the manifest reference](../../manifest-reference.md#specacls)
- [`spec.serviceAccounts` in the manifest reference](../../manifest-reference.md#specserviceaccounts)
- [Credential file catalog for the resources manifest](../../credentials/README.md)
- [Resources manifest examples](../../README.md#examples)
