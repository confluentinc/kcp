---
title: kcp create-asset migrate-acls
---

## kcp create-asset migrate-acls

Migrate ACLs from MSK to Confluent Cloud

### Synopsis

Migrate ACLs (Kafka and IAM) from MSK to executable Terraform assets for Confluent Cloud.

This command provides subcommands to convert both Kafka ACLs and IAM ACLs to Confluent Cloud compatible formats.

### Options

```
  -h, --help   help for migrate-acls
```

### Options inherited from parent commands

```
      --verbose   Enable verbose logging to console
```

### SEE ALSO

* [kcp create-asset](kcp_create-asset.md)	 - Generate infrastructure and migration assets
* [kcp create-asset migrate-acls iam](kcp_create-asset_migrate-acls_iam.md)	 - Convert IAM ACLs to Confluent Cloud IAM ACLs.
* [kcp create-asset migrate-acls kafka](kcp_create-asset_migrate-acls_kafka.md)	 - Convert Kafka ACLs to Confluent Cloud ACLs.

