//go:build e2e

package migration_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// offsetBenchResult mirrors the JSON printed by testdata/offsetbench.
type offsetBenchResult struct {
	Topics          int     `json:"topics"`
	PartitionsTotal int     `json:"partitions_total"`
	LoopMs          int64   `json:"loop_ms"`
	LoopPerTopicUs  int64   `json:"loop_per_topic_us"`
	BatchUs         int64   `json:"batch_us"`
	Speedup         float64 `json:"speedup"`
	OffsetsMatch    bool    `json:"offsets_match"`
}

// benchTopicCount returns the benchmark topic count, overridable via
// KCP_E2E_BENCH_TOPICS for quicker local iterations.
func benchTopicCount(t *testing.T) int {
	t.Helper()
	if v := os.Getenv("KCP_E2E_BENCH_TOPICS"); v != "" {
		n, err := strconv.Atoi(v)
		require.NoError(t, err, "KCP_E2E_BENCH_TOPICS must be an integer")
		return n
	}
	return 1000
}

// buildAndCopyOffsetBench cross-compiles testdata/offsetbench for the runner
// pod and copies it to /workspace/offsetbench. Mirrors what setup.sh does for
// the producer/consumer helpers, but done here so the benchmark can be
// iterated on without re-running the full environment setup.
func buildAndCopyOffsetBench(t *testing.T, cfg envConfig) {
	t.Helper()

	binPath := filepath.Join(t.TempDir(), "offsetbench-linux")
	build := exec.Command("go", "build", "-o", binPath, "./testdata/offsetbench")
	build.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux")
	out, err := build.CombinedOutput()
	require.NoError(t, err, "failed to build offsetbench: %s", string(out))

	cp := exec.Command("kubectl",
		"--context", cfg.KubeContext,
		"-n", cfg.Namespace,
		"cp", binPath, cfg.KCPPod+":/workspace/offsetbench")
	out, err = cp.CombinedOutput()
	require.NoError(t, err, "failed to copy offsetbench into pod: %s", string(out))

	_, err = runInPod(t, cfg, 30*time.Second, "chmod", "+x", "/workspace/offsetbench")
	require.NoError(t, err, "failed to chmod offsetbench in pod")
}

// runOffsetBench executes the benchmark inside the runner pod against the
// source cluster and parses the trailing JSON line from stdout.
func runOffsetBench(t *testing.T, cfg envConfig, topics int) offsetBenchResult {
	t.Helper()

	stdout, err := runInPod(t, cfg, 5*time.Minute,
		"/workspace/offsetbench",
		"--bootstrap", cfg.SourceBootstrap,
		"--topics", strconv.Itoa(topics),
	)
	require.NoError(t, err, "offsetbench run failed: %s", stdout)

	// The result is the single JSON line on stdout, but kubectl exec merges
	// remote stdout/stderr by read-arrival order, so a late progress line
	// can land after it — scan backwards for the last line that parses.
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	var res offsetBenchResult
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if strings.HasPrefix(line, "{") && json.Unmarshal([]byte(line), &res) == nil {
			return res
		}
	}
	t.Fatalf("no JSON result line in offsetbench output: %s", stdout)
	return res
}

// TestOffsetFetchBenchmark quantifies the offset-fetch sweep the migration
// workflow performs before promoting (CheckLags and the PromoteTopics
// zero-lag scan). Pre-optimization the workflow called offset.Service.Get
// once per topic per cluster — with ~1000 topics that is ~1000 serial
// ListOffsets round trips per cluster per sweep, the "~70s of prep"
// observed against real clusters. The workflow now uses GetMany (one
// ListOffsets request per leader broker); this test times both sweeps over
// the same topics in the same run and asserts the batched sweep is
// dramatically faster and returns identical offsets.
//
// The speedup floor is deliberately far below the observed in-cluster
// ratio: the point is catching a regression to per-topic round trips, not
// asserting an exact latency profile. Real-world gains are larger still —
// per-topic cost scales with network RTT (~50ms against AWS vs sub-ms
// here), while the batched sweep pays O(brokers) RTTs regardless of topic
// count.
func TestOffsetFetchBenchmark(t *testing.T) {
	cfg := loadEnvConfig(t, scenarioBaseline)
	topics := benchTopicCount(t)

	buildAndCopyOffsetBench(t, cfg)

	res := runOffsetBench(t, cfg, topics)
	require.Equal(t, topics, res.Topics)
	require.NotZero(t, res.PartitionsTotal, "sweep returned no partitions")
	require.True(t, res.OffsetsMatch, "loop and batch sweeps returned different offsets")

	t.Logf("offset fetch sweep over %d topics (%d partitions total):", res.Topics, res.PartitionsTotal)
	t.Logf("  per-topic loop (pre-optimization workflow): %d ms total, %d µs/topic",
		res.LoopMs, res.LoopPerTopicUs)
	t.Logf("  batched GetMany (current workflow):         %d µs total", res.BatchUs)
	t.Logf("  speedup: %.1fx", res.Speedup)

	// A regression back to per-topic fetching shows up as ~1x; the floor
	// only needs to sit safely above that. Under a quiet cluster the
	// observed ratio is ~85x, but when the whole e2e suite has just run,
	// broker load compresses it to single digits — so keep the floor low
	// enough to never flake on a busy broker.
	require.Greater(t, res.Speedup, 3.0,
		"batched sweep should be at least 3x faster than per-topic sweep at %d topics", topics)
}
