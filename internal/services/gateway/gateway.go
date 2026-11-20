package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/confluentinc/kcp/internal/tracing"
	"github.com/goccy/go-yaml"
	"go.opentelemetry.io/otel/attribute"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// Kubernetes resource constants
const (
	// ConfluentNamespace     = "kcp"
	GatewayGroup          = "platform.confluent.io"
	GatewayVersion        = "v1beta1"
	GatewayResourcePlural = "gateways"
	GatewayKind           = "Gateway"
)

// ErrApplyUnverified marks a JSON Patch the API server accepted and
// persisted, whose stored spec.configId kcp could not then confirm. Unlike an
// error from the Patch call itself, the CR reached the cluster: a caller that
// fails here must treat the patch as landed, not as a no-op.
var ErrApplyUnverified = errors.New("gateway CR applied but the stored configId could not be confirmed")

// TransitionClient is what applying and confirming a gateway transition needs from the cluster.
type TransitionClient interface {
	DetectCapability(ctx context.Context, namespace, gatewayName string, port int, fencedYAML, switchoverYAML []byte) (Capability, error)
	GetGatewayGeneration(ctx context.Context, namespace, gatewayName string) (int64, error)
	GetGatewayDeploymentGeneration(ctx context.Context, namespace, gatewayName string) (int64, error)
	GetGatewayPodUIDs(ctx context.Context, namespace, gatewayName string) (map[types.UID]struct{}, error)
	PatchGatewayRoute(ctx context.Context, namespace, gatewayName string, rp RoutePatch, configID string, expectedGeneration int64) (PatchResult, error)
	PatchGatewayConfigID(ctx context.Context, namespace, gatewayName, configID string, expectedGeneration int64) (PatchResult, error)
	WaitForGatewayAccepted(ctx context.Context, namespace, gatewayName string, pollInterval, timeout time.Duration) error
	WaitForGatewayConfigID(ctx context.Context, namespace, gatewayName string, opts ConfigWaitOptions) error
	WaitForGatewayPods(ctx context.Context, namespace, gatewayName string, initialPodUIDs map[types.UID]struct{}, baselineGeneration int64, pollInterval, timeout time.Duration, onProgress func(PodRolloutProgress)) error
}

// PatchResult is what a Gateway CR patch reports back. Generation is the
// response's metadata.generation whenever the patch reached the cluster,
// including alongside ErrApplyUnverified; it is 0 when nothing was written.
type PatchResult struct {
	ConfigID   string
	Generation int64
}

// PodRolloutProgress reports the current state of a pod rollout
type PodRolloutProgress struct {
	InitialPodCount  int
	NewPodsReady     int
	OldPodsRemaining int
	RolloutDetected  bool

	// Elapsed is the time since the pod-replacement wait began.
	Elapsed time.Duration
	// OldPodNames are the still-listed, non-terminal pods captured before the
	// apply (sorted); they are what the wait is blocked on.
	OldPodNames []string
}

// K8sService implements gateway operations using Kubernetes clients. Its
// clients are built lazily on first use and then reused for the life of the
// service. Construct it with NewK8sService or NewK8sServiceWithClients.
type K8sService struct {
	// clients is shared by pointer so a copy of the service reuses the same
	// clients and never copies a sync.Once.
	clients *k8sClients
}

// k8sClients holds the lazily built clients. Each build runs at most once and
// caches its error, so a bad kubeconfig fails the same way on every call.
type k8sClients struct {
	kubeConfigPath string
	// restConfig builds the REST config; RESTConfig outside tests.
	restConfig func(string) (*rest.Config, error)

	configOnce sync.Once
	config     *rest.Config
	configErr  error

	dynamicOnce sync.Once
	dynamic     dynamic.Interface
	dynamicErr  error

	clientsetOnce sync.Once
	clientset     kubernetes.Interface
	clientsetErr  error
}

// NewK8sService creates a new gateway service for kubeConfigPath. Nothing is
// read until the first method that needs a client runs.
func NewK8sService(kubeConfigPath string) *K8sService {
	return &K8sService{clients: &k8sClients{
		kubeConfigPath: kubeConfigPath,
		restConfig:     RESTConfig,
	}}
}

// NewK8sServiceWithClients creates a gateway service over already built
// clients; no kubeconfig is read.
func NewK8sServiceWithClients(dynamicClient dynamic.Interface, clientset kubernetes.Interface) *K8sService {
	c := &k8sClients{dynamic: dynamicClient, clientset: clientset}
	c.configOnce.Do(func() {})
	c.dynamicOnce.Do(func() {})
	c.clientsetOnce.Do(func() {})
	if dynamicClient == nil {
		c.dynamicErr = errors.New("gateway service built without a dynamic client")
	}
	if clientset == nil {
		c.clientsetErr = errors.New("gateway service built without a clientset")
	}
	return &K8sService{clients: c}
}

// Clientset returns the service's typed Kubernetes client, building it on
// first use.
func (s *K8sService) Clientset() (kubernetes.Interface, error) {
	return s.clients.getClientset()
}

// dynamicClient returns the service's dynamic client, building it on first use.
func (s *K8sService) dynamicClient() (dynamic.Interface, error) {
	return s.clients.getDynamic()
}

func (c *k8sClients) restConfigOnce() (*rest.Config, error) {
	c.configOnce.Do(func() {
		config, err := c.restConfig(c.kubeConfigPath)
		if err != nil {
			c.configErr = fmt.Errorf("failed to build config: %w", err)
			return
		}
		c.config = config
	})
	return c.config, c.configErr
}

func (c *k8sClients) getDynamic() (dynamic.Interface, error) {
	c.dynamicOnce.Do(func() {
		config, err := c.restConfigOnce()
		if err != nil {
			c.dynamicErr = err
			return
		}
		client, err := dynamic.NewForConfig(config)
		if err != nil {
			c.dynamicErr = fmt.Errorf("failed to create dynamic client: %w", err)
			return
		}
		c.dynamic = client
	})
	return c.dynamic, c.dynamicErr
}

func (c *k8sClients) getClientset() (kubernetes.Interface, error) {
	c.clientsetOnce.Do(func() {
		config, err := c.restConfigOnce()
		if err != nil {
			c.clientsetErr = err
			return
		}
		client, err := kubernetes.NewForConfig(config)
		if err != nil {
			c.clientsetErr = fmt.Errorf("failed to create clientset: %w", err)
			return
		}
		c.clientset = client
	})
	return c.clientset, c.clientsetErr
}

// GetGatewayYAML retrieves the gateway resource as YAML
func (s *K8sService) GetGatewayYAML(ctx context.Context, namespace, gatewayName string) ([]byte, error) {
	dynamicClient, err := s.dynamicClient()
	if err != nil {
		return nil, err
	}

	gatewayGVR := schema.GroupVersionResource{
		Group:    GatewayGroup,
		Version:  GatewayVersion,
		Resource: GatewayResourcePlural,
	}

	gateway, err := dynamicClient.Resource(gatewayGVR).Namespace(namespace).
		Get(ctx, gatewayName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get Gateway: %w", err)
	}

	yamlBytes, err := yaml.Marshal(gateway.Object)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal to YAML: %w", err)
	}

	slog.Debug("fetched gateway CR", "namespace", namespace, "gateway", gatewayName, "bytes", len(yamlBytes))
	return yamlBytes, nil
}

// PatchGatewayRoute applies a single route mutation to the gateway CR as an RFC
// 6902 JSON Patch — touching only the one route field (or, for unfence, the one
// route element) rp describes, plus spec.configId when configID is non-empty.
// Unlike a server-side apply it takes no field ownership, so it never prunes a
// field through ownership tracking the way SSA can. The unfence whole-route
// replace is a separate matter: it supplants the entire route element with the
// snapshot captured at Initialize, so it does discard any field added to that
// route after the snapshot was taken — that's the point of unfence (restore the
// pristine route), not a pruning side effect.
//
// The live CR is read first to resolve rp.RouteName to its spec.routes index; a
// test op in the patch guards that index against a concurrent reorder, and a
// second test op guards the current value of whatever the mutation is about to
// overwrite (see buildRoutePatchOps). Missing spec.routes on the live CR is a
// hard failure here — a deliberate departure from the old server-side-apply
// write, which would have self-healed by re-applying the full captured CR; a
// narrow JSON Patch has nothing to fall back to. The returned string is the
// configId the API server stored (empty when none sent).
//
// When expectedGeneration > 0 the write is conditional on the CR's
// metadata.generation still being that value: a pre-patch read refuses a CR that
// has already moved, and a test op in the patch refuses one that moves between
// the read and the write. Both return a *GenerationChangedError (Is
// ErrGatewayChanged). 0 means unknown and leaves the write unguarded.
func (s *K8sService) PatchGatewayRoute(ctx context.Context, namespace, gatewayName string, rp RoutePatch, configID string, expectedGeneration int64) (PatchResult, error) {
	dynamicClient, err := s.dynamicClient()
	if err != nil {
		return PatchResult{}, err
	}
	return patchGatewayRoute(ctx, dynamicClient, namespace, gatewayName, rp, configID, expectedGeneration)
}

// patchGatewayRoute is the inner orchestration used by PatchGatewayRoute.
// Split from the method so unit tests can inject a fake dynamic client.
func patchGatewayRoute(ctx context.Context, dynamicClient dynamic.Interface, namespace, gatewayName string, rp RoutePatch, configID string, expectedGeneration int64) (PatchResult, error) {
	gatewayGVR := schema.GroupVersionResource{Group: GatewayGroup, Version: GatewayVersion, Resource: GatewayResourcePlural}

	live, err := dynamicClient.Resource(gatewayGVR).Namespace(namespace).Get(ctx, gatewayName, metav1.GetOptions{})
	if err != nil {
		return PatchResult{}, fmt.Errorf("failed to read gateway before patch: %w", err)
	}
	if expectedGeneration > 0 && live.GetGeneration() != expectedGeneration {
		return PatchResult{}, &GenerationChangedError{Namespace: namespace, Gateway: gatewayName, Expected: expectedGeneration, Observed: live.GetGeneration()}
	}
	routes, found, err := unstructured.NestedSlice(live.Object, "spec", "routes")
	if err != nil {
		return PatchResult{}, fmt.Errorf("failed to read spec.routes on gateway %q: %w", gatewayName, err)
	}
	if !found {
		return PatchResult{}, fmt.Errorf("gateway %q has no spec.routes", gatewayName)
	}

	ops, err := buildRoutePatchOps(routes, rp, configID)
	if err != nil {
		return PatchResult{}, err
	}
	if expectedGeneration > 0 {
		ops = append([]jsonPatchOp{generationTestOp(expectedGeneration)}, ops...)
	}
	data, err := json.Marshal(ops)
	if err != nil {
		return PatchResult{}, fmt.Errorf("failed to marshal gateway JSON patch: %w", err)
	}

	slog.Debug("patching gateway CR (JSON patch)", "namespace", namespace, "gateway", gatewayName, "route", rp.RouteName, "field", rp.Field, "configId", configID)
	start := time.Now()
	patched, err := dynamicClient.Resource(gatewayGVR).Namespace(namespace).
		Patch(ctx, gatewayName, types.JSONPatchType, data, metav1.PatchOptions{})
	if err != nil {
		return PatchResult{}, patchFailure(ctx, dynamicClient, namespace, gatewayName, expectedGeneration, fmt.Errorf("failed to patch gateway route: %w", err))
	}
	slog.Debug("patched gateway CR", "namespace", namespace, "gateway", gatewayName, "ms", time.Since(start).Milliseconds())

	result := PatchResult{Generation: patched.GetGeneration()}
	if configID == "" {
		return result, nil
	}
	result.ConfigID, err = confirmStoredConfigID(patched, gatewayName, configID)
	return result, err
}

// patchFailure turns a failed guarded patch into a *GenerationChangedError when
// the CR's generation has moved off the expected value, which is the likely
// reason the test op refused it. If the re-read fails or the generation is
// unchanged, the patch's own error is returned untouched.
func patchFailure(ctx context.Context, dynamicClient dynamic.Interface, namespace, gatewayName string, expectedGeneration int64, patchErr error) error {
	if expectedGeneration <= 0 {
		return patchErr
	}
	observed, err := getGatewayGeneration(ctx, dynamicClient, namespace, gatewayName)
	if err != nil {
		slog.Debug("could not re-read gateway generation after a failed patch", "namespace", namespace, "gateway", gatewayName, "error", err)
		return patchErr
	}
	if observed != expectedGeneration {
		return &GenerationChangedError{Namespace: namespace, Gateway: gatewayName, Expected: expectedGeneration, Observed: observed}
	}
	return patchErr
}

// GetGatewayGeneration returns the Gateway CR's current metadata.generation.
func (s *K8sService) GetGatewayGeneration(ctx context.Context, namespace, gatewayName string) (int64, error) {
	dynamicClient, err := s.dynamicClient()
	if err != nil {
		return 0, err
	}
	return getGatewayGeneration(ctx, dynamicClient, namespace, gatewayName)
}

// getGatewayGeneration is the inner read used by GetGatewayGeneration.
// Split from the method so unit tests can inject a fake dynamic client.
func getGatewayGeneration(ctx context.Context, dynamicClient dynamic.Interface, namespace, gatewayName string) (int64, error) {
	gatewayGVR := schema.GroupVersionResource{Group: GatewayGroup, Version: GatewayVersion, Resource: GatewayResourcePlural}
	live, err := dynamicClient.Resource(gatewayGVR).Namespace(namespace).Get(ctx, gatewayName, metav1.GetOptions{})
	if err != nil {
		return 0, fmt.Errorf("failed to read gateway %q generation: %w", gatewayName, err)
	}
	return live.GetGeneration(), nil
}

// PatchGatewayConfigID stamps spec.configId alone on the gateway as a JSON Patch
// for the hot-reload capability check. Returns the stored configId and the
// resulting generation. expectedGeneration guards the write exactly as in
// PatchGatewayRoute; 0 leaves it unguarded.
func (s *K8sService) PatchGatewayConfigID(ctx context.Context, namespace, gatewayName, configID string, expectedGeneration int64) (PatchResult, error) {
	dynamicClient, err := s.dynamicClient()
	if err != nil {
		return PatchResult{}, err
	}
	return patchGatewayConfigID(ctx, dynamicClient, namespace, gatewayName, configID, expectedGeneration)
}

// patchGatewayConfigID is the inner orchestration used by PatchGatewayConfigID.
// Split from the method so unit tests can inject a fake dynamic client.
func patchGatewayConfigID(ctx context.Context, dynamicClient dynamic.Interface, namespace, gatewayName, configID string, expectedGeneration int64) (PatchResult, error) {
	gatewayGVR := schema.GroupVersionResource{Group: GatewayGroup, Version: GatewayVersion, Resource: GatewayResourcePlural}

	op, err := configIDOp(configID)
	if err != nil {
		return PatchResult{}, err
	}
	ops := []jsonPatchOp{op}
	if expectedGeneration > 0 {
		observed, err := getGatewayGeneration(ctx, dynamicClient, namespace, gatewayName)
		if err != nil {
			return PatchResult{}, fmt.Errorf("failed to read gateway before patch: %w", err)
		}
		if observed != expectedGeneration {
			return PatchResult{}, &GenerationChangedError{Namespace: namespace, Gateway: gatewayName, Expected: expectedGeneration, Observed: observed}
		}
		ops = append([]jsonPatchOp{generationTestOp(expectedGeneration)}, ops...)
	}
	data, err := json.Marshal(ops)
	if err != nil {
		return PatchResult{}, fmt.Errorf("failed to marshal gateway configId patch: %w", err)
	}

	slog.Debug("patching gateway configId (JSON patch)", "namespace", namespace, "gateway", gatewayName, "configId", configID)
	patched, err := dynamicClient.Resource(gatewayGVR).Namespace(namespace).
		Patch(ctx, gatewayName, types.JSONPatchType, data, metav1.PatchOptions{})
	if err != nil {
		return PatchResult{}, patchFailure(ctx, dynamicClient, namespace, gatewayName, expectedGeneration, fmt.Errorf("failed to patch gateway configId: %w", err))
	}
	result := PatchResult{Generation: patched.GetGeneration()}
	result.ConfigID, err = confirmStoredConfigID(patched, gatewayName, configID)
	return result, err
}

// confirmStoredConfigID reads spec.configId back from a JSON Patch response
// and confirms the server kept the exact value kcp sent.
//
// Every failure here is wrapped in ErrApplyUnverified: by this point the
// patch itself already succeeded, so the CR is live in the cluster whether or
// not the confirmation below passes — callers must not treat a failure here
// as if nothing had reached the cluster (see gateway.FenceGateway's use of
// this same wrap).
//
// A silently dropped configId would otherwise leave every subsequent GET
// /config poll waiting for a revision that never appears, so this fails here
// with the cause rather than later with a bare timeout.
func confirmStoredConfigID(applied *unstructured.Unstructured, gatewayName, configID string) (string, error) {
	if applied == nil {
		return "", fmt.Errorf("%w: gateway %q patch returned no object; cannot confirm the applied configId", ErrApplyUnverified, gatewayName)
	}
	stored, found, err := unstructured.NestedString(applied.Object, "spec", gatewayConfigIDField)
	if err != nil {
		return "", fmt.Errorf("%w: failed to read back spec.configId on gateway %q: %w", ErrApplyUnverified, gatewayName, err)
	}
	if !found || stored != configID {
		// Not "the CRD may not declare it" — a JSON Patch `add`/`replace` on
		// spec.configId fails outright when the CRD doesn't declare the field,
		// so a successful patch that still lost the field points at something
		// else in the request path (a mutating webhook, a conflicting controller).
		return "", fmt.Errorf("%w: gateway %q's stored spec.configId does not match what kcp patched", ErrApplyUnverified, gatewayName)
	}

	return stored, nil
}

// GatewayRejectedError reports that the CFK operator processed the applied
// Gateway CR and refused it. It carries the operator's own condition so the
// caller can surface the actual cause (a missing secretRef, an invalid spec)
// instead of a bare timeout.
//
// This is a terminal outcome, not a transient one: observedGeneration will
// never catch up to generation until the underlying problem is fixed and the
// CR re-applied, so waiting longer cannot help.
type GatewayRejectedError struct {
	Gateway            string
	ConditionType      string
	Reason             string
	Message            string
	Generation         int64
	ObservedGeneration int64
}

func (e *GatewayRejectedError) Error() string {
	return fmt.Sprintf("gateway %q was rejected by the Confluent operator: %s (reason: %s, condition: %s, generation: %d, observedGeneration: %d)",
		e.Gateway, e.Message, e.Reason, e.ConditionType, e.Generation, e.ObservedGeneration)
}

// gatewayRejectionSettleWindow is how long a failing condition must persist
// before WaitForGatewayAccepted treats it as a rejection rather than a
// transient reconcile state. The operator can briefly publish a failure while
// it retries (and a condition left over from the previous generation's
// reconcile can be momentarily stale), so we require the failure to hold
// before aborting a migration on it. var so tests can shorten it.
var gatewayRejectionSettleWindow = 30 * time.Second

// gatewayFatalConditionReasons are Gateway condition reasons known to mean the
// operator processed the spec and refused it. ApplyFailed is the one observed
// against a real CFK operator: a switchover CR referencing a missing k8s secret produced
// {"type":"platform.confluent.io/cluster-ready","status":"False",
// "reason":"ApplyFailed","message":"secretRef ... not found"} while
// observedGeneration stayed one behind generation.
//
// Adding a reason here makes kcp abort a migration on it — a deliberate call,
// not a drive-by. Reasons outside this set (and outside the <Verb>Failed
// convention below) are treated as "still reconciling" and waited on.
var gatewayFatalConditionReasons = map[string]struct{}{
	"ApplyFailed": {},
}

// isFatalGatewayConditionReason reports whether a False condition's reason
// means the operator refused the spec. Beyond the catalogued reasons it honours
// CFK's <Verb>Failed naming convention (ApplyFailed, CreateFailed,
// ValidationFailed, ...) so an uncatalogued failure still fails fast with the
// operator's message rather than hanging.
func isFatalGatewayConditionReason(reason string) bool {
	if _, ok := gatewayFatalConditionReasons[reason]; ok {
		return true
	}
	return strings.HasSuffix(reason, "Failed")
}

// WaitForGatewayAccepted blocks until the CFK operator has accepted the current
// Gateway CR spec — status.observedGeneration >= metadata.generation — or
// returns a *GatewayRejectedError if the operator refuses it. timeout == 0
// means no deadline (consistent with the other gateway waits).
//
// This is the guard that makes a downstream "no rollout observed" trustworthy.
// Applying a CR bumps the Gateway's metadata.generation; until the operator
// reconciles it (and, for a real spec change, writes the Deployment)
// observedGeneration lags. Without this wait, the Deployment-based wait
// (WaitForGatewayPods) see a perfectly healthy Deployment
// still running the *previous* generation's pods, and conclude "no restart
// required" — reporting success for a switchover or fence that never happened.
// The generation baseline those waits compare against cannot close this on its
// own: a Deployment the operator has not looked at yet has the baseline
// generation, which is indistinguishable from one it looked at and left alone.
// A no-op apply (e.g. a resume re-applying an already-fenced CR) does not bump
// generation, so observedGeneration already satisfies the check and this returns
// immediately.
//
// Watching observedGeneration alone is not enough: when the operator *rejects*
// the spec, observedGeneration never advances, so with the default timeout of 0
// the wait would block indefinitely. Polling status.conditions alongside it
// turns that hang into a fast, actionable error carrying the operator's own
// message.
func (s *K8sService) WaitForGatewayAccepted(ctx context.Context, namespace, gatewayName string, pollInterval, timeout time.Duration) error {
	dynamicClient, err := s.dynamicClient()
	if err != nil {
		return err
	}

	return waitForGatewayAccepted(ctx, dynamicClient, namespace, gatewayName, pollInterval, timeout)
}

// waitForGatewayAccepted is the inner orchestration used by
// WaitForGatewayAccepted. Split from the method so unit tests can inject a
// fake dynamic client.
func waitForGatewayAccepted(ctx context.Context, dynamicClient dynamic.Interface, namespace, gatewayName string, pollInterval, timeout time.Duration) error {
	gatewayGVR := schema.GroupVersionResource{
		Group:    GatewayGroup,
		Version:  GatewayVersion,
		Resource: GatewayResourcePlural,
	}

	noDeadline := timeout <= 0
	deadline := time.Now().Add(timeout)
	// waitStartedAt anchors the lastTransitionTime staleness fallback in
	// findGatewayRejection — see conditionPredatesGeneration.
	waitStartedAt := time.Now()

	// A failing condition only aborts once it has held for the settle window.
	// firstFailureAt marks the start of the current unbroken run of failure
	// observations; it resets the moment the operator stops reporting one.
	var (
		firstFailureAt time.Time
		pending        *GatewayRejectedError
	)

	for noDeadline || time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		gw, err := dynamicClient.Resource(gatewayGVR).Namespace(namespace).Get(ctx, gatewayName, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("failed to get gateway for reconcile check: %w", err)
		}

		generation := gw.GetGeneration()
		// Before the operator writes any status, observedGeneration is absent
		// (found == false) — treat that as not-yet-reconciled and keep waiting.
		observed, found, err := unstructured.NestedInt64(gw.Object, "status", "observedGeneration")
		if err != nil {
			return fmt.Errorf("failed to read gateway status.observedGeneration: %w", err)
		}

		// Checked before the observedGeneration acceptance test below, on
		// every poll, regardless of whether observed already caught up:
		// observedGeneration alone cannot distinguish the operator accepting
		// this generation from it refusing this generation while a stale
		// condition from an earlier, resolved generation just happens to sit
		// on the CR (cfet1 #6205997094).
		rejection, err := findGatewayRejection(gw, gatewayName, generation, observed, waitStartedAt)
		if err != nil {
			return err
		}
		switch {
		case rejection != nil && firstFailureAt.IsZero():
			firstFailureAt = time.Now()
			pending = rejection
			slog.Warn("gateway condition reports a failure; waiting to see whether the operator recovers",
				"gateway", gatewayName, "reason", rejection.Reason, "message", rejection.Message,
				"settleWindow", gatewayRejectionSettleWindow)
		case rejection != nil:
			pending = rejection
			if time.Since(firstFailureAt) >= gatewayRejectionSettleWindow {
				slog.Error("gateway spec rejected by the operator", "gateway", gatewayName,
					"reason", rejection.Reason, "message", rejection.Message,
					"generation", generation, "observedGeneration", observed)
				return rejection
			}
		default:
			if !firstFailureAt.IsZero() {
				slog.Debug("gateway failure condition cleared; resuming reconcile wait", "gateway", gatewayName)
			}
			firstFailureAt = time.Time{}
			pending = nil
			if found && observed >= generation {
				slog.Debug("gateway accepted by operator", "gateway", gatewayName, "generation", generation, "observedGeneration", observed)
				return nil
			}
		}

		slog.Debug("waiting for gateway reconcile", "gateway", gatewayName, "generation", generation, "observedGeneration", observed, "statusPresent", found)

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(pollInterval):
		}
	}

	// A rejection seen but not yet settled is still the most useful thing we
	// know — surface it rather than a bare timeout. Reached when the caller's
	// timeout is shorter than the settle window.
	if pending != nil {
		return pending
	}
	return fmt.Errorf("timed out waiting for gateway %q to be observed by the operator (timeout: %s)", gatewayName, timeout)
}

// findGatewayRejection scans the Gateway CR's status.conditions for a condition
// reporting that the operator refused the spec, returning nil when none is
// present. Only status=="False" conditions with a fatal reason qualify — a
// False condition with an unrecognised reason is normal mid-reconcile noise.
//
// A fatal condition demonstrably left over from an earlier generation (see
// conditionPredatesGeneration) is skipped rather than reported: CFK does not
// always clear a resolved condition once its generation moves on, so a stale
// one must not block a later, unrelated apply from being accepted.
func findGatewayRejection(gw *unstructured.Unstructured, gatewayName string, generation, observed int64, waitStartedAt time.Time) (*GatewayRejectedError, error) {
	conditions, found, err := unstructured.NestedSlice(gw.Object, "status", "conditions")
	if err != nil {
		return nil, fmt.Errorf("failed to read gateway status.conditions: %w", err)
	}
	if !found {
		return nil, nil
	}

	for _, raw := range conditions {
		condition, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		status, _ := condition["status"].(string)
		if status != string(metav1.ConditionFalse) {
			continue
		}
		reason, _ := condition["reason"].(string)
		if !isFatalGatewayConditionReason(reason) {
			continue
		}
		if conditionPredatesGeneration(condition, generation, waitStartedAt) {
			continue
		}
		message, _ := condition["message"].(string)
		conditionType, _ := condition["type"].(string)
		return &GatewayRejectedError{
			Gateway:            gatewayName,
			ConditionType:      conditionType,
			Reason:             reason,
			Message:            message,
			Generation:         generation,
			ObservedGeneration: observed,
		}, nil
	}
	return nil, nil
}

// gatewayClockSkewTolerance guards the lastTransitionTime staleness fallback
// (conditionPredatesGeneration) against clock skew between the
// apiserver/controller clock that stamps a condition and kcp's own local
// clock, plus ordinary scheduling jitter between the patch landing and this
// wait's first poll. Without it, a condition genuinely tied to the current
// generation but stamped a fraction of a second before waitStartedAt would
// be misread as stale and skipped — reintroducing the false-acceptance risk
// this staleness check exists to close. var so tests can shorten it.
var gatewayClockSkewTolerance = 30 * time.Second

// conditionPredatesGeneration reports whether a fatal condition demonstrably
// belongs to an earlier generation than the one kcp just applied, using
// whichever evidence the condition carries, in order:
//
//  1. the condition's own observedGeneration (the standard Kubernetes
//     Condition field, set by the controller to record which generation it
//     was reconciling when it produced this condition): less than the
//     current generation means the condition predates this apply.
//  2. lacking that, the condition's lastTransitionTime against
//     waitStartedAt (the moment kcp started waiting on this apply, captured
//     immediately after issuing it): more than gatewayClockSkewTolerance
//     before it means the condition transitioned before this attempt began,
//     with enough margin that clock skew or scheduling jitter can't account
//     for the gap.
//
// A condition carrying neither piece of evidence, or a lastTransitionTime
// within the tolerance of waitStartedAt, is NOT considered stale — this is
// the accept gate's only defence against a fatal condition genuinely tied to
// the generation kcp just applied, so the safe default when the evidence is
// missing or ambiguous is to keep treating it as current (cfet1 #6205997094:
// observedGeneration catching up is not proof the CR was accepted).
func conditionPredatesGeneration(condition map[string]any, generation int64, waitStartedAt time.Time) bool {
	if condGen, found, err := unstructured.NestedInt64(condition, "observedGeneration"); err == nil && found {
		return condGen < generation
	}
	if raw, ok := condition["lastTransitionTime"].(string); ok {
		if t, err := time.Parse(time.RFC3339, raw); err == nil {
			return t.Before(waitStartedAt.Add(-gatewayClockSkewTolerance))
		}
	}
	return false
}

// GetGatewayPodUIDs returns a set of UIDs for the current gateway pods.
// This should be called BEFORE patching the gateway to capture the initial pod state.
func (s *K8sService) GetGatewayPodUIDs(ctx context.Context, namespace, gatewayName string) (map[types.UID]struct{}, error) {
	clientset, err := s.Clientset()
	if err != nil {
		return nil, err
	}

	labelSelector := fmt.Sprintf("app=%s", gatewayName)

	pods, err := clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: labelSelector,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list gateway pods: %w", err)
	}

	uids := make(map[types.UID]struct{}, len(pods.Items))
	for _, pod := range pods.Items {
		uids[pod.UID] = struct{}{}
	}
	return uids, nil
}

// WaitForGatewayPods waits for all gateway pods to be completely replaced after a config change.
//
// After patching the Gateway CRD, the Confluent operator triggers a rolling restart of gateway pods.
// This method polls until all initial pods are replaced with new pods and all are ready, ensuring
// the rollout is truly complete even when maxSurge temporarily creates extra pods.
//
// The initialPodUIDs parameter must contain the pod UIDs captured BEFORE the gateway patch is applied.
// This is critical to avoid race conditions where the rollout completes before we capture the initial state.
//
// The method works in two phases:
//  1. Observe the mechanism: watch the backing Deployment's metadata.generation
//     for a bump past baselineGeneration, which proves the operator is replacing
//     the pods (see observeRolloutMechanism).
//  2. Wait for complete replacement: Ensure all initial pods are replaced and new pods are ready
//
// This prevents returning prematurely when maxSurge creates extra pods during the rollout.
//
// baselineGeneration is the Deployment's metadata.generation captured before the
// apply, via GetGatewayDeploymentGeneration.
func (s *K8sService) WaitForGatewayPods(ctx context.Context, namespace, gatewayName string, initialPodUIDs map[types.UID]struct{}, baselineGeneration int64, pollInterval, timeout time.Duration, onProgress func(PodRolloutProgress)) (err error) {
	ctx, span := tracing.Start(ctx, tracerName, "gateway.wait_pods")
	defer func() { tracing.End(span, err) }()
	clientset, err := s.Clientset()
	if err != nil {
		return err
	}

	return waitForGatewayPods(ctx, clientset, namespace, gatewayName, initialPodUIDs, baselineGeneration, pollInterval, timeout, onProgress)
}

// waitForGatewayPods is the inner orchestration used by WaitForGatewayPods.
// Split from the method so unit tests can inject a fake clientset.
func waitForGatewayPods(ctx context.Context, clientset kubernetes.Interface, namespace, gatewayName string, initialPodUIDs map[types.UID]struct{}, baselineGeneration int64, pollInterval, timeout time.Duration, onProgress func(PodRolloutProgress)) (err error) {
	// timeout bounds the whole wait: rollout observation, pod polling and every
	// API call. parent is kept apart from the derived ctx so a deadline we set
	// can be told from the caller cancelling.
	parent := ctx
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(parent, timeout)
		defer cancel()
	}
	var lastOldPodNames []string
	// finish reports a failure from the derived deadline as the timeout error;
	// any other error (including the parent's cancellation) passes through.
	finish := func(err error) error {
		if parent.Err() == nil && errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return gatewayPodsTimeoutError(timeout, namespace, lastOldPodNames)
		}
		return err
	}

	// Confluent CFK labels gateway pods with app=<gateway-crd-name>
	labelSelector := fmt.Sprintf("app=%s", gatewayName)

	// Calculate initial pod count from the passed UIDs
	initialPodCount := len(initialPodUIDs)
	slog.Debug("waiting for gateway pod rollout", "namespace", namespace, "gateway", gatewayName, "initialPodCount", initialPodCount, "baselineGeneration", baselineGeneration)
	tracing.SetAttributes(ctx, attribute.Int("kcp.gateway.pods.captured", initialPodCount))

	// oldGone times the part of the wait a Deployment readiness check would not
	// have made: from the Deployment reporting its rollout complete until the
	// captured pods are gone. Absent from a trace when they were already gone.
	var oldGone phaseSpan
	defer func() { oldGone.leave(err) }()

	// Phase 1: establish whether the operator is replacing the pods at all.
	// The Deployment's generation is the earliest signal there is — a new pod UID
	// cannot appear before the pod template is rewritten — and unlike the pod
	// snapshot it survives a roll that completes between two polls.
	mechanism, _, err := observeRolloutMechanism(ctx, clientset, namespace, gatewayName, baselineGeneration, pollInterval)
	if err != nil {
		return finish(err)
	}

	if mechanism == MechanismNoRollObserved {
		slog.Debug("no pod rollout observed; config change did not require a pod restart")
		if onProgress != nil {
			onProgress(PodRolloutProgress{
				InitialPodCount:  initialPodCount,
				NewPodsReady:     initialPodCount,
				OldPodsRemaining: 0,
				RolloutDetected:  false,
			})
		}
		return nil
	}

	// Phase 2: Wait for all pods to be completely replaced. A timeout of 0
	// means no deadline — poll until the old pods are gone or ctx is cancelled.
	// The caller passes rolloutTimeout, which defaults to 0.
	slog.Debug("rollout detected, waiting for complete pod replacement")

	start := time.Now()

	for {
		select {
		case <-ctx.Done():
			return finish(ctx.Err())
		default:
		}

		pods, err := clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
			LabelSelector: labelSelector,
		})
		if err != nil {
			return finish(fmt.Errorf("failed to list gateway pods: %w", err))
		}

		// A captured pod still listed well past its deletion deadline will not
		// leave on its own (e.g. its node is gone); fail whatever the timeout.
		if pod, by := overduePod(pods.Items, func(p *corev1.Pod) bool {
			_, ok := initialPodUIDs[p.UID]
			return ok
		}, time.Now()); pod != nil {
			slog.Warn("replaced gateway pod is overdue", "pod", pod.Name, "node", overdueNode(pod), "overdueBy", by.Round(time.Second))
			return overdueError(namespace, pod, by)
		}

		newPodsReady := countNewReadyPods(pods.Items, initialPodUIDs)
		remainingNames := oldPodNames(pods.Items, initialPodUIDs)
		oldPodsRemaining := len(remainingNames)
		lastOldPodNames = remainingNames

		// Completion gates on two independent signals:
		//   1. every pod captured before the patch is gone (oldPodsRemaining == 0)
		//      — the guarantee a plain Deployment readiness check cannot give, since the old
		//      unfenced pod must actually stop serving; and
		//   2. the Deployment reports a complete rollout — the right number of
		//      new pods are Ready and steady.
		// We deliberately do NOT require newPodsReady to reach the pod count
		// captured before the patch: if that capture raced an in-flight rollout
		// (a surge left 2 pods where the desired count is 1), the captured count
		// is inflated and newPodsReady can never reach it — deadlocking the wait.
		// deploymentRolloutComplete reads the Deployment's own desired/ready
		// replica counts, which are immune to that race.
		dep, err := resolveGatewayDeployment(ctx, clientset, namespace, gatewayName)
		if err != nil {
			return finish(err)
		}
		rolloutComplete := deploymentRolloutComplete(dep)
		if rolloutComplete && oldPodsRemaining > 0 {
			oldGone.enter(ctx, "gateway.wait_old_pods_gone", attribute.Int("kcp.gateway.pods.old_at_start", oldPodsRemaining))
		}

		slog.Debug("pod rollout progress",
			"newPodsReady", newPodsReady,
			"oldPodsRemaining", oldPodsRemaining,
			"deploymentRolloutComplete", rolloutComplete)

		if onProgress != nil {
			onProgress(PodRolloutProgress{
				InitialPodCount:  initialPodCount,
				NewPodsReady:     newPodsReady,
				OldPodsRemaining: oldPodsRemaining,
				RolloutDetected:  true,
				Elapsed:          time.Since(start),
				OldPodNames:      remainingNames,
			})
		}

		if oldPodsRemaining == 0 && rolloutComplete {
			slog.Debug("all old gateway pods gone and deployment rollout complete")
			return nil
		}

		select {
		case <-ctx.Done():
			return finish(ctx.Err())
		case <-time.After(pollInterval):
		}
	}
}

// gatewayPodsTimeoutError builds the replaced-pod wait's timeout error, naming
// the old pods still present when there are any.
func gatewayPodsTimeoutError(timeout time.Duration, namespace string, lastOldPodNames []string) error {
	if len(lastOldPodNames) > 0 {
		return fmt.Errorf("timed out waiting for gateway pods to be replaced (timeout: %s): still present: %s; check the pod and its node, e.g. kubectl -n %s get pod %s",
			timeout, strings.Join(lastOldPodNames, ", "), namespace, lastOldPodNames[0])
	}
	return fmt.Errorf("timed out waiting for gateway pods to be replaced (timeout: %s): the Deployment rollout did not complete", timeout)
}

// countNewReadyPods counts how many new pods (not in initial set) are ready
func countNewReadyPods(currentPods []corev1.Pod, initialUIDs map[types.UID]struct{}) int {
	count := 0
	for _, pod := range currentPods {
		if _, wasInitial := initialUIDs[pod.UID]; !wasInitial && isPodReady(&pod) {
			count++
		}
	}
	return count
}

// isTerminalPod reports whether the pod has finished for good (Succeeded or
// Failed). A kubelet-evicted or node-shutdown pod lingers in the list in that
// phase, often with no DeletionTimestamp, but runs no containers and serves
// nothing, so waits that exist to see old pods stop serving must ignore it.
func isTerminalPod(pod *corev1.Pod) bool {
	return pod.Status.Phase == corev1.PodSucceeded || pod.Status.Phase == corev1.PodFailed
}

// oldPodNames returns the sorted names of the initial pods still present and
// not in a terminal phase (see isTerminalPod).
func oldPodNames(currentPods []corev1.Pod, initialUIDs map[types.UID]struct{}) []string {
	var names []string
	for i := range currentPods {
		pod := &currentPods[i]
		if _, wasInitial := initialUIDs[pod.UID]; wasInitial && !isTerminalPod(pod) {
			names = append(names, pod.Name)
		}
	}
	sort.Strings(names)
	return names
}

// isPodReady checks if a pod has the Ready condition set to True
func isPodReady(pod *corev1.Pod) bool {
	if pod.Status.Phase != corev1.PodRunning {
		return false
	}
	for _, condition := range pod.Status.Conditions {
		if condition.Type == corev1.PodReady {
			return condition.Status == corev1.ConditionTrue
		}
	}
	return false
}

// resolveGatewayDeployment finds the Deployment backing a Gateway CR.
// Primary: Get by name (CFK convention: Deployment name == Gateway CR name).
// Fallback: list Deployments in the namespace and filter by ownerReferences.
func resolveGatewayDeployment(ctx context.Context, clientset kubernetes.Interface, namespace, gatewayName string) (*appsv1.Deployment, error) {
	dep, err := clientset.AppsV1().Deployments(namespace).Get(ctx, gatewayName, metav1.GetOptions{})
	if err == nil {
		return dep, nil
	}
	if !apierrors.IsNotFound(err) {
		return nil, fmt.Errorf("failed to get gateway deployment: %w", err)
	}

	// Fallback: list and filter by ownerReferences. This scans all Deployments in
	// the namespace and is only reached when the name-based Get returns NotFound.
	slog.Debug("deployment not found by name; falling back to ownerReferences scan", "namespace", namespace, "gateway", gatewayName)
	list, err := clientset.AppsV1().Deployments(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list deployments for gateway fallback: %w", err)
	}
	var matches []appsv1.Deployment
	for _, d := range list.Items {
		for _, ref := range d.OwnerReferences {
			if ref.Kind == "Gateway" && ref.Name == gatewayName {
				matches = append(matches, d)
				break
			}
		}
	}
	switch len(matches) {
	case 1:
		return &matches[0], nil
	case 0:
		return nil, fmt.Errorf("gateway deployment not found by name or ownerReferences for gateway %q", gatewayName)
	default:
		names := make([]string, len(matches))
		for i, m := range matches {
			names[i] = m.Name
		}
		return nil, fmt.Errorf("multiple deployments owned by gateway %q; cannot disambiguate: %v", gatewayName, names)
	}
}

// deploymentRolloutComplete reports whether d's rollout is complete.
// Mirrors the invariants used by kubectl rollout status deployment:
// observedGeneration caught up, all replicas updated, all replicas available,
// and at least one replica exists.
func deploymentRolloutComplete(d *appsv1.Deployment) bool {
	if d == nil {
		return false
	}
	s := d.Status
	return s.ObservedGeneration >= d.Generation &&
		s.UpdatedReplicas == s.Replicas &&
		s.AvailableReplicas == s.Replicas &&
		s.Replicas > 0
}
