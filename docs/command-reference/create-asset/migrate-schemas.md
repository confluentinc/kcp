---
title: kcp create-asset migrate-schemas
---

## kcp create-asset migrate-schemas

Create assets for the migrate schemas

### Synopsis

Create assets to enable the migration of schemas to Confluent Cloud.
Supports both Confluent Schema Registry (--url) and AWS Glue Schema Registry (--glue-registry) sources.

```
kcp create-asset migrate-schemas [flags]
```

### Examples

```
  # From a Confluent Schema Registry (uses schema exporter resources)
  kcp create-asset migrate-schemas \
      --state-file kcp-state.json \
      --url https://my-schema-registry.example.com \
      --cc-sr-rest-endpoint https://psrc-xxxxx.us-east-2.aws.confluent.cloud

  # From an AWS Glue Schema Registry (generates confluent_schema resources)
  kcp create-asset migrate-schemas \
      --state-file kcp-state.json \
      --glue-registry my-glue-registry \
      --region us-east-1 \
      --cc-sr-rest-endpoint https://psrc-xxxxx.us-east-2.aws.confluent.cloud
```

### Options

```
      --cc-sr-rest-endpoint string   The REST endpoint of the Confluent Cloud target schema registry.
      --glue-registry string         The name of an AWS Glue Schema Registry to migrate schemas from (uses confluent_schema resources).
  -h, --help                         help for migrate-schemas
      --output-dir string            The output directory for the generated assets. (default "migrate_schemas")
      --region string                The AWS region of the Glue Schema Registry (required when the same registry name exists in multiple regions).
      --schemas string               Comma-separated list of schema names to migrate (default: all schemas). Only applies with --glue-registry.
      --state-file string            The path to the kcp state file where the MSK cluster discovery reports have been written to.
      --url string                   The URL of a Confluent Schema Registry to migrate schemas from (uses schema exporter).
```

### Options inherited from parent commands

```
      --verbose   Enable verbose logging to console
```

### SEE ALSO

* [kcp create-asset](index.md)	 - Generate infrastructure and migration assets

