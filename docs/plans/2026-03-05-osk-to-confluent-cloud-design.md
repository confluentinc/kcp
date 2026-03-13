# OSK to Confluent Cloud Migration Support - Design Document

**Date:** 2026-03-05
**Status:** Approved
**Phase:** Phase 1 - Discovery and Scanning

## Executive Summary

This design adds support for migrating Open Source Kafka (OSK) clusters to Confluent Cloud, expanding KCP beyond its current AWS MSK-only support. The implementation uses a clean abstraction layer that allows both MSK and OSK sources to be scanned and managed through unified commands.

**Scope of Phase 1:**
- OSK cluster discovery and scanning via Kafka Admin API
- Unified `kcp scan clusters` command with `--source-type` flag
- State file restructuring to support both MSK and OSK sources
- Comprehensive testing infrastructure with Docker Compose

**Deferred to Phase 2:**
- Migration infrastructure generation (`kcp create-asset` for OSK)
- OSK-specific migration types and network topologies
- Terraform generation for OSK → Confluent Cloud

## Background

### Current State
KCP currently supports migrations from AWS MSK to Confluent Cloud:
- **Discovery**: `kcp discover` uses AWS APIs to find MSK clusters, gather costs, metrics, and metadata
- **Scanning**: `kcp scan clusters` uses Kafka Admin API to discover topics, ACLs, connectors
- **Migration**: Cluster links with Terraform-generated infrastructure

### Problem Statement
Users with self-managed Open Source Kafka deployments (on-prem, VMs, K8s, etc.) cannot use KCP to migrate to Confluent Cloud. OSK clusters don't have AWS APIs for discovery, requiring a different approach.

### Goals
1. Enable OSK cluster scanning without requiring AWS APIs
2. Maintain backward compatibility with existing MSK workflows
3. Use source-agnostic abstractions for future extensibility
4. Reuse existing Kafka Admin API scanning logic
5. Support multiple authentication methods (SASL/SCRAM, TLS, plaintext)

### Non-Goals (Phase 1)
- Migration infrastructure generation for OSK
- OSK cost estimation or metrics collection
- Schema registry or connector discovery for OSK
- UI enhancements for OSK visualization

## Architecture

### High-Level Design

```
┌─────────────────────────────────────────────────────────────┐
│                     KCP CLI Commands                         │
├─────────────────────────────────────────────────────────────┤
│  kcp discover (MSK only)  │  kcp scan clusters (MSK + OSK)  │
└──────────────┬────────────┴──────────────┬──────────────────┘
               │                           │
               ▼                           ▼
         ┌──────────┐            ┌──────────────────┐
         │ AWS APIs │            │ Source Interface │
         └──────────┘            └────────┬─────────┘
                                          │
                        ┌─────────────────┴──────────────────┐
                        │                                     │
                  ┌─────▼──────┐                    ┌────────▼────────┐
                  │ MSK Source │                    │  OSK Source     │
                  └─────┬──────┘                    └────────┬────────┘
                        │                                     │
                        └─────────────────┬──────────────────┘
                                          │
                                ┌─────────▼──────────┐
                                │ Kafka Admin API    │
                                └────────────────────┘
```

### Core Abstraction: Source Interface

```go
// internal/sources/interface.go

type SourceType string

const (
    SourceTypeMSK SourceType = "msk"
    SourceTypeOSK SourceType = "osk"
)

type Source interface {
    Type() SourceType
    LoadCredentials(credentialsPath string) error
    Scan(ctx context.Context, opts ScanOptions) (*ScanResult, error)
    GetClusters() []ClusterIdentifier
}

type ClusterIdentifier struct {
    Name            string    // MSK: cluster name, OSK: user-defined ID
    UniqueID        string    // MSK: ARN, OSK: user-defined ID
    BootstrapServers []string // Populated during scan
}

type ScanOptions struct {
    SkipTopics bool
    SkipACLs   bool
}

type ScanResult struct {
    SourceType SourceType
    Clusters   []ClusterScanResult
}

type ClusterScanResult struct {
    Identifier         ClusterIdentifier
    KafkaAdminInfo     *types.KafkaAdminClientInformation
    SourceSpecificData interface{} // MSK: AWSClientInformation, OSK: OSKClusterMetadata
}
```

### State File Structure

The state file is restructured to support multiple source types:

```json
{
  "msk_sources": {
    "regions": [
      {
        "name": "us-east-1",
        "costs": {...},
        "clusters": [
          {
            "name": "my-msk-cluster",
            "arn": "arn:aws:kafka:...",
            "aws_client_information": {...},
            "kafka_admin_client_information": {...}
          }
        ]
      }
    ]
  },
  "osk_sources": {
    "clusters": [
      {
        "id": "production-kafka-us-east",
        "bootstrap_servers": ["broker1:9092", "broker2:9092"],
        "kafka_admin_client_information": {...},
        "metadata": {
          "environment": "production",
          "location": "us-datacenter-1",
          "kafka_version": "3.6.0",
          "last_scanned": "2026-03-05T10:30:00Z"
        }
      }
    ]
  },
  "schema_registries": [...],
  "kcp_build_info": {...},
  "timestamp": "2026-03-05T10:30:00Z"
}
```

**Key Design Decisions:**
- Top-level `msk_sources` and `osk_sources` sections (both optional)
- OSK clusters have no regions or ARNs
- Reuse existing `KafkaAdminClientInformation` struct (source-agnostic)
- Old state files will NOT auto-upgrade - they fail with clear error message

### Credentials Files

#### MSK Credentials (`msk-credentials.yaml`)

**Generated by `kcp discover`** - minimal changes from existing `cluster-credentials.yaml`:

```yaml
regions:
  - name: us-east-1
    clusters:
      - name: my-msk-cluster
        arn: arn:aws:kafka:us-east-1:123456789:cluster/...
        auth_method:
          sasl_scram:
            use: true
            username: admin
            password: secret
```

**Breaking Change:** Rename from `cluster-credentials.yaml` to `msk-credentials.yaml`

#### OSK Credentials (`osk-credentials.yaml`)

**User-created** - no discovery step for OSK:

```yaml
clusters:
  - id: production-kafka-us-east  # Unique identifier (user-defined)
    bootstrap_servers:
      - broker1.prod.example.com:9092
      - broker2.prod.example.com:9092
      - broker3.prod.example.com:9092
    auth_method:
      sasl_scram:
        use: true
        username: admin
        password: changeme
    metadata:
      environment: production
      location: us-datacenter-1
```

**Supported Authentication Methods:**
- SASL/SCRAM (username/password)
- TLS/mTLS (certificate-based)
- Unauthenticated TLS (encryption only)
- Unauthenticated Plaintext (dev/test only)
- IAM not supported (AWS-specific)

**Validation Rules:**
- `id` is required and must be unique
- `bootstrap_servers` required (at least one)
- Exactly one authentication method must be enabled
- Bootstrap servers must be in `host:port` format
- TLS cert files must exist and be readable

## Implementation Details

### Command Structure

**Unified `kcp scan clusters` command:**

```bash
# MSK scanning
kcp scan clusters --source-type msk \
    --state-file kcp-state.json \
    --credentials-file msk-credentials.yaml

# OSK scanning
kcp scan clusters --source-type osk \
    --state-file kcp-state.json \
    --credentials-file osk-credentials.yaml
```

**Flags:**
- `--source-type` (required): `msk` or `osk`
- `--credentials-file` (required): Path to credentials file
- `--state-file`: Path to state file (default: `kcp-state.json`)
- `--skip-topics`: Skip topic discovery
- `--skip-acls`: Skip ACL discovery

### Source Implementations

#### MSK Source

**Location:** `internal/sources/msk/`

Thin wrapper around existing MSK scanning logic:
- Implements `Source` interface
- Delegates to existing `cmd/scan/clusters` code
- Minimal refactoring to preserve existing functionality

#### OSK Source

**Location:** `internal/sources/osk/`

New implementation:
- Implements `Source` interface
- Loads credentials from `osk-credentials.yaml`
- Connects to OSK clusters via Kafka Admin API
- Discovers topics, ACLs, cluster metadata
- Reuses existing Kafka Admin client from `internal/services/kafka`

### Error Handling

**Validation Errors:**
- Fail fast on credentials file validation
- Clear, actionable error messages
- Validate all required fields before network operations

**Runtime Errors:**
- Partial success support: continue if some clusters fail
- Detailed logging for debugging
- User-friendly terminal output

**Common Error Scenarios:**

| Error | User Message |
|-------|--------------|
| Missing credentials file | `❌ Credentials file not found: osk-credentials.yaml. Create this file with your cluster connection details.` |
| Invalid YAML | `❌ Invalid YAML in osk-credentials.yaml: <error>. Check file syntax.` |
| Duplicate cluster IDs | `❌ Duplicate cluster ID 'prod-kafka-01' found. Each cluster must have a unique ID.` |
| Invalid bootstrap format | `❌ Cluster 'prod-kafka-01': invalid bootstrap server 'broker1' (expected host:port).` |
| No auth method | `❌ Cluster 'prod-kafka-01': no authentication method enabled. Enable exactly one.` |
| Connection timeout | `❌ Failed to connect to cluster 'prod-kafka-01': connection timeout. Check bootstrap servers.` |
| Auth failure | `❌ Failed to authenticate: invalid credentials. Check auth_method configuration.` |

## Testing Strategy

### Unit Tests

**Coverage Goals:**
- `internal/types/osk_credentials.go`: 90%+
- `internal/sources/osk/osk_source.go`: 85%+
- `cmd/scan/clusters/`: 80%+

**Key Test Areas:**
- OSK credentials validation (duplicate IDs, invalid formats, auth methods)
- Source interface implementations
- State file merging (new clusters, updates, preservation of existing data)
- Error scenarios

### Integration Tests

**Docker Compose Test Environments:**

Four test environments covering different auth methods and Kafka modes:

1. **Plaintext** (`docker-compose-plaintext.yml`): ZooKeeper-based, no auth
2. **KRaft** (`docker-compose-kraft.yml`): KRaft mode (no ZooKeeper), no auth
3. **SASL/SCRAM** (`docker-compose-sasl.yml`): Username/password auth
4. **TLS/mTLS** (`docker-compose-tls.yml`): Certificate-based auth

**Makefile Targets:**
```bash
make test-env-up-plaintext   # Start plaintext Kafka
make test-env-up-kraft       # Start KRaft Kafka
make test-env-up-sasl        # Start SASL Kafka
make test-env-up-tls         # Start TLS Kafka
make test-env-down           # Stop all environments
make test-integration-osk    # Run integration tests
make test-all-envs           # Test all configurations
```

**Manual Testing Checklist:**
- [ ] Scan OSK with SASL/SCRAM auth
- [ ] Scan OSK with TLS auth
- [ ] Scan OSK with plaintext (no auth)
- [ ] Scan OSK on KRaft cluster
- [ ] Incremental scan (scan same cluster twice)
- [ ] Mixed MSK + OSK in same state file
- [ ] Invalid credentials handling
- [ ] Network timeout handling
- [ ] Partial cluster failure handling

## User Workflows

### MSK Workflow (Minimal Changes)

```bash
# 1. Discover MSK clusters via AWS API
kcp discover --region us-east-1
# Output: kcp-state.json, msk-credentials.yaml

# 2. Edit msk-credentials.yaml to select auth method

# 3. Scan clusters via Kafka Admin API
kcp scan clusters --source-type msk \
    --credentials-file msk-credentials.yaml

# 4. Generate reports
kcp report costs --state-file kcp-state.json --region us-east-1 ...
kcp report metrics --state-file kcp-state.json --cluster-arn <arn> ...

# 5. Generate migration assets
kcp create-asset target-infra ...
kcp create-asset migration-infra ...
```

### OSK Workflow (New)

```bash
# 1. Create osk-credentials.yaml manually
# (See template in documentation)

# 2. Scan OSK clusters via Kafka Admin API
kcp scan clusters --source-type osk \
    --credentials-file osk-credentials.yaml

# 3. Optionally scan schema registry
kcp scan schema-registry --url https://schema-registry:8081

# 4. Visualize in UI
kcp ui

# 5. Generate migration assets (Phase 2 - deferred)
# 6. Execute migration (Phase 2 - deferred)
```

### Mixed Workflow (MSK + OSK)

```bash
# Scan both source types into same state file
kcp discover --region us-east-1
kcp scan clusters --source-type msk --credentials-file msk-credentials.yaml
kcp scan clusters --source-type osk --credentials-file osk-credentials.yaml

# Result: kcp-state.json contains both msk_sources and osk_sources
```

## Migration Path & Breaking Changes

### Breaking Changes

This is a **breaking release** with the following incompatibilities:

**1. State File Format**
- Old state files will NOT be loaded
- Error message: `❌ Invalid state file: no msk_sources or osk_sources found. This may be a legacy state file. Please use KCP v1.x for legacy files or re-run discovery.`
- Users must re-run discovery/scanning

**2. Credentials File Name**
- `cluster-credentials.yaml` → `msk-credentials.yaml`
- `kcp discover` now generates `msk-credentials.yaml`
- Old filename not recognized

**3. `kcp scan clusters` Now Requires `--source-type` Flag**
- Previously: `kcp scan clusters --credentials-file cluster-credentials.yaml`
- Now: `kcp scan clusters --source-type msk --credentials-file msk-credentials.yaml`

### Migration Path for Existing Users

Users upgrading from older KCP versions:

1. Backup existing `kcp-state.json` if needed for historical reference
2. Re-run `kcp discover` to generate new `msk-credentials.yaml` and state file
3. Update scripts/automation to use `--source-type msk` flag
4. Update any references to `cluster-credentials.yaml` → `msk-credentials.yaml`

**Timeline:** Announce breaking changes in release notes with migration guide.

## File Changes

### New Files

```
internal/sources/
  interface.go                      # Source abstraction interfaces
  msk/
    msk_source.go                   # MSK source implementation
    msk_source_test.go              # MSK source tests
  osk/
    osk_source.go                   # OSK source implementation
    osk_source_test.go              # OSK source tests

internal/types/
  osk_credentials.go                # OSK credentials struct
  osk_credentials_test.go           # OSK credentials tests
  msk_credentials.go                # Renamed from credentials.go

test/
  docker/
    docker-compose-plaintext.yml    # Plaintext test environment
    docker-compose-kraft.yml        # KRaft test environment
    docker-compose-sasl.yml         # SASL test environment
    docker-compose-tls.yml          # TLS test environment
    configs/                        # Auth configurations
    scripts/                        # Setup scripts
  credentials/
    msk-credentials.yaml            # Test MSK credentials
    osk-credentials-*.yaml          # Test OSK credential files
  integration/
    osk_integration_test.go         # Integration tests
```

### Modified Files

```
internal/types/
  state.go                          # Add MSKSources/OSKSources fields

cmd/discover/
  cmd_discover.go                   # Generate msk-credentials.yaml

cmd/scan/clusters/
  cmd_scan_clusters.go              # Add --source-type flag, source factory
  state_merge.go                    # Add OSK merge logic
  state_merge_test.go               # Add OSK merge tests

Makefile                            # Add Docker test environment targets
docs/README.md                      # Update documentation (see below)
CLAUDE.md                           # Update with new credential file names
```

### Deleted Files

```
internal/types/credentials.go       # Replaced by msk_credentials.go
```

## Documentation Changes

### docs/README.md Updates

**Section 1: Getting Started Note**
- Update to mention both MSK and OSK support
- Clarify workflow differences

**Section 2: Authentication**
- Keep existing AWS auth section for MSK
- Add note that OSK doesn't require AWS auth

**Section 3: Workflow Steps**
- Split into "MSK Workflow" and "OSK Workflow"
- Add "Make Key Infrastructure Decisions" for each

**Section 4: kcp discover**
- Update output filename to `msk-credentials.yaml`

**Section 5: kcp scan clusters**
- Complete rewrite to document both MSK and OSK usage
- Add OSK credentials template with all auth methods
- Add examples for both source types

**All other sections:** No changes (MSK-specific commands remain unchanged)

## Success Criteria

Phase 1 is complete when:

- [x] Design document approved
- [ ] All unit tests pass with >85% coverage
- [ ] Integration tests pass against all Docker Compose environments
- [ ] Can scan OSK cluster (plaintext, SASL, TLS, KRaft)
- [ ] Can scan both MSK and OSK into same state file
- [ ] State file properly merges incremental scans
- [ ] Old state files fail with clear error message
- [ ] Error messages are clear and actionable
- [ ] docs/README.md updated
- [ ] CLAUDE.md updated
- [ ] Code review approved
- [ ] Manual testing checklist completed

## Future Phases

### Phase 2: Migration Infrastructure

- `kcp create-asset` support for OSK clusters
- OSK-specific migration infrastructure types
- Network topology handling (on-prem, cloud, K8s)
- Jump cluster configurations for private OSK
- Terraform generation for OSK → Confluent Cloud

### Phase 3: Additional Features

- OSK cost estimation (based on cluster size, not AWS APIs)
- OSK metrics collection (Prometheus/JMX endpoints)
- Schema registry scanning for OSK
- Connector discovery via Connect REST API
- Client discovery mechanisms

### Phase 4: UI Enhancements

- OSK cluster visualization
- MSK vs OSK comparison views
- OSK-specific dashboards

## Appendix

### OSK Credentials Template Example

```yaml
# OSK Credentials Configuration
# Configure your Open Source Kafka cluster connection details

clusters:
  # Production cluster with SASL/SCRAM
  - id: production-kafka-us-east
    bootstrap_servers:
      - broker1.prod.example.com:9092
      - broker2.prod.example.com:9092
      - broker3.prod.example.com:9092
    auth_method:
      sasl_scram:
        use: true
        username: admin
        password: changeme
    metadata:
      environment: production
      location: us-datacenter-1

  # Staging cluster with TLS
  - id: staging-kafka
    bootstrap_servers:
      - broker1.staging.example.com:9093
    auth_method:
      tls:
        use: true
        ca_cert: /path/to/ca-cert.pem
        client_cert: /path/to/client-cert.pem
        client_key: /path/to/client-key.pem
    metadata:
      environment: staging

  # Dev cluster (no auth)
  - id: dev-kafka
    bootstrap_servers:
      - localhost:9092
    auth_method:
      unauthenticated_plaintext:
        use: true
    metadata:
      environment: development
```

### KRaft vs ZooKeeper Compatibility

**Question:** Does OSK scanning work with KRaft clusters?
**Answer:** Yes, seamlessly. The Kafka Admin API abstracts the consensus mechanism, so all scanning operations work identically on both KRaft and ZooKeeper-based clusters.

**Testing:** We include both ZooKeeper and KRaft Docker Compose environments to validate compatibility.

---

**End of Design Document**
