// Package tbm implements the FSM-driven orchestrator for the Topic-Batch
// Migration (TBM) workflow. It reads and writes migration.MigrationConfig /
// migration.MigrationState — the same shape and file AAO's execute uses —
// so both kinds of migration live in one state file. The FSM engine itself
// (this package's Actions/Orchestrator, its state/event constants) is a
// separate implementation from internal/services/migration: each has its
// own state machine, since TBM and AAO progress through genuinely different
// steps. Lower-level mechanics that are byte-for-byte identical between the
// two — gateway CR apply/wait/verify (gateway.TransitionVerifier), the
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

	// EventAbortFence rolls back to initialized when verify_fence detects
	// unrouted producers; the transition itself unfences the gateway (see
	// onAbortFence in orchestrator.go). Rolling back to initialized — not
	// lags_ok — means a resumed run re-checks lag for real via wait_for_lags
	// before re-fencing, true parity with AAO's own EventAbortFence, whose
	// only difference here is the single source state (TBM has no
	// offset_sync_paused stage).
	EventAbortFence = "abort_fence"
	// EventExpireVerification demotes fence_verified to fenced at FSM
	// bootstrap: the verification is a point-in-time attestation and never
	// survives a restart, so a resume re-runs the verify_fence detection
	// window. Fired only by NewTBMOrchestrator; it has no action.
	EventExpireVerification = "expire_verification"
	// EventExpireFence demotes fenced to initialized at FSM bootstrap: whether
	// the live gateway still holds the fenced CR is equally a point-in-time
	// fact — a crash mid-abort_fence rollback (unfence applied, state file not
	// yet updated) would otherwise leave the state file saying fenced while
	// the live gateway is not. The state file can't distinguish that from an
	// ordinary still-fenced resume, so this targets initialized (not lags_ok)
	// for the same reason EventAbortFence does: if the gateway really was
	// silently unfenced, normal (non-rogue) traffic may have raised lag during
	// the gap, and re-fencing without re-checking it would reopen the downtime
	// window without the guarantee wait_for_lags exists to provide. When the
	// gateway never actually diverged, the extra wait_for_lags/fence pass costs
	// little — the only way lag rises while genuinely fenced is an unrouted
	// producer, which verify_fence re-checks unconditionally on this same
	// resume regardless, and source connectivity is already required for that
	// same reason. Fired only by NewTBMOrchestrator; it has no action.
	EventExpireFence = "expire_fence"
)
