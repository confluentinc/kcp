//go:build e2e

package routeconversion

import (
	"context"
	"fmt"
	"regexp"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestConversion_Baseline is the control for every other test: an
// uninterrupted conversion of the post-TBM route, with seeded groups. The dry
// run speaks in conversion terms, execute converts the route and copies every
// seeded offset (metadata included, leader epoch -1), and a re-run finds
// nothing to do and leaves the gateway CR untouched.
func TestConversion_Baseline(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.reset(t, ctx)
	seeds := e.seedGroups(t, e.defaultSeeds("base")...)
	security := e.liveRoute(t, ctx)["security"]
	linkTopics := e.linkTopics(t, ctx)
	mani, id := e.writeManifest(t, "baseline", minDetect)
	e.requireNoDestOffsets(t, seeds, "before the conversion")
	e.snapshot(t, ctx, "before", "BEFORE (expect the post-TBM route, every mirror STOPPED, no destination offsets)", seeds)

	out, err := e.dryRun(t, "kcp-run-1-dry-run.log", mani)
	require.NoError(t, err, "the dry run must produce a plan")
	require.Contains(t, out, fmt.Sprintf("Conversion plan · route %q → static on %s", e.route, e.domains.Dest))
	require.Contains(t, out, fmt.Sprintf("Link topics · %d", len(linkTopics)))
	for _, tp := range linkTopics {
		require.Regexpf(t, regexp.MustCompile(`(?m)^\s+=\s+`+regexp.QuoteMeta(tp)+`\s+promoted$`), out, "%s must read promoted", tp)
	}
	require.Regexp(t, regexp.MustCompile(`topic\(s\) [^\n]*`+regexp.QuoteMeta(e.orphanTopic)+`[^\n]* are not on the cluster link`), out,
		"the untracked-topic warning must name %s", e.orphanTopic)
	require.Contains(t, out, fmt.Sprintf("Plan: route to static, %d link topics  (plan ready)", len(linkTopics)))
	require.NotRegexp(t, regexp.MustCompile(`(?m)^Topics · \d+ requested$`), out, "a conversion selects no topics")
	require.NotRegexp(t, regexp.MustCompile(`\d+ to migrate`), out, "a conversion migrates no topics")

	out, err = e.execute(t, "kcp-run-2-execute.log", mani, nil, nil)
	requireCompleted(t, out, err, id)
	e.snapshot(t, ctx, "after", "AFTER execute (expect static on the destination, seeded offsets on the destination)", seeds)
	e.requireConvertedRoute(t, ctx, security)
	e.requireSynced(t, seeds)

	specBefore := e.readSpec(t, ctx)
	out, err = e.execute(t, "kcp-run-3-rerun.log", mani, nil, nil)
	requireNothingToDo(t, out, err, id)
	require.Equal(t, specBefore, e.readSpec(t, ctx), "a nothing-to-do run must not touch the gateway CR")
	t.Logf("\n✅ RESULT: the route converted to static, every seeded offset reached the destination, and a re-run found nothing to do.")
}
