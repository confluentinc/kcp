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
	// (spec.staticMigration.consumerOffsetSync.pause) immediately after fencing. Without the
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
	// EventAbortFence rolls back to initialized, from fenced or
	// offset_sync_paused. The orchestrator fires it for ANY halting error
	// before promote while this run's fence is up (see handleStepFailure in
	// orchestrator.go), not only a pause_offset_sync failure or unrouted
	// producers. The transition itself only unfences the gateway
	// (onAbortFence); the paused sync config is restored by handleStepFailure
	// after the transition lands.
	EventAbortFence = "abort_fence"
)
