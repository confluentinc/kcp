//go:build e2e

package tracing_e2e

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

// tracedRun is one migration the suite runs and the trace it must produce. The
// runs vary the topic count, the unrouted-producer detection window and the
// promote batch size, so a pass gives several distinct, labelled runs to compare.
// A run's label is the environment's route mode (static or dynamic) followed by
// its name, e.g. dynamic-5-topics-detect-10s.
type tracedRun struct {
	name   string
	label  string
	topics int
	policies
}

var tracedRuns = []tracedRun{
	{name: "3-topics", topics: 3},
	{name: "5-topics-detect-10s", topics: 5, policies: policies{detect: 10 * time.Second}},
	{name: "8-topics-batch-2", topics: 8, policies: policies{promoteBatch: 2}},
	{name: "10-topics-detect-15s-batch-5", topics: 10, policies: policies{detect: 15 * time.Second, promoteBatch: 5}},
	{name: "12-topics-detect-15s", topics: 12, policies: policies{detect: 15 * time.Second}},
}

// detectionSlack is how much longer than its configured window the detection
// wait may take: the window itself plus scheduling, never less than configured.
const detectionSlack = time.Second

// expectedRun is what one clean run's trace must show for the environment's
// route mode: a dynamic route runs TBM, a static route AAO.
func expectedRun(mode string) (strategy string, steps []string) {
	if mode == "static" {
		return "aao", []string{"step.initialize", "step.wait_for_lags", "step.fence", "step.pause_offset_sync",
			"step.verify_fence", "step.promote", "step.switch", "step.restore_offset_sync"}
	}
	return "tbm", []string{"step.initialize", "step.wait_for_lags", "step.fence", "step.verify_fence", "step.promote", "step.switch"}
}

func TestTracing_RunsEmitCompleteTraces(t *testing.T) {
	e := newEnv()
	for _, r := range tracedRuns {
		r.label = e.mode + "-" + r.name
		t.Run(r.label, func(t *testing.T) { e.runAndCheckTrace(t, r) })
	}
}

func (e *env) runAndCheckTrace(t *testing.T, r tracedRun) {
	ctx := context.Background()
	e.resetStaticRoute(t, ctx) // no-op on dynamic
	topics := e.freshTopics(t, ctx, r.topics)
	mani := e.writeManifest(t, r.label, topics, r.policies)
	e.saveManifest(t, mani)

	dir := e.testReportDir(t)
	if dir == "" {
		dir = t.TempDir()
	}
	traceFile := filepath.Join(dir, "kcp-traces.jsonl")

	e.snapshot(t, ctx, "before", "BEFORE — pristine (expect mirrors ACTIVE)", topics)
	out, err := e.runKCP(t, "kcp-execute.log", traceFile, r.label, "migration", "execute", "--migration-yaml", mani)
	require.NoError(t, err)
	require.Contains(t, strings.ToLower(out), "migration complete")
	e.snapshot(t, ctx, "after", "AFTER execute (expect mirrors STOPPED, route → destination-domain)", topics)

	spans := readTrace(t, traceFile)
	byName := map[string][]span{}
	traces := map[string]bool{}
	for _, s := range spans {
		byName[s.name] = append(byName[s.name], s)
		traces[s.traceID] = true
		for k, v := range s.attrs {
			for _, topic := range topics {
				assert.NotEqualf(t, topic, v, "%s.%s carries a topic name", s.name, k)
			}
		}
	}
	require.Len(t, traces, 1, "the whole run is one trace")

	root := byName["kcp migration execute"]
	require.Len(t, root, 1)
	assert.Empty(t, strings.Trim(root[0].parentID, "0"), "the command span is the root")
	assert.Equal(t, "kcp", root[0].resource["service.name"])
	assert.Equal(t, r.label, root[0].resource["run"], "OTEL_RESOURCE_ATTRIBUTES reaches the trace")

	for _, name := range []string{"migplan.reconcile", "migration.build_services", "migration.run"} {
		require.Lenf(t, byName[name], 1, "one %s span", name)
		assert.Equalf(t, root[0].spanID, byName[name][0].parentID, "%s is a child of the command span", name)
	}
	run := byName["migration.run"][0]
	wantStrategy, wantSteps := expectedRun(e.mode)
	assert.Equal(t, wantStrategy, run.attrs["kcp.strategy"])
	assert.Equal(t, strconv.Itoa(r.topics), run.attrs["kcp.topics.migrate"])

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
	assert.Equal(t, wantSteps, stepNames)

	// A clean run ends with no error on the command, the run or any step. Spans
	// below the steps may carry an error that was retried and recovered from
	// (one broker's offset read, say), so they are not checked.
	for _, s := range append([]span{root[0], run}, steps...) {
		assert.Falsef(t, s.failed, "span %s has error status", s.name)
	}

	for _, name := range []string{
		"offset.sweep", "offset.get_many", "offset.list_offsets",
		"gateway.patch_cr", "gateway.wait_accepted",
		"clusterlink.list_mirror_topics", "clusterlink.promote_mirror_topics",
		"kafka.list_topics", "kafka.cluster_id", "clusterlink.link_status",
	} {
		assert.NotEmptyf(t, byName[name], "expected at least one %s span", name)
	}
	assert.True(t, len(byName["gateway.verify_transition"])+len(byName["gateway.wait_config_applied"]) > 0, "the fence and switch are verified")

	checkDetection(t, r, byName)
	checkPromoteBatches(t, r, byName)

	counts := map[string]int{}
	for n, ss := range byName {
		counts[n] = len(ss)
	}
	t.Logf("\n✅ RESULT [%s]: %d spans in one trace; steps %v; per-name counts %v; trace file %s", r.label, len(spans), stepNames, counts, traceFile)
}

// checkDetection asserts verify_fence matches the run's detection window: off,
// a skipped step with no detection span; on, a detection span whose wait was
// configured to, and took, the manifest's window.
func checkDetection(t *testing.T, r tracedRun, byName map[string][]span) {
	t.Helper()
	verify := byName["step.verify_fence"]
	require.Len(t, verify, 1)
	if r.detect == 0 {
		assert.Equal(t, "true", verify[0].attrs["kcp.step.skipped"], "detection off skips verify_fence")
		assert.Equal(t, "detection_disabled", verify[0].attrs["kcp.step.skip_reason"])
		assert.Empty(t, byName["offset.detect_unrouted_producers"], "no detection when it is off")
		return
	}
	require.Len(t, byName["offset.detect_unrouted_producers"], 1)
	waits := byName["wait.unrouted_producers_window"]
	require.Len(t, waits, 1)
	assert.Equal(t, strconv.FormatInt(r.detect.Milliseconds(), 10), waits[0].attrs["kcp.wait.configured_ms"],
		"the wait records the manifest's window")
	took := time.Duration(waits[0].end - waits[0].start)
	assert.GreaterOrEqualf(t, took, r.detect, "the window ran its full %s (took %s)", r.detect, took)
	assert.Lessf(t, took, r.detect+detectionSlack, "the window took %s, configured %s", took, r.detect)
}

// checkPromoteBatches asserts the promote step submitted the run's topics in the
// expected number of batches: one with no batch size, ⌈topics/batch⌉ with one.
func checkPromoteBatches(t *testing.T, r tracedRun, byName map[string][]span) {
	t.Helper()
	promote := byName["step.promote"]
	require.Len(t, promote, 1)
	want := 1
	if r.promoteBatch > 0 {
		want = (r.topics + r.promoteBatch - 1) / r.promoteBatch
	}
	batches := 0
	for _, ev := range promote[0].events {
		if ev == "promote_batch" {
			batches++
		}
	}
	assert.Equal(t, want, batches, "promote_batch events")
	assert.Equal(t, fmt.Sprint(want), promote[0].attrs["kcp.batches"])
	assert.Len(t, byName["clusterlink.promote_mirror_topics"], want, "one promote call per batch")
}
