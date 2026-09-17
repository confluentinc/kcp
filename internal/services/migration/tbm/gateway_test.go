package tbm

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/confluentinc/kcp/internal/services/gateway"
	"github.com/confluentinc/kcp/internal/services/migplan"
	"github.com/confluentinc/kcp/internal/services/migration"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	k8stypes "k8s.io/apimachinery/pkg/types"
)

// mockGatewayService implements gateway.Service using function fields for test
// control, mirroring migration's own (unexported, package-private)
// mockGatewayService — this is TBM's own copy: test
// doubles are kept per-package even where the underlying interface is
// shared.
type mockGatewayService struct {
	getGatewayYAMLFn         func(ctx context.Context, namespace, name string) ([]byte, error)
	detectCapabilityFn       func(ctx context.Context, namespace, name string, port int, fenced, switchover []byte) (gateway.Capability, error)
	waitForConfigIDFn        func(ctx context.Context, namespace, name string, opts gateway.ConfigWaitOptions) error
	checkPermissionsFn       func(ctx context.Context, verb, resource, group, namespace string) (bool, error)
	patchGatewayRouteFn      func(ctx context.Context, namespace, name string, rp gateway.RoutePatch, configID string) (string, error)
	patchGatewayConfigIDFn   func(ctx context.Context, namespace, name, configID string) (string, error)
	waitForGatewayAcceptedFn func(ctx context.Context, namespace, name string, pollInterval, timeout time.Duration) error
	getGatewayPodUIDsFn      func(ctx context.Context, namespace, name string) (map[k8stypes.UID]struct{}, error)
	getDeploymentGenFn       func(ctx context.Context, namespace, name string) (int64, error)
	waitForGatewayPodsFn     func(ctx context.Context, namespace, name string, initialPodUIDs map[k8stypes.UID]struct{}, baselineGeneration int64, pollInterval, timeout time.Duration, onProgress func(gateway.PodRolloutProgress)) error
	waitForGatewayReadyFn    func(ctx context.Context, namespace, name string, baselineGeneration int64, pollInterval, timeout time.Duration, onProgress func(gateway.GatewayReadinessProgress)) error
}

func (m *mockGatewayService) GetGatewayYAML(ctx context.Context, namespace, name string) ([]byte, error) {
	if m.getGatewayYAMLFn != nil {
		return m.getGatewayYAMLFn(ctx, namespace, name)
	}
	return nil, fmt.Errorf("mockGatewayService.GetGatewayYAML not configured")
}

// DetectCapability defaults to VerifyRollout — the mode every pre-hot-reload
// cluster gets — so tests that do not care about hot-reload keep exercising
// the rollout path.
func (m *mockGatewayService) DetectCapability(ctx context.Context, namespace, name string, port int, fenced, switchover []byte) (gateway.Capability, error) {
	if m.detectCapabilityFn != nil {
		return m.detectCapabilityFn(ctx, namespace, name, port, fenced, switchover)
	}
	return gateway.Capability{Mode: gateway.VerifyRollout}, nil
}

func (m *mockGatewayService) WaitForGatewayConfigID(ctx context.Context, namespace, name string, opts gateway.ConfigWaitOptions) error {
	if m.waitForConfigIDFn != nil {
		return m.waitForConfigIDFn(ctx, namespace, name, opts)
	}
	return nil
}

func (m *mockGatewayService) CheckPermissions(ctx context.Context, verb, resource, group, namespace string) (bool, error) {
	if m.checkPermissionsFn != nil {
		return m.checkPermissionsFn(ctx, verb, resource, group, namespace)
	}
	return true, nil
}

func (m *mockGatewayService) PatchGatewayRoute(ctx context.Context, namespace, name string, rp gateway.RoutePatch, configID string) (string, error) {
	if m.patchGatewayRouteFn != nil {
		return m.patchGatewayRouteFn(ctx, namespace, name, rp, configID)
	}
	return "", fmt.Errorf("mockGatewayService.PatchGatewayRoute not configured")
}

func (m *mockGatewayService) PatchGatewayConfigID(ctx context.Context, namespace, name, configID string) (string, error) {
	if m.patchGatewayConfigIDFn != nil {
		return m.patchGatewayConfigIDFn(ctx, namespace, name, configID)
	}
	return "", fmt.Errorf("mockGatewayService.PatchGatewayConfigID not configured")
}

func (m *mockGatewayService) WaitForGatewayAccepted(ctx context.Context, namespace, name string, pollInterval, timeout time.Duration) error {
	if m.waitForGatewayAcceptedFn != nil {
		return m.waitForGatewayAcceptedFn(ctx, namespace, name, pollInterval, timeout)
	}
	return nil
}

func (m *mockGatewayService) GetGatewayPodUIDs(ctx context.Context, namespace, name string) (map[k8stypes.UID]struct{}, error) {
	if m.getGatewayPodUIDsFn != nil {
		return m.getGatewayPodUIDsFn(ctx, namespace, name)
	}
	return nil, fmt.Errorf("mockGatewayService.GetGatewayPodUIDs not configured")
}

func (m *mockGatewayService) GetGatewayDeploymentGeneration(ctx context.Context, namespace, name string) (int64, error) {
	if m.getDeploymentGenFn != nil {
		return m.getDeploymentGenFn(ctx, namespace, name)
	}
	return 0, nil
}

func (m *mockGatewayService) WaitForGatewayPods(ctx context.Context, namespace, name string, initialPodUIDs map[k8stypes.UID]struct{}, baselineGeneration int64, pollInterval, timeout time.Duration, onProgress func(gateway.PodRolloutProgress)) error {
	if m.waitForGatewayPodsFn != nil {
		return m.waitForGatewayPodsFn(ctx, namespace, name, initialPodUIDs, baselineGeneration, pollInterval, timeout, onProgress)
	}
	return fmt.Errorf("mockGatewayService.WaitForGatewayPods not configured")
}

func (m *mockGatewayService) WaitForGatewayReady(ctx context.Context, namespace, name string, baselineGeneration int64, pollInterval, timeout time.Duration, onProgress func(gateway.GatewayReadinessProgress)) error {
	if m.waitForGatewayReadyFn != nil {
		return m.waitForGatewayReadyFn(ctx, namespace, name, baselineGeneration, pollInterval, timeout, onProgress)
	}
	return nil
}

// testGatewayYAML is a minimal dynamic-route gateway CR: one route
// ("migration-route") bound to two streaming domains via the plural
// streamingDomains form, with an initial rules subtree.
const testGatewayYAML = `apiVersion: platform.confluent.io/v1beta1
kind: Gateway
metadata:
  name: gateway-initial
  namespace: confluent
  resourceVersion: "12345"
spec:
  streamingDomains:
    - name: source
      kafkaCluster:
        name: source-cluster
    - name: target
      kafkaCluster:
        name: target-cluster
  routes:
    - name: migration-route
      endpoint: kafka-gw.example.com:9092
      streamingDomains:
        - name: source
          bootstrapServerId: sasl-scram
        - name: target
          bootstrapServerId: sasl-plain
      rules:
        routing:
          coordination:
            group: source
          default: source
`

func testTBMConfig() *migration.MigrationConfig {
	return &migration.MigrationConfig{
		MigrationId:    "tbm-1",
		CurrentState:   StateLagsOk,
		K8sNamespace:   "confluent",
		InitialCrName:  "gateway-initial",
		Route:          "migration-route",
		Topics:         []string{"t1.order"},
		GatewayYAML:    testGatewayYAML,
		FenceYAML:      "rules:\n  routing:\n    coordination:\n      group: source\n    default: source\n  fencing:\n    - topics: [\"t1.order\"]\n      blocked: true\n",
		SwitchoverYAML: "rules:\n  routing:\n    coordination:\n      group: source\n    default: source\n    conditions:\n      - topics: [\"t1.order\"]\n        streamingDomain: target\n",
	}
}

// realisticReconcileResult returns a migplan.Result whose GatewayYAML/Route/
// FenceYAML/SwitchoverYAML are mutually consistent with testGatewayYAML (same
// route name, same rules shape), so orchestrator-level tests that walk
// through the now-real fence transition succeed instead of failing on an
// empty or mismatched route. Used by orchestrator_test.go.
func realisticReconcileResult() *migplan.Result {
	return &migplan.Result{
		Route:          "migration-route",
		Topics:         []string{"t1.order"},
		GatewayYAML:    testGatewayYAML,
		FenceYAML:      "rules:\n  routing:\n    coordination:\n      group: source\n    default: source\n  fencing:\n    - topics: [\"t1.order\"]\n      blocked: true\n",
		SwitchoverYAML: "rules:\n  routing:\n    coordination:\n      group: source\n    default: source\n    conditions:\n      - topics: [\"t1.order\"]\n        streamingDomain: target\n",
	}
}

func TestTBMActions_Fence_RolloutPath_AppliesAndConfirms(t *testing.T) {
	var gotRP gateway.RoutePatch
	gw := &mockGatewayService{
		patchGatewayRouteFn: func(_ context.Context, _, _ string, rp gateway.RoutePatch, configID string) (string, error) {
			gotRP = rp
			assert.Empty(t, configID, "rollout-mode capability must not stamp a configId")
			return "", nil
		},
	}
	actions := NewTBMActions(zeroLagOffsetProvider(), zeroLagOffsetProvider(), gw, &mockClusterLinkService{})
	config := testTBMConfig()

	err := actions.Fence(context.Background(), config)
	require.NoError(t, err)
	assert.Equal(t, config.Route, gotRP.RouteName, "PatchGatewayRoute must have been called")
	assert.Equal(t, "rules", gotRP.Field)
}

func TestTBMActions_Fence_PatchesRulesFieldWithFencingBlock(t *testing.T) {
	var gotRP gateway.RoutePatch
	gw := &mockGatewayService{
		patchGatewayRouteFn: func(_ context.Context, _, _ string, rp gateway.RoutePatch, _ string) (string, error) {
			gotRP = rp
			return "", nil
		},
	}
	actions := NewTBMActions(zeroLagOffsetProvider(), zeroLagOffsetProvider(), gw, &mockClusterLinkService{})
	config := testTBMConfig()

	require.NoError(t, actions.Fence(context.Background(), config))

	assert.Equal(t, config.Route, gotRP.RouteName)
	assert.Equal(t, "rules", gotRP.Field)
	rules, ok := gotRP.Value.(map[string]any)
	require.True(t, ok, "fence patch value must be a map")
	fencing, ok := rules["fencing"].([]any)
	require.True(t, ok, "the applied route patch must carry the fencing block from config.FenceYAML")
	require.Len(t, fencing, 1)
}

func TestTBMActions_Fence_PerPodConfigIdPath_StampsConfigIdAndWaitsForIt(t *testing.T) {
	var sawConfigID string
	var waitedForID string
	gw := &mockGatewayService{
		detectCapabilityFn: func(context.Context, string, string, int, []byte, []byte) (gateway.Capability, error) {
			return gateway.Capability{Mode: gateway.VerifyPerPodConfigID, CRDSupportsConfigID: true}, nil
		},
		patchGatewayRouteFn: func(_ context.Context, _, _ string, _ gateway.RoutePatch, configID string) (string, error) {
			sawConfigID = configID
			return configID, nil
		},
		patchGatewayConfigIDFn: func(_ context.Context, _, _, configID string) (string, error) {
			return configID, nil
		},
		waitForConfigIDFn: func(_ context.Context, _, _ string, opts gateway.ConfigWaitOptions) error {
			waitedForID = opts.ConfigID
			return nil
		},
	}
	actions := NewTBMActions(zeroLagOffsetProvider(), zeroLagOffsetProvider(), gw, &mockClusterLinkService{})
	config := testTBMConfig()

	require.NoError(t, actions.Fence(context.Background(), config))
	require.NotEmpty(t, sawConfigID, "per-pod-configId capability must stamp a fresh configId on apply")
	assert.Equal(t, sawConfigID, waitedForID, "the wait must poll for the exact configId that was applied")
}

func TestTBMActions_Fence_HotReloadCheckFailure_ReturnsRemediationError(t *testing.T) {
	gw := &mockGatewayService{
		detectCapabilityFn: func(context.Context, string, string, int, []byte, []byte) (gateway.Capability, error) {
			return gateway.Capability{Mode: gateway.VerifyPerPodConfigID, CRDSupportsConfigID: true}, nil
		},
		patchGatewayConfigIDFn: func(_ context.Context, _, _, configID string) (string, error) {
			return configID, nil
		},
		waitForConfigIDFn: func(_ context.Context, _, _ string, _ gateway.ConfigWaitOptions) error {
			return fmt.Errorf("timed out waiting for pods to report the new configId")
		},
	}
	actions := NewTBMActions(zeroLagOffsetProvider(), zeroLagOffsetProvider(), gw, &mockClusterLinkService{})
	config := testTBMConfig()

	err := actions.Fence(context.Background(), config)
	require.Error(t, err, "a hot-reload check that never reaches the pods must fail Fence before anything is applied")
	assert.Contains(t, err.Error(), "hot-reload check")
}

func TestTBMActions_Fence_ApplyErrorPropagates(t *testing.T) {
	gw := &mockGatewayService{
		patchGatewayRouteFn: func(context.Context, string, string, gateway.RoutePatch, string) (string, error) {
			return "", fmt.Errorf("connection refused")
		},
	}
	actions := NewTBMActions(zeroLagOffsetProvider(), zeroLagOffsetProvider(), gw, &mockClusterLinkService{})
	config := testTBMConfig()

	err := actions.Fence(context.Background(), config)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "connection refused")
}

// TestTBMActions_Fence_NoTopicsSkipsFencing proves Fence short-circuits before
// any live gateway I/O when config.Topics is empty — the legitimate "nothing
// to migrate, not refused" outcome migplan.Reconcile returns on a steady-state
// re-run (reconcile.go: Refused() is checked first, then len(migratable)==0 is
// a separate, distinct success path). Mirrors WaitForLags's existing
// len(config.Topics)==0 guard ("No topics to check").
func TestTBMActions_Fence_NoTopicsSkipsFencing(t *testing.T) {
	gw := &mockGatewayService{
		detectCapabilityFn: func(context.Context, string, string, int, []byte, []byte) (gateway.Capability, error) {
			t.Fatal("DetectCapability must not be called when there are no topics to fence")
			return gateway.Capability{}, nil
		},
		patchGatewayRouteFn: func(context.Context, string, string, gateway.RoutePatch, string) (string, error) {
			t.Fatal("PatchGatewayRoute must not be called when there are no topics to fence")
			return "", nil
		},
	}
	actions := NewTBMActions(zeroLagOffsetProvider(), zeroLagOffsetProvider(), gw, &mockClusterLinkService{})
	config := testTBMConfig()
	config.Topics = nil
	config.FenceYAML = ""
	config.SwitchoverYAML = ""

	err := actions.Fence(context.Background(), config)
	require.NoError(t, err, "an empty topic list is a legitimate no-op, not an error")
}

// TestTBMActions_Fence_AppliesFenceYAMLFencingEntryVerbatim proves Fence never
// patches config.FenceYAML's fencing entries before applying — the
// CRD-required blocked field belongs on the artifact migplan.Reconcile
// itself produces (see migplan/reconcile.PrependFence), not something a
// consumer normalizes afterward. Runs both blocked: true and blocked: false
// to prove this is a verbatim pass-through, not a preservation of one
// specific value.
func TestTBMActions_Fence_PatchesFenceYAMLFencingEntryVerbatim(t *testing.T) {
	for _, blocked := range []bool{true, false} {
		t.Run(fmt.Sprintf("blocked=%v", blocked), func(t *testing.T) {
			var gotRP gateway.RoutePatch
			gw := &mockGatewayService{
				patchGatewayRouteFn: func(_ context.Context, _, _ string, rp gateway.RoutePatch, _ string) (string, error) {
					gotRP = rp
					return "", nil
				},
			}
			actions := NewTBMActions(zeroLagOffsetProvider(), zeroLagOffsetProvider(), gw, &mockClusterLinkService{})
			config := testTBMConfig()
			config.FenceYAML = fmt.Sprintf("rules:\n  routing:\n    coordination:\n      group: source\n    default: source\n  fencing:\n    - topics: [\"t1.order\"]\n      blocked: %v\n", blocked)

			require.NoError(t, actions.Fence(context.Background(), config))

			rules, ok := gotRP.Value.(map[string]any)
			require.True(t, ok, "fence patch value must be a map")
			fencing := rules["fencing"].([]any)
			require.Len(t, fencing, 1)
			entry := fencing[0].(map[string]any)
			assert.Equal(t, blocked, entry["blocked"], "Fence must carry the fencing entry's blocked value verbatim, never alter it")
		})
	}
}

func TestTBMActions_Fence_GatewayRejectedErrorPropagates(t *testing.T) {
	gw := &mockGatewayService{
		// The apply itself must succeed so the rejection surfaces from the
		// acceptance wait, mirroring migration's own
		// TestWorkflow_SwitchGateway_OperatorRejection_FailsWithOperatorMessage.
		patchGatewayRouteFn: func(context.Context, string, string, gateway.RoutePatch, string) (string, error) { return "", nil },
		waitForGatewayAcceptedFn: func(context.Context, string, string, time.Duration, time.Duration) error {
			return &gateway.GatewayRejectedError{Reason: "InvalidSpec", Message: "route not found"}
		},
	}
	actions := NewTBMActions(zeroLagOffsetProvider(), zeroLagOffsetProvider(), gw, &mockClusterLinkService{})
	config := testTBMConfig()

	err := actions.Fence(context.Background(), config)
	require.Error(t, err)
	var rejected *gateway.GatewayRejectedError
	assert.ErrorAs(t, err, &rejected, "a GatewayRejectedError must be unwrappable by the caller")
}

func TestTBMActions_Switch_RolloutPath_AppliesAndConfirms(t *testing.T) {
	var gotRP gateway.RoutePatch
	gw := &mockGatewayService{
		patchGatewayRouteFn: func(_ context.Context, _, _ string, rp gateway.RoutePatch, configID string) (string, error) {
			gotRP = rp
			assert.Empty(t, configID, "rollout-mode capability must not stamp a configId")
			return "", nil
		},
	}
	actions := NewTBMActions(zeroLagOffsetProvider(), zeroLagOffsetProvider(), gw, &mockClusterLinkService{})
	config := testTBMConfig()

	err := actions.Switch(context.Background(), config)
	require.NoError(t, err)
	assert.Equal(t, config.Route, gotRP.RouteName, "PatchGatewayRoute must have been called")
	assert.Equal(t, "rules", gotRP.Field)
}

func TestTBMActions_Switch_PatchesRulesFieldWithRoutingConditions(t *testing.T) {
	var gotRP gateway.RoutePatch
	gw := &mockGatewayService{
		patchGatewayRouteFn: func(_ context.Context, _, _ string, rp gateway.RoutePatch, _ string) (string, error) {
			gotRP = rp
			return "", nil
		},
	}
	actions := NewTBMActions(zeroLagOffsetProvider(), zeroLagOffsetProvider(), gw, &mockClusterLinkService{})
	config := testTBMConfig()

	require.NoError(t, actions.Switch(context.Background(), config))

	rules, ok := gotRP.Value.(map[string]any)
	require.True(t, ok, "switch patch value must be a map")
	routing := rules["routing"].(map[string]any)
	conditions, ok := routing["conditions"].([]any)
	require.True(t, ok, "the route patch must carry the routing.conditions block from config.SwitchoverYAML")
	require.Len(t, conditions, 1)
}

func TestTBMActions_Switch_PerPodConfigIdPath_StampsConfigIdAndWaitsForIt(t *testing.T) {
	var sawConfigID string
	var waitedForID string
	gw := &mockGatewayService{
		detectCapabilityFn: func(context.Context, string, string, int, []byte, []byte) (gateway.Capability, error) {
			return gateway.Capability{Mode: gateway.VerifyPerPodConfigID, CRDSupportsConfigID: true}, nil
		},
		patchGatewayRouteFn: func(_ context.Context, _, _ string, _ gateway.RoutePatch, configID string) (string, error) {
			sawConfigID = configID
			return configID, nil
		},
		patchGatewayConfigIDFn: func(_ context.Context, _, _, configID string) (string, error) {
			return configID, nil
		},
		waitForConfigIDFn: func(_ context.Context, _, _ string, opts gateway.ConfigWaitOptions) error {
			waitedForID = opts.ConfigID
			return nil
		},
	}
	actions := NewTBMActions(zeroLagOffsetProvider(), zeroLagOffsetProvider(), gw, &mockClusterLinkService{})
	config := testTBMConfig()

	require.NoError(t, actions.Switch(context.Background(), config))
	require.NotEmpty(t, sawConfigID, "per-pod-configId capability must stamp a fresh configId on apply")
	assert.Equal(t, sawConfigID, waitedForID, "the wait must poll for the exact configId that was applied")
}

func TestTBMActions_Switch_HotReloadCheckFailure_ReturnsRemediationError(t *testing.T) {
	gw := &mockGatewayService{
		detectCapabilityFn: func(context.Context, string, string, int, []byte, []byte) (gateway.Capability, error) {
			return gateway.Capability{Mode: gateway.VerifyPerPodConfigID, CRDSupportsConfigID: true}, nil
		},
		patchGatewayConfigIDFn: func(_ context.Context, _, _, configID string) (string, error) {
			return configID, nil
		},
		waitForConfigIDFn: func(_ context.Context, _, _ string, _ gateway.ConfigWaitOptions) error {
			return fmt.Errorf("timed out waiting for pods to report the new configId")
		},
	}
	actions := NewTBMActions(zeroLagOffsetProvider(), zeroLagOffsetProvider(), gw, &mockClusterLinkService{})
	config := testTBMConfig()

	err := actions.Switch(context.Background(), config)
	require.Error(t, err, "a hot-reload check that never reaches the pods must fail Switch before anything is applied")
	assert.Contains(t, err.Error(), "hot-reload check")
}

func TestTBMActions_Switch_ApplyErrorPropagates(t *testing.T) {
	gw := &mockGatewayService{
		patchGatewayRouteFn: func(context.Context, string, string, gateway.RoutePatch, string) (string, error) {
			return "", fmt.Errorf("connection refused")
		},
	}
	actions := NewTBMActions(zeroLagOffsetProvider(), zeroLagOffsetProvider(), gw, &mockClusterLinkService{})
	config := testTBMConfig()

	err := actions.Switch(context.Background(), config)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "connection refused")
}

func TestTBMActions_Switch_NoTopicsSkipsSwitch(t *testing.T) {
	gw := &mockGatewayService{
		detectCapabilityFn: func(context.Context, string, string, int, []byte, []byte) (gateway.Capability, error) {
			t.Fatal("DetectCapability must not be called when there are no topics to switch")
			return gateway.Capability{}, nil
		},
		patchGatewayRouteFn: func(context.Context, string, string, gateway.RoutePatch, string) (string, error) {
			t.Fatal("PatchGatewayRoute must not be called when there are no topics to switch")
			return "", nil
		},
	}
	actions := NewTBMActions(zeroLagOffsetProvider(), zeroLagOffsetProvider(), gw, &mockClusterLinkService{})
	config := testTBMConfig()
	config.Topics = nil
	config.FenceYAML = ""
	config.SwitchoverYAML = ""

	err := actions.Switch(context.Background(), config)
	require.NoError(t, err, "an empty topic list is a legitimate no-op, not an error")
}

func TestTBMActions_Switch_GatewayRejectedErrorPropagates(t *testing.T) {
	gw := &mockGatewayService{
		patchGatewayRouteFn: func(context.Context, string, string, gateway.RoutePatch, string) (string, error) { return "", nil },
		waitForGatewayAcceptedFn: func(context.Context, string, string, time.Duration, time.Duration) error {
			return &gateway.GatewayRejectedError{Reason: "InvalidSpec", Message: "route not found"}
		},
	}
	actions := NewTBMActions(zeroLagOffsetProvider(), zeroLagOffsetProvider(), gw, &mockClusterLinkService{})
	config := testTBMConfig()

	err := actions.Switch(context.Background(), config)
	require.Error(t, err)
	var rejected *gateway.GatewayRejectedError
	assert.ErrorAs(t, err, &rejected, "a GatewayRejectedError must be unwrappable by the caller")
}

// TestTBMActions_Switch_AppliesSwitchoverYAMLVerbatim proves Switch never
// mutates config.SwitchoverYAML before applying — there is no per-consumer
// normalization step here, unlike the historical fencing[].blocked issue
// (already fixed at the source, in migplan's PrependFence — see
// TestTBMActions_Fence_AppliesFenceYAMLFencingEntryVerbatim for the
// equivalent proof on the fence side).
func TestTBMActions_Switch_PatchesSwitchoverYAMLVerbatim(t *testing.T) {
	var gotRP gateway.RoutePatch
	gw := &mockGatewayService{
		patchGatewayRouteFn: func(_ context.Context, _, _ string, rp gateway.RoutePatch, _ string) (string, error) {
			gotRP = rp
			return "", nil
		},
	}
	actions := NewTBMActions(zeroLagOffsetProvider(), zeroLagOffsetProvider(), gw, &mockClusterLinkService{})
	config := testTBMConfig()
	config.SwitchoverYAML = "rules:\n  routing:\n    coordination:\n      group: source\n    default: source\n    conditions:\n      - topics: [\"t1.order\", \"t1.shipment\"]\n        streamingDomain: target\n"

	require.NoError(t, actions.Switch(context.Background(), config))

	rules, ok := gotRP.Value.(map[string]any)
	require.True(t, ok, "switch patch value must be a map")
	routing := rules["routing"].(map[string]any)
	conditions := routing["conditions"].([]any)
	require.Len(t, conditions, 1)
	entry := conditions[0].(map[string]any)
	topics := entry["topics"].([]any)
	assert.Equal(t, []any{"t1.order", "t1.shipment"}, topics, "Switch must apply the conditions entry's topics verbatim")
}

// TestTBMActions_EnsureGatewayCapability_MemoizedAcrossFenceAndSwitch proves
// capability resolution happens at most once per process: when Fence runs
// before Switch in the same TBMActions instance (the normal, same-invocation
// case), Switch's own call to ensureGatewayCapability must be a no-op.
func TestTBMActions_EnsureGatewayCapability_MemoizedAcrossFenceAndSwitch(t *testing.T) {
	var detectCalls int
	gw := &mockGatewayService{
		detectCapabilityFn: func(context.Context, string, string, int, []byte, []byte) (gateway.Capability, error) {
			detectCalls++
			return gateway.Capability{Mode: gateway.VerifyRollout}, nil
		},
		patchGatewayRouteFn: func(context.Context, string, string, gateway.RoutePatch, string) (string, error) { return "", nil },
	}
	actions := NewTBMActions(zeroLagOffsetProvider(), zeroLagOffsetProvider(), gw, &mockClusterLinkService{})
	config := testTBMConfig()

	require.NoError(t, actions.Fence(context.Background(), config))
	require.NoError(t, actions.Switch(context.Background(), config))

	assert.Equal(t, 1, detectCalls, "capability must be resolved once per process, not once per gateway-touching transition")
}

// TestTBMActions_Switch_ResolvesCapabilityFreshWhenFenceNeverRanThisProcess
// proves the resume-directly-at-switch case: a fresh TBMActions instance
// (as a resumed process would construct) still resolves capability itself,
// rather than silently using the unresolved zero value.
func TestTBMActions_Switch_ResolvesCapabilityFreshWhenFenceNeverRanThisProcess(t *testing.T) {
	var detectCalls int
	gw := &mockGatewayService{
		detectCapabilityFn: func(context.Context, string, string, int, []byte, []byte) (gateway.Capability, error) {
			detectCalls++
			return gateway.Capability{Mode: gateway.VerifyRollout}, nil
		},
		patchGatewayRouteFn: func(context.Context, string, string, gateway.RoutePatch, string) (string, error) { return "", nil },
	}
	actions := NewTBMActions(zeroLagOffsetProvider(), zeroLagOffsetProvider(), gw, &mockClusterLinkService{})
	config := testTBMConfig()

	require.NoError(t, actions.Switch(context.Background(), config))

	assert.Equal(t, 1, detectCalls, "Switch alone (Fence never ran this process) must still resolve capability")
}

func TestTBMActions_UnfenceGateway_PatchesRouteToCapturedSnapshotVerbatim(t *testing.T) {
	var gotRP gateway.RoutePatch
	gw := &mockGatewayService{
		patchGatewayRouteFn: func(_ context.Context, _, _ string, rp gateway.RoutePatch, configID string) (string, error) {
			gotRP = rp
			assert.Empty(t, configID, "rollout-mode capability must not stamp a configId")
			return "", nil
		},
	}
	actions := NewTBMActions(zeroLagOffsetProvider(), zeroLagOffsetProvider(), gw, &mockClusterLinkService{})
	config := testTBMConfig()

	err := actions.unfenceGateway(context.Background(), config)
	require.NoError(t, err)
	assert.Equal(t, config.Route, gotRP.RouteName, "PatchGatewayRoute must have been called")
	assert.Equal(t, "", gotRP.Field, "unfence must whole-route replace, not set a single field")

	expectedRoute, err := gateway.RouteObject([]byte(testGatewayYAML), config.Route)
	require.NoError(t, err)
	assert.Equal(t, expectedRoute, gotRP.Value, "unfence must restore config.Route to exactly its captured state in config.GatewayYAML, with nothing grafted onto it and no re-cleaning (migplan already cleans it once, centrally)")
}

func TestTBMActions_UnfenceGateway_ApplyFails_ReturnsWrappedError(t *testing.T) {
	gw := &mockGatewayService{
		patchGatewayRouteFn: func(context.Context, string, string, gateway.RoutePatch, string) (string, error) {
			return "", fmt.Errorf("the server rejected our request")
		},
	}
	actions := NewTBMActions(zeroLagOffsetProvider(), zeroLagOffsetProvider(), gw, &mockClusterLinkService{})
	config := testTBMConfig()

	err := actions.unfenceGateway(context.Background(), config)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "the server rejected our request")
}

func TestTBMActions_UnfenceGateway_OperatorRejection_FailsWithOperatorMessage(t *testing.T) {
	gw := &mockGatewayService{
		patchGatewayRouteFn: func(context.Context, string, string, gateway.RoutePatch, string) (string, error) { return "", nil },
		waitForGatewayAcceptedFn: func(context.Context, string, string, time.Duration, time.Duration) error {
			return &gateway.GatewayRejectedError{Reason: "InvalidSpec", Message: "spec.routes[0] references an unknown streamingDomain"}
		},
	}
	actions := NewTBMActions(zeroLagOffsetProvider(), zeroLagOffsetProvider(), gw, &mockClusterLinkService{})
	config := testTBMConfig()

	err := actions.unfenceGateway(context.Background(), config)
	require.Error(t, err)
	var rejected *gateway.GatewayRejectedError
	require.ErrorAs(t, err, &rejected)
}

// TestResolveGatewayCapability_NonDefaultPort_ReachesDetectCapability proves
// resolveGatewayCapability passes config.GatewayConfigPort — not a hard-coded
// constant — through to DetectCapability, mirroring
// migration.ResolveGatewayCapability's own port-settling behavior (see
// internal/services/migration/workflow.go's ResolveGatewayCapability).
// mockGatewayService already supports overriding DetectCapability via its
// detectCapabilityFn function field, so no new stub type is needed here.
func TestResolveGatewayCapability_NonDefaultPort_ReachesDetectCapability(t *testing.T) {
	var sawPort int
	gw := &mockGatewayService{
		detectCapabilityFn: func(_ context.Context, _, _ string, port int, _, _ []byte) (gateway.Capability, error) {
			sawPort = port
			return gateway.Capability{Mode: gateway.VerifyRollout}, nil
		},
	}
	actions := NewTBMActions(zeroLagOffsetProvider(), zeroLagOffsetProvider(), gw, &mockClusterLinkService{})
	config := testTBMConfig()
	config.GatewayConfigPort = 9999

	require.NoError(t, actions.ensureGatewayCapability(context.Background(), config))
	assert.Equal(t, 9999, sawPort)
}

// TestResolveGatewayCapability_ZeroPort_DefaultsTo9180 proves an unset
// (zero-value) config.GatewayConfigPort still settles onto
// gateway.DefaultGatewayConfigPort before the capability probe, exactly like
// migration.ResolveGatewayCapability.
func TestResolveGatewayCapability_ZeroPort_DefaultsTo9180(t *testing.T) {
	var sawPort int
	gw := &mockGatewayService{
		detectCapabilityFn: func(_ context.Context, _, _ string, port int, _, _ []byte) (gateway.Capability, error) {
			sawPort = port
			return gateway.Capability{Mode: gateway.VerifyRollout}, nil
		},
	}
	actions := NewTBMActions(zeroLagOffsetProvider(), zeroLagOffsetProvider(), gw, &mockClusterLinkService{})
	config := testTBMConfig()
	config.GatewayConfigPort = 0

	require.NoError(t, actions.ensureGatewayCapability(context.Background(), config))
	assert.Equal(t, gateway.DefaultGatewayConfigPort, sawPort)
	assert.Equal(t, gateway.DefaultGatewayConfigPort, config.GatewayConfigPort, "resolveGatewayCapability must settle the default onto config itself, not just pass it to DetectCapability")
}
