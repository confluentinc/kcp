//go:build e2e

package migration_tbm_e2e

import (
	"io"
	"testing"

	"github.com/confluentinc/kcp/internal/manifest"
	"github.com/confluentinc/kcp/internal/services/migplan"
	"github.com/stretchr/testify/require"
)

// assertRefusedNoArtifacts is the all-or-nothing shape every refusal must have:
// no fence, no switchover, no promote list.
func assertRefusedNoArtifacts(t *testing.T, res *migplan.Result) {
	t.Helper()
	require.True(t, res.Refused, "the engine must refuse this batch")
	require.Empty(t, res.FenceYAML, "a refused run must emit no fence artifact")
	require.Empty(t, res.SwitchoverYAML, "a refused run must emit no switchover artifact")
	require.Empty(t, res.PromoteTopics, "a refused run must promote no topics")
}

// decideRaw runs the engine against the live gateway without the harness's
// no-error guard, for halts whose misconfiguration reaches a live provider before
// the checks and surfaces as an I/O error rather than a refusal.
func decideRaw(h *tbmHarness, g *manifest.GatewayMigration) (*migplan.Result, error) {
	return migplan.Reconcile(h.ctx, g, migplan.WithOutput(io.Discard))
}

// TestPromotedNotSwitchedResumesAsSwitchOnly: a mirror promoted (STOPPED) while
// the route still sends it to the source is where a run killed between promote
// and switch leaves the world. It is a resume, not a halt: the engine classifies
// it SwitchOnly and emits artifacts that switch it without promoting it again.
//
// Uses reserved topic 045. Promotion is irreversible, so there is nothing to
// restore; isolation relies on 045 being reserved out of the success range.
// Decide only reconciles, so the route itself is not switched.
func TestPromotedNotSwitchedResumesAsSwitchOnly(t *testing.T) {
	h := newHarness(t)
	reserved := h.e.topicName(h.e.reservedTopic)
	h.PromoteMirrors(t, []string{reserved})

	g := h.loadManifest(t, "resume-promoted-not-switched.yaml")
	res := h.Decide(t, g)

	require.Falsef(t, res.Refused, "promoted-not-switched is a resume, not a refusal; reasons=%v", res.Reasons)
	require.Len(t, res.Report.SwitchOnly, 1)
	require.Equal(t, reserved, res.Report.SwitchOnly[0].Topic)
	require.Empty(t, res.Report.FailFast)
	require.Empty(t, res.PromoteTopics, "an already-STOPPED mirror must not be promoted again")
	require.Empty(t, res.AwaitStopped)
	require.Contains(t, res.SwitchoverYAML, reserved, "the switchover must route the promoted topic")
	require.Contains(t, res.SwitchoverYAML, g.Spec.Route.TargetStreamingDomain, "…to the target domain")
}

// TestHaltScenarios proves migplan.Reconcile refuses each known-bad condition with
// no artifacts and the correct reason. Topic-level inconsistencies land in
// res.Reasons (fail-fast, from reconcile/verdict.go); run-level and route-shape
// problems land as failed preconditions (reconcile/preconditions.go). Every
// sub-test is self-contained: order-sensitive ones set up and restore their own
// world state, and the route-shape checks run hermetically against a file fixture
// so the live migration route is never mutated.
func TestHaltScenarios(t *testing.T) {
	h := newHarness(t)

	// --- topic-level fail-fast halts (res.Reasons substring) ---

	t.Run("missing-source", func(t *testing.T) {
		res := h.Decide(t, h.loadManifest(t, "halt-missing-source.yaml"))
		assertRefusedNoArtifacts(t, res)
		require.Truef(t, reasonsContain(res, "not found on the source"), "reasons=%v", res.Reasons)
	})

	t.Run("unmirrored", func(t *testing.T) {
		res := h.Decide(t, h.loadManifest(t, "halt-unmirrored.yaml"))
		assertRefusedNoArtifacts(t, res)
		require.Truef(t, reasonsContain(res, "is not on the cluster link"), "reasons=%v", res.Reasons)
	})

	t.Run("exists-on-target", func(t *testing.T) {
		res := h.Decide(t, h.loadManifest(t, "halt-exists-on-target.yaml"))
		assertRefusedNoArtifacts(t, res)
		require.Truef(t, reasonsContain(res, "exists on target but is not a mirror of the source"), "reasons=%v", res.Reasons)
	})

	// --- run-level precondition halts (failed precondition) ---

	// Self-contained: enable offset sync on the live link on entry, restore it on
	// exit, so this does not depend on the success suite having disabled it.
	t.Run("offset-sync-enabled", func(t *testing.T) {
		h.setOffsetSync(t, true)
		t.Cleanup(func() { h.setOffsetSync(t, false) })

		res := h.Decide(t, h.loadManifest(t, "halt-offset-sync-enabled.yaml"))
		assertRefusedNoArtifacts(t, res)
		require.Truef(t, hasFailedPrecondition(res.Report, "consumer offset sync disabled on link"),
			"preconditions=%+v", res.Report.Preconditions)
	})

	// The manifest asks for the offset-sync pause, which a dynamic route has no
	// use for: its link runs with consumer offset sync off.
	t.Run("offset-sync-pause-requested", func(t *testing.T) {
		res := h.Decide(t, h.loadManifest(t, "halt-offset-sync-pause-requested.yaml"))
		assertRefusedNoArtifacts(t, res)
		require.Truef(t, hasFailedPrecondition(res.Report, "offset-sync pause not requested"),
			"preconditions=%+v", res.Report.Preconditions)
	})

	// An invalid anchored-RE2 topicPatterns is refused at manifest load
	// (validateTopicGroup) rather than crashing or escaping a Go regexp error.
	t.Run("bad-selector", func(t *testing.T) {
		_, err := manifest.LoadGatewayMigrationFile(h.e.manifestPath("halt-bad-selector.yaml"))
		require.Error(t, err, "an invalid topicPatterns must be refused at load, not crash")
		require.Contains(t, err.Error(), "topicPatterns")
	})

	t.Run("target-domain-not-bound", func(t *testing.T) {
		res := h.Decide(t, h.loadManifest(t, "halt-target-domain-not-bound.yaml"))
		assertRefusedNoArtifacts(t, res)
		require.Truef(t, hasFailedPrecondition(res.Report, "target domain is bound"),
			"preconditions=%+v", res.Report.Preconditions)
	})

	// route-not-found and target-cluster-mismatch each drive a live provider with a
	// value the engine does not yet normalise into a refusal: an absent route makes
	// the live gateway source error before preconditions run, and a wrong
	// clusterId can break the REST link read first. Accept either an error or the
	// named precondition — both prove the batch cannot proceed.
	t.Run("route-not-found", func(t *testing.T) {
		res, err := decideRaw(h, h.loadManifest(t, "halt-route-not-found.yaml"))
		if err != nil {
			require.ErrorContains(t, err, "route")
			return
		}
		assertRefusedNoArtifacts(t, res)
		require.Truef(t, hasFailedPrecondition(res.Report, "route exists"),
			"preconditions=%+v", res.Report.Preconditions)
	})

	t.Run("target-cluster-mismatch", func(t *testing.T) {
		res, err := decideRaw(h, h.loadManifest(t, "halt-target-cluster-mismatch.yaml"))
		if err != nil {
			return
		}
		assertRefusedNoArtifacts(t, res)
		require.Truef(t, hasFailedPrecondition(res.Report, "target cluster matches the manifest"),
			"preconditions=%+v", res.Report.Preconditions)
	})

	// --- route-shape halts (hermetic: file gateway source, live migration route
	// untouched) ---

	probe := h.e.manifestPath("gateway-static-probe.yaml")

	// static-route (gateway-static-probe.yaml) has no pre-staged
	// security.cluster.<domain> block, so the static-route strategy
	// (reconcile/staticpreconditions.go) refuses on missing staged auth before
	// the dynamic-only "route is dynamic" check is ever reached — see
	// route-mode-unrecognized below for that check.
	t.Run("route-missing-staged-auth", func(t *testing.T) {
		res := h.DecideHermetic(t, h.loadManifest(t, "halt-route-missing-staged-auth.yaml"), probe)
		assertRefusedNoArtifacts(t, res)
		require.Truef(t, hasFailedPrecondition(res.Report, "route carries pre-staged auth for the target domain"),
			"preconditions=%+v", res.Report.Preconditions)
	})

	// A route mode that is neither "static" nor "dynamic" falls through
	// Reconcile's static-only dispatch (reconcile/reconcile.go) into the
	// dynamic strategy, whose "route is dynamic" precondition fails on the
	// literal unrecognized value.
	t.Run("route-mode-unrecognized", func(t *testing.T) {
		res := h.DecideHermetic(t, h.loadManifest(t, "halt-route-mode-unrecognized.yaml"), probe)
		assertRefusedNoArtifacts(t, res)
		require.Truef(t, hasFailedPrecondition(res.Report, "route is dynamic"),
			"preconditions=%+v", res.Report.Preconditions)
	})

	t.Run("route-binds-two-domains", func(t *testing.T) {
		res := h.DecideHermetic(t, h.loadManifest(t, "halt-route-binds-two-domains.yaml"), probe)
		assertRefusedNoArtifacts(t, res)
		require.Truef(t, hasFailedPrecondition(res.Report, "route binds exactly two domains"),
			"preconditions=%+v", res.Report.Preconditions)
	})
}
