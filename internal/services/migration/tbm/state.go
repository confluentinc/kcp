// Package tbm implements the FSM-driven orchestrator for the Topic-Batch
// Migration (TBM) workflow. initialize, wait_for_lags, fence, verify_fence,
// promote and switch are all real — see workflow.go — plus a compensating
// abort_fence rollback when verify_fence detects a producer bypassing the
// fence. It mirrors the shape of internal/services/migration (state.go /
// orchestrator.go / workflow.go / reporter.go) but is a fully separate
// package: no domain logic or types are shared between the two.
package tbm

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/confluentinc/kcp/internal/atomicwrite"
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
	StateSwitched      = "switched"
)

// isKnownState reports whether s is a state value this binary understands.
// Execute refuses unknown values so a corrupted state file — or one written
// by a newer kcp — fails loudly instead of skipping every workflow step.
func isKnownState(s string) bool {
	switch s {
	case StateUninitialized, StateInitialized, StateLagsOk, StateFenced,
		StateFenceVerified, StatePromoted, StateSwitched:
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

	// EventAbortFence rolls back to lags_ok when verify_fence detects unrouted
	// producers; the transition itself unfences the gateway (see onAbortFence
	// in orchestrator.go). TBM's equivalent of AAO's EventAbortFence, minus the
	// offset_sync_paused source state TBM has no equivalent stage for.
	EventAbortFence = "abort_fence"
	// EventExpireVerification demotes fence_verified to fenced at FSM
	// bootstrap: the verification is a point-in-time attestation and never
	// survives a restart, so a resume re-runs the verify_fence detection
	// window. Fired only by NewTBMOrchestrator; it has no action.
	EventExpireVerification = "expire_verification"
	// EventExpireFence demotes fenced to lags_ok at FSM bootstrap: whether the
	// live gateway still holds the fenced CR is equally a point-in-time fact —
	// a crash mid-abort_fence rollback (unfence applied, state file not yet
	// updated) would otherwise leave the state file saying fenced while the
	// live gateway is not. Demoting makes the resume re-apply the fenced CR —
	// a no-op rollout when the gateway never diverged. Fired only by
	// NewTBMOrchestrator; it has no action.
	EventExpireFence = "expire_fence"
)

// ----- TBM configuration -----

// TBMConfig is the persisted per-migration record.
type TBMConfig struct {
	MigrationId  string `json:"migration_id"`
	CurrentState string `json:"current_state"`
	// ManifestHash is the sha256 (hex) of the raw --migration-yaml file bytes
	// recorded when this migration was created. Every execute-tbm run
	// recomputes it and refuses, unconditionally, on any mismatch — there is
	// no override.
	ManifestHash string `json:"manifest_hash"`

	// The fields below are captured once, at the initialize transition, from
	// the migplan.Result the command already computed live — and never
	// re-derived by any later transition.
	Topics         []string `json:"topics,omitempty"`
	FenceYAML      string   `json:"fence_yaml,omitempty"`
	SwitchoverYAML string   `json:"switchover_yaml,omitempty"`
	GatewayYAML    string   `json:"gateway_yaml,omitempty"`
	// Route is the manifest's spec.topicGroup[0].route, echoed back via
	// migplan.Result.Route — captured at initialize like the fields above.
	// fence (and later switch) need it to know which route's rules to graft
	// FenceYAML/SwitchoverYAML into.
	Route string `json:"route,omitempty"`

	// K8sNamespace and InitialCrName are captured ONCE, at config creation
	// (resolveTBMConfig), from spec.gateway.namespace/spec.gateway.cr-name —
	// mirroring exactly how migration.MigrationConfig's fields of the same
	// name are set once by kcp migration init, never re-derived on resume.
	K8sNamespace  string `json:"k8s_namespace"`
	InitialCrName string `json:"initial_cr_name"`

	// ClusterId, ClusterRestEndpoint and ClusterLinkName are captured ONCE, at
	// config creation, from spec.target.clusterId/spec.target.kafka.restEndpoint/
	// spec.clusterLink.name — the same three manifest fields
	// migration.MigrationConfig's fields of the same name are set from by
	// kcp migration init. Promote needs them to build the clusterlink.Config
	// it polls/promotes against.
	ClusterId           string `json:"cluster_id"`
	ClusterRestEndpoint string `json:"cluster_rest_endpoint"`
	ClusterLinkName     string `json:"cluster_link_name"`
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

	return atomicwrite.WriteFile(filePath, data, 0600)
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
