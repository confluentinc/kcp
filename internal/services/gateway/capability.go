package gateway

import (
	"context"
	"fmt"
	"log/slog"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

// CustomResourceDefinition coordinates, used to inspect what the *installed*
// CFK operator's Gateway CRD declares. The CRD is the only authority on this:
// spec.configId landed in CFK v3.3.x, and applying an undeclared field is not a
// no-op — server-side apply rejects it outright (it does not prune; pruning
// happens on create/update, not apply). So the CRD must be read before kcp
// writes a configId, or `kcp migration execute` breaks on every cluster running
// an older operator.
const (
	CRDGroup          = "apiextensions.k8s.io"
	CRDVersion        = "v1"
	CRDResourcePlural = "customresourcedefinitions"
	GatewayCRDName    = "gateways.platform.confluent.io"
)

// gatewayConfigIDField is the Gateway spec field kcp sets to identify a config
// revision, and the field the gateway echoes back from GET /config.
const gatewayConfigIDField = "configId"

// VerificationMode selects how kcp confirms that a Gateway state transition
// actually landed.
type VerificationMode string

const (
	// VerifyPerPodConfigID polls GET /config on every gateway pod until each
	// reports the configId kcp injected for this transition.
	//
	// This is deliberately mechanism-agnostic. A hot-reload and a pod roll
	// converge on the *same* observable: a hot-reload has the running gateway
	// apply the new config file in place, while a roll starts new pods from that
	// same file — and either way the file carries the injected configId. kcp
	// therefore never has to predict which one CFK chose in order to know the
	// transition landed.
	VerifyPerPodConfigID VerificationMode = "per-pod-configid"

	// VerifyRollout confirms a transition using the backing Deployment's
	// rollout status alone. This is the correct — not degraded — mode whenever
	// the cluster cannot support configId verification, and it is what every
	// pre-hot-reload cluster gets.
	VerifyRollout VerificationMode = "rollout"
)

// Operator-facing advisories. These explain what kcp is going to do and why;
// they deliberately do not recommend a configuration change. Hot-reload may be
// switched off for reasons kcp cannot see.
const (
	advisoryNoConfigIDSupport = "the installed CFK operator's Gateway CRD does not declare spec.configId — this state transition will be verified by pod rollout"
	advisoryHotReloadDisabled = "spec.hotReload.enabled is not set or disabled — this state transition will roll gateway pods"
	advisoryNoConfigEndpoint  = "the gateway image does not expose /config — this state transition will be verified by pod rollout"
)

// Capability records what the live cluster supports, and therefore how kcp will
// verify each Gateway state transition.
type Capability struct {
	// Mode is the verification strategy to use. Named for the evidence
	// available, not for the pod-update type: both a hot-reload and a roll are
	// verifiable under VerifyPerPodConfigID.
	Mode VerificationMode

	// CRDSupportsConfigID reports whether the installed operator's Gateway CRD
	// declares spec.configId.
	CRDSupportsConfigID bool

	// HotReloadEnabled reports whether the live Gateway CR declares
	// spec.hotReload.enabled: true.
	HotReloadEnabled bool

	// ConfigEndpointServed reports whether the running gateway pods serve
	// GET /config. Only meaningful when the two gates above passed, since the
	// endpoint is not probed otherwise.
	ConfigEndpointServed bool

	// Advisory is a non-empty explanation whenever Mode is not
	// VerifyPerPodConfigID, suitable for showing to the operator verbatim.
	Advisory string
}

// InjectsConfigID reports whether kcp may write spec.configId on this cluster.
//
// This is the guard that keeps kcp working against pre-hot-reload clusters:
// server-side apply hard-fails on an undeclared spec.configId, so injecting it
// unconditionally would break every migration against an older CFK operator.
func (c Capability) InjectsConfigID() bool {
	return c.Mode == VerifyPerPodConfigID
}

// DetectCapability determines how Gateway state transitions can be verified on
// the live cluster. port is the port serving GET /config; 0 selects
// DefaultGatewayConfigPort.
func (s *K8sService) DetectCapability(ctx context.Context, namespace, gatewayName string, port int) (Capability, error) {
	config, err := clientcmd.BuildConfigFromFlags("", s.kubeConfigPath)
	if err != nil {
		return Capability{}, fmt.Errorf("failed to build config: %w", err)
	}

	dynamicClient, err := dynamic.NewForConfig(config)
	if err != nil {
		return Capability{}, fmt.Errorf("failed to create dynamic client: %w", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return Capability{}, fmt.Errorf("failed to create clientset: %w", err)
	}

	probe := func(ctx context.Context, endpoint GatewayPodEndpoint) (ProbeResult, error) {
		return probeGatewayConfig(ctx, newConfigProbeClient(configProbeRequestTimeout),
			gatewayConfigAddr(endpoint.IP, port))
	}

	return detectCapability(ctx, dynamicClient, clientset, probe, namespace, gatewayName)
}

// detectCapability is the inner orchestration used by DetectCapability. Split
// from the method so unit tests can inject a fake dynamic client.
//
// Gates are evaluated cheapest-first, and each failure selects a verification
// mode rather than erroring: an older cluster is a supported cluster. Only the
// gates that need the network (endpoint reachability, and whether a configId
// bump actually propagates) are left to the caller, so an operator who never
// wanted hot-reload never sees a network error.
func detectCapability(ctx context.Context, dynamicClient dynamic.Interface, clientset kubernetes.Interface, probe podProber, namespace, gatewayName string) (Capability, error) {
	if err := ctx.Err(); err != nil {
		return Capability{}, err
	}

	crdGVR := schema.GroupVersionResource{
		Group:    CRDGroup,
		Version:  CRDVersion,
		Resource: CRDResourcePlural,
	}

	// Gate 1: does the installed operator's CRD declare spec.configId?
	// A missing CRD means an older or differently-installed operator, which is
	// a mode selection, not an error.
	crd, err := dynamicClient.Resource(crdGVR).Get(ctx, GatewayCRDName, metav1.GetOptions{})
	switch {
	case apierrors.IsNotFound(err):
		slog.Debug("gateway CRD not found; treating cluster as pre-configId", "crd", GatewayCRDName)
		crd = nil
	case err != nil:
		return Capability{}, fmt.Errorf("failed to get Gateway CRD %q: %w", GatewayCRDName, err)
	}

	gatewayGVR := schema.GroupVersionResource{
		Group:    GatewayGroup,
		Version:  GatewayVersion,
		Resource: GatewayResourcePlural,
	}

	// Gate 2: has the operator declared hot-reload on the live CR? kcp never
	// sets this field itself — it is the user's declared intent.
	gw, err := dynamicClient.Resource(gatewayGVR).Namespace(namespace).Get(ctx, gatewayName, metav1.GetOptions{})
	if err != nil {
		return Capability{}, fmt.Errorf("failed to get gateway %q for capability detection: %w", gatewayName, err)
	}

	detected := Capability{
		CRDSupportsConfigID: crdSupportsConfigID(crd),
		HotReloadEnabled:    hotReloadEnabledInCR(gw),
	}

	switch {
	case !detected.CRDSupportsConfigID:
		// The more fundamental gate wins: without CRD support the hotReload
		// field cannot be honoured either, so reporting it would mislead.
		detected.Mode = VerifyRollout
		detected.Advisory = advisoryNoConfigIDSupport
	case !detected.HotReloadEnabled:
		detected.Mode = VerifyRollout
		detected.Advisory = advisoryHotReloadDisabled
	default:
		// Gate 3: do the running pods actually serve GET /config? The operator
		// and the gateway are versioned separately, so a CRD that declares
		// configId says nothing about the image the pods are running. Only this
		// gate needs the network, which is why it is last: an operator who never
		// wanted hot-reload never reaches it.
		served, err := configEndpointServed(ctx, clientset, probe, namespace, gatewayName)
		if err != nil {
			return Capability{}, err
		}
		detected.ConfigEndpointServed = served
		if served {
			detected.Mode = VerifyPerPodConfigID
		} else {
			detected.Mode = VerifyRollout
			detected.Advisory = advisoryNoConfigEndpoint
		}
	}

	slog.Debug("resolved gateway verification mode",
		"gateway", gatewayName, "namespace", namespace, "mode", detected.Mode,
		"crdSupportsConfigId", detected.CRDSupportsConfigID, "hotReloadEnabled", detected.HotReloadEnabled,
		"configEndpointServed", detected.ConfigEndpointServed)

	return detected, nil
}

// configEndpointServed reports whether the gateway pods serve GET /config.
//
// The distinction this draws is between a capability problem and an environment
// problem, and it is the reason a single call is enough:
//
//   - every ready pod answers 404 → the image predates the endpoint (it arrived
//     in gateway 1.3.0). A supported cluster, verified by rollout instead.
//   - any pod answers 200 → the endpoint is live. A mixed answer means an image
//     change is mid-flight, which is itself a rollout; treating that as capable is
//     right, because by the time kcp verifies anything the new pods are serving.
//   - nothing is reachable → the pod network is not routable from here. That is
//     an error, not a downgrade: kcp cannot reach the pods it would need to
//     verify, and silently falling back would hide it until a switchover.
//
// Returning an error here is safe because every caller runs before fencing.
func configEndpointServed(ctx context.Context, clientset kubernetes.Interface, probe podProber, namespace, gatewayName string) (bool, error) {
	endpoints, err := listGatewayPodEndpoints(ctx, clientset, namespace, gatewayName)
	if err != nil {
		return false, err
	}

	var ready, absent, reachable int
	var firstFailure error
	for _, endpoint := range endpoints {
		if !endpoint.Ready {
			continue
		}
		ready++

		result, err := probe(ctx, endpoint)
		if err != nil {
			// Reserved for context cancellation; every per-pod condition comes
			// back as a ProbeResult.
			return false, err
		}

		switch result.Outcome {
		case ProbeEndpointAbsent:
			absent++
		case ProbeApplied, ProbeNeverSet:
			reachable++
		default:
			if firstFailure == nil {
				firstFailure = result.Err
			}
		}
	}

	switch {
	case ready == 0:
		return false, fmt.Errorf("gateway %q has no ready pods to verify against", gatewayName)
	case reachable > 0:
		return true, nil
	case absent == ready:
		slog.Debug("no gateway pod serves the config endpoint; verifying by rollout",
			"gateway", gatewayName, "readyPods", ready)
		return false, nil
	}

	// Reached only when no pod was reachable and at least one failed outright.
	// Counts only — pod names and IPs belong in the log, not the error.
	return false, fmt.Errorf("could not reach the config endpoint on any of the %d ready gateway pods "+
		"(this environment may not route pod IPs from where kcp is running): %w", ready, firstFailure)
}

// crdSupportsConfigID reports whether any served version of the Gateway CRD
// declares spec.configId. Any version counts: kcp pins the Gateway GVR to
// v1beta1, but a CRD that declares the field on a different version still comes
// from an operator new enough to render it.
func crdSupportsConfigID(crd *unstructured.Unstructured) bool {
	if crd == nil {
		return false
	}

	versions, found, err := unstructured.NestedSlice(crd.Object, "spec", "versions")
	if err != nil || !found {
		return false
	}

	for _, raw := range versions {
		version, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		props, found, err := unstructured.NestedMap(version, "schema", "openAPIV3Schema", "properties", "spec", "properties")
		if err != nil || !found {
			continue
		}
		if _, ok := props[gatewayConfigIDField]; ok {
			return true
		}
	}

	return false
}

// hotReloadEnabledInCR reports whether the live Gateway CR declares
// spec.hotReload.enabled: true. Absent, false, or malformed all read as
// disabled — the conservative direction, since guessing "enabled" would send
// kcp down a verification path the gateway will not honour.
func hotReloadEnabledInCR(gw *unstructured.Unstructured) bool {
	if gw == nil {
		return false
	}

	enabled, found, err := unstructured.NestedBool(gw.Object, "spec", "hotReload", "enabled")
	if err != nil || !found {
		return false
	}

	return enabled
}
