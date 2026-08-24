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
	// It is a ceiling, not a wait: WaitForGatewayConfigID returns the moment every
	// pod reports the new configId, so this is only ever spent on failure. Measured
	// convergence is ~4-6s locally and ~14s on EKS, so 90s is deliberately generous
	// — a timeout here is terminal, and waiting longer costs far less than failing a
	// switchover that was about to succeed. Lowering it buys nothing on the happy
	// path and only makes that false failure more likely.
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

	// Mechanism is what the cluster has been observed doing so far, which
	// selects the budget and the wording. It can change from
	// MechanismNoRollObserved to MechanismPodRoll mid-wait, never back.
	Mechanism RolloutMechanism

	Elapsed   time.Duration
	Converged bool
}

// podProber reads one gateway pod's config endpoint. Injected so the wait logic
// is testable without standing up an HTTP server per pod.
type podProber func(ctx context.Context, endpoint GatewayPodEndpoint) (ProbeResult, error)

// ConfigWaitOptions parameterises a per-pod configId wait.
//
// The two budgets exist because the same verification covers both mechanisms but
// they take wildly different amounts of time. A hot-reload converges in seconds,
// so a short, always-bounded budget is right and a timeout is a real failure. A
// roll has to pull images and pass readiness probes, and the user already has a
// knob for how long they will tolerate that.
type ConfigWaitOptions struct {
	// ConfigID is the revision every Ready pod must report.
	ConfigID string

	// Port serves GET /config. 0 selects DefaultGatewayConfigPort.
	Port int

	// BaselineDeploymentGeneration is the backing Deployment's
	// metadata.generation from immediately before the apply. A generation past
	// it means CFK chose to replace the pods, which switches the wait to
	// RollTimeout. 0 means it could not be read, and any generation then counts
	// as a roll — the conservative direction, since it grants the longer budget
	// rather than cutting a real rollout short.
	BaselineDeploymentGeneration int64

	PollInterval time.Duration

	// HotReloadTimeout bounds the wait for as long as no roll has been observed.
	// 0 selects DefaultHotReloadTimeout; it is deliberately never unbounded,
	// because a hot-reload moves no Kubernetes signal that could be waited on
	// instead.
	HotReloadTimeout time.Duration

	// RollTimeout replaces HotReloadTimeout the moment a roll is observed. 0
	// means no deadline, matching --rollout-timeout's default: the operator
	// drives a rollout to completion and the user can interrupt.
	RollTimeout time.Duration

	OnProgress func(ConfigWaitProgress)
}

// WaitForGatewayConfigID blocks until every Ready gateway pod reports
// opts.ConfigID, or fails.
func (s *K8sService) WaitForGatewayConfigID(ctx context.Context, namespace, gatewayName string, opts ConfigWaitOptions) error {
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
		return probeGatewayConfig(ctx, client, gatewayConfigAddr(endpoint.IP, opts.Port))
	}

	return waitForGatewayConfigID(ctx, clientset, probe, namespace, gatewayName, opts)
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
//
// The budget adapts as the mechanism reveals itself. The wait starts on the
// hot-reload budget and switches to the roll budget the moment the Deployment's
// generation moves past the pre-apply baseline. That check is free: the loop
// already reads the Deployment every poll for the rollout clause.
//
// Doing it inside the loop rather than deciding up front matters twice over.
// Deciding up front would mean paying the roll-confirmation window before the
// first probe, adding ~10s to a hot-reload that converges in ~2s. And a roll is
// not always apparent immediately — CFK can write the Deployment a moment after
// it writes the CR status — so a wait that committed to the short budget at the
// start could still time out mid-rollout, with traffic already fenced.
func waitForGatewayConfigID(ctx context.Context, clientset kubernetes.Interface, probe podProber, namespace, gatewayName string, opts ConfigWaitOptions) error {
	want := opts.ConfigID
	if want == "" {
		return fmt.Errorf("cannot wait for an empty gateway configId: a gateway that has never applied a revision reports none, and would match immediately")
	}

	hotReloadTimeout := opts.HotReloadTimeout
	if hotReloadTimeout <= 0 {
		hotReloadTimeout = DefaultHotReloadTimeout
	}

	start := time.Now()
	mechanism := MechanismNoRollObserved

	// budgetExpired reports whether the wait has run past whichever budget the
	// currently-observed mechanism selects.
	budgetExpired := func() bool {
		if mechanism == MechanismPodRoll {
			if opts.RollTimeout <= 0 {
				return false
			}
			return time.Since(start) >= opts.RollTimeout
		}
		return time.Since(start) >= hotReloadTimeout
	}

	// Retained for the timeout message, which reports counts rather than pod
	// identities.
	var lastProgress ConfigWaitProgress

	for !budgetExpired() {
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

		// A known baseline is required to widen the budget, which is the opposite
		// of how observeRolloutMechanism treats an unknown one — deliberately.
		// There, an unknown baseline routes to the convergence wait, which is the
		// cautious choice because the alternative is declaring there is nothing to
		// wait for. Here the roll budget is usually unbounded, so accepting an
		// unknown baseline as evidence of a roll would silently convert this into
		// a wait with no deadline — and hanging forever is precisely the outcome
		// on a gateway whose config watcher never started, which is the failure
		// this budget exists to surface. A live Deployment's generation is never
		// 0, so this only excludes the case where the pre-apply read failed.
		if mechanism == MechanismNoRollObserved &&
			opts.BaselineDeploymentGeneration > 0 &&
			dep.Generation > opts.BaselineDeploymentGeneration {
			mechanism = MechanismPodRoll
			slog.Debug("gateway deployment generation advanced during the config wait; switching to the rollout budget",
				"gateway", gatewayName, "baselineGeneration", opts.BaselineDeploymentGeneration,
				"generation", dep.Generation, "rollTimeout", opts.RollTimeout)
		}

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
			Mechanism:       mechanism,
			Elapsed:         time.Since(start),
			Converged:       converged,
		}
		if opts.OnProgress != nil {
			opts.OnProgress(lastProgress)
		}

		if converged {
			slog.Debug("all ready gateway pods report the applied configId",
				"gateway", gatewayName, "pods", readyCount, "mechanism", mechanism, "elapsed", time.Since(start))
			return nil
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(opts.PollInterval):
		}
	}

	spent := hotReloadTimeout
	if mechanism == MechanismPodRoll {
		spent = opts.RollTimeout
	}
	return fmt.Errorf("timed out after %s waiting for the gateway to apply the configId on every pod: %d of %d ready pods report it, deployment rollout complete=%t, observed mechanism=%s. Treat this as a failure rather than as still propagating — a change rejected by CFK's canary is indistinguishable from one still in flight until the deadline",
		spent, lastProgress.PodsAtWant, lastProgress.PodsReady, lastProgress.RolloutComplete, mechanism)
}
