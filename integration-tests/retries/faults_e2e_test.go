//go:build e2e

package retries_e2e

import (
	"fmt"
	"net"
	"net/url"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Faults are iptables rules in the runner pod's own network namespace, keyed on a target
// pod's IP and port, so from kcp's side one cluster disappears while the cluster itself
// is untouched. The runner pod carries NET_ADMIN and iptables (kcp-runner.yaml).
//
//   - faultReset: new connections are refused and open ones reset (REJECT with tcp-reset
//     on kcp's outgoing packets). What a cluster that restarts, or drops its clients, looks like.
//   - faultBlackhole: kcp's outgoing packets are dropped, so a request waits for kcp's own
//     timeout. What an unreachable cluster looks like.
//   - dropReplies: the cluster's replies are dropped while requests still reach it. With a
//     reset added afterwards, this is a request the cluster accepted whose answer was lost.
type faultKind int

const (
	faultReset faultKind = iota
	faultBlackhole
)

func (k faultKind) String() string {
	if k == faultBlackhole {
		return "blackhole"
	}
	return "reset"
}

// target is one endpoint of one cluster, as kcp dials it.
type target struct {
	name string // for the logs
	host string
	port string
}

func parseHostPort(name, hostPort string) target {
	host, port, err := net.SplitHostPort(hostPort)
	if err != nil {
		panic(fmt.Sprintf("%s: %q is not host:port: %v", name, hostPort, err))
	}
	return target{name: name, host: host, port: port}
}

func (e *env) sourceKafka() target { return parseHostPort("source Kafka", e.sourceBootstrap) }
func (e *env) destKafka() target   { return parseHostPort("destination Kafka", e.destBootstrap) }

// destREST is the destination's REST endpoint: the cluster link's mirror status, the
// promote request and the link's configs.
func (e *env) destREST() target {
	u, err := url.Parse(e.restEndpoint)
	if err != nil {
		panic(fmt.Sprintf("REST endpoint %q: %v", e.restEndpoint, err))
	}
	port := u.Port()
	if port == "" {
		port = "80"
	}
	return target{name: "destination REST", host: u.Hostname(), port: port}
}

// ip resolves the target's pod IP at fault time: a pod that restarted has a new one.
func (tg target) ip() (string, error) {
	ips, err := net.LookupIP(tg.host)
	if err != nil {
		return "", fmt.Errorf("resolve %s (%s): %w", tg.name, tg.host, err)
	}
	for _, ip := range ips {
		if v4 := ip.To4(); v4 != nil {
			return v4.String(), nil
		}
	}
	return "", fmt.Errorf("%s (%s) resolved to no IPv4 address: %v", tg.name, tg.host, ips)
}

// iptables runs one iptables command, waiting for the xtables lock.
func iptables(args ...string) (string, error) {
	out, err := exec.Command("iptables", append([]string{"-w"}, args...)...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// faultLog serialises the log lines of the storm test's goroutine with the test's own.
var faultLog sync.Mutex

// cut starts a fault against tg and returns the function that ends it. The returned
// function is safe to call from another goroutine and more than once. It fails the test
// if the fault cannot be set up, so call it only from the test's own goroutine; a
// goroutine uses tryCut.
func (e *env) cut(t *testing.T, tg target, kind faultKind) (restore func()) {
	t.Helper()
	restore, err := e.tryCut(t, tg, kind)
	require.NoError(t, err)
	return restore
}

// tryCut is cut returning the setup error instead of failing the test.
func (e *env) tryCut(t *testing.T, tg target, kind faultKind) (func(), error) {
	t.Helper()
	ip, err := tg.ip()
	if err != nil {
		return nil, err
	}
	rule := []string{"OUTPUT", "-p", "tcp", "-d", ip, "--dport", tg.port, "-j"}
	if kind == faultBlackhole {
		rule = append(rule, "DROP")
	} else {
		rule = append(rule, "REJECT", "--reject-with", "tcp-reset")
	}
	return e.insert(t, fmt.Sprintf("%s ▸ %s (%s:%s)", kind, tg.name, ip, tg.port), rule)
}

// dropReplies drops the replies of tg, except resets, so a reset added later still reaches
// kcp's socket (REJECT's own reset comes back through the same chain).
func (e *env) dropReplies(t *testing.T, tg target) (restore func()) {
	t.Helper()
	ip, err := tg.ip()
	require.NoError(t, err)
	rule := []string{"INPUT", "-p", "tcp", "-s", ip, "--sport", tg.port, "!", "--tcp-flags", "RST", "RST", "-j", "DROP"}
	restore, err = e.insert(t, fmt.Sprintf("replies dropped ▸ %s (%s:%s)", tg.name, ip, tg.port), rule)
	require.NoError(t, err)
	return restore
}

func (e *env) insert(t *testing.T, label string, rule []string) (func(), error) {
	t.Helper()
	out, err := iptables(append([]string{"-I"}, rule...)...)
	if err != nil {
		return nil, fmt.Errorf("insert fault rule (%s): %w: %s", label, err, out)
	}
	faultLog.Lock()
	t.Logf("⚡ FAULT ON  %s  [%s]", label, time.Now().UTC().Format("15:04:05.000"))
	faultLog.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			out, err := iptables(append([]string{"-D"}, rule...)...)
			faultLog.Lock()
			defer faultLog.Unlock()
			if err != nil {
				t.Logf("⚠️ could not remove fault rule (%s): %v: %s", label, err, out)
				return
			}
			t.Logf("🩹 FAULT OFF %s  [%s]", label, time.Now().UTC().Format("15:04:05.000"))
		})
	}, nil
}

// retryWait bounds how long blipUntilRetried waits for kcp to log a retry.
const retryWait = 90 * time.Second

// blipUntilRetried cuts tg, holds the fault until kcp has logged a retry warning that
// the fault caused, keeps it for hold longer, then restores it. It returns the step kcp
// was in when it first retried. A fault that never reaches a call kcp retries fails the
// test: the run would otherwise pass by luck of timing without testing anything.
func (e *env) blipUntilRetried(t *testing.T, run *liveRun, tg target, kind faultKind, hold time.Duration) (step string) {
	t.Helper()
	before := run.retryLines()
	restore := e.cut(t, tg, kind)
	defer restore()
	deadline := time.Now().Add(retryWait)
	for run.retryLines() <= before {
		if run.Finished() || time.Now().After(deadline) {
			restore()
			require.FailNowf(t, "the fault never caused a retry",
				"kcp did not log a retry within %s of cutting the %s (run finished: %v): the fault missed every call kcp retries",
				retryWait, tg.name, run.Finished())
		}
		time.Sleep(25 * time.Millisecond)
	}
	step = stepOfFirstRetryAfter(run.output(), before)
	t.Logf("↩️  kcp retried in step %q; holding the fault %s longer", step, hold)
	time.Sleep(hold)
	return step
}

// flushFaults removes every rule in the runner pod, so a failed test never leaves a
// cluster cut for the next one. Registered by every test.
func (e *env) flushFaults(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		for _, chain := range []string{"INPUT", "OUTPUT"} {
			if out, err := iptables("-F", chain); err != nil {
				t.Logf("⚠️ could not flush %s: %v: %s", chain, err, out)
			}
		}
	})
}
