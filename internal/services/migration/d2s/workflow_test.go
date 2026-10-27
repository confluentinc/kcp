package d2s

import (
	"context"
	"testing"

	"github.com/confluentinc/kcp/internal/services/migplan"
	"github.com/confluentinc/kcp/internal/services/migration"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInitialize_CopiesTheConversionArtifactsOntoConfig(t *testing.T) {
	a := gatewayActions(&mockGatewayService{})
	config := &migration.MigrationConfig{Topics: []string{"stale"}, MigrateTopics: []string{"stale"}, AwaitStopped: []string{"stale"}}
	res := convertResult()
	res.FencedAtStart = true

	require.NoError(t, a.Initialize(context.Background(), config, res))

	assert.Equal(t, res.Route, config.Route)
	assert.Equal(t, res.GatewayYAML, config.GatewayYAML)
	assert.Equal(t, res.FenceYAML, config.FenceYAML)
	assert.Equal(t, res.SwitchoverYAML, config.SwitchoverYAML)
	assert.Equal(t, res.RollbackFenceYAML, config.RollbackFenceYAML)
	assert.True(t, config.RollbackAllowed)
	assert.True(t, config.FencedAtStart)
	assert.Nil(t, config.Topics, "a conversion promotes no topics")
	assert.Nil(t, config.MigrateTopics)
	assert.Nil(t, config.AwaitStopped)
}

func TestInitialize_RefusedPlanFailsWithReasonsAndLeavesConfigAlone(t *testing.T) {
	config := &migration.MigrationConfig{}
	res := &migplan.Result{Mode: "convert", Refused: true, Reasons: []string{"orders: its mirror is ACTIVE, not promoted"}}

	err := gatewayActions(&mockGatewayService{}).Initialize(context.Background(), config, res)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "orders: its mirror is ACTIVE, not promoted")
	assert.Empty(t, config.FenceYAML)
}

func TestInitialize_RejectsAPlanThatIsNotAConversion(t *testing.T) {
	res := convertResult()
	res.Mode = "dynamic"

	err := gatewayActions(&mockGatewayService{}).Initialize(context.Background(), &migration.MigrationConfig{}, res)

	require.Error(t, err)
	assert.Contains(t, err.Error(), `"dynamic"`)
}

func TestInitialize_RejectsAConversionWithoutItsArtifacts(t *testing.T) {
	for name, edit := range map[string]func(*migplan.Result){
		"route name": func(r *migplan.Result) { r.Route = "" },
		"gateway CR": func(r *migplan.Result) { r.GatewayYAML = "" },
		"fence":      func(r *migplan.Result) { r.FenceYAML = "" },
		"switch":     func(r *migplan.Result) { r.SwitchoverYAML = "" },
		"rollback":   func(r *migplan.Result) { r.RollbackFenceYAML = "" },
	} {
		t.Run(name, func(t *testing.T) {
			res := convertResult()
			edit(res)
			err := gatewayActions(&mockGatewayService{}).Initialize(context.Background(), &migration.MigrationConfig{}, res)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "without its "+name+" artifact")
		})
	}
}

// sync_offsets never reads the source: without this run's verify_fence hand-off it must not run at all.
func TestSyncOffsets_WithoutAHandedOverSnapshotIsAnError(t *testing.T) {
	w := cleanWorld()
	a := NewD2SActions(&mockGatewayService{}, w.deps())
	a.reporter = quietReporter()

	err := a.SyncOffsets(context.Background(), testConfig(), testPolicy())

	require.ErrorIs(t, err, errNoHandoff)
	assert.Zero(t, w.dst.topicLists+w.dst.sweeps+w.dst.builds, "nothing on the destination is touched")
	assert.Zero(t, w.src.lists+w.src.fetches, "nothing on the source is read")
}
