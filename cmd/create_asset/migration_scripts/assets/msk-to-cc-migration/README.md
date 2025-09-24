# MSK to Confluent Cloud Migration Scripts


TODO 
This directory contains automated scripts for migrating data from Amazon MSK to Confluent Cloud using cluster linking capabilities.

## Overview

The migration scripts automate the process of creating mirror topics that replicate data from your MSK cluster to Confluent Cloud.

This approach ensures zero-downtime migration with continuous data replication.

## Review the Generated Scripts

This command creates a `migration_scripts` directory containing:

- `msk-to-cc-mirror-topics.sh` - Individual cURL requests to the Confluent Cloud API per topic move data from the AWS MSK cluster to Confluent Cloud.

Before running the scripts, review the generated files:

```shell
cd migration_scripts

# Review the MSK to Confluent Cloud mirror topic scripts
cat msk-to-cc-mirror-topics.sh
```

## Executing the Migration

Create mirror topics between MSK and Confluent Cloud. Run the following script to initiate this process:

```bash
./msk-to-cc-mirror-topics.sh
```

> ⚠️ **IMPORTANT**: Processing time varies based on the number of topics being migrated. Allow the script to run to completion without interruption.

The migration scripts automate the creation of this data flow, ensuring continuous replication from MSK to Confluent Cloud
