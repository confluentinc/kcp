# Replace Sarama with confluent-kafka-go

**Date:** 2026-03-06
**Branch:** `refactor/integrateconfluentgo`

## Motivation

1. **Alignment** — This is a `github.com/confluentinc` project. Depending on `github.com/IBM/sarama` for Kafka operations is inconsistent.
2. **Maintenance** — Sarama's long-term maintenance trajectory is uncertain (transferred Shopify -> IBM, community activity has slowed).
3. **Consolidation** — confluent-kafka-go is already a dependency (used for Schema Registry). Using one Kafka library instead of two simplifies the dependency tree.

## Current State

**Sarama usage** is concentrated in these files:
- `internal/client/kafka_admin.go` — admin client, consumer, auth configuration
- `internal/mocks/mocks.go` — mock admin using Sarama types
- `internal/services/kafka/kafka_service_test.go` — tests referencing Sarama types
- `internal/client/kafka_admin_test.go` — admin client tests

**Sarama types leak through the `KafkaAdmin` interface:**
- `sarama.TopicDetail` — returned by `ListTopicsWithConfigs()`
- `sarama.ConfigEntry` — returned by `DescribeConfig()`
- `sarama.ResourceAcls` — returned by `ListAcls()`
- `*sarama.Broker` — embedded in `ClusterKafkaMetadata`

**confluent-kafka-go** is already in `go.mod` (v2.12.0), used only for `schemaregistry`.

## Approach: Two Phases

### Phase 1 — Isolate: Replace Sarama types at the interface boundary

Define project-owned types and update the `KafkaAdmin` interface so that no Sarama types leak out. Callers stop importing Sarama. Conversion from Sarama types to domain types happens inside `KafkaAdminClient` only.

**New/updated types in `internal/types/kafka.go`:**

```go
// Already exists
type TopicDetails struct { ... }

// Already exists
type Acls struct { ... }

// New
type BrokerConfigEntry struct {
    Name      string
    Value     string
    IsDefault bool
}

// New
type BrokerInfo struct {
    ID      int32
    Address string
}
```

**Updated `KafkaAdmin` interface:**

```go
type KafkaAdmin interface {
    ListTopicsWithConfigs() ([]types.TopicDetails, error)
    GetClusterKafkaMetadata() (*ClusterKafkaMetadata, error) // uses BrokerInfo instead of *sarama.Broker
    DescribeConfig() ([]types.BrokerConfigEntry, error)
    ListAcls() ([]types.Acls, error)
    GetAllMessagesWithKeyFilter(topicName string, keyPrefix string) (map[string]string, error)
    GetConnectorStatusMessages(topicName string) (map[string]string, error)
    Close() error
}
```

`ClusterKafkaMetadata` updated:
```go
type ClusterKafkaMetadata struct {
    Brokers      []types.BrokerInfo
    ControllerID int32
    ClusterID    string
}
```

**Files changed:**
- `internal/types/kafka.go` — add `BrokerConfigEntry`, `BrokerInfo`
- `internal/client/kafka_admin.go` — update interface + convert Sarama types to domain types internally
- `internal/mocks/mocks.go` — use domain types
- `internal/services/kafka/kafka_service_test.go` — use domain types
- `internal/client/kafka_admin_test.go` — use domain types
- Any service code that references Sarama types directly

This phase is a pure refactor with no behavior change.

### Phase 2 — Swap: Replace Sarama internals with confluent-kafka-go

With the interface now library-agnostic, swap the implementation inside `kafka_admin.go`.

**Admin operations:**
- `sarama.NewClusterAdmin` -> `kafka.NewAdminClient` with `kafka.ConfigMap`
- `ListTopicsWithConfigs()` -> `AdminClient.GetMetadata()` + `AdminClient.DescribeConfigs()`
- `DescribeConfig()` -> `AdminClient.DescribeConfigs()` with broker resource
- `ListAcls()` -> `AdminClient.DescribeACLs()`
- `GetClusterKafkaMetadata()` -> `AdminClient.GetMetadata()`

**Consumer operations:**
- `sarama.NewConsumer` -> `kafka.NewConsumer`
- Partition-level consumption -> confluent-kafka-go consumer with assign/poll

**Authentication mapping:**

| Current (Sarama) | confluent-kafka-go ConfigMap |
|---|---|
| IAM/OAuth | `security.protocol=SASL_SSL`, `sasl.mechanisms=OAUTHBEARER` + token refresh handler |
| SASL/SCRAM | `security.protocol=SASL_SSL`, `sasl.mechanisms=SCRAM-SHA-512`, `sasl.username`, `sasl.password` |
| TLS (mTLS) | `security.protocol=SSL`, `ssl.ca.location`, `ssl.certificate.location`, `ssl.key.location` |
| Unauthenticated TLS | `security.protocol=SSL` |
| Unauthenticated Plaintext | `security.protocol=PLAINTEXT` |

**IAM/MSK signer integration:**
The `aws-msk-iam-sasl-signer-go` library is unchanged. It generates a token string. The wiring changes from implementing `sarama.AccessTokenProvider` to handling `kafka.OAuthBearerTokenRefresh` events and calling `client.SetOAuthBearerToken()`.

**Files changed:**
- `internal/client/kafka_admin.go` — full implementation swap
- `internal/client/kafka_admin_test.go` — update tests
- `go.mod` / `go.sum` — remove `github.com/IBM/sarama`

**Build considerations:**
confluent-kafka-go bundles prebuilt librdkafka binaries for macOS (x64/arm64), Linux glibc (x64/arm64), Alpine musl (x64/arm64), and Windows (amd64). `CGO_ENABLED=1` (the default) is required. Alpine builds need `-tags musl`.

## What Does Not Change

- Schema Registry code (already uses confluent-kafka-go)
- The `aws-msk-iam-sasl-signer-go` dependency
- CLI command layer
- Service layer logic (consumes domain types, not library types)
- Migration orchestrator / workflow
