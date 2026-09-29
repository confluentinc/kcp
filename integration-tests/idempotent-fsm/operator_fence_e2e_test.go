//go:build e2e

package idempotent_fsm_e2e

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/confluentinc/kcp/internal/services/gateway"
	"github.com/confluentinc/kcp/internal/services/migration/killpoint"
	"github.com/stretchr/testify/require"
)

// operatorFenceOn is an operator's own fence over exactly topics that blocks
// produce only. It has the batch's topics and blocked: true, like kcp's fence,
// plus a trafficType kcp never writes, so kcp must leave it in place.
func operatorFenceOn(topics []string) map[string]any {
	listed := make([]any, len(topics))
	for i, tp := range topics {
		listed[i] = tp
	}
	return map[string]any{"trafficType": "PRODUCE", "topics": listed, "blocked": true}
}

// addOperatorFence appends entry to the live dynamic route's rules.fencing, as
// an operator editing the shared route would, and removes it again when the
// test ends.
func (e *env) addOperatorFence(t *testing.T, ctx context.Context, entry map[string]any) {
	t.Helper()
	e.patchFencing(t, ctx, func(fencing []any) []any { return append(fencing, entry) })
	t.Logf("\n✍️  OPERATOR FENCE ADDED ▸ %v", entry)
	t.Cleanup(func() {
		e.patchFencing(t, context.Background(), func(fencing []any) []any {
			kept := make([]any, 0, len(fencing))
			for _, fe := range fencing {
				if !reflect.DeepEqual(fe, entry) {
					kept = append(kept, fe)
				}
			}
			return kept
		})
		t.Logf("\n🧹 OPERATOR FENCE REMOVED ▸ %v", entry)
	})
}

// patchFencing rewrites the live dynamic route's rules.fencing with edit and
// waits for the operator to accept the change.
func (e *env) patchFencing(t *testing.T, ctx context.Context, edit func([]any) []any) {
	t.Helper()
	route := e.routeObj(t, e.readCR(t, ctx))
	require.NotNilf(t, route, "route %q must exist in the live CR", e.route)
	rules, _ := route["rules"].(map[string]any)
	if rules == nil {
		rules = map[string]any{}
	}
	fencing, _ := rules["fencing"].([]any)
	rules["fencing"] = edit(fencing)

	_, err := e.svc.PatchGatewayRoute(ctx, e.namespace, e.gateway, gateway.RoutePatch{RouteName: e.route, Field: "rules", Value: rules}, "")
	require.NoError(t, err, "patch the route's rules.fencing")
	require.NoError(t, e.svc.WaitForGatewayAccepted(ctx, e.namespace, e.gateway, 2*time.Second, 2*time.Minute),
		"gateway must accept the rules.fencing change")
}

// An operator's produce-only fence over exactly the batch's topics, kept
// through a resumed migration. The first run is killed right after the fence:
// the route then carries kcp's fence beside the operator's. The resume finishes
// the migration: the switch removes kcp's fence and keeps the operator's.
// Slice tbm-topic-021..025.
func TestOperatorFence_SurvivesAResumedMigration(t *testing.T) {
	e := newEnv()
	if e.mode != "dynamic" {
		t.Skip("dynamic routes only: a static route has one route-level fence, not per-topic entries")
	}
	ctx := context.Background()
	topics := e.topicRange(21, 25)
	op := operatorFenceOn(topics)
	e.addOperatorFence(t, ctx, op)
	mani := e.writeManifest(t, "operator-fence-resume", topics)
	e.saveManifest(t, mani)
	e.snapshot(t, ctx, "before", "BEFORE (expect the operator's produce-only fence on the slice, mirrors ACTIVE, route → source)", topics)

	out1, err1 := e.runKCP(t, "kcp-run-1-interrupted.log", cpFenced, "migration", "execute", "--migration-yaml", mani)
	require.Error(t, err1, "the run interrupted after the fence must exit non-zero")
	require.Contains(t, out1, "kill-point", "the non-zero exit must be the kill point firing, not a real failure")
	e.snapshot(t, ctx, "after-interrupt", "AFTER interrupt at fenced (expect kcp's fence AND the operator's)", topics)
	r := e.routeObj(t, e.readCR(t, ctx))
	require.True(t, hasKcpFence(r, topics), "kcp's fence must be on the route — the interrupt came after the fence step")
	require.Equal(t, 1, countFencingEntry(r, op), "the fence step must keep the operator's entry, unchanged")

	out2, err2 := e.runKCP(t, "kcp-run-2-resume.log", "", "migration", "execute", "--migration-yaml", mani)
	require.NoError(t, err2, "the resume must complete the migration")
	require.Contains(t, strings.ToLower(out2), "migration complete", "the resume must report completion")

	e.snapshot(t, ctx, "after-resume", "AFTER the resume (expect switched, kcp's fence gone, the operator's fence kept)", topics)
	cr := e.readCR(t, ctx)
	r = e.routeObj(t, cr)
	ms := e.mirrorStatus(t, ctx)
	require.False(t, hasKcpFence(r, topics), "the switch must remove kcp's fence")
	require.Equal(t, 1, countFencingEntry(r, op), "the switch must keep the operator's entry, unchanged")
	for _, tp := range topics {
		require.Equalf(t, "STOPPED", ms[tp], "%s must be STOPPED after the migration completes", tp)
		require.Truef(t, e.isSwitchedToTarget(t, cr, tp), "%s must be switched to the target domain", tp)
	}
	t.Logf("\n✅ RESULT: the operator's fence on the batch's topics survived the fence, the resume and the switch.")
}

// An operator's produce-only fence over exactly the batch's topics, kept
// through a rollback. A fresh run fails at verify_fence with the fence up, so
// it rolls back: the rollback removes kcp's fence and keeps the operator's.
// Slice tbm-topic-026..030.
func TestOperatorFence_SurvivesARollback(t *testing.T) {
	e := newEnv()
	if e.mode != "dynamic" {
		t.Skip("dynamic routes only: a static route has one route-level fence, not per-topic entries")
	}
	ctx := context.Background()
	topics := e.topicRange(26, 30)
	op := operatorFenceOn(topics)
	e.addOperatorFence(t, ctx, op)
	mani := e.writeManifest(t, "operator-fence-rollback", topics)
	e.saveManifest(t, mani)
	e.snapshot(t, ctx, "before", "BEFORE (expect the operator's produce-only fence on the slice, mirrors ACTIVE, route → source)", topics)

	out, err := e.runKCPFailingAt(t, "kcp-run-1-fails.log", failStep, "migration", "execute", "--migration-yaml", mani)
	require.Error(t, err, "the failed step must fail the run")
	require.Contains(t, out, killpoint.FailEnvVar, "the failure must be the hook's")
	require.Contains(t, out, "removing fence to restore traffic", "nothing is promoted, so the run rolls back")

	e.snapshot(t, ctx, "after-rollback", "AFTER the rollback (expect kcp's fence gone, the operator's fence kept, mirrors ACTIVE)", topics)
	cr := e.readCR(t, ctx)
	r := e.routeObj(t, cr)
	ms := e.mirrorStatus(t, ctx)
	require.False(t, hasKcpFence(r, topics), "the rollback must remove kcp's fence")
	require.Equal(t, 1, countFencingEntry(r, op), "the rollback must keep the operator's entry, unchanged")
	for _, tp := range topics {
		require.Equalf(t, "ACTIVE", ms[tp], "%s must still be ACTIVE — nothing was promoted", tp)
		require.Falsef(t, e.isSwitchedToTarget(t, cr, tp), "%s must still route to the source after a rollback", tp)
	}
	t.Logf("\n✅ RESULT: the operator's fence on the batch's topics survived the rollback.")
}
