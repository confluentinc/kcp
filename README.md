# KCP CLI

**A command-line tool for planning and executing Kafka migrations to Confluent Cloud.**

Scan existing Kafka deployments, generate migration plans and cost reports, create infrastructure-as-code assets, and execute end-to-end migrations with real-time monitoring.

> **[Full documentation](https://confluentinc.github.io/kcp/)** · **[Latest release](https://github.com/confluentinc/kcp/releases/latest)**

## Installation

> [!CAUTION]
> Do not build from `main` for normal use — `main` is an in-development branch and is not a defined release. **Always install a released binary** for normal use, using the methods below. Builds from `main` are untested and may contain breaking changes.

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

1. Download `kcp_windows_amd64.exe` from the [latest release](https://github.com/confluentinc/kcp/releases/latest).
2. Rename it to `kcp.exe`.
3. Create a folder to keep it in, for example `C:\Program Files\kcp`, and move `kcp.exe` there.
4. Add that folder to your `PATH` so you can run `kcp` from any terminal: open the Start menu, search for **"Edit environment variables for your account"**, select **Path**, click **Edit → New**, paste the folder path, then **OK**.
5. Open a **new** PowerShell window (so it picks up the updated `PATH`) and verify:

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

## Overview

| Feature                     | Description                                                                              |
| --------------------------- | ---------------------------------------------------------------------------------------- |
| **Multiple Auth Methods**   | SASL-IAM, SASL-SCRAM, SASL/PLAIN, TLS, and unauthenticated.                             |
| **Comprehensive Reporting** | Migration planning and cost analysis reports.                                            |
| **Infrastructure as Code**  | Generate Terraform and Ansible configurations for Confluent Cloud.                       |
| **Migration Execution**     | FSM-driven workflow with lag monitoring, gateway fencing, and topic promotion.            |
| **Private VPC Deployments** | Migrate from private networks and isolated environments.                                 |

## Contributing

This repository is public and open to community contributions. To build from source, run the tests, or submit a pull request, see **[CONTRIBUTING.md](CONTRIBUTING.md)**.

## Resources and Support

- [Kafka Migration Guide](https://www.confluent.io/resources/white-paper/migrate-from-kafka-to-confluent/)
- [Migration Hub on Confluent Cloud](https://confluent.cloud/migration-hub)
- [Talk to a migration expert from Confluent](https://meetings.salesloft.com/confluentinc/confluent-migration-assistance)

---

[![FOSSA License](https://app.fossa.com/api/projects/custom%2B65%2Fgithub.com%2Fconfluentinc%2Fkcp.svg?type=shield&issueType=license)](https://app.fossa.com/projects/custom%2B65%2Fgithub.com%2Fconfluentinc%2Fkcp?ref=badge_shield&issueType=license) [![FOSSA Security](https://app.fossa.com/api/projects/custom%2B65%2Fgithub.com%2Fconfluentinc%2Fkcp.svg?type=shield&issueType=security)](https://app.fossa.com/projects/custom%2B65%2Fgithub.com%2Fconfluentinc%2Fkcp?ref=badge_shield&issueType=security)
