//go:build e2e

package idempotent_fsm_e2e

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/confluentinc/kcp/internal/services/gateway"
	"github.com/stretchr/testify/require"
)

// A secretRef no Secret carries, so the operator refuses any spec using it.
const missingSecret = "no-such-secret"

// A re-run over a switch the operator rejected must not report completed. Slice tbm-topic-006..010.
func TestUnconfirmedSwitch_RerunDoesNotReportCompleted(t *testing.T) {
	e := newEnv()
	if e.mode != "static" {
		t.Skip("the rejected whole-route switch is static-only")
	}
	ctx := context.Background()
	e.resetStaticRoute(t, ctx)
	topics := e.topicRange(6, 10)
	mani := e.writeManifest(t, "unconfirmed-switch", topics)
	e.saveManifest(t, mani)

	_, err1 := e.runKCP(t, "kcp-run-1-interrupted.log", cpPromoted, "migration", "execute", "--migration-yaml", mani)
	require.Error(t, err1, "the run interrupted after promote must exit non-zero")
	e.snapshot(t, ctx, "after-interrupt", "AFTER interrupt at promoted (expect route fenced, mirrors STOPPED)", topics)

	e.applyRejectedSwitch(t, ctx)
	e.snapshot(t, ctx, "after-rejected-switch", "AFTER the rejected switch (spec switched and unfenced, pods still on the fenced config)", topics)

	out2, err2 := e.runKCP(t, "kcp-run-2-rerun.log", "", "migration", "execute", "--migration-yaml", mani)
	require.Error(t, err2, "a re-run over a gateway the operator rejected must fail")
	require.NotContains(t, out2, "Migration completed", "a re-run must not report completed while the operator rejects the switch")
}

// Stores the switched route over a missing Secret and waits for the operator to refuse it.
func (e *env) applyRejectedSwitch(t *testing.T, ctx context.Context) {
	t.Helper()
	route := e.routeObj(t, e.readCR(t, ctx))
	require.NotNil(t, route, "route %q must exist in the live CR", e.route)

	route["streamingDomain"].(map[string]any)["name"] = e.destDomain
	delete(route, "fence")

	swap := route["security"].(map[string]any)["cluster"].(map[string]any)[e.destDomain].(map[string]any)
	swap["authentication"].(map[string]any)["jaasConfigPassThrough"].(map[string]any)["secretRef"] = missingSecret

	_, err := e.svc.PatchGatewayRoute(ctx, e.namespace, e.gateway, gateway.RoutePatch{RouteName: e.route, Value: route}, "")
	require.NoError(t, err, "store the switched route")

	err = e.svc.WaitForGatewayAccepted(ctx, e.namespace, e.gateway, 2*time.Second, 3*time.Minute)
	var rejected *gateway.GatewayRejectedError
	require.True(t, errors.As(err, &rejected), "the operator must reject the missing secretRef, got: %v", err)
}
