package migration

import (
	"time"
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

// ----- migration configuration -----

// MigrationConfig holds all domain configuration for a migration: pure data,
// built fresh from the manifest and the reconcile result on every run, held in
// memory only, and never written anywhere.
type MigrationConfig struct {
	MigrationId string

	// Gateway configuration
	KubeConfigPath string

	// Source cluster configuration
	SourceBootstrap string

	// Destination cluster configuration
	ClusterBootstrap    string
	ClusterId           string
	ClusterRestEndpoint string
	ClusterLinkName     string
	Topics              []string

	// AwaitStopped is the subset of Topics that reconcile found already
	// mid-promotion (PENDING_STOPPED) on this run — resume-derived, so it is
	// (re)populated fresh from the reconcile Result every run. The promote
	// stage seeds these straight into its awaiting-STOPPED set: it waits for
	// them to reach STOPPED and never re-issues a promote on an already-
	// promoting mirror. Empty on a first run (nothing promoted yet).
	AwaitStopped []string

	// TopicPatterns is the declared spec.route.topicGroup[0].topicPatterns,
	// read fresh from the manifest each run alongside Route/TargetDomain —
	// nil when the manifest instead used an explicit topics list. Unlike
	// Topics (the resolved topic set, populated once reconcile runs), this is
	// the raw declared patterns themselves.
	TopicPatterns []string

	// PauseConsumerOffsetSync is the operator's opt-in (manifest
	// spec.clusterLink.pauseConsumerOffsetSync, read each run) to pause
	// cluster-link consumer offset sync for the duration of execute.
	PauseConsumerOffsetSync bool

	// ConsumerOffsetSyncBaseline is the declared pre-migration state of
	// consumer.offset.sync.enable on the cluster link (manifest
	// spec.clusterLink.consumerOffsetSyncBaseline: "enabled" or "disabled"),
	// read from the manifest each run — never from the live cluster link. The
	// pause/restore bookends apply this value as an idempotent AlterConfigs SET
	// rather than diffing against a live snapshot, so neither depends on data
	// read back from the cluster link.
	ConsumerOffsetSyncBaseline string

	// DetectUnroutedProducersDuration is the monitoring window for the post-fence
	// safety check that verifies source offsets are not still increasing before
	// promoting mirror topics. A value of 0 skips the check. An increasing offset
	// after fencing indicates a producer that bypassed the gateway and is writing
	// directly to the source cluster.
	DetectUnroutedProducersDuration time.Duration

	// ConsumerOffsetSyncDrainDuration is how long the pause_offset_sync stage
	// waits after fencing before disabling the cluster link's
	// consumer.offset.sync.enable. The fence freezes source consumer offsets
	// (clients can no longer commit), so holding here lets the link run one or
	// more further sync cycles and propagate those final committed offsets to
	// the destination, minimising messages reprocessed after switchover. Only
	// has effect when PauseConsumerOffsetSync is set. Best-effort: offset sync
	// is asynchronous, so this reduces but does not eliminate duplicate
	// processing. A value of 0 (the default) skips the wait — the link is
	// disabled immediately after fencing, the prior behaviour.
	ConsumerOffsetSyncDrainDuration time.Duration

	// Gateway CR configuration
	InitialCrName string
	K8sNamespace  string

	// GatewayYAML is the whole gateway CR migplan pulled and cleaned (see
	// migplan/gatewayfile.go's cleanGatewayDoc), set each run from the reconcile
	// result — the fence/switch derivation base. Renamed from InitialCrYAML; no
	// longer a separately re-cleaned []byte, since migplan strips server-managed
	// metadata once, centrally.
	GatewayYAML string

	// GatewayVerificationMode is how kcp confirms a gateway state transition
	// landed, as resolved against the live cluster at init time. It records what
	// the operator was told to expect; execute re-derives it and the re-derived
	// value is the one that governs the run, because the cluster can be upgraded
	// (or rolled back) between init and execute.
	GatewayVerificationMode string

	// GatewayHotReloadEnabled records whether spec.hotReload.enabled was declared
	// at init time by the live Gateway CR or by either of the CRs this migration
	// will apply — the fence apply is what puts hot-reload into force, so the files
	// count. Diagnostic: it explains which gate produced GatewayVerificationMode.
	GatewayHotReloadEnabled bool

	// GatewayConfigPort is the port the gateway's GET /config endpoint is served
	// on. Configurable because the contract requires it to be; nothing fronts
	// this port, so kcp dials pod IPs on it directly.
	GatewayConfigPort int

	// Route is the single spec.route.name this migration fences and
	// switches, read from the manifest each run. AAO, like TBM, only ever
	// operates on one route per migration.
	Route string

	// TargetDomain is spec.route.targetStreamingDomain, read directly from
	// the manifest (not from migplan.Result, which does not carry it).
	TargetDomain string

	// FenceYAML and SwitchoverYAML are the small, route-agnostic fragments
	// migplan.Reconcile returns (a {fence: {...}} block, a
	// {streamingDomain: {...}} block for a static route), set each run from the
	// reconcile result. Applied by splicing onto Route's fence/
	// streamingDomain key in GatewayYAML (see gateway.ReplaceRouteFenceObj/
	// ReplaceRouteStreamingDomainObj) rather than the old whole-CR mutation.
	FenceYAML      string
	SwitchoverYAML string

	// Mode is the route mode migplan resolved this migration under ("static" for
	// AAO, "dynamic" for TBM). Mirrors migplan.Result.Mode/reconcile.Plan.Mode.
	Mode string
}
