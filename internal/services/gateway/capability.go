package gateway

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/goccy/go-yaml"
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
//
// Reading it needs cluster-scoped RBAC, which a namespace-scoped installation of
// kcp may not have. That is why this read is reached only when hot-reload is
// enabled: when it is off kcp never injects a configId, so it never needs to ask.
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

// Operator-facing advisory. This explains what kcp is going to do and why; it
// deliberately does not recommend a configuration change. Hot-reload may be
// switched off for reasons kcp cannot see.
const advisoryHotReloadDisabled = "spec.hotReload.enabled is not set or disabled — this state transition will roll gateway pods"

// Refusal text, for the case where hot-reload is enabled but the evidence kcp
// needs to verify a transition is unavailable.
//
// Unlike an advisory, a refusal must name a remedy: kcp is stopping the operator,
// so it owes them the way forward. Disabling hot-reload is offered on every
// refusal because it is the one remedy always in the operator's own hands, and it
// is not a workaround — turning hot-reload off makes CFK roll the pods, which is
// a mechanism kcp can verify.
const (
	refusalPreamble = "cannot verify gateway state transitions: spec.hotReload.enabled is true for gateway %q " +
		"(declared in %s), so CFK " +
		"applies config changes in place and the pods never roll — pod-rollout verification would observe nothing and " +
		"report success, while a fence may not have reached the gateway at all"

	causeCRDForbidden  = "kcp is not permitted to read the Gateway CRD, so it cannot confirm the operator declares spec.configId"
	remedyCRDForbidden = "grant get on customresourcedefinitions.apiextensions.k8s.io at the cluster scope"

	causeCRDAbsent       = "the Gateway CRD is not installed, so the operator cannot declare spec.configId"
	causeNoConfigIDInCRD = "the installed CFK operator's Gateway CRD does not declare spec.configId"
	remedyUpgradeCFK     = "upgrade CFK to a version whose Gateway CRD declares spec.configId"

	causeNoConfigEndpoint = "the running gateway image does not serve GET /config, so kcp cannot read back the config revision it applied"
	remedyUpgradeGateway  = "upgrade the gateway image to one that serves GET /config (it arrived in gateway 1.3.0)"
)

// unverifiableHotReloadError builds the refusal returned when hot-reload is
// enabled but the per-pod configId evidence that verification depends on cannot
// be obtained. err may be nil when the cause is not an API failure.
//
// Refusing rather than downgrading is the whole point of this path, and the cost
// asymmetry is what settles it: a false refusal blocks a migration that says
// exactly how to unblock it, while a false acceptance promotes mirrors against a
// source that was never fenced.
//
// sources names every CR that declares hot-reload, and is what makes the remedy
// actionable: telling an operator to disable hot-reload "on the gateway" is the
// wrong instruction when the declaration lives in a file kcp is about to apply,
// because the fence apply would put it straight back.
func unverifiableHotReloadError(gatewayName string, sources []string, cause, remedy string, err error) error {
	declaredIn := joinPhrases(sources)
	disable := fmt.Sprintf("set spec.hotReload.enabled: false in %s for the duration of the migration", declaredIn)

	if err != nil {
		return fmt.Errorf(refusalPreamble+". %s: %w. To proceed, either %s, or %s",
			gatewayName, declaredIn, cause, err, remedy, disable)
	}
	return fmt.Errorf(refusalPreamble+". %s. To proceed, either %s, or %s",
		gatewayName, declaredIn, cause, remedy, disable)
}

// joinPhrases renders a list of CR names as prose: "a", "a and b", "a, b and c".
func joinPhrases(phrases []string) string {
	switch len(phrases) {
	case 0:
		return "the gateway CR"
	case 1:
		return phrases[0]
	default:
		return strings.Join(phrases[:len(phrases)-1], ", ") + " and " + phrases[len(phrases)-1]
	}
}

// checkHotReloadPresenceAgrees refuses a set of planned CRs where some mention
// spec.hotReload and others do not, while the gateway has hot-reload on.
//
// This exists because "an absent declaration inherits" stops being true once kcp
// has declared the field itself. Measured against a real CFK cluster: an apply
// from kcp's field manager that declares spec.hotReload takes ownership of it, and
// a later apply from the same manager that omits it *deletes* the field. So a
// fenced CR declaring hot-reload followed by a switchover CR that omits it turns
// hot-reload off at switchover — mid-migration, with traffic already fenced.
//
// Only the enabled case is checked. With hot-reload off there is nothing harmful
// to prune: absent and false are the same behaviour, so deleting an explicit false
// changes nothing.
//
// Checked symmetrically even though only one order is harmful. The safe order is
// safe only because of when the applies happen, and a rule resting on that decays
// the first time the sequence changes — while an operator who mentions the field
// in one file has no reason to omit it from the other.
func checkHotReloadPresenceAgrees(gatewayName string, liveHotReload bool, declarations map[string]hotReloadDeclaration) error {
	if !liveHotReload {
		return nil
	}

	var declaring, silent []string
	for _, role := range []string{roleFenced, roleSwitchover} {
		if declarations[role].present {
			declaring = append(declaring, fmt.Sprintf("the %s gateway CR", role))
			continue
		}
		silent = append(silent, fmt.Sprintf("the %s gateway CR", role))
	}
	if len(declaring) == 0 || len(silent) == 0 {
		return nil
	}

	return fmt.Errorf("cannot start a migration that would change gateway %q's hot-reload behaviour: %s "+
		"declares spec.hotReload while %s omits it, and the gateway has hot-reload enabled. kcp applies these CRs in "+
		"turn, and server-side apply deletes a field an earlier apply from the same field manager declared once a later "+
		"one leaves it out — so hot-reload would be switched off part-way through the migration. To proceed, either "+
		"declare spec.hotReload.enabled: true in both, or omit it from both so each inherits the gateway's setting",
		gatewayName, joinPhrases(declaring), joinPhrases(silent))
}

// Capability records what the live cluster supports, and therefore how kcp will
// verify each Gateway state transition.
type Capability struct {
	// Mode is the verification strategy to use. Named for the evidence
	// available, not for the pod-update type: both a hot-reload and a roll are
	// verifiable under VerifyPerPodConfigID.
	Mode VerificationMode

	// HotReloadEnabled reports whether spec.hotReload.enabled: true is declared by
	// the live Gateway CR *or* by either of the CRs kcp will apply during the
	// migration. This is the primary discriminator, and the only one always
	// available: the live read is namespaced and the files are already in hand, so
	// unlike the CRD it is never the thing an operator lacks permission for.
	HotReloadEnabled bool

	// HotReloadDeclaredIn names the CRs that declare it, as prose fragments
	// ("the fenced gateway CR"). Empty when hot-reload is off everywhere. Carried
	// so an advisory or a refusal can point at the file that has to change rather
	// than at "the gateway" — which is the wrong instruction when the declaration
	// lives in a file the fence apply would put straight back.
	HotReloadDeclaredIn []string

	// CRDSupportsConfigID reports whether the installed operator's Gateway CRD
	// declares spec.configId. Only meaningful when HotReloadEnabled is true,
	// since the CRD is not read otherwise.
	CRDSupportsConfigID bool

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
func (s *K8sService) DetectCapability(ctx context.Context, namespace, gatewayName string, port int, fencedYAML, switchoverYAML []byte) (Capability, error) {
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

	return detectCapability(ctx, dynamicClient, clientset, probe, namespace, gatewayName, fencedYAML, switchoverYAML)
}

// detectCapability is the inner orchestration used by DetectCapability. Split
// from the method so unit tests can inject a fake dynamic client.
//
// spec.hotReload.enabled is the primary discriminator, and every other gate is
// subordinate to it. The invariant this enforces:
//
//	kcp must never select VerifyRollout while spec.hotReload.enabled is true.
//
// Those two states contradict each other. VerifyRollout's only evidence is a bump
// in the backing Deployment's metadata.generation, and a hot-reload by definition
// does not produce one — CFK rewrites the ConfigMap and projects it into the
// running pods without touching the pod template. So the rollout wait observes no
// roll, concludes there is nothing to converge on, and returns success having
// verified nothing (see observeRolloutMechanism in rollout_mechanism.go). That is
// not a slow failure or a hang: it is a fast, silent false success, and the
// transition it green-lights may be a fence that never reached the gateway.
//
// Hence the shape below. Hot-reload off: every remaining gate is irrelevant,
// because kcp will not inject a configId, and CFK will roll the pods — so
// VerifyRollout is correct rather than degraded, and the cluster-scoped CRD read
// is never even attempted. Hot-reload on: every remaining gate is a hard
// requirement, and failing one is a refusal rather than a downgrade.
//
// Note that failure to *confirm* configId support is treated identically to
// confirmed absence. Keying on the conclusion rather than the reason is what
// makes this correct: a 403 on the CRD read and a CFK build whose CRD genuinely
// predates spec.configId (0.1718.10 declares spec.hotReload.enabled but not
// spec.configId) leave kcp in exactly the same position — hot-reload on, no way
// to verify — however different their remedies are.
func detectCapability(ctx context.Context, dynamicClient dynamic.Interface, clientset kubernetes.Interface, probe podProber, namespace, gatewayName string, fencedYAML, switchoverYAML []byte) (Capability, error) {
	if err := ctx.Err(); err != nil {
		return Capability{}, err
	}

	gatewayGVR := schema.GroupVersionResource{
		Group:    GatewayGroup,
		Version:  GatewayVersion,
		Resource: GatewayResourcePlural,
	}

	// Gate 1: has the operator declared hot-reload on the live CR? kcp never sets
	// this field itself — it is the user's declared intent, and the one input that
	// decides whether the pods will roll at all. A namespaced read, so it is
	// available wherever kcp can migrate at all.
	gw, err := dynamicClient.Resource(gatewayGVR).Namespace(namespace).Get(ctx, gatewayName, metav1.GetOptions{})
	if err != nil {
		return Capability{}, fmt.Errorf("failed to get gateway %q for capability detection: %w", gatewayName, err)
	}

	// The live gateway decides the mode, and the CRs kcp is about to apply are held
	// to it. They are not a second opinion to be merged in: kcp applies them, so a
	// declaration that disagrees is a behaviour change kcp would be making to the
	// operator's running gateway. Refuse it and say which file, rather than picking.
	liveHotReload := hotReloadEnabledInCR(gw)

	var sources []string
	if liveHotReload {
		sources = append(sources, "the live gateway CR")
	}
	declarations := map[string]hotReloadDeclaration{}
	for _, planned := range []plannedGatewayCR{{roleFenced, fencedYAML}, {roleSwitchover, switchoverYAML}} {
		declared, err := hotReloadDeclaredInYAML(planned)
		if err != nil {
			return Capability{}, err
		}
		if declared.present && declared.enabled != liveHotReload {
			return Capability{}, hotReloadDiscrepancyError(gatewayName, planned.role, liveHotReload, declared.enabled)
		}
		declarations[planned.role] = declared
		if declared.present && declared.enabled {
			sources = append(sources, fmt.Sprintf("the %s gateway CR", planned.role))
		}
	}

	if err := checkHotReloadPresenceAgrees(gatewayName, liveHotReload, declarations); err != nil {
		return Capability{}, err
	}

	detected := Capability{
		HotReloadEnabled:    liveHotReload,
		HotReloadDeclaredIn: sources,
	}

	if !detected.HotReloadEnabled {
		// CFK will roll the pods, so a rollout is exactly what there is to observe.
		detected.Mode = VerifyRollout
		detected.Advisory = advisoryHotReloadDisabled
		logDetectedCapability(namespace, gatewayName, detected)
		return detected, nil
	}

	crdGVR := schema.GroupVersionResource{
		Group:    CRDGroup,
		Version:  CRDVersion,
		Resource: CRDResourcePlural,
	}

	// Gate 2: does the installed operator's CRD declare spec.configId? Reached
	// only with hot-reload on, which is also the only case where kcp would write
	// the field.
	crd, err := dynamicClient.Resource(crdGVR).Get(ctx, GatewayCRDName, metav1.GetOptions{})
	switch {
	case apierrors.IsNotFound(err):
		// Defensive: with the gates in this order, a cluster with no Gateway CRD
		// fails the CR read above first. Kept so the branch cannot become a silent
		// downgrade if that ordering ever changes.
		return Capability{}, unverifiableHotReloadError(gatewayName, sources, causeCRDAbsent, remedyUpgradeCFK, nil)
	case apierrors.IsForbidden(err):
		slog.Debug("not permitted to read the gateway CRD", "crd", GatewayCRDName, "error", err)
		return Capability{}, unverifiableHotReloadError(gatewayName, sources, causeCRDForbidden, remedyCRDForbidden, nil)
	case err != nil:
		return Capability{}, fmt.Errorf("failed to get Gateway CRD %q: %w", GatewayCRDName, err)
	}

	detected.CRDSupportsConfigID = crdSupportsConfigID(crd)
	if !detected.CRDSupportsConfigID {
		return Capability{}, unverifiableHotReloadError(gatewayName, sources, causeNoConfigIDInCRD, remedyUpgradeCFK, nil)
	}

	// Gate 3: do the running pods actually serve GET /config? The operator and the
	// gateway are versioned separately, so a CRD that declares configId says
	// nothing about the image the pods are running. Only this gate needs the
	// network, which is why it is last: an operator who never wanted hot-reload
	// never reaches it.
	served, err := configEndpointServed(ctx, clientset, probe, namespace, gatewayName)
	if err != nil {
		return Capability{}, err
	}
	detected.ConfigEndpointServed = served
	if !served {
		return Capability{}, unverifiableHotReloadError(gatewayName, sources, causeNoConfigEndpoint, remedyUpgradeGateway, nil)
	}

	detected.Mode = VerifyPerPodConfigID
	logDetectedCapability(namespace, gatewayName, detected)
	return detected, nil
}

func logDetectedCapability(namespace, gatewayName string, detected Capability) {
	slog.Debug("resolved gateway verification mode",
		"gateway", gatewayName, "namespace", namespace, "mode", detected.Mode,
		"crdSupportsConfigId", detected.CRDSupportsConfigID, "hotReloadEnabled", detected.HotReloadEnabled,
		"hotReloadDeclaredIn", detected.HotReloadDeclaredIn, "configEndpointServed", detected.ConfigEndpointServed)
}

// configEndpointServed reports whether the gateway pods serve GET /config.
//
// The distinction this draws is between a capability problem and an environment
// problem, and it is the reason a single call is enough:
//
//   - every ready pod answers 404 → the image predates the endpoint (it arrived
//     in gateway 1.3.0). Reported as false, which the caller turns into a refusal:
//     this is reached only with hot-reload on, and the two together are the
//     licence-gated case where CFK reports a successful hot-reload that the
//     gateway never applied.
//   - any pod answers 200 → the endpoint is live. A mixed answer means an image
//     change is mid-flight, which is itself a rollout; treating that as capable is
//     right, because by the time kcp verifies anything the new pods are serving.
//   - nothing is reachable → the pod network is not routable from here. That is
//     an error, not a false: kcp cannot reach the pods it would need to verify,
//     and silently falling back would hide it until a switchover.
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

// plannedGatewayCR is a CR file kcp will apply during the migration, tagged with
// the role it plays so a finding can name the file the operator has to fix.
type plannedGatewayCR struct {
	role string
	yaml []byte
}

// hotReloadDeclaration is what a planned CR says about spec.hotReload.enabled.
//
// Absence is tracked separately from the value because the two mean different
// things to server-side apply. A CR that omits the field does not clear it: SSA
// leaves fields the applier does not own alone, so the gateway keeps whatever it
// was running. That makes an absent declaration a no-op, and a no-op is always
// allowed. Every example under docs/assets/gateway-switchover is this shape.
type hotReloadDeclaration struct {
	present bool
	enabled bool
}

// hotReloadDeclaredInYAML reads what a gateway CR file kcp is going to apply says
// about spec.hotReload.enabled.
//
// NestedBool is safe on a goccy-decoded tree where NestedMap would not be: it
// reads through without deep-copying, so the uint64 goccy produces for a positive
// integer never reaches DeepCopyJSONValue.
//
// A file that cannot be parsed is an error rather than an absent declaration.
// Absence and unreadability are different facts, and collapsing them would let an
// unreadable CR through as though it agreed with the gateway.
func hotReloadDeclaredInYAML(planned plannedGatewayCR) (hotReloadDeclaration, error) {
	if len(bytes.TrimSpace(planned.yaml)) == 0 {
		return hotReloadDeclaration{}, nil
	}

	var obj map[string]any
	if err := yaml.Unmarshal(planned.yaml, &obj); err != nil {
		return hotReloadDeclaration{}, fmt.Errorf("failed to read spec.hotReload.enabled from the %s gateway CR: %w", planned.role, err)
	}

	// A malformed value (a string where the CRD wants a bool) reads as absent: the
	// API server would reject it on apply, which is a better error than anything
	// kcp could say here, and "inherits" is the harmless interpretation.
	enabled, found, err := unstructured.NestedBool(obj, "spec", "hotReload", "enabled")
	if err != nil || !found {
		return hotReloadDeclaration{}, nil
	}

	return hotReloadDeclaration{present: true, enabled: enabled}, nil
}

// hotReloadDiscrepancyError is the refusal for a planned CR that would change the
// gateway's hot-reload behaviour.
//
// kcp will not make that change on the operator's behalf in either direction.
// Enabling it converts every later transition to an in-place apply on a gateway
// that was rolling pods; disabling it starts rolling pods on a gateway that was
// not. Both are changes to how a running service handles traffic, decided by a
// file kcp happens to be applying — so kcp stops and explains instead of picking.
func hotReloadDiscrepancyError(gatewayName, role string, live, planned bool) error {
	consequence := "start rolling the gateway pods on every transition"
	if planned {
		consequence = "stop CFK rolling the pods and have it apply every later change in place"
	}

	return fmt.Errorf("cannot start a migration that would change gateway %q's hot-reload behaviour: "+
		"the %s gateway CR declares spec.hotReload.enabled: %t while the gateway has it %t. Applying that CR would %s, "+
		"which kcp will not do on your behalf. To proceed, either omit spec.hotReload from the %s gateway CR so it "+
		"inherits the gateway's setting, make it declare %t to match, or set spec.hotReload.enabled: %t on the gateway "+
		"itself first",
		gatewayName, role, planned, live, consequence, role, live, planned)
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
