package gateway

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/confluentinc/kcp/internal/tracing"
	"go.opentelemetry.io/otel/attribute"
	"k8s.io/apimachinery/pkg/types"
)

// Reporter is the minimal user-facing output surface transitionVerifier
// needs. Both migration's and TBM's own unexported reporter types satisfy
// this structurally, without either package importing the other.
type Reporter interface {
	Detail(format string, args ...any)
	Success(format string, args ...any)
	Remediation(format string, args ...any)
}

// applyResult carries what an apply produced that a later verification needs
// to interpret the cluster's response.
type applyResult struct {
	// ConfigID is the config revision the API server stored, or "" when the
	// cluster cannot report one — the signal to verify by pod rollout instead.
	ConfigID string
	// BaselineDeploymentGeneration is the backing Deployment's
	// metadata.generation read immediately BEFORE the apply. 0 means it could
	// not be read, which the rollout waits treat as "any generation counts as
	// a bump".
	BaselineDeploymentGeneration int64
	// Generation is the Gateway CR's metadata.generation after the write, or 0
	// when the write never reached the cluster.
	Generation int64
	// PreApplyPodUIDs is the set of gateway pod UIDs (terminating ones included)
	// read immediately BEFORE the patch, on gateways whose pods roll. A later
	// verification waits for them to be gone. nil when the capability injects a
	// configId, because a hot-reloading gateway's pods never go away.
	PreApplyPodUIDs map[types.UID]struct{}
}

// excludePods returns a new set holding the pods of set that are not in
// exclude. Neither map is mutated. A nil set gives nil, so a hot-reload
// capture (which records no pods) keeps meaning exactly that.
func excludePods(set, exclude map[types.UID]struct{}) map[types.UID]struct{} {
	if set == nil {
		return nil
	}
	out := make(map[types.UID]struct{}, len(set))
	for uid := range set {
		if _, skip := exclude[uid]; !skip {
			out[uid] = struct{}{}
		}
	}
	return out
}

// transitionVerifier applies a gateway CR (or a configId-only stamp) and
// verifies the transition landed, by whichever means the cluster supports. It
// is the mechanism under Transitioner, which is its only caller: the
// Transitioner builds one per call and owns the per-run state (capability,
// plan baseline, start-of-run pod set) that it reads. The FSMs (AAO, TBM, d2s)
// never see this type; they reach the cluster only through Transitioner.Apply.
type transitionVerifier struct {
	Client           TransitionClient
	Reporter         Reporter
	Capability       Capability
	RolloutTimeout   time.Duration
	HotReloadTimeout time.Duration
	// Generation is the run's expected Gateway CR metadata.generation, owned by
	// the caller. Every patch is guarded on it and advances it to the
	// generation that write produced. nil, or a pointed-to 0, means unknown:
	// patches are unguarded and checkGeneration is a no-op.
	Generation *int64
}

// expectedGeneration returns the generation to guard on, 0 when unknown.
func (v *transitionVerifier) expectedGeneration() int64 {
	if v.Generation == nil {
		return 0
	}
	return *v.Generation
}

// recordGeneration advances the expected generation to the one a write
// produced. It runs whether or not the write verified: an unverified write
// still moved the generation.
func (v *transitionVerifier) recordGeneration(written int64) {
	if v.Generation != nil && written > 0 {
		*v.Generation = written
	}
}

// checkGeneration confirms the Gateway CR's metadata.generation is still the
// one kcp expects, with a single GET and no pod probes. It returns a
// *GenerationChangedError on a mismatch, and nil when the expected generation
// is unknown.
func (v *transitionVerifier) checkGeneration(ctx context.Context, namespace, crName, step string) error {
	expected := v.expectedGeneration()
	if expected == 0 {
		slog.Debug("generation check skipped: expected generation unknown", "step", step)
		return nil
	}
	observed, err := v.Client.GetGatewayGeneration(ctx, namespace, crName)
	if err != nil {
		return fmt.Errorf("failed to read the gateway generation during %s: %w", step, err)
	}
	if observed != expected {
		return &GenerationChangedError{Namespace: namespace, Gateway: crName, Expected: expected, Observed: observed}
	}
	return nil
}

// DeploymentBaseline reads the backing Deployment's generation immediately
// before an apply. A read failure is lenient only for the configId path and
// PatchConfigIDOnly: 0 makes the rollout path conservative rather than wrong.
// On rolling gateways PatchCR refuses to patch when the read fails.
func (v *transitionVerifier) DeploymentBaseline(ctx context.Context, namespace, crName, step string) int64 {
	baseline, err := v.Client.GetGatewayDeploymentGeneration(ctx, namespace, crName)
	if err != nil {
		slog.Debug("could not read the gateway deployment generation before applying; "+
			"any generation will count as a rollout", "step", step, "error", err)
		return 0
	}
	return baseline
}

// PatchCR patches a single route mutation onto the gateway CR, attaching a
// fresh config revision id when the cluster supports one (a fresh id guarantees
// the spec changes so metadata.generation always advances — same rationale as
// the whole-CR server-side apply this replaced).
func (v *transitionVerifier) PatchCR(ctx context.Context, namespace, crName string, rp RoutePatch, step string) (_ applyResult, err error) {
	ctx, span := tracing.Start(ctx, tracerName, "gateway.patch_cr",
		stepAttr(step), attribute.Bool("kcp.gateway.config_id_injected", v.Capability.InjectsConfigID()))
	defer func() { tracing.End(span, err) }()
	prep, err := strategyForCapability(v.Capability).prepare(ctx, v, namespace, crName, step)
	if err != nil {
		return applyResult{}, err
	}

	slog.Debug("patching gateway CR", "step", step, "gateway", crName,
		"route", rp.RouteName, "field", rp.Field, "configId", prep.configID, "baselineDeploymentGeneration", prep.baseline)

	patched, err := v.Client.PatchGatewayRoute(ctx, namespace, crName, rp, prep.configID, v.expectedGeneration())
	v.recordGeneration(patched.Generation)
	// An unverified write still reached the cluster, so the result is handed back
	// with the error either way.
	return applyResult{ConfigID: patched.ConfigID, BaselineDeploymentGeneration: prep.baseline, Generation: patched.Generation, PreApplyPodUIDs: prep.podUIDs}, err
}

// capturePods records the gateway pods that exist now, in a gateway.capture_pods
// span: a pod list on the API server the patch is about to write to, so its cost
// is part of what a rolling transition now adds.
func (v *transitionVerifier) capturePods(ctx context.Context, namespace, crName, step string) (uids map[types.UID]struct{}, err error) {
	ctx, span := tracing.Start(ctx, tracerName, "gateway.capture_pods", stepAttr(step))
	defer func() { tracing.End(span, err) }()
	uids, err = v.Client.GetGatewayPodUIDs(ctx, namespace, crName)
	if err == nil {
		span.SetAttributes(attribute.Int("kcp.gateway.pods", len(uids)))
	}
	return uids, err
}

// PatchConfigIDOnly stamps a fresh configId on the gateway via JSON Patch
// without touching anything else. Used only by SmokeTestHotReload.
func (v *transitionVerifier) PatchConfigIDOnly(ctx context.Context, namespace, crName, step string) (applyResult, error) {
	configID, err := NewConfigID()
	if err != nil {
		return applyResult{}, err
	}
	baseline := v.DeploymentBaseline(ctx, namespace, crName, step)
	slog.Debug("patching gateway configId only", "step", step, "gateway", crName,
		"configId", configID, "baselineDeploymentGeneration", baseline)
	patched, err := v.Client.PatchGatewayConfigID(ctx, namespace, crName, configID, v.expectedGeneration())
	v.recordGeneration(patched.Generation)
	return applyResult{ConfigID: patched.ConfigID, BaselineDeploymentGeneration: baseline, Generation: patched.Generation}, err
}

// WaitForAccepted blocks until the Confluent operator has accepted the
// gateway CR just applied, and must be called after every apply and before
// the Deployment-based readiness/pod waits.
//
// Those waits only ever look at the apps/v1 Deployment. When the operator
// rejects a CR it never touches the Deployment, so the Deployment sits
// complete and healthy running the *previous* generation's pods, still at
// the generation the baseline captured — the readiness wait sees no rollout
// to converge on, reports "No pod restart required" and returns nil. That is
// how a switchover whose CR referenced a missing secret was reported as a
// completed migration while the gateway stayed fenced and every client
// stayed blocked. Confirming the operator accepted the spec is the only
// signal that distinguishes a genuine no-op apply from a refused one.
//
// step names the phase for the error message ("fence", "switchover",
// "unfence"). An operator rejection is returned as-is: GatewayRejectedError
// already carries the operator's own reason and message, and callers can
// errors.As it.
func (v *transitionVerifier) WaitForAccepted(ctx context.Context, namespace, crName, step string) (err error) {
	ctx, span := tracing.Start(ctx, tracerName, "gateway.wait_accepted", stepAttr(step))
	defer func() { tracing.End(span, err) }()
	v.Reporter.Detail("Waiting for gateway reconcile...")
	slog.Debug("waiting for gateway acceptance", "step", step, "gateway", crName, "rolloutTimeout", v.RolloutTimeout)

	err = v.Client.WaitForGatewayAccepted(ctx, namespace, crName, 2*time.Second, v.RolloutTimeout)
	if err == nil {
		return nil
	}

	var rejected *GatewayRejectedError
	if errors.As(err, &rejected) {
		v.Reporter.Remediation("Confluent operator rejected the %s gateway spec. Inspect its view of the gateway:\n"+
			"   kubectl -n %s get gateway %s -o jsonpath='{.status.conditions}'", step, namespace, crName)
		return err
	}
	return fmt.Errorf("failed waiting for gateway reconcile during %s: %w", step, err)
}

// WaitForConfigApplied blocks until every ready gateway pod reports the
// applied configId. This is the mechanism-agnostic check: it holds whether CFK
// chose a hot-reload or a pod roll, so kcp never has to predict which.
//
// Correctness does not depend on the mechanism, but the budget does. A
// hot-reload converges in seconds and gets the bounded hot-reload budget; a roll
// has to pull images and pass readiness probes, so the moment one is observed the
// wait switches to the user's --rollout-timeout. Without that switch a
// transition that legitimately rolls — a switchover whose CR adds a route or a
// TLS secret, exactly the case that motivated per-pod verification — would be cut
// off at 90s with traffic already fenced.
func (v *transitionVerifier) WaitForConfigApplied(ctx context.Context, namespace, crName string, port int, applied applyResult, step string) (err error) {
	ctx, span := tracing.Start(ctx, tracerName, "gateway.wait_config_applied", stepAttr(step))
	defer func() { tracing.End(span, err) }()
	v.Reporter.Detail("Waiting for every gateway pod to apply the new config...")
	slog.Debug("waiting for per-pod configId", "step", step, "configId", applied.ConfigID,
		"port", port, "hotReloadTimeout", v.HotReloadTimeout,
		"rollTimeout", v.RolloutTimeout, "baselineDeploymentGeneration", applied.BaselineDeploymentGeneration)

	err = v.Client.WaitForGatewayConfigID(ctx, namespace, crName, ConfigWaitOptions{
		ConfigID:                     applied.ConfigID,
		Port:                         port,
		BaselineDeploymentGeneration: applied.BaselineDeploymentGeneration,
		// Load-bearing for fence correctness, not just a latency knob. This wait
		// returns up to one interval after the gateway actually converged, and
		// detectUnroutedProducers takes its first offset snapshot the instant it
		// returns — with no tolerance, so a single late message aborts the
		// migration for a rogue producer that does not exist. Measured against a
		// real licensed gateway (see integration-tests/migration-hot-reload), the
		// last acknowledged write landed 0.4s-2.5s BEFORE this returned, scattered
		// across the interval: that margin is supplied by this poll lag, not by
		// the fence, whose own settle time looks like roughly zero. Shrinking this
		// shrinks the margin toward zero with it.
		PollInterval:     2 * time.Second,
		HotReloadTimeout: v.HotReloadTimeout,
		RollTimeout:      v.RolloutTimeout,
		OnProgress:       v.printConfigWaitProgress,
	})
	if err != nil {
		return fmt.Errorf("failed waiting for the gateway to apply the %s config on every pod: %w", step, err)
	}

	v.Reporter.Success("All gateway pods have applied the new config")
	return nil
}

// VerifyTransition confirms a transition landed, by whichever means the
// cluster supports. An empty applied.ConfigID means the cluster cannot
// report a config revision, so this falls back to waiting for the pods the
// apply replaces to be gone.
func (v *transitionVerifier) VerifyTransition(ctx context.Context, namespace, crName string, port int, applied applyResult, step string) (err error) {
	ctx, span := tracing.Start(ctx, tracerName, "gateway.verify_transition", stepAttr(step))
	defer func() { tracing.End(span, err) }()
	strategy := strategyForApplied(applied)
	span.SetAttributes(attribute.String("kcp.gateway.mechanism", strategy.name()))
	return strategy.confirm(ctx, v, namespace, crName, port, applied, step)
}

// printConfigWaitProgress renders one line per poll tick of the per-pod
// configId wait. The converged tick is silent — the caller prints the
// success line.
func (v *transitionVerifier) printConfigWaitProgress(p ConfigWaitProgress) {
	if p.Converged {
		return
	}
	// Naming the mechanism explains why a wait is taking as long as it is: a
	// roll has pods to replace, an in-place apply does not. It is only ever a
	// description of what has been observed — the verdict comes from the pods
	// reporting the configId, never from the mechanism.
	if p.Mechanism == MechanismPodRoll {
		terminating := ""
		if p.PodsTerminating > 0 {
			terminating = fmt.Sprintf(", %d terminating", p.PodsTerminating)
		}
		v.Reporter.Detail("Gateway pods are rolling — %d/%d have applied the new config%s (elapsed %s)",
			p.PodsAtWant, p.PodsReady, terminating, FormatElapsed(p.Elapsed))
		return
	}
	terminating := ""
	if p.PodsTerminating > 0 {
		terminating = fmt.Sprintf(", %d terminating", p.PodsTerminating)
	}
	v.Reporter.Detail("Applying in place, no pod restart — %d/%d gateway pods have applied the new config%s (elapsed %s)",
		p.PodsAtWant, p.PodsReady, terminating, FormatElapsed(p.Elapsed))
}

// maxOldPodNamesShown caps how many remaining old pod names the progress line lists.
const maxOldPodNamesShown = 3

// printPodRolloutProgress renders WaitForGatewayPods progress: how many new
// pods are Ready and how many of the pre-apply pods still remain.
func (v *transitionVerifier) printPodRolloutProgress(p PodRolloutProgress) {
	if !p.RolloutDetected {
		v.Reporter.Success("No pod restart required")
		return
	}
	waiting := ""
	if n := len(p.OldPodNames); n > 0 {
		shown := p.OldPodNames
		suffix := ""
		if n > maxOldPodNamesShown {
			shown = shown[:maxOldPodNamesShown]
			suffix = fmt.Sprintf(" and %d more", n-maxOldPodNamesShown)
		}
		waiting = fmt.Sprintf(" (waiting on %s%s)", strings.Join(shown, ", "), suffix)
	}
	v.Reporter.Detail("%d/%d new pods ready, %d old pods remaining%s (elapsed %s)",
		p.NewPodsReady, p.InitialPodCount, p.OldPodsRemaining, waiting, FormatElapsed(p.Elapsed))
}

// SmokeTestHotReload proves the gateway really does apply config revisions,
// before any traffic-affecting change is made.
//
// It WRITES to the cluster: it patches a fresh spec.configId onto the LIVE
// Gateway CR, then waits for the operator to accept it and for every pod to
// report that configId. It is not a read-only check; nothing else on the CR
// is changed.
//
// This closes a hole that no Kubernetes or CFK signal can: the gateway gates
// its config-file watcher on an Enterprise licence, so with a trial licence
// spec.hotReload.enabled is true, CFK renders the new config, promotes the
// shared ConfigMap, projects it into every pod, and reports
// hot-reload-status=Succeeded — while the gateway never applies it and
// /config keeps serving the previous revision. Detecting that after fencing
// would mean discovering it with traffic already blocked.
//
// The check applies spec.configId alone, as a JSON Patch
// (Client.PatchGatewayConfigID) rather than re-applying the live spec under
// the caller's usual manager. That used to be the design — re-apply the live
// CR verbatim plus a fresh configId — and it was safe to run at any point in
// a migration for the same reason it was dangerous: server-side apply shares
// field ownership by manager, so declaring the whole live spec under the
// same manager the fence and switchover CRs apply under pre-seeded that
// manager's ownership of every field on the gateway. The fence apply — which
// typically omits fields the live spec carries and the fenced CR does not
// repeat, e.g. spec.hotReload when the fenced CR relies on inheriting it —
// then became a narrowing apply under that same manager, and server-side
// apply prunes a field an earlier apply from the same manager declared once
// a later one omits it. A JSON Patch naming only spec.configId declares no
// manager and no spec, so it can't create that hazard either, which is what
// makes this safe to run at any point in a migration, including a resume.
func (v *transitionVerifier) SmokeTestHotReload(ctx context.Context, namespace, crName string, port int) (err error) {
	if !v.Capability.InjectsConfigID() {
		return nil
	}
	ctx, span := tracing.Start(ctx, tracerName, "gateway.verify_hot_reload")
	defer func() { tracing.End(span, err) }()

	v.Reporter.Detail("Checking the gateway applies config revisions in place...")

	applied, err := v.PatchConfigIDOnly(ctx, namespace, crName, "hot-reload check")
	if err != nil {
		return fmt.Errorf("failed to apply the gateway hot-reload check: %w", err)
	}
	if applied.ConfigID == "" {
		return fmt.Errorf("the gateway hot-reload check applied no config revision")
	}

	if err := v.WaitForAccepted(ctx, namespace, crName, "hot-reload check"); err != nil {
		return err
	}

	if err := v.WaitForConfigApplied(ctx, namespace, crName, port, applied, "hot-reload check"); err != nil {
		v.Reporter.Remediation("A configId-only change must hot-reload without restarting pods. When it never reaches the pods, the\n"+
			"   gateway's config watcher is not running — most often because the gateway holds a trial rather than an\n"+
			"   Enterprise licence. CFK reports success regardless, so check the gateway itself:\n"+
			"   kubectl -n %s logs -l app=%s | grep -i hot-reload", namespace, crName)
		return err
	}

	return nil
}

// ResolveCapability probes the live gateway (via fenced/switched CR bytes,
// since applying either is what puts spec.hotReload into force — a detector
// shown only the live gateway could pick rollout verification for a
// migration that will hot-reload, and then observe nothing) to determine how
// transitions will be verified, and records the result on the verifier.
// Returns the resolved Capability so callers can layer their own
// package-specific bookkeeping (persisting it, warning on a changed mode,
// reporting a mode-specific message) on top.
func (v *transitionVerifier) ResolveCapability(ctx context.Context, namespace, crName string, port int, fencedCRYAML, switchedCRYAML []byte) (_ Capability, err error) {
	ctx, span := tracing.Start(ctx, tracerName, "gateway.resolve_capability")
	defer func() { tracing.End(span, err) }()
	capability, err := v.Client.DetectCapability(ctx, namespace, crName, port, fencedCRYAML, switchedCRYAML)
	if err != nil {
		return Capability{}, fmt.Errorf("failed to determine how gateway transitions can be verified: %w", err)
	}
	v.Capability = capability
	if capability.Advisory != "" {
		v.Reporter.Detail("%s", capability.Advisory)
	}
	slog.Debug("resolved gateway verification capability",
		"mode", capability.Mode, "crdSupportsConfigId", capability.CRDSupportsConfigID,
		"hotReloadEnabled", capability.HotReloadEnabled)
	span.SetAttributes(
		attribute.String("kcp.gateway.mode", string(capability.Mode)),
		attribute.Bool("kcp.gateway.hot_reload", capability.HotReloadEnabled),
		attribute.Bool("kcp.gateway.crd_supports_config_id", capability.CRDSupportsConfigID),
	)
	return capability, nil
}
