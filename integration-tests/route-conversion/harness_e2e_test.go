//go:build e2e

package routeconversion

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/goccy/go-yaml"
	"github.com/stretchr/testify/require"
)

// Lines kcp prints that the tests assert on (cmd/migration/execute,
// internal/services/migration/d2s, internal/services/migplan).
const (
	killPointLine      = "kill-point"
	completedLine      = "✅ Route conversion completed: %s"
	nothingToDoSuffix  = " — nothing to do: the route is already static on its target domain"
	watchingLine       = "Watching committed offsets for %s"
	fenceAppliedLine   = "Fenced gateway CR applied"
	idleGroupWarning   = "exist on the destination with committed offsets but no members"
	gatewayUnfenced    = "Gateway unfenced — traffic restored to pre-fence state"
	rogueCommitsError  = "committed offsets changed after the fence"
	rogueCommitsReason = "Direct commits detected — removing fence"
)

// reset puts the environment back in the state setup.sh leaves, before every
// test: every link mirror promoted (one an earlier test added included), the
// route in its post-TBM shape over every link topic with a fresh configId
// hot-reloaded on every pod, and no consumer group on either cluster. The
// mirrors are promoted first so the route can list every link topic.
func (e *env) reset(t *testing.T, ctx context.Context) {
	t.Helper()
	e.promoteAll(t, ctx)
	e.replaceRoute(t, ctx, PostTBMRoute(e.liveRoute(t, ctx), e.domains, e.linkTopics(t, ctx)))
	e.requirePostTBM(t, ctx, "after the reset")
	e.deleteAllGroups(t, sourceCluster)
	e.deleteAllGroups(t, destCluster)
	t.Logf("♻️  RESET ▸ link fully promoted, route %q post-TBM over %v, no consumer groups", e.route, e.linkTopics(t, ctx))
}

// requireCompleted asserts a run converted the route in this run (not a
// nothing-to-do re-run).
func requireCompleted(t *testing.T, out string, err error, id string) {
	t.Helper()
	require.NoError(t, err, "the conversion must complete")
	require.Contains(t, out, fmt.Sprintf(completedLine, id))
	require.NotContains(t, out, nothingToDoSuffix, "a run with work left must not report nothing to do")
}

// requireNothingToDo asserts a run found the route already converted.
func requireNothingToDo(t *testing.T, out string, err error, id string) {
	t.Helper()
	require.NoError(t, err, "a re-run of a finished conversion must succeed")
	require.Contains(t, out, fmt.Sprintf(completedLine, id)+nothingToDoSuffix)
}

// requireInterrupted asserts run 1 stopped at its kill point.
func requireInterrupted(t *testing.T, out string, err error) {
	t.Helper()
	require.Error(t, err, "the interrupted run must exit non-zero")
	require.Contains(t, out, killPointLine, "the non-zero exit must be the kill point firing, not a real failure")
}

// snapshot saves <report>.md in the test's folder and logs the same picture:
// the route as the live CR holds it, every mirror's state, and the seeds'
// offsets on the source and on the destination.
func (e *env) snapshot(t *testing.T, ctx context.Context, report, label string, seeds []seed) {
	t.Helper()
	route := e.liveRoute(t, ctx)
	routeYAML, err := yaml.Marshal(route)
	require.NoError(t, err)
	states := e.mirrorStates(t, ctx)
	names := make([]string, 0, len(states))
	for n := range states {
		names = append(names, n)
	}
	sort.Strings(names)

	var md strings.Builder
	fmt.Fprintf(&md, "# %s\n\n`%s` · captured %s\n\n", label, t.Name(), time.Now().UTC().Format(time.RFC3339))
	fmt.Fprintf(&md, "## Gateway route `%s`\n\n```yaml\n%s\n```\n\n", e.route, strings.TrimRight(string(routeYAML), "\n"))
	md.WriteString("## Mirror topics\n\n| Topic | Status |\n|---|---|\n")
	for _, n := range names {
		fmt.Fprintf(&md, "| `%s` | %s |\n", n, states[n])
	}
	md.WriteString("\n## Committed offsets\n\n| Group | Topic | Partition | Source | Destination (metadata, leader epoch) |\n|---|---|---|---|---|\n")
	for _, s := range seeds {
		src := e.sourceOffsets(t, s.group)
		dst := e.destOffsets(t, s.group)
		for topic, parts := range src {
			for p, c := range parts {
				d, ok := dst[topic][p]
				dv := "—"
				if ok {
					dv = fmt.Sprintf("%d (%q, %d)", d.Offset, d.Metadata, d.LeaderEpoch)
				}
				fmt.Fprintf(&md, "| `%s` | `%s` | %d | %d (%q) | %s |\n", s.group, topic, p, c.Offset, c.Metadata, dv)
			}
		}
	}
	t.Logf("\n╔══ WORLD STATE ▸ %s\n%s", label, md.String())
	e.saveReport(t, report+".md", md.String())
}
