package gateway

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"
)

// Reporter is the minimal user-facing output surface TransitionVerifier
// needs. Both migration's and TBM's own unexported reporter types satisfy
// this structurally, without either package importing the other.
type Reporter interface {
	Detail(format string, args ...any)
	Success(format string, args ...any)
	Remediation(format string, args ...any)
}

// ApplyResult carries what an apply produced that a later verification needs
// to interpret the cluster's response.
type ApplyResult struct {
	// ConfigID is the config revision the API server stored, or "" when the
	// cluster cannot report one — the signal to verify by pod rollout instead.
	ConfigID string
	// BaselineDeploymentGeneration is the backing Deployment's
	// metadata.generation read immediately BEFORE the apply. 0 means it could
	// not be read, which the rollout waits treat as "any generation counts as
	// a bump".
	BaselineDeploymentGeneration int64
}

// TransitionVerifier applies a gateway CR (or a configId-only stamp) and
// verifies the transition landed, by whichever means the cluster supports.
// Both the AAO (migration) and TBM orchestrators drive an identical gateway
// CR through this same apply/wait/verify mechanism — previously two
// hand-maintained copies (a correctness fix to one silently leaving the
// other stale). What differs between the two — how each derives the CR
// bytes, and what each does with a newly resolved capability (persisting it,
// warning on a changed mode, reporting a mode-specific message) — stays in
// each package's own thin wrapper around this type.
type TransitionVerifier struct {
	Service          Service
	Reporter         Reporter
	Capability       Capability
	RolloutTimeout   time.Duration
	HotReloadTimeout time.Duration
}

// HotReloadTimeoutOrDefault returns the configId verification deadline,
// never unbounded — a hot-reload moves no Kubernetes signal to wait on.
func (v *TransitionVerifier) HotReloadTimeoutOrDefault() time.Duration {
	if v.HotReloadTimeout <= 0 {
		return DefaultHotReloadTimeout
	}
	return v.HotReloadTimeout
}

// DeploymentBaseline reads the backing Deployment's generation immediately
// before an apply. A read failure is not fatal — 0 makes the rollout path
// conservative rather than wrong.
func (v *TransitionVerifier) DeploymentBaseline(ctx context.Context, namespace, crName, step string) int64 {
	baseline, err := v.Service.GetGatewayDeploymentGeneration(ctx, namespace, crName)
	if err != nil {
		slog.Debug("could not read the gateway deployment generation before applying; "+
			"any generation will count as a rollout", "step", step, "error", err)
		return 0
	}
	return baseline
}

// ApplyCR applies a gateway CR, attaching a fresh config revision id when the
// cluster supports one. A fresh id on every apply guarantees the spec
// changes, so metadata.generation always advances — closing the no-op blind
// spot where an apply that changed nothing leaves observedGeneration already
// satisfied and every downstream wait reports success for a transition that
// never happened.
func (v *TransitionVerifier) ApplyCR(ctx context.Context, namespace, crName string, yamlData []byte, step string) (ApplyResult, error) {
	var configID string
	if v.Capability.InjectsConfigID() {
		var err error
		configID, err = NewConfigID()
		if err != nil {
			return ApplyResult{}, err
		}
	}

	baseline := v.DeploymentBaseline(ctx, namespace, crName, step)

	slog.Debug("applying gateway CR", "step", step, "gateway", crName,
		"configId", configID, "baselineDeploymentGeneration", baseline)

	storedConfigID, err := v.Service.ApplyGatewayYAML(ctx, namespace, crName, yamlData, configID)
	if err != nil {
		return ApplyResult{}, err
	}
	return ApplyResult{ConfigID: storedConfigID, BaselineDeploymentGeneration: baseline}, nil
}

// ApplyConfigIDOnly stamps a fresh configId on the gateway without applying —
// or owning — anything else. Used only by VerifyHotReloadCapability; every
// other caller needs the CR's actual spec change and uses ApplyCR.
func (v *TransitionVerifier) ApplyConfigIDOnly(ctx context.Context, namespace, crName, step string) (ApplyResult, error) {
	configID, err := NewConfigID()
	if err != nil {
		return ApplyResult{}, err
	}

	baseline := v.DeploymentBaseline(ctx, namespace, crName, step)

	slog.Debug("applying gateway configId only", "step", step, "gateway", crName,
		"configId", configID, "baselineDeploymentGeneration", baseline)

	storedConfigID, err := v.Service.ApplyGatewayConfigID(ctx, namespace, crName, configID)
	if err != nil {
		return ApplyResult{}, err
	}
	return ApplyResult{ConfigID: storedConfigID, BaselineDeploymentGeneration: baseline}, nil
}

// PatchCR patches a single route mutation onto the gateway CR, attaching a
// fresh config revision id when the cluster supports one (a fresh id guarantees
// the spec changes so metadata.generation always advances — same rationale as
// the former ApplyCR).
func (v *TransitionVerifier) PatchCR(ctx context.Context, namespace, crName string, rp RoutePatch, step string) (ApplyResult, error) {
	var configID string
	if v.Capability.InjectsConfigID() {
		var err error
		configID, err = NewConfigID()
		if err != nil {
			return ApplyResult{}, err
		}
	}

	baseline := v.DeploymentBaseline(ctx, namespace, crName, step)

	slog.Debug("patching gateway CR", "step", step, "gateway", crName,
		"route", rp.RouteName, "field", rp.Field, "configId", configID, "baselineDeploymentGeneration", baseline)

	storedConfigID, err := v.Service.PatchGatewayRoute(ctx, namespace, crName, rp, configID)
	if err != nil {
		return ApplyResult{}, err
	}
	return ApplyResult{ConfigID: storedConfigID, BaselineDeploymentGeneration: baseline}, nil
}

// PatchConfigIDOnly stamps a fresh configId on the gateway via JSON Patch
// without touching anything else. Used only by VerifyHotReloadCapability.
func (v *TransitionVerifier) PatchConfigIDOnly(ctx context.Context, namespace, crName, step string) (ApplyResult, error) {
	configID, err := NewConfigID()
	if err != nil {
		return ApplyResult{}, err
	}
	baseline := v.DeploymentBaseline(ctx, namespace, crName, step)
	slog.Debug("patching gateway configId only", "step", step, "gateway", crName,
		"configId", configID, "baselineDeploymentGeneration", baseline)
	storedConfigID, err := v.Service.PatchGatewayConfigID(ctx, namespace, crName, configID)
	if err != nil {
		return ApplyResult{}, err
	}
	return ApplyResult{ConfigID: storedConfigID, BaselineDeploymentGeneration: baseline}, nil
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
func (v *TransitionVerifier) WaitForAccepted(ctx context.Context, namespace, crName, step string) error {
	v.Reporter.Detail("Waiting for gateway reconcile...")
	slog.Debug("waiting for gateway acceptance", "step", step, "gateway", crName, "rolloutTimeout", v.RolloutTimeout)

	err := v.Service.WaitForGatewayAccepted(ctx, namespace, crName, 2*time.Second, v.RolloutTimeout)
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
// applied configId.
func (v *TransitionVerifier) WaitForConfigApplied(ctx context.Context, namespace, crName string, port int, applied ApplyResult, step string) error {
	v.Reporter.Detail("Waiting for every gateway pod to apply the new config...")
	slog.Debug("waiting for per-pod configId", "step", step, "configId", applied.ConfigID,
		"port", port, "hotReloadTimeout", v.HotReloadTimeoutOrDefault(),
		"rollTimeout", v.RolloutTimeout, "baselineDeploymentGeneration", applied.BaselineDeploymentGeneration)

	err := v.Service.WaitForGatewayConfigID(ctx, namespace, crName, ConfigWaitOptions{
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
		HotReloadTimeout: v.HotReloadTimeoutOrDefault(),
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
// report a config revision, so this falls back to the Deployment rollout
// wait.
func (v *TransitionVerifier) VerifyTransition(ctx context.Context, namespace, crName string, port int, applied ApplyResult, step string) error {
	if applied.ConfigID != "" {
		return v.WaitForConfigApplied(ctx, namespace, crName, port, applied, step)
	}

	v.Reporter.Detail("Waiting for gateway readiness...")
	slog.Debug("waiting for gateway readiness", "step", step, "rolloutTimeout", v.RolloutTimeout,
		"baselineDeploymentGeneration", applied.BaselineDeploymentGeneration)

	if err := v.Service.WaitForGatewayReady(ctx, namespace, crName,
		applied.BaselineDeploymentGeneration, 5*time.Second, v.RolloutTimeout, v.printGatewayReadinessProgress); err != nil {
		return fmt.Errorf("failed waiting for gateway readiness during %s: %w", step, err)
	}
	return nil
}

// printConfigWaitProgress renders one line per poll tick of the per-pod
// configId wait. The converged tick is silent — the caller prints the
// success line.
func (v *TransitionVerifier) printConfigWaitProgress(p ConfigWaitProgress) {
	if p.Converged {
		return
	}
	// Naming the mechanism explains why a wait is taking as long as it is: a
	// roll has pods to replace, an in-place apply does not. It is only ever a
	// description of what has been observed — the verdict comes from the pods
	// reporting the configId, never from the mechanism.
	if p.Mechanism == MechanismPodRoll {
		v.Reporter.Detail("Gateway pods are rolling — %d/%d have applied the new config (elapsed %s)",
			p.PodsAtWant, p.PodsReady, FormatElapsed(p.Elapsed))
		return
	}
	v.Reporter.Detail("Applying in place, no pod restart — %d/%d gateway pods have applied the new config (elapsed %s)",
		p.PodsAtWant, p.PodsReady, FormatElapsed(p.Elapsed))
}

// printGatewayReadinessProgress renders WaitForGatewayReady progress.
func (v *TransitionVerifier) printGatewayReadinessProgress(p GatewayReadinessProgress) {
	if !p.RolloutDetected {
		v.Reporter.Success("No pod restart required")
		return
	}
	if p.InitialPodCount > 0 {
		v.Reporter.Detail("%d/%d pods ready (elapsed %s)", p.PodsReady, p.InitialPodCount, FormatElapsed(p.Elapsed))
	} else {
		v.Reporter.Detail("gateway reconciling (elapsed %s)", FormatElapsed(p.Elapsed))
	}
}

// VerifyHotReloadCapability proves the gateway really does apply config
// revisions, before any traffic-affecting change is made.
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
// (Service.PatchGatewayConfigID) rather than re-applying the live spec under
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
func (v *TransitionVerifier) VerifyHotReloadCapability(ctx context.Context, namespace, crName string, port int) error {
	if !v.Capability.InjectsConfigID() {
		return nil
	}

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
func (v *TransitionVerifier) ResolveCapability(ctx context.Context, namespace, crName string, port int, fencedCRYAML, switchedCRYAML []byte) (Capability, error) {
	capability, err := v.Service.DetectCapability(ctx, namespace, crName, port, fencedCRYAML, switchedCRYAML)
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
	return capability, nil
}
