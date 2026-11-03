package migration

import "go.opentelemetry.io/otel/attribute"

// tracerName is the instrumentation scope of this package's spans.
const tracerName = "kcp/services/migration"

// Rollback paths, recorded as kcp.rollback.path on migration.rollback.
const (
	RollbackPathAbortFence       = "abort_fence"       // this run's fence, undone along the FSM's abort_fence edge
	RollbackPathEarlierRunFence  = "earlier_run_fence" // an interrupted run's fence, undone outside the FSM
	RollbackPathUnconfirmedFence = "unconfirmed_fence" // a fence that reached the CR but was never confirmed
)

// RunAttributes are migration.run's plan-derived attributes. They are only
// meaningful once initialize has copied the reconcile plan onto c. Counts
// only: no topic names.
func RunAttributes(c *MigrationConfig) []attribute.KeyValue {
	return []attribute.KeyValue{
		attribute.Int("kcp.topics.promote", len(c.Topics)),
		attribute.Int("kcp.topics.migrate", len(c.MigrateTopics)),
		attribute.Int("kcp.topics.await_stopped", len(c.AwaitStopped)),
		attribute.Bool("kcp.fenced_at_start", c.FencedAtStart),
		attribute.Bool("kcp.rollback_allowed", c.RollbackAllowed),
	}
}
