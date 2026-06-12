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

| Feature                     | Description                                                                              |
| --------------------------- | ---------------------------------------------------------------------------------------- |
| **Multiple Auth Methods**   | Support for SASL-IAM, SASL-SCRAM, TLS, and unauthenticated.                              |
| **Comprehensive Reporting** | Detailed migration planning and cost analysis.                                           |
| **Infrastructure as Code**  | Generate Terraform and Ansible configurations to seamlessly migrate to Confluent Cloud.  |
| **Migration Execution**     | FSM-driven migration workflow with lag monitoring, gateway fencing, and topic promotion. |
| **Private VPC Deployments** | Migrate to Confluent Cloud from private networks and isolated environments.              |

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

# Run Go unit tests
make test-go

# Run Playwright browser tests
make test-playwright

# Run Go tests with coverage
make test-go-coverage

# Run Go tests with coverage and open HTML report
make test-go-coverage-ui
```

### Playwright E2E Tests

The frontend includes Playwright end-to-end tests for the web UI. These test the migration infrastructure wizards, cluster views, and state loading.

```bash
cd cmd/ui/frontend

# Run all E2E tests
npx playwright test

# Run with visible browser
npx playwright test --headed

# Run with Playwright UI (interactive test runner)
npx playwright test --ui

# Run a specific test file
npx playwright test tests/e2e/osk-migration-infra.spec.ts

# Debug a specific test
npx playwright test -g "Public path" --debug
```

Test fixtures are in `cmd/ui/frontend/tests/e2e/fixtures/`. The Playwright config starts `kcp ui` with `--state-file` to pre-load test data automatically.

### Integration Tests

Integration tests live in `integration-tests/` and run against real infrastructure via Docker.

#### OSK Scan Tests

Tests the `kcp scan clusters` command against a Docker Compose environment with a multi-listener KRaft Kafka broker. Covers all supported authentication methods, Jolokia metrics collection (unauthenticated, auth, TLS), and Prometheus metrics collection (unauthenticated, auth, TLS).

**Prerequisites:** Docker

```bash
make test-osk-scan
```

This starts the environment, runs all 10 scan variants, and tears down automatically. Credential files and Docker Compose configuration are in `integration-tests/osk-scan/`.

#### Schema Registry Scan Tests

Tests the `kcp scan schema-registry` command against a Docker Compose environment with two Confluent Schema Registry instances (unauthenticated and basic auth). Both instances are pre-loaded with 4 test schemas (Avro and JSON Schema).

**Prerequisites:** Docker

```bash
make test-schema-registry
```

This starts a KRaft Kafka broker and two Schema Registry instances, registers test schemas, runs scan tests against both, and tears down automatically. Configuration is in `integration-tests/schema-registry/`.

#### Migration Tests

Tests the full migration lifecycle (`kcp migration init` → `execute`) against a real CFK (Confluent for Kubernetes) cluster in Minikube.

**Prerequisites:**

- [Docker](https://docs.docker.com/get-docker/) with at least **8 GB memory** allocated
- [Minikube](https://minikube.sigs.k8s.io/docs/start/)
- [Helm](https://helm.sh/docs/intro/install/)
- [kubectl](https://kubernetes.io/docs/tasks/tools/)

```bash
# Run the full lifecycle: setup → test → teardown
make test-migration
```

Teardown runs automatically when tests finish (pass or fail). If you need to run steps individually:

```bash
make test-migration-setup       # Set up Minikube cluster with CFK, Kafka, Gateway, and cluster link
make test-migration             # Run the migration tests
make test-migration-teardown    # Tear down the infrastructure
```

If infrastructure persisted from a previous run (e.g. laptop sleep, interrupted test), run `make test-migration-teardown` before starting again.

Setup creates a Minikube cluster (`kcp-e2e` profile) with 4 CPUs and 8 GB RAM. Setup typically takes 10-15 minutes depending on image pull times. The test timeout is 15 minutes.

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
- [Talk to a migration expert from Confluent](https://meetings.salesloft.com/confluentinc/confluent-migration-assistance)

