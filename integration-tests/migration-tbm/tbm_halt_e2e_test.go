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
	require.Empty(t, res.Topics, "a refused run must promote no topics")
}

// decideRaw runs the engine against the live gateway without the harness's
// no-error guard, for halts whose misconfiguration reaches a live provider before
// the checks and surfaces as an I/O error rather than a refusal.
func decideRaw(h *tbmHarness, g *manifest.GatewayMigration) (*migplan.Result, error) {
	return migplan.Reconcile(h.ctx, g, migplan.WithOutput(io.Discard))
}

// TestHaltScenarios proves migplan.Reconcile refuses each known-bad condition with
// no artifacts and the correct reason (U6). Topic-level inconsistencies land in
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

	// Reserved topic 045: promote its mirror (STOPPED) without switching the route,
	// then Decide. Promotion is irreversible, so there is nothing to restore;
	// isolation relies on 045 being reserved out of the success range, never
	// selected by a success batch or the steady-state accounting.
	t.Run("promoted-not-switched", func(t *testing.T) {
		reserved := h.e.topicName(h.e.reservedTopic)
		h.PromoteMirrors(t, []string{reserved})

		res := h.Decide(t, h.loadManifest(t, "halt-promoted-not-switched.yaml"))
		assertRefusedNoArtifacts(t, res)
		require.Truef(t, reasonsContain(res, "is promoted but not switched over"), "reasons=%v", res.Reasons)
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

	t.Run("route-not-dynamic", func(t *testing.T) {
		res := h.DecideHermetic(t, h.loadManifest(t, "halt-route-not-dynamic.yaml"), probe)
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
