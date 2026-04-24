---
title: kcp create-asset migrate-topics
---

## kcp create-asset migrate-topics

Create assets for the migrate topics

### Synopsis

Create Terraform files for setting up mirror topics used by the cluster links to migrate data to the target cluster in Confluent Cloud

```
kcp create-asset migrate-topics [flags]
```

### Examples

```
  kcp create-asset migrate-topics \
      --state-file kcp-state.json \
      --source-type msk \
      --cluster-id arn:aws:kafka:us-east-1:XXX:cluster/my-cluster/abc-5 \
      --target-cluster-id lkc-xyz123 \
      --target-rest-endpoint https://lkc-xyz123.eu-west-3.aws.private.confluent.cloud:443 \
      --cluster-link-name msk-to-cc-link
```

### Options

```
      --cluster-id string             The cluster identifier (ARN for MSK, cluster ID from credentials file for OSK).
      --cluster-link-name string      The name of the cluster link that was created as part of the migration (e.g., msk-to-cc-migration-link).
  -h, --help                          help for migrate-topics
      --output-dir string             The directory to output the Terraform files to. (default: 'migrate_topics') (default "migrate_topics")
      --source-type string            Source type: 'msk' or 'osk' (default "msk")
      --state-file string             The path to the kcp state file where the cluster discovery reports have been written to.
      --target-cluster-id string      The Confluent Cloud cluster ID (e.g., lkc-xxxxxx).
      --target-rest-endpoint string   The Confluent Cloud cluster REST endpoint (e.g., https://xxx.xxx.aws.confluent.cloud:443).
```

### Options inherited from parent commands

```
      --verbose   Enable verbose logging to console
```

### SEE ALSO

* [kcp create-asset](index.md)	 - Generate infrastructure and migration assets

