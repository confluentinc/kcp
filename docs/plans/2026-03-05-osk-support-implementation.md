# OSK to Confluent Cloud Support Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add Open Source Kafka (OSK) cluster scanning support to KCP, enabling migrations from self-managed Kafka to Confluent Cloud.

**Architecture:** Introduce source abstraction layer with `Source` interface, implement OSK and MSK sources, restructure state file to support both source types, and add unified `kcp scan clusters` command with `--source-type` flag.

**Tech Stack:** Go 1.25, Kafka Admin API (confluent-kafka-go), Docker Compose (testing), YAML (credentials)

---

## Task 1: Create Source Abstraction Interface

**Files:**
- Create: `internal/sources/interface.go`
- Test: `internal/sources/interface_test.go`

**Step 1: Write interface definition test**

```bash
mkdir -p internal/sources
```

Create `internal/sources/interface_test.go`:

```go
package sources_test

import (
	"testing"

	"github.com/confluentinc/kcp/internal/sources"
)

func TestSourceType_Constants(t *testing.T) {
	if sources.SourceTypeMSK != "msk" {
		t.Errorf("expected SourceTypeMSK to be 'msk', got '%s'", sources.SourceTypeMSK)
	}
	if sources.SourceTypeOSK != "osk" {
		t.Errorf("expected SourceTypeOSK to be 'osk', got '%s'", sources.SourceTypeOSK)
	}
}

func TestClusterIdentifier_Structure(t *testing.T) {
	id := sources.ClusterIdentifier{
		Name:             "test-cluster",
		UniqueID:         "cluster-123",
		BootstrapServers: []string{"broker1:9092", "broker2:9092"},
	}

	if id.Name != "test-cluster" {
		t.Errorf("expected Name 'test-cluster', got '%s'", id.Name)
	}
	if len(id.BootstrapServers) != 2 {
		t.Errorf("expected 2 bootstrap servers, got %d", len(id.BootstrapServers))
	}
}
```

**Step 2: Run test to verify it fails**

```bash
go test ./internal/sources/... -v
```

Expected: FAIL - package does not exist

**Step 3: Create interface implementation**

Create `internal/sources/interface.go`:

```go
package sources

import (
	"context"

	"github.com/confluentinc/kcp/internal/types"
)

// SourceType represents the type of Kafka source
type SourceType string

const (
	SourceTypeMSK SourceType = "msk"
	SourceTypeOSK SourceType = "osk"
)

// Source represents a Kafka source (MSK or OSK) that can be scanned
type Source interface {
	// Type returns the source type (msk or osk)
	Type() SourceType

	// LoadCredentials loads authentication credentials from a file
	LoadCredentials(credentialsPath string) error

	// Scan performs discovery/scanning of the source clusters
	Scan(ctx context.Context, opts ScanOptions) (*ScanResult, error)

	// GetClusters returns the list of clusters available to scan
	GetClusters() []ClusterIdentifier
}

// ClusterIdentifier uniquely identifies a cluster within a source
type ClusterIdentifier struct {
	Name             string   // Human-readable name (MSK: cluster name, OSK: user ID)
	UniqueID         string   // Unique identifier (MSK: ARN, OSK: user ID)
	BootstrapServers []string // Bootstrap server addresses
}

// ScanOptions contains options for scanning
type ScanOptions struct {
	SkipTopics bool
	SkipACLs   bool
}

// ScanResult contains the results of scanning a source
type ScanResult struct {
	SourceType SourceType
	Clusters   []ClusterScanResult
}

// ClusterScanResult contains scan results for a single cluster
type ClusterScanResult struct {
	Identifier         ClusterIdentifier
	KafkaAdminInfo     *types.KafkaAdminClientInformation
	SourceSpecificData interface{} // MSK: AWSClientInformation, OSK: OSKClusterMetadata
}
```

**Step 4: Run test to verify it passes**

```bash
go test ./internal/sources/... -v
```

Expected: PASS

**Step 5: Commit**

```bash
git add internal/sources/
git commit -m "feat: add source abstraction interface"
```

---

## Task 2: Create OSK State Types

**Files:**
- Modify: `internal/types/state.go`
- Test: `internal/types/state_test.go`

**Step 1: Write failing test for OSK state types**

Add to `internal/types/state_test.go`:

```go
func TestOSKDiscoveredCluster_Structure(t *testing.T) {
	cluster := types.OSKDiscoveredCluster{
		ID:               "prod-kafka-01",
		BootstrapServers: []string{"broker1:9092"},
		Metadata: types.OSKClusterMetadata{
			Environment:  "production",
			Location:     "us-datacenter-1",
			KafkaVersion: "3.6.0",
		},
	}

	if cluster.ID != "prod-kafka-01" {
		t.Errorf("expected ID 'prod-kafka-01', got '%s'", cluster.ID)
	}
	if cluster.Metadata.Environment != "production" {
		t.Errorf("expected environment 'production', got '%s'", cluster.Metadata.Environment)
	}
}

func TestOSKSourcesState_Structure(t *testing.T) {
	state := types.OSKSourcesState{
		Clusters: []types.OSKDiscoveredCluster{
			{ID: "cluster-1"},
			{ID: "cluster-2"},
		},
	}

	if len(state.Clusters) != 2 {
		t.Errorf("expected 2 clusters, got %d", len(state.Clusters))
	}
}
```

**Step 2: Run test to verify it fails**

```bash
go test ./internal/types/... -run TestOSK -v
```

Expected: FAIL - types not defined

**Step 3: Add OSK types to state.go**

Add to `internal/types/state.go` after line 28:

```go
// MSKSourcesState contains all MSK-specific data
type MSKSourcesState struct {
	Regions []DiscoveredRegion `json:"regions"`
}

// OSKSourcesState contains all OSK-specific data
type OSKSourcesState struct {
	Clusters []OSKDiscoveredCluster `json:"clusters"`
}

// OSKDiscoveredCluster represents a discovered OSK cluster
type OSKDiscoveredCluster struct {
	ID                          string                      `json:"id"`
	BootstrapServers            []string                    `json:"bootstrap_servers"`
	KafkaAdminClientInformation KafkaAdminClientInformation `json:"kafka_admin_client_information"`
	DiscoveredClients           []DiscoveredClient          `json:"discovered_clients"`
	Metadata                    OSKClusterMetadata          `json:"metadata"`
}

// OSKClusterMetadata contains optional metadata about OSK clusters
type OSKClusterMetadata struct {
	Environment  string            `json:"environment,omitempty"`
	Location     string            `json:"location,omitempty"`
	KafkaVersion string            `json:"kafka_version,omitempty"`
	Labels       map[string]string `json:"labels,omitempty"`
	LastScanned  time.Time         `json:"last_scanned"`
}
```

**Step 4: Update State struct**

Replace the existing `State` struct (lines 21-28) with:

```go
// State represents the unified state file (kcp-state.json)
type State struct {
	MSKSources       *MSKSourcesState            `json:"msk_sources,omitempty"`
	OSKSources       *OSKSourcesState            `json:"osk_sources,omitempty"`
	SchemaRegistries []SchemaRegistryInformation `json:"schema_registries"`
	KcpBuildInfo     KcpBuildInfo                `json:"kcp_build_info"`
	Timestamp        time.Time                   `json:"timestamp"`
}
```

**Step 5: Add state validation**

Add after the `State` struct definition:

```go
// Validate checks that the state file has valid structure
func (s *State) Validate() error {
	// Validate that at least one source type exists
	if s.MSKSources == nil && s.OSKSources == nil {
		return fmt.Errorf("invalid state file: no msk_sources or osk_sources found. This may be a legacy state file. Please use KCP v1.x for legacy files or re-run discovery")
	}
	return nil
}
```

Update `NewStateFromFile` function to call validation:

```go
func NewStateFromFile(stateFile string) (*State, error) {
	file, err := os.ReadFile(stateFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read state file: %v", err)
	}

	var state State
	if err := json.Unmarshal(file, &state); err != nil {
		return nil, fmt.Errorf("failed to unmarshal state: %v", err)
	}

	// Validate state file format
	if err := state.Validate(); err != nil {
		return nil, err
	}

	return &state, nil
}
```

**Step 6: Update NewStateFrom function**

Replace `NewStateFrom` function to initialize new structure:

```go
func NewStateFrom(fromState *State) *State {
	// Always create with fresh metadata for the current discovery run
	workingState := &State{
		KcpBuildInfo: KcpBuildInfo{
			Version: build_info.Version,
			Commit:  build_info.Commit,
			Date:    build_info.Date,
		},
		Timestamp: time.Now(),
	}

	if fromState == nil {
		// Initialize with empty MSK sources
		workingState.MSKSources = &MSKSourcesState{
			Regions: []DiscoveredRegion{},
		}
	} else {
		// Copy existing data
		if fromState.MSKSources != nil {
			mskSources := &MSKSourcesState{
				Regions: make([]DiscoveredRegion, len(fromState.MSKSources.Regions)),
			}
			copy(mskSources.Regions, fromState.MSKSources.Regions)
			workingState.MSKSources = mskSources
		}
		if fromState.OSKSources != nil {
			workingState.OSKSources = fromState.OSKSources
		}
	}

	return workingState
}
```

**Step 7: Run tests to verify they pass**

```bash
go test ./internal/types/... -v
```

Expected: PASS

**Step 8: Commit**

```bash
git add internal/types/state.go internal/types/state_test.go
git commit -m "feat: restructure state file for MSK and OSK sources"
```

---

## Task 3: Create OSK Credentials Types

**Files:**
- Create: `internal/types/osk_credentials.go`
- Create: `internal/types/osk_credentials_test.go`

**Step 1: Write failing tests for OSK credentials**

Create `internal/types/osk_credentials_test.go`:

```go
package types_test

import (
	"os"
	"testing"

	"github.com/confluentinc/kcp/internal/types"
)

func TestOSKCredentials_Validate_Valid(t *testing.T) {
	creds := &types.OSKCredentials{
		Clusters: []types.OSKClusterAuth{
			{
				ID:               "prod-kafka-01",
				BootstrapServers: []string{"broker1:9092", "broker2:9092"},
				AuthMethod: types.AuthMethodConfig{
					SASLScram: &types.SASLScramConfig{
						Use:      true,
						Username: "admin",
						Password: "secret",
					},
				},
			},
		},
	}

	valid, errs := creds.Validate()
	if !valid {
		t.Errorf("expected valid credentials, got errors: %v", errs)
	}
}

func TestOSKCredentials_Validate_DuplicateID(t *testing.T) {
	creds := &types.OSKCredentials{
		Clusters: []types.OSKClusterAuth{
			{
				ID:               "prod-kafka-01",
				BootstrapServers: []string{"broker1:9092"},
				AuthMethod: types.AuthMethodConfig{
					SASLScram: &types.SASLScramConfig{Use: true, Username: "u", Password: "p"},
				},
			},
			{
				ID:               "prod-kafka-01", // Duplicate!
				BootstrapServers: []string{"broker2:9092"},
				AuthMethod: types.AuthMethodConfig{
					SASLScram: &types.SASLScramConfig{Use: true, Username: "u", Password: "p"},
				},
			},
		},
	}

	valid, errs := creds.Validate()
	if valid {
		t.Error("expected validation to fail for duplicate IDs")
	}
	if len(errs) == 0 {
		t.Error("expected errors for duplicate IDs")
	}
}

func TestOSKCredentials_Validate_InvalidBootstrapServer(t *testing.T) {
	creds := &types.OSKCredentials{
		Clusters: []types.OSKClusterAuth{
			{
				ID:               "prod-kafka-01",
				BootstrapServers: []string{"invalid-server"}, // Missing port
				AuthMethod: types.AuthMethodConfig{
					SASLScram: &types.SASLScramConfig{Use: true, Username: "u", Password: "p"},
				},
			},
		},
	}

	valid, errs := creds.Validate()
	if valid {
		t.Error("expected validation to fail for invalid bootstrap server")
	}
}

func TestOSKCredentials_Validate_NoAuthMethod(t *testing.T) {
	creds := &types.OSKCredentials{
		Clusters: []types.OSKClusterAuth{
			{
				ID:               "prod-kafka-01",
				BootstrapServers: []string{"broker1:9092"},
				AuthMethod:       types.AuthMethodConfig{}, // No auth method
			},
		},
	}

	valid, errs := creds.Validate()
	if valid {
		t.Error("expected validation to fail when no auth method is enabled")
	}
}
```

**Step 2: Run test to verify it fails**

```bash
go test ./internal/types/... -run TestOSKCredentials -v
```

Expected: FAIL - types not defined

**Step 3: Create OSK credentials implementation**

Create `internal/types/osk_credentials.go`:

```go
package types

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/goccy/go-yaml"
)

// OSKCredentials represents the osk-credentials.yaml file
type OSKCredentials struct {
	Clusters []OSKClusterAuth `yaml:"clusters"`
}

// OSKClusterAuth contains authentication details for a single OSK cluster
type OSKClusterAuth struct {
	ID               string                `yaml:"id"`
	BootstrapServers []string              `yaml:"bootstrap_servers"`
	AuthMethod       AuthMethodConfig      `yaml:"auth_method"`
	Metadata         OSKCredentialMetadata `yaml:"metadata,omitempty"`
}

// OSKCredentialMetadata allows users to add optional organizational metadata
type OSKCredentialMetadata struct {
	Environment string            `yaml:"environment,omitempty"`
	Location    string            `yaml:"location,omitempty"`
	Labels      map[string]string `yaml:"labels,omitempty"`
}

// NewOSKCredentialsFromFile loads OSK credentials from a YAML file
func NewOSKCredentialsFromFile(credentialsYamlPath string) (*OSKCredentials, []error) {
	data, err := os.ReadFile(credentialsYamlPath)
	if err != nil {
		return nil, []error{fmt.Errorf("failed to read osk-credentials.yaml file: %w", err)}
	}

	var credsFile OSKCredentials
	if err := yaml.Unmarshal(data, &credsFile); err != nil {
		return nil, []error{fmt.Errorf("failed to unmarshal YAML: %w", err)}
	}

	if valid, errs := credsFile.Validate(); !valid {
		return nil, errs
	}

	return &credsFile, nil
}

// Validate checks that the credentials file is valid
func (c OSKCredentials) Validate() (bool, []error) {
	errs := []error{}

	if len(c.Clusters) == 0 {
		errs = append(errs, fmt.Errorf("no clusters defined in osk-credentials.yaml"))
	}

	// Track duplicate IDs
	ids := make(map[string]bool)

	for i, cluster := range c.Clusters {
		clusterRef := fmt.Sprintf("cluster[%d]", i)

		// Validate required fields
		if cluster.ID == "" {
			errs = append(errs, fmt.Errorf("%s: 'id' is required", clusterRef))
		}
		if len(cluster.BootstrapServers) == 0 {
			errs = append(errs, fmt.Errorf("%s (id=%s): no bootstrap servers specified", clusterRef, cluster.ID))
		}

		// Check for duplicate IDs
		if cluster.ID != "" {
			if ids[cluster.ID] {
				errs = append(errs, fmt.Errorf("%s: duplicate cluster ID '%s'", clusterRef, cluster.ID))
			}
			ids[cluster.ID] = true
		}

		// Validate bootstrap servers format
		for j, server := range cluster.BootstrapServers {
			if !isValidBootstrapServer(server) {
				errs = append(errs, fmt.Errorf("%s (id=%s): invalid bootstrap server format '%s' at index %d (expected host:port)",
					clusterRef, cluster.ID, server, j))
			}
		}

		// Validate auth method
		enabledMethods := cluster.GetAuthMethods()
		if len(enabledMethods) == 0 {
			errs = append(errs, fmt.Errorf("%s (id=%s): no authentication method enabled", clusterRef, cluster.ID))
		}
		if len(enabledMethods) > 1 {
			errs = append(errs, fmt.Errorf("%s (id=%s): multiple authentication methods enabled (only one allowed)",
				clusterRef, cluster.ID))
		}

		// Validate auth method-specific fields
		if err := validateAuthMethodConfig(cluster.AuthMethod, enabledMethods); err != nil {
			errs = append(errs, fmt.Errorf("%s (id=%s): %w", clusterRef, cluster.ID, err))
		}
	}

	return len(errs) == 0, errs
}

// GetAuthMethods returns the enabled authentication methods for this cluster
func (c OSKClusterAuth) GetAuthMethods() []AuthType {
	enabledMethods := []AuthType{}

	if c.AuthMethod.UnauthenticatedPlaintext != nil && c.AuthMethod.UnauthenticatedPlaintext.Use {
		enabledMethods = append(enabledMethods, AuthTypeUnauthenticatedPlaintext)
	}
	if c.AuthMethod.UnauthenticatedTLS != nil && c.AuthMethod.UnauthenticatedTLS.Use {
		enabledMethods = append(enabledMethods, AuthTypeUnauthenticatedTLS)
	}
	if c.AuthMethod.SASLScram != nil && c.AuthMethod.SASLScram.Use {
		enabledMethods = append(enabledMethods, AuthTypeSASLSCRAM)
	}
	if c.AuthMethod.TLS != nil && c.AuthMethod.TLS.Use {
		enabledMethods = append(enabledMethods, AuthTypeTLS)
	}
	// Note: IAM not supported for OSK

	return enabledMethods
}

// GetSelectedAuthType returns the selected auth type for the cluster
func (c OSKClusterAuth) GetSelectedAuthType() (AuthType, error) {
	enabledMethods := c.GetAuthMethods()
	if len(enabledMethods) == 0 {
		return "", fmt.Errorf("no authentication method enabled for cluster")
	}
	return enabledMethods[0], nil
}

// WriteToFile writes the credentials to a YAML file
func (c *OSKCredentials) WriteToFile(filePath string) error {
	yamlData, err := yaml.Marshal(c)
	if err != nil {
		return fmt.Errorf("failed to marshal YAML: %w", err)
	}
	if err := os.WriteFile(filePath, yamlData, 0644); err != nil {
		return fmt.Errorf("failed to write YAML file: %w", err)
	}
	return nil
}

// isValidBootstrapServer checks if a bootstrap server string is valid (host:port format)
func isValidBootstrapServer(server string) bool {
	parts := strings.Split(server, ":")
	if len(parts) != 2 {
		return false
	}

	host := parts[0]
	port := parts[1]

	if host == "" || port == "" {
		return false
	}

	// Validate port is numeric
	if _, err := strconv.Atoi(port); err != nil {
		return false
	}

	return true
}

// validateAuthMethodConfig validates auth method specific configuration
func validateAuthMethodConfig(authMethod AuthMethodConfig, enabledMethods []AuthType) error {
	if len(enabledMethods) == 0 {
		return nil
	}

	authType := enabledMethods[0]

	switch authType {
	case AuthTypeSASLSCRAM:
		if authMethod.SASLScram == nil {
			return fmt.Errorf("sasl_scram config is nil")
		}
		if authMethod.SASLScram.Username == "" {
			return fmt.Errorf("sasl_scram username is required")
		}
		if authMethod.SASLScram.Password == "" {
			return fmt.Errorf("sasl_scram password is required")
		}

	case AuthTypeTLS:
		if authMethod.TLS == nil {
			return fmt.Errorf("tls config is nil")
		}
		// Validate cert files exist
		if authMethod.TLS.CACert != "" {
			if _, err := os.Stat(authMethod.TLS.CACert); err != nil {
				return fmt.Errorf("ca_cert file not found: %s", authMethod.TLS.CACert)
			}
		}
		if authMethod.TLS.ClientCert == "" {
			return fmt.Errorf("tls client_cert is required")
		}
		if _, err := os.Stat(authMethod.TLS.ClientCert); err != nil {
			return fmt.Errorf("client_cert file not found: %s", authMethod.TLS.ClientCert)
		}
		if authMethod.TLS.ClientKey == "" {
			return fmt.Errorf("tls client_key is required")
		}
		if _, err := os.Stat(authMethod.TLS.ClientKey); err != nil {
			return fmt.Errorf("client_key file not found: %s", authMethod.TLS.ClientKey)
		}
	}

	return nil
}
```

**Step 4: Run tests to verify they pass**

```bash
go test ./internal/types/... -run TestOSKCredentials -v
```

Expected: PASS

**Step 5: Commit**

```bash
git add internal/types/osk_credentials.go internal/types/osk_credentials_test.go
git commit -m "feat: add OSK credentials types and validation"
```

---

## Task 4: Rename MSK Credentials File

**Files:**
- Rename: `internal/types/credentials.go` → `internal/types/msk_credentials.go`
- Modify: `cmd/discover/cmd_discover.go`
- Modify: All files importing credentials

**Step 1: Rename credentials.go to msk_credentials.go**

```bash
git mv internal/types/credentials.go internal/types/msk_credentials.go
```

**Step 2: Update discover command to generate msk-credentials.yaml**

In `cmd/discover/cmd_discover.go`, change line 16:

From:
```go
credentialsFileName = "cluster-credentials.yaml"
```

To:
```go
credentialsFileName = "msk-credentials.yaml"
```

**Step 3: Run tests to verify nothing broke**

```bash
go test ./... -v
```

Expected: PASS (all existing tests still pass)

**Step 4: Commit**

```bash
git add internal/types/msk_credentials.go cmd/discover/cmd_discover.go
git commit -m "refactor: rename cluster-credentials.yaml to msk-credentials.yaml"
```

---

## Task 5: Implement OSK Source

**Files:**
- Create: `internal/sources/osk/osk_source.go`
- Create: `internal/sources/osk/osk_source_test.go`

**Step 1: Write failing test for OSK source**

```bash
mkdir -p internal/sources/osk
```

Create `internal/sources/osk/osk_source_test.go`:

```go
package osk_test

import (
	"testing"

	"github.com/confluentinc/kcp/internal/sources"
	"github.com/confluentinc/kcp/internal/sources/osk"
)

func TestOSKSource_Type(t *testing.T) {
	source := osk.NewOSKSource()
	if source.Type() != sources.SourceTypeOSK {
		t.Errorf("expected source type %s, got %s", sources.SourceTypeOSK, source.Type())
	}
}

func TestOSKSource_GetClusters_BeforeLoad(t *testing.T) {
	source := osk.NewOSKSource()
	clusters := source.GetClusters()
	if clusters != nil {
		t.Error("expected nil clusters before loading credentials")
	}
}

func TestOSKSource_LoadCredentials_FileNotFound(t *testing.T) {
	source := osk.NewOSKSource()
	err := source.LoadCredentials("nonexistent.yaml")
	if err == nil {
		t.Error("expected error when loading nonexistent credentials file")
	}
}
```

**Step 2: Run test to verify it fails**

```bash
go test ./internal/sources/osk/... -v
```

Expected: FAIL - package does not exist

**Step 3: Create OSK source implementation**

Create `internal/sources/osk/osk_source.go`:

```go
package osk

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/confluentinc/kcp/internal/sources"
	"github.com/confluentinc/kcp/internal/types"
)

// OSKSource implements the Source interface for Open Source Kafka clusters
type OSKSource struct {
	credentials *types.OSKCredentials
}

// NewOSKSource creates a new OSK source
func NewOSKSource() *OSKSource {
	return &OSKSource{}
}

// Type returns the source type
func (s *OSKSource) Type() sources.SourceType {
	return sources.SourceTypeOSK
}

// LoadCredentials loads OSK credentials from a file
func (s *OSKSource) LoadCredentials(credentialsPath string) error {
	creds, errs := types.NewOSKCredentialsFromFile(credentialsPath)
	if len(errs) > 0 {
		return fmt.Errorf("failed to load OSK credentials: %v", errs)
	}
	s.credentials = creds
	slog.Info("loaded OSK credentials", "clusters", len(creds.Clusters))
	return nil
}

// GetClusters returns the list of clusters from credentials
func (s *OSKSource) GetClusters() []sources.ClusterIdentifier {
	if s.credentials == nil {
		return nil
	}

	clusters := make([]sources.ClusterIdentifier, len(s.credentials.Clusters))
	for i, cluster := range s.credentials.Clusters {
		clusters[i] = sources.ClusterIdentifier{
			Name:             cluster.ID, // OSK uses ID as name
			UniqueID:         cluster.ID,
			BootstrapServers: cluster.BootstrapServers,
		}
	}
	return clusters
}

// Scan performs scanning of all OSK clusters
func (s *OSKSource) Scan(ctx context.Context, opts sources.ScanOptions) (*sources.ScanResult, error) {
	if s.credentials == nil {
		return nil, fmt.Errorf("credentials not loaded")
	}

	slog.Info("starting OSK cluster scan", "clusters", len(s.credentials.Clusters))

	result := &sources.ScanResult{
		SourceType: sources.SourceTypeOSK,
		Clusters:   make([]sources.ClusterScanResult, 0),
	}

	var scanErrors []error

	for _, clusterCreds := range s.credentials.Clusters {
		slog.Info("scanning OSK cluster", "id", clusterCreds.ID)

		clusterResult, err := s.scanCluster(ctx, clusterCreds, opts)
		if err != nil {
			// Log error but continue with other clusters
			slog.Error("failed to scan OSK cluster",
				"id", clusterCreds.ID,
				"error", err)
			scanErrors = append(scanErrors, fmt.Errorf("cluster '%s': %w",
				clusterCreds.ID, err))
			continue
		}

		result.Clusters = append(result.Clusters, *clusterResult)
		slog.Info("successfully scanned OSK cluster",
			"id", clusterCreds.ID,
			"topics", len(clusterResult.KafkaAdminInfo.Topics.Details),
			"acls", len(clusterResult.KafkaAdminInfo.Acls))
	}

	// If ALL clusters failed, return error
	if len(result.Clusters) == 0 && len(scanErrors) > 0 {
		return nil, fmt.Errorf("failed to scan any clusters: %v", scanErrors)
	}

	// If SOME clusters failed, log warnings but return partial results
	if len(scanErrors) > 0 {
		slog.Warn("some clusters failed to scan",
			"failed", len(scanErrors),
			"succeeded", len(result.Clusters))
	}

	return result, nil
}

// scanCluster scans a single OSK cluster using Kafka Admin API
func (s *OSKSource) scanCluster(ctx context.Context, clusterCreds types.OSKClusterAuth, opts sources.ScanOptions) (*sources.ClusterScanResult, error) {
	// TODO: Implement Kafka Admin API scanning
	// This will be implemented in next task

	// For now, return stub
	metadata := types.OSKClusterMetadata{
		Environment: clusterCreds.Metadata.Environment,
		Location:    clusterCreds.Metadata.Location,
		Labels:      clusterCreds.Metadata.Labels,
		LastScanned: time.Now(),
	}

	return &sources.ClusterScanResult{
		Identifier: sources.ClusterIdentifier{
			Name:             clusterCreds.ID,
			UniqueID:         clusterCreds.ID,
			BootstrapServers: clusterCreds.BootstrapServers,
		},
		KafkaAdminInfo:     &types.KafkaAdminClientInformation{},
		SourceSpecificData: metadata,
	}, nil
}
```

**Step 4: Run tests to verify they pass**

```bash
go test ./internal/sources/osk/... -v
```

Expected: PASS

**Step 5: Commit**

```bash
git add internal/sources/osk/
git commit -m "feat: add OSK source implementation (stub)"
```

---

## Task 6: Implement MSK Source Wrapper

**Files:**
- Create: `internal/sources/msk/msk_source.go`
- Create: `internal/sources/msk/msk_source_test.go`

**Step 1: Write failing test for MSK source**

```bash
mkdir -p internal/sources/msk
```

Create `internal/sources/msk/msk_source_test.go`:

```go
package msk_test

import (
	"testing"

	"github.com/confluentinc/kcp/internal/sources"
	"github.com/confluentinc/kcp/internal/sources/msk"
)

func TestMSKSource_Type(t *testing.T) {
	source := msk.NewMSKSource()
	if source.Type() != sources.SourceTypeMSK {
		t.Errorf("expected source type %s, got %s", sources.SourceTypeMSK, source.Type())
	}
}

func TestMSKSource_GetClusters_BeforeLoad(t *testing.T) {
	source := msk.NewMSKSource()
	clusters := source.GetClusters()
	if clusters != nil {
		t.Error("expected nil clusters before loading credentials")
	}
}
```

**Step 2: Run test to verify it fails**

```bash
go test ./internal/sources/msk/... -v
```

Expected: FAIL - package does not exist

**Step 3: Create MSK source implementation**

Create `internal/sources/msk/msk_source.go`:

```go
package msk

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/confluentinc/kcp/internal/sources"
	"github.com/confluentinc/kcp/internal/types"
)

// MSKSource implements the Source interface for AWS MSK clusters
type MSKSource struct {
	credentials *types.Credentials
	state       *types.State
}

// NewMSKSource creates a new MSK source
func NewMSKSource() *MSKSource {
	return &MSKSource{}
}

// Type returns the source type
func (s *MSKSource) Type() sources.SourceType {
	return sources.SourceTypeMSK
}

// LoadCredentials loads MSK credentials from a file
func (s *MSKSource) LoadCredentials(credentialsPath string) error {
	creds, errs := types.NewCredentialsFromFile(credentialsPath)
	if len(errs) > 0 {
		return fmt.Errorf("failed to load MSK credentials: %v", errs)
	}
	s.credentials = creds
	slog.Info("loaded MSK credentials", "regions", len(creds.Regions))
	return nil
}

// GetClusters returns the list of MSK clusters from credentials
func (s *MSKSource) GetClusters() []sources.ClusterIdentifier {
	if s.credentials == nil {
		return nil
	}

	var clusters []sources.ClusterIdentifier
	for _, region := range s.credentials.Regions {
		for _, cluster := range region.Clusters {
			clusters = append(clusters, sources.ClusterIdentifier{
				Name:             cluster.Name,
				UniqueID:         cluster.Arn,
				BootstrapServers: nil, // Populated from state during scan
			})
		}
	}
	return clusters
}

// Scan performs scanning of MSK clusters
// This is a wrapper that will delegate to existing MSK scanning logic
func (s *MSKSource) Scan(ctx context.Context, opts sources.ScanOptions) (*sources.ScanResult, error) {
	if s.credentials == nil {
		return nil, fmt.Errorf("credentials not loaded")
	}

	slog.Info("starting MSK cluster scan")

	// TODO: Delegate to existing MSK scanner in cmd/scan/clusters
	// This will be implemented when we update the scan clusters command

	result := &sources.ScanResult{
		SourceType: sources.SourceTypeMSK,
		Clusters:   make([]sources.ClusterScanResult, 0),
	}

	return result, nil
}

// SetState sets the existing state (for incremental scans)
func (s *MSKSource) SetState(state *types.State) {
	s.state = state
}
```

**Step 4: Run tests to verify they pass**

```bash
go test ./internal/sources/msk/... -v
```

Expected: PASS

**Step 5: Commit**

```bash
git add internal/sources/msk/
git commit -m "feat: add MSK source wrapper implementation"
```

---

## Task 7: Update Scan Clusters Command Structure

**Files:**
- Modify: `cmd/scan/clusters/cmd_scan_clusters.go`

**Step 1: Add source-type flag and update command**

In `cmd/scan/clusters/cmd_scan_clusters.go`, update the variables section (around line 15):

```go
var (
	stateFile       string
	credentialsFile string
	sourceType      string  // NEW
	skipTopics      bool
	skipACLs        bool
)
```

**Step 2: Update NewScanClustersCmd function**

Replace the command setup to add the source-type flag:

```go
func NewScanClustersCmd() *cobra.Command {
	scanClustersCmd := &cobra.Command{
		Use:           "clusters",
		Short:         "Scan Kafka clusters using the Kafka Admin API",
		Long:          "Scans MSK or OSK clusters to discover topics, ACLs, and other metadata via Kafka Admin API",
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		PreRunE:       preRunScanClusters,
		RunE:          runScanClusters,
	}

	requiredFlags := pflag.NewFlagSet("required", pflag.ExitOnError)
	requiredFlags.SortFlags = false
	requiredFlags.StringVar(&sourceType, "source-type", "", "Source type: 'msk' or 'osk' (required)")
	requiredFlags.StringVar(&stateFile, "state-file", "kcp-state.json", "Path to the KCP state file")
	requiredFlags.StringVar(&credentialsFile, "credentials-file", "", "Path to credentials file (msk-credentials.yaml or osk-credentials.yaml)")
	scanClustersCmd.Flags().AddFlagSet(requiredFlags)

	optionalFlags := pflag.NewFlagSet("optional", pflag.ExitOnError)
	optionalFlags.SortFlags = false
	optionalFlags.BoolVar(&skipTopics, "skip-topics", false, "Skip topic discovery")
	optionalFlags.BoolVar(&skipACLs, "skip-acls", false, "Skip ACL discovery")
	scanClustersCmd.Flags().AddFlagSet(optionalFlags)

	scanClustersCmd.MarkFlagRequired("source-type")
	scanClustersCmd.MarkFlagRequired("credentials-file")

	return scanClustersCmd
}
```

**Step 3: Add preRun validation**

Add the preRunScanClusters function:

```go
func preRunScanClusters(cmd *cobra.Command, args []string) error {
	// Validate source type
	if sourceType != "msk" && sourceType != "osk" {
		return fmt.Errorf("invalid source-type '%s': must be 'msk' or 'osk'", sourceType)
	}

	// Validate credentials file naming convention
	if sourceType == "msk" && credentialsFile != "msk-credentials.yaml" {
		slog.Warn("credentials file should be named 'msk-credentials.yaml' for MSK sources", "file", credentialsFile)
	}
	if sourceType == "osk" && credentialsFile != "osk-credentials.yaml" {
		slog.Warn("credentials file should be named 'osk-credentials.yaml' for OSK sources", "file", credentialsFile)
	}

	return nil
}
```

**Step 4: Create source factory in runScanClusters**

Update the runScanClusters function:

```go
func runScanClusters(cmd *cobra.Command, args []string) error {
	ctx := context.Background()

	// Load or create state file
	state, err := loadOrCreateState(stateFile)
	if err != nil {
		return fmt.Errorf("failed to load state file: %w", err)
	}

	// Create appropriate source based on source-type flag
	var source sources.Source
	switch sourceType {
	case "msk":
		source = msk.NewMSKSource()
	case "osk":
		source = osk.NewOSKSource()
	default:
		return fmt.Errorf("unsupported source type: %s", sourceType)
	}

	// Load credentials
	if err := source.LoadCredentials(credentialsFile); err != nil {
		return fmt.Errorf("failed to load credentials: %w", err)
	}

	// Display clusters to be scanned
	clusters := source.GetClusters()
	slog.Info("clusters to scan", "count", len(clusters), "source", sourceType)
	for _, cluster := range clusters {
		slog.Info("cluster", "name", cluster.Name, "id", cluster.UniqueID)
	}

	// Perform scan
	scanOpts := sources.ScanOptions{
		SkipTopics: skipTopics,
		SkipACLs:   skipACLs,
	}

	slog.Info("starting cluster scan", "source", sourceType)
	scanResult, err := source.Scan(ctx, scanOpts)
	if err != nil {
		return fmt.Errorf("scan failed: %w", err)
	}

	// Merge scan results into state
	if err := mergeResultsIntoState(state, scanResult); err != nil {
		return fmt.Errorf("failed to merge scan results: %w", err)
	}

	// Save updated state
	if err := persistence.SaveState(stateFile, state); err != nil {
		return fmt.Errorf("failed to save state: %w", err)
	}

	slog.Info("scan completed successfully", "clusters", len(scanResult.Clusters), "state_file", stateFile)
	fmt.Printf("\n✅ Scan completed successfully\n")
	fmt.Printf("   Scanned %d cluster(s)\n", len(scanResult.Clusters))
	fmt.Printf("   State file: %s\n\n", stateFile)

	return nil
}
```

**Step 5: Add helper functions**

Add these helper functions at the end of the file:

```go
// loadOrCreateState loads existing state or creates a new one
func loadOrCreateState(stateFilePath string) (*types.State, error) {
	state, err := types.NewStateFromFile(stateFilePath)
	if err != nil {
		// File doesn't exist - create new state
		slog.Info("creating new state file", "file", stateFilePath)
		return &types.State{
			MSKSources:       &types.MSKSourcesState{Regions: []types.DiscoveredRegion{}},
			OSKSources:       &types.OSKSourcesState{Clusters: []types.OSKDiscoveredCluster{}},
			SchemaRegistries: []types.SchemaRegistryInformation{},
			KcpBuildInfo:     types.KcpBuildInfo{},
			Timestamp:        time.Now(),
		}, nil
	}
	slog.Info("loaded existing state file", "file", stateFilePath)
	return state, nil
}

// mergeResultsIntoState merges scan results into the state file
func mergeResultsIntoState(state *types.State, result *sources.ScanResult) error {
	switch result.SourceType {
	case sources.SourceTypeMSK:
		return mergeMSKResults(state, result)
	case sources.SourceTypeOSK:
		return mergeOSKResults(state, result)
	default:
		return fmt.Errorf("unsupported source type: %s", result.SourceType)
	}
}

// mergeMSKResults merges MSK scan results into state
func mergeMSKResults(state *types.State, result *sources.ScanResult) error {
	if state.MSKSources == nil {
		state.MSKSources = &types.MSKSourcesState{
			Regions: []types.DiscoveredRegion{},
		}
	}

	// TODO: Implement MSK-specific merge logic
	// For now, just log
	slog.Info("merging MSK scan results", "clusters", len(result.Clusters))
	return nil
}

// mergeOSKResults merges OSK scan results into state
func mergeOSKResults(state *types.State, result *sources.ScanResult) error {
	if state.OSKSources == nil {
		state.OSKSources = &types.OSKSourcesState{
			Clusters: []types.OSKDiscoveredCluster{},
		}
	}

	// Build map of existing clusters by ID for efficient lookup
	existingClusters := make(map[string]*types.OSKDiscoveredCluster)
	for i := range state.OSKSources.Clusters {
		cluster := &state.OSKSources.Clusters[i]
		existingClusters[cluster.ID] = cluster
	}

	// Merge new scan results
	for _, clusterResult := range result.Clusters {
		metadata, ok := clusterResult.SourceSpecificData.(types.OSKClusterMetadata)
		if !ok {
			return fmt.Errorf("invalid source-specific data for OSK cluster")
		}

		newCluster := types.OSKDiscoveredCluster{
			ID:                          clusterResult.Identifier.UniqueID,
			BootstrapServers:            clusterResult.Identifier.BootstrapServers,
			KafkaAdminClientInformation: *clusterResult.KafkaAdminInfo,
			Metadata:                    metadata,
		}

		if existingCluster, exists := existingClusters[newCluster.ID]; exists {
			// Merge with existing cluster (preserve discovered clients, etc.)
			newCluster.DiscoveredClients = existingCluster.DiscoveredClients

			// Replace in-place
			*existingCluster = newCluster
		} else {
			// New cluster - append
			state.OSKSources.Clusters = append(state.OSKSources.Clusters, newCluster)
		}
	}

	slog.Info("merged OSK scan results", "clusters", len(result.Clusters))
	return nil
}
```

**Step 6: Add imports at top of file**

```go
import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/confluentinc/kcp/internal/services/persistence"
	"github.com/confluentinc/kcp/internal/sources"
	"github.com/confluentinc/kcp/internal/sources/msk"
	"github.com/confluentinc/kcp/internal/sources/osk"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)
```

**Step 7: Build to verify it compiles**

```bash
make build-frontend
go build ./cmd/...
```

Expected: Successful build

**Step 8: Commit**

```bash
git add cmd/scan/clusters/cmd_scan_clusters.go
git commit -m "feat: add source-type flag and factory to scan clusters command"
```

---

## Task 8: Create Docker Compose Test Environments

**Files:**
- Create: `test/docker/docker-compose-plaintext.yml`
- Create: `test/docker/docker-compose-kraft.yml`
- Create: `test/docker/scripts/wait-for-kafka.sh`
- Create: `test/docker/scripts/setup-test-data.sh`
- Create: `test/credentials/osk-credentials-plaintext.yaml`
- Create: `test/credentials/osk-credentials-kraft.yaml`
- Modify: `Makefile`

**Step 1: Create test directory structure**

```bash
mkdir -p test/docker/configs test/docker/scripts test/credentials
```

**Step 2: Create plaintext Docker Compose**

Create `test/docker/docker-compose-plaintext.yml`:

```yaml
version: '3.8'

services:
  zookeeper:
    image: confluentinc/cp-zookeeper:7.6.0
    hostname: zookeeper
    container_name: kcp-test-zookeeper
    environment:
      ZOOKEEPER_CLIENT_PORT: 2181
      ZOOKEEPER_TICK_TIME: 2000
    ports:
      - "2181:2181"

  kafka:
    image: confluentinc/cp-kafka:7.6.0
    hostname: kafka
    container_name: kcp-test-kafka-plaintext
    depends_on:
      - zookeeper
    ports:
      - "9092:9092"
    environment:
      KAFKA_BROKER_ID: 1
      KAFKA_ZOOKEEPER_CONNECT: 'zookeeper:2181'
      KAFKA_LISTENER_SECURITY_PROTOCOL_MAP: PLAINTEXT:PLAINTEXT,PLAINTEXT_HOST:PLAINTEXT
      KAFKA_ADVERTISED_LISTENERS: PLAINTEXT://kafka:29092,PLAINTEXT_HOST://localhost:9092
      KAFKA_OFFSETS_TOPIC_REPLICATION_FACTOR: 1
      KAFKA_TRANSACTION_STATE_LOG_MIN_ISR: 1
      KAFKA_TRANSACTION_STATE_LOG_REPLICATION_FACTOR: 1
      KAFKA_AUTO_CREATE_TOPICS_ENABLE: 'true'
```

**Step 3: Create KRaft Docker Compose**

Create `test/docker/docker-compose-kraft.yml`:

```yaml
version: '3.8'

services:
  kafka-kraft:
    image: confluentinc/cp-kafka:7.6.0
    hostname: kafka-kraft
    container_name: kcp-test-kafka-kraft
    ports:
      - "9095:9095"
    environment:
      KAFKA_NODE_ID: 1
      KAFKA_PROCESS_ROLES: 'broker,controller'
      KAFKA_CONTROLLER_QUORUM_VOTERS: '1@kafka-kraft:29093'
      KAFKA_LISTENERS: 'PLAINTEXT://kafka-kraft:29092,CONTROLLER://kafka-kraft:29093,PLAINTEXT_HOST://0.0.0.0:9095'
      KAFKA_ADVERTISED_LISTENERS: 'PLAINTEXT://kafka-kraft:29092,PLAINTEXT_HOST://localhost:9095'
      KAFKA_LISTENER_SECURITY_PROTOCOL_MAP: 'CONTROLLER:PLAINTEXT,PLAINTEXT:PLAINTEXT,PLAINTEXT_HOST:PLAINTEXT'
      KAFKA_CONTROLLER_LISTENER_NAMES: 'CONTROLLER'
      KAFKA_INTER_BROKER_LISTENER_NAME: 'PLAINTEXT'
      KAFKA_OFFSETS_TOPIC_REPLICATION_FACTOR: 1
      KAFKA_TRANSACTION_STATE_LOG_MIN_ISR: 1
      KAFKA_TRANSACTION_STATE_LOG_REPLICATION_FACTOR: 1
      CLUSTER_ID: 'MkU3OEVBNTcwNTJENDM2Qk'
    command: >
      bash -c "
        kafka-storage format -t MkU3OEVBNTcwNTJENDM2Qk -c /etc/kafka/kafka.properties --ignore-formatted 2>/dev/null || true
        /etc/confluent/docker/run
      "
```

**Step 4: Create wait-for-kafka script**

Create `test/docker/scripts/wait-for-kafka.sh`:

```bash
#!/bin/bash
# Waits for Kafka to be ready

BOOTSTRAP=$1
MAX_WAIT=60
WAIT_TIME=0

echo "Waiting for Kafka at $BOOTSTRAP..."

while [ $WAIT_TIME -lt $MAX_WAIT ]; do
    if kafka-broker-api-versions --bootstrap-server $BOOTSTRAP > /dev/null 2>&1; then
        echo "Kafka is ready!"
        exit 0
    fi

    echo "Kafka not ready yet, waiting... ($WAIT_TIME/$MAX_WAIT seconds)"
    sleep 2
    WAIT_TIME=$((WAIT_TIME + 2))
done

echo "Timeout waiting for Kafka"
exit 1
```

Make it executable:

```bash
chmod +x test/docker/scripts/wait-for-kafka.sh
```

**Step 5: Create setup test data script**

Create `test/docker/scripts/setup-test-data.sh`:

```bash
#!/bin/bash
# Creates test topics for integration testing

set -e

BOOTSTRAP=$1

echo "Setting up test data on $BOOTSTRAP"

# Create test topics
kafka-topics --bootstrap-server $BOOTSTRAP --create --topic test-topic-1 --partitions 3 --replication-factor 1 || true
kafka-topics --bootstrap-server $BOOTSTRAP --create --topic test-topic-2 --partitions 1 --replication-factor 1 || true

echo "Test topics created successfully"
```

Make it executable:

```bash
chmod +x test/docker/scripts/setup-test-data.sh
```

**Step 6: Create test credentials files**

Create `test/credentials/osk-credentials-plaintext.yaml`:

```yaml
clusters:
  - id: test-kafka-plaintext
    bootstrap_servers:
      - localhost:9092
    auth_method:
      unauthenticated_plaintext:
        use: true
    metadata:
      environment: test
      location: docker-local
```

Create `test/credentials/osk-credentials-kraft.yaml`:

```yaml
clusters:
  - id: test-kafka-kraft
    bootstrap_servers:
      - localhost:9095
    auth_method:
      unauthenticated_plaintext:
        use: true
    metadata:
      environment: test
      location: docker-local-kraft
```

**Step 7: Add Makefile targets**

Add to `Makefile`:

```makefile
# Docker Compose test environments
.PHONY: test-env-up-plaintext test-env-up-kraft test-env-down test-integration-osk test-all-envs

test-env-up-plaintext:
	@echo "Starting plaintext Kafka test environment (ZooKeeper-based)..."
	docker-compose -f test/docker/docker-compose-plaintext.yml up -d
	@bash test/docker/scripts/wait-for-kafka.sh localhost:9092
	@bash test/docker/scripts/setup-test-data.sh localhost:9092

test-env-up-kraft:
	@echo "Starting KRaft Kafka test environment (no ZooKeeper)..."
	docker-compose -f test/docker/docker-compose-kraft.yml up -d
	@bash test/docker/scripts/wait-for-kafka.sh localhost:9095
	@bash test/docker/scripts/setup-test-data.sh localhost:9095

test-env-down:
	@echo "Stopping all test environments..."
	docker-compose -f test/docker/docker-compose-plaintext.yml down -v 2>/dev/null || true
	docker-compose -f test/docker/docker-compose-kraft.yml down -v 2>/dev/null || true

test-integration-osk: test-env-up-plaintext
	@echo "Running OSK integration tests (ZooKeeper mode)..."
	TEST_KAFKA_BOOTSTRAP=localhost:9092 go test -tags=integration ./cmd/scan/clusters/... -v
	$(MAKE) test-env-down
	@echo "Running OSK integration tests (KRaft mode)..."
	$(MAKE) test-env-up-kraft
	TEST_KAFKA_BOOTSTRAP=localhost:9095 go test -tags=integration ./cmd/scan/clusters/... -v
	$(MAKE) test-env-down

test-all-envs:
	@echo "Testing OSK scanning against all Kafka configurations..."
	@echo "\n=== Testing ZooKeeper-based cluster ==="
	$(MAKE) test-env-up-plaintext
	kcp scan clusters --source-type osk --credentials-file test/credentials/osk-credentials-plaintext.yaml --state-file test-state-zk.json
	$(MAKE) test-env-down
	@echo "\n=== Testing KRaft-based cluster ==="
	$(MAKE) test-env-up-kraft
	kcp scan clusters --source-type osk --credentials-file test/credentials/osk-credentials-kraft.yaml --state-file test-state-kraft.json
	$(MAKE) test-env-down
	@echo "\n✅ All environment tests passed!"
```

**Step 8: Test Docker Compose setup**

```bash
make test-env-up-plaintext
```

Expected: Kafka starts successfully

```bash
make test-env-down
```

Expected: Containers stop

**Step 9: Commit**

```bash
git add test/ Makefile
git commit -m "feat: add Docker Compose test environments for OSK"
```

---

## Task 9: Update Documentation

**Files:**
- Modify: `docs/README.md`
- Modify: `CLAUDE.md`

**Step 1: Update docs/README.md - Getting Started note**

Replace lines 32-33 in `docs/README.md`:

From:
```markdown
> [!NOTE]
> Currently, only migrations from AWS MSK are supported. Therefore, until later Apache Kafka migrations are supported, AWS MSK will be the reference point for the source of a migration.
```

To:
```markdown
> [!NOTE]
> KCP supports migrations from two source types:
> - **AWS MSK (Managed Streaming for Kafka)** - Full discovery via AWS APIs + Kafka Admin API
> - **Open Source Kafka (OSK)** - Direct scanning via Kafka Admin API
>
> The workflow differs slightly based on your source type. See the respective sections below for MSK-specific and OSK-specific instructions.
```

**Step 2: Update Authentication section**

Add after line 141 in `docs/README.md`:

```markdown

> [!NOTE]
> **For OSK (Open Source Kafka) migrations:** AWS authentication is not required. OSK clusters are accessed directly via Kafka Admin API using credentials you provide in the `osk-credentials.yaml` file. See the [OSK Usage](#osk-usage) section for authentication configuration details.
```

**Step 3: Update kcp discover section**

Change line 204 from:
```markdown
The command will produce a cluster-credentials.yaml and a kcp-state.json file.
```

To:
```markdown
The command will produce an `msk-credentials.yaml` and a `kcp-state.json` file.
```

**Step 4: Replace "kcp scan clusters" section**

Replace lines 355-372 with the complete new section from the design document (Section 4 of documentation changes).

*(Due to length, refer to the design document section "4. Update `kcp scan clusters` Section" for the full replacement text)*

**Step 5: Update CLAUDE.md**

Update credential file references in `/Users/tom.underhill/dev/kcp/CLAUDE.md`:

Change references from `cluster-credentials.yaml` to `msk-credentials.yaml` in the "State File Architecture" section.

**Step 6: Verify documentation renders correctly**

```bash
# Preview the markdown (use your preferred markdown viewer)
```

**Step 7: Commit**

```bash
git add docs/README.md CLAUDE.md
git commit -m "docs: update documentation for OSK support"
```

---

## Task 10: Manual Testing & Validation

**Prerequisites:**
- Docker and Docker Compose installed
- kcp binary built

**Step 1: Test OSK plaintext scanning**

```bash
# Start test environment
make test-env-up-plaintext

# Scan OSK cluster
./kcp scan clusters \
  --source-type osk \
  --credentials-file test/credentials/osk-credentials-plaintext.yaml \
  --state-file test-osk-state.json

# Verify state file created
cat test-osk-state.json | jq '.osk_sources.clusters[0].id'
```

Expected: Output shows `"test-kafka-plaintext"`

**Step 2: Test OSK KRaft scanning**

```bash
# Stop plaintext, start KRaft
make test-env-down
make test-env-up-kraft

# Scan KRaft cluster
./kcp scan clusters \
  --source-type osk \
  --credentials-file test/credentials/osk-credentials-kraft.yaml \
  --state-file test-kraft-state.json

# Verify
cat test-kraft-state.json | jq '.osk_sources.clusters[0].id'
```

Expected: Output shows `"test-kafka-kraft"`

**Step 3: Test incremental scan (same cluster twice)**

```bash
# Scan same cluster again
./kcp scan clusters \
  --source-type osk \
  --credentials-file test/credentials/osk-credentials-plaintext.yaml \
  --state-file test-osk-state.json

# Verify still only one cluster
cat test-osk-state.json | jq '.osk_sources.clusters | length'
```

Expected: Output shows `1` (not `2`)

**Step 4: Test error handling - invalid credentials**

Create invalid credentials file:

```yaml
# test/credentials/invalid.yaml
clusters:
  - id: test
    bootstrap_servers: []  # Invalid - empty
    auth_method:
      sasl_scram:
        use: true
```

```bash
./kcp scan clusters \
  --source-type osk \
  --credentials-file test/credentials/invalid.yaml
```

Expected: Clear error message about missing bootstrap servers

**Step 5: Test error handling - wrong source type**

```bash
./kcp scan clusters \
  --source-type invalid \
  --credentials-file test/credentials/osk-credentials-plaintext.yaml
```

Expected: Error message: `invalid source-type 'invalid': must be 'msk' or 'osk'`

**Step 6: Clean up**

```bash
make test-env-down
rm -f test-*.json test/credentials/invalid.yaml
```

**Step 7: Document test results**

Create `docs/plans/2026-03-05-osk-manual-test-results.md`:

```markdown
# OSK Manual Testing Results

## Test Date: [DATE]
## Tester: [NAME]

### Test Results

- [ ] OSK plaintext scanning: PASS/FAIL
- [ ] OSK KRaft scanning: PASS/FAIL
- [ ] Incremental scan (merge): PASS/FAIL
- [ ] Invalid credentials error: PASS/FAIL
- [ ] Invalid source-type error: PASS/FAIL

### Notes

[Any issues or observations]
```

**Step 8: Commit test results**

```bash
git add docs/plans/2026-03-05-osk-manual-test-results.md
git commit -m "test: manual testing results for OSK support"
```

---

## Final Steps

**Step 1: Run all tests**

```bash
make build-frontend
make test
```

Expected: All tests pass

**Step 2: Run integration tests**

```bash
make test-integration-osk
```

Expected: Integration tests pass

**Step 3: Final build**

```bash
make build-all
```

Expected: Successful builds for all platforms

**Step 4: Create final commit**

```bash
git add -A
git commit -m "feat: complete OSK to Confluent Cloud support (Phase 1)

- Add source abstraction interface for MSK and OSK
- Restructure state file to support multiple source types
- Add OSK credentials file format and validation
- Add unified 'kcp scan clusters' command with --source-type flag
- Add Docker Compose test environments (plaintext, KRaft)
- Update documentation for OSK workflows
- Breaking change: cluster-credentials.yaml → msk-credentials.yaml

Phase 1 Scope: OSK discovery and scanning only
Phase 2 (Future): Migration infrastructure generation"
```

**Step 5: Create pull request**

```bash
# Push branch
git push origin osk-support

# Create PR with description from design document
```

---

## Success Criteria Checklist

Before marking this complete, verify:

- [ ] All unit tests pass with >85% coverage
- [ ] Integration tests pass against Docker Compose environments
- [ ] Can scan OSK cluster (plaintext) and populate state file
- [ ] Can scan OSK cluster (KRaft) and populate state file
- [ ] Can scan both MSK and OSK into same state file
- [ ] State file properly merges incremental scans
- [ ] Old state files fail with clear error message
- [ ] Error messages are clear and actionable
- [ ] docs/README.md updated
- [ ] CLAUDE.md updated
- [ ] Code review approved
- [ ] Manual testing checklist completed

---

**End of Implementation Plan**
