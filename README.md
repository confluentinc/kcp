# KCP CLI

[![FOSSA Status](https://app.fossa.com/api/projects/custom%2B65%2Fgithub.com%2Fconfluentinc%2Fkcp.svg?type=shield&issueType=license)](https://app.fossa.com/projects/custom%2B65%2Fgithub.com%2Fconfluentinc%2Fkcp?ref=badge_shield&issueType=license) [![FOSSA Status](https://app.fossa.com/api/projects/custom%2B65%2Fgithub.com%2Fconfluentinc%2Fkcp.svg?type=shield&issueType=security)](https://app.fossa.com/projects/custom%2B65%2Fgithub.com%2Fconfluentinc%2Fkcp?ref=badge_shield&issueType=security)

This repository is part of the Confluent organization on GitHub.
It is public and open to contributions from the community.

Please see the LICENSE file for contribution terms.
Please see the CHANGELOG.md for details of recent updates.

---

<div align="center">

**A comprehensive command-line tool for planning and executing Kafka migrations to Confluent Cloud.**

</div>

---

## Table of Contents

- [Overview](#overview)
- [Documentation](#documentation)
- [Installation](#installation)
- [Upgrading](#upgrading)
- [Contributing](#contributing)
- [Resources and Support](#resources-and-support)

## Overview

**Mission**: Simplify and streamline your Kafka migration journey to Confluent Cloud!

kcp helps you migrate your Kafka setups to Confluent Cloud by providing tools to:

- **Scan** scan and identify resources in existing Kafka deployments.
- **Create** reports for migration planning and cost analysis.
- **Generate** migration assets and infrastructure configurations.
- **Migrate** execute end-to-end migrations with real-time offset monitoring and resumable workflows.

### Key Features

| Feature                     | Description                                                                              |
| --------------------------- | ---------------------------------------------------------------------------------------- |
| **Multiple Auth Methods**   | Support for SASL-IAM, SASL-SCRAM, TLS, and unauthenticated.                              |
| **Comprehensive Reporting** | Detailed migration planning and cost analysis.                                           |
| **Infrastructure as Code**  | Generate Terraform and Ansible configurations to seamlessly migrate to Confluent Cloud.  |
| **Migration Execution**     | FSM-driven migration workflow with lag monitoring, gateway fencing, and topic promotion. |
| **Private VPC Deployments** | Migrate to Confluent Cloud from private networks and isolated environments.              |

## Documentation

Full documentation is published at **[confluentinc.github.io/kcp](https://confluentinc.github.io/kcp/)**, with a version selector for each release. The docs for the latest release are also linked from the [latest release page](https://github.com/confluentinc/kcp/releases/latest).

## Installation

> [!IMPORTANT]
> Install a **released binary** as described below. Do not build from `main` for normal use — `main` is an in-development branch and is not a defined release.

kcp ships pre-built binaries with every [GitHub release](https://github.com/confluentinc/kcp/releases/latest): macOS and Linux (amd64/arm64), and Windows (amd64).

### macOS & Linux

**Recommended — install script.** Detects your OS and architecture, downloads the latest stable release, verifies its checksum, and installs it onto your `PATH`:

```bash
curl -fsSL https://raw.githubusercontent.com/confluentinc/kcp/main/install.sh | sh
```

To pin a specific version or change the install directory:

```bash
# Install a specific release
curl -fsSL https://raw.githubusercontent.com/confluentinc/kcp/main/install.sh | KCP_VERSION=v0.8.5 sh

# Install somewhere other than /usr/local/bin
curl -fsSL https://raw.githubusercontent.com/confluentinc/kcp/main/install.sh | KCP_INSTALL_DIR="$HOME/.local/bin" sh
```

**Manual download.** Prefer to do it by hand? Run `uname -m` if unsure of your architecture (`arm64`/`aarch64` → `arm64`; `x86_64` → `amd64`):

```bash
# Apple Silicon: darwin_arm64. Intel Mac: darwin_amd64. Linux: linux_amd64 or linux_arm64.
PLATFORM=darwin_arm64
LATEST_TAG=$(curl -s https://api.github.com/repos/confluentinc/kcp/releases/latest \
  | grep '"tag_name":' | sed -E 's/.*"([^"]+)".*/\1/')

curl -L -o kcp "https://github.com/confluentinc/kcp/releases/download/${LATEST_TAG}/kcp_${PLATFORM}"
chmod +x kcp
sudo mv kcp /usr/local/bin/kcp
kcp version
```

### Windows

Download `kcp_windows_amd64.exe` from the [latest release](https://github.com/confluentinc/kcp/releases/latest), rename it to `kcp.exe`, move it to a folder on your `PATH`, then verify in PowerShell:

```powershell
kcp version
```

## Upgrading

kcp can update itself in place:

```bash
kcp update              # check for and install the latest release
kcp update --check-only # report the latest version without installing
sudo kcp update         # for system-wide installs (e.g. /usr/local/bin)
```

## Contributing

This repository is public and open to community contributions. To build from source, run the tests, or cut a release, see **[CONTRIBUTING.md](CONTRIBUTING.md)**. Please also see the [LICENSE](LICENSE) for contribution terms.

## Resources and Support

- [Kafka Migration Guide](https://www.confluent.io/resources/white-paper/migrate-from-kafka-to-confluent/)
- [Migration Hub on Confluent Cloud](https://confluent.cloud/migration-hub)
- [Talk to a migration expert from Confluent](https://meetings.salesloft.com/confluentinc/confluent-migration-assistance)

