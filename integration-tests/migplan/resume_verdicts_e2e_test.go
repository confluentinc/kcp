//go:build e2e

package migplan_e2e

import (
	"context"
	"strings"
	"testing"

	"github.com/confluentinc/kcp/internal/services/migplan/reconcile"
)

// The resume verdicts exist so a migration interrupted mid-cutover can be
// re-run and complete from wherever it stopped. The happy-path test only
// exercises ACTIVE mirrors (all MIGRATABLE); these drive the two resume
// verdicts that a durable mirror state can seed at the engine tier:
//
//   SWITCH-ONLY  — mirror already promoted (STOPPED) but the route still points
//                  at source: the resume must switch it, NOT re-promote it.
//   UNCHANGED    — mirror STOPPED and the route already points at target: a
//                  finished topic the resume must treat as a no-op.
//
// Both use the durable STOPPED mirror `resume.switchonly` seeded by setup.sh;
// they differ only in the gateway fixture's routing (source vs target). The
// third resume verdict, AWAIT-STOPPED, needs a durable PENDING_STOPPED mirror —
// a transient the engine tier cannot seed reliably (a promote either completes
// to STOPPED or, on a broken link, fails to LINK_FAILED). Its Pending→AwaitStopped
// classification is covered by the reconcile unit tests, and proven live
// end-to-end by the idempotent-fsm suite, which holds a real PENDING_STOPPED by
// interrupting KCP mid-promote via the killpoint seam.

// TestReconcileSwitchOnlyLive: resume.switchonly is STOPPED on the link but the
// default gateway routes it to source (msk). The engine must classify it
// SWITCH-ONLY and emit artifacts that switch it WITHOUT promoting it again —
// so the promote set (Artifacts.PromoteTopics) is empty while the switchover routes it
// to the target.
func TestReconcileSwitchOnlyLive(t *testing.T) {
	eng := newLiveEngine(t) // default fixture: routing defaults to source (msk)
	in := reconcile.ReconcileInput{
		Topics:          []string{"resume.switchonly"},
		Route:           "migration-route",
		TargetDomain:    "cc",
		TargetClusterID: destClusterID,
	}

	plan, err := eng.Run(context.Background(), in)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if plan.Report.Refused() {
		t.Fatalf("switch-only is not a refusal; got preconditions=%+v failFast=%+v",
			plan.Report.Preconditions, plan.Report.FailFast)
	}

	assertOnlyVerdict(t, plan.Report, "SwitchOnly", "resume.switchonly")
	for _, tv := range plan.Report.SwitchOnly {
		t.Logf("switch-only: %s (S=%s M=%s T=%s R=%s)", tv.Topic, tv.S, tv.M, tv.T, tv.R)
	}

	if plan.Artifacts == nil {
		t.Fatal("switch-only is still to migrate (needs switching), so Artifacts must be non-nil")
	}
	// Already STOPPED ⇒ nothing to promote: the promote set must be empty.
	if len(plan.Artifacts.PromoteTopics) != 0 {
		t.Errorf("Artifacts.PromoteTopics (promote set) must be empty for a switch-only topic, got %v", plan.Artifacts.PromoteTopics)
	}
	if len(plan.Artifacts.AwaitStopped) != 0 {
		t.Errorf("Artifacts.AwaitStopped must be empty (nothing is PENDING), got %v", plan.Artifacts.AwaitStopped)
	}

	switchover := string(plan.Artifacts.SwitchoverRules)
	t.Logf("switchover-rules.yaml:\n%s", switchover)
	if !strings.Contains(switchover, "resume.switchonly") {
		t.Errorf("SwitchoverRules must route resume.switchonly:\n%s", switchover)
	}
	if !strings.Contains(switchover, "cc") {
		t.Errorf("SwitchoverRules must route resume.switchonly to the target domain cc:\n%s", switchover)
	}
	// The operator's pre-existing edits must survive in both artifacts.
	fence := string(plan.Artifacts.FenceRules)
	for _, must := range []string{"TRANSACTION", "ops-audit"} {
		if !strings.Contains(fence, must) {
			t.Errorf("FenceRules dropped the operator's entry %q:\n%s", must, fence)
		}
	}
	if !strings.Contains(switchover, "team-a.*") {
		t.Errorf("SwitchoverRules dropped the operator's routing condition (team-a.*):\n%s", switchover)
	}
}

// TestReconcileUnchangedLive: resume.switchonly is STOPPED AND the fixture's
// route already sends it to the target (cc). The engine must classify it
// UNCHANGED and plan zero work — nil Artifacts, no refusal — the clean no-op a
// re-run of a completed migration depends on.
func TestReconcileUnchangedLive(t *testing.T) {
	eng := newLiveEngineFor(t, "testdata/gateway-resume-unchanged.yaml", "migration-route", fakeSecretChecker{})
	in := reconcile.ReconcileInput{
		Topics:          []string{"resume.switchonly"},
		Route:           "migration-route",
		TargetDomain:    "cc",
		TargetClusterID: destClusterID,
	}

	plan, err := eng.Run(context.Background(), in)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if plan.Report.Refused() {
		t.Fatalf("an already-switched, stopped topic must not refuse; preconditions=%+v failFast=%+v",
			plan.Report.Preconditions, plan.Report.FailFast)
	}

	assertOnlyVerdict(t, plan.Report, "Unchanged", "resume.switchonly")
	for _, tv := range plan.Report.Unchanged {
		t.Logf("unchanged: %s (S=%s M=%s T=%s R=%s)", tv.Topic, tv.S, tv.M, tv.T, tv.R)
	}

	if plan.Artifacts != nil {
		t.Errorf("a fully-migrated (unchanged) topic must plan no work: Artifacts must be nil, got %+v", plan.Artifacts)
	}
}

// assertOnlyVerdict asserts the report classifies exactly the one topic into the
// named bucket and leaves every other bucket empty — so a test can't pass by
// accident when a topic lands in an unexpected verdict.
func assertOnlyVerdict(t *testing.T, r reconcile.Report, want, topic string) {
	t.Helper()
	buckets := map[string][]reconcile.TopicVerdict{
		"Migratable":   r.Migratable,
		"Unchanged":    r.Unchanged,
		"SwitchOnly":   r.SwitchOnly,
		"AwaitStopped": r.AwaitStopped,
		"FailFast":     r.FailFast,
	}
	for name, b := range buckets {
		switch {
		case name == want:
			if len(b) != 1 {
				t.Fatalf("expected exactly 1 %s topic, got %d: %+v", want, len(b), b)
			}
			if b[0].Topic != topic {
				t.Errorf("expected %s topic %q, got %q", want, topic, b[0].Topic)
			}
		case len(b) != 0:
			t.Errorf("expected no %s topics, got %d: %+v", name, len(b), b)
		}
	}
}
