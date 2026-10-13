//go:build e2e

package tracing_e2e

import (
	"context"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTracing_CleanTBMRunEmitsOneCompleteTrace(t *testing.T) {
	e := newEnv()
	ctx := context.Background()
	e.resetStaticRoute(t, ctx) // no-op on dynamic
	topics := e.freshTopics(t, ctx, 5)
	mani := e.writeManifest(t, "tracing", topics)
	e.saveManifest(t, mani)

	dir := e.testReportDir(t)
	if dir == "" {
		dir = t.TempDir()
	}
	traceFile := filepath.Join(dir, "kcp-traces.jsonl")

	e.snapshot(t, ctx, "before", "BEFORE — pristine (expect mirrors ACTIVE)", topics)
	out, err := e.runKCP(t, "kcp-execute.log", traceFile, "migration", "execute", "--migration-yaml", mani)
	require.NoError(t, err)
	require.Contains(t, strings.ToLower(out), "migration complete")
	e.snapshot(t, ctx, "after", "AFTER execute (expect mirrors STOPPED, route → destination-domain)", topics)

	spans := readTrace(t, traceFile)
	byName := map[string][]span{}
	traces := map[string]bool{}
	for _, s := range spans {
		byName[s.name] = append(byName[s.name], s)
		traces[s.traceID] = true
		assert.Falsef(t, s.failed, "span %s has error status", s.name)
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

	for _, name := range []string{"migplan.reconcile", "migration.build_services", "migration.run"} {
		require.Lenf(t, byName[name], 1, "one %s span", name)
		assert.Equalf(t, root[0].spanID, byName[name][0].parentID, "%s is a child of the command span", name)
	}
	run := byName["migration.run"][0]
	assert.Equal(t, "tbm", run.attrs["kcp.strategy"])

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
	assert.Equal(t, []string{"step.initialize", "step.wait_for_lags", "step.fence", "step.verify_fence", "step.promote", "step.switch"}, stepNames)

	for _, name := range []string{
		"offset.sweep", "offset.get_many", "offset.list_offsets",
		"gateway.patch_cr", "gateway.wait_accepted",
		"clusterlink.list_mirror_topics", "clusterlink.promote_mirror_topics",
		"kafka.list_topics", "kafka.cluster_id", "clusterlink.link_status",
	} {
		assert.NotEmptyf(t, byName[name], "expected at least one %s span", name)
	}
	assert.True(t, len(byName["gateway.verify_transition"])+len(byName["gateway.wait_config_applied"]) > 0, "the fence and switch are verified")

	counts := map[string]int{}
	for n, ss := range byName {
		counts[n] = len(ss)
	}
	t.Logf("\n✅ RESULT: %d spans in one trace; steps %v; per-name counts %v; trace file %s", len(spans), stepNames, counts, traceFile)
}
