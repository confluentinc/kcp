---
title: kcp create-asset migrate-connectors self-managed
---

## kcp create-asset migrate-connectors self-managed

Migrate self-managed connectors to Confluent Cloud

### Synopsis

Generate Terraform configuration that recreates self-managed Kafka Connect connectors (discovered by `kcp scan clusters`) as Confluent Cloud fully-managed connectors.

```
kcp create-asset migrate-connectors self-managed [flags]
```

### Examples

```
  kcp create-asset migrate-connectors self-managed \
      --state-file kcp-state.json \
      --source-type msk \
      --cluster-id arn:aws:kafka:us-east-1:XXX:cluster/my-cluster/abc-5 \
      --cc-environment-id env-a1bcde \
      --cc-cluster-id lkc-xyz123 \
      --cc-api-key ABCDEFGHIJKLMNOP \
      --cc-api-secret xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx
```

### Options

```
      --cc-api-key string          The API key for the Confluent Cloud cluster to migrate connectors to.
      --cc-api-secret string       The API secret for the Confluent Cloud cluster to migrate connectors to.
      --cc-cluster-id string       The ID of the Confluent Cloud cluster to migrate connectors to.
      --cc-environment-id string   The ID of the Confluent Cloud environment to migrate connectors to.
      --cluster-id string          The cluster identifier (ARN for MSK, cluster ID from credentials file for OSK).
  -h, --help                       help for self-managed
      --output-dir string          The directory where the Confluent Cloud Terraform connector assets will be written to
      --source-type string         The source type (msk or osk). (default "msk")
      --state-file string          The path to the kcp state file where the cluster discovery reports have been written to.
```

### Options inherited from parent commands

```
      --verbose   Enable verbose logging to console
```

### SEE ALSO

* [kcp create-asset migrate-connectors](index.md)	 - Migrate connectors to Confluent Cloud

