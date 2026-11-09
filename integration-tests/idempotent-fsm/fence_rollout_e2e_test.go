//go:build e2e

package idempotent_fsm_e2e

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/IBM/sarama"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/types"
)

// TestFenceRollout_ReplacedPodIsGoneBeforePromote guards the fence step on a
// hot-reload-off gateway. With unrouted-producer detection off (the default),
// verify_fence is skipped, but the fence step still waits for the replaced
// gateway pods to be gone before promote can run. A producer connected to the
// replaced pod is therefore cut off and fenced before the mirror stops, so no
// record is acked by the source after promote. (Before the fix the wait ran
// only when detection was on, and a producer on the replaced pod kept writing
// to the source after its mirror stopped: acked records that never reached the
// destination.) Static only. Slice tbm-topic-021..025.
func TestFenceRollout_ReplacedPodIsGoneBeforePromote(t *testing.T) {
	e := newEnv()
	if e.mode != "static" {
		t.Skip("static routes only: a dynamic route hot-reloads, so no pod is replaced")
	}
	r := e.migrateWithProducerOnReplacedPod(t, "fence-rollout-replaced-pod", e.topicRange(21, 25), "")
	require.Contains(t, r.out, "Detection disabled", "the default policy must skip verify_fence")
	require.Falsef(t, r.stoppedAt.Before(r.oldGoneAt), "the replaced pod must be gone before the mirror stops (%s)", r.timeline)
	require.Positive(t, r.errors, "the fence must reject the producer once its connection to the replaced pod is cut")
	require.Emptyf(t, r.lost, "no record may reach the source after the mirror stops (%s)", r.timeline)
	t.Logf("\nRESULT: with detection off, the replaced pod was gone %s before the mirror of %s stopped, the producer was fenced (%d failed sends), "+
		"and no record reached the source after promote.", r.stoppedAt.Sub(r.oldGoneAt).Round(time.Second), r.probe, r.errors)
}

// TestFenceRollout_DetectionOnWaitsForReplacedPod is the detection-on variant
// of TestFenceRollout_ReplacedPodIsGoneBeforePromote: the same migration and
// producer, with unrouted-producer detection on. The fence step waits for the
// replaced pods to be gone (as it does with detection off), so the producer's
// connection to the unfenced pod is cut before promote, verify_fence finds no
// unrouted producer, and no record reaches the source after the mirror stops.
// Static only. Slice tbm-topic-026..030.
func TestFenceRollout_DetectionOnWaitsForReplacedPod(t *testing.T) {
	e := newEnv()
	if e.mode != "static" {
		t.Skip("static routes only: a dynamic route hot-reloads, so no pod is replaced")
	}
	r := e.migrateWithProducerOnReplacedPod(t, "fence-rollout-detection-on", e.topicRange(26, 30),
		"    policies:\n      detectUnroutedProducersDuration: 10s\n")
	require.Contains(t, r.out, "no unrouted producers detected")
	require.Falsef(t, r.stoppedAt.Before(r.oldGoneAt), "the replaced pod must be gone before the mirror stops (%s)", r.timeline)
	require.Positive(t, r.errors, "the fence must reject the producer once its connection to the replaced pod is cut")
	require.Emptyf(t, r.lost, "no record may reach the source after the mirror stops (%s)", r.timeline)
	t.Logf("\nRESULT: with detection on, the replaced pod was gone %s before the mirror of %s stopped, the producer was fenced (%d failed sends), "+
		"and no record reached the source after promote.", r.stoppedAt.Sub(r.oldGoneAt).Round(time.Second), r.probe, r.errors)
}

// replacedPodRun is what migrateWithProducerOnReplacedPod observed.
type replacedPodRun struct {
	out       string        // kcp's output
	probe     string        // the topic the producer wrote to
	stoppedAt time.Time     // when the watcher first saw the probe's mirror STOPPED
	oldGoneAt time.Time     // when the watcher first saw the pre-fence pod gone
	errors    int           // the producer's failed sends
	lost      []ackedRecord // records sent after stoppedAt that the source holds
	timeline  string        // a one-line summary for messages
}

// migrateWithProducerOnReplacedPod runs a full migration of topics while a
// producer writes to the first of them through the gateway, once a second, on
// a connection to the pod the fence will replace. policies is the manifest's
// policies block ("" for the defaults). It waits until the pre-fence pod is
// gone, then reads every acked record back from the source.
func (e *env) migrateWithProducerOnReplacedPod(t *testing.T, name string, topics []string, policies string) replacedPodRun {
	t.Helper()
	ctx := context.Background()
	e.resetStaticRoute(t, ctx)
	probe := topics[0]
	mani := e.renderManifest(t, name, topics, "", policies)
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
	out, err := e.runKCP(t, "kcp-run.log", "", "migration", "cutover", "execute", "--migration-yaml", mani)
	require.NoError(t, err, "the migration must complete")
	require.Contains(t, strings.ToLower(out), "migration complete")

	require.Eventually(t, func() bool { return !w.oldPodsGoneAt().IsZero() }, 2*time.Minute, time.Second,
		"the replaced gateway pod must be gone")
	acks := p.stop()
	stoppedAt, oldGoneAt := w.stop()
	require.False(t, stoppedAt.IsZero(), "the watcher must have seen the probe topic's mirror STOPPED")
	e.snapshot(t, ctx, "after", "AFTER the migration (expect mirrors STOPPED, route → destination)", topics)

	// A record whose send started after the watcher saw the mirror STOPPED was
	// appended after the mirror stopped. stoppedAt can be up to one poll (1s)
	// late, so a record sent in that second is not counted as lost. If the source holds it, at the offset
	// it was acked at, no copy of it can reach the destination.
	onSource := e.recordsOnSource(t, probe, acks)
	r := replacedPodRun{out: out, probe: probe, stoppedAt: stoppedAt, oldGoneAt: oldGoneAt, errors: p.errorCount()}
	for _, a := range acks {
		if a.sentAt.After(stoppedAt) && onSource[a.value] {
			r.lost = append(r.lost, a)
		}
	}
	r.timeline = fmt.Sprintf("mirror of %s seen STOPPED at %s; replaced pod gone at %s; %d records acked in all, %d of them on the source; %d failed sends (first: %v)",
		probe, stoppedAt.Format(time.RFC3339Nano), oldGoneAt.Format(time.RFC3339Nano), len(acks), len(onSource), r.errors, p.firstError())
	var b strings.Builder
	for _, a := range r.lost {
		fmt.Fprintf(&b, "  %s  sent %s  acked at %s[%d]@%d\n", a.value, a.sentAt.Format(time.RFC3339Nano), probe, a.partition, a.offset)
	}
	e.saveReport(t, "lost-records.txt", r.timeline+"\n"+b.String())
	t.Logf("\nDATA PLANE ▸ %s\n%s", r.timeline, b.String())
	return r
}

// waitForSingleGatewayPod waits until exactly one gateway pod is listed,
// terminating ones included, and returns its UID.
func (e *env) waitForSingleGatewayPod(t *testing.T, ctx context.Context) map[types.UID]struct{} {
	t.Helper()
	return e.waitForGatewayPodCount(t, ctx, 1)
}

// waitForGatewayPodCount waits until exactly n gateway pods are listed,
// terminating ones included (so none is still draining), and returns their UIDs.
func (e *env) waitForGatewayPodCount(t *testing.T, ctx context.Context, n int) map[types.UID]struct{} {
	t.Helper()
	var uids map[types.UID]struct{}
	require.Eventuallyf(t, func() bool {
		var err error
		uids, err = e.svc.GetGatewayPodUIDs(ctx, e.namespace, e.gateway)
		return err == nil && len(uids) == n
	}, 3*time.Minute, 2*time.Second, "exactly %d gateway pod(s) must be left once any earlier rollout settles", n)
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

// startGatewayProducer starts the producer against a plain (static route)
// gateway listener. A client that can't be created counts as a failed send,
// retried on the next tick. No retries, so a send is one attempt against
// whatever the gateway serves right now.
func startGatewayProducer(bootstrap, topic string, interval time.Duration) *gatewayProducer {
	return runGatewayProducer(probeConfig(), []string{bootstrap}, topic, interval, "fence-rollout")
}

// startSNIGatewayProducer is startGatewayProducer for a dynamic route, whose
// client listener needs TLS with host (SNI) routing: the bootstrap and every
// broker the gateway advertises are *.gw.local names that don't resolve. The
// client keeps those names for the TLS server name but dials the gateway
// service, whatever the name (sarama takes the SNI from the broker address, then
// wraps the connection the dialer returns). The self-signed certificate is not
// verified. prefix keeps record values unique across producers.
func startSNIGatewayProducer(bootstrap, dialAddr, topic string, interval time.Duration, prefix string) *gatewayProducer {
	cfg := probeConfig()
	cfg.Net.TLS.Enable = true
	cfg.Net.TLS.Config = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // self-signed test certificate
	cfg.Net.Proxy.Enable = true
	cfg.Net.Proxy.Dialer = fixedAddrDialer{addr: dialAddr}
	return runGatewayProducer(cfg, []string{bootstrap}, topic, interval, prefix)
}

// fixedAddrDialer dials addr whatever address it is asked for.
type fixedAddrDialer struct{ addr string }

func (d fixedAddrDialer) Dial(network, _ string) (net.Conn, error) {
	return net.DialTimeout(network, d.addr, 3*time.Second)
}

func probeConfig() *sarama.Config {
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
	return cfg
}

func runGatewayProducer(cfg *sarama.Config, brokers []string, topic string, interval time.Duration, prefix string) *gatewayProducer {
	p := &gatewayProducer{done: make(chan struct{})}
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
				if producer, err = sarama.NewSyncProducer(brokers, cfg); err != nil {
					p.fail(err)
					continue
				}
			}
			value := fmt.Sprintf("%s-%06d", prefix, seq)
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
			// Poll order matters: read the pods BEFORE the mirror status. Both
			// marks share this poll's `now`, so a tie between goneAt and
			// stoppedAt is accepted: the watcher cannot resolve order finer
			// than its 1s poll. kcp's wait guarantees the order by
			// construction, and `r.lost` guards the data plane.
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
			if mirrors, err := e.linkSvc.ListMirrorTopics(ctx, e.linkConfig()); err == nil {
				for _, m := range mirrors {
					if m.MirrorTopicName == topic && m.MirrorStatus == "STOPPED" {
						w.mark(&w.stoppedAt, now)
					}
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
