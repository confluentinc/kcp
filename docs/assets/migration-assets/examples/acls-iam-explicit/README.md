# Example: ACLs — IAM (explicit)

Migrates ACLs for an MSK cluster authorized via the **AWS MSK IAM plane**,
translating a fixed list of named IAM roles. `kcp migrate` reads each role's
identity-based policies and translates its `kafka-cluster:*` grants into
Confluent Cloud ACLs, scoped to `clusterArn`.

`verifyEffectiveAccess: true` simulates each role's identity policies and
attached permissions boundary via `iam:SimulatePrincipalPolicy`, and keeps only
the grants that are effectively allowed.

`principalArns` and `discoverAllRoles` are mutually exclusive; exactly one is
required whenever `spec.acls.iam` is present. This example uses `principalArns`
— see [`acls-iam-discover`](../acls-iam-discover/) for auto-discovery of every
workload role in the account.

The shell running `kcp migrate` needs AWS credentials with `iam:GetRole`,
`iam:GetPolicy`, `iam:GetPolicyVersion`, `iam:ListRolePolicies`, and
`iam:ListAttachedRolePolicies`, plus `iam:SimulatePrincipalPolicy` for
`verifyEffectiveAccess`.

## Credential slots

| Slot | Family | This example | Catalog-link |
|---|---|---|---|
| `spec.source.credentials` | Kafka | IAM | [`kafka-iam.yaml`](../../credentials/kafka-iam.yaml) |
| `spec.target.clusterCredentials` | REST | API key | [`rest-api-key.yaml`](../../credentials/rest-api-key.yaml) |
| `spec.target.cloudCredentials` | REST | API key (distinct Cloud/Global key, same file format) | [`rest-api-key.yaml`](../../credentials/rest-api-key.yaml) |

`spec.source.credentials` here only reads the MSK cluster id over IAM auth — the
IAM authorization-plane reads themselves go through the AWS SDK using the
shell's AWS credentials, not this file. See the
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
