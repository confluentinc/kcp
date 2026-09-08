package tbm

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/confluentinc/kcp/internal/services/gateway"
	"github.com/confluentinc/kcp/internal/services/migplan"
	"github.com/goccy/go-yaml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	k8stypes "k8s.io/apimachinery/pkg/types"
)

// yamlUnmarshalForTest is a thin wrapper so tests below don't need to import
// goccy/go-yaml under a different alias than this file already uses.
func yamlUnmarshalForTest(t *testing.T, data []byte, v any) error {
	t.Helper()
	return yaml.Unmarshal(data, v)
}

// mockGatewayService implements gateway.Service using function fields for test
// control, mirroring migration's own (unexported, package-private)
// mockGatewayService — this is TBM's own copy, not shared, since the two
// packages intentionally have no cross-imports.
type mockGatewayService struct {
	getGatewayYAMLFn           func(ctx context.Context, namespace, name string) ([]byte, error)
	detectCapabilityFn         func(ctx context.Context, namespace, name string, port int, fenced, switchover []byte) (gateway.Capability, error)
	waitForConfigIDFn          func(ctx context.Context, namespace, name string, opts gateway.ConfigWaitOptions) error
	checkRedundantAuthStagedFn func(ctx context.Context, namespace string, initial []byte, targets []gateway.RouteSwitchoverTarget) (gateway.CRValidationResult, error)
	checkPermissionsFn         func(ctx context.Context, verb, resource, group, namespace string) (bool, error)
	applyGatewayYAMLFn         func(ctx context.Context, namespace, name string, yaml []byte, configID string) (string, error)
	applyGatewayConfigIDFn     func(ctx context.Context, namespace, name, configID string) (string, error)
	waitForGatewayAcceptedFn   func(ctx context.Context, namespace, name string, pollInterval, timeout time.Duration) error
	getGatewayPodUIDsFn        func(ctx context.Context, namespace, name string) (map[k8stypes.UID]struct{}, error)
	getDeploymentGenFn         func(ctx context.Context, namespace, name string) (int64, error)
	waitForGatewayPodsFn       func(ctx context.Context, namespace, name string, initialPodUIDs map[k8stypes.UID]struct{}, baselineGeneration int64, pollInterval, timeout time.Duration, onProgress func(gateway.PodRolloutProgress)) error
	waitForGatewayReadyFn      func(ctx context.Context, namespace, name string, baselineGeneration int64, pollInterval, timeout time.Duration, onProgress func(gateway.GatewayReadinessProgress)) error
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

func (m *mockGatewayService) CheckRedundantAuthStaged(ctx context.Context, namespace string, initial []byte, targets []gateway.RouteSwitchoverTarget) (gateway.CRValidationResult, error) {
	if m.checkRedundantAuthStagedFn != nil {
		return m.checkRedundantAuthStagedFn(ctx, namespace, initial, targets)
	}
	return gateway.CRValidationResult{}, nil
}

func (m *mockGatewayService) CheckPermissions(ctx context.Context, verb, resource, group, namespace string) (bool, error) {
	if m.checkPermissionsFn != nil {
		return m.checkPermissionsFn(ctx, verb, resource, group, namespace)
	}
	return true, nil
}

func (m *mockGatewayService) ApplyGatewayYAML(ctx context.Context, namespace, name string, yamlData []byte, configID string) (string, error) {
	if m.applyGatewayYAMLFn != nil {
		return m.applyGatewayYAMLFn(ctx, namespace, name, yamlData, configID)
	}
	return "", fmt.Errorf("mockGatewayService.ApplyGatewayYAML not configured")
}

func (m *mockGatewayService) ApplyGatewayConfigID(ctx context.Context, namespace, name, configID string) (string, error) {
	if m.applyGatewayConfigIDFn != nil {
		return m.applyGatewayConfigIDFn(ctx, namespace, name, configID)
	}
	return configID, nil
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

func testTBMConfig() *TBMConfig {
	return &TBMConfig{
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
	var appliedYAML []byte
	gw := &mockGatewayService{
		applyGatewayYAMLFn: func(_ context.Context, _, _ string, yamlData []byte, configID string) (string, error) {
			appliedYAML = yamlData
			assert.Empty(t, configID, "rollout-mode capability must not stamp a configId")
			return "", nil
		},
	}
	actions := NewTBMActions(zeroLagOffsetProvider(), zeroLagOffsetProvider(), gw)
	config := testTBMConfig()

	err := actions.Fence(context.Background(), config)
	require.NoError(t, err)
	require.NotEmpty(t, appliedYAML, "ApplyGatewayYAML must have been called")

	var patched map[string]any
	require.NoError(t, yamlUnmarshalForTest(t, appliedYAML, &patched))
}

func TestTBMActions_Fence_ReplacesNamedRouteRulesInAppliedCR(t *testing.T) {
	var appliedYAML []byte
	gw := &mockGatewayService{
		applyGatewayYAMLFn: func(_ context.Context, _, _ string, yamlData []byte, _ string) (string, error) {
			appliedYAML = yamlData
			return "", nil
		},
	}
	actions := NewTBMActions(zeroLagOffsetProvider(), zeroLagOffsetProvider(), gw)
	config := testTBMConfig()

	require.NoError(t, actions.Fence(context.Background(), config))

	var obj map[string]any
	require.NoError(t, yamlUnmarshalForTest(t, appliedYAML, &obj))
	spec := obj["spec"].(map[string]any)
	routes := spec["routes"].([]any)
	route := routes[0].(map[string]any)
	rules := route["rules"].(map[string]any)
	fencing, ok := rules["fencing"].([]any)
	require.True(t, ok, "the applied CR's route must carry the fencing block from config.FenceYAML")
	require.Len(t, fencing, 1)
}

func TestTBMActions_Fence_PerPodConfigIdPath_StampsConfigIdAndWaitsForIt(t *testing.T) {
	var sawConfigID string
	var waitedForID string
	gw := &mockGatewayService{
		detectCapabilityFn: func(context.Context, string, string, int, []byte, []byte) (gateway.Capability, error) {
			return gateway.Capability{Mode: gateway.VerifyPerPodConfigID, CRDSupportsConfigID: true}, nil
		},
		applyGatewayYAMLFn: func(_ context.Context, _, _ string, _ []byte, configID string) (string, error) {
			sawConfigID = configID
			return configID, nil
		},
		waitForConfigIDFn: func(_ context.Context, _, _ string, opts gateway.ConfigWaitOptions) error {
			waitedForID = opts.ConfigID
			return nil
		},
	}
	actions := NewTBMActions(zeroLagOffsetProvider(), zeroLagOffsetProvider(), gw)
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
		applyGatewayConfigIDFn: func(_ context.Context, _, _, configID string) (string, error) {
			return configID, nil
		},
		waitForConfigIDFn: func(_ context.Context, _, _ string, _ gateway.ConfigWaitOptions) error {
			return fmt.Errorf("timed out waiting for pods to report the new configId")
		},
	}
	actions := NewTBMActions(zeroLagOffsetProvider(), zeroLagOffsetProvider(), gw)
	config := testTBMConfig()

	err := actions.Fence(context.Background(), config)
	require.Error(t, err, "a hot-reload check that never reaches the pods must fail Fence before anything is applied")
	assert.Contains(t, err.Error(), "hot-reload check")
}

func TestTBMActions_Fence_ApplyErrorPropagates(t *testing.T) {
	gw := &mockGatewayService{
		applyGatewayYAMLFn: func(context.Context, string, string, []byte, string) (string, error) {
			return "", fmt.Errorf("connection refused")
		},
	}
	actions := NewTBMActions(zeroLagOffsetProvider(), zeroLagOffsetProvider(), gw)
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
		applyGatewayYAMLFn: func(context.Context, string, string, []byte, string) (string, error) {
			t.Fatal("ApplyGatewayYAML must not be called when there are no topics to fence")
			return "", nil
		},
	}
	actions := NewTBMActions(zeroLagOffsetProvider(), zeroLagOffsetProvider(), gw)
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
func TestTBMActions_Fence_AppliesFenceYAMLFencingEntryVerbatim(t *testing.T) {
	for _, blocked := range []bool{true, false} {
		t.Run(fmt.Sprintf("blocked=%v", blocked), func(t *testing.T) {
			var appliedYAML []byte
			gw := &mockGatewayService{
				applyGatewayYAMLFn: func(_ context.Context, _, _ string, yamlData []byte, _ string) (string, error) {
					appliedYAML = yamlData
					return "", nil
				},
			}
			actions := NewTBMActions(zeroLagOffsetProvider(), zeroLagOffsetProvider(), gw)
			config := testTBMConfig()
			config.FenceYAML = fmt.Sprintf("rules:\n  routing:\n    coordination:\n      group: source\n    default: source\n  fencing:\n    - topics: [\"t1.order\"]\n      blocked: %v\n", blocked)

			require.NoError(t, actions.Fence(context.Background(), config))

			var obj map[string]any
			require.NoError(t, yamlUnmarshalForTest(t, appliedYAML, &obj))
			spec := obj["spec"].(map[string]any)
			routes := spec["routes"].([]any)
			route := routes[0].(map[string]any)
			rules := route["rules"].(map[string]any)
			fencing := rules["fencing"].([]any)
			require.Len(t, fencing, 1)
			entry := fencing[0].(map[string]any)
			assert.Equal(t, blocked, entry["blocked"], "Fence must apply the fencing entry's blocked value verbatim, never patch it")
		})
	}
}

func TestTBMActions_Fence_GatewayRejectedErrorPropagates(t *testing.T) {
	gw := &mockGatewayService{
		// The apply itself must succeed so the rejection surfaces from the
		// acceptance wait, mirroring migration's own
		// TestWorkflow_SwitchGateway_OperatorRejection_FailsWithOperatorMessage.
		applyGatewayYAMLFn: func(context.Context, string, string, []byte, string) (string, error) { return "", nil },
		waitForGatewayAcceptedFn: func(context.Context, string, string, time.Duration, time.Duration) error {
			return &gateway.GatewayRejectedError{Reason: "InvalidSpec", Message: "route not found"}
		},
	}
	actions := NewTBMActions(zeroLagOffsetProvider(), zeroLagOffsetProvider(), gw)
	config := testTBMConfig()

	err := actions.Fence(context.Background(), config)
	require.Error(t, err)
	var rejected *gateway.GatewayRejectedError
	assert.ErrorAs(t, err, &rejected, "a GatewayRejectedError must be unwrappable by the caller")
}
