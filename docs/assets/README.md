# KCP CLI

KCP (Kafka Copy Paste) is a CLI tool for planning and executing Kafka migrations to Confluent Cloud.

> [!NOTE]
> KCP supports migrations from two source types:
>
> - **AWS MSK (Managed Streaming for Kafka)** — full discovery via AWS APIs + Kafka Admin API.
> - **Open Source Kafka (OSK)** — direct scanning via Kafka Admin API.
>
> The workflow differs slightly based on your source type. See the [Command Reference](command-reference/index.md) for per-command specifics.

## Installation

Download the `kcp` binary for your platform from the [latest release on GitHub](https://github.com/confluentinc/kcp/releases/latest). Binaries are published for Linux, macOS, and Windows (amd64 and arm64 where applicable).

### Picking the right architecture

If you're unsure whether to download the `amd64` or `arm64` build:

- **macOS**: run `uname -m` — `arm64` means Apple Silicon (M1/M2/M3/M4); `x86_64` means Intel (use `amd64`).
- **Linux**: run `uname -m` — `aarch64` maps to `arm64`; `x86_64` maps to `amd64`.
- **Windows**: open _System Information_ and check _System type_, or run `echo %PROCESSOR_ARCHITECTURE%` in cmd — `ARM64` maps to `arm64`; `AMD64` maps to `amd64`. Only `amd64` is currently published for Windows.

### Linux / macOS

Resolve the latest tag and pick your platform:

```shell
LATEST_TAG=$(curl -s https://api.github.com/repos/confluentinc/kcp/releases/latest \
  | grep '"tag_name":' | sed -E 's/.*"([^"]+)".*/\1/')

# Pick one:
PLATFORM=darwin_arm64    # Apple Silicon
# PLATFORM=darwin_amd64  # Intel Mac
# PLATFORM=linux_amd64
# PLATFORM=linux_arm64
```

Download, extract, and verify:

```shell
curl -L -o kcp_${LATEST_TAG}.tar.gz \
  "https://github.com/confluentinc/kcp/releases/download/${LATEST_TAG}/kcp_${PLATFORM}.tar.gz"

tar -xzf kcp_${LATEST_TAG}.tar.gz
chmod +x ./kcp/kcp
./kcp/kcp version
```

To run `kcp` from anywhere, move the binary onto your `PATH`:

```shell
sudo mv ./kcp/kcp /usr/local/bin/kcp
kcp version
```

### Windows

Download `kcp_windows_amd64.exe` (single executable) or `kcp_windows_amd64.zip` (packaged archive) from the [releases page](https://github.com/confluentinc/kcp/releases/latest). If you take the zip, extract it and run `kcp.exe`. Optionally move `kcp.exe` to a folder on your `PATH` so you can run `kcp` from any terminal.

Verify with `kcp version`.

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

Each command's per-command AWS IAM permission requirements are documented on its page in the [Command Reference](command-reference/index.md).

> [!NOTE]
> **OSK (Open Source Kafka)** migrations do not require AWS authentication. OSK clusters are accessed directly via Kafka Admin API using the credentials you provide in `osk-credentials.yaml`. See [`kcp scan clusters`](command-reference/scan/clusters.md) for details.

## Workflow

The typical migration flow:

1. **Discover / scan** — `kcp discover` (MSK) or `kcp scan clusters` (MSK or OSK) to build `kcp-state.json`.
2. **Report** — `kcp report costs` and `kcp report metrics` for cost and utilization analysis. Alternatively, use the `kcp ui` for fine-grained analysis.
3. **Generate migration assets for data migration** — `kcp create-asset target-infra`, `migration-infra`, `migrate-topics`, `migrate-schemas`, `migrate-acls`, `migrate-connectors`.
4. **Initialize and execute client switchover** — `kcp migration init` followed by `kcp migration execute`.

The [Getting Started with Zero-Cut Migrations](getting-started-with-zero-cut-migrations.md) guide walks through the end-to-end migration reference, including how KCP fits with the [Confluent Cloud Gateway](https://docs.confluent.io/cloud/current/cp-component/gateway/overview.html).

## Key infrastructure decisions

Before starting, decide on:

1. Is your source Kafka cluster reachable over the public internet, or only from within a private VPC?
2. If private, do you already have a bastion / jump host, or do you need one?
3. What authentication methods are enabled on the source, and which will you use for the migration cluster link?

Only certain migration topologies are possible for a given combination — see [`kcp create-asset migration-infra`](command-reference/create-asset/migration-infra.md) for the type matrix.

### Bastion host requirements

- **Public endpoints** — you can run `kcp` commands directly from your local machine.
- **Private endpoints** — `kcp` must run from inside the source VPC. Either:
  1. Provision a new bastion with [`kcp create-asset bastion-host`](command-reference/create-asset/bastion-host.md), or
  2. Use an existing jump server and copy the `kcp` binary onto it.

> [!NOTE]
> For private MSK, transfer the `kcp` binary to a host inside the same VPC before continuing.

## Command reference

The full CLI reference is generated directly from the Cobra command definitions and lives under **[Command Reference](command-reference/index.md)**. Entry points:

- [`kcp discover`](command-reference/discover.md) — scan AWS for MSK clusters
- [`kcp scan`](command-reference/scan/index.md) — scan a Kafka cluster, S3 broker logs, or a schema registry
- [`kcp report`](command-reference/report/index.md) — generate cost and metrics reports
- [`kcp create-asset`](command-reference/create-asset/index.md) — generate Terraform for target, migration, topic, schema, ACL and connector assets
- [`kcp migration`](command-reference/migration/index.md) — initialize, list, monitor and execute migrations
- [`kcp ui`](command-reference/ui.md) — launch the local web UI
- [`kcp update`](command-reference/update.md) / [`kcp version`](command-reference/version.md) / [`kcp docs`](command-reference/docs.md) — housekeeping

## Related guides

- [Getting Started with Zero-Cut Migrations](getting-started-with-zero-cut-migrations.md)
- [Gateway Switchover Examples](gateway-switchover/index.md)
- [OSK Configuration → OSK credentials](osk-configuration/osk-credentials.md) — schema and worked examples for `osk-credentials.yaml`
- [OSK Configuration → Metrics collection](osk-configuration/metrics-collection.md) — Jolokia and Prometheus design notes for OSK metrics
