// Package tbm scaffolds a new FSM-driven orchestrator for the not-yet-designed
// Topic-Batch Migration (TBM) workflow. Every transition is currently a noop —
// see workflow.go. It mirrors the shape of internal/services/migration
// (state.go / orchestrator.go / workflow.go / reporter.go) but is a fully
// separate package: no domain logic or types are shared between the two.
package tbm

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/confluentinc/kcp/internal/build_info"
	"github.com/confluentinc/kcp/internal/types"
)

// ----- TBM FSM state and events -----

const (
	StateUninitialized = "uninitialized"
	StateInitialized   = "initialized"
	StateLagsOk        = "lags_ok"
	StateFenced        = "fenced"
	StateFenceVerified = "fence_verified"
	StatePromoted      = "promoted"
	StateDone          = "done"
)

// isKnownState reports whether s is a state value this binary understands.
// Execute refuses unknown values so a corrupted state file — or one written
// by a newer kcp — fails loudly instead of skipping every workflow step.
func isKnownState(s string) bool {
	switch s {
	case StateUninitialized, StateInitialized, StateLagsOk, StateFenced,
		StateFenceVerified, StatePromoted, StateDone:
		return true
	}
	return false
}

const (
	EventInitialize  = "initialize"
	EventWaitForLags = "wait_for_lags"
	EventFence       = "fence"
	EventVerifyFence = "verify_fence"
	EventPromote     = "promote"
	EventSwitch      = "switch"
)

// ----- TBM configuration -----

// TBMConfig is the persisted per-migration record. It carries no domain data
// yet (no batch plan, no topic list) — only enough to drive the FSM and detect
// manifest drift. Real fields will be added once the TBM batch design lands.
type TBMConfig struct {
	MigrationId  string `json:"migration_id"`
	CurrentState string `json:"current_state"`
	// ManifestHash is the sha256 (hex) of the raw --migration-yaml file bytes
	// recorded when this migration was created. Every execute-tbm run
	// recomputes it and refuses, unconditionally, on any mismatch — there is
	// no override.
	ManifestHash string `json:"manifest_hash"`
}

// HashManifest returns the sha256 (hex-encoded) digest of raw manifest file
// bytes, used to detect manifest drift against a persisted TBMConfig.
func HashManifest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// ----- TBM state file -----

// TBMState is the on-disk TBM state file structure — a dedicated file,
// separate from migration-state.json, since this is an unrelated workflow.
type TBMState struct {
	Migrations   []TBMConfig        `json:"migrations"`
	KcpBuildInfo types.KcpBuildInfo `json:"kcp_build_info"`
	Timestamp    time.Time          `json:"timestamp"`
}

// NewTBMState creates a new empty TBMState with build metadata.
func NewTBMState() *TBMState {
	return &TBMState{
		Migrations: []TBMConfig{},
		KcpBuildInfo: types.KcpBuildInfo{
			Version: build_info.Version,
			Commit:  build_info.Commit,
			Date:    build_info.Date,
		},
		Timestamp: time.Now(),
	}
}

// NewTBMStateFromFile loads a TBMState from a JSON file.
func NewTBMStateFromFile(filePath string) (*TBMState, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read tbm state file: %w", err)
	}

	var state TBMState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("failed to unmarshal tbm state: %w", err)
	}

	return &state, nil
}

// WriteToFile saves the TBMState to a JSON file using an atomic write, 0600 —
// the same safety properties as migration.MigrationState.WriteToFile.
func (ts *TBMState) WriteToFile(filePath string) error {
	ts.Timestamp = time.Now()

	data, err := json.MarshalIndent(ts, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal tbm state: %w", err)
	}

	return writeFileAtomic(filePath, data, 0600)
}

// UpsertMigration adds a new migration or updates an existing one by id.
func (ts *TBMState) UpsertMigration(config TBMConfig) {
	for i, existing := range ts.Migrations {
		if existing.MigrationId == config.MigrationId {
			ts.Migrations[i] = config
			return
		}
	}
	ts.Migrations = append(ts.Migrations, config)
}

// GetMigrationById retrieves a migration by its id.
func (ts *TBMState) GetMigrationById(migrationId string) (*TBMConfig, error) {
	for _, config := range ts.Migrations {
		if config.MigrationId == migrationId {
			c := config
			return &c, nil
		}
	}
	return nil, fmt.Errorf("migration not found: %s", migrationId)
}
