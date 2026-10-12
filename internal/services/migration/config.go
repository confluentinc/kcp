package migration

import "time"

// MigrationConfig holds all domain configuration for a migration: pure data,
// built from the manifest and the run's reconcile result.
type MigrationConfig struct {
	MigrationId string

	// Destination cluster configuration
	ClusterId           string
	ClusterRestEndpoint string
	ClusterLinkName     string
	Topics              []string

	// MigrateTopics is every topic this run migrates — Topics plus the
	// already-promoted ones — set each run from the reconcile result. The fence
	// check watches their source offsets: an already-promoted topic's mirror no
	// longer copies from the source, so a write straight to it would be lost.
	MigrateTopics []string

	// AwaitStopped is the subset of Topics that reconcile found already
	// mid-promotion (PENDING_STOPPED) on this run — resume-derived, so it is
	// (re)populated fresh from the reconcile Result every run. The promote
	// stage seeds these straight into its awaiting-STOPPED set: it waits for
	// them to reach STOPPED and never re-issues a promote on an already-
	// promoting mirror. Empty on a first run (nothing promoted yet).
	AwaitStopped []string

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
	// result — the fence/switch derivation base.
	GatewayYAML string

	// GatewayConfigPort is the port the gateway's GET /config endpoint is served
	// on. Configurable because the contract requires it to be; nothing fronts
	// this port, so kcp dials pod IPs on it directly.
	GatewayConfigPort int

	// Route is the single spec.route.name this migration fences and
	// switches, read from the manifest each run. AAO, like TBM, only ever
	// operates on one route per migration.
	Route string

	// FenceYAML, SwitchoverYAML and RollbackFenceYAML are the route shapes
	// migplan.Reconcile returns, set each run from the reconcile result and
	// applied as given. For a static route: FenceYAML is a {fence: …} block set
	// on Route; SwitchoverYAML (the switched route) and RollbackFenceYAML (the
	// route as a rollback leaves it, the start-of-run route with the fence
	// taken out) are whole {route: …} documents that replace Route. For a
	// dynamic route each is a whole rules: block set on Route's rules.
	FenceYAML         string
	SwitchoverYAML    string
	RollbackFenceYAML string

	// RollbackAllowed is set each run from the reconcile result: true when a
	// pre-promote failure may roll back (unfence), i.e. no topic in the batch
	// is promoted or promoting yet.
	RollbackAllowed bool

	// FencedAtStart is set each run from the reconcile result: true when the
	// route already carried kcp's fence for this migration at the start of the
	// run, left by an earlier, interrupted run.
	FencedAtStart bool
}
