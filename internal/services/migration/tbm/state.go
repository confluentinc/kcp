// Package tbm implements the FSM-driven orchestrator for the Topic-Batch
// Migration (TBM) workflow. It reads and writes migration.MigrationConfig —
// the same shape AAO's execute uses, built fresh from the manifest on every
// run (there is no migration state file). The FSM engine itself (this
// package's Actions/Orchestrator, its state/event constants) is a separate
// implementation from internal/services/migration: each has its own state
// machine, since TBM and AAO progress through genuinely different steps.
// Lower-level mechanics that are byte-for-byte identical between the two —
// gateway CR apply/wait/verify (gateway.TransitionVerifier), the
// source/destination offset sweep and unrouted-producer detection
// (internal/services/offset) — are shared service modules both packages
// call, rather than two hand-maintained copies drifting apart.
package tbm

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

const (
	EventInitialize  = "initialize"
	EventWaitForLags = "wait_for_lags"
	EventFence       = "fence"
	EventVerifyFence = "verify_fence"
	EventPromote     = "promote"
	EventSwitch      = "switch"

	// EventAbortFence rolls back to initialized when verify_fence detects
	// unrouted producers; the transition itself unfences the gateway (see
	// onAbortFence in orchestrator.go). Rolling back to initialized — not
	// lags_ok — means a resumed run re-checks lag for real via wait_for_lags
	// before re-fencing, true parity with AAO's own EventAbortFence, whose
	// only difference here is the single source state (TBM has no
	// offset_sync_paused stage).
	EventAbortFence = "abort_fence"
)
