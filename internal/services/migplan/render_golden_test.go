package migplan

import (
	"bytes"
	"context"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/confluentinc/kcp/internal/services/migplan/reconcile"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/fatih/color"
	"github.com/stretchr/testify/require"
)

var update = flag.Bool("update", false, "rewrite testdata/render goldens")

// TestRenderReport_Golden pins each existing mode's rendered plan byte for byte.
// A diff is a change to what users see: review it, then rerun with -update.
func TestRenderReport_Golden(t *testing.T) {
	prev := color.NoColor
	color.NoColor = true
	t.Cleanup(func() { color.NoColor = prev })

	activeLink := func() *fakeLink {
		return &fakeLink{ls: &LinkStatus{Mirrors: map[string]reconcile.MirrorState{"orders": reconcile.MirrorActive}}}
	}
	for _, tc := range []struct {
		mode   string
		engine *ReconciliationEngine
		in     reconcile.ReconcileInput
	}{
		{
			mode: "static",
			engine: NewReconciliationEngine(&fakeGateway{gw: staticGatewayConfig()},
				&fakeLister{topics: []string{"orders"}}, &fakeLister{topics: []string{"orders"}}, activeLink(), &fakeSecretChecker{}),
			in: reconcile.ReconcileInput{Topics: []string{"orders"}, Route: "migration-route", TargetDomain: "cc", Type: reconcile.TypeStatic},
		},
		{
			mode: "dynamic",
			engine: NewReconciliationEngine(&fakeGateway{gw: dynGatewayConfig()},
				&fakeLister{topics: []string{"orders"}}, &fakeLister{topics: []string{"orders"}}, activeLink(), &fakeSecretChecker{}),
			in: reconcile.ReconcileInput{Topics: []string{"orders"}, Route: "migration-route", TargetDomain: "cc", Type: reconcile.TypeDynamic},
		},
		{
			mode: "convert",
			engine: convertEngine(
				&fakeGroupLister{groups: []types.ConsumerGroupListing{{GroupID: "orders-app"}}, tracked: map[string][]string{"orders-app": {"orders"}}},
				&fakeGroupLister{}),
			in: convertIn,
		},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			plan, err := tc.engine.Run(context.Background(), tc.in)
			require.NoError(t, err)

			var buf bytes.Buffer
			RenderReport(&buf, plan.Report, RenderView{
				Route:        tc.in.Route,
				TargetDomain: tc.in.TargetDomain,
				ArtifactNote: "plan ready",
				Conversion:   tc.in.Type == reconcile.TypeDynamicToStatic,
			})

			path := filepath.Join("testdata", "render", tc.mode+".golden")
			if *update {
				require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
				require.NoError(t, os.WriteFile(path, buf.Bytes(), 0o644))
				return
			}
			want, err := os.ReadFile(path)
			require.NoError(t, err, "missing golden; generate it with -update")
			require.Equal(t, string(want), buf.String())
		})
	}
}
