package types

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/confluentinc/kcp/internal/build_info"
)

// MigrationState represents the migration state file structure
// This is a dedicated state file for migration commands (init, execute, list)
type MigrationState struct {
	Migrations   []Migration  `json:"migrations"`
	KcpBuildInfo KcpBuildInfo `json:"kcp_build_info"`
	Timestamp    time.Time    `json:"timestamp"`
}

// NewMigrationState creates a new empty MigrationState with metadata
func NewMigrationState() *MigrationState {
	return &MigrationState{
		Migrations: []Migration{},
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
func (ms *MigrationState) UpsertMigration(migration Migration) {
	for i, existing := range ms.Migrations {
		if existing.MigrationId == migration.MigrationId {
			ms.Migrations[i] = migration
			return
		}
	}
	ms.Migrations = append(ms.Migrations, migration)
}

// GetMigrationById retrieves a migration by its ID
func (ms *MigrationState) GetMigrationById(migrationId string) (*Migration, error) {
	for _, migration := range ms.Migrations {
		if migration.MigrationId == migrationId {
			// Return a copy to avoid external mutation
			m := migration
			return &m, nil
		}
	}
	return nil, fmt.Errorf("migration not found: %s", migrationId)
}

// UpdateFromConfig updates a migration's config fields from a MigrationConfig
// This is a bridge method for the orchestrator until state structure is fully migrated
func (ms *MigrationState) UpdateFromConfig(config *MigrationConfig) error {
	for i := range ms.Migrations {
		if ms.Migrations[i].MigrationId == config.MigrationId {
			// Update all config fields
			ms.Migrations[i].CurrentState = config.CurrentState
			ms.Migrations[i].GatewayNamespace = config.GatewayNamespace
			ms.Migrations[i].GatewayCrdName = config.GatewayCrdName
			ms.Migrations[i].SourceName = config.SourceName
			ms.Migrations[i].DestinationName = config.DestinationName
			ms.Migrations[i].SourceRouteName = config.SourceRouteName
			ms.Migrations[i].DestinationRouteName = config.DestinationRouteName
			ms.Migrations[i].KubeConfigPath = config.KubeConfigPath
			ms.Migrations[i].ClusterId = config.ClusterId
			ms.Migrations[i].ClusterRestEndpoint = config.ClusterRestEndpoint
			ms.Migrations[i].ClusterLinkName = config.ClusterLinkName
			ms.Migrations[i].Topics = config.Topics
			ms.Migrations[i].AuthMode = config.AuthMode
			ms.Migrations[i].ClusterLinkTopics = config.ClusterLinkTopics
			ms.Migrations[i].ClusterLinkConfigs = config.ClusterLinkConfigs
			ms.Migrations[i].GatewayOriginalYAML = config.GatewayOriginalYAML
			ms.Migrations[i].CCBootstrapEndpoint = config.CCBootstrapEndpoint
			ms.Migrations[i].LoadBalancerEndpoint = config.LoadBalancerEndpoint
			return nil
		}
	}
	return fmt.Errorf("migration %s not found in state", config.MigrationId)
}
