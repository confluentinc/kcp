# Contributing to kcp

Thanks for your interest in contributing to kcp! This guide covers building from
source, running the tests, and cutting a release.

> [!NOTE]
> If you just want to **use** kcp, you do not need to build from source — install a
> released binary instead. See the [Installation](README.md#installation) section of
> the README. Building from `main` produces an unreleased, in-development binary.

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

The binary is stamped with a version derived from the latest git tag (via
`git describe`). Untagged checkouts report `0.0.0-localdev` and are treated as
development builds.

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

```bash
make docs-install   # Install MkDocs and plugins (pip)
make docs-gen       # Regenerate the per-command reference from the Cobra definitions
make docs-serve     # Serve the docs locally with live reload
make docs-build     # Build the docs site into ./site
```

## Cutting a release

Releases are driven by [GoReleaser](https://goreleaser.com/) from a git tag. Each
release publishes raw binaries named `kcp_<os>_<arch>` (plus `kcp_windows_amd64.exe`)
and a `checksums.txt`, which is what `install.sh`, the docs install commands, and the
`kcp update` self-updater all expect.

```bash
# 1. Tag the release (semantic version, v-prefixed)
git tag v0.9.0
git push-external origin v0.9.0

# 2. Dry-run locally to inspect the artifacts (writes to ./dist, publishes nothing)
make release-snapshot

# 3. Publish (requires goreleaser + a GITHUB_TOKEN with repo scope)
make release
```

> [!NOTE]
> The authoritative release is produced by the Confluent Semaphore release pipeline.
> The targets above mirror what that pipeline runs (`goreleaser`) and are useful for
> local dry-runs and verification.

## Pull requests

Please fill out the [pull request template](.github/pull_request_template.md),
including tests and documentation updates where relevant.
