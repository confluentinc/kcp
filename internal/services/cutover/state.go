package cutover

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/confluentinc/kcp/internal/build_info"
	"github.com/confluentinc/kcp/internal/types"
)

// ----- cutover FSM state and events -----

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

// ----- cutover configuration -----

// CutoverConfig holds all domain configuration for a cutover
// This is pure data with no behavior - just fields that get serialized
type CutoverConfig struct {
	CutoverId    string `json:"cutover_id"`
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

	// Cutover runtime data (populated during initialization)
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

// ----- cutover state file -----

// CutoverState represents the cutover state file structure
// This is a dedicated state file for cutover commands (init, execute, list)
type CutoverState struct {
	Cutovers     []CutoverConfig    `json:"cutovers"`
	KcpBuildInfo types.KcpBuildInfo `json:"kcp_build_info"`
	Timestamp    time.Time          `json:"timestamp"`
}

// NewCutoverState creates a new empty CutoverState with metadata
func NewCutoverState() *CutoverState {
	return &CutoverState{
		Cutovers: []CutoverConfig{},
		KcpBuildInfo: types.KcpBuildInfo{
			Version: build_info.Version,
			Commit:  build_info.Commit,
			Date:    build_info.Date,
		},
		Timestamp: time.Now(),
	}
}

// NewCutoverStateFromFile loads a CutoverState from a JSON file
func NewCutoverStateFromFile(filePath string) (*CutoverState, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read cutover state file: %w", err)
	}

	var state CutoverState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("failed to unmarshal cutover state: %w", err)
	}

	return &state, nil
}

// WriteToFile saves the CutoverState to a JSON file using atomic write
func (ms *CutoverState) WriteToFile(filePath string) error {
	// Update timestamp
	ms.Timestamp = time.Now()

	// Marshal to JSON with indentation
	data, err := json.MarshalIndent(ms, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal cutover state: %w", err)
	}

	// Atomic write: write to a uniquely-named temp file (created at mode 0600 by
	// os.CreateTemp and pinned explicitly), then rename it onto the target. The
	// cutover state holds sensitive metadata, so it must never be group/world
	// readable, even briefly or under an unusual umask. The real file is only
	// ever replaced by the rename and is never deleted directly, so a crash
	// before the rename leaves the previous cutover state intact.
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

// UpsertCutover adds a new cutover or updates an existing one by ID
func (ms *CutoverState) UpsertCutover(config CutoverConfig) {
	for i, existing := range ms.Cutovers {
		if existing.CutoverId == config.CutoverId {
			ms.Cutovers[i] = config
			return
		}
	}
	ms.Cutovers = append(ms.Cutovers, config)
}

// GetCutoverById retrieves a cutover by its ID
func (ms *CutoverState) GetCutoverById(cutoverId string) (*CutoverConfig, error) {
	for _, config := range ms.Cutovers {
		if config.CutoverId == cutoverId {
			c := config
			return &c, nil
		}
	}
	return nil, fmt.Errorf("cutover not found: %s", cutoverId)
}
