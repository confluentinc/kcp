//go:build e2e

package routeconversion

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/confluentinc/kcp/internal/services/migration/d2s"
	"github.com/stretchr/testify/require"
)

// The continuous-client timings of the spec (2.5).
const (
	runBefore    = 30 * time.Second // clients run before the conversion
	runAfter     = 60 * time.Second // clients run after the switch
	drainFor     = 40 * time.Second // consumers drain after the producers stop
	switchSeenBy = 30 * time.Second // the watcher must see the switch this soon after kcp returns
)

// groupSpec is one consumer group of a client run.
type groupSpec struct {
	group      string
	topic      string
	members    []string
	autoCommit bool
}

// clientRun is the producers and consumers of one continuous-client test.
type clientRun struct {
	dir       string
	specs     []groupSpec
	prefixes  map[string]int // topic -> producer value prefix
	producers []*clientProc
	consumers map[string][]*clientProc // group -> members
}

// startClientRun starts, through the gateway: ct-manual (2 members, commit
// after every batch) on the first link topic and ct-auto (1 member,
// auto-commit) on the second, waits for both groups to be Stable on the source
// (group coordination is pinned there), then one producer per topic.
func (e *env) startClientRun(t *testing.T, dir string) *clientRun {
	t.Helper()
	run := &clientRun{dir: dir, prefixes: map[string]int{}, consumers: map[string][]*clientProc{}, specs: []groupSpec{
		{group: "ct-manual", topic: e.topic(1), members: []string{"a", "b"}},
		{group: "ct-auto", topic: e.topic(2), members: []string{"a"}, autoCommit: true},
	}}
	t.Cleanup(func() { run.stopAll(t) })
	props := e.gatewayClientProps(t, dir)
	for _, s := range run.specs {
		for _, m := range s.members {
			c := e.startConsumer(t, dir, e.gatewayBootstrap, props, s.group, s.topic, m, s.autoCommit)
			run.consumers[s.group] = append(run.consumers[s.group], c)
		}
	}
	for _, s := range run.specs {
		for _, c := range run.consumers[s.group] {
			e.waitAssigned(t, s, c)
		}
		e.waitGroupState(t, sourceCluster, s.group, "Stable", len(s.members), 2*time.Minute)
	}
	// Unique per test: the consumers read from earliest, so records earlier
	// tests left on the topic are told apart by their prefix.
	base := int(time.Now().Unix()%1_000_000) * 10
	for i, s := range run.specs {
		run.prefixes[s.topic] = base + i + 1
		run.producers = append(run.producers, e.startProducer(t, dir, s.topic, run.prefixes[s.topic]))
	}
	for _, p := range run.producers {
		p.waitForLine(t, `"name":"producer_send_success"`, 2*time.Minute)
	}
	return run
}

func (r *clientRun) stopProducers(t *testing.T) {
	for _, p := range r.producers {
		p.stop(t)
	}
}

func (r *clientRun) stopConsumers(t *testing.T) {
	for _, cs := range r.consumers {
		for _, c := range cs {
			c.stop(t)
		}
	}
}

func (r *clientRun) stopAll(t *testing.T) {
	r.stopProducers(t)
	r.stopConsumers(t)
}

// finishClientRun waits for the watcher to see the switch, lets the clients run
// on, stops the producers, lets the consumers drain, stops them, and returns
// the watcher's windows.
func finishClientRun(t *testing.T, w *watcher, run *clientRun) Windows {
	t.Helper()
	require.Eventually(t, func() bool { return w.windows().SwitchDoneMs > 0 }, switchSeenBy, 500*time.Millisecond,
		"the watcher must see the route static")
	time.Sleep(runAfter)
	run.stopProducers(t)
	time.Sleep(drainFor)
	run.stopConsumers(t)
	return w.stop()
}

// checkClientRun parses every client log, runs the checker per group, saves
// the verdicts as checker.txt and fails the test on any failing group.
func (e *env) checkClientRun(t *testing.T, run *clientRun, win Windows) {
	t.Helper()
	var report strings.Builder
	var results []GroupResult
	bounds := DefaultBounds()
	bounds.ProduceRatePerSec = producerRate
	for _, s := range run.specs {
		in := CheckInput{Group: s.group, Topic: s.topic, ValuePrefix: fmt.Sprint(run.prefixes[s.topic]),
			AutoCommit: s.autoCommit, Windows: win, Bounds: bounds}
		for _, p := range run.producers {
			if p.name != "producer-"+s.topic {
				continue
			}
			f, err := os.Open(p.logPath)
			require.NoError(t, err)
			pl, err := ParseProducerLog(f)
			_ = f.Close()
			require.NoError(t, err)
			in.Producers = append(in.Producers, pl)
		}
		for i, c := range run.consumers[s.group] {
			f, err := os.Open(c.logPath)
			require.NoError(t, err)
			cl, err := ParseConsumerLog(s.members[i], f)
			_ = f.Close()
			require.NoError(t, err)
			in.Consumers = append(in.Consumers, cl)
		}
		res := Check(in)
		results = append(results, res)
		report.WriteString(res.String())
	}
	t.Logf("\n%s", report.String())
	e.saveReport(t, "checker.txt", report.String())
	for _, r := range results {
		require.Truef(t, r.OK(), "group %s: %s", r.Group, strings.Join(r.Failures, "; "))
	}
}

// Clients run through the gateway across an uninterrupted conversion: no
// acknowledged record is missed, and every re-read is the fence onset's or the
// switch's, within its bound.
func TestContinuousClients_Conversion(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.reset(t, ctx)
	security := e.liveRoute(t, ctx)["security"]
	dir := e.workDir(t)
	w := e.startWatcher(ctx)
	run := e.startClientRun(t, dir)
	time.Sleep(runBefore)

	mani, id := e.writeManifest(t, "continuous", minDetect)
	out, err := e.execute(t, "kcp-run-1-execute.log", mani, nil, nil)
	requireCompleted(t, out, err, id)
	win := finishClientRun(t, w, run)
	e.requireConvertedRoute(t, ctx, security)
	e.checkClientRun(t, run, win)
	t.Logf("\n✅ RESULT: clients through the gateway missed nothing across the conversion; every re-read was bounded.")
}

// The same, with run 1 interrupted after offsets_synced while the clients keep
// running: run 2 finishes, and the fence-onset window is run 1's.
func TestContinuousClients_ResumeAfterOffsetsSynced(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.reset(t, ctx)
	security := e.liveRoute(t, ctx)["security"]
	dir := e.workDir(t)
	w := e.startWatcher(ctx)
	run := e.startClientRun(t, dir)
	time.Sleep(runBefore)

	mani, id := e.writeManifest(t, "continuous-resume", minDetect)
	out, err := e.execute(t, "kcp-run-1-interrupted.log", mani, cancelAfter(d2s.StateOffsetsSynced), nil)
	requireInterrupted(t, out, err)
	e.requireFenced(t, ctx, "interrupted after sync_offsets")
	out, err = e.execute(t, "kcp-run-2-resume.log", mani, nil, nil)
	requireCompleted(t, out, err, id)
	win := finishClientRun(t, w, run)
	e.requireConvertedRoute(t, ctx, security)
	e.checkClientRun(t, run, win)
	t.Logf("\n✅ RESULT: clients missed nothing across an interrupted-then-resumed conversion; every re-read was bounded.")
}

// waitAssigned waits for a member's first assignment. On a timeout it logs the
// group's state and members on the source before failing, so a stalled join can
// be diagnosed from the report.
func (e *env) waitAssigned(t *testing.T, s groupSpec, c *clientProc) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Minute)
	for {
		if raw, err := os.ReadFile(c.logPath); err == nil && strings.Contains(string(raw), `"name":"partitions_assigned"`) {
			return
		}
		if time.Now().After(deadline) {
			ds, err := e.groupClient(t, sourceCluster).DescribeGroups([]string{s.group})
			if err != nil {
				t.Logf("group %s on the source could not be described: %v", s.group, err)
			}
			for _, d := range ds {
				t.Logf("group %s on the source: state %s, %d member(s): %+v", s.group, d.State, len(d.Members), d.Members)
			}
			t.Fatalf("%s never logged partitions_assigned within 3m (see %s)", c.name, c.errPath)
		}
		time.Sleep(time.Second)
	}
}
