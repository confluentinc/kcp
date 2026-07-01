# Contributing to kcp

Thanks for your interest in contributing to kcp! This guide covers building from
source, running the tests, and how versioning works.

> [!CAUTION]
> **Do not build from source for normal use.** Install a released binary using the methods in the [main README installation section](README.md#installation) instead. Source builds can contain breaking, untested changes.
## Prerequisites

- Go 1.25+
- Make
- Node
- Yarn
- Docker (for integration tests)

## Clone and build

```bash
# Clone the repository
git clone https://github.com/confluentinc/kcp.git
cd kcp

# Build the binary for your current platform
make build

# (Optional) install it to /usr/local/bin (requires sudo)
make install
```

> [!IMPORTANT]
> The frontend MUST be built before the Go binary or tests — the binary embeds the
> web UI via `//go:embed`. `make build`, `make test-go`, etc. build it automatically.

## Build commands

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

Builds from source are stamped as `0.0.0-localdev` and treated as development
builds (showing a dev warning banner). Official version numbers are assigned only
by the release pipeline. See [Versioning](#versioning) below.

## Testing & quality

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

### Playwright E2E tests

The frontend includes Playwright end-to-end tests for the web UI. These test the
migration infrastructure wizards, cluster views, and state loading.

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

Test fixtures are in `cmd/ui/frontend/tests/e2e/fixtures/`. The Playwright config
starts `kcp ui` with `--state-file` to pre-load test data automatically.

### Integration tests

Integration tests live in `integration-tests/` and run against real infrastructure
via Docker.

#### Apache Kafka scan tests

Tests the `kcp scan clusters` command against a Docker Compose environment with a
multi-listener KRaft Kafka broker. Covers all supported authentication methods,
Jolokia metrics collection (unauthenticated, auth, TLS), and Prometheus metrics
collection (unauthenticated, auth, TLS).

**Prerequisites:** Docker

```bash
make test-osk-scan
```

This starts the environment, runs all scan variants, and tears down automatically.
Credential files and Docker Compose configuration are in `integration-tests/osk-scan/`.

#### Schema Registry scan tests

Tests the `kcp scan schema-registry` command against a Docker Compose environment
with two Confluent Schema Registry instances (unauthenticated and basic auth). Both
instances are pre-loaded with 4 test schemas (Avro and JSON Schema).

**Prerequisites:** Docker

```bash
make test-schema-registry
```

This starts a KRaft Kafka broker and two Schema Registry instances, registers test
schemas, runs scan tests against both, and tears down automatically. Configuration is
in `integration-tests/schema-registry/`.

#### Migration tests

Tests the full migration lifecycle (`kcp migration init` → `execute`) against a real
CFK (Confluent for Kubernetes) cluster in Minikube.

**Prerequisites:**

- [Docker](https://docs.docker.com/get-docker/) with at least **8 GB memory** allocated
- [Minikube](https://minikube.sigs.k8s.io/docs/start/)
- [Helm](https://helm.sh/docs/intro/install/)
- [kubectl](https://kubernetes.io/docs/tasks/tools/)

```bash
# Run the full lifecycle: setup → test → teardown
make test-migration
```

Teardown runs automatically when tests finish (pass or fail). If you need to run steps
individually:

```bash
make test-migration-setup       # Set up Minikube cluster with CFK, Kafka, Gateway, and cluster link
make test-migration             # Run the migration tests
make test-migration-teardown    # Tear down the infrastructure
```

If infrastructure persisted from a previous run (e.g. laptop sleep, interrupted test),
run `make test-migration-teardown` before starting again.

Setup creates a Minikube cluster (`kcp-e2e` profile) with 4 CPUs and 8 GB RAM. Setup
typically takes 10-15 minutes depending on image pull times. The test timeout is 15
minutes.

### Linting & pre-commit hooks

```bash
# Install golangci-lint
brew install golangci-lint

# Run Go linters
make lint

# Install git pre-commit hooks (runs linters automatically on commit)
make pre-commit-install
```

## Documentation

The published docs site is built from `docs/assets/` with MkDocs Material.
The only prerequisite is **Python 3** — the docs targets create and manage a
local virtualenv (`.venv-docs/`, git-ignored) for you, so there is no global
`pip install`. If [`uv`](https://docs.astral.sh/uv/) is installed it is used
automatically (faster); otherwise the standard-library `venv` is used.

```bash
make docs-install   # Set up the local docs env (auto-managed venv; only needs Python 3)
make docs-gen       # Regenerate the per-command reference from the Cobra definitions
make docs-serve     # Serve the docs locally with live reload
make docs-build     # Build the docs site into ./site
```

## Versioning

Released versions follow [semantic versioning](https://semver.org/) with
`v`-prefixed git tags (e.g. `v0.8.5`); the release pipeline injects the version at
build time. Binaries built from source (`make build`) instead report
`0.0.0-localdev` and are treated as development builds — the dev sentinel keeps
local builds reproducible and lets the CLI recognise itself as unreleased (e.g. to
skip self-update and show the dev docs). Check what you're running with:

```bash
kcp version
```

## Pull requests

Please fill out the [pull request template](.github/pull_request_template.md),
including tests and documentation updates where relevant.
