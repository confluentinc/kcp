//go:build unix

package txndiscovery

import (
	"context"
	"os"
	"os/exec"
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

// The environment a child process is driven by. The case below has to run in a
// process of its own: what it asserts is that a signal TERMINATES that process,
// which cannot be asserted from inside the process it would terminate.
const (
	signalChildEnv    = "KCP_TXNDISCOVERY_SIGNAL_CHILD"
	signalChildDirEnv = "KCP_TXNDISCOVERY_SIGNAL_DIR"

	readyMarker       = "child-reading"
	windowEndedMarker = "child-window-ended"
)

// R4/F1's other half: a SECOND signal, sent while the run is draining, must kill the
// process.
//
// signal.NotifyContext runs a one-shot goroutine that cancels the context on the first
// signal and then exits, leaving os/signal's registration in place on a size-1 channel
// nobody will ever read again. Every later signal is therefore delivered into that
// buffer and discarded. With stopSignals() deferred to the end of Run, that state lasts
// for the whole drain — precisely the stretch where the operator has reason to want out,
// because the drain is where the run talks to the broker for the last time and where an
// unresponsive one makes it hang. The operator's Ctrl-C does nothing, twice, three
// times, and SIGKILL — which loses all three artifacts, the entire product of the run —
// is the only escape.
//
// So stopSignals() is called as soon as the window is over, before the drain starts,
// which hands SIGINT back to Go's default disposition.
func TestRun_ASecondSignalDuringTheDrainTerminatesTheProcess(t *testing.T) {
	if os.Getenv(signalChildEnv) != "" {
		t.Skip("this process IS the child")
	}

	dir := t.TempDir()
	// The child's output goes to a FILE rather than to a bytes.Buffer: exec copies
	// into a non-*os.File on a goroutine of its own, and this case has to read that
	// output before Wait has returned, which would be a data race.
	logPath := filepath.Join(dir, "child.log")
	childLog, err := os.Create(logPath) //nolint:gosec // a path this test built
	require.NoError(t, err)
	defer func() { _ = childLog.Close() }()

	child := exec.Command(os.Args[0], "-test.run=^TestRun_SignalDrainChild$", "-test.timeout=120s") //nolint:gosec // the test binary re-executing itself
	child.Env = append(os.Environ(), signalChildEnv+"=1", signalChildDirEnv+"="+dir)
	child.Stdout = childLog
	child.Stderr = childLog
	require.NoError(t, child.Start())
	t.Cleanup(func() { _ = child.Process.Kill() })

	// The handler is installed before the tail starts fetching, so a child that is
	// demonstrably reading is a child that will not die of the first signal.
	waitForMarker(t, filepath.Join(dir, readyMarker), 60*time.Second, "the child never started reading", logPath)

	t.Log("sending SIGINT #1 (should end the observation window)")
	require.NoError(t, syscall.Kill(child.Process.Pid, syscall.SIGINT))
	waitForMarker(t, filepath.Join(dir, windowEndedMarker), 60*time.Second, "the first signal did not end the child's observation window", logPath)

	exited := make(chan error, 1)
	go func() { exited <- child.Wait() }()

	// The operator aborting a drain that has stopped responding: repeated, spaced
	// out, because that is what a person does — and because the first of them can
	// land in the microseconds between the window ending and stopSignals() running,
	// where it is still swallowed by design.
	jab := time.NewTicker(250 * time.Millisecond)
	defer jab.Stop()
	giveUp := time.After(8 * time.Second)

	var waitErr error
wait:
	for {
		select {
		case waitErr = <-exited:
			break wait
		case <-jab.C:
			t.Log("sending another SIGINT (the operator aborting a hung drain)")
			_ = syscall.Kill(child.Process.Pid, syscall.SIGINT)
		case <-giveUp:
			t.Fatalf("child STILL ALIVE after repeated SIGINTs: the process was never interruptible, so SIGKILL and the loss of every artifact is the only way out.\nchild output:\n%s", tailOf(logPath))
		}
	}

	var exitErr *exec.ExitError
	require.ErrorAs(t, waitErr, &exitErr, "child output:\n%s", tailOf(logPath))
	status, ok := exitErr.Sys().(syscall.WaitStatus)
	require.True(t, ok)
	require.True(t, status.Signaled(),
		"the child exited on its own (code %d) rather than being terminated by a signal: the drain finished before the SIGINTs did anything, so this proves nothing about interruptibility.\nchild output:\n%s",
		status.ExitStatus(), tailOf(logPath))
	assert.Equal(t, syscall.SIGINT, status.Signal(), "terminated by the wrong signal")
}

// TestRun_SignalDrainChild is the child half of the case above: a run whose drain is
// stuck talking to a broker that has stopped answering. It is skipped unless its parent
// started it.
func TestRun_SignalDrainChild(t *testing.T) {
	dir := os.Getenv(signalChildDirEnv)
	if os.Getenv(signalChildEnv) == "" || dir == "" {
		t.Skip("driven by TestRun_ASecondSignalDuringTheDrainTerminatesTheProcess")
	}

	h := newHarness()
	h.tailClient.script(discovery.DefaultTxnStateTopic, stateRecordBatch(0,
		[][]byte{txnKey("hung-drain-txn")},
		[][]byte{txnValue(31337, "hung.out")},
	))
	// Enrichment's closing pass is the last thing the run does before it writes
	// anything, and it talks to the broker. A broker that does not answer is what
	// puts the drain in the state the operator wants out of.
	h.admin.groups = map[string]string{"hung-drain": "consumer"}
	h.admin.delay = 30 * time.Second

	opts := baseOpts(dir)
	opts.Duration = time.Hour // only the signal can end this window
	// No tick fires, so the pass that stalls is the closing one rather than one
	// that happened to be in flight.
	opts.Interval = time.Hour
	opts.TailConsumerOffsets = false
	opts.EnrichConsumerGroups = true

	runner := h.runner(t, opts)
	runner.window = func(ctx context.Context, d time.Duration) {
		waitWindow(ctx, d)
		touch(filepath.Join(dir, windowEndedMarker))
	}

	go func() {
		for h.tailClient.fetchesFor(discovery.DefaultTxnStateTopic) < 3 {
			time.Sleep(5 * time.Millisecond)
		}
		touch(filepath.Join(dir, readyMarker))
	}()

	_ = runner.Run(context.Background())
}

// touch writes a marker the parent process polls for. Errors are not reported: the
// parent's own timeout is what fails the case, and this runs on a goroutine where a
// t.Fatal would be a data race with the run under test.
func touch(path string) {
	_ = os.WriteFile(path, []byte("x"), 0o600)
}

// waitForMarker blocks until the child has written path, failing with the child's
// output when it does not.
func waitForMarker(t *testing.T, path string, within time.Duration, what, logPath string) {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("%s within %s.\nchild output:\n%s", what, within, tailOf(logPath))
}

// tailOf reads whatever the child has written so far, for a failure message. The
// child holds the file open, so a partial read is expected and fine.
func tailOf(logPath string) string {
	b, err := os.ReadFile(logPath) //nolint:gosec // a path this test built
	if err != nil {
		return "<unreadable: " + err.Error() + ">"
	}
	return string(b)
}
