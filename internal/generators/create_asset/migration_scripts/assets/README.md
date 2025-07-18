# Migration Scripts

This directory contains automated scripts for migrating data from Amazon MSK to Confluent Cloud using cluster linking capabilities.

## Overview

The migration scripts automate the process of creating mirror topics that replicate data from your MSK cluster to Confluent Cloud. The migration follows a two-phase approach:

1. **Phase 1**: MSK → Confluent Platform
2. **Phase 2**: Confluent Platform → Confluent Cloud

This approach ensures zero-downtime migration with continuous data replication.

## Review the Generated Scripts

This command creates a `migration-scripts` directory containing:

- `msk-to-cp-mirror-topics.sh` - Individiual `kafka-mirror` commands per topic to move data from MSK to the Confluent Platform jump cluster.
- `destination-cluster-properties` - Kafka client configuration file.
- `cp-to-cc-mirror-topics.sh` - Individual cURL requests to the Confluent Cloud API per topic move data from the Confluent Platform jump cluster to Confluent Cloud.

Before running the scripts, review the generated files:

```shell
cd migration-scripts

# Review the MSK to Confluent Platform mirror topic scripts
cat msk-to-cp-mirror-topics.sh

# Review the Confluent Platform to Confluent Cloud mirror topic scripts
cat cp-to-cc-mirror-topics.sh

# Review the destination cluster configuration
cat destination-cluster.properties
```

## Executing the Migration

### Phase 1: MSK to Confluent Platform

Begin by establishing mirror topics between MSK and Confluent Platform. Run the following script to initiate this process:

```bash
./msk-to-cp-mirror-topics.sh
```

This script establishes mirror topics on the Confluent Platform brokers that continuously replicate data from your MSK cluster.

> ⚠️ **IMPORTANT**: Processing time varies based on the number of topics being migrated. Allow the script to run to completion without interruption.

### Phase 2: Confluent Platform to Confluent Cloud

Once topic mirroring from MSK to Confluent Platform is complete, proceed with replicating data to Confluent Cloud:

```bash
./cp-to-cc-mirror-topics.sh
```

This script establishes mirror topics on Confluent Cloud that continuously replicate data from your Confluent Platform brokers.

> ⚠️ **IMPORTANT**: Processing time varies based on the number of topics being migrated. Allow the script to run to completion without interruption.

## Architecture Overview

```
┌─────────────────┐    ┌──────────────────┐    ┌─────────────────┐
│   MSK Cluster   │───►│    Confluent     │───►│ Confluent Cloud │
│                 │    │    Platform      │    │                 │
│  ┌───────────┐  │    │    Brokers       │    │  ┌───────────┐  │
│  │  Topic A  │──┼───►│                  │───►│  │  Topic A  │  │
│  └───────────┘  │    │ ┌──────────────┐ │    │  └───────────┘  │
│  ┌───────────┐  │    │ │    Mirror    │ │    │  ┌───────────┐  │
│  │  Topic B  │──┼───►│ │    Topics    │ │───►│  │  Topic B  │  │
│  └───────────┘  │    │ └──────────────┘ │    │  └───────────┘  │
│  ┌───────────┐  │    │                  │    │  ┌───────────┐  │
│  │  Topic C  │──┼───►│                  │───►│  │  Topic C  │  │
│  └───────────┘  │    │                  │    │  └───────────┘  │
└─────────────────┘    └──────────────────┘    └─────────────────┘
```

The migration scripts automate the creation of this data flow, ensuring continuous replication from MSK to Confluent Cloud through the Confluent Platform brokers.
