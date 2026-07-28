package plan

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadPlanInputsEmptyPath(t *testing.T) {
	in, err := LoadPlanInputs("")
	require.NoError(t, err)
	assert.Nil(t, in)
}

func TestLoadPlanInputsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "plan-inputs.yaml")
	require.NoError(t, os.WriteFile(path, []byte(`
sla_target: "99.95"
sizing_percentile: P95
headroom_fraction: 0.45
spiky_workload_ratio: 2.5
`), 0o644))

	in, err := LoadPlanInputs(path)
	require.NoError(t, err)
	require.NotNil(t, in)
	assert.Equal(t, "99.95", *in.SLATarget)
	assert.InDelta(t, 0.45, *in.HeadroomFraction, 0.0001)
	assert.InDelta(t, 2.5, *in.SpikyWorkloadRatio, 0.0001)
}

func TestLoadPlanInputsErrors(t *testing.T) {
	t.Run("missing file is wrapped", func(t *testing.T) {
		_, err := LoadPlanInputs("/no/such/inputs.yaml")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to read plan-inputs")
	})

	t.Run("malformed YAML is wrapped", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "bad.yaml")
		require.NoError(t, os.WriteFile(path, []byte("headroom_fraction: [oops"), 0o644))
		_, err := LoadPlanInputs(path)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to parse plan-inputs")
	})
}

func TestResolvePlanInputsDefaults(t *testing.T) {
	cfg, err := LoadPlanConfig("")
	require.NoError(t, err)

	t.Run("nil inputs yields defaults from plan-config", func(t *testing.T) {
		resolved := ResolvePlanInputs(nil, cfg)
		assert.Nil(t, resolved.Raw)
		assert.Equal(t, "p95", resolved.SizingPercentile)
		assert.InDelta(t, 0.30, resolved.HeadroomFraction, 0.0001)
		assert.InDelta(t, 2.0, resolved.SpikyWorkloadRatio, 0.0001)
		assert.Equal(t, "99.9", resolved.SLATarget)
		assert.False(t, resolved.CCEgressRequired)
		assert.Equal(t, 1, resolved.ProjectedPNIGatewayCount)
	})

	t.Run("customer values override defaults", func(t *testing.T) {
		hf := 0.45
		sla := "99.95"
		egress := true
		gateways := 3
		in := &PlanInputs{
			HeadroomFraction:         &hf,
			SLATarget:                &sla,
			CCEgressRequired:         &egress,
			ProjectedPNIGatewayCount: &gateways,
		}
		resolved := ResolvePlanInputs(in, cfg)
		assert.InDelta(t, 0.45, resolved.HeadroomFraction, 0.0001)
		assert.Equal(t, "99.95", resolved.SLATarget)
		assert.True(t, resolved.CCEgressRequired)
		assert.Equal(t, 3, resolved.ProjectedPNIGatewayCount)
		// Untouched defaults persist.
		assert.Equal(t, "p95", resolved.SizingPercentile)
		// Raw preserves the pointer.
		assert.Same(t, in, resolved.Raw)
	})
}

func TestResolvePlanInputsForCluster_OverrideWins(t *testing.T) {
	cfg, err := LoadPlanConfig("")
	require.NoError(t, err)

	hf := 0.45
	sla := "99.95"
	enf := true
	in := &PlanInputs{
		HeadroomFraction: &hf, // global override
		Clusters: map[string]ClusterPlanInputs{
			"alpha": {SLATarget: &sla, EnforceSchemasAtTheBroker: &enf},
			"beta":  {}, // empty override entry — should still fall back
		},
	}

	t.Run("override wins over global; non-overridden fields keep global", func(t *testing.T) {
		resolved := ResolvePlanInputsForCluster(in, cfg, "alpha")
		assert.Equal(t, "99.95", resolved.SLATarget, "per-cluster sla_target must win")
		assert.True(t, resolved.EnforceSchemasAtTheBroker, "per-cluster enforce_schemas_at_the_broker must win")
		assert.InDelta(t, 0.45, resolved.HeadroomFraction, 0.0001, "global override fields must persist when cluster doesn't override them")
		assert.Equal(t, "p95", resolved.SizingPercentile, "untouched defaults must persist")
	})

	t.Run("empty cluster override block falls back to global resolved view", func(t *testing.T) {
		resolved := ResolvePlanInputsForCluster(in, cfg, "beta")
		assert.Equal(t, "99.9", resolved.SLATarget, "empty override entry must fall back to default")
		assert.False(t, resolved.EnforceSchemasAtTheBroker)
		assert.InDelta(t, 0.45, resolved.HeadroomFraction, 0.0001)
	})

	t.Run("missing cluster key returns global resolved view unchanged", func(t *testing.T) {
		resolved := ResolvePlanInputsForCluster(in, cfg, "no-such-cluster")
		assert.Equal(t, "99.9", resolved.SLATarget)
		assert.False(t, resolved.EnforceSchemasAtTheBroker)
		assert.InDelta(t, 0.45, resolved.HeadroomFraction, 0.0001)
	})

	t.Run("Raw preserved across per-cluster resolve", func(t *testing.T) {
		resolved := ResolvePlanInputsForCluster(in, cfg, "alpha")
		assert.Same(t, in, resolved.Raw, "Raw must still point at the global PlanInputs")
	})

	t.Run("nil PlanInputs short-circuits to global defaults", func(t *testing.T) {
		resolved := ResolvePlanInputsForCluster(nil, cfg, "alpha")
		assert.Equal(t, "99.9", resolved.SLATarget)
	})
}

func TestApplyClusterOverride_EquivalentToResolvePlanInputsForCluster(t *testing.T) {
	// Lock the hot-path optimization: applying an override on top of a
	// pre-resolved global must produce the same result as calling
	// ResolvePlanInputsForCluster from scratch. If the two ever drift,
	// Build's per-cluster loop will quietly diverge from the public API.
	cfg, err := LoadPlanConfig("")
	require.NoError(t, err)
	hf := 0.45
	sla := "99.95"
	enf := true
	in := &PlanInputs{
		HeadroomFraction: &hf,
		Clusters: map[string]ClusterPlanInputs{
			"alpha": {SLATarget: &sla, EnforceSchemasAtTheBroker: &enf},
			"beta":  {},
		},
	}
	global := ResolvePlanInputs(in, cfg)
	for _, name := range []string{"alpha", "beta", "no-such-cluster"} {
		fromGlobal := applyClusterOverride(global, in, name)
		fromScratch := ResolvePlanInputsForCluster(in, cfg, name)
		// Raw is the same pointer in both, so equality of the resolved
		// fields is what we actually care about.
		assert.Equal(t, fromScratch.SLATarget, fromGlobal.SLATarget, "cluster %q SLA", name)
		assert.Equal(t, fromScratch.HeadroomFraction, fromGlobal.HeadroomFraction, "cluster %q headroom", name)
		assert.Equal(t, fromScratch.EnforceSchemasAtTheBroker, fromGlobal.EnforceSchemasAtTheBroker, "cluster %q enforce", name)
		assert.Equal(t, fromScratch.TargetCloud, fromGlobal.TargetCloud, "cluster %q target_cloud", name)
		assert.Equal(t, fromScratch.CCEgressRequired, fromGlobal.CCEgressRequired, "cluster %q cc_egress", name)
	}
}
