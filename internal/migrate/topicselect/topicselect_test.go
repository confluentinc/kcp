package topicselect

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSelectTopics(t *testing.T) {
	all := []string{"orders", "orders.created", "events", "_schemas", "__consumer_offsets", "_confluent-x"}

	got, err := SelectTopics(all, []string{"*"}, nil)
	require.NoError(t, err)
	// internal topics (leading "_") always dropped; result sorted
	require.Equal(t, []string{"events", "orders", "orders.created"}, got)

	got, err = SelectTopics(all, []string{"orders*"}, nil)
	require.NoError(t, err)
	require.Equal(t, []string{"orders", "orders.created"}, got)

	got, err = SelectTopics(all, []string{"*"}, []string{"orders.*"})
	require.NoError(t, err)
	require.Equal(t, []string{"events", "orders"}, got) // exclude removes orders.created (orders has no dot suffix)

	_, err = SelectTopics(all, []string{"["}, nil)
	require.Error(t, err)
}

// catalog is a realistic mixed topic list: dot/underscore/hyphen separators,
// internal topics (leading "_"), and near-collision names like metrics.cpu vs
// metrics_cpu and orders-* vs orders.created.v2.
func catalog() []string {
	return []string{
		"orders-1", "orders-2", "orders-3", "orders-4",
		"products-1", "products-2",
		"transactions-1",
		"orders.created.v2",
		"inventory_snapshot",
		"events-2026.q1",
		"metrics.cpu",
		"metrics_cpu",
		"__consumer_offsets",
		"_confluent-link-coordinator",
		"_schemas",
	}
}

// TestSelectTopics_Globs exercises glob include/exclude semantics over a
// realistic catalog: family globs, the single-char "?" wildcard, multi-include,
// excludes, empty matches, and the dot/underscore/hyphen distinctions that
// path.Match treats as literal, distinct characters.
func TestSelectTopics_Globs(t *testing.T) {
	tests := []struct {
		name    string
		include []string
		exclude []string
		want    []string
	}{
		{
			// orders-* matches the hyphen family only; orders.created.v2 is
			// "orders." not "orders-" so it is NOT included.
			name:    "family glob orders-*",
			include: []string{"orders-*"},
			want:    []string{"orders-1", "orders-2", "orders-3", "orders-4"},
		},
		{
			// "?" matches exactly one char; each orders-N has a single trailing
			// digit so all four match.
			name:    "question-mark wildcard orders-?",
			include: []string{"orders-?"},
			want:    []string{"orders-1", "orders-2", "orders-3", "orders-4"},
		},
		{
			name:    "multi-include products and transactions",
			include: []string{"products-*", "transactions-*"},
			want:    []string{"products-1", "products-2", "transactions-1"},
		},
		{
			name:    "empty match",
			include: []string{"nope-*"},
			want:    nil,
		},
		{
			// dotted glob: orders.created.* matches the dotted topic; the
			// hyphen family does not (no dot).
			name:    "dotted glob orders.created.*",
			include: []string{"orders.created.*"},
			want:    []string{"orders.created.v2"},
		},
		{
			name:    "suffix glob *.v2",
			include: []string{"*.v2"},
			want:    []string{"orders.created.v2"},
		},
		{
			name:    "underscore glob inventory_*",
			include: []string{"inventory_*"},
			want:    []string{"inventory_snapshot"},
		},
		{
			// "." and "_" are distinct literals to path.Match: metrics.* matches
			// only metrics.cpu, never metrics_cpu.
			name:    "dot is distinct from underscore metrics.*",
			include: []string{"metrics.*"},
			want:    []string{"metrics.cpu"},
		},
		{
			// ...and metrics_* matches only metrics_cpu, never metrics.cpu.
			name:    "underscore is distinct from dot metrics_*",
			include: []string{"metrics_*"},
			want:    []string{"metrics_cpu"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := SelectTopics(catalog(), tc.include, tc.exclude)
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

// TestSelectTopics_Exclude asserts an exclude glob (plus a literal exclude)
// removes the transactions family and metrics_cpu while leaving the
// near-collision metrics.cpu intact.
func TestSelectTopics_Exclude(t *testing.T) {
	got, err := SelectTopics(catalog(), []string{"*"}, []string{"transactions-*", "metrics_cpu"})
	require.NoError(t, err)
	require.NotContains(t, got, "transactions-1")
	require.NotContains(t, got, "metrics_cpu")
	require.Contains(t, got, "metrics.cpu")
	// excluded-distinct sanity: the dotted twin survives the underscore exclude.
	require.Contains(t, got, "orders-1")
}

// TestSelectTopics_InternalExclusion confirms a broad "*" include never
// resurfaces internal topics (leading "_") — they are excluded by default.
func TestSelectTopics_InternalExclusion(t *testing.T) {
	got, err := SelectTopics(catalog(), []string{"*"}, nil)
	require.NoError(t, err)
	require.NotContains(t, got, "__consumer_offsets")
	require.NotContains(t, got, "_confluent-link-coordinator")
	require.NotContains(t, got, "_schemas")
	require.Contains(t, got, "orders-1")
	require.Contains(t, got, "metrics.cpu")
}

// TestSelectTopics_InternalOptIn covers Option A: an internal topic is admitted
// only when an include pattern that itself starts with "_" matches it; a broad
// "*" does not opt internals in; and an exclude glob always wins, even over an
// opted-in internal topic.
func TestSelectTopics_InternalOptIn(t *testing.T) {
	tests := []struct {
		name      string
		all       []string
		include   []string
		exclude   []string
		wantHas   []string // must be present
		wantMisse []string // must be absent
	}{
		{
			name:      "literal underscore include opts in just that topic",
			all:       []string{"orders", "_foo", "_schemas", "__consumer_offsets"},
			include:   []string{"*", "_foo"},
			wantHas:   []string{"orders", "_foo"},
			wantMisse: []string{"_schemas", "__consumer_offsets"}, // not named, "*" doesn't opt in
		},
		{
			name:      "broad star alone opts in no internals",
			all:       []string{"orders", "_foo", "_schemas"},
			include:   []string{"*"},
			wantHas:   []string{"orders"},
			wantMisse: []string{"_foo", "_schemas"},
		},
		{
			name:      "underscore-star opts in all internal topics",
			all:       []string{"orders", "_foo", "_schemas", "__consumer_offsets"},
			include:   []string{"_*"},
			wantHas:   []string{"_foo", "_schemas", "__consumer_offsets"},
			wantMisse: []string{"orders"}, // "_*" doesn't match a non-underscore topic
		},
		{
			name:      "exclude wins over an opted-in internal topic",
			all:       []string{"orders", "_foo", "_schemas"},
			include:   []string{"*", "_*"},
			exclude:   []string{"_foo"},
			wantHas:   []string{"orders", "_schemas"},
			wantMisse: []string{"_foo"}, // explicitly opted in, but exclude wins
		},
		{
			name:      "double-underscore opt-in via __* pattern",
			all:       []string{"_schemas", "__consumer_offsets"},
			include:   []string{"__*"},
			wantHas:   []string{"__consumer_offsets"},
			wantMisse: []string{"_schemas"}, // __* doesn't match a single-underscore topic
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := SelectTopics(tc.all, tc.include, tc.exclude)
			require.NoError(t, err)
			for _, h := range tc.wantHas {
				require.Contains(t, got, h)
			}
			for _, m := range tc.wantMisse {
				require.NotContains(t, got, m)
			}
		})
	}
}
