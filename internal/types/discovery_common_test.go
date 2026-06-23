package types

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// smc builds a SelfManagedConnectors with the named connectors and the given
// metrics pointer (which may be nil).
func smc(names []string, metrics *ConnectClusterMetrics) *SelfManagedConnectors {
	conns := make([]SelfManagedConnector, 0, len(names))
	for _, n := range names {
		conns = append(conns, SelfManagedConnector{Name: n})
	}
	return &SelfManagedConnectors{Connectors: conns, Metrics: metrics}
}

// R9: re-running a scan without --metrics must not wipe previously-collected
// metrics. New connectors take precedence; metrics prefer-new-fall-back-to-old.
func TestMergeSelfManagedConnectors_NewNilMetrics_KeepsOld(t *testing.T) {
	oldM := &ConnectClusterMetrics{}
	merged := mergeSelfManagedConnectors(smc([]string{"a"}, nil), smc([]string{"a"}, oldM))
	require.NotNil(t, merged.Metrics)
	require.Same(t, oldM, merged.Metrics, "old metrics preserved when new run carries none")
}

func TestMergeSelfManagedConnectors_NewMetrics_Wins(t *testing.T) {
	oldM := &ConnectClusterMetrics{}
	newM := &ConnectClusterMetrics{}
	merged := mergeSelfManagedConnectors(smc([]string{"a"}, newM), smc([]string{"a"}, oldM))
	require.Same(t, newM, merged.Metrics, "freshly-collected metrics take precedence")
}

// Edge case (R9): the side that survives the early return has zero connectors
// but carries metrics — those metrics must not be dropped. RED against the
// current early-return at discovery_common.go.
func TestMergeSelfManagedConnectors_OldZeroConnectorsButMetrics_Preserved(t *testing.T) {
	oldM := &ConnectClusterMetrics{}
	old := smc(nil, oldM) // zero connectors, but has metrics
	new := smc([]string{"a"}, nil)

	merged := mergeSelfManagedConnectors(new, old)

	require.Len(t, merged.Connectors, 1, "new connectors retained")
	require.NotNil(t, merged.Metrics, "metrics must survive the zero-connector early return")
	require.Same(t, oldM, merged.Metrics)
}

// Edge case (R9): the new run discovered zero connectors — old connectors and
// old metrics must both survive.
func TestMergeSelfManagedConnectors_NewZeroConnectors_KeepsOld(t *testing.T) {
	oldM := &ConnectClusterMetrics{}
	old := smc([]string{"a"}, oldM)
	new := smc(nil, nil)

	merged := mergeSelfManagedConnectors(new, old)

	require.Len(t, merged.Connectors, 1, "old connectors retained")
	require.Same(t, oldM, merged.Metrics, "old metrics retained")
}

// Edge case (R9): new run has zero connectors but freshly-collected metrics —
// the new metrics win, old connectors survive.
func TestMergeSelfManagedConnectors_NewZeroConnectorsWithMetrics_PrefersNew(t *testing.T) {
	oldM := &ConnectClusterMetrics{}
	newM := &ConnectClusterMetrics{}
	old := smc([]string{"a"}, oldM)
	new := smc(nil, newM)

	merged := mergeSelfManagedConnectors(new, old)

	require.Len(t, merged.Connectors, 1, "old connectors retained")
	require.Same(t, newM, merged.Metrics, "freshly-collected metrics take precedence even with zero new connectors")
}

// R6 (Set path): SetSelfManagedConnectors rebuilds the connectors object when a
// scan updates connectors; it must carry forward any previously-collected metrics.
// Preservation is split across this path and mergeSelfManagedConnectors — guarding
// both prevents a silent regression through the type change.
func TestSetSelfManagedConnectors_PreservesExistingMetrics(t *testing.T) {
	existing := &ConnectClusterMetrics{Metadata: ConnectMetricMetadata{MetricsSource: "jolokia"}}
	info := &KafkaAdminClientInformation{
		SelfManagedConnectors: &SelfManagedConnectors{
			Connectors: []SelfManagedConnector{{Name: "old"}},
			Metrics:    existing,
		},
	}

	info.SetSelfManagedConnectors([]SelfManagedConnector{{Name: "new"}})

	require.NotNil(t, info.SelfManagedConnectors.Metrics, "existing metrics must survive a connector update")
	require.Same(t, existing, info.SelfManagedConnectors.Metrics)
	require.Len(t, info.SelfManagedConnectors.Connectors, 1)
	require.Equal(t, "new", info.SelfManagedConnectors.Connectors[0].Name)
}
