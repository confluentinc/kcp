package d2s

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/confluentinc/kcp/internal/services/gateway"
	"github.com/confluentinc/kcp/internal/services/migration/migrun"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The goldens are shared with the gateway package's RouteChange tests and were
// generated from this d2s's derivations before it moved onto RouteChange.
const routeChangeGoldenDir = "../../gateway/testdata/route_change/d2s"

func routeChangeGolden(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(routeChangeGoldenDir, name))
	require.NoError(t, err)
	return string(b)
}

func routeChangeGoldenConfig(t *testing.T) *migrun.MigrationConfig {
	t.Helper()
	return &migrun.MigrationConfig{
		GatewayYAML:       routeChangeGolden(t, "cr.yaml"),
		Route:             "migration-route",
		FenceYAML:         routeChangeGolden(t, "fence.yaml"),
		SwitchoverYAML:    routeChangeGolden(t, "switch.yaml"),
		RollbackFenceYAML: routeChangeGolden(t, "rollback.yaml"),
	}
}

func TestRouteChangeGoldens_Patches(t *testing.T) {
	config := routeChangeGoldenConfig(t)
	for _, tc := range []struct {
		name   string
		derive func(*migrun.MigrationConfig) (gateway.RoutePatch, error)
		golden string
	}{
		{"fence", deriveFenceRoutePatch, "fence_patch.golden"},
		{"switch", deriveSwitchRoutePatch, "switch_patch.golden"},
		{"unfence", deriveUnfenceRoutePatch, "unfence_patch.golden"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rp, err := tc.derive(config)
			require.NoError(t, err)
			var want struct {
				RouteName string `json:"route_name"`
				Field     string `json:"field"`
				Value     any    `json:"value"`
			}
			require.NoError(t, json.Unmarshal([]byte(routeChangeGolden(t, tc.golden)), &want))
			assert.Equal(t, want.RouteName, rp.RouteName)
			assert.Equal(t, want.Field, rp.Field)
			assert.Equal(t, want.Value, rp.Value)
		})
	}
}

// probeCRs builds the capability probe CRs the gateway Transitioner derives
// from the FSM's target, the same RouteProbe call over the same plain data.
func probeCRs(a *D2SActions, config *migrun.MigrationConfig) (fenced, switched []byte, err error) {
	tg := a.target(config)
	return gateway.RouteProbe(tg.GatewayCR, tg.Route, tg.Fence, tg.Switch)
}

func TestRouteChangeGoldens_ProbeCRs(t *testing.T) {
	config := routeChangeGoldenConfig(t)
	fenced, switched, err := probeCRs(&D2SActions{}, config)
	require.NoError(t, err)
	assert.Equal(t, routeChangeGolden(t, "fenced_cr.yaml"), string(fenced))
	assert.Equal(t, routeChangeGolden(t, "switched_cr.yaml"), string(switched))
}

func TestRouteChangeGoldens_ProbeErrorsKeepWrappers(t *testing.T) {
	config := routeChangeGoldenConfig(t)
	config.GatewayYAML = "{{ not yaml"
	_, _, err := probeCRs(&D2SActions{}, config)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to derive fenced gateway CR: failed to parse gateway CR YAML")
}
