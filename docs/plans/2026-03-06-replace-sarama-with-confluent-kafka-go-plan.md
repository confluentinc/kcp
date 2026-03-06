# Replace Sarama with confluent-kafka-go — Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Replace the Sarama Kafka library with confluent-kafka-go, isolating library types behind domain types first, then swapping the implementation.

**Architecture:** Two-phase approach. Phase 1 introduces domain types at the `KafkaAdmin` interface boundary so callers never import Sarama. Phase 2 swaps Sarama internals for confluent-kafka-go. Each phase is independently testable.

**Tech Stack:** Go, confluent-kafka-go v2, aws-msk-iam-sasl-signer-go

**Design Doc:** `docs/plans/2026-03-06-replace-sarama-with-confluent-kafka-go-design.md`

---

## Phase 1: Isolate Sarama Types Behind Domain Types

### Task 1: Add new domain types to `internal/types/kafka.go`

**Files:**
- Modify: `internal/types/kafka.go`

**Step 1: Add `BrokerConfigEntry` and `BrokerInfo` types**

Add these types after the existing `Acls` struct (around line 40):

```go
type BrokerConfigEntry struct {
	Name      string `json:"name"`
	Value     string `json:"value"`
	IsDefault bool   `json:"is_default"`
}

type BrokerInfo struct {
	ID      int32  `json:"id"`
	Address string `json:"address"`
}
```

**Step 2: Verify it compiles**

Run: `go build ./internal/types/...`
Expected: success, no errors

**Step 3: Commit**

```
git add internal/types/kafka.go
git commit -m "refactor: add BrokerConfigEntry and BrokerInfo domain types"
```

---

### Task 2: Update the `KafkaAdmin` interface and `ClusterKafkaMetadata` to use domain types

**Files:**
- Modify: `internal/client/kafka_admin.go`

This task changes the interface signatures and the `ClusterKafkaMetadata` struct. The implementation methods will be updated to convert Sarama types to domain types internally.

**Step 1: Update `ClusterKafkaMetadata` struct**

In `internal/client/kafka_admin.go`, change `ClusterKafkaMetadata` (around line 140):

From:
```go
type ClusterKafkaMetadata struct {
	Brokers      []*sarama.Broker
	ControllerID int32
	ClusterID    string
}
```

To:
```go
type ClusterKafkaMetadata struct {
	Brokers      []types.BrokerInfo
	ControllerID int32
	ClusterID    string
}
```

Add `"github.com/confluentinc/kcp/internal/types"` to the import block (it's already imported).

**Step 2: Update the `KafkaAdmin` interface**

Change the interface (around line 147):

From:
```go
type KafkaAdmin interface {
	ListTopicsWithConfigs() (map[string]sarama.TopicDetail, error)
	GetClusterKafkaMetadata() (*ClusterKafkaMetadata, error)
	DescribeConfig() ([]sarama.ConfigEntry, error)
	ListAcls() ([]sarama.ResourceAcls, error)
	GetAllMessagesWithKeyFilter(topicName string, keyPrefix string) (map[string]string, error)
	GetConnectorStatusMessages(topicName string) (map[string]string, error)
	Close() error
}
```

To:
```go
type KafkaAdmin interface {
	ListTopicsWithConfigs() ([]types.TopicDetails, error)
	GetClusterKafkaMetadata() (*ClusterKafkaMetadata, error)
	DescribeConfig() ([]types.BrokerConfigEntry, error)
	ListAcls() ([]types.Acls, error)
	GetAllMessagesWithKeyFilter(topicName string, keyPrefix string) (map[string]string, error)
	GetConnectorStatusMessages(topicName string) (map[string]string, error)
	Close() error
}
```

**Step 3: Update `ListTopicsWithConfigs()` implementation to return `[]types.TopicDetails`**

The current implementation returns `map[string]sarama.TopicDetail`. Change the method signature and add conversion at the end. The Sarama calls stay the same internally — just convert before returning.

Change the method signature (around line 183):
```go
func (k *KafkaAdminClient) ListTopicsWithConfigs() ([]types.TopicDetails, error) {
```

Replace the return type from `map[string]sarama.TopicDetail` — keep all existing Sarama logic but convert the final `topicsDetailsMap` to `[]types.TopicDetails` before returning. Replace the final `return topicsDetailsMap, nil` with:

```go
	var result []types.TopicDetails
	for topicName, topic := range topicsDetailsMap {
		configurations := make(map[string]*string)
		for key, valuePtr := range topic.ConfigEntries {
			if valuePtr != nil {
				configurations[key] = valuePtr
			}
		}
		result = append(result, types.TopicDetails{
			Name:              topicName,
			Partitions:        int(topic.NumPartitions),
			ReplicationFactor: int(topic.ReplicationFactor),
			Configurations:    configurations,
		})
	}
	return result, nil
```

**Step 4: Update `DescribeConfig()` implementation to return `[]types.BrokerConfigEntry`**

Change the method (around line 258):

From:
```go
func (k *KafkaAdminClient) DescribeConfig() ([]sarama.ConfigEntry, error) {
	return k.admin.DescribeConfig(sarama.ConfigResource{
		Type: sarama.ConfigResourceType(sarama.ConfigResourceType(sarama.BrokerResource)),
		Name: "1",
	})
}
```

To:
```go
func (k *KafkaAdminClient) DescribeConfig() ([]types.BrokerConfigEntry, error) {
	entries, err := k.admin.DescribeConfig(sarama.ConfigResource{
		Type: sarama.ConfigResourceType(sarama.ConfigResourceType(sarama.BrokerResource)),
		Name: "1",
	})
	if err != nil {
		return nil, err
	}
	var result []types.BrokerConfigEntry
	for _, entry := range entries {
		result = append(result, types.BrokerConfigEntry{
			Name:      entry.Name,
			Value:     entry.Value,
			IsDefault: entry.Default,
		})
	}
	return result, nil
}
```

**Step 5: Update `GetClusterKafkaMetadata()` to return `[]types.BrokerInfo`**

The method currently returns `[]*sarama.Broker` in the struct. Update the method (around line 265) to convert brokers:

Change the return block from:
```go
	return &ClusterKafkaMetadata{
		Brokers:      brokers,
		ControllerID: controllerID,
		ClusterID:    clusterID,
	}, nil
```

To:
```go
	var brokerInfos []types.BrokerInfo
	for _, broker := range brokers {
		brokerInfos = append(brokerInfos, types.BrokerInfo{
			ID:      broker.ID(),
			Address: broker.Addr(),
		})
	}

	return &ClusterKafkaMetadata{
		Brokers:      brokerInfos,
		ControllerID: controllerID,
		ClusterID:    clusterID,
	}, nil
```

Also update the `getClusterIDFromBroker` helper — its parameter changes. Since `brokers` from `DescribeCluster()` returns `[]*sarama.Broker`, keep the helper accepting `*sarama.Broker` (it's internal to this file and will be removed in Phase 2).

**Step 6: Update `ListAcls()` to return `[]types.Acls`**

Change the method (around line 312):

From:
```go
func (k *KafkaAdminClient) ListAcls() ([]sarama.ResourceAcls, error) {
	...
	return result, nil
}
```

To:
```go
func (k *KafkaAdminClient) ListAcls() ([]types.Acls, error) {
	aclFilter := sarama.AclFilter{
		ResourceType:              sarama.AclResourceAny,
		ResourceName:              nil,
		ResourcePatternTypeFilter: sarama.AclPatternAny,
		Principal:                 nil,
		Host:                      nil,
		Operation:                 sarama.AclOperationAny,
		PermissionType:            sarama.AclPermissionAny,
	}

	resourceAcls, err := k.admin.ListAcls(aclFilter)
	if err != nil {
		return nil, fmt.Errorf("failed to list ACLs: %w", err)
	}

	var result []types.Acls
	for _, resourceAcl := range resourceAcls {
		for _, acl := range resourceAcl.Acls {
			result = append(result, types.Acls{
				ResourceType:        resourceAcl.ResourceType.String(),
				ResourceName:        resourceAcl.ResourceName,
				ResourcePatternType: resourceAcl.ResourcePatternType.String(),
				Principal:           acl.Principal,
				Host:                acl.Host,
				Operation:           acl.Operation.String(),
				PermissionType:      acl.PermissionType.String(),
			})
		}
	}

	return result, nil
}
```

**Step 7: Verify kafka_admin.go compiles (it won't yet — callers need updating)**

Run: `go build ./internal/client/...`
Expected: compilation errors in callers (mocks, service, tests) — this is expected, we fix those next.

---

### Task 3: Update mocks to use domain types

**Files:**
- Modify: `internal/mocks/mocks.go`

**Step 1: Update MockKafkaAdmin to use domain types**

Remove the `"github.com/IBM/sarama"` import. Update the struct and methods:

```go
type MockKafkaAdmin struct {
	ListTopicsWithConfigsFunc       func() ([]types.TopicDetails, error)
	GetClusterKafkaMetadataFunc     func() (*client.ClusterKafkaMetadata, error)
	DescribeConfigFunc              func() ([]types.BrokerConfigEntry, error)
	ListAclsFunc                    func() ([]types.Acls, error)
	GetAllMessagesWithKeyFilterFunc func(topicName string, keyPrefix string) (map[string]string, error)
	GetConnectorStatusMessagesFunc  func(topicName string) (map[string]string, error)
	CloseFunc                       func() error
}

func (m *MockKafkaAdmin) ListTopicsWithConfigs() ([]types.TopicDetails, error) {
	return m.ListTopicsWithConfigsFunc()
}

func (m *MockKafkaAdmin) GetClusterKafkaMetadata() (*client.ClusterKafkaMetadata, error) {
	return m.GetClusterKafkaMetadataFunc()
}

func (m *MockKafkaAdmin) DescribeConfig() ([]types.BrokerConfigEntry, error) {
	return m.DescribeConfigFunc()
}

func (m *MockKafkaAdmin) ListAcls() ([]types.Acls, error) {
	return m.ListAclsFunc()
}
```

The remaining methods (`GetAllMessagesWithKeyFilter`, `GetConnectorStatusMessages`, `Close`) stay the same.

**Step 2: Verify mocks compile**

Run: `go build ./internal/mocks/...`
Expected: success

**Step 3: Commit**

```
git add internal/mocks/mocks.go
git commit -m "refactor: update MockKafkaAdmin to use domain types"
```

---

### Task 4: Update `kafka_service.go` to work with new interface

**Files:**
- Modify: `internal/services/kafka/kafka_service.go`

**Step 1: Simplify `scanClusterTopics()`**

The method currently receives `map[string]sarama.TopicDetail` and converts to `[]types.TopicDetails`. Since `ListTopicsWithConfigs()` now returns `[]types.TopicDetails` directly, simplify:

```go
func (ks *KafkaService) scanClusterTopics() ([]types.TopicDetails, error) {
	slog.Info("scanning for cluster topics", "clusterArn", ks.clusterArn)

	topics, err := ks.client.ListTopicsWithConfigs()
	if err != nil {
		return nil, fmt.Errorf("Failed to list topics with configs: %v", err)
	}

	slog.Info("found topics", "count", len(topics))
	return topics, nil
}
```

**Step 2: Simplify `scanKafkaAcls()`**

The method currently receives `[]sarama.ResourceAcls` and flattens to `[]types.Acls`. Since `ListAcls()` now returns `[]types.Acls` directly, simplify:

```go
func (ks *KafkaService) scanKafkaAcls() ([]types.Acls, error) {
	slog.Info("scanning for kafka acls", "clusterArn", ks.clusterArn)

	acls, err := ks.client.ListAcls()
	if err != nil {
		return nil, fmt.Errorf("Failed to list acls: %v", err)
	}

	return acls, nil
}
```

**Step 3: Verify service compiles**

Run: `go build ./internal/services/kafka/...`
Expected: success

---

### Task 5: Update `kafka_service_test.go` to use domain types

**Files:**
- Modify: `internal/services/kafka/kafka_service_test.go`

**Step 1: Remove Sarama import and update all mock data**

Remove `"github.com/IBM/sarama"` from imports.

Update all `ListTopicsWithConfigsFunc` lambdas — change from returning `map[string]sarama.TopicDetail` to `[]types.TopicDetails`. For example:

```go
// Old:
ListTopicsWithConfigsFunc: func() (map[string]sarama.TopicDetail, error) {
    return map[string]sarama.TopicDetail{
        "serverless-topic": {
            NumPartitions:     int32(1),
            ReplicationFactor: int16(1),
            ConfigEntries:     map[string]*string{},
        },
    }, nil
},

// New:
ListTopicsWithConfigsFunc: func() ([]types.TopicDetails, error) {
    return []types.TopicDetails{
        {
            Name:              "serverless-topic",
            Partitions:        1,
            ReplicationFactor: 1,
            Configurations:    map[string]*string{},
        },
    }, nil
},
```

Update all `ListAclsFunc` lambdas — change from returning `[]sarama.ResourceAcls` to `[]types.Acls`. For example:

```go
// Old:
ListAclsFunc: func() ([]sarama.ResourceAcls, error) {
    return []sarama.ResourceAcls{
        {
            Resource: sarama.Resource{
                ResourceType:        sarama.AclResourceTopic,
                ResourceName:        "orders",
                ResourcePatternType: sarama.AclPatternLiteral,
            },
            Acls: []*sarama.Acl{
                {
                    Principal:      "User:orders-service",
                    Host:           "*",
                    Operation:      sarama.AclOperationWrite,
                    PermissionType: sarama.AclPermissionAllow,
                },
            },
        },
    }, nil
},

// New:
ListAclsFunc: func() ([]types.Acls, error) {
    return []types.Acls{
        {
            ResourceType:        "Topic",
            ResourceName:        "orders",
            ResourcePatternType: "Literal",
            Principal:           "User:orders-service",
            Host:                "*",
            Operation:           "Write",
            PermissionType:      "Allow",
        },
    }, nil
},
```

Note: ACLs are now pre-flattened (one entry per ACL, not nested). Update test assertions accordingly — the `scanKafkaAcls` test that checks flattening now just verifies pass-through since flattening moved to `kafka_admin.go`.

**Step 2: Run tests**

Run: `go test ./internal/services/kafka/... -v`
Expected: all tests pass

**Step 3: Commit**

```
git add internal/services/kafka/kafka_service.go internal/services/kafka/kafka_service_test.go
git commit -m "refactor: update kafka service and tests to use domain types"
```

---

### Task 6: Update `kafka_admin_test.go` to use domain types

**Files:**
- Modify: `internal/client/kafka_admin_test.go`

**Step 1: Update `TestClusterKafkaMetadata_Structure`**

Change from using `*sarama.Broker` to `types.BrokerInfo`:

```go
func TestClusterKafkaMetadata_Structure(t *testing.T) {
	metadata := &ClusterKafkaMetadata{
		Brokers: []types.BrokerInfo{
			{ID: 1, Address: "broker1:9092"},
			{ID: 2, Address: "broker2:9092"},
		},
		ControllerID: 1,
		ClusterID:    "test-cluster",
	}

	assert.Len(t, metadata.Brokers, 2)
	assert.Equal(t, int32(1), metadata.ControllerID)
	assert.Equal(t, "test-cluster", metadata.ClusterID)
}
```

**Step 2: Update `TestSaramaKafkaVersionParsing`**

This test validates Kafka version string parsing using `sarama.ParseKafkaVersion`. For Phase 1, keep this test as-is since Sarama is still used internally. It will be updated in Phase 2.

**Step 3: Remove the `MockClusterAdmin` and `MockBroker` structs**

These mock Sarama's internal `ClusterAdmin` interface — a massive mock with ~20 stub methods. They're only used by skipped integration tests (`TestNewKafkaAdmin` etc). Remove:
- `MockClusterAdmin` struct and all its methods (lines 26-138)
- `MockBroker` struct and all its methods (lines 144-167)

The tests that use them (`TestNewKafkaAdmin`, `TestNewKafkaAdmin_DefaultConfiguration`, `TestNewKafkaAdmin_MultipleOptions`) are all `t.Skip`'d integration tests — they don't use these mocks anyway.

**Step 4: Remove Sarama import if no longer needed**

Check if `"github.com/IBM/sarama"` is still needed. The remaining tests that reference Sarama directly are:
- `TestConfigureCommonSettings` — tests internal Sarama config
- `TestConfigureSASLTypeOAuthAuthentication` — tests internal Sarama config
- `TestConfigureSASLTypeSCRAMAuthentication` — tests internal Sarama config
- `TestConfigureUnauthenticatedAuthentication` — tests internal Sarama config
- `TestConfigureTLSAuth` — tests internal Sarama config
- `TestSaramaKafkaVersionParsing` — tests Sarama version parsing

These test internal implementation details (Sarama config functions). Keep the Sarama import for now — these tests will be replaced in Phase 2.

**Step 5: Run tests**

Run: `go test ./internal/client/... -v`
Expected: all tests pass

**Step 6: Commit**

```
git add internal/client/kafka_admin.go internal/client/kafka_admin_test.go
git commit -m "refactor: isolate Sarama types behind domain types in KafkaAdmin interface"
```

---

### Task 7: Verify full Phase 1 — all tests pass, no Sarama leaks outside client package

**Step 1: Run all tests**

Run: `go test ./... -count=1`
Expected: all tests pass

**Step 2: Verify Sarama is only imported in `internal/client/`**

Run: `grep -r '"github.com/IBM/sarama"' --include='*.go' . | grep -v '_test.go' | grep -v 'internal/client/'`
Expected: no output (Sarama is only used in `internal/client/` production code)

**Step 3: Commit (if any fixups needed)**

```
git commit -m "refactor: phase 1 complete — Sarama isolated behind domain types"
```

---

## Phase 2: Swap Sarama for confluent-kafka-go

### Task 8: Replace auth configuration with confluent-kafka-go ConfigMap

**Files:**
- Modify: `internal/client/kafka_admin.go`

**Step 1: Replace auth helper functions**

Remove these Sarama-specific functions:
- `configureSASLTypeOAuthAuthentication`
- `configureSASLTypeSCRAMAuthentication`
- `configureUnauthenticatedAuthentication`
- `configureTLSAuth`
- `configureCommonSettings`

Add a new function that builds a `kafka.ConfigMap` from `AdminConfig`:

```go
import (
	"github.com/confluentinc/confluent-kafka-go/v2/kafka"
)

func buildConfigMap(brokerAddresses []string, config AdminConfig) (*kafka.ConfigMap, error) {
	bootstrapServers := strings.Join(brokerAddresses, ",")

	configMap := &kafka.ConfigMap{
		"bootstrap.servers": bootstrapServers,
		"client.id":         "kcp-cli",
		"socket.timeout.ms": 10000,
		"request.timeout.ms": 30000,
	}

	switch config.authType {
	case types.AuthTypeIAM:
		configMap.SetKey("security.protocol", "SASL_SSL")
		configMap.SetKey("sasl.mechanisms", "OAUTHBEARER")
	case types.AuthTypeSASLSCRAM:
		configMap.SetKey("security.protocol", "SASL_SSL")
		configMap.SetKey("sasl.mechanisms", "SCRAM-SHA-512")
		configMap.SetKey("sasl.username", config.username)
		configMap.SetKey("sasl.password", config.password)
	case types.AuthTypeUnauthenticatedTLS:
		configMap.SetKey("security.protocol", "SSL")
	case types.AuthTypeUnauthenticatedPlaintext:
		configMap.SetKey("security.protocol", "PLAINTEXT")
	case types.AuthTypeTLS:
		configMap.SetKey("security.protocol", "SSL")
		configMap.SetKey("ssl.ca.location", config.caCertFile)
		configMap.SetKey("ssl.certificate.location", config.clientCertFile)
		configMap.SetKey("ssl.key.location", config.clientKeyFile)
	default:
		return nil, fmt.Errorf("auth type: %v not yet supported", config.authType)
	}

	return configMap, nil
}
```

**Step 2: Don't compile yet — continue to Task 9**

---

### Task 9: Replace `KafkaAdminClient` struct and `NewKafkaAdmin` constructor

**Files:**
- Modify: `internal/client/kafka_admin.go`

**Step 1: Update the struct**

Replace:
```go
type KafkaAdminClient struct {
	admin           sarama.ClusterAdmin
	region          string
	config          AdminConfig
	saramaConfig    *sarama.Config
	resourceAcls    map[string]sarama.ResourceAcls
	brokerAddresses []string
}
```

With:
```go
type KafkaAdminClient struct {
	admin           *kafka.AdminClient
	region          string
	config          AdminConfig
	configMap       *kafka.ConfigMap
	brokerAddresses []string
}
```

**Step 2: Update `NewKafkaAdmin`**

Replace the constructor to use `kafka.NewAdminClient`:

```go
func NewKafkaAdmin(brokerAddresses []string, clientBrokerEncryptionInTransit kafkatypes.ClientBroker, region string, kafkaVersion string, opts ...AdminOption) (KafkaAdmin, error) {
	config := AdminConfig{
		authType: types.AuthTypeIAM,
	}
	for _, opt := range opts {
		opt(&config)
	}

	configMap, err := buildConfigMap(brokerAddresses, config)
	if err != nil {
		return nil, fmt.Errorf("failed to build config: %v", err)
	}

	admin, err := kafka.NewAdminClient(configMap)
	if err != nil {
		return nil, fmt.Errorf("failed to create admin client: authType=%v brokerAddresses=%v error=%v", config.authType, brokerAddresses, err)
	}

	// Handle OAuthBearer token refresh for IAM auth
	if config.authType == types.AuthTypeIAM {
		go handleOAuthTokenRefresh(admin, region)
	}

	return &KafkaAdminClient{
		admin:           admin,
		region:          region,
		config:          config,
		configMap:       configMap,
		brokerAddresses: brokerAddresses,
	}, nil
}
```

**Step 3: Add OAuth token refresh handler**

```go
func handleOAuthTokenRefresh(client kafka.Handle, region string) {
	for {
		ev := <-client.Events()
		switch e := ev.(type) {
		case kafka.OAuthBearerTokenRefresh:
			token, expirationTimeMs, err := signer.GenerateAuthToken(context.TODO(), region)
			if err != nil {
				_ = client.SetOAuthBearerTokenFailure(err.Error())
				slog.Error("failed to generate IAM auth token", "error", err)
				continue
			}
			oauthToken := kafka.OAuthBearerToken{
				TokenValue: token,
				Expiration: time.UnixMilli(expirationTimeMs),
			}
			err = client.SetOAuthBearerToken(oauthToken)
			if err != nil {
				slog.Error("failed to set OAuthBearer token", "error", err)
			}
		default:
			_ = e // ignore other events
		}
	}
}
```

**Step 4: Don't compile yet — continue to Task 10**

---

### Task 10: Replace admin operation implementations

**Files:**
- Modify: `internal/client/kafka_admin.go`

**Step 1: Replace `ListTopicsWithConfigs()`**

Replace the entire method with confluent-kafka-go's `GetMetadata` + `DescribeConfigs`:

```go
func (k *KafkaAdminClient) ListTopicsWithConfigs() ([]types.TopicDetails, error) {
	metadata, err := k.admin.GetMetadata(nil, true, 15000)
	if err != nil {
		return nil, fmt.Errorf("failed to get metadata: %w", err)
	}

	if len(metadata.Topics) == 0 {
		slog.Warn("no topics found in metadata response")
		return nil, nil
	}

	// Build config resources for all topics
	var configResources []kafka.ConfigResource
	for topicName := range metadata.Topics {
		configResources = append(configResources, kafka.ConfigResource{
			Type: kafka.ResourceTopic,
			Name: topicName,
		})
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	configResults, err := k.admin.DescribeConfigs(ctx, configResources)
	if err != nil {
		return nil, fmt.Errorf("failed to describe configs: %w", err)
	}

	// Build a map of topic name -> config entries for quick lookup
	topicConfigs := make(map[string][]kafka.ConfigEntryResult)
	for _, result := range configResults {
		topicConfigs[result.Name] = result.Config
	}

	var result []types.TopicDetails
	for topicName, topicMeta := range metadata.Topics {
		if topicMeta.Error.Code() != kafka.ErrNoError {
			continue
		}

		replicationFactor := 0
		if len(topicMeta.Partitions) > 0 {
			replicationFactor = len(topicMeta.Partitions[0].Replicas)
		}

		configurations := make(map[string]*string)
		if configs, ok := topicConfigs[topicName]; ok {
			for _, entry := range configs {
				val := entry.Value
				configurations[entry.Name] = &val
			}
		}

		result = append(result, types.TopicDetails{
			Name:              topicName,
			Partitions:        len(topicMeta.Partitions),
			ReplicationFactor: replicationFactor,
			Configurations:    configurations,
		})
	}

	return result, nil
}
```

**Step 2: Replace `DescribeConfig()`**

```go
func (k *KafkaAdminClient) DescribeConfig() ([]types.BrokerConfigEntry, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	results, err := k.admin.DescribeConfigs(ctx, []kafka.ConfigResource{
		{Type: kafka.ResourceBroker, Name: "1"},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to describe broker config: %w", err)
	}

	var entries []types.BrokerConfigEntry
	for _, result := range results {
		for _, entry := range result.Config {
			entries = append(entries, types.BrokerConfigEntry{
				Name:      entry.Name,
				Value:     entry.Value,
				IsDefault: entry.IsDefault,
			})
		}
	}
	return entries, nil
}
```

**Step 3: Replace `GetClusterKafkaMetadata()`**

```go
func (k *KafkaAdminClient) GetClusterKafkaMetadata() (*ClusterKafkaMetadata, error) {
	metadata, err := k.admin.GetMetadata(nil, false, 15000)
	if err != nil {
		return nil, fmt.Errorf("failed to get cluster metadata: %w", err)
	}

	var brokerInfos []types.BrokerInfo
	for _, broker := range metadata.Brokers {
		brokerInfos = append(brokerInfos, types.BrokerInfo{
			ID:      broker.ID,
			Address: broker.Host,
		})
	}

	return &ClusterKafkaMetadata{
		Brokers:      brokerInfos,
		ControllerID: metadata.OriginatingBroker.ID,
		ClusterID:    metadata.ClusterID,
	}, nil
}
```

Note: Check the actual confluent-kafka-go `Metadata` struct fields — `ClusterID` may need to be accessed differently. The `OriginatingBroker` field gives the controller info. Verify the exact API at implementation time.

**Step 4: Replace `ListAcls()`**

```go
func (k *KafkaAdminClient) ListAcls() ([]types.Acls, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	aclBindingFilter := kafka.ACLBindingFilter{
		Type:                kafka.ResourceAny,
		PatternTypeFilter:   kafka.ResourcePatternTypeAny,
		Operation:           kafka.ACLOperationAny,
		PermissionType:      kafka.ACLPermissionTypeAny,
	}

	aclResults, err := k.admin.DescribeACLs(ctx, aclBindingFilter)
	if err != nil {
		return nil, fmt.Errorf("failed to list ACLs: %w", err)
	}

	var result []types.Acls
	for _, binding := range aclResults.ACLBindings {
		result = append(result, types.Acls{
			ResourceType:        binding.Type.String(),
			ResourceName:        binding.Name,
			ResourcePatternType: binding.ResourcePatternType.String(),
			Principal:           binding.Principal,
			Host:                binding.Host,
			Operation:           binding.Operation.String(),
			PermissionType:      binding.PermissionType.String(),
		})
	}

	return result, nil
}
```

Note: Verify exact confluent-kafka-go ACL API — the `DescribeACLs` method signature and `ACLBindingFilter` struct fields may vary. Check the godoc at implementation time.

**Step 5: Replace `Close()`**

```go
func (k *KafkaAdminClient) Close() error {
	k.admin.Close()
	return nil
}
```

---

### Task 11: Replace consumer operations

**Files:**
- Modify: `internal/client/kafka_admin.go`

**Step 1: Replace `GetAllMessagesWithKeyFilter()`**

```go
func (k *KafkaAdminClient) GetAllMessagesWithKeyFilter(topicName string, keyPrefix string) (map[string]string, error) {
	consumerConfig := k.configMap.Clone()
	_ = consumerConfig.SetKey("group.id", "kcp-cli-temp-"+topicName)
	_ = consumerConfig.SetKey("auto.offset.reset", "earliest")
	_ = consumerConfig.SetKey("enable.auto.commit", false)

	consumer, err := kafka.NewConsumer(consumerConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create consumer: %w", err)
	}
	defer consumer.Close()

	// Get partition metadata
	metadata, err := consumer.GetMetadata(&topicName, false, 10000)
	if err != nil {
		return nil, fmt.Errorf("failed to get metadata for topic %s: %w", topicName, err)
	}

	topicMeta, ok := metadata.Topics[topicName]
	if !ok || len(topicMeta.Partitions) == 0 {
		return nil, fmt.Errorf("topic %s has no partitions", topicName)
	}

	// Assign all partitions from beginning
	var partitions []kafka.TopicPartition
	for _, p := range topicMeta.Partitions {
		partitions = append(partitions, kafka.TopicPartition{
			Topic:     &topicName,
			Partition: p.ID,
			Offset:    kafka.OffsetBeginning,
		})
	}

	err = consumer.Assign(partitions)
	if err != nil {
		return nil, fmt.Errorf("failed to assign partitions: %w", err)
	}

	connectorConfigs := make(map[string]string)
	deadline := time.Now().Add(30 * time.Second)
	lastMessageTime := time.Now()

	for time.Now().Before(deadline) {
		msg, err := consumer.ReadMessage(5 * time.Second)
		if err != nil {
			// Timeout means no more messages
			if time.Since(lastMessageTime) >= 5*time.Second {
				break
			}
			continue
		}

		lastMessageTime = time.Now()
		if len(msg.Key) > 0 {
			keyStr := string(msg.Key)
			if len(keyStr) >= len(keyPrefix) && keyStr[:len(keyPrefix)] == keyPrefix {
				connectorConfigs[keyStr] = string(msg.Value)
			}
		}
	}

	return connectorConfigs, nil
}
```

**Step 2: Replace `GetConnectorStatusMessages()`**

Follow the same pattern as above but with the status-connector key filtering logic. The structure is identical — use `consumer.ReadMessage()` with timeout-based termination, filtering by `status-connector-` prefix and tracking timestamps.

```go
func (k *KafkaAdminClient) GetConnectorStatusMessages(topicName string) (map[string]string, error) {
	consumerConfig := k.configMap.Clone()
	_ = consumerConfig.SetKey("group.id", "kcp-cli-temp-status-"+topicName)
	_ = consumerConfig.SetKey("auto.offset.reset", "earliest")
	_ = consumerConfig.SetKey("enable.auto.commit", false)

	consumer, err := kafka.NewConsumer(consumerConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create consumer: %w", err)
	}
	defer consumer.Close()

	metadata, err := consumer.GetMetadata(&topicName, false, 10000)
	if err != nil {
		return nil, fmt.Errorf("failed to get metadata for topic %s: %w", topicName, err)
	}

	topicMeta, ok := metadata.Topics[topicName]
	if !ok || len(topicMeta.Partitions) == 0 {
		return nil, fmt.Errorf("topic %s has no partitions", topicName)
	}

	var partitions []kafka.TopicPartition
	for _, p := range topicMeta.Partitions {
		partitions = append(partitions, kafka.TopicPartition{
			Topic:     &topicName,
			Partition: p.ID,
			Offset:    kafka.OffsetBeginning,
		})
	}

	err = consumer.Assign(partitions)
	if err != nil {
		return nil, fmt.Errorf("failed to assign partitions: %w", err)
	}

	connectorStatuses := make(map[string]string)
	connectorTimestamps := make(map[string]int64)
	deadline := time.Now().Add(30 * time.Second)
	lastMessageTime := time.Now()

	for time.Now().Before(deadline) {
		msg, err := consumer.ReadMessage(5 * time.Second)
		if err != nil {
			if time.Since(lastMessageTime) >= 5*time.Second {
				break
			}
			continue
		}

		lastMessageTime = time.Now()
		if len(msg.Key) > 0 {
			keyStr := string(msg.Key)
			if len(keyStr) >= 17 && keyStr[:17] == "status-connector-" {
				connectorName := keyStr[17:]

				var msgTimestamp int64
				if msg.Timestamp.IsZero() {
					msgTimestamp = int64(msg.TopicPartition.Offset)
				} else {
					msgTimestamp = msg.Timestamp.UnixMilli()
				}

				if existingTimestamp, exists := connectorTimestamps[connectorName]; !exists || msgTimestamp > existingTimestamp {
					connectorStatuses[connectorName] = string(msg.Value)
					connectorTimestamps[connectorName] = msgTimestamp
				}
			}
		}
	}

	return connectorStatuses, nil
}
```

Note: The `ConfigMap.Clone()` method may not exist — check the API. If not, build a new `ConfigMap` from scratch using `buildConfigMap` with additional consumer-specific keys.

---

### Task 12: Remove Sarama-specific code and dependencies

**Files:**
- Modify: `internal/client/kafka_admin.go` — remove old helper functions and `MSKAccessTokenProvider`
- Delete: `internal/client/scram_client.go` — no longer needed (confluent-kafka-go handles SCRAM internally)
- Modify: `go.mod` — remove `github.com/IBM/sarama` and `github.com/xdg-go/scram`

**Step 1: Remove from kafka_admin.go**

Remove:
- `MSKAccessTokenProvider` struct and its `Token()` method
- `configureSASLTypeOAuthAuthentication` (if not already removed in Task 8)
- `configureSASLTypeSCRAMAuthentication`
- `configureUnauthenticatedAuthentication`
- `configureTLSAuth`
- `configureCommonSettings`
- `getClusterIDFromBroker` helper
- All `sarama` imports

**Step 2: Delete scram_client.go**

```
rm internal/client/scram_client.go
```

**Step 3: Remove Sarama from go.mod**

```
go mod tidy
```

This should remove `github.com/IBM/sarama` and its transitive dependencies (`github.com/xdg-go/scram` is still needed if used elsewhere — `go mod tidy` will handle this).

**Step 4: Verify it compiles**

Run: `go build ./...`
Expected: success

**Step 5: Commit**

```
git add -A
git commit -m "refactor: remove Sarama dependency, replace with confluent-kafka-go"
```

---

### Task 13: Update `kafka_admin_test.go` for Phase 2

**Files:**
- Modify: `internal/client/kafka_admin_test.go`

**Step 1: Remove all Sarama-specific tests**

Remove these tests that test Sarama internals:
- `TestConfigureCommonSettings`
- `TestConfigureSASLTypeOAuthAuthentication`
- `TestConfigureSASLTypeSCRAMAuthentication`
- `TestConfigureUnauthenticatedAuthentication`
- `TestConfigureTLSAuth`
- `TestSaramaKafkaVersionParsing`

**Step 2: Add `buildConfigMap` tests**

```go
func TestBuildConfigMap(t *testing.T) {
	tests := []struct {
		name         string
		brokers      []string
		config       AdminConfig
		expectError  bool
		expectKeys   map[string]string
	}{
		{
			name:    "IAM auth config",
			brokers: []string{"broker1:9098", "broker2:9098"},
			config:  AdminConfig{authType: types.AuthTypeIAM},
			expectKeys: map[string]string{
				"security.protocol": "SASL_SSL",
				"sasl.mechanisms":   "OAUTHBEARER",
			},
		},
		{
			name:    "SASL/SCRAM auth config",
			brokers: []string{"broker1:9096"},
			config:  AdminConfig{authType: types.AuthTypeSASLSCRAM, username: "user", password: "pass"},
			expectKeys: map[string]string{
				"security.protocol": "SASL_SSL",
				"sasl.mechanisms":   "SCRAM-SHA-512",
				"sasl.username":     "user",
				"sasl.password":     "pass",
			},
		},
		{
			name:    "TLS auth config",
			brokers: []string{"broker1:9094"},
			config:  AdminConfig{authType: types.AuthTypeTLS, caCertFile: "ca.crt", clientCertFile: "client.crt", clientKeyFile: "client.key"},
			expectKeys: map[string]string{
				"security.protocol":      "SSL",
				"ssl.ca.location":        "ca.crt",
				"ssl.certificate.location": "client.crt",
				"ssl.key.location":       "client.key",
			},
		},
		{
			name:    "unauthenticated TLS config",
			brokers: []string{"broker1:9094"},
			config:  AdminConfig{authType: types.AuthTypeUnauthenticatedTLS},
			expectKeys: map[string]string{
				"security.protocol": "SSL",
			},
		},
		{
			name:    "unauthenticated plaintext config",
			brokers: []string{"broker1:9092"},
			config:  AdminConfig{authType: types.AuthTypeUnauthenticatedPlaintext},
			expectKeys: map[string]string{
				"security.protocol": "PLAINTEXT",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			configMap, err := buildConfigMap(tt.brokers, tt.config)
			if tt.expectError {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			for key, expected := range tt.expectKeys {
				val, err := configMap.Get(key, "")
				require.NoError(t, err)
				assert.Equal(t, expected, val)
			}
		})
	}
}
```

**Step 3: Keep and update remaining tests**

Keep `TestAdminOptionFunctions`, `TestKafkaAdminInterface`, `TestClusterKafkaMetadata_Structure`, and `TestMSKAccessTokenProvider_Token` (update the last one to use the new token refresh approach if the `MSKAccessTokenProvider` struct was removed).

**Step 4: Run tests**

Run: `go test ./internal/client/... -v`
Expected: all tests pass

**Step 5: Commit**

```
git add internal/client/kafka_admin_test.go
git commit -m "test: update kafka admin tests for confluent-kafka-go"
```

---

### Task 14: Final verification

**Step 1: Run full test suite**

Run: `go test ./... -count=1`
Expected: all tests pass

**Step 2: Verify no Sarama references remain**

Run: `grep -r 'sarama' --include='*.go' . | grep -v vendor/ | grep -v go.sum`
Expected: only the kafka trace line parser test (which references "sarama" as a Kafka client ID string in test data, not as an import)

**Step 3: Verify go.mod is clean**

Run: `go mod tidy && go mod verify`
Expected: no changes, verification succeeds

**Step 4: Build for current platform**

Run: `go build ./...`
Expected: success

**Step 5: Commit final state**

```
git add -A
git commit -m "refactor: complete migration from Sarama to confluent-kafka-go"
```
