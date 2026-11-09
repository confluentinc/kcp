//go:build e2e

package idempotent_fsm_e2e

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const (
	// falseAbortProducers matches the report this reproduces: 20 producers over
	// 4 topics, 3 gateway pods.
	falseAbortProducers = 20
	falseAbortTopics    = 4
	// sniBootstrap is the dynamic route's endpoint, as
	// testdata/manifests/templates/gateway-dynamic.yaml declares it.
	sniBootstrap = "bootstrap.gw.local:9595"
)

// TestFenceRollout_TBMDetectionAbortsOnReplacedPod reproduces a false abort and
// asserts TODAY's behaviour, so a pass confirms the bug. dynamic-rollout only:
// a dynamic route on a gateway with hot reload off, three replicas, so every
// route change rolls the pods.
//
// TBM fences through VerifyTransition, which waits only for the Deployment's
// rollout to complete (WaitForGatewayReady) and never for the replaced pods to
// be gone (unlike AAO, whose detection-on branch calls WaitForGatewayPods).
// With detectUnroutedProducersDuration set, verify_fence takes its first source
// offset snapshot the moment that wait returns. The replaced pods are still
// serving, and still accept connections for part of their grace period, so
// producers routed through the gateway keep landing on the source. The second
// snapshot sees offsets rising, so kcp reports an unrouted producer, rolls the
// fence back and exits non-zero. Every producer here goes through the gateway:
// there is no unrouted producer, so the abort is false. A re-run starts from a
// rolled-back (itself rolling) gateway and can hit the same race.
//
// When the fence wait also waits for the replaced pods to be gone, this test
// flips to assert the migration completes. Slice tbm-topic-001..004.
func TestFenceRollout_TBMDetectionAbortsOnReplacedPod(t *testing.T) {
	e := newEnv()
	if e.mode != "dynamic-rollout" {
		t.Skip("dynamic-rollout only: needs a dynamic route on a hot-reload-off gateway whose pods roll (GATEWAY_MODE=dynamic-rollout)")
	}
	ctx := context.Background()
	topics := e.topicRange(1, falseAbortTopics)
	mani := e.renderManifest(t, "false-abort", topics, "",
		"    policies:\n      detectUnroutedProducersDuration: 30s\n")
	e.saveManifest(t, mani)

	// Only the settled pods may be left, so the producers connect to pods the
	// fence will replace.
	oldPods := e.waitForGatewayPodCount(t, ctx, 3)

	var producers []*gatewayProducer
	defer func() {
		for _, p := range producers {
			p.stop()
		}
	}()
	for i := 0; i < falseAbortProducers; i++ {
		producers = append(producers, startSNIGatewayProducer(sniBootstrap, e.gatewayBootstrap(),
			topics[i%len(topics)], time.Second, fmt.Sprintf("false-abort-p%02d", i)))
	}
	allWriting := func() bool {
		for _, p := range producers {
			if len(p.acked()) < 3 {
				return false
			}
		}
		return true
	}
	deadline := time.Now().Add(time.Minute)
	for !allWriting() && time.Now().Before(deadline) {
		time.Sleep(time.Second)
	}
	if !allWriting() {
		var b strings.Builder
		for i, p := range producers {
			fmt.Fprintf(&b, "  producer %02d: %d acked, %d failed sends, first error: %v\n", i, len(p.acked()), p.errorCount(), p.firstError())
		}
		t.Fatalf("every producer must be writing through the gateway before the migration:\n%s", b.String())
	}
	e.snapshot(t, ctx, "before", "BEFORE (expect mirrors ACTIVE, route → source, producers writing through the gateway)", topics)

	w := e.watchPromoteAndPods(topics[0], oldPods)
	start := time.Now()
	out, err := e.runKCP(t, "kcp-run.log", "", "migration", "cutover", "execute", "--migration-yaml", mani)
	kcpDone := time.Now()
	acked := 0
	for _, p := range producers {
		acked += len(p.stop())
	}
	_, oldGoneAt := w.stop()
	e.snapshot(t, ctx, "after", "AFTER the aborted run (expect mirrors ACTIVE, fence removed)", topics)

	timeline := fmt.Sprintf("kcp ran %s, exit error: %v; pre-fence pods gone at %s (kcp started %s, finished %s); %d records acked through the gateway by %d producers",
		kcpDone.Sub(start).Round(time.Second), err, formatAt(oldGoneAt), start.Format(time.RFC3339), kcpDone.Format(time.RFC3339), acked, falseAbortProducers)
	e.saveReport(t, "false-abort.txt", timeline+"\n")
	t.Logf("\n🔍 DATA PLANE ▸ %s", timeline)

	require.Positive(t, acked, "the producers must have written through the gateway")
	require.Errorf(t, err, "today the migration aborts (%s)", timeline)
	require.Contains(t, out, "Unrouted producers detected", "the abort must be the unrouted-producer detector's")
	for _, ms := range e.mirrorStatus(t, ctx) {
		require.NotEqual(t, "STOPPED", ms, "the abort is before promote: no mirror may be stopped")
	}
	require.False(t, strings.Contains(strings.ToLower(out), "migration complete"), "the run must not report completion")
	t.Logf("\n✅ RESULT: FALSE ABORT REPRODUCED — kcp reported an unrouted producer and rolled the fence back, but all %d producers write through the gateway. "+
		"The replaced pods were still serving when the first source-offset snapshot was taken.", falseAbortProducers)
}

func formatAt(at time.Time) string {
	if at.IsZero() {
		return "never"
	}
	return at.Format(time.RFC3339)
}
