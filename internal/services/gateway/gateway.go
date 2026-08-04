package gateway

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/goccy/go-yaml"
	appsv1 "k8s.io/api/apps/v1"
	authorizationv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

// Kubernetes resource constants
const (
	// ConfluentNamespace     = "kcp"
	GatewayGroup          = "platform.confluent.io"
	GatewayVersion        = "v1beta1"
	GatewayResourcePlural = "gateways"
	GatewayKind           = "Gateway"
)

// Service defines gateway operations
type Service interface {
	GetGatewayYAML(ctx context.Context, namespace, gatewayName string) ([]byte, error)
	ValidateGatewayCRs(ctx context.Context, namespace, gatewayName string, initialYAML, fencedYAML, switchoverYAML []byte) (CRValidationResult, error)
	CheckPermissions(ctx context.Context, verb, resource, group, namespace string) (bool, error)
	ApplyGatewayYAML(ctx context.Context, namespace, gatewayName string, yamlData []byte) error
	WaitForGatewayAccepted(ctx context.Context, namespace, gatewayName string, conditionsBefore ConditionSnapshot, pollInterval, timeout time.Duration) error
	GetGatewayPodUIDs(ctx context.Context, namespace, gatewayName string) (map[types.UID]struct{}, error)
	WaitForGatewayPods(ctx context.Context, namespace, gatewayName string, initialPodUIDs map[types.UID]struct{}, pollInterval, timeout time.Duration, onProgress func(PodRolloutProgress)) error
	WaitForGatewayReady(ctx context.Context, namespace, gatewayName string, pollInterval, timeout time.Duration, onProgress func(GatewayReadinessProgress)) error

	// Hot-reload verification. Under spec.hotReload.enabled a config change
	// applies in place, so every Kubernetes-side signal above — rollout, pod
	// replacement, observedGeneration — reports success without proving the
	// pods serve the new config. These three select and drive the verification
	// that does prove it.
	DetectGatewayVerificationTier(ctx context.Context, namespace, gatewayName string, initialYAML, candidateYAML []byte) (VerificationTier, error)
	SnapshotGatewayConditions(ctx context.Context, namespace, gatewayName string) (ConditionSnapshot, error)
	WaitForGatewayConfigApplied(ctx context.Context, namespace, gatewayName, targetConfigID string, conditionsBefore ConditionSnapshot, port string, pollInterval, timeout time.Duration, onProgress func(ConfigApplyProgress)) error
}

// PodRolloutProgress reports the current state of a pod rollout
type PodRolloutProgress struct {
	InitialPodCount  int
	NewPodsReady     int
	OldPodsRemaining int
	RolloutDetected  bool
}

// GatewayReadinessProgress reports the current state of a gateway readiness wait.
// The gate (whether the wait returns) comes from the underlying apps/v1
// Deployment's rollout status; pod counts come from Deployment.status.readyReplicas.
type GatewayReadinessProgress struct {
	InitialPodCount int
	PodsReady       int
	Elapsed         time.Duration
	RolloutDetected bool
	Ready           bool
}

// gatewayReadinessDetectionWindow is the time we wait at the start of a
// readiness wait to distinguish a no-op patch from a rollout that has not
// yet begun. var so tests can shorten it.
var gatewayReadinessDetectionWindow = 10 * time.Second

// K8sService implements gateway operations using Kubernetes clients
type K8sService struct {
	kubeConfigPath string
}

// NewK8sService creates a new gateway service
func NewK8sService(kubeConfigPath string) *K8sService {
	return &K8sService{
		kubeConfigPath: kubeConfigPath,
	}
}

// GetGatewayYAML retrieves the gateway resource as YAML
func (s *K8sService) GetGatewayYAML(ctx context.Context, namespace, gatewayName string) ([]byte, error) {
	config, err := clientcmd.BuildConfigFromFlags("", s.kubeConfigPath)
	if err != nil {
		return nil, fmt.Errorf("failed to build config: %w", err)
	}

	dynamicClient, err := dynamic.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create dynamic client: %w", err)
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

// CheckPermissions checks if the user has the required Kubernetes permissions
func (s *K8sService) CheckPermissions(ctx context.Context, verb, resource, group, namespace string) (bool, error) {
	config, err := clientcmd.BuildConfigFromFlags("", s.kubeConfigPath)
	if err != nil {
		return false, fmt.Errorf("failed to build config: %w", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return false, fmt.Errorf("failed to create clientset: %w", err)
	}

	sar := &authorizationv1.SelfSubjectAccessReview{
		Spec: authorizationv1.SelfSubjectAccessReviewSpec{
			ResourceAttributes: &authorizationv1.ResourceAttributes{
				Namespace: namespace,
				Verb:      verb,
				Group:     group,
				Resource:  resource,
			},
		},
	}

	response, err := clientset.AuthorizationV1().SelfSubjectAccessReviews().Create(
		ctx,
		sar,
		metav1.CreateOptions{},
	)

	if err != nil {
		return false, fmt.Errorf("failed to check permissions: %w", err)
	}

	return response.Status.Allowed, nil
}

// ApplyGatewayYAML applies a complete gateway CR YAML to the cluster using server-side apply
func (s *K8sService) ApplyGatewayYAML(ctx context.Context, namespace, gatewayName string, yamlData []byte) error {
	config, err := clientcmd.BuildConfigFromFlags("", s.kubeConfigPath)
	if err != nil {
		return fmt.Errorf("failed to build config: %w", err)
	}

	dynamicClient, err := dynamic.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("failed to create dynamic client: %w", err)
	}

	gatewayGVR := schema.GroupVersionResource{
		Group:    GatewayGroup,
		Version:  GatewayVersion,
		Resource: GatewayResourcePlural,
	}

	// Parse YAML into unstructured object
	var obj unstructured.Unstructured
	if err := yaml.Unmarshal(yamlData, &obj.Object); err != nil {
		return fmt.Errorf("failed to parse gateway YAML: %w", err)
	}

	// Ensure metadata matches the expected resource
	obj.SetName(gatewayName)
	obj.SetNamespace(namespace)

	slog.Debug("🔍 applying gateway CR (server-side apply)", "namespace", namespace, "gateway", gatewayName, "bytes", len(yamlData))
	start := time.Now()
	_, err = dynamicClient.Resource(gatewayGVR).Namespace(namespace).
		Apply(ctx, gatewayName, &obj, metav1.ApplyOptions{
			FieldManager: gatewayFieldManager,
			Force:        true,
		})
	if err != nil {
		return fmt.Errorf("failed to apply gateway YAML: %w", err)
	}

	slog.Debug("applied gateway CR", "namespace", namespace, "gateway", gatewayName, "ms", time.Since(start).Milliseconds())
	return nil
}

// gatewayRejectionSettleWindow is how long a *newly transitioned* failure
// condition must persist before WaitForGatewayAccepted aborts the migration on
// it. The operator can briefly publish a failure while it retries, and on this
// path the rejection is the gate — nothing stronger runs behind it to catch a
// false one — so the failure has to hold before we act.
//
// This deliberately differs from the per-pod /config wait, which acts on a
// rejection immediately (see gatewayRejectionSince): there /config remains the
// real gate, so a premature fast-fail costs only latency, not correctness.
// var so tests can shorten it.
var gatewayRejectionSettleWindow = 30 * time.Second

// blindAcceptanceTimeout bounds the acceptance wait when no condition baseline
// was captured, since without one a rejection cannot be recognised at all and
// the no-deadline default would poll forever. Generous relative to the ~1.2s
// reconcile measured on a healthy gateway: this is a backstop against hanging,
// not a latency budget, and it must not fire on a merely slow operator.
// var so tests can shorten it.
var blindAcceptanceTimeout = 5 * time.Minute

// WaitForGatewayAccepted blocks until the CFK operator has accepted the current
// Gateway CR spec — status.observedGeneration >= metadata.generation — or
// returns a *GatewayRejection if the operator refuses it. timeout == 0
// means no deadline (consistent with the other gateway waits).
//
// This is the guard that makes a downstream "no rollout detected" trustworthy.
// Applying a CR bumps the Gateway's metadata.generation; until the operator
// reconciles it (and, for a real spec change, begins the pod rollout)
// observedGeneration lags. Without this wait, the Deployment-based waits
// (WaitForGatewayReady, WaitForGatewayPods) see a perfectly healthy Deployment
// still running the *previous* generation's pods, and their detection windows
// expire concluding "no restart required" — reporting success for a switchover
// or fence that never happened. A no-op apply (e.g. a resume re-applying an
// already-fenced CR) does not bump generation, so observedGeneration already
// satisfies the check and this returns immediately.
//
// Watching observedGeneration alone is not enough: when the operator *rejects*
// the spec, observedGeneration never advances, so with the default timeout of 0
// the wait would block indefinitely. Polling status.conditions alongside it
// turns that hang into a fast, actionable error carrying the operator's own
// message.
//
// conditionsBefore is the condition baseline captured immediately before the
// apply, and is what makes acting on a condition safe at all: Gateway conditions
// carry no observedGeneration and go stale for days, so only one that has
// transitioned since the baseline says anything about this apply — see
// conditions.go. An empty baseline therefore disables the rejection check rather
// than guessing, degrading to waiting out the timeout.
func (s *K8sService) WaitForGatewayAccepted(ctx context.Context, namespace, gatewayName string, conditionsBefore ConditionSnapshot, pollInterval, timeout time.Duration) error {
	config, err := clientcmd.BuildConfigFromFlags("", s.kubeConfigPath)
	if err != nil {
		return fmt.Errorf("failed to build config: %w", err)
	}

	dynamicClient, err := dynamic.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("failed to create dynamic client: %w", err)
	}

	return waitForGatewayAccepted(ctx, dynamicClient, namespace, gatewayName, conditionsBefore, pollInterval, timeout)
}

// waitForGatewayAccepted is the inner orchestration used by
// WaitForGatewayAccepted. Split from the method so unit tests can inject a
// fake dynamic client.
func waitForGatewayAccepted(ctx context.Context, dynamicClient dynamic.Interface, namespace, gatewayName string, conditionsBefore ConditionSnapshot, pollInterval, timeout time.Duration) error {
	gatewayGVR := schema.GroupVersionResource{
		Group:    GatewayGroup,
		Version:  GatewayVersion,
		Resource: GatewayResourcePlural,
	}

	// Running unbounded is only safe while we can still recognise a rejection.
	// Without a baseline the condition check is off (see findNewRejection), so a
	// refused spec never advances observedGeneration and never produces a
	// verdict — the wait would poll forever on the one outcome it exists to
	// catch. Falling back to a bound turns that into a timeout the caller can
	// report. A caller-supplied timeout always wins; this only fills in for the
	// no-deadline default.
	if timeout <= 0 && len(conditionsBefore) == 0 {
		timeout = blindAcceptanceTimeout
		slog.Warn("⚠️ no gateway condition baseline; cannot detect a rejected spec, so bounding the reconcile wait",
			"gateway", gatewayName, "timeout", timeout)
	}

	noDeadline := timeout <= 0
	deadline := time.Now().Add(timeout)

	// A newly transitioned failure condition only aborts once it has held for the
	// settle window. firstFailureAt marks the start of the current unbroken run of
	// failure observations; it resets the moment the operator stops reporting one.
	var (
		firstFailureAt time.Time
		pending        error
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
		if found && observed >= generation {
			slog.Debug("gateway accepted by operator", "gateway", gatewayName, "generation", generation, "observedGeneration", observed)
			return nil
		}

		// Not accepted yet — is the operator telling us why it never will be?
		// Only a condition that transitioned after conditionsBefore was captured
		// counts; an ApplyFailed left over from a previous generation is history,
		// not a verdict on this apply.
		conds, err := gatewayConditions(gw.Object)
		if err != nil {
			return fmt.Errorf("failed to read gateway status.conditions: %w", err)
		}
		rejection := findNewRejection(conds, conditionsBefore)
		switch {
		case rejection != nil && firstFailureAt.IsZero():
			firstFailureAt = time.Now()
			pending = rejectedGatewayError(gatewayName, generation, observed, rejection)
			slog.Warn("⚠️ gateway condition reports a failure; waiting to see whether the operator recovers",
				"gateway", gatewayName, "reason", rejection.Reason, "message", rejection.Message,
				"settleWindow", gatewayRejectionSettleWindow)
		case rejection != nil:
			pending = rejectedGatewayError(gatewayName, generation, observed, rejection)
			if time.Since(firstFailureAt) >= gatewayRejectionSettleWindow {
				slog.Error("❌ gateway spec rejected by the operator", "gateway", gatewayName,
					"reason", rejection.Reason, "message", rejection.Message,
					"generation", generation, "observedGeneration", observed)
				return pending
			}
		default:
			if !firstFailureAt.IsZero() {
				slog.Debug("gateway failure condition cleared; resuming reconcile wait", "gateway", gatewayName)
			}
			firstFailureAt = time.Time{}
			pending = nil
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

// rejectedGatewayError pairs a condition-level rejection with the reconcile
// state that produced it. The *GatewayRejection stays reachable via errors.As,
// so callers still branch on the type; the wrapper only adds the generation
// pair, which is what tells a reader whether the operator ever got as far as
// looking at this spec.
func rejectedGatewayError(gatewayName string, generation, observed int64, r *GatewayRejection) error {
	return fmt.Errorf("gateway %q was rejected by the Confluent operator (generation: %d, observedGeneration: %d): %w",
		gatewayName, generation, observed, r)
}

// GetGatewayPodUIDs returns a set of UIDs for the current gateway pods.
// This should be called BEFORE patching the gateway to capture the initial pod state.
func (s *K8sService) GetGatewayPodUIDs(ctx context.Context, namespace, gatewayName string) (map[types.UID]struct{}, error) {
	config, err := clientcmd.BuildConfigFromFlags("", s.kubeConfigPath)
	if err != nil {
		return nil, fmt.Errorf("failed to build config: %w", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create clientset: %w", err)
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
//  1. Wait for change detection: Wait up to 10 seconds to detect if rollout starts
//  2. Wait for complete replacement: Ensure all initial pods are replaced and new pods are ready
//
// This prevents returning prematurely when maxSurge creates extra pods during the rollout.
func (s *K8sService) WaitForGatewayPods(ctx context.Context, namespace, gatewayName string, initialPodUIDs map[types.UID]struct{}, pollInterval, timeout time.Duration, onProgress func(PodRolloutProgress)) error {
	config, err := clientcmd.BuildConfigFromFlags("", s.kubeConfigPath)
	if err != nil {
		return fmt.Errorf("failed to build config: %w", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("failed to create clientset: %w", err)
	}

	return waitForGatewayPods(ctx, clientset, namespace, gatewayName, initialPodUIDs, pollInterval, timeout, onProgress)
}

// waitForGatewayPods is the inner orchestration used by WaitForGatewayPods.
// Split from the method so unit tests can inject a fake clientset.
func waitForGatewayPods(ctx context.Context, clientset kubernetes.Interface, namespace, gatewayName string, initialPodUIDs map[types.UID]struct{}, pollInterval, timeout time.Duration, onProgress func(PodRolloutProgress)) error {
	// Confluent CFK labels gateway pods with app=<gateway-crd-name>
	labelSelector := fmt.Sprintf("app=%s", gatewayName)

	// Calculate initial pod count from the passed UIDs
	initialPodCount := len(initialPodUIDs)
	slog.Debug("waiting for gateway pod rollout", "namespace", namespace, "gateway", gatewayName, "initialPodCount", initialPodCount)

	// Phase 1: Wait for rollout to start (detection window)
	changeDetectionDeadline := time.Now().Add(gatewayReadinessDetectionWindow)
	rolloutDetected := false

	for time.Now().Before(changeDetectionDeadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		pods, err := clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
			LabelSelector: labelSelector,
		})
		if err != nil {
			return fmt.Errorf("failed to list gateway pods: %w", err)
		}

		// Check if any pod changed (new UID) or became not ready
		for _, pod := range pods.Items {
			if _, wasInitial := initialPodUIDs[pod.UID]; !wasInitial {
				slog.Debug("new pod detected, rollout started", "pod", pod.Name)
				rolloutDetected = true
				break
			}
			if !isPodReady(&pod) {
				slog.Debug("pod not ready, rollout started", "pod", pod.Name)
				rolloutDetected = true
				break
			}
		}

		if rolloutDetected {
			break
		}

		time.Sleep(pollInterval)
	}

	if !rolloutDetected {
		slog.Debug("no rollout detected within detection window, assuming config change did not require pod restart")
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
	// This matches WaitForGatewayReady, whose caller (FenceGateway) passes the
	// same rolloutTimeout, which defaults to 0.
	slog.Debug("rollout detected, waiting for complete pod replacement")

	noDeadline := timeout <= 0
	deadline := time.Now().Add(timeout)

	for noDeadline || time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		pods, err := clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
			LabelSelector: labelSelector,
		})
		if err != nil {
			return fmt.Errorf("failed to list gateway pods: %w", err)
		}

		newPodsReady := countNewReadyPods(pods.Items, initialPodUIDs)
		oldPodsRemaining := countOldPods(pods.Items, initialPodUIDs)

		// Completion gates on two independent signals:
		//   1. every pod captured before the patch is gone (oldPodsRemaining == 0)
		//      — the guarantee WaitForGatewayReady cannot give, since the old
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
			return err
		}
		rolloutComplete := deploymentRolloutComplete(dep)

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
			})
		}

		if oldPodsRemaining == 0 && rolloutComplete {
			slog.Debug("all old gateway pods gone and deployment rollout complete")
			return nil
		}

		time.Sleep(pollInterval)
	}

	return fmt.Errorf("timed out waiting for gateway pods to be replaced (timeout: %s)", timeout)
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

// countOldPods counts how many initial pods are still present
func countOldPods(currentPods []corev1.Pod, initialUIDs map[types.UID]struct{}) int {
	count := 0
	for _, pod := range currentPods {
		if _, wasInitial := initialUIDs[pod.UID]; wasInitial {
			count++
		}
	}
	return count
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

// WaitForGatewayReady polls the underlying apps/v1 Deployment's rollout status
// until the Deployment reports a complete rollout, or until ctx is cancelled.
// When timeout > 0, a hard deadline is applied via context; a timeout of 0
// means no deadline.
//
// The wait runs in two phases:
//  1. Detection (up to 10s): if the Deployment is already at rollout-complete
//     state for the entire window, no pod restart was required — onProgress is
//     invoked once with RolloutDetected=false and the wait returns nil.
//  2. Convergence: polls until the Deployment reports rollout complete
//     (observedGeneration >= generation, updatedReplicas == replicas,
//     availableReplicas == replicas, replicas > 0), calling onProgress on
//     every poll tick with monotonically increasing Elapsed.
func (s *K8sService) WaitForGatewayReady(ctx context.Context, namespace, gatewayName string, pollInterval, timeout time.Duration, onProgress func(GatewayReadinessProgress)) error {
	config, err := clientcmd.BuildConfigFromFlags("", s.kubeConfigPath)
	if err != nil {
		return fmt.Errorf("failed to build config: %w", err)
	}
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("failed to create clientset: %w", err)
	}
	return waitForGatewayReady(ctx, clientset, namespace, gatewayName, pollInterval, timeout, onProgress)
}

// waitForGatewayReady is the inner orchestration used by WaitForGatewayReady.
// Split from the method so unit tests can inject a fake clientset.
func waitForGatewayReady(ctx context.Context, clientset kubernetes.Interface, namespace, gatewayName string, pollInterval, timeout time.Duration, onProgress func(GatewayReadinessProgress)) error {
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	dep, err := resolveGatewayDeployment(ctx, clientset, namespace, gatewayName)
	if err != nil {
		return err
	}
	slog.Debug("resolved gateway deployment", "namespace", namespace, "gateway", gatewayName, "deployment", dep.Name)

	initialReplicas := dep.Status.Replicas
	start := time.Now()

	// Phase 1: detection window.
	detectionDeadline := time.Now().Add(gatewayReadinessDetectionWindow)
	rolloutDetected := false
	for time.Now().Before(detectionDeadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		dep, err = resolveGatewayDeployment(ctx, clientset, namespace, gatewayName)
		if err != nil {
			return err
		}
		if !deploymentRolloutComplete(dep) {
			slog.Debug("rollout detected during detection window", "gateway", gatewayName)
			rolloutDetected = true
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(pollInterval):
		}
	}

	if !rolloutDetected {
		slog.Debug("no rollout detected within detection window; treating patch as no-op", "gateway", gatewayName)
		if onProgress != nil {
			onProgress(GatewayReadinessProgress{
				InitialPodCount: int(initialReplicas),
				PodsReady:       int(initialReplicas),
				Elapsed:         time.Since(start),
				RolloutDetected: false,
				Ready:           true,
			})
		}
		return nil
	}

	// Phase 2: convergence — poll until Deployment rollout is complete.
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		dep, err = resolveGatewayDeployment(ctx, clientset, namespace, gatewayName)
		if err != nil {
			return err
		}
		ready := deploymentRolloutComplete(dep)
		if onProgress != nil {
			onProgress(GatewayReadinessProgress{
				InitialPodCount: int(initialReplicas),
				PodsReady:       int(dep.Status.ReadyReplicas),
				Elapsed:         time.Since(start),
				RolloutDetected: true,
				Ready:           ready,
			})
		}
		if ready {
			slog.Debug("gateway deployment rollout complete", "gateway", gatewayName, "elapsed", time.Since(start))
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(pollInterval):
		}
	}
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
