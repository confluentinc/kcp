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
- [Development](#development)
- [Resources and Support](#resources-and-support)

## Overview

**Mission**: Simplify and streamline your Kafka migration journey to Confluent Cloud!

kcp helps you migrate your Kafka setups to Confluent Cloud by providing tools to:

- **Scan** scan and identify resources in existing Kafka deployments.
- **Create** reports for migration planning and cost analysis.
- **Generate** migration assets and infrastructure configurations.
- **Migrate** execute end-to-end migrations with real-time offset monitoring and resumable workflows.

### Key Features

| Feature                     | Description                                                                             |
| --------------------------- | --------------------------------------------------------------------------------------- |
| **Multiple Auth Methods**   | Support for SASL-IAM, SASL-SCRAM, TLS, and unauthenticated.                             |
| **Comprehensive Reporting** | Detailed migration planning and cost analysis.                                          |
| **Infrastructure as Code**  | Generate Terraform and Ansible configurations to seamlessly migrate to Confluent Cloud. |
| **Migration Execution**     | FSM-driven migration workflow with lag monitoring, gateway fencing, and topic promotion. |
| **Private VPC Deployments** | Migrate to Confluent Cloud from private networks and isolated environments.             |

### Documentation

The docs for the latest release are available [here](https://github.com/confluentinc/kcp/releases/latest)

### Installation

The recommended way to install kcp is by downloading the latest release binary. Instructions for installing the latest release are available in the [latest documentation](https://github.com/confluentinc/kcp/releases/latest).

## Development

### Prerequisites

- Go 1.25+
- Make
- Node
- Yarn

```bash
# Clone the repository
git clone https://github.com/confluentinc/kcp.git
cd kcp

# Install to system path (requires sudo)
make install
```

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

### E2E Integration Tests (Migration)

The migration commands have end-to-end tests that run against a real CFK (Confluent for Kubernetes) cluster in Minikube.

**Prerequisites:**

- [Docker](https://docs.docker.com/get-docker/) with at least **8 GB memory** allocated
- [Minikube](https://minikube.sigs.k8s.io/docs/start/)
- [Helm](https://helm.sh/docs/intro/install/)
- [kubectl](https://kubernetes.io/docs/tasks/tools/)

```bash
# Set up Minikube cluster with CFK, Kafka clusters, Gateway, and cluster link
make e2e-setup

# Run the E2E tests
make ci-e2e-tests

# Tear down the infrastructure
make e2e-teardown
```

The setup creates a Minikube cluster (`kcp-e2e` profile) with 4 CPUs and 8 GB RAM, deploys source and destination Kafka clusters, a Gateway, and a cluster link. The kcp binary is built for Linux and runs inside the cluster to avoid TLS/DNS issues.

Setup typically takes 10-15 minutes depending on image pull times. The test timeout is 15 minutes.

### Linting & Pre-commit Hooks

```bash
# Install golangci-lint
brew install golangci-lint

# Run Go linters
make lint

# Install git pre-commit hooks (runs linters automatically on commit)
make pre-commit-install
```

## Resources and Support

- [Kafka Migration Guide](https://www.confluent.io/resources/white-paper/migrate-from-kafka-to-confluent/)
- [Migration Hub on Confluent Cloud](https://confluent.cloud/migration-hub)
- [Talk to migration expert from Confluent](https://meetings.salesloft.com/confluentinc/confluent-migration-assistance)
