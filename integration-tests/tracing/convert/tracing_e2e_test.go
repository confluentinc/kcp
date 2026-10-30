//go:build e2e

package tracing_convert_e2e

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// conversionRun is one dynamic-to-static migration the suite runs and the trace
// it must produce. The runs vary how many consumer groups have offsets to sync,
// the commit-detection window and the offset-sync worker count, so a pass gives
// several distinct, labelled runs to compare.
type conversionRun struct {
	label   string
	groups  int
	detect  time.Duration
	workers int // 0 = the default
}

var conversionRuns = []conversionRun{
	{label: "dynamic-to-static-2-groups-detect-10s", groups: 2, detect: 10 * time.Second},
	{label: "dynamic-to-static-6-groups-detect-15s", groups: 6, detect: 15 * time.Second},
	{label: "dynamic-to-static-12-groups-1-worker", groups: 12, detect: 10 * time.Second, workers: 1},
}

// detectionSlack is how much longer than its configured window the
// commit-detection wait may take.
const detectionSlack = time.Second

var conversionSteps = []string{"step.initialize", "step.fence", "step.verify_fence", "step.sync_offsets", "step.switch"}

func TestTracing_ConversionsEmitCompleteTraces(t *testing.T) {
	e := newEnv(t)
	for _, r := range conversionRuns {
		t.Run(r.label, func(t *testing.T) { e.runAndCheckTrace(t, r) })
	}
}

// seeds returns n consumer groups, each with a committed offset on partition 0
// of one link topic (cycling through the six), for the conversion to copy. The
// offsets stay at or below 10, the records setup.sh guarantees per partition.
func (e *env) seeds(prefix string, n int) []seed {
	out := make([]seed, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, seed{
			group:   fmt.Sprintf("%s-%02d", prefix, i+1),
			offsets: offsets{e.topic(i%6 + 1): {0: {Offset: int64(1 + i%10)}}},
		})
	}
	return out
}

func (e *env) runAndCheckTrace(t *testing.T, r conversionRun) {
	ctx := context.Background()
	e.reset(t, ctx) // route back to its post-dynamic-migration shape, no groups
	seeds := e.seedGroups(t, e.seeds(r.label, r.groups)...)
	security := e.liveRoute(t, ctx)["security"]
	mani, id := e.writeManifest(t, r.label, r.detect, r.workers)
	e.requireNoDestOffsets(t, seeds, "before the conversion")

	dir := e.testReportDir(t)
	if dir == "" {
		dir = t.TempDir()
	}
	traceFile := filepath.Join(dir, "kcp-traces.jsonl")

	e.snapshot(t, ctx, "before", "BEFORE (expect the post-dynamic route, every mirror STOPPED, no destination offsets)", seeds)
	out, err := e.execute(t, "kcp-execute.log", mani, traceFile, r.label)
	requireCompleted(t, out, err, id)
	e.snapshot(t, ctx, "after", "AFTER execute (expect static on the destination, seeded offsets on the destination)", seeds)
	e.requireConvertedRoute(t, ctx, security)
	e.requireSynced(t, seeds)

	spans := readTrace(t, traceFile)
	byName := map[string][]span{}
	traces := map[string]bool{}
	for _, s := range spans {
		byName[s.name] = append(byName[s.name], s)
		traces[s.traceID] = true
		for k, v := range s.attrs {
			for _, sd := range seeds {
				assert.NotEqualf(t, sd.group, v, "%s.%s carries a group name", s.name, k)
			}
		}
	}
	require.Len(t, traces, 1, "the whole run is one trace")

	root := byName["kcp migration cutover execute"]
	require.Len(t, root, 1)
	assert.Empty(t, strings.Trim(root[0].parentID, "0"), "the command span is the root")
	assert.Equal(t, r.label, root[0].resource["run"], "OTEL_RESOURCE_ATTRIBUTES reaches the trace")

	for _, name := range []string{"migplan.reconcile", "migration.build_services", "migration.run"} {
		require.Lenf(t, byName[name], 1, "one %s span", name)
		assert.Equalf(t, root[0].spanID, byName[name][0].parentID, "%s is a child of the command span", name)
	}
	run := byName["migration.run"][0]
	assert.Equal(t, "d2s", run.attrs["kcp.strategy"])
	wantWorkers := r.workers
	if wantWorkers == 0 {
		wantWorkers = 8
	}
	assert.Equal(t, strconv.Itoa(wantWorkers), run.attrs["kcp.workers"])
	assert.Equal(t, strconv.FormatInt(r.detect.Milliseconds(), 10), run.attrs["kcp.detect_unrouted_commits_ms"])

	var steps []span
	for _, s := range spans {
		if strings.HasPrefix(s.name, "step.") && s.parentID == run.spanID {
			steps = append(steps, s)
		}
	}
	sort.Slice(steps, func(i, j int) bool { return steps[i].start < steps[j].start })
	var stepNames []string
	for _, s := range steps {
		stepNames = append(stepNames, s.name)
	}
	assert.Equal(t, conversionSteps, stepNames)
	for _, s := range append([]span{root[0], run}, steps...) {
		assert.Falsef(t, s.failed, "span %s has error status", s.name)
	}

	for _, name := range []string{
		"gateway.patch_cr", "gateway.wait_accepted",
		"groupoffsets.snapshot", "groupoffsets.detect_rogue_commits",
		"groupoffsets.destination_hwm", "groupoffsets.apply",
	} {
		assert.NotEmptyf(t, byName[name], "expected at least one %s span", name)
	}

	// The commit-detection wait was configured to, and took, the manifest's window.
	waits := byName["wait.rogue_commit_window"]
	require.Len(t, waits, 1)
	assert.Equal(t, strconv.FormatInt(r.detect.Milliseconds(), 10), waits[0].attrs["kcp.wait.configured_ms"])
	took := time.Duration(waits[0].end - waits[0].start)
	assert.GreaterOrEqualf(t, took, r.detect, "the window ran its full %s (took %s)", r.detect, took)
	assert.Lessf(t, took, r.detect+detectionSlack, "the window took %s, configured %s", took, r.detect)

	// Every seeded group was synced, with the configured number of workers.
	apply := byName["groupoffsets.apply"]
	require.Len(t, apply, 1)
	assert.Equal(t, strconv.Itoa(r.groups), apply[0].attrs["kcp.groups"], "groups synced")
	assert.Equal(t, strconv.Itoa(wantWorkers), apply[0].attrs["kcp.workers"])

	// Each worker's client build is its own span: one per worker actually built (no more than the groups
	// handed out), under each snapshot and under the apply.
	opensBy := map[string][]span{}
	for _, o := range byName["groupoffsets.open_client"] {
		opensBy[o.parentID] = append(opensBy[o.parentID], o)
	}
	for _, name := range []string{"groupoffsets.snapshot", "groupoffsets.apply"} {
		for _, s := range byName[name] {
			groups, err := strconv.Atoi(s.attrs["kcp.groups"])
			require.NoErrorf(t, err, "%s carries kcp.groups", name)
			workers, err := strconv.Atoi(s.attrs["kcp.workers"])
			require.NoErrorf(t, err, "%s carries kcp.workers", name)
			assert.Lenf(t, opensBy[s.spanID], min(workers, groups), "one open_client per worker under %s", name)
		}
	}

	// The listings: the source three times (reconcile's, snapshot 1 under verify_fence, snapshot 2 under the
	// detection), the destination once.
	reconcile := byName["migplan.reconcile"][0]
	verify := byName["step.verify_fence"]
	require.Len(t, verify, 1)
	detect := byName["groupoffsets.detect_rogue_commits"]
	require.Len(t, detect, 1)
	lists := byName["groupoffsets.list_groups"]
	require.Len(t, lists, 4)
	parentsByCluster := map[string][]string{}
	for _, l := range lists {
		parentsByCluster[l.attrs["kcp.cluster"]] = append(parentsByCluster[l.attrs["kcp.cluster"]], l.parentID)
		n, err := strconv.Atoi(l.attrs["kcp.groups"])
		require.NoError(t, err, "list_groups carries kcp.groups")
		if l.attrs["kcp.cluster"] == "source" {
			assert.GreaterOrEqual(t, n, r.groups, "the source listing sees every seeded group")
		}
	}
	assert.ElementsMatch(t, []string{reconcile.spanID, verify[0].spanID, detect[0].spanID}, parentsByCluster["source"])

	// Reconcile's conversion reads: one span each, directly under migplan.reconcile.
	for _, name := range []string{"migplan.gather_facts", "migplan.committed_topics"} {
		require.Lenf(t, byName[name], 1, "one %s span", name)
		assert.Equalf(t, reconcile.spanID, byName[name][0].parentID, "%s is a child of migplan.reconcile", name)
	}

	// Every per-group fan-out has one kafka.group_worker per worker (no more than its groups), whose groups add
	// up to the fan-out's, each costing at least one coordinator lookup and one request of the right kind.
	workersBy := map[string][]span{}
	for _, w := range byName["kafka.group_worker"] {
		workersBy[w.parentID] = append(workersBy[w.parentID], w)
	}
	fanOuts := []struct {
		name, api string
		workers   int
	}{
		{"migplan.committed_topics", "OffsetFetch", 8},
		{"groupoffsets.snapshot", "OffsetFetch", wantWorkers},
		{"groupoffsets.apply", "OffsetCommit", wantWorkers},
	}
	for _, f := range fanOuts {
		for _, s := range byName[f.name] {
			groups, err := strconv.Atoi(s.attrs["kcp.groups"])
			require.NoErrorf(t, err, "%s carries kcp.groups", f.name)
			ws := workersBy[s.spanID]
			require.Lenf(t, ws, min(f.workers, groups), "one kafka.group_worker per worker under %s", f.name)
			sum := 0
			for _, w := range ws {
				n := intAttr(t, w, "kcp.groups")
				sum += n
				assert.GreaterOrEqualf(t, intAttr(t, w, "kcp.coordinator_lookup.count"), n, "a lookup per group under %s", f.name)
				assert.GreaterOrEqualf(t, intAttr(t, w, "kcp.request.count"), n, "a request per group under %s", f.name)
				if n > 0 {
					assert.Equalf(t, f.api, w.attrs["kcp.request.api"], "the request under %s", f.name)
				}
				for _, key := range []string{"kcp.coordinator_lookup.total_ms", "kcp.request.total_ms"} {
					_, err := strconv.ParseFloat(w.attrs[key], 64)
					assert.NoErrorf(t, err, "kafka.group_worker under %s carries %s", f.name, key)
				}
				assert.Falsef(t, w.failed, "kafka.group_worker under %s has error status", f.name)
			}
			assert.Equalf(t, groups, sum, "the workers under %s finished every group", f.name)
		}
	}
	assert.Equal(t, []string{verify[0].spanID}, parentsByCluster["destination"])

	// The rest of verify_fence: one span each, directly under the step.
	for _, name := range []string{"d2s.gather_facts", "d2s.check_link_scope", "d2s.check_split_brain"} {
		require.Lenf(t, byName[name], 1, "one %s span", name)
		assert.Equalf(t, verify[0].spanID, byName[name][0].parentID, "%s is a child of step.verify_fence", name)
	}
	assert.Equal(t, strconv.Itoa(r.groups), byName["d2s.check_link_scope"][0].attrs["kcp.groups.in_scope"], "every seeded group is in scope")
	for _, name := range []string{"groupoffsets.open_client", "groupoffsets.list_groups", "d2s.gather_facts", "d2s.check_link_scope", "d2s.check_split_brain",
		"migplan.gather_facts", "migplan.committed_topics"} {
		for _, s := range byName[name] {
			assert.Falsef(t, s.failed, "span %s has error status", name)
		}
	}

	counts := map[string]int{}
	for n, ss := range byName {
		counts[n] = len(ss)
	}
	t.Logf("\n✅ RESULT [%s]: %d spans in one trace; steps %v; per-name counts %v; trace file %s", r.label, len(spans), stepNames, counts, traceFile)
}

// intAttr is span s's integer attribute key.
func intAttr(t *testing.T, s span, key string) int {
	t.Helper()
	n, err := strconv.Atoi(s.attrs[key])
	require.NoErrorf(t, err, "%s carries %s", s.name, key)
	return n
}
