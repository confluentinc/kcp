package d2s

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/confluentinc/kcp/internal/services/gateway"
	"github.com/confluentinc/kcp/internal/services/migplan"
	"github.com/confluentinc/kcp/internal/services/migration"
	"github.com/goccy/go-yaml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	k8stypes "k8s.io/apimachinery/pkg/types"
)

// mockGatewayService implements gateway.Service with function fields, d2s's own copy of tbm's (test doubles
// are kept per package).
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

// DetectCapability defaults to VerifyRollout, so tests that don't care about hot-reload take the rollout path.
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

// patchRecorder records every route patch in order; failAt makes the n-th patch (1-based) fail.
type patchRecorder struct {
	mu      sync.Mutex
	patches []gateway.RoutePatch
	failAt  map[int]error
}

func (p *patchRecorder) service() *mockGatewayService {
	return &mockGatewayService{
		patchGatewayRouteFn: func(_ context.Context, _, _ string, rp gateway.RoutePatch, _ string) (string, error) {
			p.mu.Lock()
			defer p.mu.Unlock()
			p.patches = append(p.patches, rp)
			return "", p.failAt[len(p.patches)]
		},
	}
}

// fields is each patch's Field: "rules" for a rules patch, "" for a whole-route replace.
func (p *patchRecorder) fields() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]string, 0, len(p.patches))
	for _, rp := range p.patches {
		out = append(out, rp.Field)
	}
	return out
}

// testGatewayYAML is a dynamic-route gateway CR after a finished topic-based migration: orders routes to
// target, everything else to source, coordination pinned to source.
const testGatewayYAML = `apiVersion: platform.confluent.io/v1beta1
kind: Gateway
metadata:
  name: gateway-initial
  namespace: confluent
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
      security:
        cluster:
          target:
            auth: passthrough
      rules:
        routing:
          coordination:
            group: source
          default: source
          conditions:
            - topics: ["orders"]
              streamingDomain: target
`

// The three conversion artifacts reconcile returns for testGatewayYAML's route: the rules without kcp's fence
// (rollback), the rules with it (fence), and the route converted to static (switch).
const (
	testRollbackFenceYAML = "rules:\n  routing:\n    coordination:\n      group: source\n    default: source\n    conditions:\n      - topics: [\"orders\"]\n        streamingDomain: target\n"
	testFenceYAML         = testRollbackFenceYAML + "  fencing:\n    - topicPatterns: [\".*\"]\n      blocked: true\n"
	testSwitchoverYAML    = "route:\n  name: migration-route\n  endpoint: kafka-gw.example.com:9092\n  streamingDomain:\n    name: target\n    bootstrapServerId: sasl-plain\n  security:\n    cluster:\n      target:\n        auth: passthrough\n"
)

func testConfig() *migration.MigrationConfig {
	return &migration.MigrationConfig{
		MigrationId:       "d2s-1",
		K8sNamespace:      "confluent",
		InitialCrName:     "gateway-initial",
		Route:             "migration-route",
		GatewayYAML:       testGatewayYAML,
		FenceYAML:         testFenceYAML,
		SwitchoverYAML:    testSwitchoverYAML,
		RollbackFenceYAML: testRollbackFenceYAML,
		RollbackAllowed:   true,
	}
}

// convertResult is the reconcile result a live migplan.Reconcile returns for testGatewayYAML's route.
func convertResult() *migplan.Result {
	return &migplan.Result{
		Route:             "migration-route",
		Mode:              "convert",
		GatewayYAML:       testGatewayYAML,
		FenceYAML:         testFenceYAML,
		SwitchoverYAML:    testSwitchoverYAML,
		RollbackFenceYAML: testRollbackFenceYAML,
		RollbackAllowed:   true,
	}
}

func gatewayActions(gw gateway.Service) *D2SActions {
	a := NewD2SActions(gw, cleanWorld().deps())
	a.reporter = quietReporter()
	return a
}

// routeOf returns the one route of a whole gateway CR.
func routeOf(t *testing.T, cr []byte) map[string]any {
	t.Helper()
	var obj map[string]any
	require.NoError(t, yaml.Unmarshal(cr, &obj))
	spec, ok := obj["spec"].(map[string]any)
	require.True(t, ok)
	routes, ok := spec["routes"].([]any)
	require.True(t, ok)
	require.Len(t, routes, 1)
	route, ok := routes[0].(map[string]any)
	require.True(t, ok)
	return route
}

func TestFence_PatchesTheRulesFieldWithTheConvertFence(t *testing.T) {
	rec := &patchRecorder{}

	require.NoError(t, gatewayActions(rec.service()).Fence(context.Background(), testConfig()))

	require.Len(t, rec.patches, 1)
	rp := rec.patches[0]
	assert.Equal(t, "migration-route", rp.RouteName)
	assert.Equal(t, "rules", rp.Field, "the fence is a rules field patch, as TBM's is")
	rules, ok := rp.Value.(map[string]any)
	require.True(t, ok)
	fencing, ok := rules["fencing"].([]any)
	require.True(t, ok)
	require.Len(t, fencing, 1)
	entry, ok := fencing[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, []any{".*"}, entry["topicPatterns"], "the convert fence blocks every topic on the route")
	assert.Equal(t, true, entry["blocked"])
	_, hasRouting := rules["routing"]
	assert.True(t, hasRouting, "the fence keeps the route's routing")
}

func TestFence_AFenceThatLandedButWasNotConfirmedIsMarked(t *testing.T) {
	gw := (&patchRecorder{}).service()
	gw.waitForGatewayAcceptedFn = func(context.Context, string, string, time.Duration, time.Duration) error {
		return fmt.Errorf("operator never reconciled")
	}

	err := gatewayActions(gw).Fence(context.Background(), testConfig())

	require.ErrorIs(t, err, migration.ErrFenceUnconfirmed)
}

func TestFence_APatchThatFailedIsNotUnconfirmed(t *testing.T) {
	rec := &patchRecorder{failAt: map[int]error{1: fmt.Errorf("k8s API unavailable")}}

	err := gatewayActions(rec.service()).Fence(context.Background(), testConfig())

	require.Error(t, err)
	assert.NotErrorIs(t, err, migration.ErrFenceUnconfirmed, "a patch that never landed has nothing to remove")
}

func TestSwitch_ReplacesTheWholeRouteWithTheStaticOne(t *testing.T) {
	rec := &patchRecorder{}

	require.NoError(t, gatewayActions(rec.service()).Switch(context.Background(), testConfig()))

	require.Len(t, rec.patches, 1)
	rp := rec.patches[0]
	assert.Equal(t, "migration-route", rp.RouteName)
	assert.Empty(t, rp.Field, "a whole-route replace: a field patch cannot drop streamingDomains and rules")
	route, ok := rp.Value.(map[string]any)
	require.True(t, ok)
	sd, ok := route["streamingDomain"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "target", sd["name"])
	assert.Equal(t, "sasl-plain", sd["bootstrapServerId"])
	_, hasRules := route["rules"]
	assert.False(t, hasRules, "the static route has no rules tree, so no fence either")
	_, hasDomains := route["streamingDomains"]
	assert.False(t, hasDomains)
}

func TestUnfenceGateway_SetsTheRulesToTheRollbackTarget(t *testing.T) {
	rec := &patchRecorder{}

	require.NoError(t, gatewayActions(rec.service()).unfenceGateway(context.Background(), testConfig()))

	require.Len(t, rec.patches, 1)
	want, err := gateway.FragmentValue([]byte(testRollbackFenceYAML), "rules")
	require.NoError(t, err)
	assert.Equal(t, "rules", rec.patches[0].Field)
	assert.Equal(t, want, rec.patches[0].Value)
}

func TestEnsureGatewayCapability_ProbesWithTheFencedRulesAndTheSwitchedRouteOnce(t *testing.T) {
	var fenced, switched []byte
	calls := 0
	gw := (&patchRecorder{}).service()
	gw.detectCapabilityFn = func(_ context.Context, _, _ string, port int, f, s []byte) (gateway.Capability, error) {
		calls++
		fenced, switched = f, s
		assert.Equal(t, gateway.DefaultGatewayConfigPort, port, "an unset port resolves to the gateway default")
		return gateway.Capability{Mode: gateway.VerifyRollout}, nil
	}
	a := gatewayActions(gw)
	cfg := testConfig()

	require.NoError(t, a.Fence(context.Background(), cfg))
	require.NoError(t, a.Switch(context.Background(), cfg))

	assert.Equal(t, 1, calls, "resolved once per process, not once per gateway step")
	fr := routeOf(t, fenced)
	rules, ok := fr["rules"].(map[string]any)
	require.True(t, ok)
	_, hasFencing := rules["fencing"]
	assert.True(t, hasFencing, "the probe's fenced CR carries the convert fence")
	_, stillDynamic := fr["streamingDomains"]
	assert.True(t, stillDynamic, "the probe's fenced CR is still the dynamic route")
	sr := routeOf(t, switched)
	_, hasRules := sr["rules"]
	assert.False(t, hasRules, "the probe's switched CR is the static route")
	_, hasDomain := sr["streamingDomain"]
	assert.True(t, hasDomain)
}
