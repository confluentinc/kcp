# Example: Migrate ACLs From Named IAM Roles

This example migrates access control lists (ACLs) for an MSK cluster that uses the MSK IAM authorization plane, by translating the grants of a fixed list of named IAM roles into Confluent Cloud ACLs.

`kcp migration resources` reads each role's identity-based policies and translates its `kafka-cluster:*` grants into Confluent Cloud ACLs, scoped to `clusterArn`.

`verifyEffectiveAccess: true` simulates each role's identity policies and attached permissions boundary through `iam:SimulatePrincipalPolicy`, and keeps only the grants that are effectively allowed.

`principalArns` and `discoverAllRoles` are mutually exclusive, and exactly one is required whenever `spec.acls.iam` is present. This example uses `principalArns`. To discover every workload role in the account automatically, see [Example: migrate ACLs from discovered IAM roles](../acls-iam-discover/README.md).

The shell that runs `kcp migration resources` needs AWS credentials with `iam:GetRole`, `iam:GetPolicy`, `iam:GetPolicyVersion`, `iam:ListRolePolicies`, and `iam:ListAttachedRolePolicies`, plus `iam:SimulatePrincipalPolicy` for `verifyEffectiveAccess`.

## Credential slots

| Slot | Family | This example | Catalog file |
|---|---|---|---|
| `spec.source.credentials` | Kafka | IAM | [`kafka-iam.yaml`](../../credentials/kafka-iam.yaml) |
| `spec.target.clusterCredentials` | REST | API key | [`rest-api-key.yaml`](../../credentials/rest-api-key.yaml) |
| `spec.target.cloudCredentials` | REST | API key (a separate Cloud or Global key, same file format) | [`rest-api-key.yaml`](../../credentials/rest-api-key.yaml) |

In this example, `spec.source.credentials` only reads the MSK cluster ID over IAM authentication. The IAM authorization plane reads go through the AWS SDK with the shell's AWS credentials, not this file. For every format, see the [credential file catalog](../../credentials/README.md).

## Run the example

```bash
kcp migration resources validate -f migration.yaml   # structural check
kcp migration resources create -f migration.yaml --dry-run   # preview, change nothing
kcp migration resources create -f migration.yaml             # migrate the ACLs
```

The credential paths in this example point at the shared catalog (`../../credentials/…`) to avoid duplication. For a real migration, copy the credential files next to your manifest and update the paths.

## Related content

- [Example: migrate ACLs from discovered IAM roles](../acls-iam-discover/README.md)
- [`spec.acls.iam` in the manifest reference](../../manifest-reference.md#specaclsiam)
- [Credential file catalog for the resources manifest](../../credentials/README.md)
- [Resources manifest examples](../../README.md#examples)
