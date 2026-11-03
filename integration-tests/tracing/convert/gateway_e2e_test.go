//go:build e2e

package tracing_convert_e2e

import (
	"context"
	"testing"
	"time"

	"github.com/confluentinc/kcp/internal/services/gateway"
	"github.com/stretchr/testify/require"
)

// readCR fetches the live Gateway CR as YAML.
func (e *env) readCR(t *testing.T, ctx context.Context) []byte {
	t.Helper()
	raw, err := e.svc.GetGatewayYAML(ctx, e.namespace, e.gateway)
	require.NoError(t, err, "read the gateway CR")
	return raw
}

// liveRoute is the suite's route as the live CR holds it.
func (e *env) liveRoute(t *testing.T, ctx context.Context) map[string]any {
	t.Helper()
	r := FindRoute(e.readCR(t, ctx), e.route)
	require.NotNilf(t, r, "route %q must exist in the gateway CR", e.route)
	return r
}

// replaceRoute writes route as the whole of the suite's route with a fresh
// configId, then waits for the operator to accept it and for every gateway pod
// to report that configId, whether CFK hot-reloads the pods or rolls them (a
// roll is detected from the Deployment generation and gets the longer budget).
func (e *env) replaceRoute(t *testing.T, ctx context.Context, route map[string]any) {
	t.Helper()
	id, err := gateway.NewConfigID()
	require.NoError(t, err)
	baseline, err := e.svc.GetGatewayDeploymentGeneration(ctx, e.namespace, e.gateway)
	require.NoError(t, err, "read the gateway deployment generation")
	stored, err := e.svc.PatchGatewayRoute(ctx, e.namespace, e.gateway, gateway.RoutePatch{RouteName: e.route, Value: route}, id, 0)
	require.NoError(t, err, "patch route %q", e.route)
	require.Equal(t, id, stored.ConfigID, "the gateway CR must store the configId sent")
	require.NoError(t, e.svc.WaitForGatewayAccepted(ctx, e.namespace, e.gateway, 2*time.Second, 3*time.Minute),
		"the operator must accept the patched route")
	require.NoError(t, e.svc.WaitForGatewayConfigID(ctx, e.namespace, e.gateway, gateway.ConfigWaitOptions{
		ConfigID: id, PollInterval: time.Second, HotReloadTimeout: 2 * time.Minute,
		BaselineDeploymentGeneration: baseline, RollTimeout: 10 * time.Minute,
	}), "every gateway pod must hot-reload configId %s", id)
}

// requirePostTBM asserts the route is the unfenced post-TBM route over every
// link topic.
func (e *env) requirePostTBM(t *testing.T, ctx context.Context, why string) {
	t.Helper()
	p := DynamicProblems(e.liveRoute(t, ctx), e.domains, e.linkTopics(t, ctx), false)
	require.Emptyf(t, p, "%s: the route must be the unfenced post-TBM route", why)
}

// requireConvertedRoute asserts the route is a finished conversion with its
// security unchanged from before.
func (e *env) requireConvertedRoute(t *testing.T, ctx context.Context, securityBefore any) {
	t.Helper()
	require.Empty(t, ConvertedProblems(e.liveRoute(t, ctx), e.domains, securityBefore),
		"the route must be static on the destination with no rules, no streamingDomains and the same security")
}
