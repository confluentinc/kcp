# KCP CLI

[![FOSSA Status](https://app.fossa.com/api/projects/custom%2B65%2Fgithub.com%2Fconfluentinc%2Fkcp-internal.svg?type=shield&issueType=license)](https://app.fossa.com/projects/custom%2B65%2Fgithub.com%2Fconfluentinc%2Fkcp-internal?ref=badge_shield&issueType=license) [![FOSSA Status](https://app.fossa.com/api/projects/custom%2B65%2Fgithub.com%2Fconfluentinc%2Fkcp-internal.svg?type=shield&issueType=security)](https://app.fossa.com/projects/custom%2B65%2Fgithub.com%2Fconfluentinc%2Fkcp-internal?ref=badge_shield&issueType=security)

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
- [Installation](#installation)
- [Authentication](#authentication)
- [Development](#development)
- [Examples](#examples)

## Overview

**Mission**: Simplify and streamline your Kafka migration journey to Confluent Cloud!

kcp helps you migrate your Kafka setups to Confluent Cloud by providing tools to:

- **Scan** scan and identify resources in existing Kafka deployments.
- **Create** reports for migration planning and cost analysis.
- **Generate** migration assets and infrastructure configurations.

A short demo of kcp can be viewed here:

https://github.com/user-attachments/assets/11ecd725-9172-47a5-8872-bfb4914a8ed7

### Key Features

| Feature                     | Description                                                                             |
| --------------------------- | --------------------------------------------------------------------------------------- |
| **Multiple Auth Methods**   | Support for SASL-IAM, SASL-SCRAM, TLS, and unauthenticated.                             |
| **Comprehensive Reporting** | Detailed migration planning and cost analysis.                                          |
| **Infrastructure as Code**  | Generate Terraform and Ansible configurations to seamlessly migrate to Confluent Cloud. |
| **Private VPC Deployments** | Migrate to Confluent Cloud from private networks and isolated environments.             |

## Installation

### Build/Install from Source

> [!TIP]
> Make sure you have Go 1.24+ installed before building from source

```bash
# Clone the repository
git clone https://github.com/confluentinc/kcp.git
cd kcp-internal

# Install to system path (requires sudo)
make install
```

#### Installing from GitHub Releases (macOS)

If you're downloading pre-built binaries directly from 'GitHub Releases' on macOS, as a temporary workaround until we sign and notorize the binary.
You will need to remove the quarantine attribute after extracting the tar.gz file:

```bash
xattr -d com.apple.quarantine ./kcp
```

## Authentication

Ensure that your terminal session is authenticated with AWS. The kcp CLI uses the standard AWS credential chain and supports multiple authentication methods:

**Authentication options:**

- **Environment variables**: Export `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, and optionally `AWS_SESSION_TOKEN`
- **AWS credentials file**: Configure with `aws configure` (requires AWS CLI)
- **AWS SSO/Identity Center**: Use `aws sso login` (requires AWS CLI)
- **IAM Roles**: Assume roles or use instance profiles
- **Other tools**: Any tool that sets AWS credentials in the standard locations such as `granted`.

**Verify your authentication:**
The easiest way to test authentication is to run a kcp command that requires AWS access such as `kcp scan region`, or if you have AWS CLI installed:

```bash
aws sts get-caller-identity
```

## Examples

### Basic Migration Workflow

1. **Initialize configuration**:

   ```bash
   kcp init
   ```

2. **Scan your AWS region**:

   ```bash
   kcp scan region --region us-east-1
   ```

3. **Scan specific cluster**:

   ```bash
   kcp scan cluster \
     --cluster-arn arn:aws:kafka:us-east-1:123456789012:cluster/my-cluster \
     --use-sasl-iam
   ```

4. **Generate reports**:

   ```bash
   kcp report region costs \
     --region us-east-1
     --start 2025-07-10 \
     --end 2025-07-11 \
     --daily \
     --tag Environment=Staging \
     --tag Owner=ajanuzi
   ```

   ```bash
   kcp report region \
     --region us-east-1 \
     --start 2025-07-10 \
     --end 2025-07-11
   ```

5. **Generate migration assets**:

   ```bash
   kcp create-asset bastion-host \
     --bastion-host-cidr 10.0.XXX.0/24 \
     --vpc-id vpc-xxxxxxxxx \
     --region us-east-1
   ```

   ```bash
   kcp create-asset migration-infra \
     --region us-east-1
     --cluster-file cluster.json \
     --vpc-id vpc-xxxxxxxxx
     --type 1 \
     --cc-api-key EXAMPLECCKEY \
     --cc-api-secret EXAMPLECCSECRET \
     --cc-env-name my-new-environment \
     --cc-cluster-name my-new-cluster \
     --cluster-type enterprise \
     --ansible-control-node-subnet-cidr 10.0.10.0/24 \
     --jump-cluster-broker-subnet-config us-east-1a:10.0.50.0/24,us-east-1b:10.0.60.0/24,us-east-1c:10.0.70.0/24 \
     --use-sasl-iam \
     --jump-cluster-broker-iam-role-name msk-cluster-link-role
   ```

## Development

### Prerequisites

- Go 1.24+
- Make

### Build Commands

```bash
# Build for current platform
make build

# Build for Linux
make build-linux

# Build for all platforms
make build-all

# Clean build artifacts
make clean
```

### Testing & Quality

```bash
# Format go code
make fmt

# Run tests
make test

# Run tests with coverage
make test-cov

# Run tests with coverage and open UI coverage browser
make test-cov-ui
```
