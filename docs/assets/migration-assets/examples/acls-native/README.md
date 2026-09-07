# Example: ACLs — native

Migrates the source's **native Kafka ACLs** to Confluent Cloud, with no AWS IAM
plane involved. `kcp migrate` reads the source's ACLs live via `DescribeAcls`,
resolves each distinct principal to a Confluent Cloud service account
(`serviceAccounts.autoCreate`), and writes the matching ACLs on the target.

`spec.acls` alone is enough to apply — no `spec.clusterLink` or `spec.topics` is
required.

`User:ANONYMOUS` and `User:*` are always skipped (with a warning) unless you map
them explicitly under `spec.serviceAccounts.mapping`; this example additionally
excludes `User:ANONYMOUS` from `spec.acls.include` for clarity.

## Credential slots

| Slot | Family | This example | Catalog-link |
|---|---|---|---|
| `spec.source.credentials` | Kafka | SASL/SCRAM | [`kafka-sasl-scram.yaml`](../../credentials/kafka-sasl-scram.yaml) |
| `spec.target.clusterCredentials` | REST | API key | [`rest-api-key.yaml`](../../credentials/rest-api-key.yaml) |
| `spec.target.cloudCredentials` | REST | API key (distinct Cloud/Global key, same file format) | [`rest-api-key.yaml`](../../credentials/rest-api-key.yaml) |

Any Kafka credential works for the source (swap in IAM, mTLS, SASL/PLAIN, …).
`clusterCredentials` and `cloudCredentials` are two separate Confluent Cloud API
keys — a Kafka-cluster key and a Cloud/Global key respectively — even though this
example points both at the same credential file format. See the
[credential catalog](../../credentials/README.md).

## Run

```bash
kcp migrate validate -f migration.yaml   # structural check
kcp migrate apply -f migration.yaml --dry-run   # preview, change nothing
kcp migrate apply -f migration.yaml             # migrate the ACLs
```

> Credential paths here point at the shared catalog (`../../credentials/…`) to
> avoid duplication. For a real migration, copy the credential files next to your
> manifest and update the paths.
