package migration

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
)
