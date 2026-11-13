# Migrate to Confluent Cloud With kcp

kcp is a command-line tool that plans and runs migrations from Amazon MSK and Apache Kafka® to Confluent Cloud. This page covers how to install kcp, how it authenticates to AWS, the migration workflow, and the infrastructure decisions to make before you start.

## Source types

kcp supports two source types:

- **Amazon MSK (Amazon Managed Streaming for Apache Kafka).** kcp discovers clusters through AWS APIs and the Kafka Admin API.
- **Apache Kafka.** This includes Confluent Platform and other distributions compatible with the Kafka API. kcp scans these clusters directly through the Kafka Admin API.

The workflow differs slightly by source type. To check which commands support your source (MSK Provisioned and Express, MSK Serverless, or Apache Kafka), see [Source compatibility for kcp commands](source-compatibility.md). For per-command details, see the [command reference](command-reference/index.md).

## Install kcp

On macOS or Linux, install the latest stable release with the install script. The script detects your operating system and architecture, downloads the matching binary, verifies its checksum, and places it on your `PATH`:

```shell
curl -fsSL https://raw.githubusercontent.com/confluentinc/kcp/main/install.sh | sh
```

To pin a version, set `KCP_VERSION=v0.8.5`. To change the target directory, set `KCP_INSTALL_DIR="$HOME/.local/bin"`.

Every [release on GitHub](https://github.com/confluentinc/kcp/releases/latest) publishes binaries for macOS, Linux (amd64 and arm64), and Windows (amd64). Each release includes these assets: `kcp_darwin_amd64`, `kcp_darwin_arm64`, `kcp_linux_amd64`, `kcp_linux_arm64`, and `kcp_windows_amd64.exe`.

!!! warning
    Always install a released binary. A build from the `main` branch is an unreleased development build. If you intend to build from source, see [Contributing to kcp](https://github.com/confluentinc/kcp/blob/main/CONTRIBUTING.md) on GitHub.

If you're unsure of your architecture, run `uname -m`. An output of `arm64` or `aarch64` needs the `arm64` build, and `x86_64` needs the `amd64` build. Apple silicon Macs use `arm64`, and older Intel Macs use `amd64`.

### Download kcp manually

To download kcp without the install script, for example for a headless or scripted install, run the commands for your operating system:

=== "macOS"

    ```shell
    # Apple Silicon: PLATFORM=darwin_arm64. Intel: PLATFORM=darwin_amd64.
    PLATFORM=darwin_arm64
    LATEST_TAG=$(curl -s https://api.github.com/repos/confluentinc/kcp/releases/latest \
      | grep '"tag_name":' | sed -E 's/.*"([^"]+)".*/\1/')

    curl -L -o kcp "https://github.com/confluentinc/kcp/releases/download/${LATEST_TAG}/kcp_${PLATFORM}"
    chmod +x kcp
    ./kcp version

    # Optional: put kcp on your PATH so you can run it from any directory
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
    ./kcp version

    # Optional: put kcp on your PATH so you can run it from any directory
    sudo mv kcp /usr/local/bin/kcp
    kcp version
    ```

=== "Windows"

    Download [`kcp_windows_amd64.exe`](https://github.com/confluentinc/kcp/releases/latest) from the releases page, move it to a folder on your `PATH`, and run `kcp version` to verify it.

## Authenticate to AWS

kcp uses the standard AWS credential chain for any command that calls AWS APIs. It supports these authentication methods:

- **Environment variables.** `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, and optionally `AWS_SESSION_TOKEN`.
- **AWS credentials file.** Configure it with `aws configure`.
- **AWS SSO or IAM Identity Center.** Sign in with `aws sso login`.
- **IAM roles.** Assumed roles or EC2 instance profiles.
- **Credential helpers.** Any tool that writes to the standard AWS credential locations, such as `granted`.

To verify your credentials, run:

```shell
aws sts get-caller-identity
```

Each command's page in the [command reference](command-reference/index.md) lists the AWS IAM permissions that command needs.

Apache Kafka migrations don't need AWS authentication. kcp connects to Apache Kafka clusters directly through the Kafka Admin API, using the credentials you provide in `apache-kafka-credentials.yaml`. For details, see [`kcp scan clusters`](command-reference/scan/clusters.md).

## Migration workflow

A typical migration has four stages:

1. **Discover and scan.** Run `kcp discover` (MSK) or `kcp scan clusters` (MSK or Apache Kafka) to build `kcp-state.json`. For a step-by-step runbook, see [Get started with kcp](getting-started.md).
2. **Report.** Run `kcp report costs` and `kcp report metrics` for cost and utilization analysis, or use `kcp ui` for fine-grained analysis.
3. **Generate migration assets.** Prepare the data migration in one of two ways:
    - To generate Terraform, use `kcp iac <asset> generate` with `target-infra`, `migration-infra`, `topics`, or `schemas`, plus `kcp iac acls kafka|iam generate` and `kcp iac connectors msk|self-managed|connector-utility generate`.
    - To create cluster links, topics, and access control lists (ACLs) directly on the destination cluster through its REST API, use `kcp migration resources`.
4. **Cut over clients.** Run `kcp migration cutover execute`. It validates and runs the cutover in one command, and if an earlier run was interrupted, it continues from the live state. To validate without running the cutover, pass `--dry-run`.

## Plan your migration infrastructure

Before you start, answer these questions:

1. Is your source Kafka cluster reachable over the public internet, or only from inside a virtual private cloud (VPC)?
2. If it's private, do you already have a bastion or jump host, or do you need one?
3. Which authentication methods are enabled on the source, and which one do you plan to use for the migration cluster link?

Your answers limit which migration topologies are possible. For the matrix of migration types, see [`kcp iac migration-infra generate`](command-reference/iac/migration-infra/generate.md).

### Bastion host requirements

- **Public endpoints.** You can run `kcp` commands directly from your local machine.
- **Private endpoints.** `kcp` must run from inside the source VPC, on one of these hosts:
    - A new bastion host, which you can provision with [`kcp iac bastion-host generate`](command-reference/iac/bastion-host/generate.md).
    - An existing jump server, after you copy the `kcp` binary onto it.

For a private MSK cluster, copy the `kcp` binary to a host inside the same VPC before you continue.

## Command reference

The [command reference](command-reference/index.md) covers every kcp command and is generated from the command definitions. Start with these commands:

- [`kcp discover`](command-reference/discover.md): scans AWS for MSK clusters.
- [`kcp scan`](command-reference/scan/index.md): scans a Kafka cluster, S3 broker logs, or a schema registry.
- [`kcp report`](command-reference/report/index.md): generates cost and metrics reports.
- [`kcp iac`](command-reference/iac/index.md): generates Terraform for target, migration, topic, schema, ACL, and connector assets.
- [`kcp migration`](command-reference/migration/index.md): cuts clients over to Confluent Cloud through Confluent Gateway (`cutover`) and creates Kafka resources directly (`resources`).
- [`kcp ui`](command-reference/ui.md): launches the local web UI.
- [`kcp update`](command-reference/update.md), [`kcp version`](command-reference/version.md), and [`kcp docs`](command-reference/docs.md): housekeeping commands.

## Next steps

- [Get started with kcp](getting-started.md): install kcp, scan your clusters, and review the result.

## Related content

- [Run kcp in Docker](running-kcp-in-docker.md)
- [Source compatibility for kcp commands](source-compatibility.md)
- [Apache Kafka credentials](apache-kafka-configuration/credentials.md)
- [Apache Kafka metrics collection](apache-kafka-configuration/metrics-collection.md)
