---
title: kcp create-asset migrate-acls kafka
---

## kcp create-asset migrate-acls kafka

Convert Kafka ACLs to Confluent Cloud ACLs.

### Synopsis

Convert Kafka ACLs to Confluent Cloud ACLs as individual Terraform resources.

```
kcp create-asset migrate-acls kafka [flags]
```

### Examples

```
  kcp create-asset migrate-acls kafka \
      --state-file kcp-state.json \
      --source-type msk \
      --cluster-id arn:aws:kafka:us-east-1:XXX:cluster/my-cluster/abc-5 \
      --target-cluster-id lkc-xyz123 \
      --target-rest-endpoint https://lkc-xyz123.eu-west-3.aws.confluent.cloud:443
```

### Options

```
      --cluster-id string             The cluster identifier (ARN for MSK, cluster ID from credentials file for OSK).
  -h, --help                          help for kafka
      --output-dir string             The directory where the Confluent Cloud Terraform ACL assets will be written to
      --prevent-destroy               Whether to set lifecycle { prevent_destroy = true } on generated Terraform resources (default true)
      --skip-audit-report             Skip generating an audit report of the converted ACLs
      --source-type string            The source type (msk or osk). (default "msk")
      --state-file string             The path to the kcp state file where the cluster discovery reports have been written to.
      --target-cluster-id string      The Confluent Cloud cluster ID (e.g., lkc-xxxxxx).
      --target-rest-endpoint string   The Confluent Cloud cluster REST endpoint (e.g., https://xxx.xxx.aws.confluent.cloud:443).
```

### Options inherited from parent commands

```
      --verbose   Enable verbose logging to console
```

### SEE ALSO

* [kcp create-asset migrate-acls](index.md)	 - Migrate ACLs from MSK to Confluent Cloud

