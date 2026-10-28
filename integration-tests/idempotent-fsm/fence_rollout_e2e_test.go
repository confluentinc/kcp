//go:build e2e

package idempotent_fsm_e2e

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/IBM/sarama"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/types"
)

// TestFenceRollout_ReplacedPodWritesAfterPromoteAreLost reproduces a suspected
// data-loss bug on a hot-reload-off gateway and asserts TODAY's behaviour, so a
// pass confirms the bug. With unrouted-producer detection off (the default),
// the AAO fence step waits only for the Deployment rollout to complete
// (VerifyTransition → WaitForGatewayReady). The replaced pod, still serving
// the unfenced route, keeps its existing client connections for up to its
// termination grace period, and verify_fence is skipped, so promote can run
// inside that window. A producer connected to the replaced pod keeps writing
// to the source after its mirror has stopped: acked records that never reach
// the destination. When the fence wait also waits for the replaced pods to be
// gone, no record can be acked by the source after promote, and this test
// flips to assert that. Static only. Slice tbm-topic-021..025.
func TestFenceRollout_ReplacedPodWritesAfterPromoteAreLost(t *testing.T) {
	e := newEnv()
	if e.mode != "static" {
		t.Skip("static routes only: a dynamic route hot-reloads, so no pod is replaced")
	}
	ctx := context.Background()
	e.resetStaticRoute(t, ctx)
	topics := e.topicRange(21, 25)
	probe := topics[0]
	mani := e.writeManifest(t, "fence-rollout-replaced-pod", topics)
	e.saveManifest(t, mani)

	// The reset rolls the gateway pods. Wait until only the settled pod is
	// left, so the producer below connects to the pod the fence will replace.
	oldPods := e.waitForSingleGatewayPod(t, ctx)
	p := startGatewayProducer(e.gatewayBootstrap(), probe, time.Second)
	defer p.stop()
	require.Eventuallyf(t, func() bool { return len(p.acked()) >= 3 }, 3*time.Minute, time.Second,
		"the producer must be writing through the gateway before the migration (errors: %d, first: %v)", p.errorCount(), p.firstError())
	e.snapshot(t, ctx, "before", "BEFORE (expect mirrors ACTIVE, route → source, producer writing through the gateway)", topics)

	w := e.watchPromoteAndPods(probe, oldPods)
	out, err := e.runKCP(t, "kcp-run.log", "", "migration", "execute", "--migration-yaml", mani)
	require.NoError(t, err, "the migration must complete")
	require.Contains(t, strings.ToLower(out), "migration complete")
	require.Contains(t, out, "Detection disabled", "the default policy must skip verify_fence")

	require.Eventually(t, func() bool { return !w.oldPodsGoneAt().IsZero() }, 2*time.Minute, time.Second,
		"the replaced gateway pod must be gone")
	acks := p.stop()
	stoppedAt, oldGoneAt := w.stop()
	require.False(t, stoppedAt.IsZero(), "the watcher must have seen the probe topic's mirror STOPPED")
	e.snapshot(t, ctx, "after", "AFTER the migration (expect mirrors STOPPED, route → destination)", topics)

	// A record whose send started after the watcher saw the mirror STOPPED was
	// appended after the mirror stopped. If the source holds it, at the offset
	// it was acked at, no copy of it can reach the destination.
	onSource := e.recordsOnSource(t, probe, acks)
	var lost []ackedRecord
	for _, a := range acks {
		if a.sentAt.After(stoppedAt) && onSource[a.value] {
			lost = append(lost, a)
		}
	}
	timeline := fmt.Sprintf("mirror of %s seen STOPPED at %s; replaced pod gone at %s (%s later); %d records acked in all, %d of them on the source",
		probe, stoppedAt.Format(time.RFC3339Nano), oldGoneAt.Format(time.RFC3339Nano), oldGoneAt.Sub(stoppedAt).Round(time.Second), len(acks), len(onSource))
	var b strings.Builder
	for _, a := range lost {
		fmt.Fprintf(&b, "  %s  sent %s  acked at %s[%d]@%d\n", a.value, a.sentAt.Format(time.RFC3339Nano), probe, a.partition, a.offset)
	}
	e.saveReport(t, "lost-records.txt", timeline+"\n"+b.String())
	t.Logf("\n🔍 DATA PLANE ▸ %s\n%s", timeline, b.String())

	require.NotEmptyf(t, lost, "today a producer on the replaced pod writes to the source after promote (%s)", timeline)
	t.Logf("\n✅ RESULT: BUG REPRODUCED — %d records were acked by the source after the mirror of %s stopped, through the replaced gateway pod "+
		"that the fence step treated as gone; none of them can reach the destination, and the migration reported success.", len(lost), probe)
}

// gatewayBootstrap is the static route's client endpoint, as
// testdata/manifests/templates/gateway-static.yaml declares it.
func (e *env) gatewayBootstrap() string {
	return fmt.Sprintf("%s.%s.svc.cluster.local:9595", e.gateway, e.namespace)
}

// waitForSingleGatewayPod waits until exactly one gateway pod is listed,
// terminating ones included, and returns its UID.
func (e *env) waitForSingleGatewayPod(t *testing.T, ctx context.Context) map[types.UID]struct{} {
	t.Helper()
	var uids map[types.UID]struct{}
	require.Eventually(t, func() bool {
		var err error
		uids, err = e.svc.GetGatewayPodUIDs(ctx, e.namespace, e.gateway)
		return err == nil && len(uids) == 1
	}, 3*time.Minute, 2*time.Second, "exactly one gateway pod must be left once the reset's rollout settles")
	return uids
}

// ackedRecord is one record the gateway acknowledged: its unique value, when
// its send started, and where it was acked.
type ackedRecord struct {
	value     string
	sentAt    time.Time
	partition int32
	offset    int64
}

// gatewayProducer writes one uniquely-valued record to a topic through the
// gateway every interval, on one long-lived client, so its connection stays
// on whichever pod it first reached.
type gatewayProducer struct {
	done     chan struct{}
	wg       sync.WaitGroup
	once     sync.Once
	mu       sync.Mutex
	acks     []ackedRecord
	errs     int
	firstErr error
}

// startGatewayProducer starts the producer. A client that can't be created
// counts as a failed send, retried on the next tick. No retries, so a send is
// one attempt against whatever the gateway serves right now.
func startGatewayProducer(bootstrap, topic string, interval time.Duration) *gatewayProducer {
	p := &gatewayProducer{done: make(chan struct{})}
	cfg := sarama.NewConfig()
	cfg.Version = sarama.V3_6_0_0
	cfg.ClientID = "kcp-fence-rollout-probe"
	cfg.Producer.Return.Successes = true
	cfg.Producer.RequiredAcks = sarama.WaitForAll
	cfg.Producer.Retry.Max = 0
	cfg.Metadata.Retry.Max = 0
	cfg.Producer.Timeout = 3 * time.Second
	cfg.Metadata.Full = false
	cfg.Net.DialTimeout = 3 * time.Second
	cfg.Net.ReadTimeout = 3 * time.Second
	cfg.Net.WriteTimeout = 3 * time.Second

	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		var producer sarama.SyncProducer
		defer func() {
			if producer != nil {
				_ = producer.Close()
			}
		}()
		tick := time.NewTicker(interval)
		defer tick.Stop()
		for seq := 0; ; seq++ {
			select {
			case <-p.done:
				return
			case <-tick.C:
			}
			if producer == nil {
				var err error
				if producer, err = sarama.NewSyncProducer([]string{bootstrap}, cfg); err != nil {
					p.fail(err)
					continue
				}
			}
			value := fmt.Sprintf("fence-rollout-%06d", seq)
			sentAt := time.Now()
			partition, offset, err := producer.SendMessage(&sarama.ProducerMessage{Topic: topic, Value: sarama.StringEncoder(value)})
			if err != nil {
				p.fail(err)
				continue
			}
			p.mu.Lock()
			p.acks = append(p.acks, ackedRecord{value: value, sentAt: sentAt, partition: partition, offset: offset})
			p.mu.Unlock()
		}
	}()
	return p
}

func (p *gatewayProducer) fail(err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.errs++
	if p.firstErr == nil {
		p.firstErr = err
	}
}

func (p *gatewayProducer) acked() []ackedRecord {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]ackedRecord(nil), p.acks...)
}

func (p *gatewayProducer) errorCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.errs
}

func (p *gatewayProducer) firstError() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.firstErr
}

// stop stops the producer, once, and returns every acked record.
func (p *gatewayProducer) stop() []ackedRecord {
	p.once.Do(func() {
		close(p.done)
		p.wg.Wait()
	})
	return p.acked()
}

// promoteWatcher polls, every second, the probe topic's mirror status and
// whether any of the pre-fence gateway pods is still listed, and records the
// first time it saw the mirror STOPPED and the pods gone. Read errors are
// skipped: the next poll retries.
type promoteWatcher struct {
	done      chan struct{}
	wg        sync.WaitGroup
	once      sync.Once
	mu        sync.Mutex
	stoppedAt time.Time
	goneAt    time.Time
}

func (e *env) watchPromoteAndPods(topic string, oldPods map[types.UID]struct{}) *promoteWatcher {
	w := &promoteWatcher{done: make(chan struct{})}
	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		ctx := context.Background()
		tick := time.NewTicker(time.Second)
		defer tick.Stop()
		for {
			select {
			case <-w.done:
				return
			case <-tick.C:
			}
			now := time.Now()
			if mirrors, err := e.linkSvc.ListMirrorTopics(ctx, e.linkConfig()); err == nil {
				for _, m := range mirrors {
					if m.MirrorTopicName == topic && m.MirrorStatus == "STOPPED" {
						w.mark(&w.stoppedAt, now)
					}
				}
			}
			if uids, err := e.svc.GetGatewayPodUIDs(ctx, e.namespace, e.gateway); err == nil {
				left := false
				for uid := range oldPods {
					if _, ok := uids[uid]; ok {
						left = true
					}
				}
				if !left {
					w.mark(&w.goneAt, now)
				}
			}
		}
	}()
	return w
}

func (w *promoteWatcher) mark(at *time.Time, now time.Time) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if at.IsZero() {
		*at = now
	}
}

func (w *promoteWatcher) oldPodsGoneAt() time.Time {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.goneAt
}

// stop stops the watcher and returns when it first saw the mirror STOPPED and
// the old pods gone (zero if never).
func (w *promoteWatcher) stop() (stoppedAt, goneAt time.Time) {
	w.once.Do(func() {
		close(w.done)
		w.wg.Wait()
	})
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.stoppedAt, w.goneAt
}

// recordsOnSource reads, straight from the source cluster, the record at each
// acked position of topic and returns the acked values the source holds there.
// A record acked by the destination after the switch isn't on the source at
// its acked position, so it's left out.
func (e *env) recordsOnSource(t *testing.T, topic string, acks []ackedRecord) map[string]bool {
	t.Helper()
	client, err := sarama.NewClient([]string{e.sourceBootstrap}, sarama.NewConfig())
	require.NoError(t, err, "connect a client directly to the source cluster")
	defer func() { _ = client.Close() }()
	consumer, err := sarama.NewConsumerFromClient(client)
	require.NoError(t, err, "consumer on the source client")
	defer func() { _ = consumer.Close() }()

	byPartition := map[int32][]ackedRecord{}
	for _, a := range acks {
		byPartition[a.partition] = append(byPartition[a.partition], a)
	}
	found := map[string]bool{}
	for partition, as := range byPartition {
		end, err := client.GetOffset(topic, partition, sarama.OffsetNewest)
		require.NoErrorf(t, err, "read the end offset of %s[%d] on the source", topic, partition)
		pc, err := consumer.ConsumePartition(topic, partition, sarama.OffsetOldest)
		require.NoErrorf(t, err, "consume %s[%d] from the source", topic, partition)
		at := map[int64]string{}
	read:
		for end > 0 {
			select {
			case m := <-pc.Messages():
				at[m.Offset] = string(m.Value)
				if m.Offset >= end-1 {
					break read
				}
			case <-time.After(30 * time.Second):
				t.Fatalf("timed out reading %s[%d] from the source up to offset %d", topic, partition, end)
			}
		}
		_ = pc.Close()
		for _, a := range as {
			if at[a.offset] == a.value {
				found[a.value] = true
			}
		}
	}
	return found
}
