//go:build e2e

package retries_e2e

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// liveRun is one `kcp migration execute` the test watches while it runs, so a fault can
// be timed to a step: the test waits for a line kcp prints (a step banner such as
// "Verifying fence") and acts the moment it appears.
type liveRun struct {
	t     *testing.T
	desc  string
	cmd   *exec.Cmd
	start time.Time

	mu   sync.Mutex
	buf  bytes.Buffer
	done chan struct{}
	err  error
}

// Write collects kcp's stdout and stderr, which share this writer.
func (r *liveRun) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.buf.Write(p)
}

func (r *liveRun) output() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.buf.String()
}

// startKCP starts kcp with args in the background.
func (e *env) startKCP(t *testing.T, args ...string) *liveRun {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), executeTimeout)
	t.Cleanup(cancel)

	r := &liveRun{t: t, desc: "kcp " + strings.Join(args, " "), done: make(chan struct{}), start: time.Now()}
	r.cmd = exec.CommandContext(ctx, kcpBinary, args...)
	r.cmd.Env = os.Environ()
	r.cmd.Stdout = r
	r.cmd.Stderr = r
	require.NoError(t, r.cmd.Start(), "start kcp")
	go func() {
		r.err = r.cmd.Wait()
		close(r.done)
	}()
	return r
}

// WaitFor blocks until kcp has printed substr, the run ends, or timeout passes. It
// reports whether the line appeared.
func (r *liveRun) WaitFor(substr string, timeout time.Duration) bool {
	r.t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if strings.Contains(r.output(), substr) {
			return true
		}
		select {
		case <-r.done:
			return strings.Contains(r.output(), substr)
		case <-time.After(25 * time.Millisecond):
		}
		if time.Now().After(deadline) {
			return false
		}
	}
}

// Finished reports whether the run has ended.
func (r *liveRun) Finished() bool {
	select {
	case <-r.done:
		return true
	default:
		return false
	}
}

// Wait blocks until kcp exits, logs its full raw output as evidence (never truncated),
// saves it as reportName in the test's report folder, and returns the output and exit
// error.
func (r *liveRun) Wait(e *env, reportName string) (string, error) {
	r.t.Helper()
	<-r.done
	out := r.output()
	elapsed := time.Since(r.start).Round(time.Second)
	r.t.Logf("\n┌─────────────────────────────────────────────────────────────────────────────┐\n"+
		"│ RAW KCP RUN ▸ %s\n"+
		"└─────────────────────────────────────────────────────────────────────────────┘\n"+
		"%s\n───── process exit: err=%v after %s ─────\n", r.desc, out, r.err, elapsed)
	e.saveReport(r.t, reportName, fmt.Sprintf("$ %s\n\n%s\n───── process exit: err=%v after %s ─────\n", r.desc, out, r.err, elapsed))
	return out, r.err
}

// runKCP runs kcp to completion with no fault timing.
func (e *env) runKCP(t *testing.T, reportName string, args ...string) (string, error) {
	t.Helper()
	return e.startKCP(t, args...).Wait(e, reportName)
}

// retryLines is how many retry warnings kcp has logged so far.
func (r *liveRun) retryLines() int {
	return strings.Count(r.output(), retryLine)
}

// stepOfFirstRetryAfter returns the step banner ("Verifying fence", "Promoting batch", ...)
// kcp was in when it logged its first retry warning after the first `before` ones, or ""
// when it has not logged that many. Step banners are the lines that start a step: an
// emoji, the step's name and "...".
func stepOfFirstRetryAfter(out string, before int) string {
	step := ""
	seen := 0
	for _, line := range strings.Split(out, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "🔍 ") && strings.HasSuffix(trimmed, "...") {
			step = strings.TrimSuffix(strings.TrimPrefix(trimmed, "🔍 "), "...")
		}
		if strings.Contains(line, retryLine) {
			seen++
			if seen > before {
				return step
			}
		}
	}
	return ""
}
