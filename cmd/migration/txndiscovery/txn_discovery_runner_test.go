package txndiscovery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/IBM/sarama"
	"github.com/confluentinc/kcp/internal/client"
	"github.com/confluentinc/kcp/internal/services/txndiscovery/discovery"
	"github.com/confluentinc/kcp/internal/services/txndiscovery/report"
	"github.com/confluentinc/kcp/internal/services/txndiscovery/tail"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeDescriber stands in for the cluster admin's DescribeTopics.
type fakeDescriber struct {
	md      []*sarama.TopicMetadata
	err     error
	askedes [][]string
}

func (f *fakeDescriber) DescribeTopics(topics []string) ([]*sarama.TopicMetadata, error) {
	f.askedes = append(f.askedes, topics)
	return f.md, f.err
}

func metadataFor(name string, err sarama.KError, partitions int) []*sarama.TopicMetadata {
	md := &sarama.TopicMetadata{Name: name, Err: err, IsInternal: true}
	for i := range partitions {
		md.Partitions = append(md.Partitions, &sarama.PartitionMetadata{ID: int32(i)})
	}
	return []*sarama.TopicMetadata{md}
}

// --- the two-client wiring -------------------------------------------------

// stubSaramaClient answers only what building a cluster admin over it needs. Its
// Close count is what proves no connection is leaked.
type stubSaramaClient struct {
	sarama.Client
	id     int
	closes int
}

func (s *stubSaramaClient) Controller() (*sarama.Broker, error) { return &sarama.Broker{}, nil }
func (s *stubSaramaClient) Config() *sarama.Config              { return sarama.NewConfig() }
func (s *stubSaramaClient) Close() error                        { s.closes++; return nil }

// stubAdmin is a cluster admin that remembers which client it was built over, which
// is the whole question: sarama pipelines per connection and answers in order, so an
// admin sharing the tail's client has its requests queue behind long-poll fetches.
type stubAdmin struct {
	over   *stubSaramaClient
	closes int
}

func (s *stubAdmin) DescribeTopics(topics []string) ([]*sarama.TopicMetadata, error) {
	return metadataFor(topics[0], sarama.ErrNoError, 1), nil
}

func (s *stubAdmin) ListConsumerGroups() (map[string]string, error) { return nil, nil }

func (s *stubAdmin) ListConsumerGroupOffsets(string, map[string][]int32) (*sarama.OffsetFetchResponse, error) {
	return &sarama.OffsetFetchResponse{}, nil
}

// Close releases the client, exactly as sarama's own ClusterAdmin.Close does.
func (s *stubAdmin) Close() error { s.closes++; return s.over.Close() }

// stubConnect records every client and admin one connect built, and the auth options
// each client was built with.
type stubConnect struct {
	clients []*stubSaramaClient
	admins  []*stubAdmin
	auth    [][]client.AdminOption
	failAt  int // 1-based call number the client factory fails on; 0 never fails
}

func (s *stubConnect) newClient(_ []string, _ string, opts ...client.AdminOption) (sarama.Client, error) {
	if s.failAt == len(s.clients)+1 {
		return nil, errors.New("no route to broker")
	}
	s.auth = append(s.auth, opts)
	c := &stubSaramaClient{id: len(s.clients)}
	s.clients = append(s.clients, c)
	return c, nil
}

func (s *stubConnect) newAdmin(c sarama.Client) (clusterAdmin, error) {
	a := &stubAdmin{over: c.(*stubSaramaClient)}
	s.admins = append(s.admins, a)
	return a, nil
}

// The regression this change closes. sarama pipelines requests on one connection and
// delivers responses strictly in order, so an admin built over the client the tail
// holds has every ListConsumerGroups and ListConsumerGroupOffsets queue behind the
// tail's long-poll fetches — measured at 6.3-8.1s and 8.1-30.9s against the compose
// broker, versus 0-2ms on a connection of its own. That turns --interval 5s over a
// 60s window into four or five enrichment passes instead of thirteen.
//
// U7's reason for sharing was to avoid widening kcp's KafkaAdmin interface, and that
// reason is untouched: the narrow ConsumerGroupAdmin interface still stands, and a
// second client satisfies it identically.
func TestConnect_TheEnrichmentAdminDoesNotShareTheTailsConnection(t *testing.T) {
	s := &stubConnect{}
	opts := baseOpts(t.TempDir())
	opts.Auth = plaintextAuth()

	cl, err := connectSaramaWith(opts, s.newClient, s.newAdmin)
	require.NoError(t, err)

	require.Len(t, s.clients, 2, "enrichment needs a connection of its own, so two clients are built")
	require.Len(t, s.admins, 2)

	preflight, ok := cl.Describer.(*stubAdmin)
	require.True(t, ok)
	enrichment, ok := cl.Admin.(*stubAdmin)
	require.True(t, ok)

	assert.Same(t, s.clients[0], preflight.over,
		"the preflight admin rides the tail's client, which is free before the fetch loops start")
	assert.NotSame(t, preflight.over, enrichment.over,
		"the enrichment admin is on the tail's connection, so its calls queue behind the fetches")
	assert.Same(t, s.clients[1], enrichment.over)

	// A second connection is a second authentication, and the two must present
	// identical credentials, TLS settings and --insecure-skip-tls-verify handling. The
	// auth is resolved once and handed to both, which is what this compares: the same
	// closure, not two resolutions that could diverge.
	require.Len(t, s.auth, 2)
	require.Len(t, s.auth[0], 1)
	require.Len(t, s.auth[1], 1)
	assert.Equal(t,
		reflect.ValueOf(s.auth[0][0]).Pointer(), reflect.ValueOf(s.auth[1][0]).Pointer(),
		"the two connections were given separately-resolved auth, so they can drift")
}

// plaintextAuth is the auth resolution --use-unauthenticated-plaintext produces, which
// is what the compose broker in the integration suite speaks.
func plaintextAuth() authResolution {
	return authResolution{
		AuthType: types.AuthTypeUnauthenticatedPlaintext,
		Method:   types.AuthMethodConfig{UnauthenticatedPlaintext: &types.UnauthenticatedPlaintextConfig{Use: true}},
	}
}

// Two connections mean two things to release. A run that closed only the first would
// leak a socket and an authenticated session per invocation.
func TestConnect_CloseReleasesBothConnections(t *testing.T) {
	s := &stubConnect{}

	opts := baseOpts(t.TempDir())
	opts.Auth = plaintextAuth()

	cl, err := connectSaramaWith(opts, s.newClient, s.newAdmin)
	require.NoError(t, err)
	require.NoError(t, cl.Close())

	for i, c := range s.clients {
		assert.Positive(t, c.closes, "client %d was never closed", i)
	}
}

// A second connection is a second authentication to the broker, so it is not opened
// unless something is going to use it.
func TestConnect_EnrichmentDisabledBuildsNoSecondConnection(t *testing.T) {
	s := &stubConnect{}
	opts := baseOpts(t.TempDir())
	opts.Auth = plaintextAuth()
	opts.EnrichConsumerGroups = false

	cl, err := connectSaramaWith(opts, s.newClient, s.newAdmin)
	require.NoError(t, err)

	assert.Len(t, s.clients, 1, "--enrich-consumer-groups=false must not authenticate a second time")
	assert.Nil(t, cl.Admin, "with no enrichment client there is no enrichment admin")

	require.NoError(t, cl.Close())
	assert.Positive(t, s.clients[0].closes)
}

// R13's precedent applied to this phase: an optional enrichment source whose
// connection cannot be opened warns and the run continues. Failing here would make a
// SECOND authenticated connection a hard precondition of a command whose primary job —
// the transaction-state footprints — needs only the first, and would throw the run away
// before it observed anything.
//
// The first connection is still released: a failure part-way through must not leave an
// authenticated session behind.
func TestConnect_EnrichmentConnectionFailureDegradesRatherThanFailingTheRun(t *testing.T) {
	s := &stubConnect{failAt: 2}
	opts := baseOpts(t.TempDir())
	opts.Auth = plaintextAuth()

	cl, err := connectSaramaWith(opts, s.newClient, s.newAdmin)

	require.NoError(t, err, "a second connection is not a precondition of reading the transaction-state log")
	require.NotNil(t, cl)
	assert.Nil(t, cl.Admin, "enrichment has no admin, so the run must not start the phase")
	assert.NotNil(t, cl.Describer, "the preflight and the tail are unaffected")

	require.NoError(t, cl.Close())
	require.Len(t, s.clients, 1)
	assert.Positive(t, s.clients[0].closes, "the first connection leaked when the second failed")
}

// The other half of that degradation: the run must not claim a phase it did not start.
// A run whose enrichment connection failed and which still named consumer-group
// enrichment among its sources would have every artifact report the phase as having
// found nothing rather than as never having run — the exact confusion R13's Unavailable
// flag exists to prevent for the offsets tail.
func TestRun_NoEnrichmentAdminMeansThePhaseIsNotClaimedAsActive(t *testing.T) {
	dir := t.TempDir()
	h := newHarness()
	h.admin = nil // the enrichment connection could not be opened
	h.tailClient.script(discovery.DefaultTxnStateTopic, stateRecordBatch(0,
		[][]byte{txnKey("payments-processor-abc12")},
		[][]byte{txnValue(4242, "payments.out")},
	))

	opts := baseOpts(dir)
	opts.StatsOutPath = filepath.Join(dir, "stats.json")

	require.NoError(t, h.runner(t, opts).Run(context.Background()),
		"an unopenable enrichment connection must not fail the run")

	var doc struct {
		ActiveSources []string `json:"active_sources"`
		Enrichment    struct {
			Passes int `json:"passes"`
		} `json:"consumer_group_enrichment"`
	}
	require.NoError(t, json.Unmarshal([]byte(readFile(t, opts.StatsOutPath)), &doc))

	assert.NotContains(t, doc.ActiveSources, discovery.SourceConsumerGroups,
		"the run claims a phase it never started")
	assert.Contains(t, doc.ActiveSources, discovery.SourceTxnStateLog, "the primary source still ran")
	assert.Zero(t, doc.Enrichment.Passes)
	assert.Contains(t, readFile(t, opts.OutPath), "payments.out", "the footprints are still worth having")
}

// R12: a readable transaction-state topic is the whole precondition of the run,
// so it passes preflight.
func TestPreflight_ReadableTopic_Passes(t *testing.T) {
	d := &fakeDescriber{md: metadataFor("__transaction_state", sarama.ErrNoError, 50)}

	require.NoError(t, probeTxnStateTopic(d, "__transaction_state"))
	assert.Equal(t, [][]string{{"__transaction_state"}}, d.askedes)
}

// R12, abuse case: U1 turned auto-creation off, so a mistyped --txn-state-topic
// now reaches preflight as an unknown topic rather than being created. Reporting
// it as an ACL problem sends the operator to their Kafka administrator for a
// typo they can fix themselves.
func TestPreflight_UnknownTopic_IsNotReportedAsAuthorization(t *testing.T) {
	d := &fakeDescriber{md: metadataFor("_transaction_state", sarama.ErrUnknownTopicOrPartition, 0)}

	err := probeTxnStateTopic(d, "_transaction_state")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not exist")
	assert.Contains(t, err.Error(), "--txn-state-topic")
	assert.NotContains(t, strings.ToLower(err.Error()), "acl")
	assert.NotContains(t, strings.ToLower(err.Error()), "credential")
}

// R12: an authorization failure names the two things that can fix it.
func TestPreflight_AuthorizationFailure_NamesCredentialsAndACLs(t *testing.T) {
	for _, kerr := range []sarama.KError{
		sarama.ErrTopicAuthorizationFailed,
		sarama.ErrClusterAuthorizationFailed,
		sarama.ErrSASLAuthenticationFailed,
	} {
		t.Run(kerr.Error(), func(t *testing.T) {
			d := &fakeDescriber{md: metadataFor("__transaction_state", kerr, 0)}

			err := probeTxnStateTopic(d, "__transaction_state")
			require.Error(t, err)
			assert.Contains(t, strings.ToLower(err.Error()), "credential")
			assert.Contains(t, strings.ToLower(err.Error()), "acl")
			assert.NotContains(t, err.Error(), "does not exist")
		})
	}
}

// R12: a broker that cannot be reached at all is the same fail-fast, and its
// cause is still credentials or connectivity rather than a missing topic.
func TestPreflight_UnreachableBroker_FailsNamingCredentialsAndACLs(t *testing.T) {
	d := &fakeDescriber{err: errors.New("dial tcp 10.0.0.1:9092: connect: connection refused")}

	err := probeTxnStateTopic(d, "__transaction_state")
	require.Error(t, err)
	assert.Contains(t, strings.ToLower(err.Error()), "credential")
	assert.Contains(t, strings.ToLower(err.Error()), "acl")
	assert.Contains(t, err.Error(), "connection refused", "the underlying cause has to survive")
}

// Data sensitivity: every command error is slog.Error'd by main, so it lands in
// kcp.log — the file operators attach to support tickets. The topic name is
// operator-supplied and must not travel with it.
func TestPreflight_Errors_DoNotCarryTheTopicName(t *testing.T) {
	const distinctive = "acme.payments.settlements.txnstate"

	for _, d := range []*fakeDescriber{
		{md: metadataFor(distinctive, sarama.ErrUnknownTopicOrPartition, 0)},
		{md: metadataFor(distinctive, sarama.ErrTopicAuthorizationFailed, 0)},
		{err: errors.New("connection refused")},
		{md: nil},
	} {
		err := probeTxnStateTopic(d, distinctive)
		require.Error(t, err)
		assert.NotContains(t, err.Error(), distinctive)
	}
}

// R12: a response that names no topic at all is a failure, not a pass. Treating
// an empty metadata list as success would let the run start against a cluster
// that never confirmed the topic.
func TestPreflight_EmptyMetadata_Fails(t *testing.T) {
	d := &fakeDescriber{md: nil}

	require.Error(t, probeTxnStateTopic(d, "__transaction_state"))
}

// R10: one tail.Tail serves both readers over ONE channel carrying both topics.
// A shared channel delivers each batch to exactly one receiver, so handing the
// same channel to both readers silently splits the stream between them —
// records vanish with no error anywhere. Every batch must be COPIED to every
// destination.
func TestFanOut_CopiesEveryBatchToEveryDestination(t *testing.T) {
	src := make(chan tail.Batch, 2)
	src <- tail.Batch{Topic: "__transaction_state", Partition: 3, Records: []tail.Record{{Offset: 7}}}
	src <- tail.Batch{Topic: "__consumer_offsets", Partition: 11, ProducerID: 4242}
	close(src)

	a, b := make(chan tail.Batch, 4), make(chan tail.Batch, 4)
	fanOut(context.Background(), src, []chan tail.Batch{a, b})

	got := func(ch chan tail.Batch) []tail.Batch {
		var out []tail.Batch
		for batch := range ch {
			out = append(out, batch)
		}
		return out
	}
	first, second := got(a), got(b)

	require.Len(t, first, 2, "every destination sees every batch")
	assert.Equal(t, first, second, "both destinations see the same batches, in the same order")
	assert.Equal(t, "__transaction_state", first[0].Topic)
	assert.Equal(t, "__consumer_offsets", first[1].Topic)
}

// The fan-out closes its destinations when the source ends: that close is what
// makes the __transaction_state reader return and, crucially, what triggers the
// consumer-offsets tail's final flush.
func TestFanOut_ClosesDestinationsWhenTheSourceEnds(t *testing.T) {
	src := make(chan tail.Batch)
	close(src)

	a := make(chan tail.Batch, 1)
	fanOut(context.Background(), src, []chan tail.Batch{a})

	_, open := <-a
	assert.False(t, open, "the destination must be closed, not merely empty")
}

// A reader that has stopped receiving must not wedge the fan-out: the
// destinations still close, so the other readers still finish and flush.
func TestFanOut_UnreadDestination_DoesNotWedgeShutdown(t *testing.T) {
	src := make(chan tail.Batch, 1)
	src <- tail.Batch{Topic: "__transaction_state"}

	ctx, cancel := context.WithCancel(context.Background())
	blocked := make(chan tail.Batch) // unbuffered and never read
	done := make(chan struct{})
	go func() {
		defer close(done)
		fanOut(ctx, src, []chan tail.Batch{blocked})
	}()

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("fanOut blocked on an unread destination after cancellation")
	}
	_, open := <-blocked
	assert.False(t, open, "the destination is closed even on the cancellation path")
}

// R7, R9, R16, R17: the run holds the window, then produces the artifacts. This
// is the whole happy path — a transaction's produced footprint from
// __transaction_state, its consumed input recovered from __consumer_offsets by
// producer id, both in one group in the YAML.
func TestRun_TheWindowEndsTheRunAndTheArtifactsAreWritten(t *testing.T) {
	dir := t.TempDir()
	h := newHarness()
	h.tailClient.script(discovery.DefaultTxnStateTopic, stateRecordBatch(0,
		[][]byte{txnKey("payments-txn-0")},
		[][]byte{txnValue(4242, "payments.out", "__consumer_offsets")},
	))
	h.tailClient.script(discovery.DefaultConsumerOffsetsTopic, commitRecordBatch(0, 4242,
		commitKey("payments-group", "payments.in", 0),
	))

	opts := baseOpts(dir)
	opts.StatsOutPath = filepath.Join(dir, "stats.json")

	require.NoError(t, h.runner(t, opts).Run(context.Background()))

	yaml := readFile(t, opts.OutPath)
	assert.Contains(t, yaml, "payments.out", "the produced footprint reaches the YAML")
	assert.Contains(t, yaml, "payments.in", "the consumed input recovered by producer id reaches the YAML")
	assert.Contains(t, yaml, "payments-txn-0")

	assert.True(t, exists(t, opts.StatsOutPath), "--stats-out is written")
	assert.True(t, exists(t, opts.AuditLogPath), "the audit trail is on by default")
	assert.Equal(t, 1, h.closes, "the sarama client and cluster admin are released")
}

// R10, integration constraint: one tail serves both readers over one channel,
// so the command must fan it out BY COPY. A shared channel delivers each batch
// to exactly one receiver — the offsets tail would swallow transaction-state
// batches and vice versa, losing roughly half of each stream with no error
// anywhere.
//
// Enough batches on each topic that the split cannot go unnoticed: a shared
// channel would have to route all sixteen correctly by chance.
func TestRun_BothReadersSeeEveryBatch(t *testing.T) {
	const n = 8

	dir := t.TempDir()
	h := newHarness()
	for i := range n {
		h.tailClient.script(discovery.DefaultTxnStateTopic, stateRecordBatch(int64(i),
			[][]byte{txnKey(fmt.Sprintf("txn-%d", i))},
			[][]byte{txnValue(int64(100+i), fmt.Sprintf("out.%d", i), "__consumer_offsets")},
		))
		h.tailClient.script(discovery.DefaultConsumerOffsetsTopic, commitRecordBatch(int64(i), int64(100+i),
			commitKey(fmt.Sprintf("group-%d", i), fmt.Sprintf("in.%d", i), 0),
		))
	}

	opts := baseOpts(dir)
	opts.EnrichConsumerGroups = false
	require.NoError(t, h.runner(t, opts).Run(context.Background()))

	yaml := readFile(t, opts.OutPath)
	for i := range n {
		assert.Contains(t, yaml, fmt.Sprintf("out.%d", i), "every transaction-state batch reached its reader")
		assert.Contains(t, yaml, fmt.Sprintf("in.%d", i), "every consumer-offsets batch reached its reader")
	}
}

// R21, integration constraint: the tail's stats must be snapshotted BEFORE the
// run's context is cancelled. Every partition loop clears its running flag as it
// exits, so a snapshot taken after shutdown reports zero partitions live and the
// health line condemns a perfectly healthy run as failed.
func TestRun_TailStatsAreSnapshottedBeforeShutdown(t *testing.T) {
	dir := t.TempDir()
	h := newHarness()
	h.tailClient.script(discovery.DefaultTxnStateTopic, stateRecordBatch(0,
		[][]byte{txnKey("txn-a")},
		[][]byte{txnValue(11, "topic.a")},
	))

	opts := baseOpts(dir)
	opts.StatsOutPath = filepath.Join(dir, "stats.json")
	require.NoError(t, h.runner(t, opts).Run(context.Background()))

	var doc struct {
		Tail struct {
			PartitionsAssigned int `json:"partitions_assigned"`
			PartitionsRunning  int `json:"partitions_running"`
		} `json:"tail"`
	}
	require.NoError(t, json.Unmarshal([]byte(readFile(t, opts.StatsOutPath)), &doc))

	require.Positive(t, doc.Tail.PartitionsAssigned, "the run must actually have assigned partitions")
	assert.Equal(t, doc.Tail.PartitionsAssigned, doc.Tail.PartitionsRunning,
		"every assigned partition was live when the window closed; a post-cancel snapshot would report zero")
}

// R20: --dry-run suppresses every artifact write. It does not suppress kcp.log.
func TestRun_DryRun_WritesNothing(t *testing.T) {
	dir := t.TempDir()
	h := newHarness()
	h.tailClient.script(discovery.DefaultTxnStateTopic, stateRecordBatch(0,
		[][]byte{txnKey("txn-a")},
		[][]byte{txnValue(11, "topic.a", "topic.b")},
	))

	opts := baseOpts(dir)
	opts.StatsOutPath = filepath.Join(dir, "stats.json")
	opts.DryRun = true

	require.NoError(t, h.runner(t, opts).Run(context.Background()))

	assert.False(t, exists(t, opts.OutPath), "no groups YAML")
	assert.False(t, exists(t, opts.StatsOutPath), "no stats JSON")
	assert.False(t, exists(t, opts.AuditLogPath), "no audit trail — the audit log is a file write like any other")

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	assert.Empty(t, entries, "a dry run leaves the output directory untouched")
}

// R18: --no-audit-log suppresses the audit trail and nothing else.
func TestRun_NoAuditLog_SuppressesOnlyTheAuditTrail(t *testing.T) {
	dir := t.TempDir()
	h := newHarness()
	h.tailClient.script(discovery.DefaultTxnStateTopic, stateRecordBatch(0,
		[][]byte{txnKey("txn-a")},
		[][]byte{txnValue(11, "topic.a", "topic.b")},
	))

	opts := baseOpts(dir)
	opts.StatsOutPath = filepath.Join(dir, "stats.json")
	opts.AuditLogPath = "" // what --no-audit-log resolves to

	require.NoError(t, h.runner(t, opts).Run(context.Background()))

	assert.True(t, exists(t, opts.OutPath), "the groups YAML is still written")
	assert.True(t, exists(t, opts.StatsOutPath), "the stats JSON is still written")
	assert.False(t, exists(t, filepath.Join(dir, DefaultAuditBasename)), "no audit trail")
	assert.Contains(t, readFile(t, opts.OutPath), "topic.a")
}

// R18, integration constraint: a truncated audit trail reads downstream as "no
// transaction coupled these topics", which is indistinguishable from a clean
// run. The exit code is the only thing that tells them apart.
func TestRun_AuditWriteErrors_ProduceANonZeroExit(t *testing.T) {
	dir := t.TempDir()
	h := newHarness()
	h.tailClient.script(discovery.DefaultTxnStateTopic, stateRecordBatch(0,
		[][]byte{txnKey("txn-a")},
		[][]byte{txnValue(11, "topic.a", "topic.b")},
	))

	opts := baseOpts(dir)
	runner := h.runner(t, opts)
	// A writer whose file is already gone: every Record fails, exactly as a
	// disk that filled part-way through the run would.
	runner.newAudit = func(path string) (*report.AuditWriter, error) {
		w, err := report.NewAuditWriter(path)
		require.NoError(t, err)
		require.NoError(t, w.Close())
		return w, nil
	}

	err := runner.Run(context.Background())

	require.Error(t, err, "an incomplete audit trail must not exit zero")
	assert.Contains(t, err.Error(), "audit")
	assert.True(t, exists(t, opts.OutPath), "the groups YAML is still written; only the exit code reports the gap")
}

// R9, integration constraint: the artifacts are written only after the offsets
// tail's final flush. Here the resolve interval is longer than any plausible
// run, so the producer-id correlation can ONLY be resolved by that final flush —
// if the YAML were written first, the recovered input would be missing from it.
func TestRun_ArtifactsAreWrittenAfterTheFinalFlush(t *testing.T) {
	dir := t.TempDir()
	h := newHarness()
	h.tailClient.script(discovery.DefaultTxnStateTopic, stateRecordBatch(0,
		[][]byte{txnKey("eos-app-txn")},
		[][]byte{txnValue(777, "eos.output", "__consumer_offsets")},
	))
	h.tailClient.script(discovery.DefaultConsumerOffsetsTopic, commitRecordBatch(0, 777,
		// A group id bearing no naming relationship to the transactional id, so
		// only the exact producer-id join can recover this input.
		commitKey("unrelated-group-name", "eos.input", 0),
	))

	opts := baseOpts(dir)
	opts.Interval = time.Hour
	opts.EnrichConsumerGroups = false

	require.NoError(t, h.runner(t, opts).Run(context.Background()))

	assert.Contains(t, readFile(t, opts.OutPath), "eos.input",
		"the late producer-id resolution must reach the YAML, so the write happens after the final flush")
}

// R12, abuse case: preflight failure exits non-zero and leaves nothing behind —
// not even the audit trail, which is opened after the probe for exactly this
// reason.
func TestRun_PreflightFailure_ExitsNonZeroAndWritesNothing(t *testing.T) {
	dir := t.TempDir()
	h := newHarness()
	h.describer = &fakeDescriber{md: metadataFor(discovery.DefaultTxnStateTopic, sarama.ErrTopicAuthorizationFailed, 0)}

	opts := baseOpts(dir)
	opts.StatsOutPath = filepath.Join(dir, "stats.json")

	err := h.runner(t, opts).Run(context.Background())

	require.Error(t, err)
	assert.Contains(t, strings.ToLower(err.Error()), "credential")
	assert.Contains(t, strings.ToLower(err.Error()), "acl")

	entries, rerr := os.ReadDir(dir)
	require.NoError(t, rerr)
	assert.Empty(t, entries, "a failed preflight writes no files at all")
	// Both of the run's connections are open by the time the preflight runs, and a
	// preflight that exits without releasing them leaks two authenticated sessions on
	// every misconfigured invocation — which is the invocation an operator repeats.
	assert.Equal(t, 1, h.closes, "the preflight failure path released the cluster's connections")
}

// R21: the enrichment phase's counters have to reach the stats document, or a
// captured run cannot say whether the cadence --interval asked for was achieved.
// Every other source's counters are wired through; this one's were not collected
// at all, which is why a run that enriched four times in a window sized for
// thirteen looked identical to one that enriched thirteen.
func TestRun_EnrichmentCountersReachTheStatsDocument(t *testing.T) {
	dir := t.TempDir()
	h := newHarness()
	h.tailClient.script(discovery.DefaultTxnStateTopic, stateRecordBatch(0,
		[][]byte{txnKey("payments-processor-abc12")},
		[][]byte{txnValue(4242, "payments.out")},
	))
	// A group the Streams naming convention correlates to that transaction, so the
	// pass has something to find rather than merely something to count.
	h.admin.groups = map[string]string{"payments-processor": "consumer"}
	h.admin.offsets = map[string][]string{"payments-processor": {"payments.in"}}

	opts := baseOpts(dir)
	opts.StatsOutPath = filepath.Join(dir, "stats.json")

	require.NoError(t, h.runner(t, opts).Run(context.Background()))

	var doc struct {
		Enrichment struct {
			Passes       int `json:"passes"`
			PassFailures int `json:"pass_failures"`
			GroupsListed int `json:"groups_listed"`
			Correlations int `json:"correlations"`
		} `json:"consumer_group_enrichment"`
	}
	require.NoError(t, json.Unmarshal([]byte(readFile(t, opts.StatsOutPath)), &doc))

	assert.Positive(t, doc.Enrichment.Passes, "the run completed enrichment passes and the document reports none")
	assert.Equal(t, 1, doc.Enrichment.GroupsListed, "the pass listed one group")
	assert.Equal(t, 1, doc.Enrichment.Correlations, "the group correlated to the transaction")
	assert.Zero(t, doc.Enrichment.PassFailures)
}

// R13: an unreadable consumer-offsets log degrades the run to consumer-group
// enrichment alone rather than failing it.
func TestRun_UnreadableConsumerOffsets_DegradesRatherThanFailing(t *testing.T) {
	dir := t.TempDir()
	h := newHarness()
	h.probeErr = sarama.ErrTopicAuthorizationFailed
	h.tailClient.script(discovery.DefaultTxnStateTopic, stateRecordBatch(0,
		[][]byte{txnKey("txn-a")},
		[][]byte{txnValue(11, "topic.a", "topic.b")},
	))

	opts := baseOpts(dir)
	opts.StatsOutPath = filepath.Join(dir, "stats.json")

	require.NoError(t, h.runner(t, opts).Run(context.Background()))

	assert.Contains(t, readFile(t, opts.OutPath), "topic.a", "the transaction-state footprints are still worth having")

	var doc struct {
		Offsets struct {
			Unavailable bool `json:"unavailable"`
		} `json:"consumer_offsets_log"`
	}
	require.NoError(t, json.Unmarshal([]byte(readFile(t, opts.StatsOutPath)), &doc))
	assert.True(t, doc.Offsets.Unavailable,
		"the phase must be recorded as never having run, so the summary does not present it as having found nothing")
}
