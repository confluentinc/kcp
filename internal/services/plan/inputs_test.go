package plan

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/confluentinc/kcp/internal/types"
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
		assert.InDelta(t, 0.80, resolved.PrivateLinkSafetyThreshold, 0.0001)
		assert.InDelta(t, 2.0, resolved.SpikyWorkloadRatio, 0.0001)
		assert.Equal(t, "99.9", resolved.SLATarget)
	})

	t.Run("customer values override defaults", func(t *testing.T) {
		hf := 0.45
		sla := "99.95"
		in := &types.PlanInputs{HeadroomFraction: &hf, SLATarget: &sla}
		resolved := ResolvePlanInputs(in, cfg)
		assert.InDelta(t, 0.45, resolved.HeadroomFraction, 0.0001)
		assert.Equal(t, "99.95", resolved.SLATarget)
		// Untouched defaults persist.
		assert.Equal(t, "p95", resolved.SizingPercentile)
		// Raw preserves the pointer.
		assert.Same(t, in, resolved.Raw)
	})
}
