package gateway

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

// RolloutMechanism names how a gateway config change reached the pods, as
// observed after the operator acted on it. It is never predicted from the spec:
// kcp does not inspect the user's Gateway CRs to decide what a transition will
// do, it watches what the cluster did.
//
// The two values are deliberately asymmetric in strength, and are named for the
// evidence behind them rather than for the mechanism:
//
//   - MechanismPodRoll is positive evidence. The backing Deployment's
//     metadata.generation advanced past the pre-apply baseline, which only
//     happens when the operator rewrote the pod template. Pods are being
//     replaced, and the Deployment's own rollout status tells us when that is
//     done.
//   - MechanismNoRollObserved is the absence of evidence. No generation bump
//     appeared within the confirmation window, so there is no pod rollout to
//     wait on. It does NOT mean the config reached the pods — a hot-reload and a
//     rejected reconcile look identical from here. Proving the config landed
//     needs the per-pod configId probe (WaitForGatewayConfigID); this value only
//     says there is no Kubernetes rollout left to watch.
type RolloutMechanism string

const (
	MechanismPodRoll        RolloutMechanism = "pod-roll"
	MechanismNoRollObserved RolloutMechanism = "no-roll-observed"
)

// gatewayRollConfirmationWindow bounds how long we look for a pod roll to
// materialise after the operator has already accepted the spec.
//
// Callers reach the observation only after WaitForGatewayAccepted has confirmed
// status.observedGeneration caught up, so the operator has finished reconciling
// the generation we applied. This window covers only the residual gap between
// the operator writing that status and writing the Deployment — not "did
// anything happen at all", which the generation baseline answers outright.
//
// This window only affects gateways that cannot report a configId; the modern
// path detects a roll inline from the generation baseline (see
// waitForGatewayConfigID) and never observes it. Kept at 10s rather than shrunk:
// on that legacy fallback, reading a roll CFK writes to the Deployment a beat
// late as "no roll" would skip the replacement wait with no configId backstop.
//
// var so tests can shorten it.
var gatewayRollConfirmationWindow = 10 * time.Second

// GetGatewayDeploymentGeneration returns metadata.generation of the Deployment
// backing a Gateway CR.
//
// Call this immediately BEFORE applying a CR. metadata.generation is bumped by
// the API server on every write to the Deployment's spec, so comparing it
// against the value read afterwards is a positive test for "the operator
// decided to replace the pods" that holds even when the roll starts and
// finishes between two polls.
func (s *K8sService) GetGatewayDeploymentGeneration(ctx context.Context, namespace, gatewayName string) (int64, error) {
	config, err := clientcmd.BuildConfigFromFlags("", s.kubeConfigPath)
	if err != nil {
		return 0, fmt.Errorf("failed to build config: %w", err)
	}
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return 0, fmt.Errorf("failed to create clientset: %w", err)
	}
	return getGatewayDeploymentGeneration(ctx, clientset, namespace, gatewayName)
}

// getGatewayDeploymentGeneration is the inner lookup used by
// GetGatewayDeploymentGeneration. Split from the method so unit tests can inject
// a fake clientset.
func getGatewayDeploymentGeneration(ctx context.Context, clientset kubernetes.Interface, namespace, gatewayName string) (int64, error) {
	dep, err := resolveGatewayDeployment(ctx, clientset, namespace, gatewayName)
	if err != nil {
		return 0, err
	}
	slog.Debug("captured gateway deployment generation baseline",
		"namespace", namespace, "gateway", gatewayName, "deployment", dep.Name, "generation", dep.Generation)
	return dep.Generation, nil
}

// observeRolloutMechanism watches the backing Deployment until it can say
// whether the apply is rolling pods, returning as soon as a generation bump
// proves it is.
//
// baselineGeneration is the generation captured before the apply. A baseline of
// 0 means it could not be read, and any generation the Deployment reports then
// counts as a bump — the conservative direction, since it routes to the
// convergence wait rather than declaring there is nothing to wait for.
//
// The returned Deployment is the last one read, so callers do not have to
// re-fetch it to start their convergence loop.
func observeRolloutMechanism(ctx context.Context, clientset kubernetes.Interface, namespace, gatewayName string, baselineGeneration int64, pollInterval time.Duration) (RolloutMechanism, *appsv1.Deployment, error) {
	deadline := time.Now().Add(gatewayRollConfirmationWindow)

	var dep *appsv1.Deployment
	for {
		select {
		case <-ctx.Done():
			return "", nil, ctx.Err()
		default:
		}

		var err error
		dep, err = resolveGatewayDeployment(ctx, clientset, namespace, gatewayName)
		if err != nil {
			return "", nil, err
		}

		if dep.Generation > baselineGeneration {
			slog.Debug("gateway deployment generation advanced; pods are being replaced",
				"gateway", gatewayName, "baselineGeneration", baselineGeneration, "generation", dep.Generation)
			return MechanismPodRoll, dep, nil
		}

		if !time.Now().Before(deadline) {
			slog.Debug("no gateway deployment generation bump within the confirmation window; no pod rollout to wait for",
				"gateway", gatewayName, "generation", dep.Generation, "window", gatewayRollConfirmationWindow)
			return MechanismNoRollObserved, dep, nil
		}

		select {
		case <-ctx.Done():
			return "", nil, ctx.Err()
		case <-time.After(pollInterval):
		}
	}
}
