package gateway

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"k8s.io/apimachinery/pkg/types"
)

// Target names the gateway a Transitioner drives and the route edits the run
// makes. It is plain data: the Transitioner derives from it whatever its
// verification needs. Capability detection inspects the fenced and switched
// CRs (built with RouteProbe, at most once per Transitioner) because applying
// either is what puts spec.hotReload into force: a detector shown only the
// live gateway could pick rollout verification for a run that will hot-reload,
// and then observe nothing.
type Target struct {
	Namespace, Name  string
	StartRouteFenced bool // the start-of-run route already carried kcp's fence

	GatewayCR     []byte      // the whole gateway CR the run started from
	Route         string      // the route the run fences and switches
	Fence, Switch RouteChange // the run's fence and switch edits to Route
}

// Transition is one route change a Transitioner applies and confirms. The
// Transitioner derives the RoutePatch from Change and the target's route, so an
// FSM names the edit and never the patch type.
type Transition struct {
	Step          string // labels errors, spans and reporter output
	Label         string // names the transition in a derivation failure: "failed to build <Label> route patch: ..."
	Change        RouteChange
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
	configPort       int // 0 or less selects DefaultGatewayConfigPort

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

// WithConfigPort sets the port of the per-pod GET /config endpoint that
// capability detection and hot-reload verification poll. 0 or less selects
// DefaultGatewayConfigPort.
func WithConfigPort(port int) TransitionerOption {
	return func(t *Transitioner) { t.configPort = port }
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

// TransitionerFactory builds a Transitioner for one FSM. The FSM supplies its
// own reporter provider (so a reporter it replaces later is honored); the
// client, timeouts and config port are fixed by whoever built the factory.
type TransitionerFactory func(reporter func() Reporter) *Transitioner

// NewTransitionerFactory returns a factory whose Transitioners run over svc with
// opts. A nil svc yields Transitioners whose HasClient reports false.
func NewTransitionerFactory(svc TransitionClient, opts ...TransitionerOption) TransitionerFactory {
	return func(reporter func() Reporter) *Transitioner {
		return NewTransitioner(svc, reporter, opts...)
	}
}

// HasClient reports whether the Transitioner was built over a gateway client. A
// nil receiver is not handled: callers guard a nil Transitioner themselves.
func (t *Transitioner) HasClient() bool { return t.svc != nil }

// SetExpectedGeneration seeds the Gateway CR generation patches are guarded on.
func (t *Transitioner) SetExpectedGeneration(g int64) { t.generation = g }

// ExpectedGeneration returns the generation the next patch is guarded on.
func (t *Transitioner) ExpectedGeneration() int64 { return t.generation }

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

// port normalizes the configured /config port: 0 or less selects the default.
func (t *Transitioner) port() int {
	if t.configPort <= 0 {
		return DefaultGatewayConfigPort
	}
	return t.configPort
}

// CheckGeneration confirms the Gateway CR's generation is still the one kcp
// expects. See TransitionVerifier.CheckGeneration.
func (t *Transitioner) CheckGeneration(ctx context.Context, target Target, step string) error {
	return t.verifier().CheckGeneration(ctx, target.Namespace, target.Name, step)
}

// CheckObservedGeneration compares a generation the caller already read (for
// example from facts it gathered) with the one patches are guarded on. It does
// no I/O, unlike CheckGeneration. It returns nil when the expected generation
// is unknown (0), when observed is not positive, or when they are equal;
// otherwise a *GenerationChangedError.
func (t *Transitioner) CheckObservedGeneration(target Target, observed int64) error {
	if observed <= 0 || t.generation <= 0 || observed == t.generation {
		return nil
	}
	return &GenerationChangedError{Namespace: target.Namespace, Gateway: target.Name, Expected: t.generation, Observed: observed}
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
	fenced, switched, err := RouteProbe(target.GatewayCR, target.Route, target.Fence, target.Switch)
	if err != nil {
		return err
	}
	capability, err := t.verifier().ResolveCapability(ctx, target.Namespace, target.Name, t.port(), fenced, switched)
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
	return t.verifier().SmokeTestHotReload(ctx, target.Namespace, target.Name, t.port())
}

// Apply derives tr's patch, applies it onto the target gateway and confirms it
// landed: patch derivation, capability, patch, operator acceptance, then per-pod
// or rollout verification.
func (t *Transitioner) Apply(ctx context.Context, target Target, tr Transition) TransitionResult {
	// Derive the patch first: a bad artifact must stop before capability
	// detection or any write.
	patch, err := tr.Change.Patch(target.Route)
	if err != nil {
		return TransitionResult{Err: fmt.Errorf("failed to build %s route patch: %w", tr.Label, err)}
	}
	if err := t.EnsureCapability(ctx, target); err != nil {
		return TransitionResult{Err: fmt.Errorf("failed to resolve gateway capability: %w", err)}
	}
	v := t.verifier()

	applied, err := v.PatchCR(ctx, target.Namespace, target.Name, patch, tr.Step)
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

	if err := v.VerifyTransition(ctx, target.Namespace, target.Name, t.port(), applied, tr.Step); err != nil {
		return TransitionResult{Landed: true, Err: err}
	}
	return TransitionResult{Landed: true}
}
