# KCP CLI

KCP is a CLI tool for planning and executing Apache Kafka® migrations to Confluent Cloud.

> [!NOTE]
> KCP supports migrations from two source types:
>
> - **AWS MSK (Managed Streaming for Apache Kafka)** — full discovery via AWS APIs + Kafka Admin API.
> - **Apache Kafka** — direct scanning via Kafka Admin API.
>
> The workflow differs slightly based on your source type. See the [Command Reference](command-reference/index.md) for per-command specifics, or the [Source Compatibility](source-compatibility.md) matrix for which commands support which source flavor (MSK Provisioned/Express, MSK Serverless, Apache Kafka).

## Installation

> [!TIP]
> **Recommended:** on macOS or Linux, install the latest stable release with the install script. It detects your OS/architecture, downloads the matching binary, verifies its checksum, and places it on your `PATH`:
>
> ```shell
> curl -fsSL https://raw.githubusercontent.com/confluentinc/kcp/main/install.sh | sh
> ```
>
> Pin a version with `KCP_VERSION=v0.8.5` or change the target directory with `KCP_INSTALL_DIR="$HOME/.local/bin"`.

Binaries are published for macOS, Linux (amd64/arm64), and Windows (amd64) with every [release on GitHub](https://github.com/confluentinc/kcp/releases/latest).

> [!IMPORTANT]
> Always install a released binary. Building from the `main` branch produces an unreleased, in-development build — see [CONTRIBUTING](https://github.com/confluentinc/kcp/blob/main/CONTRIBUTING.md) if you intend to build from source.

Run `uname -m` if you're unsure of your architecture: `arm64` / `aarch64` → `arm64` build, `x86_64` → `amd64` build. Apple Silicon Macs are `arm64`; older Intel Macs are `amd64`.

To download manually instead of using the install script, the tabs below show the equivalent terminal flow for headless or scripted installs.

=== "macOS"

    ```shell
    # Apple Silicon: PLATFORM=darwin_arm64. Intel: PLATFORM=darwin_amd64.
    PLATFORM=darwin_arm64
    LATEST_TAG=$(curl -s https://api.github.com/repos/confluentinc/kcp/releases/latest \
      | grep '"tag_name":' | sed -E 's/.*"([^"]+)".*/\1/')

    curl -L -o kcp "https://github.com/confluentinc/kcp/releases/download/${LATEST_TAG}/kcp_${PLATFORM}"
    chmod +x kcp
    sudo mv kcp /usr/local/bin/kcp
    kcp version
    ```

=== "Linux"

    ```shell
    # PLATFORM=linux_amd64 or linux_arm64.
    PLATFORM=linux_amd64
    LATEST_TAG=$(curl -s https://api.github.com/repos/confluentinc/kcp/releases/latest \
      | grep '"tag_name":' | sed -E 's/.*"([^"]+)".*/\1/')

    curl -L -o kcp "https://github.com/confluentinc/kcp/releases/download/${LATEST_TAG}/kcp_${PLATFORM}"
    chmod +x kcp
    sudo mv kcp /usr/local/bin/kcp
    kcp version
    ```

=== "Windows"

    Download [`kcp_windows_amd64.exe`](https://github.com/confluentinc/kcp/releases/latest) from the releases page, move it onto a folder on your `PATH`, and verify with `kcp version`.

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
> **Apache Kafka** migrations do not require AWS authentication. Apache Kafka clusters are accessed directly via Kafka Admin API using the credentials you provide in `apache-kafka-credentials.yaml`. See [`kcp scan clusters`](command-reference/scan/clusters.md) for details.

## Workflow

The typical migration flow:

1. **Discover / scan** — `kcp discover` (MSK) or `kcp scan clusters` (MSK or Apache Kafka) to build `kcp-state.json`.
2. **Report** — `kcp report costs` and `kcp report metrics` for cost and utilization analysis. Alternatively, use the `kcp ui` for fine-grained analysis.
3. **Generate migration assets for data migration** — `kcp create-asset target-infra`, `migration-infra`, `migrate-topics`, `migrate-schemas`, `migrate-acls`, `migrate-connectors`.
4. **Initialize and execute client switchover** — `kcp cutover init` followed by `kcp cutover execute`.

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
- [`kcp cutover`](command-reference/cutover/index.md) — initialize, list, monitor and execute migrations
- [`kcp ui`](command-reference/ui.md) — launch the local web UI
- [`kcp update`](command-reference/update.md) / [`kcp version`](command-reference/version.md) / [`kcp docs`](command-reference/docs.md) — housekeeping

## Related guides

- [Getting Started with Zero-Cut Migrations](getting-started-with-zero-cut-migrations.md)
- [Gateway Switchover Examples](gateway-switchover/index.md)
- [Apache Kafka configuration → Credentials](apache-kafka-configuration/credentials.md) — schema and worked examples for `apache-kafka-credentials.yaml`
- [Apache Kafka configuration → Metrics collection](apache-kafka-configuration/metrics-collection.md) — Jolokia and Prometheus design notes for Apache Kafka metrics
