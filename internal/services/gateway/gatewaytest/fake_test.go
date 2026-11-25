package gatewaytest

import (
	"context"
	"testing"

	"github.com/confluentinc/kcp/internal/services/gateway"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	k8stypes "k8s.io/apimachinery/pkg/types"
)

var _ gateway.TransitionClient = (*Fake)(nil)

func TestFakeDefaults(t *testing.T) {
	f := &Fake{}
	ctx := context.Background()

	cap, err := f.DetectCapability(ctx, "ns", "gw", 0, nil, nil)
	require.NoError(t, err)
	assert.Equal(t, gateway.VerifyRollout, cap.Mode)

	uids, err := f.GetGatewayPodUIDs(ctx, "ns", "gw")
	require.NoError(t, err)
	assert.Equal(t, map[k8stypes.UID]struct{}{"default-pod": {}}, uids)

	depGen, err := f.GetGatewayDeploymentGeneration(ctx, "ns", "gw")
	require.NoError(t, err)
	assert.Equal(t, int64(0), depGen)

	gen, err := f.GetGatewayGeneration(ctx, "ns", "gw")
	require.NoError(t, err)
	assert.Equal(t, int64(0), gen)

	assert.NoError(t, f.WaitForGatewayAccepted(ctx, "ns", "gw", 0, 0))
	assert.NoError(t, f.WaitForGatewayConfigID(ctx, "ns", "gw", gateway.ConfigWaitOptions{}))
	assert.NoError(t, f.WaitForGatewayPods(ctx, "ns", "gw", nil, 0, 0, 0, nil))
}

func TestFakePatchRoutePrecedence(t *testing.T) {
	f := &Fake{}
	f.PatchGatewayRouteFn = func(context.Context, string, string, gateway.RoutePatch, string) (string, error) {
		t.Fatal("PatchGatewayRouteFn must not run when PatchRouteGenFn is set")
		return "", nil
	}
	f.PatchRouteGenFn = func(_ context.Context, _, _ string, _ gateway.RoutePatch, _ string, expected int64) (gateway.PatchResult, error) {
		return gateway.PatchResult{ConfigID: "from-gen-fn"}, nil
	}

	res, err := f.PatchGatewayRoute(context.Background(), "ns", "gw", gateway.RoutePatch{}, "cfg", 7)
	require.NoError(t, err)
	assert.Equal(t, "from-gen-fn", res.ConfigID)
	assert.Equal(t, []int64{7}, f.RouteExpected)
}

func TestFakeUnconfiguredPatchRoute(t *testing.T) {
	f := &Fake{}
	_, err := f.PatchGatewayRoute(context.Background(), "ns", "gw", gateway.RoutePatch{}, "cfg", 3)
	require.Error(t, err)
	assert.EqualError(t, err, "mockGatewayService.PatchGatewayRoute not configured")
	assert.Equal(t, []int64{3}, f.RouteExpected)
}
