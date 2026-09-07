# Example: ACLs — IAM (auto-discovery)

Same outcome as [`acls-iam-explicit`](../acls-iam-explicit/), but instead of
naming roles individually, `discoverAllRoles: true` enumerates **every workload
IAM role in the account** — AWS service-linked roles and SSO-provisioned roles
are excluded automatically. Discovered grants are scoped to `clusterArn`: roles
whose policies only grant access to other MSK clusters contribute nothing.

`verifyEffectiveAccess: true` simulates each discovered role's identity policies
and attached permissions boundary via `iam:SimulatePrincipalPolicy`, and keeps
only the grants that are effectively allowed.

`principalArns` and `discoverAllRoles` are mutually exclusive; exactly one is
required whenever `spec.acls.iam` is present.

The shell running `kcp migrate` needs the same AWS credentials as the explicit
case (`iam:GetRole`, `iam:GetPolicy`, `iam:GetPolicyVersion`,
`iam:ListRolePolicies`, `iam:ListAttachedRolePolicies`,
`iam:SimulatePrincipalPolicy`), plus `iam:GetAccountAuthorizationDetails` for
the account-wide enumeration.

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
