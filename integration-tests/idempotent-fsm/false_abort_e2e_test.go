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

// TestFenceRollout_TBMDetectionPassesWithReplacedPods guards against a false
// abort. dynamic-rollout only: a dynamic route on a gateway with hot reload
// off, three replicas, so every route change rolls the pods.
//
// With detectUnroutedProducersDuration set, verify_fence takes its first source
// offset snapshot right after the fence step. The fence step waits for the
// replaced pods to be gone, so no pod still serving the unfenced route can let
// a producer land on the source. Every producer here goes through the gateway,
// so there is no unrouted producer: the migration must complete, with no
// "Unrouted producers detected" and every mirror STOPPED. (Before the fix the
// fence waited only for the Deployment rollout, the replaced pods were still
// serving at the first snapshot, and kcp falsely aborted and rolled the fence
// back.) Slice tbm-topic-001..004.
func TestFenceRollout_TBMDetectionPassesWithReplacedPods(t *testing.T) {
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
	e.snapshot(t, ctx, "after", "AFTER the migration (expect mirrors STOPPED, route → destination)", topics)

	timeline := fmt.Sprintf("kcp ran %s, exit error: %v; pre-fence pods gone at %s (kcp started %s, finished %s); %d records acked through the gateway by %d producers",
		kcpDone.Sub(start).Round(time.Second), err, formatAt(oldGoneAt), start.Format(time.RFC3339), kcpDone.Format(time.RFC3339), acked, falseAbortProducers)
	e.saveReport(t, "false-abort.txt", timeline+"\n")
	t.Logf("\nDATA PLANE ▸ %s", timeline)

	require.Positive(t, acked, "the producers must have written through the gateway")
	require.NoErrorf(t, err, "the migration must complete (%s)", timeline)
	require.NotContains(t, out, "Unrouted producers detected", "no producer is unrouted: every one goes through the gateway")
	require.Contains(t, strings.ToLower(out), "migration complete")
	status := e.mirrorStatus(t, ctx)
	for _, topic := range topics {
		require.Equalf(t, "STOPPED", status[topic], "the migration completed: the mirror of %s must be stopped", topic)
	}
	t.Logf("\nRESULT: the migration completed with detection on and %d producers writing through the gateway: no unrouted producer was reported "+
		"and every mirror is STOPPED.", falseAbortProducers)
}

func formatAt(at time.Time) string {
	if at.IsZero() {
		return "never"
	}
	return at.Format(time.RFC3339)
}
