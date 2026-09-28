package migration

import "time"

// MigrationConfig holds all domain configuration for a migration: pure data,
// built from the manifest and the run's reconcile result.
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
	// restore_offset_sync step and the abort_fence rollback apply this value
	// as an idempotent AlterConfigs SET.
	ConsumerOffsetSyncBaseline string

	// RestoreOffsetSync is set each run from the reconcile result: true when
	// the restore_offset_sync step must set consumer.offset.sync.enable back
	// to ConsumerOffsetSyncBaseline after the switch.
	RestoreOffsetSync bool

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
