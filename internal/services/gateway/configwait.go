package gateway

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

// Per-pod verification of an applied gateway config.
//
// This is the only sound gate for a hot-reloadable change. The Deployment never
// changes, so a rollout wait reports "no rollout detected, treating patch as a
// no-op" and passes; status.observedGeneration goes true ~1.2s after the apply,
// measured 4.6s before CFK published a rejection for a config no pod ever ran.
// Only the gateway's own /config endpoint says what a given pod is serving.
//
// The endpoint is reached through the API-server pod proxy rather than by
// dialling pod IPs. That matters: port 9180 is not a declared containerPort and
// no Service exposes it, and kcp normally runs outside the cluster, where pod
// IPs are unroutable. The proxy needs only the kubeconfig kcp already uses to
// apply the CR — plus RBAC to get pods/proxy in the namespace.

// GatewayConfigAccessDeniedError reports that kcp may not read the gateway's
// config endpoint through the API-server pod proxy.
//
// This is worth its own type because it is the one probe failure that is not
// transient. Every other one — a pod that vanished mid-roll, a connection reset,
// an empty body from a dying pod — resolves by polling again, so the wait keeps
// going. Missing RBAC never resolves, and treating it as transient turns a
// one-line permission fix into a full verification timeout with the gateway
// already fenced.
type GatewayConfigAccessDeniedError struct {
	Namespace string
	Err       error
}

func (e *GatewayConfigAccessDeniedError) Error() string {
	return fmt.Sprintf("not authorized to read the gateway config endpoint: confirming a hot-reloaded config change requires %q access to %q in namespace %q, which this kubeconfig context does not have",
		gatewayProxyVerb, gatewayProxyResource, e.Namespace)
}

func (e *GatewayConfigAccessDeniedError) Unwrap() error { return e.Err }

// The RBAC verb and resource named in the error above. The pod proxy is a
// subresource, so granting "get pods" is not enough — this is the detail that
// makes the message actionable rather than merely accurate.
const (
	gatewayProxyVerb     = "get"
	gatewayProxyResource = "pods/proxy"
)

// ConfigApplyProgress reports the state of a /config convergence wait.
type ConfigApplyProgress struct {
	TargetConfigID string
	PodsApplied    int
	PodsTotal      int
	Elapsed        time.Duration
	Converged      bool
	Reason         string
}

// SnapshotGatewayConditions captures the Gateway's condition transition times.
//
// Call this immediately BEFORE applying a CR, and pass the result to
// WaitForGatewayConfigApplied. It is what lets the wait distinguish a rejection
// of this apply from a condition that has been sitting failed for days.
func (s *K8sService) SnapshotGatewayConditions(ctx context.Context, namespace, gatewayName string) (ConditionSnapshot, error) {
	config, err := clientcmd.BuildConfigFromFlags("", s.kubeConfigPath)
	if err != nil {
		return nil, fmt.Errorf("failed to build config: %w", err)
	}

	dynamicClient, err := dynamic.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create dynamic client: %w", err)
	}

	return snapshotGatewayConditions(ctx, dynamicClient, namespace, gatewayName)
}

// snapshotGatewayConditions is the inner orchestration used by
// SnapshotGatewayConditions. Split from the method so unit tests can inject a
// fake dynamic client.
func snapshotGatewayConditions(ctx context.Context, dynamicClient dynamic.Interface, namespace, gatewayName string) (ConditionSnapshot, error) {
	gw, err := dynamicClient.Resource(gatewayGVR()).Namespace(namespace).
		Get(ctx, gatewayName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get gateway for condition snapshot: %w", err)
	}

	conds, err := gatewayConditions(gw.Object)
	if err != nil {
		return nil, fmt.Errorf("failed to read gateway conditions: %w", err)
	}
	return snapshotConditions(conds), nil
}

// WaitForGatewayConfigApplied blocks until every gateway pod reports
// targetConfigID from GET /config, or until the apply is rejected, ctx is
// cancelled, or the timeout expires. A timeout of 0 means no deadline,
// consistent with the other gateway waits.
//
// conditionsBefore must come from SnapshotGatewayConditions called before the
// apply; port is the gateway's HTTP endpoint port (DefaultGatewayConfigPort).
func (s *K8sService) WaitForGatewayConfigApplied(ctx context.Context, namespace, gatewayName, targetConfigID string, conditionsBefore ConditionSnapshot, port string, pollInterval, timeout time.Duration, onProgress func(ConfigApplyProgress)) error {
	config, err := clientcmd.BuildConfigFromFlags("", s.kubeConfigPath)
	if err != nil {
		return fmt.Errorf("failed to build config: %w", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	dynamicClient, err := dynamic.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("failed to create dynamic client: %w", err)
	}

	return waitForGatewayConfigApplied(ctx, clientset, dynamicClient, namespace, gatewayName,
		targetConfigID, conditionsBefore, port, pollInterval, timeout, onProgress)
}

// waitForGatewayConfigApplied is the inner orchestration used by
// WaitForGatewayConfigApplied. Split from the method so unit tests can inject
// fake clients.
func waitForGatewayConfigApplied(ctx context.Context, clientset kubernetes.Interface, dynamicClient dynamic.Interface, namespace, gatewayName, targetConfigID string, conditionsBefore ConditionSnapshot, port string, pollInterval, timeout time.Duration, onProgress func(ConfigApplyProgress)) error {
	labelSelector := fmt.Sprintf("app=%s", gatewayName)

	noDeadline := timeout <= 0
	deadline := time.Now().Add(timeout)
	start := time.Now()

	slog.Debug("🔍 verifying gateway pods applied the new config",
		"namespace", namespace, "gateway", gatewayName, "configId", targetConfigID, "port", port)

	lastReason := "no polls completed"

	for noDeadline || time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		podList, err := clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{LabelSelector: labelSelector})
		if err != nil {
			return fmt.Errorf("failed to list gateway pods for config verification: %w", err)
		}
		eligible := eligiblePods(podList.Items)

		wantReplicas := expectedGatewayReplicas(ctx, clientset, namespace, gatewayName)
		results := probeGatewayPods(ctx, clientset, namespace, port, eligible)

		// Checked before anything else reports progress: a denial cannot resolve
		// by polling, and a "0 of 3 pods have applied the new config" line ahead
		// of it would suggest the config is the problem rather than the RBAC.
		if denied := configAccessDenied(results); denied != nil {
			return denied
		}

		done, reason := converged(results, targetConfigID, wantReplicas)
		lastReason = reason
		applied := appliedCount(results, targetConfigID)

		total := wantReplicas
		if total == 0 {
			total = len(results)
		}

		if onProgress != nil {
			onProgress(ConfigApplyProgress{
				TargetConfigID: targetConfigID,
				PodsApplied:    applied,
				PodsTotal:      total,
				Elapsed:        time.Since(start),
				Converged:      done,
				Reason:         reason,
			})
		}

		// Convergence is checked before rejection deliberately. If every pod is
		// already serving the target config then the change landed, whatever the
		// operator's conditions happen to say.
		if done {
			slog.Debug("gateway pods applied the new config",
				"gateway", gatewayName, "configId", targetConfigID,
				"pods", len(results), "ms", time.Since(start).Milliseconds())
			return nil
		}

		if rejection := gatewayRejectionSince(ctx, dynamicClient, namespace, gatewayName, conditionsBefore); rejection != nil {
			slog.Debug("gateway apply rejected by operator",
				"gateway", gatewayName, "condition", rejection.ConditionType, "reason", rejection.Reason)
			return rejection
		}

		slog.Debug("waiting for gateway pods to apply config",
			"gateway", gatewayName, "configId", targetConfigID, "reason", reason)

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(pollInterval):
		}
	}

	return fmt.Errorf("timed out waiting for gateway %q pods to apply configId %q: %s (timeout: %s)",
		gatewayName, targetConfigID, lastReason, timeout)
}

// expectedGatewayReplicas reports the Deployment's ready-replica count, or 0
// when it cannot be determined.
//
// Zero is a deliberate "unknown" rather than a failure: the count exists to
// stop the wait converging on a subset of pods mid-rollout, and if the
// Deployment is unreadable it is better to fall back to "all eligible pods
// agree" than to block until the timeout.
func expectedGatewayReplicas(ctx context.Context, clientset kubernetes.Interface, namespace, gatewayName string) int {
	dep, err := resolveGatewayDeployment(ctx, clientset, namespace, gatewayName)
	if err != nil {
		slog.Debug("could not resolve gateway deployment for replica cross-check; falling back to eligible-pod agreement",
			"namespace", namespace, "gateway", gatewayName, "error", err)
		return 0
	}
	return int(dep.Status.ReadyReplicas)
}

// probeGatewayPods asks each pod what config it is serving.
//
// Sequential by design: the measured spread between pods applying a hot reload
// was ~173ms across 3 pods, so a handful of round trips per tick costs nothing
// and keeps failures attributable to a single pod.
func probeGatewayPods(ctx context.Context, clientset kubernetes.Interface, namespace, port string, pods []string) []podConfig {
	results := make([]podConfig, 0, len(pods))
	for _, name := range pods {
		results = append(results, probeGatewayPod(ctx, clientset, namespace, port, name))
	}
	return results
}

// probeGatewayPod performs one GET /config through the API-server pod proxy.
func probeGatewayPod(ctx context.Context, clientset kubernetes.Interface, namespace, port, podName string) podConfig {
	result := podConfig{Pod: podName}

	response := clientset.CoreV1().Pods(namespace).
		ProxyGet("http", podName, port, GatewayConfigEndpointPath, nil)
	if response == nil {
		result.Err = fmt.Errorf("pod proxy returned no response for %s", GatewayConfigEndpointPath)
		return result
	}

	raw, err := response.DoRaw(ctx)
	if err != nil {
		if apierrors.IsForbidden(err) {
			result.Err = &GatewayConfigAccessDeniedError{Namespace: namespace, Err: err}
			return result
		}
		result.Err = fmt.Errorf("failed to reach %s on gateway pod: %w", GatewayConfigEndpointPath, err)
		return result
	}

	configID, appliedAt, err := parseConfigResponse(raw)
	if err != nil {
		result.Err = err
		return result
	}
	result.ConfigID = configID
	result.AppliedAt = appliedAt
	return result
}

// configAccessDenied returns the first access denial among the probe results,
// or nil.
//
// Any single denial is treated as fatal for all pods: pods/proxy is granted per
// namespace, so one pod refusing means the permission is missing for every pod,
// and there is nothing to gain by asking the rest.
func configAccessDenied(results []podConfig) *GatewayConfigAccessDeniedError {
	for _, r := range results {
		var denied *GatewayConfigAccessDeniedError
		if errors.As(r.Err, &denied) {
			return denied
		}
	}
	return nil
}

// appliedCount counts pods confirmed to be serving target.
func appliedCount(results []podConfig, target string) int {
	count := 0
	for _, r := range results {
		if r.Err == nil && r.ConfigID != nil && *r.ConfigID == target {
			count++
		}
	}
	return count
}

// gatewayRejectionSince reports a rejection only if a failure condition differs
// from what conditionsBefore recorded.
//
// A CR that cannot be read or parsed yields no rejection rather than an error:
// /config is the gate, and this check exists only to fail fast on a rejection
// the operator has already published. Losing it costs the wait its timeout;
// acting on a misread would abort a healthy migration.
//
// An empty baseline disables the check entirely — findNewRejection enforces
// that; returning early here just avoids a pointless GET. Without a pre-apply
// snapshot there is no way to tell a fresh rejection from a condition that has
// been failing for days, and gateways really do carry those: one ApplyFailed was
// observed still present 6 days and 13 generations after the fact. So the wait
// falls back to /config alone and lets the timeout do the work.
func gatewayRejectionSince(ctx context.Context, dynamicClient dynamic.Interface, namespace, gatewayName string, before ConditionSnapshot) *GatewayRejection {
	if len(before) == 0 {
		return nil
	}

	gw, err := dynamicClient.Resource(gatewayGVR()).Namespace(namespace).
		Get(ctx, gatewayName, metav1.GetOptions{})
	if err != nil {
		slog.Debug("could not read gateway CR for rejection check",
			"namespace", namespace, "gateway", gatewayName, "error", err)
		return nil
	}

	conds, err := gatewayConditions(gw.Object)
	if err != nil {
		slog.Debug("could not parse gateway conditions for rejection check",
			"namespace", namespace, "gateway", gatewayName, "error", err)
		return nil
	}

	return findNewRejection(conds, before)
}
