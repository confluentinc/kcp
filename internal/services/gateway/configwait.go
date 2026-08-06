package gateway

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

const (
	// configProbeRequestTimeout bounds a single GET /config call. Short, because
	// it is retried every poll and a pod that is slow to answer is indistinguishable
	// from one that is not answering.
	configProbeRequestTimeout = 5 * time.Second

	// DefaultHotReloadTimeout is the budget for a hot-reload to reach every pod.
	//
	// Measured convergence on a working gateway is ~14s, so this is deliberately
	// generous: a timeout here is terminal, and the cost of waiting slightly
	// longer is far lower than the cost of failing a switchover that was about to
	// succeed. It matches the budget the rig's verify.sh uses.
	DefaultHotReloadTimeout = 90 * time.Second
)

// ConfigWaitProgress reports the state of a per-pod configId wait.
type ConfigWaitProgress struct {
	// Want is the configId being waited for.
	Want string

	// PodsTotal is every gateway pod with an IP, ready or not.
	PodsTotal int

	// PodsReady is how many of those are Ready, and therefore probed.
	PodsReady int

	// PodsAtWant is how many ready pods report Want.
	PodsAtWant int

	// RolloutComplete is the backing Deployment's rollout state.
	RolloutComplete bool

	Elapsed   time.Duration
	Converged bool
}

// podProber reads one gateway pod's config endpoint. Injected so the wait logic
// is testable without standing up an HTTP server per pod.
type podProber func(ctx context.Context, endpoint GatewayPodEndpoint) (ProbeResult, error)

// WaitForGatewayConfigID blocks until every Ready gateway pod reports configID,
// or fails. timeout == 0 means no deadline, consistent with the other waits.
func (s *K8sService) WaitForGatewayConfigID(ctx context.Context, namespace, gatewayName, configID string, port int, pollInterval, timeout time.Duration, onProgress func(ConfigWaitProgress)) error {
	config, err := clientcmd.BuildConfigFromFlags("", s.kubeConfigPath)
	if err != nil {
		return fmt.Errorf("failed to build config: %w", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("failed to create clientset: %w", err)
	}

	client := newConfigProbeClient(configProbeRequestTimeout)
	probe := func(ctx context.Context, endpoint GatewayPodEndpoint) (ProbeResult, error) {
		return probeGatewayConfig(ctx, client, gatewayConfigAddr(endpoint.IP, port))
	}

	return waitForGatewayConfigID(ctx, clientset, probe, namespace, gatewayName, configID, pollInterval, timeout, onProgress)
}

// waitForGatewayConfigID is the inner orchestration used by
// WaitForGatewayConfigID. Split from the method so unit tests can inject a fake
// clientset and a stub prober.
//
// This is the mechanism-agnostic verification the whole design rests on. The
// predicate is:
//
//	the Deployment's rollout is complete
//	AND at least one pod is Ready
//	AND every Ready pod reports the wanted configId
//
// It holds for a hot-reload and for a pod roll alike, so kcp never has to
// predict which one CFK chose. A hot-reload has the running gateway apply the
// new config file in place; a roll starts replacements from that same file. In
// both cases the file carries the wanted configId, and in both cases the
// Deployment ends up rollout-complete — immediately for a hot-reload, eventually
// for a roll.
//
// Each clause is load-bearing:
//
//   - Deployment rollout: absorbs the pod-set churn of a roll, where surviving
//     old pods may already report the wanted id while replacements are still
//     coming up. Without it, a roll could be declared converged halfway through.
//   - at least one Ready pod: an all-of over an empty set is vacuously true, so
//     without this an empty or entirely unready pod set would read as success.
//   - fresh enumeration every poll: a snapshot taken before the apply goes stale
//     the moment a roll replaces a pod.
func waitForGatewayConfigID(ctx context.Context, clientset kubernetes.Interface, probe podProber, namespace, gatewayName, want string, pollInterval, timeout time.Duration, onProgress func(ConfigWaitProgress)) error {
	if want == "" {
		return fmt.Errorf("cannot wait for an empty gateway configId: a gateway that has never applied a revision reports none, and would match immediately")
	}

	noDeadline := timeout <= 0
	deadline := time.Now().Add(timeout)
	start := time.Now()

	// Retained for the timeout message, which reports counts rather than pod
	// identities.
	var lastProgress ConfigWaitProgress

	for noDeadline || time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		endpoints, err := listGatewayPodEndpoints(ctx, clientset, namespace, gatewayName)
		if err != nil {
			return err
		}

		dep, err := resolveGatewayDeployment(ctx, clientset, namespace, gatewayName)
		if err != nil {
			return err
		}
		rolloutComplete := deploymentRolloutComplete(dep)

		readyCount := 0
		atWant := 0
		absentCount := 0
		for _, endpoint := range endpoints {
			// Not-ready pods are not probed: a pod still starting has nothing
			// meaningful to report, and requiring it would deadlock a roll.
			if !endpoint.Ready {
				continue
			}
			readyCount++

			result, err := probe(ctx, endpoint)
			if err != nil {
				return fmt.Errorf("failed to probe the gateway config endpoint: %w", err)
			}

			switch result.Outcome {
			case ProbeEndpointAbsent:
				// Counted rather than failed on immediately. A 404 from *every*
				// pod means the image does not serve /config and no amount of
				// waiting will change that. A 404 from only some pods means a
				// mixed-image roll is in progress — an image change is itself a
				// rolling change — and the pods still coming up may well serve it.
				absentCount++
				slog.Debug("gateway pod does not serve the config endpoint", "pod", endpoint.Name, "addr", result.Addr)
			case ProbeApplied:
				if result.ConfigID == want {
					atWant++
				} else {
					slog.Debug("gateway pod still reports a previous configId", "pod", endpoint.Name, "addr", result.Addr, "got", result.ConfigID, "want", want)
				}
			case ProbeNeverSet:
				slog.Debug("gateway pod reports no applied configId yet", "pod", endpoint.Name, "addr", result.Addr)
			case ProbeUnreachable, ProbeUnexpected:
				// Kept non-terminal on purpose. A pod that has just gone Ready can
				// briefly refuse the connection; the terminal reachability verdict
				// belongs to the init-time capability probe, not to a
				// mid-migration wait that would flap on a transient.
				slog.Debug("gateway config endpoint probe did not yield a configId", "pod", endpoint.Name, "outcome", result.Outcome, "error", result.Err)
			}
		}

		// Unanimous 404: the image does not serve /config, so this will never
		// succeed and waiting out the timeout only delays the real cause.
		if readyCount > 0 && absentCount == readyCount {
			return fmt.Errorf("no gateway pod serves %s (HTTP 404 from all %d ready pods); the gateway image predates 1.3.0, so a configId cannot be verified per pod", GatewayConfigEndpointPath, readyCount)
		}

		converged := rolloutComplete && readyCount > 0 && atWant == readyCount

		lastProgress = ConfigWaitProgress{
			Want:            want,
			PodsTotal:       len(endpoints),
			PodsReady:       readyCount,
			PodsAtWant:      atWant,
			RolloutComplete: rolloutComplete,
			Elapsed:         time.Since(start),
			Converged:       converged,
		}
		if onProgress != nil {
			onProgress(lastProgress)
		}

		if converged {
			slog.Debug("all ready gateway pods report the applied configId",
				"gateway", gatewayName, "pods", readyCount, "elapsed", time.Since(start))
			return nil
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(pollInterval):
		}
	}

	return fmt.Errorf("timed out after %s waiting for the gateway to apply the configId on every pod: %d of %d ready pods report it, deployment rollout complete=%t. Treat this as a failure rather than as still propagating — a change rejected by CFK's canary is indistinguishable from one still in flight until the deadline",
		timeout, lastProgress.PodsAtWant, lastProgress.PodsReady, lastProgress.RolloutComplete)
}
