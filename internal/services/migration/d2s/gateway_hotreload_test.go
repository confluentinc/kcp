package d2s

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/confluentinc/kcp/internal/services/gateway"
	"github.com/confluentinc/kcp/internal/services/migration/migrun"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	k8stypes "k8s.io/apimachinery/pkg/types"
)

// hotReloadWorld is a gateway double that claims hot-reload and records how d2s drives it: which configIds it
// stamps, what each WaitForGatewayConfigID call is asked to wait for, and whether the rollout (pod) path is
// touched. WaitForGatewayConfigID call 1 is the capability smoke test, call 2 onward are transitions' waits.
type hotReloadWorld struct {
	rec *patchRecorder
	svc *mockGatewayService

	detects        int
	routeConfigIDs []string
	stamped        []string
	waits          []gateway.ConfigWaitOptions
	failWaitOn     map[int]error
	podCaptures    int
	podWaits       int
}

func newHotReloadWorld() *hotReloadWorld {
	w := &hotReloadWorld{rec: &patchRecorder{}}
	w.svc = w.rec.service()
	w.svc.DetectCapabilityFn = func(context.Context, string, string, int, []byte, []byte) (gateway.Capability, error) {
		w.detects++
		return gateway.Capability{Mode: gateway.VerifyPerPodConfigID, CRDSupportsConfigID: true, HotReloadEnabled: true}, nil
	}
	w.svc.PatchGatewayRouteFn = func(_ context.Context, _, _ string, rp gateway.RoutePatch, configID string) (string, error) {
		w.rec.mu.Lock()
		defer w.rec.mu.Unlock()
		w.rec.patches = append(w.rec.patches, rp)
		w.routeConfigIDs = append(w.routeConfigIDs, configID)
		return configID, nil
	}
	w.svc.PatchGatewayConfigIDFn = func(_ context.Context, _, _, configID string) (string, error) {
		w.stamped = append(w.stamped, configID)
		return configID, nil
	}
	w.svc.WaitForConfigIDFn = func(_ context.Context, _, _ string, opts gateway.ConfigWaitOptions) error {
		w.waits = append(w.waits, opts)
		return w.failWaitOn[len(w.waits)]
	}
	w.svc.GetGatewayPodUIDsFn = func(context.Context, string, string) (map[k8stypes.UID]struct{}, error) {
		w.podCaptures++
		return map[k8stypes.UID]struct{}{}, nil
	}
	w.svc.WaitForGatewayPodsFn = func(context.Context, string, string, map[k8stypes.UID]struct{}, int64, time.Duration, time.Duration, func(gateway.PodRolloutProgress)) error {
		w.podWaits++
		return nil
	}
	return w
}

func hotReloadActions(w *hotReloadWorld) *D2SActions {
	return gatewayActions(w.svc, gateway.WithTimeouts(7*time.Minute, 3*time.Minute))
}

// assertStampedAndWaited checks the transition stamped one configId on the route and waited for exactly that id
// with the configured timeouts, without touching the rollout path.
func assertStampedAndWaited(t *testing.T, w *hotReloadWorld) {
	t.Helper()
	require.Len(t, w.routeConfigIDs, 1)
	require.NotEmpty(t, w.routeConfigIDs[0])
	require.Len(t, w.waits, 2) // smoke test, then the transition
	assert.Equal(t, w.routeConfigIDs[0], w.waits[1].ConfigID)
	require.Len(t, w.stamped, 1)
	assert.NotEqual(t, w.stamped[0], w.routeConfigIDs[0], "the smoke test used its own id")
	assert.Equal(t, 7*time.Minute, w.waits[1].RollTimeout)
	assert.Equal(t, 3*time.Minute, w.waits[1].HotReloadTimeout)
	assert.Equal(t, gateway.DefaultGatewayConfigPort, w.waits[1].Port)
	assert.Equal(t, 0, w.podCaptures)
	assert.Equal(t, 0, w.podWaits)
}

func TestD2SFence_HotReload_StampsAndWaitsForTheConfigID(t *testing.T) {
	w := newHotReloadWorld()
	a := hotReloadActions(w)

	require.NoError(t, a.Fence(context.Background(), testConfig()))

	assertStampedAndWaited(t, w)
}

func TestD2SSwitch_HotReload_StampsAndWaitsForTheConfigID(t *testing.T) {
	w := newHotReloadWorld()
	a := hotReloadActions(w)

	require.NoError(t, a.Switch(context.Background(), testConfig()))

	assertStampedAndWaited(t, w)
	assert.Equal(t, []string{""}, w.rec.fields(), "the switch replaces the whole route")
}

func TestD2SUnfence_HotReload_WaitsForTheConfigIDNotPods(t *testing.T) {
	w := newHotReloadWorld()
	a := hotReloadActions(w)

	require.NoError(t, a.unfenceGateway(context.Background(), testConfig()))

	require.Len(t, w.routeConfigIDs, 1)
	require.Len(t, w.waits, 2)
	assert.Equal(t, w.routeConfigIDs[0], w.waits[1].ConfigID)
	assert.Equal(t, 0, w.podCaptures)
	assert.Equal(t, 0, w.podWaits)
}

func TestD2SEnsureGatewayCapability_HotReload_SmokeTestsOncePerProcess(t *testing.T) {
	w := newHotReloadWorld()
	a := hotReloadActions(w)
	cfg := testConfig()

	require.NoError(t, a.Fence(context.Background(), cfg))
	require.NoError(t, a.Switch(context.Background(), cfg))

	assert.Equal(t, 1, w.detects)
	assert.Len(t, w.stamped, 1)
	assert.Len(t, w.waits, 3) // smoke, fence, switch
}

func TestD2SFence_HotReload_SmokeTestFailureWritesNoFence(t *testing.T) {
	w := newHotReloadWorld()
	w.failWaitOn = map[int]error{1: errors.New("config watcher not running")}
	a := hotReloadActions(w)

	err := a.Fence(context.Background(), testConfig())

	require.Error(t, err)
	assert.NotErrorIs(t, err, migrun.ErrFenceUnconfirmed)
	assert.Empty(t, w.rec.patches)
	assert.Empty(t, w.routeConfigIDs)
}

func TestD2SFence_HotReload_PodsNeverReportingTheConfigIDIsFenceUnconfirmed(t *testing.T) {
	w := newHotReloadWorld()
	w.failWaitOn = map[int]error{2: errors.New("pods never converged")}
	a := hotReloadActions(w)

	err := a.Fence(context.Background(), testConfig())

	require.Error(t, err)
	assert.ErrorIs(t, err, migrun.ErrFenceUnconfirmed)
	assert.Equal(t, []string{"rules"}, w.rec.fields())
}

func TestD2SSwitch_HotReload_PodsNeverReportingTheConfigIDIsSwitchUnconfirmed(t *testing.T) {
	w := newHotReloadWorld()
	w.failWaitOn = map[int]error{2: errors.New("pods never converged")}
	a := hotReloadActions(w)

	err := a.Switch(context.Background(), testConfig())

	require.Error(t, err)
	assert.ErrorIs(t, err, migrun.ErrSwitchUnconfirmed)
	assert.Equal(t, []string{""}, w.rec.fields())
}
