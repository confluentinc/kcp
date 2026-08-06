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
// the live cluster.
func (s *K8sService) DetectCapability(ctx context.Context, namespace, gatewayName string) (Capability, error) {
	config, err := clientcmd.BuildConfigFromFlags("", s.kubeConfigPath)
	if err != nil {
		return Capability{}, fmt.Errorf("failed to build config: %w", err)
	}

	dynamicClient, err := dynamic.NewForConfig(config)
	if err != nil {
		return Capability{}, fmt.Errorf("failed to create dynamic client: %w", err)
	}

	return detectCapability(ctx, dynamicClient, namespace, gatewayName)
}

// detectCapability is the inner orchestration used by DetectCapability. Split
// from the method so unit tests can inject a fake dynamic client.
//
// Gates are evaluated cheapest-first, and each failure selects a verification
// mode rather than erroring: an older cluster is a supported cluster. Only the
// gates that need the network (endpoint reachability, and whether a configId
// bump actually propagates) are left to the caller, so an operator who never
// wanted hot-reload never sees a network error.
func detectCapability(ctx context.Context, dynamicClient dynamic.Interface, namespace, gatewayName string) (Capability, error) {
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
		detected.Mode = VerifyPerPodConfigID
	}

	slog.Debug("resolved gateway verification mode",
		"gateway", gatewayName, "namespace", namespace, "mode", detected.Mode,
		"crdSupportsConfigId", detected.CRDSupportsConfigID, "hotReloadEnabled", detected.HotReloadEnabled)

	return detected, nil
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
