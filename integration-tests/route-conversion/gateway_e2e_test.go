//go:build e2e

package routeconversion

import (
	"context"
	"testing"
	"time"

	"github.com/confluentinc/kcp/internal/services/gateway"
	"github.com/goccy/go-yaml"
	"github.com/stretchr/testify/require"
)

// readCR fetches the live Gateway CR as YAML.
func (e *env) readCR(t *testing.T, ctx context.Context) []byte {
	t.Helper()
	raw, err := e.svc.GetGatewayYAML(ctx, e.namespace, e.gateway)
	require.NoError(t, err, "read the gateway CR")
	return raw
}

// readSpec is the live Gateway CR's spec, re-encoded: what a nothing-to-do run
// must leave untouched (status and metadata move on their own).
func (e *env) readSpec(t *testing.T, ctx context.Context) string {
	t.Helper()
	var obj map[string]any
	require.NoError(t, yaml.Unmarshal(e.readCR(t, ctx), &obj))
	spec, err := yaml.Marshal(obj["spec"])
	require.NoError(t, err)
	return string(spec)
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
	stored, err := e.svc.PatchGatewayRoute(ctx, e.namespace, e.gateway, gateway.RoutePatch{RouteName: e.route, Value: route}, id)
	require.NoError(t, err, "patch route %q", e.route)
	require.Equal(t, id, stored, "the gateway CR must store the configId sent")
	require.NoError(t, e.svc.WaitForGatewayAccepted(ctx, e.namespace, e.gateway, 2*time.Second, 3*time.Minute),
		"the operator must accept the patched route")
	require.NoError(t, e.svc.WaitForGatewayConfigID(ctx, e.namespace, e.gateway, gateway.ConfigWaitOptions{
		ConfigID: id, PollInterval: time.Second, HotReloadTimeout: 2 * time.Minute,
		BaselineDeploymentGeneration: baseline, RollTimeout: 10 * time.Minute,
	}), "every gateway pod must hot-reload configId %s", id)
}

// fenceByHand puts kcp's conversion fence on the live route, as a run
// interrupted after its fence step would leave it.
func (e *env) fenceByHand(t *testing.T, ctx context.Context) {
	t.Helper()
	e.replaceRoute(t, ctx, WithConvertFence(e.liveRoute(t, ctx)))
	require.True(t, HasConvertFence(e.liveRoute(t, ctx)), "the route must now carry kcp's conversion fence")
}

// requirePostTBM asserts the route is the unfenced post-TBM route over every
// link topic.
func (e *env) requirePostTBM(t *testing.T, ctx context.Context, why string) {
	t.Helper()
	p := DynamicProblems(e.liveRoute(t, ctx), e.domains, e.linkTopics(t, ctx), false)
	require.Emptyf(t, p, "%s: the route must be the unfenced post-TBM route", why)
}

// requireFenced asserts the route is the post-TBM route with kcp's conversion
// fence (and only it) in rules.fencing.
func (e *env) requireFenced(t *testing.T, ctx context.Context, why string) {
	t.Helper()
	p := DynamicProblems(e.liveRoute(t, ctx), e.domains, e.linkTopics(t, ctx), true)
	require.Emptyf(t, p, "%s: the route must be the post-TBM route carrying kcp's conversion fence", why)
}

// requireConvertedRoute asserts the route is a finished conversion with its
// security unchanged from before.
func (e *env) requireConvertedRoute(t *testing.T, ctx context.Context, securityBefore any) {
	t.Helper()
	require.Empty(t, ConvertedProblems(e.liveRoute(t, ctx), e.domains, securityBefore),
		"the route must be static on the destination with no rules, no streamingDomains and the same security")
}
