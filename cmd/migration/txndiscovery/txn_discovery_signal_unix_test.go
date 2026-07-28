//go:build unix

package txndiscovery

import (
	"context"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/confluentinc/kcp/internal/services/txndiscovery/discovery"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// R4/F1: SIGINT ends the observation window early and the artifacts are still
// written. An operator who has seen enough must be able to stop the run and
// keep what it observed; exiting empty-handed would make a long window
// unusable, because stopping it would throw the whole run away.
//
// kcp ships a Windows binary and syscall.Kill does not exist there, so this
// file is unix-gated: a runtime GOOS skip cannot save a package that will not
// compile.
func TestRun_SIGINT_EndsTheWindowEarlyAndStillWritesTheArtifacts(t *testing.T) {
	dir := t.TempDir()
	h := newHarness()
	h.tailClient.script(discovery.DefaultTxnStateTopic, stateRecordBatch(0,
		[][]byte{txnKey("interrupted-txn")},
		[][]byte{txnValue(31337, "interrupted.out.a", "interrupted.out.b")},
	))

	opts := baseOpts(dir)
	// Far longer than the test may take, so only the signal can end this run.
	opts.Duration = time.Hour
	opts.StatsOutPath = filepath.Join(dir, "stats.json")
	opts.TailConsumerOffsets = false
	// Enrichment on, so the run holds its own second connection to the broker: SIGINT
	// is the exit path most likely to skip a release, and a signalled run that leaked
	// an authenticated session would do so once per interrupted invocation.
	opts.EnrichConsumerGroups = true

	runner := h.runner(t, opts)
	// The real production window: wait for the duration or for ctx, which
	// signal.NotifyContext cancels on SIGINT. Nothing here fakes the signal
	// path — the point is that the wiring in Run works.
	runner.window = waitWindow

	// Send the signal only once the tail is demonstrably reading, so it cannot
	// arrive before Run has installed its handler — where the process default
	// for SIGINT would terminate the test binary.
	go func() {
		for h.tailClient.fetchesFor(discovery.DefaultTxnStateTopic) < 3 {
			time.Sleep(5 * time.Millisecond)
		}
		require.NoError(t, syscall.Kill(syscall.Getpid(), syscall.SIGINT))
	}()

	started := time.Now()
	require.NoError(t, runner.Run(context.Background()))

	assert.Less(t, time.Since(started), 30*time.Second, "the signal, not the hour-long window, ended the run")

	yaml := readFile(t, opts.OutPath)
	assert.Contains(t, yaml, "interrupted.out.a", "everything observed before the signal is kept")
	assert.Contains(t, yaml, "interrupted.out.b")
	assert.True(t, exists(t, opts.StatsOutPath))
	assert.True(t, exists(t, opts.AuditLogPath))
	assert.Equal(t, 1, h.closes, "the signal path released the cluster's connections exactly once")
}
