package reconcile

import (
	"context"
	"fmt"
)

// Step pairs a Change (the reporting view) with the typed payload a reconciler
// needs to apply it. Payload is meaningful only for ActionCreate steps; Present
// and Drift steps carry the zero value.
type Step[T any] struct {
	Change  Change
	Payload T
}

// StepPlan is a generic Plan built from an ordered list of Steps. It provides
// the Changes() and Empty() implementations every step-based reconciler shares,
// written once here instead of duplicated per reconciler.
type StepPlan[T any] struct {
	Steps []Step[T]
}

func (p StepPlan[T]) Changes() []Change {
	out := make([]Change, len(p.Steps))
	for i, s := range p.Steps {
		out[i] = s.Change
	}
	return out
}

func (p StepPlan[T]) Empty() bool {
	for _, s := range p.Steps {
		if s.Change.Action == ActionCreate {
			return false
		}
	}
	return true
}

// ApplyContinueOnError applies a StepPlan[T] additively: it calls create for
// each ActionCreate step and CONTINUES past failures (each collected in
// Outcome.Failed). Present and Drift steps are recorded unchanged. If any create
// failed it returns a "<n> of <total> <noun> failed to create" error alongside
// the partial Outcome (total = number of create attempts).
//
// It is the shared driver for reconcilers whose creates are independent and
// best-effort (newTopics, mirrorTopics). Reconcilers needing fail-fast or
// per-step client routing (clusterLink) implement Apply themselves but still use
// StepPlan for Changes/Empty.
func ApplyContinueOnError[T any](ctx context.Context, p Plan, noun string, create func(context.Context, T) error) (Outcome, error) {
	sp, ok := p.(StepPlan[T])
	if !ok {
		return Outcome{}, fmt.Errorf("unexpected plan type %T", p)
	}
	var out Outcome
	var failed int
	for _, s := range sp.Steps {
		switch s.Change.Action {
		case ActionCreate:
			if err := create(ctx, s.Payload); err != nil {
				failed++
				out.Failed = append(out.Failed, Change{Action: ActionCreate, Summary: s.Change.Summary, Detail: err.Error()})
				continue
			}
			out.Created = append(out.Created, s.Change)
		case ActionPresent:
			out.Present = append(out.Present, s.Change)
		default: // ActionDrift — report only, never altered (§8.6)
			out.Drift = append(out.Drift, s.Change)
		}
	}
	if failed > 0 {
		return out, fmt.Errorf("%d of %d %s failed to create", failed, failed+len(out.Created), noun)
	}
	return out, nil
}
