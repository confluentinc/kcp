# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Plans

Store implementation plans outside the repository. Use `~/claude-plans/kcp/` as the default location. Never write plans into `docs/plans/` or anywhere inside the repo — they are local working artefacts and must not be committed to GitHub.

## Project Overview

KCP (Kafka Copy) is a CLI tool for planning and executing Kafka migrations from **AWS MSK and Open Source Kafka (OSK)** to Confluent Cloud. It provides commands for discovering resources, generating reports, creating migration assets (Terraform), and managing the migration workflow.

## Source Types

KCP supports two Kafka source types via a `Source` abstraction pattern:

### MSK (AWS Managed Streaming for Kafka)
- **Discovery**: Automated via AWS APIs (`kcp discover`)
- **Credentials**: Auto-generated in `msk-credentials.yaml`
- **Authentication**: IAM, SASL/SCRAM-SHA-512
- **Code location**: `internal/sources/msk/`
- **Workflow**: AWS API discovery → Kafka Admin API scanning

### OSK (Open Source Kafka)
- **Discovery**: Manual configuration
- **Credentials**: User-created `osk-credentials.yaml` file
- **Authentication**: SASL/SCRAM (SHA-256/SHA-512), TLS/mTLS, Plaintext
- **Code location**: `internal/sources/osk/`
- **Workflow**: Direct Kafka Admin API scanning
- **Test environments**: Docker Compose in `test/docker/`

## Build Commands

**CRITICAL**: The frontend MUST be built before building the Go binary or running tests.

```bash
# Build everything (frontend + Go binary)
make build

# Build just the frontend
make build-frontend

# Build for specific platforms
make build-linux
make build-darwin-arm64
make build-all

# Install to system path (requires sudo)
make install
```

## Development Commands

```bash
# Format code
make fmt

# Run all tests (Go unit tests + Playwright E2E tests)
make test

# Run Go unit tests only
make test-go

# Run Playwright E2E tests only (builds frontend + Go binary first)
make test-e2e

# Run tests with coverage (Go only)
make test-cov

# Run tests with coverage HTML viewer (Go only)
make test-cov-ui

# Run tests for a specific Go package
go test ./cmd/scan/clusters -v
go test ./internal/sources/osk -v

# Clean build artifacts
make clean
```

## Testing

### Unit Tests
- All Go tests require the frontend to be built first (`make build-frontend`)
- Tests will fail with "pattern all:dist: no matching files found" if frontend is not built
- Run with `make test-go` or `go test ./...`

### Playwright E2E Tests
- Tests are in `cmd/ui/frontend/tests/e2e/`
- Playwright config starts `kcp ui` with `--state-file` to pre-load test data
- Test fixtures in `cmd/ui/frontend/tests/e2e/fixtures/`
- Run with `make test-e2e` or from `cmd/ui/frontend`: `npx playwright test`
- Interactive UI mode: `cd cmd/ui/frontend && npx playwright test --ui`
- Headed mode (visible browser): `cd cmd/ui/frontend && npx playwright test --headed`
- Debug a test: `cd cmd/ui/frontend && npx playwright test -g "test name" --debug`

### OSK Integration Tests

Docker-based test environments for all OSK authentication methods:

| Environment | Port | Auth Method | Command |
|-------------|------|-------------|---------|
| PLAINTEXT (ZooKeeper) | 9092 | None | `make test-env-up-plaintext` |
| PLAINTEXT (KRaft) | 9095 | None | `make test-env-up-kraft` |
| SASL/SCRAM | 9093 | Username/password (SHA-256) | `make test-env-up-sasl` |
| TLS/mTLS | 9094 | Client certificates | `make test-env-up-tls` |
| Schema Registry (unauth) | 8081 | None | `make test-env-up-schema-registry` |
| Schema Registry (basic auth) | 8082 | Username/password | `make test-env-up-schema-registry` |
| JMX/Jolokia (unauth) | 9096 (Kafka) / 8778 (Jolokia) | None | `make test-env-up-jmx` |
| JMX/Jolokia (password) | 9097 (Kafka) / 8779 (Jolokia) | Username/password | `make test-env-up-jmx-auth` |
| JMX/Jolokia (TLS) | 9098 (Kafka) / 8780 (Jolokia) | Username/password + TLS | `make test-env-up-jmx-tls` |

All Kafka environments have an authorizer enabled and are populated with 4 topics (`test-topic-1`, `test-topic-2`, `orders`, `events`) and 12 ACLs across 5 team principals.

JMX environments additionally include a continuous producer and consumer container to generate traffic for non-zero throughput metrics.

Schema Registry environments are populated with 4 schemas: `orders-value` (Avro), `orders-key` (Avro), `events-value` (JSON Schema), `test-topic-1-value` (Avro). The Schema Registry requires the plaintext Kafka environment to be running first.

**Test workflow:**
```bash
# Start a specific Kafka environment
make test-env-up-sasl

# Start Schema Registry (starts plaintext Kafka if needed)
make test-env-up-schema-registry

# Run tests against all environments
make test-all-envs

# Stop all test environments
make test-env-down

# Generate fresh TLS certificates
make test-certs-generate

# Scan Schema Registry
kcp scan schema-registry --url http://localhost:8081 --use-unauthenticated --state-file kcp-state.json
kcp scan schema-registry --url http://localhost:8082 --use-basic-auth --username schemauser --password schemapass --state-file kcp-state.json

# Start JMX environment and scan with metrics collection
make test-env-up-jmx
kcp scan clusters --source-type osk --state-file kcp-state.json --credentials-file test/credentials/osk-credentials-jmx.yaml --jmx --jmx-scan-duration 30s --jmx-poll-interval 1s
```

**Test credentials:**
- SASL Kafka: `kafkauser` / `kafkapass` (created in Docker startup)
- Schema Registry basic auth: `schemauser` / `schemapass`
- JMX Jolokia auth: `monitorUser` / `monitorPass`
- TLS: Certificates in `test/docker/certs/` (generated by `scripts/generate-certs.sh`)
- Credential files: `test/credentials/osk-credentials-*.yaml`

**Important**: All test environments use `localhost` and are safe to commit. The `.gitignore` excludes generated certificates (`test/docker/certs/*`).

## High-Level Architecture

### Command Structure

The CLI follows a hierarchical command structure using Cobra:

```
kcp
├── discover          # Scan AWS account for MSK clusters (MSK only)
├── scan             # Granular scanning operations
│   ├── clusters     # Scan cluster-level details via Kafka API (MSK & OSK)
│   ├── client-inventory  # Parse S3 broker logs for client discovery
│   └── schema-registry   # Scan schema registry
├── report           # Generate reports from discovered data
│   ├── costs        # Cost analysis reports
│   └── metrics      # Metrics analysis reports
├── create-asset     # Generate Terraform for migration
│   ├── bastion-host
│   ├── target-infra
│   ├── migration-infra
│   ├── migrate-topics
│   ├── migrate-schemas
│   ├── migrate-acls
│   ├── migrate-connectors
│   └── reverse-proxy
├── migration        # Migration execution workflow
│   ├── init         # Initialize migration state
│   ├── list         # List migrations
│   ├── status       # Check migration status
│   └── execute      # Execute migration steps
├── ui               # Web UI for visualization
├── update           # Self-update the CLI
└── version          # Show version info
```

### Source Abstraction Pattern

The `internal/sources/` package implements a `Source` interface that abstracts MSK and OSK:

```go
type Source interface {
    Type() SourceType
    LoadCredentials(path string) error
    Scan(ctx context.Context, opts ScanOptions) (*ScanResult, error)
    GetClusterIdentifiers() []ClusterIdentifier
}
```

- **MSKSource** (`internal/sources/msk/`): Wraps existing MSK scanning logic
- **OSKSource** (`internal/sources/osk/`): Implements Kafka Admin API scanning for OSK
- **Factory** (`cmd/scan/clusters/`): Creates appropriate source based on `--source-type` flag

### State File Architecture

The workflow is state-driven:

1. **`kcp-state.json`**: Central state file tracking discovered clusters, topics, ACLs, connectors, costs, metrics, and migration progress. Commands progressively append to this file.
2. **`msk-credentials.yaml`**: Generated by `kcp discover`, contains authentication details for MSK clusters.
3. **`osk-credentials.yaml`**: User-created file with OSK cluster connection details (YAML format).
4. **`.kcp-migration-state.json`**: Tracks migration execution state (created by `kcp migration init`).

Commands read from and write to these files, making the workflow idempotent and resumable.

**OSK Credentials Format** (`osk-credentials.yaml`):
```yaml
clusters:
  - id: my-kafka-cluster
    bootstrap_servers:
      - broker1:9092
      - broker2:9092
    auth_method:
      sasl_scram:
        use: true
        username: admin
        password: secret
        mechanism: SHA256  # or SHA512
      # OR
      tls:
        use: true
        ca_cert: /path/to/ca.pem
        client_cert: /path/to/client.pem
        client_key: /path/to/key.pem
      # OR
      unauthenticated_plaintext:
        use: true
    jmx:                              # optional — enables JMX metrics collection
      type: jolokia                   # only "jolokia" supported currently
      endpoints:
        - http://broker1:8778/jolokia
      auth:                           # optional — omit for unauthenticated
        username: monitorRole
        password: secret
      tls:                            # optional — omit for plain HTTP
        ca_cert: /path/to/ca.pem
        insecure_skip_verify: false
    metadata:
      environment: production
      location: datacenter-1
```

### Internal Services

The `internal/services/` directory contains business logic organized by domain:

- **AWS Services**: `msk`, `ec2`, `iam`, `s3`, `msk_connect` - AWS API integrations
- **Kafka**: `kafka` - Kafka Admin API operations (used by both MSK and OSK)
- **Sources**: `sources/` - Source abstraction and implementations (MSK, OSK)
- **Cost & Metrics**: `cost`, `metrics` - CloudWatch and Cost Explorer integration
- **Schema Registry**: `schema_registry` - Schema Registry API client
- **Infrastructure Generation**: `hcl` - Terraform HCL generation
- **Cluster Link**: `clusterlink` - Confluent cluster link management
- **Gateway**: `gateway` - Confluent Gateway integration
- **Persistence**: `persistence` - State file I/O
- **Report**: `report` - Report generation logic
- **Markdown**: `markdown` - Markdown rendering (uses glamour)

### Frontend Architecture

- **Location**: `cmd/ui/frontend/`
- **Stack**: React + TypeScript + Vite
- **Build**: Yarn-based (`yarn install && yarn build`)
- **Embedding**: Built assets are embedded into the Go binary via `embed` directive in `cmd/ui/frontend/frontend.go`
- **Server**: Echo web framework serves embedded assets and REST API

The frontend provides a web UI for visualizing state files, generating TCO reports, and creating migration assets interactively.

### Migration Types

The tool supports 4 migration infrastructure types (configured via `kcp create-asset migration-infra --type N`):

- **Type 1**: Public source endpoints with SASL/SCRAM cluster link
- **Type 2**: Private source with external outbound cluster link (SASL/SCRAM)
- **Type 3**: Private source with jump cluster (SASL/SCRAM)
- **Type 4**: Private source with jump cluster (IAM) — MSK only

Types 1-3 support both MSK and OSK sources. Type 4 is MSK-only (IAM is an AWS-specific auth method).

Jump clusters are Confluent Platform brokers that bridge the source Kafka cluster and Confluent Cloud.

## Key Implementation Patterns

### Command Structure

Commands follow this pattern:

- `cmd/<command>/cmd_<command>.go` - Command definition and flag setup
- Implementation logic often delegates to `internal/services/` or `internal/sources/`
- Cobra's `RunE` function for error handling
- Viper for configuration management

### State File Operations

When modifying state file logic:

1. Read state with `persistence.LoadState()`
2. Modify the `types.State` struct
3. Write back with `persistence.SaveState()`
4. The state file is append-only - new discoveries augment existing data

### HCL/Terraform Generation

The `internal/services/hcl` package generates Terraform configurations:

- Uses `hashicorp/hcl/v2` for programmatic HCL generation
- Templates follow Confluent Terraform provider patterns
- Generated configs are written to output directories (e.g., `migration-infra/`)

### Authentication Methods

The tool supports multiple Kafka authentication methods:

**MSK:**
- **IAM**: AWS MSK IAM authentication
- **SASL/SCRAM-SHA-512**: AWS MSK's SCRAM implementation (SHA-512 only)

**OSK:**
- **SASL/SCRAM-SHA-256**: Most common for open source Kafka
- **SASL/SCRAM-SHA-512**: Also supported
- **TLS/mTLS**: Client certificate authentication
- **Unauthenticated/Plaintext**: For testing environments

**Implementation**: The `internal/client/kafka_admin.go` package handles all authentication types. SASL/SCRAM defaults to SHA-256 for OSK and SHA-512 for MSK (set by `kcp discover`). The SASL mechanism used during scan is stored in `KafkaAdminClientInformation.SaslMechanism` and plumbed through to generated Terraform as `source_sasl_scram_mechanism`.

### JMX Metrics (OSK only)

OSK clusters can collect JMX metrics via Jolokia HTTP bridge during scan:

```bash
kcp scan clusters --source-type osk --state-file kcp-state.json \
  --credentials-file osk-credentials.yaml \
  --jmx --jmx-scan-duration 5m --jmx-poll-interval 10s
```

- `--jmx` — opt-in, requires `jmx` section in credentials file
- `--jmx-scan-duration` — how long to poll (required when `--jmx` is set)
- `--jmx-poll-interval` — polling frequency (default: 10s)
- Only valid with `--source-type osk` (MSK uses CloudWatch via `kcp discover`)

**Metrics collected** (aligned with CloudWatch names): `BytesInPerSec`, `BytesOutPerSec`, `MessagesInPerSec`, `PartitionCount`, `GlobalPartitionCount`, `ClientConnectionCount`, `TotalLocalStorageUsage`

**Implementation**: `internal/client/jolokia_client.go` (HTTP client), `internal/services/jmx/jmx_service.go` (metric collection). Results stored as `JMXMetrics` on `OSKDiscoveredCluster` in state file.

## Common Workflows

### MSK Migration Flow

```bash
# 1. Discover MSK clusters
kcp discover --region us-east-1

# 2. Scan clusters for topics/ACLs (requires network access)
kcp scan clusters --source-type msk --state-file kcp-state.json --credentials-file msk-credentials.yaml

# 3. Generate reports
kcp report costs --state-file kcp-state.json
kcp report metrics --state-file kcp-state.json

# 4. Visualize in UI
kcp ui

# 5. Generate target infrastructure Terraform
kcp create-asset target-infra --state-file kcp-state.json --cluster-arn <arn> ...

# 6. Generate migration infrastructure Terraform
kcp create-asset migration-infra --state-file kcp-state.json --cluster-arn <arn> --type 3 ...

# 7. Generate migration assets
kcp create-asset migrate-topics --state-file kcp-state.json --cluster-arn <arn> ...
kcp create-asset migrate-acls kafka --state-file kcp-state.json --cluster-arn <arn>
kcp create-asset migrate-schemas --state-file kcp-state.json --url <schema-registry-url>

# 8. Initialize migration
kcp migration init --state-file kcp-state.json --cluster-arn <arn>

# 9. Execute migration
kcp migration execute --state-file kcp-state.json
```

### OSK Migration Flow

```bash
# 1. Create OSK credentials file (manual)
cat > osk-credentials.yaml <<EOF
clusters:
  - id: prod-kafka
    bootstrap_servers: [broker1:9092, broker2:9092]
    auth_method:
      sasl_scram:
        use: true
        username: admin
        password: secret
        mechanism: SHA256
EOF

# 2. Scan OSK cluster
kcp scan clusters --source-type osk --state-file kcp-state.json --credentials-file osk-credentials.yaml

# 2b. (Optional) Scan with JMX metrics — requires jmx section in credentials file
kcp scan clusters --source-type osk --state-file kcp-state.json --credentials-file osk-credentials.yaml \
  --jmx --jmx-scan-duration 5m --jmx-poll-interval 10s

# 3. Continue with standard migration workflow (same as MSK steps 4-9)
kcp ui
# ... etc
```

## Prerequisites

- Go 1.24+ (currently using 1.25.0)
- Make
- Node.js and Yarn (for frontend)
- Docker (for OSK integration tests)
- AWS credentials configured (for MSK, standard AWS credential chain)
- Network access to Kafka clusters (may require bastion host for private clusters)

## Logging

- Logs written to `kcp.log` via lumberjack (rotating logger)
- Uses `slog` for structured logging
- Log level: DEBUG by default
- Custom pretty handler outputs to both file and stdout

## Build Info

Version information is injected at build time via ldflags:

- `internal/build_info.Version` - Version tag
- `internal/build_info.Commit` - Git commit hash
- `internal/build_info.Date` - Build timestamp

Development builds show a warning banner and set version to "dev".
