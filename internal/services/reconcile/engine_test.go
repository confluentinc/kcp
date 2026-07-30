package reconcile

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

// fakeReconciler lets each test script the engine's inputs.
type fakeReconciler struct {
	name        string
	precondErr  error
	plan        Plan
	planErr     error
	applyOut    Outcome
	applyErr    error
	applyCalled bool
}

func (f *fakeReconciler) Name() string                             { return f.name }
func (f *fakeReconciler) CheckPreconditions(context.Context) error { return f.precondErr }
func (f *fakeReconciler) Plan(context.Context) (Plan, error)       { return f.plan, f.planErr }
func (f *fakeReconciler) Apply(context.Context, Plan) (Outcome, error) {
	f.applyCalled = true
	return f.applyOut, f.applyErr
}

func simplePlan(c ...Change) Plan { return staticPlan(c) }

// staticPlan is a minimal Plan implementation for tests.
type staticPlan []Change

func (p staticPlan) Changes() []Change { return p }
func (p staticPlan) Empty() bool {
	for _, c := range p {
		if c.Action == ActionCreate {
			return false
		}
	}
	return true
}

func TestEngine_DryRun_DoesNotApply(t *testing.T) {
	f := &fakeReconciler{name: "clusterLink", plan: simplePlan(Change{Action: ActionCreate, Summary: `cluster link "x"`, Detail: "source s1"})}
	var out bytes.Buffer
	eng := NewEngine(&out)

	report, err := eng.Run(context.Background(), [][]Reconciler{{f}}, true)

	require.NoError(t, err)
	require.False(t, f.applyCalled, "dry-run must not call Apply")
	require.Contains(t, out.String(), `cluster link "x"`)
	require.Contains(t, out.String(), "— source s1")
	require.True(t, report.DryRun)
}

func TestEngine_Apply_CallsApplyAndReports(t *testing.T) {
	created := Change{Action: ActionCreate, Summary: `cluster link "x"`}
	f := &fakeReconciler{
		name:     "clusterLink",
		plan:     simplePlan(created),
		applyOut: Outcome{Created: []Change{created}},
	}
	var out bytes.Buffer
	report, err := NewEngine(&out).Run(context.Background(), [][]Reconciler{{f}}, false)

	require.NoError(t, err)
	require.True(t, f.applyCalled)
	require.Len(t, report.Outcomes["clusterLink"].Created, 1)
}

func TestEngine_PreconditionFailure_AbortsBeforePlan(t *testing.T) {
	f := &fakeReconciler{name: "clusterLink", precondErr: errors.New("target unreachable")}
	_, err := NewEngine(&bytes.Buffer{}).Run(context.Background(), [][]Reconciler{{f}}, false)

	require.Error(t, err)
	require.Contains(t, err.Error(), "target unreachable")
	require.False(t, f.applyCalled)
}

func TestEngine_PlanError_Propagates(t *testing.T) {
	f := &fakeReconciler{name: "clusterLink", planErr: errors.New("read failed")}
	_, err := NewEngine(&bytes.Buffer{}).Run(context.Background(), [][]Reconciler{{f}}, false)
	require.ErrorContains(t, err, "read failed")
}

func TestEngine_ApplyError_Propagates(t *testing.T) {
	f := &fakeReconciler{
		name:     "clusterLink",
		plan:     simplePlan(Change{Action: ActionCreate, Summary: "link"}),
		applyErr: errors.New("API 503"),
	}
	_, err := NewEngine(&bytes.Buffer{}).Run(context.Background(), [][]Reconciler{{f}}, false)
	require.ErrorContains(t, err, "API 503")
	require.ErrorContains(t, err, "clusterLink")
}

func TestEngine_Apply_RendersOutcomeEvenOnError(t *testing.T) {
	created := Change{Action: ActionCreate, Summary: "topic a"}
	f := &fakeReconciler{
		name:     "newTopics",
		plan:     simplePlan(created, Change{Action: ActionCreate, Summary: "topic b"}),
		applyOut: Outcome{Created: []Change{created}, Failed: []Change{{Action: ActionCreate, Summary: "topic b", Detail: "rejected"}}},
		applyErr: errors.New("1 of 2 topics failed"),
	}
	var out bytes.Buffer
	report, err := NewEngine(&out).Run(context.Background(), [][]Reconciler{{f}}, false)

	require.Error(t, err)
	require.Contains(t, err.Error(), "1 of 2 topics failed")
	// Outcome is still recorded and rendered despite the error:
	require.Len(t, report.Outcomes["newTopics"].Failed, 1)
	require.Contains(t, out.String(), "1 failed")
	require.Contains(t, out.String(), "topic b")
}

// A failure in one track must NOT skip an independent track: the second track's
// reconciler still applies, and the run returns the first track's error.
func TestEngine_IndependentTracks_OneFailsOthersRun(t *testing.T) {
	failing := &fakeReconciler{
		name:     "mirrorTopics",
		plan:     simplePlan(Change{Action: ActionCreate, Summary: "mirror x"}),
		applyErr: errors.New("92 of 154 mirror topic(s) failed to create"),
	}
	independent := &fakeReconciler{
		name: "acls",
		plan: simplePlan(Change{Action: ActionCreate, Summary: `acl for "sa-1"`}),
	}
	report, err := NewEngine(&bytes.Buffer{}).Run(context.Background(),
		[][]Reconciler{{failing}, {independent}}, false)

	require.True(t, failing.applyCalled)
	require.True(t, independent.applyCalled, "independent track must run despite the other track failing")
	require.Error(t, err)
	require.Contains(t, err.Error(), "92 of 154")
	require.Contains(t, report.Outcomes, "acls", "the independent track's outcome is recorded")
}

// Within a track, a failure fail-fasts: the earlier reconciler's failure skips
// its dependents (the later members of the same track).
func TestEngine_WithinTrack_FailureSkipsDependents(t *testing.T) {
	first := &fakeReconciler{
		name:     "serviceAccounts",
		plan:     simplePlan(Change{Action: ActionCreate, Summary: "sa x"}),
		applyErr: errors.New("service account create failed"),
	}
	dependent := &fakeReconciler{
		name: "acls",
		plan: simplePlan(Change{Action: ActionCreate, Summary: "acl x"}),
	}
	_, err := NewEngine(&bytes.Buffer{}).Run(context.Background(),
		[][]Reconciler{{first, dependent}}, false)

	require.True(t, first.applyCalled)
	require.False(t, dependent.applyCalled, "a dependent reconciler must be skipped when its predecessor in the track fails")
	require.Error(t, err)
	require.Contains(t, err.Error(), "service account create failed")
}

// When multiple independent tracks fail, the errors are joined so the command
// exits non-zero with all of them.
func TestEngine_MultipleTrackFailures_JoinedError(t *testing.T) {
	a := &fakeReconciler{name: "mirrorTopics", plan: simplePlan(Change{Action: ActionCreate, Summary: "x"}), applyErr: errors.New("mirror boom")}
	b := &fakeReconciler{name: "acls", plan: simplePlan(Change{Action: ActionCreate, Summary: "y"}), applyErr: errors.New("acl boom")}
	_, err := NewEngine(&bytes.Buffer{}).Run(context.Background(),
		[][]Reconciler{{a}, {b}}, false)

	require.Error(t, err)
	require.Contains(t, err.Error(), "mirror boom")
	require.Contains(t, err.Error(), "acl boom")
}

func TestAction_String(t *testing.T) {
	require.Equal(t, "create", ActionCreate.String())
	require.Equal(t, "present", ActionPresent.String())
	require.Equal(t, "drift", ActionDrift.String())
	require.Equal(t, "unknown", Action(99).String())
}
