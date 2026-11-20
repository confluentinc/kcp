package gateway

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The goldens under testdata/route_change were generated from the FSMs' own
// derivations before they moved onto RouteChange: aao/ from
// internal/services/migration (static route, fence + whole-route switch),
// tbm/ from migration/tbm (dynamic route, rules throughout) and d2s/ from
// migration/d2s (rules fence, whole-route switch). Each directory holds the
// inputs (cr/fence/switch/rollback.yaml) and the FSM's outputs.

func readGolden(t *testing.T, fsm, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "route_change", fsm, name))
	require.NoError(t, err)
	return b
}

type goldenPatch struct {
	RouteName string `json:"route_name"`
	Field     string `json:"field"`
	Value     any    `json:"value"`
}

func readGoldenPatch(t *testing.T, fsm, name string) goldenPatch {
	t.Helper()
	var p goldenPatch
	require.NoError(t, json.Unmarshal(readGolden(t, fsm, name), &p))
	return p
}

// fsmChanges is how each FSM maps its artifacts onto RouteChange.
var fsmChanges = map[string]struct{ fence, sw, unfence RouteEdit }{
	"aao": {EditRouteFence, EditRoute, EditRoute},
	"tbm": {EditRouteRules, EditRouteRules, EditRouteRules},
	"d2s": {EditRouteRules, EditRoute, EditRouteRules},
}

func TestRouteChange_Patch(t *testing.T) {
	for fsm, edits := range fsmChanges {
		for _, tc := range []struct {
			name     string
			edit     RouteEdit
			artifact string
			golden   string
		}{
			{"fence", edits.fence, "fence.yaml", "fence_patch.golden"},
			{"switch", edits.sw, "switch.yaml", "switch_patch.golden"},
			{"unfence", edits.unfence, "rollback.yaml", "unfence_patch.golden"},
		} {
			t.Run(fsm+"/"+tc.name, func(t *testing.T) {
				rp, err := RouteChange{Edit: tc.edit, Artifact: readGolden(t, fsm, tc.artifact)}.Patch("migration-route")
				require.NoError(t, err)
				want := readGoldenPatch(t, fsm, tc.golden)
				assert.Equal(t, want.RouteName, rp.RouteName)
				assert.Equal(t, want.Field, rp.Field)
				assert.Equal(t, want.Value, rp.Value)
			})
		}
	}

	t.Run("field and key per edit", func(t *testing.T) {
		for _, tc := range []struct {
			edit       RouteEdit
			field, key string
			artifact   string
		}{
			{EditRouteFence, "fence", "fence", "fence:\n  scope: ALL\n"},
			{EditRouteRules, "rules", "rules", "rules:\n  routing:\n    default: source\n"},
			{EditRoute, "", "route", "route:\n  name: r\n"},
		} {
			rp, err := RouteChange{Edit: tc.edit, Artifact: []byte(tc.artifact)}.Patch("r")
			require.NoError(t, err)
			want, err := FragmentValue([]byte(tc.artifact), tc.key)
			require.NoError(t, err)
			assert.Equal(t, "r", rp.RouteName)
			assert.Equal(t, tc.field, rp.Field)
			assert.Equal(t, want, rp.Value)
		}
	})

	t.Run("missing key returns the FragmentValue error", func(t *testing.T) {
		for _, edit := range []RouteEdit{EditRouteFence, EditRouteRules, EditRoute} {
			_, err := RouteChange{Edit: edit, Artifact: []byte("other:\n  a: 1\n")}.Patch("r")
			require.Error(t, err)
			assert.Contains(t, err.Error(), "gateway fragment has no top-level")
		}
	})
}

func TestRouteChange_ApplyTo(t *testing.T) {
	for fsm, edits := range fsmChanges {
		cr := readGolden(t, fsm, "cr.yaml")
		t.Run(fsm+"/fenced", func(t *testing.T) {
			got, err := RouteChange{Edit: edits.fence, Artifact: readGolden(t, fsm, "fence.yaml")}.ApplyTo(cr, "migration-route")
			require.NoError(t, err)
			assert.Equal(t, string(readGolden(t, fsm, "fenced_cr.yaml")), string(got))
		})
		t.Run(fsm+"/switched", func(t *testing.T) {
			got, err := RouteChange{Edit: edits.sw, Artifact: readGolden(t, fsm, "switch.yaml")}.ApplyTo(cr, "migration-route")
			require.NoError(t, err)
			assert.Equal(t, string(readGolden(t, fsm, "switched_cr.yaml")), string(got))
		})
	}

	t.Run("bad CR YAML returns the parse error", func(t *testing.T) {
		_, err := RouteChange{Edit: EditRoute, Artifact: []byte("route:\n  name: r\n")}.ApplyTo([]byte("{{ not yaml"), "r")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to parse gateway CR YAML")
	})
}

func TestRouteProbe(t *testing.T) {
	cr := readGolden(t, "d2s", "cr.yaml")
	fenced := RouteChange{Edit: EditRouteRules, Artifact: readGolden(t, "d2s", "fence.yaml")}
	switched := RouteChange{Edit: EditRoute, Artifact: readGolden(t, "d2s", "switch.yaml")}

	t.Run("builds both CRs with ApplyTo", func(t *testing.T) {
		f, s, err := RouteProbe(cr, "migration-route", fenced, switched)()
		require.NoError(t, err)
		wantF, err := fenced.ApplyTo(cr, "migration-route")
		require.NoError(t, err)
		wantS, err := switched.ApplyTo(cr, "migration-route")
		require.NoError(t, err)
		assert.Equal(t, string(wantF), string(f))
		assert.Equal(t, string(wantS), string(s))
	})

	t.Run("bad CR returns the error", func(t *testing.T) {
		_, _, err := RouteProbe([]byte("{{ not yaml"), "migration-route", fenced, switched)()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to parse gateway CR YAML")
	})
}
