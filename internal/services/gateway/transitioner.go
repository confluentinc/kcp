package gateway

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"k8s.io/apimachinery/pkg/types"
)

// Target names the gateway a Transitioner drives and what it needs to resolve
// the gateway's capability.
type Target struct {
	Namespace, Name  string
	ConfigPort       int  // 0 or less selects DefaultGatewayConfigPort
	StartRouteFenced bool // the start-of-run route already carried kcp's fence
	// Probe returns the full fenced and switched Gateway CRs for capability detection.
	// Empty slices mean "this FSM never applies that CR". Called at most once per Transitioner.
	Probe func() (fenced, switched []byte, err error)
}

// Transition is one route patch a Transitioner applies and confirms.
type Transition struct {
	Step          string // labels errors, spans and reporter output
	Patch         RoutePatch
	RestoresStart bool   // true only for a patch that puts back the start-of-run route
	What          string // names the CR in a patch failure: "failed to apply <What>: ..."
	Applied       string // Reporter.Success message printed once the patch has landed; "" prints none
}

// TransitionResult reports whether a transition's patch reached the cluster,
// and the first error, if any.
type TransitionResult struct {
	Landed bool // the patch call returned nil or ErrApplyUnverified
	Err    error
}

// Transitioner applies a gateway route patch and confirms it landed in one
// call, on top of TransitionVerifier. It owns the per-process state every FSM
// needs around that: the capability (resolved once, smoke-tested at most once),
// the run's expected Gateway CR generation, and the start-of-run pod set an
// unfence must not wait on. It is not safe for concurrent use: one FSM run, one goroutine.
type Transitioner struct {
	svc      TransitionClient
	reporter func() Reporter

	rolloutTimeout   time.Duration
	hotReloadTimeout time.Duration

	capability     Capability
	resolved       bool
	smokeAttempted bool

	// firstApplyDone: the start-of-run pod set is captured only on the first
	// Apply of the process, even if PatchCR failed before capturing; a later
	// RestoresStart apply then waits on all pods, as the old code did when
	// capture failed.
	firstApplyDone bool
	startPods      map[types.UID]struct{}

	generation int64
}

// TransitionerOption configures a Transitioner at construction.
type TransitionerOption func(*Transitioner)

// WithTimeouts sets the rollout and hot-reload verification budgets.
func WithTimeouts(rollout, hotReload time.Duration) TransitionerOption {
	return func(t *Transitioner) {
		t.rolloutTimeout = rollout
		t.hotReloadTimeout = hotReload
	}
}

// NewTransitioner returns a Transitioner over svc. reporter is a provider, not
// a value, so a caller that replaces its reporter after construction is honored.
func NewTransitioner(svc TransitionClient, reporter func() Reporter, opts ...TransitionerOption) *Transitioner {
	t := &Transitioner{svc: svc, reporter: reporter}
	for _, opt := range opts {
		opt(t)
	}
	return t
}

// HasClient reports whether the Transitioner was built over a gateway client.
func (t *Transitioner) HasClient() bool { return t.svc != nil }

// SetExpectedGeneration seeds the Gateway CR generation patches are guarded on.
func (t *Transitioner) SetExpectedGeneration(g int64) { t.generation = g }

// ExpectedGeneration returns the generation the next patch is guarded on.
func (t *Transitioner) ExpectedGeneration() int64 { return t.generation }

// HotReloadTimeout returns the hot-reload verification budget in force: the one
// set, or DefaultHotReloadTimeout when it is unset or negative.
func (t *Transitioner) HotReloadTimeout() time.Duration {
	return t.verifier().HotReloadTimeoutOrDefault()
}

// Capability returns the resolved capability (the zero value until resolved).
func (t *Transitioner) Capability() Capability { return t.capability }

// verifier builds the shared apply/wait/verify mechanism for one call.
func (t *Transitioner) verifier() *TransitionVerifier {
	return &TransitionVerifier{
		Client:           t.svc,
		Reporter:         t.reporter(),
		Capability:       t.capability,
		RolloutTimeout:   t.rolloutTimeout,
		HotReloadTimeout: t.hotReloadTimeout,
		Generation:       &t.generation,
	}
}

// configPort normalizes the target's port: 0 or less selects the default.
func configPort(target Target) int {
	if target.ConfigPort <= 0 {
		return DefaultGatewayConfigPort
	}
	return target.ConfigPort
}

// CheckGeneration confirms the Gateway CR's generation is still the one kcp
// expects. See TransitionVerifier.CheckGeneration.
func (t *Transitioner) CheckGeneration(ctx context.Context, target Target, step string) error {
	return t.verifier().CheckGeneration(ctx, target.Namespace, target.Name, step)
}

// EnsureCapability resolves the capability once per process and then, at most
// once per process even if it failed, smoke-tests hot-reload.
func (t *Transitioner) EnsureCapability(ctx context.Context, target Target) error {
	if err := t.resolveCapability(ctx, target); err != nil {
		return err
	}
	if t.smokeAttempted {
		return nil
	}
	return t.SmokeTestHotReload(ctx, target)
}

// ResolveCapability resolves the gateway capability without the hot-reload
// smoke test, and marks the smoke test as attempted: a caller that resolves
// directly owns the smoke test (it runs SmokeTestHotReload itself, or skips it),
// as AAO's tests do, so a later Apply or EnsureCapability never runs it. The
// resolve itself is a no-op once resolved.
func (t *Transitioner) ResolveCapability(ctx context.Context, target Target) error {
	t.smokeAttempted = true
	return t.resolveCapability(ctx, target)
}

// resolveCapability resolves the capability once per process, leaving the
// smoke test to EnsureCapability.
func (t *Transitioner) resolveCapability(ctx context.Context, target Target) error {
	if t.resolved {
		return nil
	}
	var fenced, switched []byte
	if target.Probe != nil {
		var err error
		fenced, switched, err = target.Probe()
		if err != nil {
			return err
		}
	}
	capability, err := t.verifier().ResolveCapability(ctx, target.Namespace, target.Name, configPort(target), fenced, switched)
	if err != nil {
		return err
	}
	t.capability = capability
	if capability.Mode == VerifyPerPodConfigID {
		t.reporter().Success("Gateway transitions will be verified per pod via %s", GatewayConfigEndpointPath)
	}
	t.resolved = true
	return nil
}

// SmokeTestHotReload runs the hot-reload smoke test and records it as
// attempted, so EnsureCapability never runs it again. It writes a fresh
// spec.configId to the LIVE Gateway CR (see TransitionVerifier.SmokeTestHotReload).
func (t *Transitioner) SmokeTestHotReload(ctx context.Context, target Target) error {
	t.smokeAttempted = true
	return t.verifier().SmokeTestHotReload(ctx, target.Namespace, target.Name, configPort(target))
}

// Apply patches tr onto the target gateway and confirms it landed: capability,
// patch, operator acceptance, then per-pod or rollout verification.
func (t *Transitioner) Apply(ctx context.Context, target Target, tr Transition) TransitionResult {
	if err := t.EnsureCapability(ctx, target); err != nil {
		return TransitionResult{Err: fmt.Errorf("failed to resolve gateway capability: %w", err)}
	}
	v := t.verifier()

	applied, err := v.PatchCR(ctx, target.Namespace, target.Name, tr.Patch, tr.Step)
	if !t.firstApplyDone && !tr.RestoresStart && !target.StartRouteFenced && applied.PreApplyPodUIDs != nil {
		t.startPods = applied.PreApplyPodUIDs
	}
	t.firstApplyDone = true
	if err != nil {
		return TransitionResult{Landed: errors.Is(err, ErrApplyUnverified), Err: fmt.Errorf("failed to apply %s: %w", tr.What, err)}
	}

	if tr.Applied != "" {
		v.Reporter.Success("%s", tr.Applied)
	}
	slog.Debug("gateway transition applied", "step", tr.Step)

	if err := v.WaitForAccepted(ctx, target.Namespace, target.Name, tr.Step); err != nil {
		return TransitionResult{Landed: true, Err: err}
	}

	if tr.RestoresStart {
		before := len(applied.PreApplyPodUIDs)
		applied.PreApplyPodUIDs = ExcludePods(applied.PreApplyPodUIDs, t.startPods)
		slog.Debug("unfence skips this run's pre-fence pods", "excluded", before-len(applied.PreApplyPodUIDs))
	}

	if err := v.VerifyTransition(ctx, target.Namespace, target.Name, configPort(target), applied, tr.Step); err != nil {
		return TransitionResult{Landed: true, Err: err}
	}
	return TransitionResult{Landed: true}
}
