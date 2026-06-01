package plan

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadPlanConfigEmbedded(t *testing.T) {
	cfg, err := LoadPlanConfig("")
	require.NoError(t, err)

	assert.Equal(t, ExpectedSchemaVersion, cfg.SchemaVersion)
	assert.Equal(t, 60, cfg.EnterpriseCaps.PerECKUIngressMBps)
	assert.Equal(t, 180, cfg.EnterpriseCaps.PerECKUEgressMBps)
	assert.Equal(t, 3000, cfg.EnterpriseCaps.PerECKUPartitionRate)
	assert.Equal(t, 10, cfg.EnterpriseCaps.PrivateLinkMaxECKU)
	assert.Equal(t, 32, cfg.EnterpriseCaps.PNIMaxECKU)
	assert.Equal(t, "p95", cfg.PlanInputDefaults.SizingPercentile)
	assert.InDelta(t, 0.30, cfg.PlanInputDefaults.HeadroomFraction, 0.0001)
	assert.False(t, cfg.PlanInputDefaults.CCEgressRequired)
	assert.Equal(t, 1, cfg.PlanInputDefaults.ProjectedPNIGatewayCount)
}

func TestLoadPlanConfigOverride(t *testing.T) {
	dir := t.TempDir()
	overridePath := filepath.Join(dir, "override.yaml")
	require.NoError(t, os.WriteFile(overridePath, []byte(`
plan_input_defaults:
  headroom_fraction: 0.45
`), 0o644))

	cfg, err := LoadPlanConfig(overridePath)
	require.NoError(t, err)
	assert.InDelta(t, 0.45, cfg.PlanInputDefaults.HeadroomFraction, 0.0001)
	// Untouched defaults keep embedded values.
	assert.Equal(t, "p95", cfg.PlanInputDefaults.SizingPercentile)
	assert.Equal(t, 60, cfg.EnterpriseCaps.PerECKUIngressMBps)
}

func TestLoadPlanConfigErrors(t *testing.T) {
	t.Run("malformed YAML returns useful error", func(t *testing.T) {
		dir := t.TempDir()
		bad := filepath.Join(dir, "bad.yaml")
		require.NoError(t, os.WriteFile(bad, []byte("plan_input_defaults: [oops"), 0o644))
		_, err := LoadPlanConfig(bad)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to parse plan-config override")
	})

	t.Run("missing override file is wrapped", func(t *testing.T) {
		_, err := LoadPlanConfig("/no/such/path/x.yaml")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to read plan-config override")
	})

	t.Run("schema_version mismatch rejected", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "x.yaml")
		require.NoError(t, os.WriteFile(path, []byte("schema_version: 99\n"), 0o644))
		_, err := LoadPlanConfig(path)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "schema_version 99 does not match expected 1")
	})
}
