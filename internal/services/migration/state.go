package migration

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/confluentinc/kcp/internal/build_info"
	"github.com/confluentinc/kcp/internal/types"
)

// ----- migration FSM state and events -----

// FSM State constants
const (
	StateUninitialized    = "uninitialized"
	StateInitialized      = "initialized"
	StateLagsOk           = "lags_ok"
	StateFenced           = "fenced"
	StateOffsetSyncPaused = "offset_sync_paused"
	StateFenceVerified    = "fence_verified"
	StatePromoted         = "promoted"
	StateSwitched         = "switched"
)

// isKnownState reports whether s is a state value this binary understands.
// Execute refuses unknown values so a corrupted state file — or one written
// by a newer kcp — fails loudly instead of skipping every workflow step.
func isKnownState(s string) bool {
	switch s {
	case StateUninitialized, StateInitialized, StateLagsOk, StateFenced,
		StateOffsetSyncPaused, StateFenceVerified, StatePromoted, StateSwitched:
		return true
	}
	return false
}

// FSM Event constants
const (
	EventInitialize  = "initialize"
	EventWaitForLags = "wait_for_lags"
	EventFence       = "fence"
	// EventPauseOffsetSync pauses cluster-link consumer offset sync
	// (--pause-consumer-offset-sync) immediately after fencing. Without the
	// opt-in the transition still fires as a pass-through so the forward
	// walk is identical either way.
	EventPauseOffsetSync = "pause_offset_sync"
	EventVerifyFence     = "verify_fence"
	EventPromote         = "promote"
	EventSwitch          = "switch"
	// EventAbortFence rolls back to initialized when the pause_offset_sync
	// step fails (from fenced) or the verify_fence step detects unrouted
	// producers (from offset_sync_paused); the transition itself unfences
	// the gateway and restores any paused sync config (see onAbortFence in
	// orchestrator.go).
	EventAbortFence = "abort_fence"
	// EventExpireVerification demotes fence_verified to fenced at FSM
	// bootstrap: the verification is a point-in-time attestation and never
	// survives a restart, so a resume re-runs the verify_fence detection
	// window. Fired only by NewMigrationOrchestrator; it has no action.
	EventExpireVerification = "expire_verification"
	// EventExpireFence demotes fenced and offset_sync_paused to lags_ok at FSM
	// bootstrap: whether the live gateway still holds the fenced CR is equally
	// a point-in-time fact. A crash or a partially-completed abort_fence
	// rollback (initial CR applied, process gone before the rolled-back state
	// reached disk) leaves the gateway unfenced while the state file still
	// records a fenced-family state. Demoting makes the resume re-apply the
	// fenced CR — a no-op rollout when the gateway never diverged — instead of
	// verifying and promoting behind a fence that may not exist. Fired only by
	// NewMigrationOrchestrator; it has no action.
	EventExpireFence = "expire_fence"
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

	// DetectUnroutedProducersDuration is the monitoring window for the post-fence
	// safety check that verifies source offsets are not still increasing before
	// promoting mirror topics. A value of 0 skips the check. An increasing offset
	// after fencing indicates a producer that bypassed the gateway and is writing
	// directly to the source cluster.
	DetectUnroutedProducersDuration time.Duration `json:"detect_unrouted_producers_duration"`

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
	Migrations   []MigrationConfig  `json:"migrations"`
	KcpBuildInfo types.KcpBuildInfo `json:"kcp_build_info"`
	Timestamp    time.Time          `json:"timestamp"`
}

// NewMigrationState creates a new empty MigrationState with metadata
func NewMigrationState() *MigrationState {
	return &MigrationState{
		Migrations: []MigrationConfig{},
		KcpBuildInfo: types.KcpBuildInfo{
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

	// Atomic write: write to a uniquely-named temp file (created at mode 0600 by
	// os.CreateTemp and pinned explicitly), then rename it onto the target. The
	// migration state holds sensitive metadata, so it must never be group/world
	// readable, even briefly or under an unusual umask. The real file is only
	// ever replaced by the rename and is never deleted directly, so a crash
	// before the rename leaves the previous migration state intact.
	tmpFile, err := os.CreateTemp(filepath.Dir(filePath), "."+filepath.Base(filePath)+".tmp-*")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpName := tmpFile.Name()

	if err := tmpFile.Chmod(0600); err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmpName)
		return fmt.Errorf("failed to set temp file permissions: %w", err)
	}
	if _, err := tmpFile.Write(data); err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmpName)
		return fmt.Errorf("failed to write temp file: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("failed to close temp file: %w", err)
	}

	if err := os.Rename(tmpName, filePath); err != nil {
		_ = os.Remove(tmpName) // best-effort cleanup of temp file on error
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
