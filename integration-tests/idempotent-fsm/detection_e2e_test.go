//go:build e2e

package idempotent_fsm_e2e

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/IBM/sarama"
	"github.com/stretchr/testify/require"
)

// Unrouted producers on a MID-BATCH resume. promoteBatchSize 2 and a kill right
// after the first promote request is accepted leave 2 topics promoted and 3
// ACTIVE, with the route fenced. A producer then writes straight to the source
// of the 2 PROMOTED topics, bypassing the gateway. Their mirrors no longer copy
// from the source, so those writes would never reach the target — the resume's
// fence check must watch them and catch it. Part of the batch is promoted, so
// the run keeps the fence and fails; with the producer stopped, a plain re-run
// completes the migration. Slice tbm-topic-031..035.
func TestDetection_OnMidBatchResume_WatchesPromotedTopics(t *testing.T) {
	e := newEnv()
	ctx := context.Background()
	e.resetStaticRoute(t, ctx)
	topics := e.topicRange(31, 35)
	mani := e.renderManifest(t, "detect-mid-batch", topics, "",
		fmt.Sprintf("  defaultPolicies:\n    promoteBatchSize: %d\n    detectUnroutedProducersDuration: 10s\n", midBatchPromoteSize))
	e.saveManifest(t, mani)
	e.snapshot(t, ctx, "before", "BEFORE (expect mirrors ACTIVE, route → source)", topics)

	out1, err1 := e.runKCP(t, "kcp-run-1-interrupted.log", cpPromoteAccepted, "migration", "execute", "--migration-yaml", mani)
	require.Error(t, err1, "the run interrupted mid-batch must exit non-zero")
	require.Contains(t, out1, "kill-point", "the non-zero exit must be the kill point firing, not a real failure")

	promoted := e.waitForPromoted(t, ctx, topics, midBatchPromoteSize)
	e.snapshot(t, ctx, "after-interrupt", "AFTER the mid-batch interrupt (expect fenced, 2 STOPPED, 3 ACTIVE)", topics)

	stop := e.produceToSource(t, promoted)
	out2, err2 := e.runKCP(t, "kcp-run-2-resume-detects.log", "", "migration", "execute", "--migration-yaml", mani)
	sent := stop()
	require.Positive(t, sent, "the producer must have written to the promoted topics' source")
	require.Error(t, err2, "the resume must fail: a producer is writing straight to a promoted topic's source")
	require.Contains(t, out2, "Unrouted producers detected — keeping the fence")
	require.NotContains(t, out2, "removing fence")

	e.snapshot(t, ctx, "after-detection", "AFTER the resume detects the producer (expect still fenced, route → source)", topics)
	cr := e.readCR(t, ctx)
	for _, tp := range topics {
		require.Truef(t, e.isFenced(t, cr, tp), "%s must still be fenced", tp)
		require.Falsef(t, e.isSwitchedToTarget(t, cr, tp), "%s must not be switched", tp)
	}

	out3, err3 := e.runKCP(t, "kcp-run-3-resume.log", "", "migration", "execute", "--migration-yaml", mani)
	require.NoError(t, err3, "with the producer stopped, a plain re-run must complete the migration")
	require.Contains(t, strings.ToLower(out3), "migration complete")

	e.snapshot(t, ctx, "after-resume", "AFTER the re-run (expect mirrors STOPPED, route → destination, fence cleared)", topics)
	cr = e.readCR(t, ctx)
	ms := e.mirrorStatus(t, ctx)
	for _, tp := range topics {
		require.Equalf(t, "STOPPED", ms[tp], "%s must be STOPPED after the migration completes", tp)
		require.Truef(t, e.isSwitchedToTarget(t, cr, tp), "%s must be switched to the target domain", tp)
		require.Falsef(t, e.isFenced(t, cr, tp), "%s must not be fenced after completion", tp)
	}
	t.Logf("\n✅ RESULT: the resume caught a producer writing straight to promoted topics (%d records), kept the fence, and a re-run completed.", sent)
}

// waitForPromoted waits until exactly n of topics are STOPPED (a promote
// request accepted just before a kill reaches STOPPED shortly after) and
// returns them.
func (e *env) waitForPromoted(t *testing.T, ctx context.Context, topics []string, n int) []string {
	t.Helper()
	var stopped []string
	require.Eventuallyf(t, func() bool {
		ms := e.mirrorStatus(t, ctx)
		stopped = stopped[:0]
		for _, tp := range topics {
			if ms[tp] == "STOPPED" {
				stopped = append(stopped, tp)
			}
		}
		return len(stopped) == n
	}, 2*time.Minute, 2*time.Second, "exactly %d of %v must reach STOPPED after the mid-batch interrupt", n, topics)
	return append([]string(nil), stopped...)
}

// produceToSource writes to topics directly on the source cluster, bypassing
// the gateway — a producer the fence does not stop — one record per topic
// every 200ms until the returned stop is called. stop reports how many records
// were written.
func (e *env) produceToSource(t *testing.T, topics []string) (stop func() int) {
	t.Helper()
	cfg := sarama.NewConfig()
	cfg.Producer.Return.Successes = true
	producer, err := sarama.NewSyncProducer([]string{e.sourceBootstrap}, cfg)
	require.NoError(t, err, "connect a producer directly to the source cluster")

	done := make(chan struct{})
	var wg sync.WaitGroup
	var sent int64
	wg.Add(1)
	go func() {
		defer wg.Done()
		tick := time.NewTicker(200 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-done:
				return
			case <-tick.C:
				for _, tp := range topics {
					if _, _, err := producer.SendMessage(&sarama.ProducerMessage{Topic: tp, Value: sarama.StringEncoder("written directly to the source")}); err == nil {
						atomic.AddInt64(&sent, 1)
					}
				}
			}
		}
	}()
	return func() int {
		close(done)
		wg.Wait()
		_ = producer.Close()
		return int(atomic.LoadInt64(&sent))
	}
}
