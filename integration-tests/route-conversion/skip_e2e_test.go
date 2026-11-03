//go:build e2e

package routeconversion

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// An omitted or explicit 0s commits window converts the route without the direct-commit check.
func TestConversion_CommitsCheckOff(t *testing.T) {
	const window = "detectUnroutedCommitsDuration"
	cases := []struct {
		name     string
		policies string
		written  bool
	}{
		{"omitted", "", false},
		{"explicit-0s", "    policies:\n      " + window + ": 0s\n", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			ctx := context.Background()
			e.reset(t, ctx)
			seeds := e.seedGroups(t, e.defaultSeeds("off-"+tc.name)...)
			security := e.liveRoute(t, ctx)["security"]
			mani, id := e.writeManifestWithPolicies(t, "commits-off-"+tc.name, tc.policies)
			raw, err := os.ReadFile(mani)
			require.NoError(t, err)
			require.Equal(t, tc.written, strings.Contains(string(raw), window), "the manifest's window must match the case")
			e.requireNoDestOffsets(t, seeds, "before the conversion")

			out, err := e.execute(t, "kcp-run-1-execute.log", mani, nil, nil)

			requireCompleted(t, out, err, id)
			require.Contains(t, out, commitsSkippedLine)
			require.NotContains(t, out, "Watching committed offsets")
			require.NotContains(t, out, commitsCleanLine)
			e.requireConvertedRoute(t, ctx, security)
			e.requireSynced(t, seeds)
		})
	}
}

// Clients through the gateway miss nothing when the omitted window shrinks the fence-to-switch gap.
func TestContinuousClients_ConversionCommitsCheckOff(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.reset(t, ctx)
	security := e.liveRoute(t, ctx)["security"]
	dir := e.workDir(t)
	w := e.startWatcher(ctx)
	run := e.startClientRun(t, dir)
	time.Sleep(runBefore)

	mani, id := e.writeManifest(t, "continuous-commits-off", 0)
	out, err := e.execute(t, "kcp-run-1-execute.log", mani, nil, nil)
	requireCompleted(t, out, err, id)
	require.Contains(t, out, commitsSkippedLine)
	require.NotContains(t, out, "Watching committed offsets")
	win := e.finishClientRun(t, w, run)
	e.requireConvertedRoute(t, ctx, security)
	e.checkClientRun(t, run, win)
}
