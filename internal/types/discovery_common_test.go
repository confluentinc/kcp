package types

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// cc builds a single-endpoint ConnectCluster slice (for ConnectRestURL url) with
// the named connectors and the given metrics pointer (which may be nil). Used to
// exercise mergeConnectClusters's per-URL merge behavior with both sides sharing
// the same endpoint.
func cc(url string, names []string, metrics *ConnectClusterMetrics) []ConnectCluster {
	conns := make([]Connector, 0, len(names))
	for _, n := range names {
		conns = append(conns, Connector{Name: n})
	}
	return []ConnectCluster{{ConnectRestURL: url, Connectors: conns, Metrics: metrics}}
}

func TestMergeConnectClusters_ByURLThenName(t *testing.T) {
	old := []ConnectCluster{{
		ConnectRestURL: "u1",
		Connectors:     []Connector{{Name: "a"}, {Name: "b"}},
		Metrics:        &ConnectClusterMetrics{},
	}}
	new_ := []ConnectCluster{{
		ConnectRestURL: "u1",
		Connectors:     []Connector{{Name: "b", State: "RUNNING"}, {Name: "c"}},
	}}
	got := mergeConnectClusters(new_, old)
	if len(got) != 1 {
		t.Fatalf("want 1 cluster, got %d", len(got))
	}
	names := map[string]string{}
	for _, c := range got[0].Connectors {
		names[c.Name] = c.State
	}
	if len(names) != 3 || names["b"] != "RUNNING" { // a preserved, b updated, c added
		t.Fatalf("connector merge wrong: %+v", got[0].Connectors)
	}
	if got[0].Metrics == nil { // old metrics preserved when new run had none
		t.Fatalf("metrics dropped on merge")
	}
}

func TestMergeConnectClusters_PreservesUntouchedEndpoint(t *testing.T) {
	old := []ConnectCluster{{ConnectRestURL: "u1", Connectors: []Connector{{Name: "a"}}}}
	new_ := []ConnectCluster{{ConnectRestURL: "u2", Connectors: []Connector{{Name: "z"}}}}
	got := mergeConnectClusters(new_, old)
	if len(got) != 2 {
		t.Fatalf("want 2 endpoints preserved, got %d", len(got))
	}
}

// R9: re-running a scan without --metrics must not wipe previously-collected
// metrics. New connectors take precedence; metrics prefer-new-fall-back-to-old.
func TestMergeConnectClusters_NewNilMetrics_KeepsOld(t *testing.T) {
	oldM := &ConnectClusterMetrics{}
	merged := mergeConnectClusters(cc("u1", []string{"a"}, nil), cc("u1", []string{"a"}, oldM))
	require.Len(t, merged, 1)
	require.NotNil(t, merged[0].Metrics)
	require.Same(t, oldM, merged[0].Metrics, "old metrics preserved when new run carries none")
}

func TestMergeConnectClusters_NewMetrics_Wins(t *testing.T) {
	oldM := &ConnectClusterMetrics{}
	newM := &ConnectClusterMetrics{}
	merged := mergeConnectClusters(cc("u1", []string{"a"}, newM), cc("u1", []string{"a"}, oldM))
	require.Same(t, newM, merged[0].Metrics, "freshly-collected metrics take precedence")
}

// Edge case (R9): the side that survives the per-URL merge has zero connectors
// but carries metrics — those metrics must not be dropped.
func TestMergeConnectClusters_OldZeroConnectorsButMetrics_Preserved(t *testing.T) {
	oldM := &ConnectClusterMetrics{}
	old := cc("u1", nil, oldM) // zero connectors, but has metrics
	new_ := cc("u1", []string{"a"}, nil)

	merged := mergeConnectClusters(new_, old)

	require.Len(t, merged[0].Connectors, 1, "new connectors retained")
	require.NotNil(t, merged[0].Metrics, "metrics must survive the zero-connector case")
	require.Same(t, oldM, merged[0].Metrics)
}

// Edge case (R9): the new run discovered zero connectors — old connectors and
// old metrics must both survive.
func TestMergeConnectClusters_NewZeroConnectors_KeepsOld(t *testing.T) {
	oldM := &ConnectClusterMetrics{}
	old := cc("u1", []string{"a"}, oldM)
	new_ := cc("u1", nil, nil)

	merged := mergeConnectClusters(new_, old)

	require.Len(t, merged[0].Connectors, 1, "old connectors retained")
	require.Same(t, oldM, merged[0].Metrics, "old metrics retained")
}

// Edge case (R9): new run has zero connectors but freshly-collected metrics —
// the new metrics win, old connectors survive.
func TestMergeConnectClusters_NewZeroConnectorsWithMetrics_PrefersNew(t *testing.T) {
	oldM := &ConnectClusterMetrics{}
	newM := &ConnectClusterMetrics{}
	old := cc("u1", []string{"a"}, oldM)
	new_ := cc("u1", nil, newM)

	merged := mergeConnectClusters(new_, old)

	require.Len(t, merged[0].Connectors, 1, "old connectors retained")
	require.Same(t, newM, merged[0].Metrics, "freshly-collected metrics take precedence even with zero new connectors")
}

func TestSetConnectCluster_ReplacesMatchedEntryOnly(t *testing.T) {
	info := &KafkaAdminClientInformation{ConnectClusters: []ConnectCluster{
		{ConnectRestURL: "u1", Connectors: []Connector{{Name: "old"}}, Metrics: &ConnectClusterMetrics{}},
		{ConnectRestURL: "u2", Connectors: []Connector{{Name: "keep"}}},
	}}
	info.SetConnectCluster("u1", []Connector{{Name: "new"}})
	if len(info.ConnectClusters) != 2 {
		t.Fatalf("want 2 entries, got %d", len(info.ConnectClusters))
	}
	var u1 *ConnectCluster
	for i := range info.ConnectClusters {
		if info.ConnectClusters[i].ConnectRestURL == "u1" {
			u1 = &info.ConnectClusters[i]
		}
	}
	if len(u1.Connectors) != 1 || u1.Connectors[0].Name != "new" {
		t.Fatalf("u1 connectors not replaced: %+v", u1.Connectors)
	}
	if u1.Metrics == nil {
		t.Fatalf("u1 metrics should be preserved on connector replace")
	}
}

func TestSetConnectCluster_CreatesWhenAbsent(t *testing.T) {
	info := &KafkaAdminClientInformation{}
	info.SetConnectCluster("u9", []Connector{{Name: "x"}})
	if len(info.ConnectClusters) != 1 || info.ConnectClusters[0].ConnectRestURL != "u9" {
		t.Fatalf("create-on-absent failed: %+v", info.ConnectClusters)
	}
}

func TestSetConnectClusterMetrics_CreatesWhenAbsent(t *testing.T) {
	info := &KafkaAdminClientInformation{}
	m := &ConnectClusterMetrics{}
	info.SetConnectClusterMetrics("u9", m)
	require.Len(t, info.ConnectClusters, 1)
	require.Equal(t, "u9", info.ConnectClusters[0].ConnectRestURL)
	require.Same(t, m, info.ConnectClusters[0].Metrics)
}

// SetConnectClusterMetrics must only touch the matched entry's Metrics, leaving
// its Connectors (and other entries entirely) untouched.
func TestSetConnectClusterMetrics_UpdatesMatchedEntryOnly(t *testing.T) {
	info := &KafkaAdminClientInformation{ConnectClusters: []ConnectCluster{
		{ConnectRestURL: "u1", Connectors: []Connector{{Name: "a"}}},
		{ConnectRestURL: "u2", Connectors: []Connector{{Name: "b"}}},
	}}
	m := &ConnectClusterMetrics{}
	info.SetConnectClusterMetrics("u1", m)
	require.Len(t, info.ConnectClusters, 2)
	for _, c := range info.ConnectClusters {
		if c.ConnectRestURL == "u1" {
			require.Same(t, m, c.Metrics)
			require.Len(t, c.Connectors, 1, "connectors untouched")
		} else {
			require.Nil(t, c.Metrics)
		}
	}
}
