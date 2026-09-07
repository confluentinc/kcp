//go:build e2e

// Fence verification at the data plane.
//
// The rest of this suite proves a transition's configId reached every gateway
// pod. This file asks the question that actually matters to kcp's rogue-producer
// detection: once WaitForGatewayConfigID has returned, can a producer still get
// a write acknowledged at the source?
//
// detectUnroutedProducers takes its first offset snapshot immediately after that
// wait returns and aborts the migration — unfencing and rolling back — if the
// source's log end offset moves afterwards. It has no tolerance: a single
// message on any partition trips it. So if the fence is not fully settled at the
// instant the wait returns, a write still in flight lands after snapshot 1 and
// the migration is rolled back for a rogue producer that does not exist.
//
// That is the false positive this file exists to detect. Note the reference
// point is when the wait RETURNS, not when the gateway actually converged: the
// wait polls on an interval, so it returns somewhat late, and that lateness is
// part of kcp's real behaviour rather than a measurement artefact. It is exactly
// the instant kcp starts trusting the fence.

package hotreload

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/IBM/sarama"
	"github.com/confluentinc/kcp/internal/services/gateway"
	"github.com/confluentinc/kcp/internal/services/offset"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	// Produce for this long before fencing, to prove the data path works at all.
	// A fence test against a producer that was never landing writes is vacuous.
	warmupWindow = 5 * time.Second
	// Keep producing this long after the wait returns. Generous enough that a
	// straggler has every chance to land and be caught.
	settleWindow = 15 * time.Second
	// Applied only after a failed send, so the loop does not spin hot once the
	// fence is up. Successful sends are not throttled.
	produceBackoff = 100 * time.Millisecond
)

// dataPlane holds the addresses setup.sh provisioned.
type dataPlane struct {
	sourceBootstrap  string
	gatewayBootstrap string
	topic            string
}

func newDataPlane(t *testing.T) dataPlane {
	t.Helper()

	d := dataPlane{
		sourceBootstrap:  os.Getenv("KCP_HR_SOURCE_BOOTSTRAP"),
		gatewayBootstrap: os.Getenv("KCP_HR_GATEWAY_BOOTSTRAP"),
		topic:            os.Getenv("KCP_HR_TOPIC"),
	}
	require.NotEmpty(t, d.sourceBootstrap, "KCP_HR_SOURCE_BOOTSTRAP must be set; run setup.sh first")
	require.NotEmpty(t, d.gatewayBootstrap, "KCP_HR_GATEWAY_BOOTSTRAP must be set; run setup.sh first")
	require.NotEmpty(t, d.topic, "KCP_HR_TOPIC must be set; run setup.sh first")
	return d
}

// producerProbe produces continuously through the gateway and records when the
// last write was acknowledged. The ack time is taken when SendMessage returns,
// which is the observable moment a write is known to have reached the source.
type producerProbe struct {
	producer sarama.SyncProducer

	mu       sync.Mutex
	lastAck  time.Time
	acks     int
	failures int

	stopOnce sync.Once
	stop     chan struct{}
	done     chan struct{}
}

func startProducerProbe(t *testing.T, bootstrap, topic string) *producerProbe {
	t.Helper()

	cfg := sarama.NewConfig()
	cfg.Version = sarama.V3_6_0_0
	cfg.ClientID = "kcp-hr-fence-probe"
	cfg.Producer.Return.Successes = true
	cfg.Producer.RequiredAcks = sarama.WaitForAll
	// Retries are disabled everywhere on purpose. A retry would let sarama
	// re-send across the fence boundary, which is precisely the boundary being
	// measured — a resend landing late would be indistinguishable from the
	// gateway leaking a write after convergence.
	cfg.Producer.Retry.Max = 0
	cfg.Metadata.Retry.Max = 0
	cfg.Producer.Timeout = 3 * time.Second
	cfg.Metadata.Full = false
	cfg.Net.DialTimeout = 3 * time.Second
	cfg.Net.ReadTimeout = 3 * time.Second
	cfg.Net.WriteTimeout = 3 * time.Second

	p, err := sarama.NewSyncProducer([]string{bootstrap}, cfg)
	require.NoError(t, err, "creating a producer against the gateway at %s", bootstrap)

	probe := &producerProbe{
		producer: p,
		stop:     make(chan struct{}),
		done:     make(chan struct{}),
	}
	go probe.run(topic)
	return probe
}

func (p *producerProbe) run(topic string) {
	defer close(p.done)

	for {
		select {
		case <-p.stop:
			return
		default:
		}

		_, _, err := p.producer.SendMessage(&sarama.ProducerMessage{
			Topic: topic,
			Value: sarama.StringEncoder(time.Now().UTC().Format(time.RFC3339Nano)),
		})

		p.mu.Lock()
		if err == nil {
			p.acks++
			p.lastAck = time.Now()
		} else {
			p.failures++
		}
		p.mu.Unlock()

		if err != nil {
			select {
			case <-p.stop:
				return
			case <-time.After(produceBackoff):
			}
		}
	}
}

func (p *producerProbe) snapshot() (lastAck time.Time, acks, failures int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.lastAck, p.acks, p.failures
}

func (p *producerProbe) stopAndWait() {
	p.stopOnce.Do(func() {
		close(p.stop)
		<-p.done
		_ = p.producer.Close()
	})
}

// newSourceOffsets reads offsets with kcp's own offset.Service, over a client
// pointed straight at the broker rather than through the gateway. The fence
// would block a gateway-side read too, and the question is what actually
// reached the source, not what the gateway is willing to say about it.
func newSourceOffsets(t *testing.T, bootstrap string) *offset.Service {
	t.Helper()

	cfg := sarama.NewConfig()
	cfg.Version = sarama.V3_6_0_0
	cfg.ClientID = "kcp-hr-offset-probe"
	cfg.Metadata.Full = false

	c, err := sarama.NewClient([]string{bootstrap}, cfg)
	require.NoError(t, err, "connecting to the source broker at %s", bootstrap)

	svc := offset.NewOffsetService(c)
	t.Cleanup(func() { _ = svc.Close() })
	return svc
}

// logEndOffset sums the LEO across the topic's partitions, which is the same
// quantity detectUnroutedProducers compares between its two snapshots.
func logEndOffset(t *testing.T, ctx context.Context, svc *offset.Service, topic string) int64 {
	t.Helper()

	got, err := svc.GetMany(ctx, []string{topic})
	require.NoError(t, err)

	var total int64
	for _, o := range got[topic] {
		total += o
	}
	return total
}

// applyAndConverge applies a CR under a fresh configId and blocks until every
// pod reports it, mirroring what the migration workflow does at each transition.
func (e *env) applyAndConverge(t *testing.T, ctx context.Context, crYAML []byte) {
	t.Helper()

	configID, err := gateway.NewConfigID()
	require.NoError(t, err)

	_, err = e.svc.ApplyGatewayYAML(ctx, e.namespace, e.gateway, crYAML, configID)
	require.NoError(t, err)
	require.NoError(t, e.svc.WaitForGatewayAccepted(ctx, e.namespace, e.gateway, pollInterval, convergeTimeout))
	require.NoError(t, e.svc.WaitForGatewayConfigID(ctx, e.namespace, e.gateway, gateway.ConfigWaitOptions{
		ConfigID:         configID,
		Port:             gateway.DefaultGatewayConfigPort,
		PollInterval:     pollInterval,
		HotReloadTimeout: convergeTimeout,
	}))
}

// fenceObservation is one measured fence transition.
type fenceObservation struct {
	converged      time.Time
	lastAck        time.Time
	acksAtConverge int
	acksTotal      int
	failuresDuring int
	leoAtConverge  int64
	leoFinal       int64
}

// settle is how long after the wait returned the last write was acknowledged.
// Negative means every write had already landed by the time kcp trusted the
// fence, which is the outcome detectUnroutedProducers assumes.
func (o fenceObservation) settle() time.Duration {
	return o.lastAck.Sub(o.converged)
}

// observeFence runs one full cycle: unfenced warm-up, fence, wait for the
// configId on every pod, then keep producing past it. It leaves the gateway
// fenced; the caller restores.
func (e *env) observeFence(t *testing.T, ctx context.Context, d dataPlane, offsets *offset.Service) fenceObservation {
	t.Helper()

	leoStart := logEndOffset(t, ctx, offsets, d.topic)

	probe := startProducerProbe(t, d.gatewayBootstrap, d.topic)
	defer probe.stopAndWait()

	time.Sleep(warmupWindow)

	_, acksWarm, _ := probe.snapshot()
	require.Greater(t, acksWarm, 0,
		"no write was acknowledged through the gateway before fencing — the data path is broken, "+
			"so anything this test concludes about the fence would be meaningless")
	require.Greater(t, logEndOffset(t, ctx, offsets, d.topic), leoStart,
		"the source log end offset did not move while writes were being acknowledged; "+
			"the producer may be reaching a different cluster than the one being sampled")

	e.applyAndConverge(t, ctx, mustReadFile(t, "KCP_HR_FENCED_CR"))

	converged := time.Now()
	_, acksAtConverge, failuresAtConverge := probe.snapshot()
	leoAtConverge := logEndOffset(t, ctx, offsets, d.topic)

	time.Sleep(settleWindow)

	lastAck, acksTotal, failuresTotal := probe.snapshot()
	leoFinal := logEndOffset(t, ctx, offsets, d.topic)

	return fenceObservation{
		converged:      converged,
		lastAck:        lastAck,
		acksAtConverge: acksAtConverge,
		acksTotal:      acksTotal,
		failuresDuring: failuresTotal - failuresAtConverge,
		leoAtConverge:  leoAtConverge,
		leoFinal:       leoFinal,
	}
}

// TestFenceStopsSourceWritesOnceConfigIDConverges is the invariant kcp's
// rogue-producer detection rests on. It is deliberately a boolean, not a
// measurement: timing numbers belong in the opt-in sweep below, where a slow
// machine cannot turn them into a failure.
func TestFenceStopsSourceWritesOnceConfigIDConverges(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	d := newDataPlane(t)

	// Start unfenced whatever the previous test left behind, and hand the
	// cluster back unfenced so the ordering of this file against the others
	// cannot matter.
	e.applyAndConverge(t, ctx, mustReadFile(t, "KCP_HR_INITIAL_CR"))
	t.Cleanup(func() {
		e.applyAndConverge(t, context.Background(), mustReadFile(t, "KCP_HR_INITIAL_CR"))
	})

	offsets := newSourceOffsets(t, d.sourceBootstrap)
	obs := e.observeFence(t, ctx, d, offsets)

	// Non-vacuity. "No writes landed" proves nothing if the producer had given
	// up: a dead producer produces the same evidence as a perfect fence.
	require.Greater(t, obs.failuresDuring, 0,
		"the producer recorded no failed sends during the settle window, so it was not actually "+
			"attempting writes — 'no writes landed after convergence' is vacuous here")

	// The assertion detectUnroutedProducers depends on.
	if obs.lastAck.After(obs.converged) {
		t.Errorf("a write was acknowledged %s AFTER WaitForGatewayConfigID returned.\n"+
			"detectUnroutedProducers takes its first offset snapshot at that instant and aborts the "+
			"migration if the offset moves afterwards, so this window is a false rogue-producer abort. "+
			"A settle delay is needed between the fence converging and snapshot 1.",
			obs.settle().Round(time.Millisecond))
	}

	assert.Equal(t, obs.leoAtConverge, obs.leoFinal,
		"the source log end offset moved after the fence converged")
	assert.Equal(t, obs.acksAtConverge, obs.acksTotal,
		"the producer had writes acknowledged after the fence converged")

	t.Logf("fence settled %s before the wait returned; %d acks, %d rejected sends during the settle window",
		(-obs.settle()).Round(time.Millisecond), obs.acksTotal, obs.failuresDuring)
}

// TestSettleWindowSweep measures the margin rather than asserting on it. Opt-in
// because it is slow and because its output is a number, not a pass/fail: run it
// on a quiet machine to size any settle delay, then encode the result as a
// constant rather than leaving a timing measurement in the assertion path.
func TestSettleWindowSweep(t *testing.T) {
	iterations := 0
	if v := os.Getenv("KCP_HR_SETTLE_ITERATIONS"); v != "" {
		if _, err := fmt.Sscanf(v, "%d", &iterations); err != nil {
			t.Fatalf("bad KCP_HR_SETTLE_ITERATIONS: %v", err)
		}
	}
	if iterations <= 0 {
		t.Skip("set KCP_HR_SETTLE_ITERATIONS=N to measure the fence settle margin")
	}

	ctx := context.Background()
	e := newEnv(t)
	d := newDataPlane(t)
	offsets := newSourceOffsets(t, d.sourceBootstrap)

	t.Cleanup(func() {
		e.applyAndConverge(t, context.Background(), mustReadFile(t, "KCP_HR_INITIAL_CR"))
	})

	worst := time.Duration(-1 << 62)
	for i := range iterations {
		e.applyAndConverge(t, ctx, mustReadFile(t, "KCP_HR_INITIAL_CR"))

		obs := e.observeFence(t, ctx, d, offsets)
		if obs.settle() > worst {
			worst = obs.settle()
		}
		t.Logf("iteration %d/%d: last ack %s relative to the wait returning (negative is margin)",
			i+1, iterations, obs.settle().Round(time.Millisecond))
	}

	t.Logf("worst observed: last ack %s relative to the wait returning across %d iterations",
		worst.Round(time.Millisecond), iterations)
}
