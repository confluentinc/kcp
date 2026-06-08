package types

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/confluentinc/kcp/internal/build_info"
)

// ----- migration infrastructure type -----

type MigrationType int

const (
	PublicMskEndpoints                   MigrationType = 1
	ExternalOutboundClusterLink          MigrationType = 2
	ExternalOutboundClusterLinkPlaintext MigrationType = 3
	JumpClusterSaslScram                 MigrationType = 4
	JumpClusterIam                       MigrationType = 5
)

func (m MigrationType) IsValid() bool {
	switch m {
	case PublicMskEndpoints, ExternalOutboundClusterLink, ExternalOutboundClusterLinkPlaintext, JumpClusterSaslScram, JumpClusterIam:
		return true
	default:
		return false
	}
}

func (m MigrationType) RequiresSaslScram() bool {
	switch m {
	case PublicMskEndpoints, ExternalOutboundClusterLink, JumpClusterSaslScram:
		return true
	default:
		return false
	}
}

func ToMigrationType(input string) (MigrationType, error) {
	value, err := strconv.Atoi(input)
	if err != nil {
		return 0, fmt.Errorf("invalid input: must be a number")
	}
	m := MigrationType(value)
	if !m.IsValid() {
		return 0, fmt.Errorf("invalid MigrationType value: %d", value)
	}
	return m, nil
}

type Manifest struct {
	MigrationInfraType MigrationType `json:"migration_infra_type"`
}

// ----- migration FSM state and events -----

// FSM State constants
const (
	StateUninitialized = "uninitialized"
	StateInitialized   = "initialized"
	StateLagsOk        = "lags_ok"
	StateFenced        = "fenced"
	StatePromoted      = "promoted"
	StateSwitched      = "switched"
)

// FSM Event constants
const (
	EventInitialize  = "initialize"
	EventWaitForLags = "wait_for_lags"
	EventFence       = "fence"
	EventPromote     = "promote"
	EventSwitch      = "switch"
)

// ----- migration configuration -----

// MigrationConfig holds all domain configuration for a migration
// This is pure data with no behavior - just fields that get serialized
type MigrationConfig struct {
	MigrationId  string `json:"migration_id"`
	CurrentState string `json:"current_state"`

	// Gateway configuration
	KubeConfigPath string `json:"kube_config_path"`

	// Source cluster configuration
	SourceBootstrap string `json:"source_bootstrap"`

	// Destination cluster configuration
	ClusterBootstrap    string   `json:"cluster_bootstrap"`
	ClusterId           string   `json:"cluster_id"`
	ClusterRestEndpoint string   `json:"cluster_rest_endpoint"`
	ClusterLinkName     string   `json:"cluster_link_name"`
	Topics              []string `json:"topics"`

	// Migration runtime data (populated during initialization)
	ClusterLinkTopics  []string          `json:"cluster_link_topics"`
	ClusterLinkConfigs map[string]string `json:"cluster_link_configs"`

	// Operator intent: pause cluster-link consumer offset sync for the duration of execute.
	// PauseConsumerOffsetSync records the operator's choice at init time.
	// PauseConsumerOffsetSyncFlipped is set when kcp has executed the disable AlterConfigs and
	// not yet restored — supports drift detection, idempotent resume, and remediation messaging.
	PauseConsumerOffsetSync        bool `json:"pause_consumer_offset_sync"`
	PauseConsumerOffsetSyncFlipped bool `json:"pause_consumer_offset_sync_flipped"`

	// Gateway CR configuration
	InitialCrName    string `json:"initial_cr_name"`
	K8sNamespace     string `json:"k8s_namespace"`
	InitialCrYAML    []byte `json:"initial_cr_yaml"`
	FencedCrYAML     []byte `json:"fenced_cr_yaml"`
	SwitchoverCrYAML []byte `json:"switchover_cr_yaml"`
}

// ----- migration state file -----

// MigrationState represents the migration state file structure
// This is a dedicated state file for migration commands (init, execute, list)
type MigrationState struct {
	Migrations   []MigrationConfig `json:"migrations"`
	KcpBuildInfo KcpBuildInfo      `json:"kcp_build_info"`
	Timestamp    time.Time         `json:"timestamp"`
}

// NewMigrationState creates a new empty MigrationState with metadata
func NewMigrationState() *MigrationState {
	return &MigrationState{
		Migrations: []MigrationConfig{},
		KcpBuildInfo: KcpBuildInfo{
			Version: build_info.Version,
			Commit:  build_info.Commit,
			Date:    build_info.Date,
		},
		Timestamp: time.Now(),
	}
}

// NewMigrationStateFromFile loads a MigrationState from a JSON file
func NewMigrationStateFromFile(filePath string) (*MigrationState, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read migration state file: %w", err)
	}

	var state MigrationState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("failed to unmarshal migration state: %w", err)
	}

	return &state, nil
}

// WriteToFile saves the MigrationState to a JSON file using atomic write
func (ms *MigrationState) WriteToFile(filePath string) error {
	// Update timestamp
	ms.Timestamp = time.Now()

	// Marshal to JSON with indentation
	data, err := json.MarshalIndent(ms, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal migration state: %w", err)
	}

	// Atomic write: write to temp file first, then rename
	tmpFile := filePath + ".tmp"
	if err := os.WriteFile(tmpFile, data, 0644); err != nil {
		return fmt.Errorf("failed to write temp file: %w", err)
	}

	if err := os.Rename(tmpFile, filePath); err != nil {
		os.Remove(tmpFile) // Clean up temp file on error
		return fmt.Errorf("failed to rename temp file: %w", err)
	}

	return nil
}

// UpsertMigration adds a new migration or updates an existing one by ID
func (ms *MigrationState) UpsertMigration(config MigrationConfig) {
	for i, existing := range ms.Migrations {
		if existing.MigrationId == config.MigrationId {
			ms.Migrations[i] = config
			return
		}
	}
	ms.Migrations = append(ms.Migrations, config)
}

// GetMigrationById retrieves a migration by its ID
func (ms *MigrationState) GetMigrationById(migrationId string) (*MigrationConfig, error) {
	for _, config := range ms.Migrations {
		if config.MigrationId == migrationId {
			c := config
			return &c, nil
		}
	}
	return nil, fmt.Errorf("migration not found: %s", migrationId)
}
