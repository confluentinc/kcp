---
title: kcp create-asset
---

## kcp create-asset

Generate infrastructure and migration assets

### Synopsis

Generate various infrastructure and migration assets including bastion host configurations, data migration tools, and target environment setups.

### Options

```
  -h, --help   help for create-asset
```

### Options inherited from parent commands

```
      --verbose   Enable verbose logging to console
```

### SEE ALSO

* [kcp](kcp.md)	 - A CLI tool for kafka cluster planning and migration
* [kcp create-asset bastion-host](kcp_create-asset_bastion-host.md)	 - Create assets for the bastion host
* [kcp create-asset migrate-acls](kcp_create-asset_migrate-acls.md)	 - Migrate ACLs from MSK to Confluent Cloud
* [kcp create-asset migrate-connectors](kcp_create-asset_migrate-connectors.md)	 - Migrate connectors to Confluent Cloud
* [kcp create-asset migrate-schemas](kcp_create-asset_migrate-schemas.md)	 - Create assets for the migrate schemas
* [kcp create-asset migrate-topics](kcp_create-asset_migrate-topics.md)	 - Create assets for the migrate topics
* [kcp create-asset migration-infra](kcp_create-asset_migration-infra.md)	 - Create migration infrastructure Terraform for a source cluster
* [kcp create-asset target-infra](kcp_create-asset_target-infra.md)	 - Create a target infrastructure asset

