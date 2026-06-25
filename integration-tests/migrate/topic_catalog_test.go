//go:build integration

package migrate

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Source topic catalog — a standard fixture set seeded on a data-source broker
// for glob/selection testing in the later topic tests (T4-T6).
// ---------------------------------------------------------------------------

// catalogTopic is a fixture topic: name + partition count.
type catalogTopic struct {
	name       string
	partitions int
}

// topicCatalog is the standard fixture set seeded on a data-source broker for
// glob/selection testing. The numbered families (orders-*, products-*,
// transactions-*) exercise prefix globs; the exotic names exercise dot,
// underscore and hyphen handling — the only special characters Kafka allows in
// topic names.
//
// NOTE: a dot and an underscore are interchangeable for Kafka's topic-name
// COLLISION check (metrics.cpu and metrics_cpu cannot coexist — the broker
// rejects the second with InvalidTopicException). So the catalog must not
// contain a dotted/underscored pair that collides. We keep the dotted
// "metrics.cpu" and use a non-colliding underscore name ("latency_ms") to still
// exercise underscore handling and the dot-vs-underscore distinction.
func topicCatalog() []catalogTopic {
	return []catalogTopic{
		{"orders-1", 3}, {"orders-2", 1}, {"orders-3", 2}, {"orders-4", 1},
		{"products-1", 2}, {"products-2", 1}, {"products-3", 1}, {"products-4", 1},
		{"transactions-1", 4}, {"transactions-2", 1}, {"transactions-3", 1}, {"transactions-4", 1},
		{"orders.created.v2", 3}, {"inventory_snapshot", 1}, {"events-2026.q1", 2},
		{"metrics.cpu", 1}, {"latency_ms", 1},
	}
}

// catalogUserTopics returns just the catalog topic names (for subset assertions
// on include:["*"]).
func catalogUserTopics() []string {
	cat := topicCatalog()
	out := make([]string, 0, len(cat))
	for _, ct := range cat {
		out = append(out, ct.name)
	}
	return out
}

// seedTopicCatalog idempotently creates the catalog on the given broker
// (createTopic already tolerates 409 already-exists). Fixtures persist for the
// run and are NOT deleted, so multiple topic tests can share them.
func seedTopicCatalog(t *testing.T, c restClient, clusterID string) {
	t.Helper()
	for _, ct := range topicCatalog() {
		c.createTopic(t, clusterID, ct.name, ct.partitions)
	}
}

// ---------------------------------------------------------------------------
// Pure smoke test — references the catalog helpers so they are not flagged as
// unused, and asserts the catalog is well-formed. Needs no broker.
// ---------------------------------------------------------------------------

// TestTopicCatalog_Wellformed checks the catalog is non-empty, has unique names,
// positive partition counts, and that catalogUserTopics() matches topicCatalog().
func TestTopicCatalog_Wellformed(t *testing.T) {
	cat := topicCatalog()
	require.NotEmpty(t, cat, "topic catalog must not be empty")

	seen := map[string]struct{}{}
	for _, ct := range cat {
		require.NotEmpty(t, ct.name, "catalog topic name must not be empty")
		require.Positive(t, ct.partitions, "catalog topic %q must have a positive partition count", ct.name)
		_, dup := seen[ct.name]
		require.False(t, dup, "catalog topic name %q is duplicated", ct.name)
		seen[ct.name] = struct{}{}
	}

	names := catalogUserTopics()
	require.Len(t, names, len(cat), "catalogUserTopics() length must match topicCatalog()")
	for i, ct := range cat {
		require.Equal(t, ct.name, names[i], "catalogUserTopics()[%d] must match topicCatalog()[%d]", i, i)
	}
}
