---
title: kcp create-asset migrate-connectors connector-utility
---

## kcp create-asset migrate-connectors connector-utility

Export discovered connector configs for the Connect Migration Utility

### Synopsis

Emit a JSON file per MSK cluster containing its discovered connector configs, in the format expected by the [Connect Migration Utility](https://github.com/confluentinc/connect-migration-utility).

```
kcp create-asset migrate-connectors connector-utility [flags]
```

### Examples

```
  # All MSK clusters in the state file
  kcp create-asset migrate-connectors connector-utility --state-file kcp-state.json

  # Single cluster, custom output directory
  kcp create-asset migrate-connectors connector-utility \
      --state-file kcp-state.json \
      --cluster-id arn:aws:kafka:us-east-1:XXX:cluster/my-cluster/abc-5 \
      --output-dir connector-configs
```

### Options

```
      --cluster-id string   The ARN of the MSK cluster to generate the connector configs JSON from.
  -h, --help                help for connector-utility
      --output-dir string   The directory where the connector configs JSON will be written to
      --state-file string   The path to the kcp state file where the cluster discovery reports have been written to.
```

### Options inherited from parent commands

```
      --verbose   Enable verbose logging to console
```

### SEE ALSO

* [kcp create-asset migrate-connectors](kcp_create-asset_migrate-connectors.md)	 - Migrate connectors to Confluent Cloud

