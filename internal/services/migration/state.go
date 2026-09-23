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

// MigrationConfig holds all domain configuration for a migration.
// This is pure data with no behavior - just fields that get serialized (for
// the run report) or passed to a live call - there is no migration state
// file, so this struct is built fresh from the manifest on every run.
type MigrationConfig struct {
	MigrationId string `json:"migration_id"`

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

	// AwaitStopped is the subset of Topics that reconcile found already
	// mid-promotion (PENDING_STOPPED) on this run — resume-derived, so it is
	// (re)populated fresh from the reconcile Result every run. The promote
	// stage seeds these straight into its awaiting-STOPPED set: it waits for
	// them to reach STOPPED and never re-issues a promote on an already-
	// promoting mirror. Empty on a first run (nothing promoted yet).
	AwaitStopped []string `json:"await_stopped,omitempty"`

	// TopicPatterns is the declared spec.route.topicGroup[0].topicPatterns,
	// read fresh from the manifest each run alongside Route/TargetDomain —
	// nil when the manifest instead used an explicit topics list. Unlike
	// Topics (the resolved topic set, populated once reconcile runs), this is
	// the raw declared patterns themselves.
	TopicPatterns []string `json:"topic_patterns,omitempty"`

	// Operator intent: pause cluster-link consumer offset sync for the duration of execute.
	// PauseConsumerOffsetSync records the operator's choice at init time.
	PauseConsumerOffsetSync bool `json:"pause_consumer_offset_sync"`

	// ConsumerOffsetSyncBaseline is the declared pre-migration state of
	// consumer.offset.sync.enable on the cluster link (manifest
	// spec.clusterLink.consumerOffsetSyncBaseline: "enabled" or "disabled"),
	// captured once at registration from the manifest projection — never from
	// the live cluster link. The pause/restore bookends apply this value as an
	// idempotent AlterConfigs SET rather than diffing against a live snapshot,
	// so neither depends on data read back from the cluster link.
	ConsumerOffsetSyncBaseline string `json:"consumer_offset_sync_baseline,omitempty"`

	// DetectUnroutedProducersDuration is the monitoring window for the post-fence
	// safety check that verifies source offsets are not still increasing before
	// promoting mirror topics. A value of 0 skips the check. An increasing offset
	// after fencing indicates a producer that bypassed the gateway and is writing
	// directly to the source cluster.
	DetectUnroutedProducersDuration time.Duration `json:"detect_unrouted_producers_duration"`

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
	ConsumerOffsetSyncDrainDuration time.Duration `json:"consumer_offset_sync_drain_duration"`

	// Gateway CR configuration
	InitialCrName string `json:"initial_cr_name"`
	K8sNamespace  string `json:"k8s_namespace"`

	// GatewayYAML is the whole gateway CR migplan pulled and cleaned (see
	// migplan/gatewayfile.go's cleanGatewayDoc), captured once at init — the
	// fence/switch derivation base. Renamed from InitialCrYAML; no longer a
	// separately re-cleaned []byte, since migplan strips server-managed
	// metadata once, centrally.
	GatewayYAML string `json:"gateway_yaml"`

	// GatewayVerificationMode is how kcp confirms a gateway state transition
	// landed, as resolved against the live cluster at init time. It records what
	// the operator was told to expect; execute re-derives it and the re-derived
	// value is the one that governs the run, because the cluster can be upgraded
	// (or rolled back) between init and execute.
	GatewayVerificationMode string `json:"gateway_verification_mode"`

	// GatewayHotReloadEnabled records whether spec.hotReload.enabled was declared
	// at init time by the live Gateway CR or by either of the CRs this migration
	// will apply — the fence apply is what puts hot-reload into force, so the files
	// count. Diagnostic: it explains which gate produced GatewayVerificationMode.
	GatewayHotReloadEnabled bool `json:"gateway_hot_reload_enabled"`

	// GatewayConfigPort is the port the gateway's GET /config endpoint is served
	// on. Configurable because the contract requires it to be; nothing fronts
	// this port, so kcp dials pod IPs on it directly.
	GatewayConfigPort int `json:"gateway_config_port"`

	// Route is the single spec.route.name this migration fences and
	// switches — captured once at init, mirroring TBMConfig.Route. AAO, like
	// TBM, only ever operates on one route per migration.
	Route string `json:"route"`

	// TargetDomain is spec.route.targetStreamingDomain, read directly from
	// the manifest (not from migplan.Result, which does not carry it).
	TargetDomain string `json:"target_domain"`

	// FenceYAML and SwitchoverYAML are the small, route-agnostic fragments
	// migplan.Reconcile returns (a {fence: {...}} block, a
	// {streamingDomain: {...}} block for a static route) — captured once at
	// init, never re-derived. Applied by splicing onto Route's fence/
	// streamingDomain key in GatewayYAML (see gateway.ReplaceRouteFenceObj/
	// ReplaceRouteStreamingDomainObj) rather than the old whole-CR mutation.
	FenceYAML      string `json:"fence_yaml"`
	SwitchoverYAML string `json:"switchover_yaml"`

	// Mode is the route mode migplan resolved this migration under ("static" for
	// AAO, "dynamic" for TBM). Mirrors migplan.Result.Mode/reconcile.Plan.Mode.
	Mode string `json:"mode"`

	// LastRunPolicies records the effective execute-time policy this run used —
	// the manifest's spec.defaultPolicies with any per-run flag overrides
	// applied. It is observational: written for the operator and support (the
	// run report), never read back by kcp. A pointer with omitempty so it is
	// absent until an execute has actually populated it.
	LastRunPolicies *LastRunPolicies `json:"last_run_policies,omitempty"`
}

// LastRunPolicies is the observational record of the effective policy an execute
// run used (see MigrationConfig.LastRunPolicies). Its fields mirror
// manifest.DefaultPolicies one-to-one; zero values are recorded verbatim because
// zero is meaningful for every knob (0 skips the check / imposes no deadline /
// promotes all at once), so an audit reader sees exactly what each was set to.
type LastRunPolicies struct {
	LagThreshold                    int           `json:"lag_threshold"`
	PromoteBatchSize                int           `json:"promote_batch_size"`
	RolloutTimeout                  time.Duration `json:"rollout_timeout"`
	DetectUnroutedProducersDuration time.Duration `json:"detect_unrouted_producers_duration"`
	ConsumerOffsetSyncDrainDuration time.Duration `json:"consumer_offset_sync_drain_duration"`
	// HotReloadTimeout mirrors manifest.DefaultPolicies.HotReloadTimeout.
	HotReloadTimeout time.Duration `json:"hot_reload_timeout"`
	// GatewayConfigPort mirrors manifest.DefaultPolicies.GatewayConfigPort.
	GatewayConfigPort int `json:"gateway_config_port"`
}
