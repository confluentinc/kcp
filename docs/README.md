# KCP CLI

KCP (Kafka Copy) is a CLI tool for planning and executing Kafka migrations to Confluent Cloud.

> [!NOTE]
> KCP supports migrations from two source types:
>
> - **AWS MSK (Managed Streaming for Kafka)** — full discovery via AWS APIs + Kafka Admin API.
> - **Open Source Kafka (OSK)** — direct scanning via Kafka Admin API.
>
> The workflow differs slightly based on your source type. See the [Command Reference](command-reference/kcp.md) for per-command specifics.

## Installation

Download `kcp` from GitHub under the [releases tab](https://github.com/confluentinc/kcp/releases/latest). Binaries are published for Linux, macOS, and Windows (amd64 and arm64 where applicable).

### Linux / macOS

Set a variable for the latest release:

```shell
LATEST_TAG=$(curl -s https://api.github.com/repos/confluentinc/kcp/releases/latest | grep '"tag_name":' | sed -E 's/.*"([^"]+)".*/\1/')
```

Pick your platform:

```shell
PLATFORM=darwin_amd64
# PLATFORM=darwin_arm64
# PLATFORM=linux_amd64
# PLATFORM=linux_arm64
```

Download, extract, and install:

```shell
curl -L -o "kcp_${LATEST_TAG}.tar.gz" "https://github.com/confluentinc/kcp/releases/download/${LATEST_TAG}/kcp_${PLATFORM}.tar.gz"
tar -xzf "kcp_${LATEST_TAG}.tar.gz"
chmod +x ./kcp/kcp
sudo mv ./kcp/kcp /usr/local/bin/kcp
kcp version
```

You should see output similar to:

```text
Executing kcp with build version=0.4.5 commit=a8ef9fd... date=2025-11-13T12:56:00Z
Version: 0.4.5
Commit:  a8ef9fd...
Date:    2025-11-13T12:56:00Z
```

### Windows

Download the latest Windows artifact from the [releases page](https://github.com/confluentinc/kcp/releases/latest):

- `kcp_windows_amd64.exe` — single executable
- `kcp_windows_amd64.zip` — packaged archive

Extract (if zipped), optionally move `kcp.exe` onto your `PATH`, and run `kcp version` to verify.

## Authentication

KCP uses the standard AWS credential chain for any command that calls AWS APIs. Supported auth methods:

- **Environment variables**: `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, and (optionally) `AWS_SESSION_TOKEN`.
- **AWS credentials file**: configure with `aws configure`.
- **AWS SSO / Identity Center**: `aws sso login`.
- **IAM roles**: assumed roles or EC2 instance profiles.
- **Credential helpers**: any tool that writes to the standard AWS credential locations (e.g. `granted`).

Verify with:

```shell
aws sts get-caller-identity
```

Each command's per-command AWS IAM permission requirements are documented on its page in the [Command Reference](command-reference/kcp.md).

> [!NOTE]
> **OSK (Open Source Kafka)** migrations do not require AWS authentication. OSK clusters are accessed directly via Kafka Admin API using the credentials you provide in `osk-credentials.yaml`. See [`kcp scan clusters`](command-reference/kcp_scan_clusters.md) for details.

## Workflow

The typical migration flow:

1. **Discover / scan** — `kcp discover` (MSK) or `kcp scan clusters` (MSK or OSK) to build `kcp-state.json`.
2. **Report** — `kcp report costs` and `kcp report metrics` for cost and utilization analysis.
3. **Generate migration assets** — `kcp create-asset target-infra`, `migration-infra`, `migrate-topics`, `migrate-schemas`, `migrate-acls`, `migrate-connectors`.
4. **Initialize and execute** — `kcp migration init` followed by `kcp migration execute`.

The [Getting Started with Zero-Cut Migrations](getting-started-with-zero-cut-migrations.md) guide walks through the end-to-end migration reference, including how KCP fits with the Confluent Cloud Gateway.

## Key infrastructure decisions

Before starting, decide on:

1. Is your source Kafka cluster reachable over the public internet, or only from within a private VPC?
2. If private, do you already have a bastion / jump host, or do you need one?
3. What authentication methods are enabled on the source, and which will you use for the migration cluster link?

Only certain migration topologies are possible for a given combination — see [`kcp create-asset migration-infra`](command-reference/kcp_create-asset_migration-infra.md) for the type matrix.

### Bastion host requirements

- **Public endpoints** — you can run `kcp` commands directly from your local machine.
- **Private endpoints** — `kcp` must run from inside the source VPC. Either:
  1. Provision a new bastion with [`kcp create-asset bastion-host`](command-reference/kcp_create-asset_bastion-host.md), or
  2. Use an existing jump server and copy the `kcp` binary onto it.

> [!NOTE]
> For private MSK, transfer the `kcp` binary to a host inside the same VPC before continuing.

## Command reference

The full CLI reference is generated directly from the Cobra command definitions and lives under **[Command Reference](command-reference/kcp.md)**. Entry points:

- [`kcp discover`](command-reference/kcp_discover.md) — scan AWS for MSK clusters
- [`kcp scan`](command-reference/kcp_scan.md) — scan a Kafka cluster, S3 broker logs, or a schema registry
- [`kcp report`](command-reference/kcp_report.md) — generate cost and metrics reports
- [`kcp create-asset`](command-reference/kcp_create-asset.md) — generate Terraform for target, migration, topic, schema, ACL and connector assets
- [`kcp migration`](command-reference/kcp_migration.md) — initialize, list, monitor and execute migrations
- [`kcp ui`](command-reference/kcp_ui.md) — launch the local web UI
- [`kcp update`](command-reference/kcp_update.md) / [`kcp version`](command-reference/kcp_version.md) / [`kcp docs`](command-reference/kcp_docs.md) — housekeeping

## Related guides

- [Getting Started with Zero-Cut Migrations](getting-started-with-zero-cut-migrations.md)
- [Gateway Switchover Examples](gateway-switchover-examples.md)
