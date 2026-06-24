// Package reconcile implements the generic desired-state reconcile loop
// (feasibility §8.4): for each Reconciler, check preconditions, compute the
// plan (desired vs actual), and either preview it (dry-run) or apply it
// additively. It knows nothing about specific resources.
package reconcile

import "context"

// Action classifies one change in a plan.
type Action int

const (
	ActionCreate  Action = iota // missing on target → will be created
	ActionPresent               // present and matches → no-op
	ActionDrift                 // present but differs from desired → reported, never changed (§8.6)
)

func (a Action) String() string {
	switch a {
	case ActionCreate:
		return "create"
	case ActionPresent:
		return "present"
	case ActionDrift:
		return "drift"
	default:
		return "unknown"
	}
}

// Change is one item in a plan, in reporting form.
type Change struct {
	Action  Action
	Summary string // e.g. `cluster link "source-to-cc"`
	Detail  string // optional, e.g. a drift explanation
}

// Plan is the reporting view of a computed delta. The concrete plan a
// Reconciler returns may also carry the typed payload it needs in Apply.
type Plan interface {
	// Changes returns every change in the plan (Create, Present, and Drift),
	// for rendering in dry-run and apply output.
	Changes() []Change
	// Empty reports whether the plan would create nothing (no ActionCreate
	// items). Note this differs from len(Changes())==0: a plan that only
	// reports Present/Drift items is Empty() but still has changes to display.
	// The engine renders all changes regardless; Empty is for callers deciding
	// whether an apply would be a no-op.
	Empty() bool
}

// Outcome is what Apply actually did.
type Outcome struct {
	Created []Change
	Present []Change
	Drift   []Change
	Failed  []Change // attempted Create that errored (continue-on-error reconcilers)
}

// Report aggregates outcomes across reconcilers for one engine run.
type Report struct {
	DryRun   bool
	Outcomes map[string]Outcome // keyed by Reconciler.Name()
}

// Reconciler reconciles one resource section of the manifest.
type Reconciler interface {
	Name() string
	CheckPreconditions(ctx context.Context) error
	Plan(ctx context.Context) (Plan, error)
	Apply(ctx context.Context, p Plan) (Outcome, error)
}
