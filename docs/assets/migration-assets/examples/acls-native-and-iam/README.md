# Example: Migrate Native and IAM ACLs Together

This example migrates access control lists (ACLs) for an MSK cluster that serves both SASL/SCRAM principals and IAM roles, by combining its native Apache Kafka® ACLs with the grants derived from IAM.

`kcp migration resources` reads the native Kafka ACLs for the SASL/SCRAM principals from the source, combines them as a union with the IAM-derived grants (`discoverAllRoles`, scoped to `clusterArn`), deduplicates on the exact ACL tuple, and writes the result to the target.

`spec.source.credentials` uses SASL/SCRAM, which must be SCRAM-SHA-512 on MSK. This one connection reads both the source cluster ID and the native ACLs.

`spec.serviceAccounts` shows an explicit `mapping` alongside `autoCreate`. The `User:legacy-app` principal resolves to the existing `sa-xxxxx` service account, and kcp finds or creates a service account for every other unmapped principal, native or IAM-derived, through `autoCreate`. An explicit mapping entry always wins over `autoCreate`.

The shell that runs `kcp migration resources` needs AWS credentials with `iam:GetRole`, `iam:GetPolicy`, `iam:GetPolicyVersion`, `iam:ListRolePolicies`, `iam:ListAttachedRolePolicies`, `iam:GetAccountAuthorizationDetails`, and `iam:SimulatePrincipalPolicy` for the IAM reads and `verifyEffectiveAccess`.

## Credential slots

| Slot | Family | This example | Catalog file |
|---|---|---|---|
| `spec.source.credentials` | Kafka | SASL/SCRAM (SHA-512) | [`kafka-sasl-scram.yaml`](../../credentials/kafka-sasl-scram.yaml) |
| `spec.target.clusterCredentials` | REST | API key | [`rest-api-key.yaml`](../../credentials/rest-api-key.yaml) |
| `spec.target.cloudCredentials` | REST | API key (a separate Cloud or Global key, same file format) | [`rest-api-key.yaml`](../../credentials/rest-api-key.yaml) |

For every supported authentication method, see the [credential file catalog](../../credentials/README.md).

## Run the example

```bash
kcp migration resources validate -f migration.yaml   # structural check
kcp migration resources create -f migration.yaml --dry-run   # preview, change nothing
kcp migration resources create -f migration.yaml             # migrate the ACLs
```

The credential paths in this example point at the shared catalog (`../../credentials/…`) to avoid duplication. For a real migration, copy the credential files next to your manifest and update the paths.

## Related content

- [Example: migrate native ACLs](../acls-native/README.md)
- [Example: migrate ACLs from discovered IAM roles](../acls-iam-discover/README.md)
- [`spec.acls` in the manifest reference](../../manifest-reference.md#specacls)
- [`spec.serviceAccounts` in the manifest reference](../../manifest-reference.md#specserviceaccounts)
- [Credential file catalog for the resources manifest](../../credentials/README.md)
