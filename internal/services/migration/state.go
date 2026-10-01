package migration

// ----- migration FSM state and events -----

// FSM State constants
const (
	StateUninitialized      = "uninitialized"
	StateInitialized        = "initialized"
	StateLagsOk             = "lags_ok"
	StateFenced             = "fenced"
	StateOffsetSyncPaused   = "offset_sync_paused"
	StateFenceVerified      = "fence_verified"
	StatePromoted           = "promoted"
	StateSwitched           = "switched"
	StateOffsetSyncRestored = "offset_sync_restored"
)

// FSM Event constants
const (
	EventInitialize  = "initialize"
	EventWaitForLags = "wait_for_lags"
	EventFence       = "fence"
	// EventPauseOffsetSync pauses cluster-link consumer offset sync
	// (spec.clusterLink.pauseConsumerOffsetSync) immediately after fencing. Without the
	// opt-in the transition still fires as a pass-through so the forward
	// walk is identical either way.
	EventPauseOffsetSync = "pause_offset_sync"
	EventVerifyFence     = "verify_fence"
	EventPromote         = "promote"
	EventSwitch          = "switch"
	// EventRestoreOffsetSync sets cluster-link consumer offset sync back to
	// the manifest's baseline after the switch, when the plan owes a restore.
	// Otherwise the transition still fires as a pass-through, like
	// pause_offset_sync.
	EventRestoreOffsetSync = "restore_offset_sync"
	// EventAbortFence rolls back to initialized when the pause_offset_sync
	// step fails (from fenced) or the verify_fence step detects unrouted
	// producers (from offset_sync_paused); the transition itself unfences
	// the gateway and restores any paused sync config (see onAbortFence in
	// orchestrator.go).
	EventAbortFence = "abort_fence"
)
