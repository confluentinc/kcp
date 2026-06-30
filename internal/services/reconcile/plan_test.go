package reconcile

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func mkPlan(actions ...Action) StepPlan[string] {
	steps := make([]Step[string], len(actions))
	for i, a := range actions {
		steps[i] = Step[string]{Change: Change{Action: a, Summary: a.String()}, Payload: a.String()}
	}
	return StepPlan[string]{Steps: steps}
}

func TestStepPlan_Changes(t *testing.T) {
	p := mkPlan(ActionCreate, ActionPresent, ActionDrift)
	got := p.Changes()
	require.Len(t, got, 3)
	require.Equal(t, ActionCreate, got[0].Action)
	require.Equal(t, ActionPresent, got[1].Action)
	require.Equal(t, ActionDrift, got[2].Action)
}

func TestStepPlan_Empty(t *testing.T) {
	require.True(t, mkPlan(ActionPresent, ActionDrift).Empty(), "no create → Empty")
	require.False(t, mkPlan(ActionPresent, ActionCreate).Empty(), "a create → not Empty")
	require.True(t, mkPlan().Empty(), "no steps → Empty")
}

func TestApplyContinueOnError_AllOutcomes(t *testing.T) {
	p := mkPlan(ActionCreate, ActionPresent, ActionDrift)
	var created []string
	out, err := ApplyContinueOnError(context.Background(), p, "thing(s)", func(_ context.Context, payload string) error {
		created = append(created, payload)
		return nil
	})
	require.NoError(t, err)
	require.Equal(t, []string{"create"}, created, "create fn called only for ActionCreate")
	require.Len(t, out.Created, 1)
	require.Len(t, out.Present, 1)
	require.Len(t, out.Drift, 1)
	require.Empty(t, out.Failed)
}

func TestApplyContinueOnError_ContinuesPastFailures(t *testing.T) {
	// Two creates: the first fails, the second succeeds. Apply must continue and
	// report "1 of 2 ... failed to create" with the partial Outcome.
	p := StepPlan[string]{Steps: []Step[string]{
		{Change: Change{Action: ActionCreate, Summary: "a"}, Payload: "boom"},
		{Change: Change{Action: ActionCreate, Summary: "b"}, Payload: "ok"},
		{Change: Change{Action: ActionPresent, Summary: "c"}},
	}}
	out, err := ApplyContinueOnError(context.Background(), p, "topic(s)", func(_ context.Context, payload string) error {
		if payload == "boom" {
			return errors.New("nope")
		}
		return nil
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "1 of 2 topic(s) failed to create")
	require.Len(t, out.Created, 1)
	require.Equal(t, "b", out.Created[0].Summary)
	require.Len(t, out.Failed, 1)
	require.Equal(t, "a", out.Failed[0].Summary)
	require.Contains(t, out.Failed[0].Detail, "nope")
	require.Len(t, out.Present, 1)
}

func TestApplyContinueOnError_WrongPlanType(t *testing.T) {
	// A Plan whose concrete type isn't StepPlan[string] must error, not panic.
	_, err := ApplyContinueOnError(context.Background(), StepPlan[int]{}, "x", func(_ context.Context, _ string) error { return nil })
	require.Error(t, err)
	require.Contains(t, err.Error(), "unexpected plan type")
}
