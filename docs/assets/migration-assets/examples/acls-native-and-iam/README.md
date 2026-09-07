# Example: ACLs — native + IAM

Migrates ACLs for an MSK cluster that serves **both** SASL/SCRAM principals and
IAM roles at once. `kcp migrate` reads the native Kafka ACLs (for the SASL/SCRAM
principals) from the source, unions them with the IAM-derived grants
(`discoverAllRoles`, scoped to `clusterArn`), deduplicates on the exact ACL
tuple, and writes the result to the target.

`spec.source.credentials` uses SASL/SCRAM (MSK requires **SCRAM-SHA-512**) —
this single connection both reads the source cluster id and reads the native
ACLs.

`spec.serviceAccounts` shows an explicit `mapping` alongside `autoCreate`: the
`User:legacy-app` principal resolves to the pre-existing `sa-xxxxx` service
account, while every other unmapped principal (native or IAM-derived) is
found-or-created via `autoCreate`. An explicit mapping entry always wins over
`autoCreate`.

The shell running `kcp migrate` needs AWS credentials with `iam:GetRole`,
`iam:GetPolicy`, `iam:GetPolicyVersion`, `iam:ListRolePolicies`,
`iam:ListAttachedRolePolicies`, `iam:GetAccountAuthorizationDetails`, and
`iam:SimulatePrincipalPolicy` for the IAM reads and `verifyEffectiveAccess`.

## Credential slots

| Slot | Family | This example | Catalog-link |
|---|---|---|---|
| `spec.source.credentials` | Kafka | SASL/SCRAM (SHA-512) | [`kafka-sasl-scram.yaml`](../../credentials/kafka-sasl-scram.yaml) |
| `spec.target.clusterCredentials` | REST | API key | [`rest-api-key.yaml`](../../credentials/rest-api-key.yaml) |
| `spec.target.cloudCredentials` | REST | API key (distinct Cloud/Global key, same file format) | [`rest-api-key.yaml`](../../credentials/rest-api-key.yaml) |

See the [credential catalog](../../credentials/README.md) for every supported
auth method.

## Run

```bash
kcp migrate validate -f migration.yaml   # structural check
kcp migrate apply -f migration.yaml --dry-run   # preview, change nothing
kcp migrate apply -f migration.yaml             # migrate the ACLs
```

> Credential paths here point at the shared catalog (`../../credentials/…`) to
> avoid duplication. For a real migration, copy the credential files next to your
> manifest and update the paths.
