package gateway

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Real timestamps observed on EKS gateway-hotreload-test. The cluster-ready
// ApplyFailed condition below is genuinely 6 days and 13 generations stale on a
// healthy 3/3 gateway — it is the false-rejection hazard these tests exist for.
const (
	staleClusterReadyAt   = "2026-07-28T15:14:46Z"
	staleGarbageCollectAt = "2026-07-28T14:49:30Z"
	staleHotReloadAt      = "2026-07-31T12:20:15Z"
	// Test B: CFK published the canary crash here, 5.8s after the apply.
	crashedHotReloadAt = "2026-08-03T10:54:18Z"
)

func mustTime(t *testing.T, s string) metav1.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, s)
	require.NoError(t, err)
	return metav1.NewTime(parsed)
}

// gatewayObj builds the unstructured shape the dynamic client returns.
func gatewayObj(conds ...map[string]any) map[string]any {
	list := make([]any, 0, len(conds))
	for _, c := range conds {
		list = append(list, c)
	}
	return map[string]any{
		"apiVersion": "platform.confluent.io/v1beta1",
		"kind":       "Gateway",
		"metadata":   map[string]any{"name": "migration-gateway", "generation": int64(30)},
		"status":     map[string]any{"observedGeneration": int64(30), "conditions": list},
	}
}

func cond(condType, status, reason, lastTransition string) map[string]any {
	return map[string]any{
		"type":               condType,
		"status":             status,
		"reason":             reason,
		"message":            reason + " detail",
		"lastProbeTime":      lastTransition,
		"lastTransitionTime": lastTransition,
	}
}

// The exact condition set present on the cluster before an apply.
func healthyGatewayConditions() map[string]any {
	return gatewayObj(
		cond(ConditionGarbageCollecting, "False", "Garbage Collection not triggered", staleGarbageCollectAt),
		cond(ConditionHotReloadStatus, "True", "Succeeded", staleHotReloadAt),
		cond(ConditionClusterReady, "False", "ApplyFailed", staleClusterReadyAt),
	)
}

// --- gatewayConditions ------------------------------------------------------

func TestGatewayConditions_ParsesRealShape(t *testing.T) {
	conds, err := gatewayConditions(healthyGatewayConditions())
	require.NoError(t, err)
	require.Len(t, conds, 3)

	byType := map[string]gatewayCondition{}
	for _, c := range conds {
		byType[c.Type] = c
	}

	hr := byType[ConditionHotReloadStatus]
	assert.Equal(t, "True", hr.Status)
	assert.Equal(t, "Succeeded", hr.Reason)
	assert.Equal(t, mustTime(t, staleHotReloadAt), hr.LastTransitionTime)

	cr := byType[ConditionClusterReady]
	assert.Equal(t, "False", cr.Status)
	assert.Equal(t, "ApplyFailed", cr.Reason)
	assert.Contains(t, cr.Message, "ApplyFailed")
}

func TestGatewayConditions_AbsentStatusIsNotAnError(t *testing.T) {
	// A freshly created CR has no status yet.
	conds, err := gatewayConditions(map[string]any{"kind": "Gateway"})
	require.NoError(t, err)
	assert.Empty(t, conds)
}

func TestGatewayConditions_AbsentConditionsIsNotAnError(t *testing.T) {
	obj := map[string]any{"status": map[string]any{"observedGeneration": int64(1)}}

	conds, err := gatewayConditions(obj)
	require.NoError(t, err)
	assert.Empty(t, conds)
}

func TestGatewayConditions_SkipsMalformedEntries(t *testing.T) {
	obj := map[string]any{"status": map[string]any{"conditions": []any{
		cond(ConditionHotReloadStatus, "True", "Succeeded", staleHotReloadAt),
		"not-a-condition-object",
		map[string]any{"status": "True"}, // no type
	}}}

	conds, err := gatewayConditions(obj)
	require.NoError(t, err, "one bad entry must not blind us to the good ones")
	require.Len(t, conds, 1)
	assert.Equal(t, ConditionHotReloadStatus, conds[0].Type)
}

func TestGatewayConditions_UnparseableTimestampYieldsZeroTime(t *testing.T) {
	obj := map[string]any{"status": map[string]any{"conditions": []any{
		cond(ConditionHotReloadStatus, "False", "ContainerCrashed", "not-a-timestamp"),
	}}}

	conds, err := gatewayConditions(obj)
	require.NoError(t, err)
	require.Len(t, conds, 1)
	assert.True(t, conds[0].LastTransitionTime.IsZero())
}

// --- ConditionSnapshot.changed ----------------------------------------------

// condAt rebuilds one of healthyGatewayConditions' entries with an overridden
// timestamp, so a test can vary exactly one field of the fingerprint.
func condAt(t *testing.T, condType, status, reason, at string) gatewayCondition {
	t.Helper()
	return gatewayCondition{
		Type:               condType,
		Status:             status,
		Reason:             reason,
		Message:            reason + " detail", // matches cond()
		LastTransitionTime: mustTime(t, at),
	}
}

func TestConditionSnapshot_Changed(t *testing.T) {
	conds, err := gatewayConditions(healthyGatewayConditions())
	require.NoError(t, err)
	before := snapshotConditions(conds)

	t.Run("identical condition is not changed", func(t *testing.T) {
		assert.False(t, before.changed(condAt(t, ConditionClusterReady, "False", "ApplyFailed", staleClusterReadyAt)),
			"a condition still carrying its pre-apply values is stale, not new")
	})

	t.Run("advanced timestamp is changed", func(t *testing.T) {
		assert.True(t, before.changed(condAt(t, ConditionHotReloadStatus, "True", "Succeeded", crashedHotReloadAt)))
	})

	t.Run("earlier timestamp is changed", func(t *testing.T) {
		// Any difference counts. A timestamp moving backwards is not something a
		// real operator does, and inventing a rule for it would only add a way to
		// miss a genuine change.
		assert.True(t, before.changed(condAt(t, ConditionHotReloadStatus, "True", "Succeeded", staleGarbageCollectAt)))
	})

	t.Run("condition absent from snapshot counts as changed", func(t *testing.T) {
		assert.True(t, before.changed(condAt(t, "platform.confluent.io/brand-new", "False", "Whatever", crashedHotReloadAt)),
			"a condition type the operator only just started reporting is new information")
	})

	// The sticky-timestamp hole. Kubernetes only moves lastTransitionTime when a
	// condition's status flips, so a gateway already sitting at False/ApplyFailed
	// that refuses another apply reports the same status at the same instant — and
	// the E14 gateway held exactly that for 6 days. A time-only comparison goes
	// blind there; the reason and message are what still move.
	t.Run("same timestamp with a new reason is changed", func(t *testing.T) {
		assert.True(t, before.changed(condAt(t, ConditionClusterReady, "False", "ValidationFailed", staleClusterReadyAt)),
			"a fresh failure reason at an unchanged timestamp is still a fresh failure")
	})

	t.Run("same timestamp and reason with a new message is changed", func(t *testing.T) {
		c := condAt(t, ConditionClusterReady, "False", "ApplyFailed", staleClusterReadyAt)
		c.Message = "secretRef kcp-perf-plain-jaas not found"
		assert.True(t, before.changed(c),
			"the operator rewrites the message per failure, so a new one is a new report")
	})

	t.Run("zero timestamp against a known condition is changed", func(t *testing.T) {
		// An unparseable timestamp used to be silently ignored. It is a difference
		// from the baseline like any other, and the reason/message still carry the
		// verdict.
		assert.True(t, before.changed(condAt(t, ConditionClusterReady, "False", "ApplyFailed", "1970-01-01T00:00:00Z")))
	})
}

func TestSnapshotConditions_EmptyInput(t *testing.T) {
	s := snapshotConditions(nil)
	assert.Empty(t, s)

	// Everything is "new" against an empty snapshot, which is the safe default:
	// we would rather act on a failure than miss one.
	assert.True(t, s.changed(condAt(t, ConditionHotReloadStatus, "False", "ContainerCrashed", crashedHotReloadAt)))
}

// --- findNewRejection -------------------------------------------------------

// The load-bearing test: the 6-day-stale ApplyFailed must NOT be reported as a
// rejection of the apply we just made.
func TestFindNewRejection_IgnoresStaleApplyFailed(t *testing.T) {
	conds, err := gatewayConditions(healthyGatewayConditions())
	require.NoError(t, err)
	before := snapshotConditions(conds)

	// Nothing changed since the snapshot — the same conditions are re-read.
	rejection := findNewRejection(conds, before)

	assert.Nil(t, rejection,
		"a condition that has not transitioned since the apply carries no information about it")
}

// Test B reproduced: the canary crashed and CFK flipped hot-reload-status to
// False/ContainerCrashed 5.8s after the apply, while cluster-ready stayed stale.
func TestFindNewRejection_DetectsCanaryCrash(t *testing.T) {
	beforeConds, err := gatewayConditions(healthyGatewayConditions())
	require.NoError(t, err)
	before := snapshotConditions(beforeConds)

	afterConds, err := gatewayConditions(gatewayObj(
		cond(ConditionGarbageCollecting, "False", "Garbage Collection not triggered", staleGarbageCollectAt),
		cond(ConditionHotReloadStatus, "False", "ContainerCrashed", crashedHotReloadAt),
		cond(ConditionClusterReady, "False", "ApplyFailed", staleClusterReadyAt), // still stale
	))
	require.NoError(t, err)

	rejection := findNewRejection(afterConds, before)

	require.NotNil(t, rejection, "a newly-transitioned hot-reload failure must be reported")
	assert.Equal(t, ConditionHotReloadStatus, rejection.ConditionType)
	assert.Equal(t, "ContainerCrashed", rejection.Reason)
	assert.Contains(t, rejection.Error(), "ContainerCrashed")
	assert.Contains(t, rejection.Error(), "ContainerCrashed detail", "message carries the canary stack trace")
}

// Test A reproduced: the in-flight and success states must never fast-fail.
func TestFindNewRejection_IgnoresInProgressAndSuccess(t *testing.T) {
	beforeConds, err := gatewayConditions(healthyGatewayConditions())
	require.NoError(t, err)
	before := snapshotConditions(beforeConds)

	for _, tc := range []struct{ name, status, reason string }{
		{"in progress", "Unknown", "InProgress"},
		{"succeeded", "True", "Succeeded"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			after, err := gatewayConditions(gatewayObj(
				cond(ConditionHotReloadStatus, tc.status, tc.reason, crashedHotReloadAt),
			))
			require.NoError(t, err)

			assert.Nil(t, findNewRejection(after, before),
				"status %q must not be treated as a rejection", tc.status)
		})
	}
}

// garbage-collecting sits at False/"Garbage Collection not triggered" on a
// perfectly healthy gateway. Treating any False condition as a failure would
// fail every single apply.
func TestFindNewRejection_IgnoresGarbageCollectingEvenWhenItTransitions(t *testing.T) {
	before := snapshotConditions(nil)

	after, err := gatewayConditions(gatewayObj(
		cond(ConditionGarbageCollecting, "False", "Garbage Collection not triggered", crashedHotReloadAt),
	))
	require.NoError(t, err)

	assert.Nil(t, findNewRejection(after, before),
		"garbage-collecting is not a rejection signal at any status")
}

func TestFindNewRejection_DetectsNewlyTransitionedApplyFailed(t *testing.T) {
	before := snapshotConditions([]gatewayCondition{{
		Type:               ConditionClusterReady,
		Status:             "False",
		Reason:             "ApplyFailed",
		LastTransitionTime: mustTime(t, staleClusterReadyAt),
	}})

	// Same reason, but it transitioned again after our apply — that is real.
	after, err := gatewayConditions(gatewayObj(
		cond(ConditionClusterReady, "False", "ApplyFailed", crashedHotReloadAt),
	))
	require.NoError(t, err)

	rejection := findNewRejection(after, before)
	require.NotNil(t, rejection)
	assert.Equal(t, ConditionClusterReady, rejection.ConditionType)
}

// TestFindNewRejection_HonoursFailedNamingConvention covers refusals CFK reports
// with a reason outside the catalogue. Without the <Verb>Failed rule these
// produce no verdict at all, and the caller cannot tell that from an operator
// still working — so the refusal costs the whole wait instead of one poll.
func TestFindNewRejection_HonoursFailedNamingConvention(t *testing.T) {
	for _, reason := range []string{"ValidationFailed", "CreateFailed", "UpdateFailed"} {
		t.Run(reason, func(t *testing.T) {
			before := snapshotConditions([]gatewayCondition{{
				Type:               ConditionClusterReady,
				Status:             "True",
				Reason:             "Reconciled",
				LastTransitionTime: mustTime(t, staleClusterReadyAt),
			}})

			after, err := gatewayConditions(gatewayObj(
				cond(ConditionClusterReady, "False", reason, crashedHotReloadAt),
			))
			require.NoError(t, err)

			rejection := findNewRejection(after, before)
			require.NotNil(t, rejection, "a <Verb>Failed reason is a refusal")
			assert.Equal(t, reason, rejection.Reason)
		})
	}
}

// TestFindNewRejection_FailedNamingConventionStillNeedsToBeNew is what makes the
// naming rule safe to apply at all: widening which reasons are read must not
// widen what counts as evidence. A days-old ValidationFailed is as much history
// as a days-old ApplyFailed.
func TestFindNewRejection_FailedNamingConventionStillNeedsToBeNew(t *testing.T) {
	// Snapshot and re-read the same condition: the refusal was already there when
	// we applied, so nothing about it dates it to our apply.
	conds, err := gatewayConditions(gatewayObj(
		cond(ConditionClusterReady, "False", "ValidationFailed", staleClusterReadyAt),
	))
	require.NoError(t, err)

	assert.Nil(t, findNewRejection(conds, snapshotConditions(conds)),
		"unchanged since the baseline, so it says nothing about this apply")
}

// cluster-ready goes False transiently while the operator reconciles, so only
// the failure reasons should fast-fail.
func TestFindNewRejection_IgnoresClusterReadyFalseWithoutAFailureReason(t *testing.T) {
	before := snapshotConditions(nil)

	after, err := gatewayConditions(gatewayObj(
		cond(ConditionClusterReady, "False", "Reconciling", crashedHotReloadAt),
	))
	require.NoError(t, err)

	assert.Nil(t, findNewRejection(after, before),
		"a transient reconcile is not a rejection")
}

func TestFindNewRejection_PrefersHotReloadStatusWhenBothTransitioned(t *testing.T) {
	// Both conditions existed before the apply and both have since moved, so both
	// qualify as verdicts and precedence is what decides.
	beforeConds, err := gatewayConditions(healthyGatewayConditions())
	require.NoError(t, err)
	before := snapshotConditions(beforeConds)

	after, err := gatewayConditions(gatewayObj(
		cond(ConditionClusterReady, "False", "ApplyFailed", crashedHotReloadAt),
		cond(ConditionHotReloadStatus, "False", "ContainerCrashed", crashedHotReloadAt),
	))
	require.NoError(t, err)

	rejection := findNewRejection(after, before)
	require.NotNil(t, rejection)
	assert.Equal(t, ConditionHotReloadStatus, rejection.ConditionType,
		"the hot-reload condition is the direct signal and should be reported")
}

func TestFindNewRejection_NoConditionsAtAll(t *testing.T) {
	assert.Nil(t, findNewRejection(nil, snapshotConditions(nil)))
}

// TestFindNewRejection_EmptyBaselineNeverRejects pins the no-baseline guard.
// Changed() counts an unseen condition type as changed, so without this an
// absent baseline would make the 6-day-stale ApplyFailed in
// healthyGatewayConditions look like a brand-new rejection.
func TestFindNewRejection_EmptyBaselineNeverRejects(t *testing.T) {
	conds, err := gatewayConditions(healthyGatewayConditions())
	require.NoError(t, err)

	assert.Nil(t, findNewRejection(conds, nil),
		"no baseline means no way to date a condition, so nothing is actionable")
	assert.Nil(t, findNewRejection(conds, ConditionSnapshot{}),
		"an empty snapshot is the same as no snapshot")
}

// --- findStaleRejection -----------------------------------------------------

// findStaleRejection is the mirror of findNewRejection and is never a verdict —
// it exists so a wait that ran out can name the condition the operator was
// reporting throughout, which is the only diagnosis available when a repeat
// refusal is indistinguishable from the baseline.
func TestFindStaleRejection_ReturnsTheUnchangedFailure(t *testing.T) {
	conds, err := gatewayConditions(gatewayObj(
		cond(ConditionClusterReady, "False", "ApplyFailed", staleClusterReadyAt),
	))
	require.NoError(t, err)
	before := snapshotConditions(conds)

	stale := findStaleRejection(conds, before)
	require.NotNil(t, stale)
	assert.Equal(t, "ApplyFailed", stale.Reason)
	assert.Equal(t, ConditionClusterReady, stale.ConditionType)
}

func TestFindStaleRejection_IgnoresAFailureThatMoved(t *testing.T) {
	before := snapshotConditions([]gatewayCondition{{
		Type:               ConditionClusterReady,
		Status:             "False",
		Reason:             "ApplyFailed",
		LastTransitionTime: mustTime(t, staleClusterReadyAt),
	}})

	// Transitioned after the baseline: that is findNewRejection's business, and
	// reporting it here as well would double-count it.
	after, err := gatewayConditions(gatewayObj(
		cond(ConditionClusterReady, "False", "ApplyFailed", crashedHotReloadAt),
	))
	require.NoError(t, err)

	assert.Nil(t, findStaleRejection(after, before))
}

func TestFindStaleRejection_NoBaselineYieldsNothing(t *testing.T) {
	conds, err := gatewayConditions(healthyGatewayConditions())
	require.NoError(t, err)

	assert.Nil(t, findStaleRejection(conds, nil),
		"without a baseline there is nothing to call unchanged")
}

func TestFindStaleRejection_IgnoresHealthyConditions(t *testing.T) {
	conds, err := gatewayConditions(gatewayObj(
		cond(ConditionClusterReady, "True", "Reconciled", staleClusterReadyAt),
		cond(ConditionGarbageCollecting, "False", "Garbage Collection not triggered", staleGarbageCollectAt),
	))
	require.NoError(t, err)

	assert.Nil(t, findStaleRejection(conds, snapshotConditions(conds)),
		"garbage-collecting sits False on a healthy gateway and must never be reported")
}

// GatewayRejection is returned up through the wait as an error.
func TestGatewayRejection_IsAnError(t *testing.T) {
	var err error = &GatewayRejection{
		ConditionType: ConditionHotReloadStatus,
		Reason:        "ContainerCrashed",
		Message:       "canary exited 1: InvalidFormatException",
		At:            mustTime(t, crashedHotReloadAt),
	}

	require.Error(t, err)
	assert.Contains(t, err.Error(), "ContainerCrashed")
	assert.Contains(t, err.Error(), "InvalidFormatException")
}

// --- end-to-end: the false-success sequence from Test B ---------------------

// Walks the measured Test B timeline and asserts the fast-fail fires exactly
// once the crash is published, and never before.
func TestFindNewRejection_TestBTimeline(t *testing.T) {
	beforeConds, err := gatewayConditions(healthyGatewayConditions())
	require.NoError(t, err)
	before := snapshotConditions(beforeConds)

	// t+0: apply. Conditions unchanged — observedGeneration will catch up at
	// +1.2s here, which is exactly the false success we must not report.
	assert.Nil(t, findNewRejection(beforeConds, before), "t+0 must not fast-fail")

	// t+1.2s: obs==gen, hot-reload flipped to InProgress.
	inProgress, err := gatewayConditions(gatewayObj(
		cond(ConditionGarbageCollecting, "False", "Garbage Collection not triggered", staleGarbageCollectAt),
		cond(ConditionHotReloadStatus, "Unknown", "InProgress", "2026-08-03T10:54:13Z"),
		cond(ConditionClusterReady, "False", "ApplyFailed", staleClusterReadyAt),
	))
	require.NoError(t, err)
	assert.Nil(t, findNewRejection(inProgress, before), "InProgress must not fast-fail")

	// t+5.8s: CFK publishes the crash.
	crashed, err := gatewayConditions(gatewayObj(
		cond(ConditionGarbageCollecting, "False", "Garbage Collection not triggered", staleGarbageCollectAt),
		cond(ConditionHotReloadStatus, "False", "ContainerCrashed", crashedHotReloadAt),
		cond(ConditionClusterReady, "False", "ApplyFailed", staleClusterReadyAt),
	))
	require.NoError(t, err)

	rejection := findNewRejection(crashed, before)
	require.NotNil(t, rejection, "the crash must be caught rather than waiting out the full timeout")
	assert.Equal(t, "ContainerCrashed", rejection.Reason)
}
