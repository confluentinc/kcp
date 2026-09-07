package gateway

import (
	"context"
	"fmt"
	"testing"

	"github.com/goccy/go-yaml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes"
	k8stesting "k8s.io/client-go/testing"
)

// servingPods builds a clientset holding n Ready gateway pods, which is what the
// endpoint gate enumerates before probing.
func servingPods(namespace, gatewayName string, n int) kubernetes.Interface {
	objs := make([]runtime.Object, 0, n)
	for i := 0; i < n; i++ {
		// A pod with no IP is not an endpoint the probe can reach, so the lister
		// skips it — the IP is what makes these pods count as serving.
		objs = append(objs, gatewayPodWithIP(
			fmt.Sprintf("%s-%d", gatewayName, i), namespace, gatewayName,
			fmt.Sprintf("uid-%d", i), fmt.Sprintf("10.0.1.%d", i+1), true))
	}
	return newFakeClientset(objs...)
}

// probeStub answers every pod with the same outcome.
func probeStub(outcome ProbeOutcome) podProber {
	return func(context.Context, GatewayPodEndpoint) (ProbeResult, error) {
		return ProbeResult{Outcome: outcome, ConfigID: "rev-1"}, nil
	}
}

var crdGVRForTest = schema.GroupVersionResource{
	Group:    CRDGroup,
	Version:  CRDVersion,
	Resource: CRDResourcePlural,
}

// newGatewayCRD builds a Gateway CustomResourceDefinition whose named versions
// each declare the given spec property names under
// spec.versions[].schema.openAPIV3Schema.properties.spec.properties.
func newGatewayCRD(versionProps map[string][]string) *unstructured.Unstructured {
	versions := make([]any, 0, len(versionProps))
	// Deterministic order: v1alpha1 before v1beta1 when both are present.
	for _, name := range []string{"v1alpha1", "v1beta1"} {
		props, ok := versionProps[name]
		if !ok {
			continue
		}
		specProps := map[string]any{}
		for _, p := range props {
			specProps[p] = map[string]any{"type": "string"}
		}
		versions = append(versions, map[string]any{
			"name": name,
			"schema": map[string]any{
				"openAPIV3Schema": map[string]any{
					"properties": map[string]any{
						"spec": map[string]any{
							"properties": specProps,
						},
					},
				},
			},
		})
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "apiextensions.k8s.io/v1",
		"kind":       "CustomResourceDefinition",
		"metadata":   map[string]any{"name": GatewayCRDName},
		"spec":       map[string]any{"versions": versions},
	}}
}

// newGatewayCRWithHotReload builds a minimal Gateway CR. hotReload is omitted
// entirely when enabled is nil.
func newGatewayCRWithHotReload(name, namespace string, enabled *bool) *unstructured.Unstructured {
	spec := map[string]any{}
	if enabled != nil {
		spec["hotReload"] = map[string]any{"enabled": *enabled}
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": GatewayGroup + "/" + GatewayVersion,
		"kind":       GatewayKind,
		"metadata":   map[string]any{"name": name, "namespace": namespace},
		"spec":       spec,
	}}
}

// newFakeDynamicClientWithCRD seeds a fake dynamic client that serves both the
// Gateway GVR (namespaced) and the CRD GVR (cluster-scoped).
func newFakeDynamicClientWithCRD(crd *unstructured.Unstructured, gateways ...*unstructured.Unstructured) *dynamicfake.FakeDynamicClient {
	scheme := runtime.NewScheme()
	cs := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme,
		map[schema.GroupVersionResource]string{
			gatewayGVRForTest: "GatewayList",
			crdGVRForTest:     "CustomResourceDefinitionList",
		},
	)
	if crd != nil {
		if _, err := cs.Resource(crdGVRForTest).Create(context.Background(), crd, metav1.CreateOptions{}); err != nil {
			panic(fmt.Sprintf("newFakeDynamicClientWithCRD seed crd: %v", err))
		}
	}
	for _, g := range gateways {
		if _, err := cs.Resource(gatewayGVRForTest).Namespace(g.GetNamespace()).
			Create(context.Background(), g, metav1.CreateOptions{}); err != nil {
			panic(fmt.Sprintf("newFakeDynamicClientWithCRD seed gateway: %v", err))
		}
	}
	return cs
}

func boolPtr(b bool) *bool { return &b }

// forbidCRDReads makes every CRD read fail the way a namespace-scoped service
// account's does: a 403 at the cluster scope, which is the normal shape of an
// enterprise Kubernetes install rather than a misconfiguration.
func forbidCRDReads(cs *dynamicfake.FakeDynamicClient) *dynamicfake.FakeDynamicClient {
	cs.PrependReactor("get", CRDResourcePlural, func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(
			schema.GroupResource{Group: CRDGroup, Resource: CRDResourcePlural},
			GatewayCRDName,
			fmt.Errorf(`User "system:serviceaccount:confluent:kcp-runner" cannot get resource `+
				`"customresourcedefinitions" in API group "apiextensions.k8s.io" at the cluster scope`))
	})
	return cs
}

// countCRDReads records how many times the cluster-scoped CRD read is attempted,
// so a test can assert kcp never asks for a permission it does not need.
func countCRDReads(cs *dynamicfake.FakeDynamicClient, reads *int) *dynamicfake.FakeDynamicClient {
	cs.PrependReactor("get", CRDResourcePlural, func(k8stesting.Action) (bool, runtime.Object, error) {
		*reads++
		return false, nil, nil
	})
	return cs
}

func TestCRDSupportsConfigID(t *testing.T) {
	tests := []struct {
		name string
		crd  *unstructured.Unstructured
		want bool
	}{
		{
			name: "configId declared on v1beta1",
			crd:  newGatewayCRD(map[string][]string{"v1beta1": {"configId", "hotReload", "replicas"}}),
			want: true,
		},
		{
			name: "no configId anywhere - pre-hot-reload CFK",
			crd:  newGatewayCRD(map[string][]string{"v1beta1": {"replicas", "routes"}}),
			want: false,
		},
		{
			name: "configId on only one of several versions still counts",
			crd: newGatewayCRD(map[string][]string{
				"v1alpha1": {"replicas"},
				"v1beta1":  {"configId"},
			}),
			want: true,
		},
		{
			name: "no versions at all",
			crd:  newGatewayCRD(map[string][]string{}),
			want: false,
		},
		{
			name: "nil CRD",
			crd:  nil,
			want: false,
		},
		{
			name: "malformed - versions is not a slice",
			crd: &unstructured.Unstructured{Object: map[string]any{
				"spec": map[string]any{"versions": "nonsense"},
			}},
			want: false,
		},
		{
			name: "malformed - version entry is not a map",
			crd: &unstructured.Unstructured{Object: map[string]any{
				"spec": map[string]any{"versions": []any{"nonsense"}},
			}},
			want: false,
		},
		{
			name: "empty object",
			crd:  &unstructured.Unstructured{Object: map[string]any{}},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, crdSupportsConfigID(tt.crd))
		})
	}
}

func TestHotReloadEnabledInCR(t *testing.T) {
	tests := []struct {
		name string
		gw   *unstructured.Unstructured
		want bool
	}{
		{
			name: "enabled true",
			gw:   newGatewayCRWithHotReload("gw", "confluent", boolPtr(true)),
			want: true,
		},
		{
			name: "enabled false",
			gw:   newGatewayCRWithHotReload("gw", "confluent", boolPtr(false)),
			want: false,
		},
		{
			name: "hotReload block absent",
			gw:   newGatewayCRWithHotReload("gw", "confluent", nil),
			want: false,
		},
		{
			name: "hotReload present but enabled absent",
			gw: &unstructured.Unstructured{Object: map[string]any{
				"spec": map[string]any{"hotReload": map[string]any{}},
			}},
			want: false,
		},
		{
			name: "enabled is a string, not a bool",
			gw: &unstructured.Unstructured{Object: map[string]any{
				"spec": map[string]any{"hotReload": map[string]any{"enabled": "true"}},
			}},
			want: false,
		},
		{
			name: "nil CR",
			gw:   nil,
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, hotReloadEnabledInCR(tt.gw))
		})
	}
}

func TestDetectCapability(t *testing.T) {
	const ns, gw = "confluent", "test-gateway"

	t.Run("CRD supports configId and hotReload is enabled - per-pod configId", func(t *testing.T) {
		cs := newFakeDynamicClientWithCRD(
			newGatewayCRD(map[string][]string{"v1beta1": {"configId", "hotReload"}}),
			newGatewayCRWithHotReload(gw, ns, boolPtr(true)),
		)

		got, err := detectCapability(context.Background(), cs, servingPods(ns, gw, 1), probeStub(ProbeApplied), ns, gw, nil, nil)
		require.NoError(t, err)

		assert.Equal(t, VerifyPerPodConfigID, got.Mode)
		assert.True(t, got.CRDSupportsConfigID)
		assert.True(t, got.HotReloadEnabled)
		assert.Empty(t, got.Advisory, "no advisory when hot-reload verification is available")
	})

	// The CFK version window that makes this a refusal rather than a downgrade:
	// chart 0.1718.10 declares spec.hotReload.enabled but not spec.configId. Hot
	// reload therefore works, no pod ever rolls, and rollout verification would
	// pass having observed nothing.
	t.Run("hot-reload on but CRD lacks configId - refuses rather than downgrading", func(t *testing.T) {
		cs := newFakeDynamicClientWithCRD(
			newGatewayCRD(map[string][]string{"v1beta1": {"replicas"}}),
			newGatewayCRWithHotReload(gw, ns, boolPtr(true)),
		)

		_, err := detectCapability(context.Background(), cs, servingPods(ns, gw, 1), probeStub(ProbeApplied), ns, gw, nil, nil)
		require.Error(t, err, "rollout verification is vacuous once hot-reload is on")

		assert.Contains(t, err.Error(), "spec.configId")
		assert.Contains(t, err.Error(), gw)
		// A refusal, unlike an advisory, owes the operator a way forward.
		assert.Contains(t, err.Error(), "upgrade CFK")
		assert.Contains(t, err.Error(), "spec.hotReload.enabled: false")
	})

	t.Run("hotReload disabled - rollout mode, advisory names the field", func(t *testing.T) {
		cs := newFakeDynamicClientWithCRD(
			newGatewayCRD(map[string][]string{"v1beta1": {"configId", "hotReload"}}),
			newGatewayCRWithHotReload(gw, ns, boolPtr(false)),
		)

		got, err := detectCapability(context.Background(), cs, servingPods(ns, gw, 1), probeStub(ProbeApplied), ns, gw, nil, nil)
		require.NoError(t, err)

		assert.Equal(t, VerifyRollout, got.Mode)
		assert.False(t, got.HotReloadEnabled)
		assert.Contains(t, got.Advisory, "spec.hotReload.enabled")
		assert.Contains(t, got.Advisory, "roll")
		// Left false even though this CRD does declare configId: the field is only
		// meaningful once hot-reload is on, and the read is skipped otherwise.
		assert.False(t, got.CRDSupportsConfigID)
	})

	t.Run("hotReload block absent entirely - rollout mode", func(t *testing.T) {
		cs := newFakeDynamicClientWithCRD(
			newGatewayCRD(map[string][]string{"v1beta1": {"configId", "hotReload"}}),
			newGatewayCRWithHotReload(gw, ns, nil),
		)

		got, err := detectCapability(context.Background(), cs, servingPods(ns, gw, 1), probeStub(ProbeApplied), ns, gw, nil, nil)
		require.NoError(t, err)

		assert.Equal(t, VerifyRollout, got.Mode)
		assert.False(t, got.HotReloadEnabled)
		assert.Contains(t, got.Advisory, "spec.hotReload.enabled")
	})

	t.Run("hot-reload on but CRD absent - refuses", func(t *testing.T) {
		cs := newFakeDynamicClientWithCRD(nil, newGatewayCRWithHotReload(gw, ns, boolPtr(true)))

		_, err := detectCapability(context.Background(), cs, servingPods(ns, gw, 1), probeStub(ProbeApplied), ns, gw, nil, nil)
		require.Error(t, err)

		assert.Contains(t, err.Error(), "not installed")
		assert.Contains(t, err.Error(), "spec.hotReload.enabled: false")
	})

	// The permission case, and the reason it is not a downgrade: a namespaced
	// service account cannot read the CRD, but that changes nothing about whether
	// the pods will roll. Treating a 403 as "no configId support" would select
	// rollout verification on a cluster that hot-reloads.
	t.Run("hot-reload on but CRD read forbidden - refuses, naming the permission", func(t *testing.T) {
		cs := forbidCRDReads(newFakeDynamicClientWithCRD(
			newGatewayCRD(map[string][]string{"v1beta1": {"configId", "hotReload"}}),
			newGatewayCRWithHotReload(gw, ns, boolPtr(true)),
		))

		_, err := detectCapability(context.Background(), cs, servingPods(ns, gw, 1), probeStub(ProbeApplied), ns, gw, nil, nil)
		require.Error(t, err, "kcp must not verify by rollout when it cannot rule out a hot-reload")

		assert.Contains(t, err.Error(), "customresourcedefinitions")
		// Both remedies, because only one of them is in a namespaced user's hands.
		assert.Contains(t, err.Error(), "grant get on customresourcedefinitions")
		assert.Contains(t, err.Error(), "spec.hotReload.enabled: false")
	})

	// The mirror image, and the reachability regression this fixes: with
	// hot-reload off kcp never injects a configId, so it has no reason to ask for
	// a cluster-scoped permission at all.
	t.Run("hot-reload off - CRD is never read, so a 403 cannot block the migration", func(t *testing.T) {
		reads := 0
		cs := countCRDReads(forbidCRDReads(newFakeDynamicClientWithCRD(
			newGatewayCRD(map[string][]string{"v1beta1": {"configId", "hotReload"}}),
			newGatewayCRWithHotReload(gw, ns, boolPtr(false)),
		)), &reads)

		got, err := detectCapability(context.Background(), cs, servingPods(ns, gw, 1), probeStub(ProbeApplied), ns, gw, nil, nil)
		require.NoError(t, err, "a namespaced install must still be able to migrate")

		assert.Equal(t, VerifyRollout, got.Mode)
		assert.Zero(t, reads, "kcp must not require a permission it does not use")
		assert.Contains(t, got.Advisory, "spec.hotReload.enabled")
	})

	t.Run("hotReload gate takes precedence over the CRD gate", func(t *testing.T) {
		cs := newFakeDynamicClientWithCRD(
			newGatewayCRD(map[string][]string{"v1beta1": {"replicas"}}),
			newGatewayCRWithHotReload(gw, ns, boolPtr(false)),
		)

		got, err := detectCapability(context.Background(), cs, servingPods(ns, gw, 1), probeStub(ProbeApplied), ns, gw, nil, nil)
		require.NoError(t, err)

		assert.Equal(t, VerifyRollout, got.Mode)
		// Hot-reload decides the mechanism, so it is the gate worth reporting; the
		// CRD's state is not consulted and must not be implied.
		assert.Contains(t, got.Advisory, "spec.hotReload.enabled")
		assert.NotContains(t, got.Advisory, "spec.configId")
	})

	t.Run("gateway CR missing is an error", func(t *testing.T) {
		cs := newFakeDynamicClientWithCRD(
			newGatewayCRD(map[string][]string{"v1beta1": {"configId", "hotReload"}}),
		)

		_, err := detectCapability(context.Background(), cs, servingPods(ns, gw, 1), probeStub(ProbeApplied), ns, gw, nil, nil)
		require.Error(t, err, "we cannot decide a mode without reading the live gateway CR")
		assert.Contains(t, err.Error(), gw)
	})

	t.Run("cancelled context propagates", func(t *testing.T) {
		cs := newFakeDynamicClientWithCRD(
			newGatewayCRD(map[string][]string{"v1beta1": {"configId", "hotReload"}}),
			newGatewayCRWithHotReload(gw, ns, boolPtr(true)),
		)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		_, err := detectCapability(ctx, cs, servingPods(ns, gw, 1), probeStub(ProbeApplied), ns, gw, nil, nil)
		require.Error(t, err)
	})
}

// plannedCRYAML renders a gateway CR file of the kind an operator hands to
// --fenced-cr-yaml or --switchover-cr-yaml. hotReload is omitted when enabled is
// nil, which is the common shape: most CRs say nothing about it.
func plannedCRYAML(t *testing.T, enabled *bool) []byte {
	t.Helper()
	data, err := yaml.Marshal(newGatewayCRWithHotReload("test-gateway", "confluent", enabled).Object)
	require.NoError(t, err)
	return data
}

// kcp applies the operator's fenced and switchover files, so those files can
// change the gateway's hot-reload behaviour — and changing it is not kcp's call to
// make. A CR that enables hot-reload on a gateway that never had it converts every
// later transition to an in-place apply; one that disables it on a gateway running
// it starts rolling pods that were not rolling before. Either way the operator's
// running gateway ends up behaving differently because of a file kcp applied on
// their behalf, so kcp refuses and explains rather than picking a side.
//
// A CR that says nothing inherits, which is the shape of every example under
// docs/assets/gateway-switchover: none of the 24 mention spec.hotReload.
func TestDetectCapabilityRejectsHotReloadDiscrepancies(t *testing.T) {
	const ns, gw = "confluent", "test-gateway"

	capableCRD := func() *unstructured.Unstructured {
		return newGatewayCRD(map[string][]string{"v1beta1": {"configId", "hotReload"}})
	}

	t.Run("a planned CR would enable hot-reload on a gateway that never set it", func(t *testing.T) {
		cs := newFakeDynamicClientWithCRD(capableCRD(), newGatewayCRWithHotReload(gw, ns, nil))

		_, err := detectCapability(context.Background(), cs, servingPods(ns, gw, 1), probeStub(ProbeApplied), ns, gw,
			plannedCRYAML(t, boolPtr(true)), plannedCRYAML(t, nil))
		require.Error(t, err, "applying this CR would turn hot-reload on, which the operator did not ask for")

		assert.Contains(t, err.Error(), "fenced", "the operator has to know which file to fix")
		assert.Contains(t, err.Error(), "spec.hotReload.enabled")
	})

	t.Run("a planned CR would disable hot-reload on a gateway running it", func(t *testing.T) {
		cs := newFakeDynamicClientWithCRD(capableCRD(), newGatewayCRWithHotReload(gw, ns, boolPtr(true)))

		_, err := detectCapability(context.Background(), cs, servingPods(ns, gw, 1), probeStub(ProbeApplied), ns, gw,
			plannedCRYAML(t, boolPtr(false)), plannedCRYAML(t, boolPtr(false)))
		require.Error(t, err, "applying this CR would start rolling pods that were not rolling before")

		assert.Contains(t, err.Error(), "fenced")
	})

	t.Run("the switchover CR is checked too", func(t *testing.T) {
		cs := newFakeDynamicClientWithCRD(capableCRD(), newGatewayCRWithHotReload(gw, ns, nil))

		_, err := detectCapability(context.Background(), cs, servingPods(ns, gw, 1), probeStub(ProbeApplied), ns, gw,
			plannedCRYAML(t, nil), plannedCRYAML(t, boolPtr(true)))
		require.Error(t, err)

		assert.Contains(t, err.Error(), "switchover")
	})

	// The over-rejection guards. Both of these must keep working, or the rule
	// refuses migrations that change nothing about the gateway.
	t.Run("planned CRs that omit the field inherit what the gateway runs", func(t *testing.T) {
		cs := newFakeDynamicClientWithCRD(capableCRD(), newGatewayCRWithHotReload(gw, ns, boolPtr(true)))

		got, err := detectCapability(context.Background(), cs, servingPods(ns, gw, 1), probeStub(ProbeApplied), ns, gw,
			plannedCRYAML(t, nil), plannedCRYAML(t, nil))
		require.NoError(t, err, "this is the shape of every shipped example; it must not be a refusal")

		assert.Equal(t, VerifyPerPodConfigID, got.Mode)
	})

	t.Run("an explicit false agrees with a gateway that never set the field", func(t *testing.T) {
		// Absent and false are the same behaviour, so nothing changes.
		cs := newFakeDynamicClientWithCRD(capableCRD(), newGatewayCRWithHotReload(gw, ns, nil))

		got, err := detectCapability(context.Background(), cs, servingPods(ns, gw, 1), probeStub(ProbeApplied), ns, gw,
			plannedCRYAML(t, boolPtr(false)), plannedCRYAML(t, boolPtr(false)))
		require.NoError(t, err)

		assert.Equal(t, VerifyRollout, got.Mode)
	})

	// Measured against a real CFK cluster, not inferred: once an apply from kcp's
	// field manager declares spec.hotReload, a later apply from the same manager
	// that OMITS it deletes the field. So "absent inherits" stops being true after
	// kcp has declared it once — a fenced CR that declares hot-reload followed by a
	// switchover CR that does not would silently disable hot-reload at switchover,
	// mid-migration, which is exactly the behaviour change kcp must not make.
	//
	// Enforced symmetrically rather than only in the harmful order. The safe
	// direction (fenced omits, switchover declares) is safe only because of the
	// order the applies happen in, and a rule that depends on that is a rule that
	// rots; an operator who mentions the field in one file has no reason to omit it
	// from the other.
	t.Run("planned CRs must agree on whether they mention hot-reload at all", func(t *testing.T) {
		cs := newFakeDynamicClientWithCRD(capableCRD(), newGatewayCRWithHotReload(gw, ns, boolPtr(true)))

		_, err := detectCapability(context.Background(), cs, servingPods(ns, gw, 1), probeStub(ProbeApplied), ns, gw,
			plannedCRYAML(t, boolPtr(true)), plannedCRYAML(t, nil))
		require.Error(t, err, "the switchover apply would prune the field the fence apply declared")

		assert.Contains(t, err.Error(), "switchover")
		assert.Contains(t, err.Error(), "spec.hotReload")
	})

	t.Run("mentioning hot-reload in the switchover CR alone is refused too", func(t *testing.T) {
		cs := newFakeDynamicClientWithCRD(capableCRD(), newGatewayCRWithHotReload(gw, ns, boolPtr(true)))

		_, err := detectCapability(context.Background(), cs, servingPods(ns, gw, 1), probeStub(ProbeApplied), ns, gw,
			plannedCRYAML(t, nil), plannedCRYAML(t, boolPtr(true)))
		require.Error(t, err)

		assert.Contains(t, err.Error(), "fenced")
	})

	// With hot-reload off there is nothing a prune could take away: absent and
	// false are the same behaviour, so deleting an explicit false changes nothing
	// and the files are free to disagree about mentioning it.
	t.Run("presence may differ freely when the gateway has hot-reload off", func(t *testing.T) {
		cs := newFakeDynamicClientWithCRD(capableCRD(), newGatewayCRWithHotReload(gw, ns, boolPtr(false)))

		got, err := detectCapability(context.Background(), cs, servingPods(ns, gw, 1), probeStub(ProbeApplied), ns, gw,
			plannedCRYAML(t, boolPtr(false)), plannedCRYAML(t, nil))
		require.NoError(t, err)

		assert.Equal(t, VerifyRollout, got.Mode)
	})

	// A file kcp cannot read is not a file that says "hot-reload is off". Treating
	// a parse failure as absence would let an unreadable CR through as though it
	// agreed, and it is reachable at execute time, where these CRs come from
	// migration-state.json rather than from the init-time validator.
	t.Run("a planned CR that cannot be parsed is an error, not an absent declaration", func(t *testing.T) {
		cs := newFakeDynamicClientWithCRD(capableCRD(), newGatewayCRWithHotReload(gw, ns, boolPtr(false)))

		_, err := detectCapability(context.Background(), cs, servingPods(ns, gw, 1), probeStub(ProbeApplied), ns, gw,
			[]byte("spec: [unclosed"), plannedCRYAML(t, nil))
		require.Error(t, err, "kcp cannot confirm agreement with a file it was unable to read")

		assert.Contains(t, err.Error(), "fenced")
	})

	// The gate refusals still name every CR that declares hot-reload, because the
	// remedy has to be applied consistently: setting it false on the gateway alone
	// would leave the files contradicting it and trade one refusal for another.
	t.Run("a gate refusal names every CR declaring hot-reload", func(t *testing.T) {
		cs := newFakeDynamicClientWithCRD(
			newGatewayCRD(map[string][]string{"v1beta1": {"replicas"}}),
			newGatewayCRWithHotReload(gw, ns, boolPtr(true)),
		)

		_, err := detectCapability(context.Background(), cs, servingPods(ns, gw, 1), probeStub(ProbeApplied), ns, gw,
			plannedCRYAML(t, boolPtr(true)), plannedCRYAML(t, boolPtr(true)))
		require.Error(t, err)

		for _, role := range []string{"live", "fenced", "switchover"} {
			assert.Contains(t, err.Error(), role)
		}
		assert.Contains(t, err.Error(), "spec.hotReload.enabled: false")
	})
}

// A VerifyRollout capability must never permit configId injection: server-side
// apply hard-fails on an undeclared spec.configId, so this is the gate that
// keeps kcp working against every pre-hot-reload cluster.
func TestCapabilityInjectsConfigID(t *testing.T) {
	tests := []struct {
		name string
		cap  Capability
		want bool
	}{
		{
			name: "per-pod configId mode injects",
			cap:  Capability{Mode: VerifyPerPodConfigID, CRDSupportsConfigID: true, HotReloadEnabled: true},
			want: true,
		},
		{
			name: "rollout mode does not inject",
			cap:  Capability{Mode: VerifyRollout, CRDSupportsConfigID: false},
			want: false,
		},
		{
			name: "rollout mode does not inject even when the CRD would allow it",
			cap:  Capability{Mode: VerifyRollout, CRDSupportsConfigID: true, HotReloadEnabled: false},
			want: false,
		},
		{
			name: "zero value does not inject",
			cap:  Capability{},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.cap.InjectsConfigID())
		})
	}
}
