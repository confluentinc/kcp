package gateway

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/yaml"
)

// PodSyncVerdict is the pod check's answer to whether the gateway pods serve
// the config the CR declares.
type PodSyncVerdict string

const (
	PodsInSync     PodSyncVerdict = "in sync"
	PodsOutOfSync  PodSyncVerdict = "out of sync"
	PodsCannotTell PodSyncVerdict = "cannot tell"
)

// PodSync is the pod check's result. Detail is the one observation that
// decided the verdict; Evidence is every fact observed, one per line, in
// display order.
type PodSync struct {
	Verdict  PodSyncVerdict
	Detail   string
	Evidence []string
}

// CheckPodSync reads whether the gateway pods serve the config in crYAML, the
// live Gateway CR. It only reads: the Deployment, the pods and each Ready
// pod's GET /config. The verdict is data; the error is reserved for a CR that
// will not parse, a failed Kubernetes read, or context cancellation.
func (s *K8sService) CheckPodSync(ctx context.Context, namespace, gatewayName string, crYAML []byte, port int) (PodSync, error) {
	cr, err := parseGatewayCR(crYAML)
	if err != nil {
		return PodSync{}, err
	}

	config, err := RESTConfig(s.kubeConfigPath)
	if err != nil {
		return PodSync{}, fmt.Errorf("failed to build config: %w", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return PodSync{}, fmt.Errorf("failed to create clientset: %w", err)
	}

	pods := clientset.CoreV1().Pods(namespace)
	probe := func(ctx context.Context, endpoint GatewayPodEndpoint) (ProbeResult, error) {
		probeCtx, cancel := context.WithTimeout(ctx, configProbeRequestTimeout)
		defer cancel()
		result, err := probeGatewayConfig(probeCtx, pods, endpoint.Name, port)
		return podSyncProbeResult(ctx, endpoint.Name, result, err)
	}

	return checkPodSync(ctx, clientset, probe, namespace, gatewayName, cr)
}

// podSyncProbeResult keeps a per-pod probe problem a verdict rather than an
// error. probeGatewayConfig returns an error whenever its own context is done,
// so a pod that accepts the proxied request but never answers surfaces as the
// per-request deadline; with the check's own context still live, that pod is
// unreachable. Only the parent context's end abandons the whole check.
func podSyncProbeResult(parent context.Context, podName string, result ProbeResult, err error) (ProbeResult, error) {
	if err == nil {
		return result, nil
	}
	if parentErr := parent.Err(); parentErr != nil {
		return ProbeResult{}, parentErr
	}
	return ProbeResult{Addr: podName, Outcome: ProbeUnreachable, Err: err}, nil
}

// parseGatewayCR decodes a Gateway CR through JSON, so integer fields read as
// int64 as they do off the API.
func parseGatewayCR(crYAML []byte) (*unstructured.Unstructured, error) {
	crJSON, err := yaml.YAMLToJSON(crYAML)
	if err != nil {
		return nil, fmt.Errorf("failed to parse the gateway CR: %w", err)
	}
	cr := &unstructured.Unstructured{}
	if err := cr.UnmarshalJSON(crJSON); err != nil {
		return nil, fmt.Errorf("failed to parse the gateway CR: %w", err)
	}
	return cr, nil
}

// checkPodSync is the inner check used by CheckPodSync, split out so unit tests
// can inject a fake clientset and a stub prober. It applies the design's pod
// checks in order, and the first failing rule decides:
//
//  1. the operator has caught up with the CR, in every mode;
//  2. with hot reload off, the Deployment has rolled out and no pod is
//     terminating;
//  3. with hot reload on, every Ready pod reports the CR's configId.
func checkPodSync(ctx context.Context, clientset kubernetes.Interface, probe podProber, namespace, gatewayName string, cr *unstructured.Unstructured) (PodSync, error) {
	if err := ctx.Err(); err != nil {
		return PodSync{}, err
	}

	generation := cr.GetGeneration()
	observed, haveObserved, err := unstructured.NestedInt64(cr.Object, "status", "observedGeneration")
	if err != nil {
		return PodSync{}, fmt.Errorf("failed to read gateway status.observedGeneration: %w", err)
	}
	crConfigID, _, err := unstructured.NestedString(cr.Object, "spec", "configId")
	if err != nil {
		return PodSync{}, fmt.Errorf("failed to read gateway spec.configId: %w", err)
	}
	hotReload := hotReloadEnabledInCR(cr)

	observedText := "none"
	if haveObserved {
		observedText = strconv.FormatInt(observed, 10)
	}
	r := &podSyncReport{
		inspect: fmt.Sprintf("inspect: kubectl -n %s get gateway %s -o yaml; kubectl -n %s get pods -l app=%s",
			namespace, gatewayName, namespace, gatewayName),
	}
	r.observe("CR %s/%s: metadata.generation %d, status.observedGeneration %s, spec.configId %s, hot reload %s",
		namespace, gatewayName, generation, observedText, orNone(crConfigID), onOff(hotReload))

	// Step 1: the operator has caught up. It comes first because step 2 or 3
	// is chosen from spec.hotReload, which is not in force until it has.
	if !haveObserved {
		return r.verdict(PodsOutOfSync, "the operator has not reported status.observedGeneration for this CR"), nil
	}
	if observed < generation {
		return r.verdict(PodsOutOfSync, fmt.Sprintf("the operator has not caught up with the CR: status.observedGeneration is %d, metadata.generation is %d",
			observed, generation)), nil
	}
	rej, undecided, err := currentFatalCondition(cr, gatewayName)
	if err != nil {
		return PodSync{}, err
	}
	if rej != nil {
		detail := fmt.Sprintf("the operator rejected the CR: %s (reason %s, condition %s)", rej.Message, rej.Reason, rej.ConditionType)
		if undecided {
			detail += "; kcp could not tell whether this condition is left over from an earlier spec"
		}
		return r.verdict(PodsOutOfSync, detail), nil
	}

	if !hotReload {
		return checkRolledOut(ctx, clientset, namespace, gatewayName, r)
	}
	return checkPodsReportConfigID(ctx, clientset, probe, namespace, gatewayName, crConfigID, r)
}

// checkRolledOut is step 2, hot reload off: CFK bumps observedGeneration as
// soon as it has written the Deployment, so the pods are in sync only once
// the rollout is complete and no replaced pod is still terminating.
func checkRolledOut(ctx context.Context, clientset kubernetes.Interface, namespace, gatewayName string, r *podSyncReport) (PodSync, error) {
	dep, err := resolveGatewayDeployment(ctx, clientset, namespace, gatewayName)
	if err != nil {
		return PodSync{}, err
	}
	pods, err := listGatewayPods(ctx, clientset, namespace, gatewayName)
	if err != nil {
		return PodSync{}, err
	}

	s := dep.Status
	r.observe("Deployment %s: %d replicas, %d updated, %d available", dep.Name, s.Replicas, s.UpdatedReplicas, s.AvailableReplicas)
	var terminatingPods []string
	for _, pod := range pods {
		if pod.DeletionTimestamp != nil {
			terminatingPods = append(terminatingPods, pod.Name)
			r.observe("pod %s: terminating", pod.Name)
		}
	}

	if !deploymentRolloutComplete(dep) {
		// The one case where kcp can tell "in progress" from "stuck": a
		// stuck rollout flips Progressing to False once its deadline passes. A
		// terminating pod does not override that: the hint would contradict
		// the Deployment.
		if deploymentProgressing(dep) {
			r.observe("The gateway Deployment reports a rollout in progress (%d of %d pods updated). If it completes, re-run.",
				s.UpdatedReplicas, s.Replicas)
		}
		if s.ObservedGeneration < dep.Generation {
			return r.verdict(PodsOutOfSync, fmt.Sprintf("the gateway Deployment %q has not finished rolling out: its controller has observed generation %d of %d",
				dep.Name, s.ObservedGeneration, dep.Generation)), nil
		}
		return r.verdict(PodsOutOfSync, fmt.Sprintf("the gateway Deployment %q has not finished rolling out: %d of %d pods updated, %d available",
			dep.Name, s.UpdatedReplicas, s.Replicas, s.AvailableReplicas)), nil
	}
	if len(terminatingPods) > 0 {
		// The Deployment reports complete while a replaced pod still runs the
		// previous config, for up to its grace period, which bounds the hint.
		r.observe("A replaced gateway pod is still terminating (%d pod(s)). If it completes, re-run.",
			len(terminatingPods))
		return r.verdict(PodsOutOfSync, fmt.Sprintf("gateway pod %s is still terminating and may still serve the previous config",
			terminatingPods[0])), nil
	}
	return r.verdict(PodsInSync, "the gateway Deployment has finished rolling out and no gateway pod is terminating"), nil
}

// checkPodsReportConfigID is step 3, hot reload on: every Ready pod's GET
// /config is compared with the CR's spec.configId. Terminating pods that are
// still Ready are probed, because they still serve traffic; not-Ready pods
// are not, because they have nothing meaningful to report.
func checkPodsReportConfigID(ctx context.Context, clientset kubernetes.Interface, probe podProber, namespace, gatewayName, crConfigID string, r *podSyncReport) (PodSync, error) {
	pods, err := listGatewayPods(ctx, clientset, namespace, gatewayName)
	if err != nil {
		return PodSync{}, err
	}

	type probed struct {
		pod    string
		result ProbeResult
	}
	var results []probed
	for _, pod := range pods {
		if !isPodReady(&pod) {
			r.observe("pod %s: not Ready, not checked", pod.Name)
			continue
		}
		result, err := probe(ctx, GatewayPodEndpoint{Name: pod.Name, Ready: true})
		if err != nil {
			return PodSync{}, fmt.Errorf("failed to probe the gateway config endpoint: %w", err)
		}
		results = append(results, probed{pod: pod.Name, result: result})
		r.observe("pod %s: %s", pod.Name, probeEvidence(result))
	}

	if len(results) == 0 {
		return r.verdict(PodsOutOfSync, "no gateway pod is Ready, so none can be checked"), nil
	}

	for _, p := range results {
		switch p.result.Outcome {
		case ProbeUnreachable:
			return r.verdict(PodsOutOfSync, fmt.Sprintf("gateway pod %s did not answer GET /config: %v", p.pod, p.result.Err)), nil
		case ProbeUnexpected:
			return r.verdict(PodsOutOfSync, fmt.Sprintf("gateway pod %s gave an unreadable GET /config answer: %v", p.pod, p.result.Err)), nil
		}
	}

	values := map[string]bool{}
	anyAbsent, anyUnset, allAbsent := false, false, true
	for _, p := range results {
		if p.result.Outcome != ProbeEndpointAbsent {
			allAbsent = false
		}
		switch p.result.Outcome {
		case ProbeApplied:
			values[p.result.ConfigID] = true
		case ProbeEndpointAbsent:
			anyAbsent = true
		case ProbeNeverSet:
			anyUnset = true
		}
	}

	if len(values) == 0 {
		if allAbsent {
			return r.verdict(PodsCannotTell, "the gateway pods do not serve GET /config, so kcp cannot read which config they loaded"), nil
		}
		return r.verdict(PodsCannotTell, "no gateway pod reports a configId yet, so there is nothing to compare with the CR"), nil
	}
	if anyAbsent || anyUnset || len(values) > 1 {
		reports := make([]string, len(results))
		for i, p := range results {
			reports[i] = p.pod + " " + partialReport(p.result)
		}
		return r.verdict(PodsOutOfSync, fmt.Sprintf("the gateway pods report different configIds: %s", strings.Join(reports, ", "))), nil
	}

	podsConfigID := results[0].result.ConfigID
	if crConfigID == "" {
		return r.verdict(PodsCannotTell, "the CR has no spec.configId, so there is nothing to compare the pods with"), nil
	}
	if podsConfigID != crConfigID {
		return r.verdict(PodsOutOfSync, fmt.Sprintf("every gateway pod reports configId %q, but the CR's spec.configId is %q", podsConfigID, crConfigID)), nil
	}
	return r.verdict(PodsInSync, fmt.Sprintf("every Ready gateway pod reports the CR's configId %q", crConfigID)), nil
}

// podSyncReport accumulates the evidence lines; the inspect line always closes
// them.
type podSyncReport struct {
	evidence []string
	inspect  string
}

func (r *podSyncReport) observe(format string, args ...any) {
	r.evidence = append(r.evidence, fmt.Sprintf(format, args...))
}

func (r *podSyncReport) verdict(v PodSyncVerdict, detail string) PodSync {
	return PodSync{Verdict: v, Detail: detail, Evidence: append(r.evidence, r.inspect)}
}

// listGatewayPods lists the gateway's pods by CFK's app=<gateway-cr-name>
// label, sorted by name so the evidence reads the same on every run.
func listGatewayPods(ctx context.Context, clientset kubernetes.Interface, namespace, gatewayName string) ([]corev1.Pod, error) {
	list, err := clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("app=%s", gatewayName),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list gateway pods: %w", err)
	}
	pods := list.Items
	sort.Slice(pods, func(i, j int) bool { return pods[i].Name < pods[j].Name })
	return pods, nil
}

// deploymentProgressing reports whether d's Progressing condition is True.
func deploymentProgressing(d *appsv1.Deployment) bool {
	for _, c := range d.Status.Conditions {
		if c.Type == appsv1.DeploymentProgressing {
			return c.Status == corev1.ConditionTrue
		}
	}
	return false
}

// probeEvidence renders one pod's probe outcome for its evidence line.
func probeEvidence(result ProbeResult) string {
	switch result.Outcome {
	case ProbeApplied:
		return "configId " + result.ConfigID
	case ProbeNeverSet:
		return "configId never set"
	case ProbeEndpointAbsent:
		return "GET /config not served"
	case ProbeUnreachable:
		return "unreachable"
	default:
		return "unreadable answer"
	}
}

// partialReport renders one pod's answer within the partial-reload Detail.
func partialReport(result ProbeResult) string {
	switch result.Outcome {
	case ProbeApplied:
		return result.ConfigID
	case ProbeNeverSet:
		return "never set"
	default:
		return "GET /config not served"
	}
}

func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}
