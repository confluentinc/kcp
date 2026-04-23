---
title: kcp create-asset migrate-acls iam
---

## kcp create-asset migrate-acls iam

Convert IAM ACLs to Confluent Cloud IAM ACLs.

### Synopsis

Convert IAM ACLs from IAM roles or users to Confluent Cloud IAM ACLs as individual Terraform resources.

```
kcp create-asset migrate-acls iam [flags]
```

### Examples

```
  # From an IAM role
  kcp create-asset migrate-acls iam \
      --role-arn arn:aws:iam::123456789012:role/MyKafkaRole \
      --state-file kcp-state.json \
      --cluster-id arn:aws:kafka:us-east-1:XXX:cluster/my-cluster/abc-5 \
      --target-cluster-id lkc-xyz123 \
      --target-rest-endpoint https://lkc-xyz123.eu-west-3.aws.confluent.cloud:443

  # From an IAM user
  kcp create-asset migrate-acls iam \
      --user-arn arn:aws:iam::123456789012:user/app-user \
      --state-file kcp-state.json \
      --cluster-id arn:aws:kafka:us-east-1:XXX:cluster/my-cluster/abc-5 \
      --target-cluster-id lkc-xyz123 \
      --target-rest-endpoint https://lkc-xyz123.eu-west-3.aws.confluent.cloud:443
```

### Options

```
      --cluster-id string             The ARN of the MSK cluster.
  -h, --help                          help for iam
      --output-dir string             The directory where the Confluent Cloud Terraform ACL assets will be written to
      --prevent-destroy               Whether to set lifecycle { prevent_destroy = true } on generated Terraform resources (default true)
      --role-arn string               IAM Role ARN to convert ACLs from
      --skip-audit-report             Skip generating an audit report of the converted ACLs
      --state-file string             The path to the kcp state file.
      --target-cluster-id string      The Confluent Cloud cluster ID (e.g., lkc-xxxxxx).
      --target-rest-endpoint string   The Confluent Cloud cluster REST endpoint (e.g., https://xxx.xxx.aws.confluent.cloud:443).
      --user-arn string               IAM User ARN to convert ACLs from
```

### Options inherited from parent commands

```
      --verbose   Enable verbose logging to console
```

### AWS IAM Permissions

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": [
        "iam:GetRole",
        "iam:GetUser",
        "iam:GetRolePolicy",
        "iam:ListRolePolicies",
        "iam:ListAttachedRolePolicies",
        "iam:GetUserPolicy",
        "iam:ListUserPolicies",
        "iam:ListAttachedUserPolicies",
        "iam:GetPolicy",
        "iam:GetPolicyVersion"
      ],
      "Resource": "*"
    }
  ]
}
```

### SEE ALSO

* [kcp create-asset migrate-acls](kcp_create-asset_migrate-acls.md)	 - Migrate ACLs from MSK to Confluent Cloud

