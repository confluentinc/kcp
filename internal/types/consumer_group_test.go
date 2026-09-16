package types

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCalculateConsumerGroupSummary_Empty(t *testing.T) {
	summary := CalculateConsumerGroupSummary(nil)
	require.Equal(t, 0, summary.Total)
	require.Empty(t, summary.ByType)
	require.Empty(t, summary.ByState)
}

func TestCalculateConsumerGroupSummary_TotalByTypeByState(t *testing.T) {
	details := []ConsumerGroupDetails{
		{GroupID: "g1", Type: ConsumerGroupTypeClassic, State: "Stable"},
		{GroupID: "g2", Type: ConsumerGroupTypeClassic, State: "Empty"},
		{GroupID: "g3", Type: ConsumerGroupTypeConsumer, State: "Stable"},
		{GroupID: "g4", Type: ConsumerGroupTypeShare, State: "Dead"},
		{GroupID: "g5", Type: ConsumerGroupTypeStreams, State: "Stable"},
		// broker could not report a type (v4 ListGroups fallback) — must NOT be
		// fabricated as "classic", tallied under the "" bucket instead.
		{GroupID: "g6", Type: "", State: "Stable"},
	}

	summary := CalculateConsumerGroupSummary(details)

	require.Equal(t, 6, summary.Total)
	require.Equal(t, map[string]int{
		ConsumerGroupTypeClassic:  2,
		ConsumerGroupTypeConsumer: 1,
		ConsumerGroupTypeShare:    1,
		ConsumerGroupTypeStreams:  1,
		"":                        1,
	}, summary.ByType)
	require.Equal(t, map[string]int{
		"Stable": 4,
		"Empty":  1,
		"Dead":   1,
	}, summary.ByState)
}

func TestCalculateConsumerGroupSummary_UnknownTypeBucket(t *testing.T) {
	details := []ConsumerGroupDetails{
		{GroupID: "g1", Type: "", State: "Stable"},
		{GroupID: "g2", Type: "", State: "Stable"},
	}

	summary := CalculateConsumerGroupSummary(details)

	require.Equal(t, 2, summary.Total)
	require.Equal(t, 2, summary.ByType[""])
	require.Equal(t, 2, summary.ByState["Stable"])
}

// --- mergeConsumerGroups ---

func TestMergeConsumerGroups_NewWinsByGroupID(t *testing.T) {
	old := &ConsumerGroups{Details: []ConsumerGroupDetails{
		{GroupID: "g1", State: "Stable", Type: ConsumerGroupTypeClassic},
		{GroupID: "g2", State: "Stable", Type: ConsumerGroupTypeClassic},
	}}
	new_ := &ConsumerGroups{Details: []ConsumerGroupDetails{
		{GroupID: "g1", State: "Dead", Type: ConsumerGroupTypeClassic},    // updated
		{GroupID: "g3", State: "Stable", Type: ConsumerGroupTypeConsumer}, // added
	}}

	got := mergeConsumerGroups(new_, old)
	require.NotNil(t, got)
	require.Len(t, got.Details, 3, "g1 updated, g2 preserved, g3 added")

	byID := map[string]ConsumerGroupDetails{}
	for _, d := range got.Details {
		byID[d.GroupID] = d
	}
	require.Equal(t, "Dead", byID["g1"].State, "new wins for a re-discovered group")
	require.Equal(t, "Stable", byID["g2"].State, "old preserved when not re-discovered")
	require.Equal(t, "Stable", byID["g3"].State, "new-only group added")

	// Summary must be recomputed from the merged details.
	require.Equal(t, 3, got.Summary.Total)
}

func TestMergeConsumerGroups_OldPreservedWhenNotRediscovered(t *testing.T) {
	old := &ConsumerGroups{Details: []ConsumerGroupDetails{
		{GroupID: "g1", State: "Stable"},
		{GroupID: "g2", State: "Stable"},
	}}
	new_ := &ConsumerGroups{Details: []ConsumerGroupDetails{
		{GroupID: "g1", State: "Dead"},
	}}

	got := mergeConsumerGroups(new_, old)
	require.Len(t, got.Details, 2)

	byID := map[string]ConsumerGroupDetails{}
	for _, d := range got.Details {
		byID[d.GroupID] = d
	}
	require.Equal(t, "Dead", byID["g1"].State)
	require.Equal(t, "Stable", byID["g2"].State, "g2 not re-discovered this run, must survive")
}

func TestMergeConsumerGroups_NilSafety(t *testing.T) {
	groups := &ConsumerGroups{Details: []ConsumerGroupDetails{{GroupID: "g1"}}}

	require.Same(t, groups, mergeConsumerGroups(groups, nil), "nil old -> return new")
	require.Same(t, groups, mergeConsumerGroups(nil, groups), "nil new -> return old")
	require.Nil(t, mergeConsumerGroups(nil, nil), "both nil -> nil")
}

func TestMergeConsumerGroups_EmptyDetailsTreatedLikeNil(t *testing.T) {
	populated := &ConsumerGroups{Details: []ConsumerGroupDetails{{GroupID: "g1"}}}
	empty := &ConsumerGroups{Details: []ConsumerGroupDetails{}}

	// Old has zero details: new returned as-is (mirrors mergeTopics semantics).
	require.Same(t, populated, mergeConsumerGroups(populated, empty))
	// New has zero details: old returned as-is (a denied/empty re-scan must not wipe state).
	require.Same(t, populated, mergeConsumerGroups(empty, populated))
}

func TestMergeConsumerGroups_BothEmptyDetails(t *testing.T) {
	empty := &ConsumerGroups{Details: []ConsumerGroupDetails{}}
	got := mergeConsumerGroups(empty, empty)
	require.Same(t, empty, got)
}
