//go:build e2e

package migplan_reversible_e2e

import (
	"strings"
	"testing"

	"github.com/confluentinc/kcp/internal/manifest"
	"github.com/confluentinc/kcp/internal/services/migplan"
	"github.com/confluentinc/kcp/internal/services/migplan/reconcile"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const groupFilterCheck = "consumer offset group filter is the single * LOCAL_MIRROR entry on both copies"

func requirePlan(t *testing.T, res *migplan.Result) *reconcile.Reversal {
	t.Helper()
	require.False(t, res.Refused, "refused: %v", res.Reasons)
	require.Equal(t, reconcile.ModeReversibleStatic, res.Mode)
	require.NotNil(t, res.Reversal)
	return res.Reversal
}

func requireRefusedWith(t *testing.T, res *migplan.Result, want string) {
	t.Helper()
	require.True(t, res.Refused, "want a refusal mentioning %q", want)
	assert.Contains(t, strings.Join(res.Reasons, "\n"), want)
}

func writableOn(domain string) func(*manifest.GatewayMigration) {
	return func(g *manifest.GatewayMigration) { g.Spec.ReversibleStaticMigration.WritableOn = domain }
}

// The link as setup.sh leaves it, never reversed, with the route on cp: a flip
// toward cc reverses both topics on the target copy. The group filter cp-server
// returns must pass the semantic comparison.
func TestReversiblePlanTowardTargetLive(t *testing.T) {
	res, _ := reconcileWith(t, loadManifest(t, nil), "testdata/gateway-bound-cp.yaml")
	rev := requirePlan(t, res)
	assert.Equal(t, reconcile.SideTarget, rev.ReverseOn)
	assert.Equal(t, []string{"rev.a", "rev.b"}, rev.Reverse)
	assert.Contains(t, rev.InScopeGroups, "g-rev")
	for _, p := range res.Report.Preconditions {
		if p.Name == groupFilterCheck {
			assert.True(t, p.OK, "the * filter as cp-server returns it: %s", p.Detail)
		}
	}
}

// A flip toward cc stopped after reversing rev.a, fenced, with the route still
// on cp; writableOn set back to cp backs it out: rev.a reverses back on the
// source copy and rev.b only switches (the fence comes off).
func TestReversibleBackOutLive(t *testing.T) {
	reverseAndSettle(t, false, "rev.a")
	res, out := reconcileWith(t, loadManifest(t, writableOn("cp")), "testdata/gateway-bound-cp-fenced.yaml")
	rev := requirePlan(t, res)
	assert.True(t, rev.Sides.BackOut)
	assert.Equal(t, reconcile.SideSource, rev.ReverseOn)
	assert.Equal(t, []string{"rev.a"}, rev.Reverse)
	assert.Equal(t, []string{"rev.b"}, rev.SwitchOnly)
	assert.Contains(t, out, "Back-out: the route stays on cp")
}

// Both topics already writable on cc with the route there: a flip toward cp
// reverses both on the source copy.
func TestReversiblePlanTowardSourceLive(t *testing.T) {
	reverseAndSettle(t, false, "rev.a", "rev.b")
	res, _ := reconcileWith(t, loadManifest(t, writableOn("cp")), "testdata/gateway-bound-cc.yaml")
	rev := requirePlan(t, res)
	assert.Equal(t, reconcile.SideSource, rev.ReverseOn)
	assert.Equal(t, []string{"rev.a", "rev.b"}, rev.Reverse)
}

func TestReversibleRefusesSwappedDomainsLive(t *testing.T) {
	g := loadManifest(t, func(g *manifest.GatewayMigration) {
		r := &g.Spec.ReversibleStaticMigration.Route
		r.SourceStreamingDomain, r.TargetStreamingDomain = "cc", "cp"
	})
	res, _ := reconcileWith(t, g, "testdata/gateway-bound-cp.yaml")
	requireRefusedWith(t, res, "swapped")
}

func TestReversibleRefusesHalfCreatedLinkLive(t *testing.T) {
	g := loadManifest(t, func(g *manifest.GatewayMigration) { g.Spec.ClusterLink.Name = "rev-half" })
	res, _ := reconcileWith(t, g, "testdata/gateway-bound-cp.yaml")
	requireRefusedWith(t, res, "was never created on the source cluster")
}

func TestReversibleRefusesOffsetSyncOffLive(t *testing.T) {
	alterLinkConfig(t, sourceREST, sourceClusterID, "consumer.offset.sync.enable", "false")
	res, _ := reconcileWith(t, loadManifest(t, nil), "testdata/gateway-bound-cp.yaml")
	requireRefusedWith(t, res, "source copy")
}

func TestReversibleRefusesNonStarFilterLive(t *testing.T) {
	alterLinkConfig(t, sourceREST, sourceClusterID, "consumer.offset.group.filters",
		`{"groupFilters":[{"name":"g-","patternType":"PREFIXED","filterType":"INCLUDE","topicTypes":["LOCAL_MIRROR"]}]}`)
	res, _ := reconcileWith(t, loadManifest(t, nil), "testdata/gateway-bound-cp.yaml")
	requireRefusedWith(t, res, "only the * filter")
}

// g-rev has members directly on cc, the side a flip toward cc enters.
func TestReversibleRefusesActiveGroupOnEnteredSideLive(t *testing.T) {
	joinGroup(t, destBroker, "g-rev", "rev.a")
	res, _ := reconcileWith(t, loadManifest(t, nil), "testdata/gateway-bound-cp.yaml")
	requireRefusedWith(t, res, "g-rev")
}

// g-offlink commits on a source topic that isn't on the link; that topic
// doesn't follow the switch.
func TestReversibleRefusesOffLinkCommitsLive(t *testing.T) {
	commitOffLink(t, "rev.offlink", "g-offlink")
	res, _ := reconcileWith(t, loadManifest(t, nil), "testdata/gateway-bound-cp.yaml")
	requireRefusedWith(t, res, "g-offlink")
}
