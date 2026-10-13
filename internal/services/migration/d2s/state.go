// Package d2s implements the FSM-driven orchestrator for a dynamic-to-static route conversion
// (spec.route.convertTo: static): fence the dynamic route, re-check the conversion on post-fence data, copy
// the in-scope consumer groups' committed offsets to the destination, and switch the route to static. It reads
// and writes migration.MigrationConfig, built from the manifest and the run's reconcile result on every run,
// like AAO and TBM.
//
// The state machine is its own, in tbm's shape. The lower-level mechanics are shared: gateway CR apply and
// verify (gateway.TransitionVerifier), and the committed-offset engine (groupoffsets). Nothing is persisted:
// every run starts at uninitialized and re-derives everything from the live world.
//
// Not to be confused with internal/services/migration/offset_sync.go, which toggles the cluster link's
// consumer.offset.sync.enable.
package d2s

// ----- d2s FSM states and events -----

const (
	StateUninitialized = "uninitialized"
	StateInitialized   = "initialized"
	StateFenced        = "fenced"
	StateFenceVerified = "fence_verified"
	StateOffsetsSynced = "offsets_synced"
	StateSwitched      = "switched"
)

const (
	EventInitialize  = "initialize"
	EventFence       = "fence"
	EventVerifyFence = "verify_fence"
	EventSyncOffsets = "sync_offsets"
	EventSwitch      = "switch"

	// EventAbortFence rolls back to initialized from any fenced state before the switch (fenced,
	// fence_verified, offsets_synced; spec decision 10): offsets written before the switch are inert, because
	// group coordination stays pinned to the source until the switch rewrites the route. The transition itself
	// unfences the gateway (see onAbortFence). A switch failure never takes it (decision 25), so in practice it
	// fires from fenced and fence_verified.
	EventAbortFence = "abort_fence"
)
