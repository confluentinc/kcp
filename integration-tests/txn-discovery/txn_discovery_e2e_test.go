//go:build e2e_txndiscovery

// Package txndiscovery_test exercises `kcp migration txn-discovery` against a
// real single-node Kafka broker started by docker-compose.
//
// Per KTD5 the suite runs the BUILT BINARY as a subprocess and asserts on its
// exit code, its stdout, and the files it wrote. Calling the discovery packages
// directly — which the superseded branch's e2e did — would leave the command,
// its flags, its preflight and its output formatting entirely unexercised, and
// those are the deliverable.
//
// Fixtures are produced in-process by sarama transactional producers, so there
// is no seed script to keep in step with the assertions.
//
// Every fixture name carries the `zzqx-` prefix. That is not decoration: three
// abuse-case tests assert that no topic name and no transactional id reaches
// stdout or kcp.log, and a prefix that cannot occur incidentally is what makes
// those assertions meaningful rather than accidentally true.
package txndiscovery_test

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/IBM/sarama"
	"github.com/goccy/go-yaml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	// bootstrapAddr is the EXTERNAL listener the compose file advertises to the
	// host. The broker reaches itself on the INTERNAL one.
	bootstrapAddr = "localhost:29092"

	// containerName must match docker-compose.yml; the broker-restart test
	// drives it directly.
	containerName = "kcp-test-txn-discovery-kafka"

	// fixturePrefix is what the no-names assertions grep for.
	fixturePrefix = "zzqx-"
)

// kafkaVersion is the broker's protocol version. cp-kafka 7.6.0 is Kafka 3.6.
var kafkaVersion = sarama.V3_6_0_0

// ---------------------------------------------------------------------------
// Kafka fixture helpers
// ---------------------------------------------------------------------------

// baseConfig is the shared sarama configuration for every fixture client.
func baseConfig() *sarama.Config {
	cfg := sarama.NewConfig()
	cfg.Version = kafkaVersion
	cfg.ClientID = "kcp-txn-discovery-e2e"
	cfg.Metadata.Retry.Max = 10
	cfg.Metadata.Retry.Backoff = 500 * time.Millisecond
	return cfg
}

// newAdmin returns a cluster admin against the broker.
func newAdmin(t *testing.T) sarama.ClusterAdmin {
	t.Helper()
	admin, err := sarama.NewClusterAdmin([]string{bootstrapAddr}, baseConfig())
	require.NoError(t, err, "failed to connect a cluster admin to %s — is the compose broker up?", bootstrapAddr)
	t.Cleanup(func() { _ = admin.Close() })
	return admin
}

// createTopics creates each topic with one partition, ignoring "already exists".
//
// One partition, deliberately: a transaction's Ongoing record is written as
// partitions are added to it, so a multi-partition output topic can produce two
// footprint records with a growing topic-partition set. TestAuditLogRecordsGrowthOnly
// asserts an exact line count and would be at the mercy of that.
func createTopics(t *testing.T, names ...string) {
	t.Helper()
	admin := newAdmin(t)
	for _, name := range names {
		err := admin.CreateTopic(name, &sarama.TopicDetail{NumPartitions: 1, ReplicationFactor: 1}, false)
		if err != nil && !errors.Is(err, sarama.ErrTopicAlreadyExists) &&
			!strings.Contains(err.Error(), "already exists") {
			t.Fatalf("failed to create topic: %v", err)
		}
	}
}

// txnFixture describes one transaction to produce.
type txnFixture struct {
	// TxnID is the transactional.id, which is what the transaction-state log
	// records the footprint under.
	TxnID string

	// Produce are the topics the transaction writes to. They become the
	// transaction's footprint and therefore its group.
	Produce []string

	// ConsumeTopic and Group, when set, make this a consume-transform-produce
	// transaction: the offsets are committed INSIDE the transaction with
	// AddOffsetsToTxn, so the resulting __consumer_offsets record is a
	// transactional one carrying the producer id. That is the record
	// producer-id correlation joins on.
	ConsumeTopic string
	Group        string

	// Abort rolls the transaction back instead of committing it.
	Abort bool
}

// produceTxn runs one transaction to completion against the broker.
func produceTxn(t *testing.T, f txnFixture) {
	t.Helper()

	cfg := baseConfig()
	cfg.Producer.Idempotent = true
	cfg.Producer.RequiredAcks = sarama.WaitForAll
	cfg.Producer.Return.Successes = true
	cfg.Producer.Return.Errors = true
	cfg.Net.MaxOpenRequests = 1
	cfg.Producer.Transaction.ID = f.TxnID
	cfg.Producer.Transaction.Timeout = 60 * time.Second

	producer, err := sarama.NewSyncProducer([]string{bootstrapAddr}, cfg)
	require.NoError(t, err, "failed to create a transactional producer")
	defer func() { _ = producer.Close() }()

	require.NoError(t, producer.BeginTxn(), "BeginTxn failed")

	for _, topic := range f.Produce {
		_, _, err := producer.SendMessage(&sarama.ProducerMessage{
			Topic: topic,
			Key:   sarama.StringEncoder("k"),
			Value: sarama.StringEncoder("v"),
		})
		require.NoError(t, err, "failed to produce inside the transaction")
	}

	if f.ConsumeTopic != "" {
		require.NotEmpty(t, f.Group, "a consumed topic needs the group that committed it")
		err := producer.AddOffsetsToTxn(map[string][]*sarama.PartitionOffsetMetadata{
			f.ConsumeTopic: {{Partition: 0, Offset: 1}},
		}, f.Group)
		require.NoError(t, err, "AddOffsetsToTxn failed")
	}

	if f.Abort {
		require.NoError(t, producer.AbortTxn(), "AbortTxn failed")
		return
	}
	require.NoError(t, producer.CommitTxn(), "CommitTxn failed")
}

// commitGroupOffsets commits an offset for topic under group OUTSIDE any
// transaction, which is how a plain consumer records what it consumes.
//
// Consumer-group enrichment reads exactly this — the topics a group has
// committed offsets for — so it is what the Kafka Streams naming scenario needs
// on the cluster. The commit is deliberately non-transactional: it must be the
// NAMING convention that recovers the input, not producer-id correlation.
func commitGroupOffsets(t *testing.T, group, topic string) {
	t.Helper()

	cfg := baseConfig()
	cfg.Consumer.Offsets.AutoCommit.Enable = false
	cfg.Consumer.Offsets.Initial = sarama.OffsetOldest

	client, err := sarama.NewClient([]string{bootstrapAddr}, cfg)
	require.NoError(t, err, "failed to create a client for the offset commit")
	defer func() { _ = client.Close() }()

	om, err := sarama.NewOffsetManagerFromClient(group, client)
	require.NoError(t, err, "failed to create an offset manager")

	pom, err := om.ManagePartition(topic, 0)
	require.NoError(t, err, "failed to manage the partition's offsets")
	pom.MarkOffset(1, "")
	om.Commit()

	// om.Close(), never pom.Close(). With AutoCommit disabled sarama never
	// starts the offset manager's main loop, and it is that loop which releases
	// a partition manager and closes the error channel pom.Close() drains — so
	// pom.Close() deadlocks. om.Close() releases every partition manager itself.
	require.NoError(t, om.Close(), "failed to close the offset manager")

	// The fixture verifies itself. A commit that silently did not land would
	// make the enrichment scenario fail for a reason three components away from
	// the one under test.
	admin := newAdmin(t)
	resp, err := admin.ListConsumerGroupOffsets(group, nil)
	require.NoError(t, err, "failed to read back the committed offsets")
	block := resp.GetBlock(topic, 0)
	require.NotNil(t, block, "the offset commit did not land: the group has no offset for the topic")
	require.GreaterOrEqual(t, block.Offset, int64(0), "the offset commit did not land: the group's offset is unset")
}

// restartBroker stops and starts the broker container, then blocks until the
// cluster can serve the transaction-state log again.
//
// `docker restart` rather than `docker compose up --force-recreate`: the
// container keeps its filesystem, so the log the reader must resume within
// survives. Recreating it would wipe the data and turn a resumption test into
// a fresh-cluster test.
func restartBroker(t *testing.T) {
	t.Helper()
	out, err := exec.Command("docker", "restart", containerName).CombinedOutput()
	require.NoError(t, err, "failed to restart the broker: %s", out)
	awaitBrokerUp(t)
	// Only meaningful AFTER a restart, where the log demonstrably existed
	// beforehand. Requiring it on a fresh cluster would wait forever: the
	// coordinator creates __transaction_state on the first transaction, not at
	// boot.
	awaitTxnStateLoaded(t)
}

// awaitBrokerUp blocks until the broker answers a metadata request.
func awaitBrokerUp(t *testing.T) {
	t.Helper()
	require.Eventually(t, func() bool { _, ok := listTopics(t); return ok },
		120*time.Second, 2*time.Second, "the broker never came back up")
}

// awaitTxnStateLoaded blocks until the transaction-state log is listable again.
//
// Answering a metadata request is not enough on its own: the broker accepts
// requests well before the transaction coordinator has loaded its state, and a
// fixture produced into that gap fails on the fixture rather than on the
// behaviour under test.
func awaitTxnStateLoaded(t *testing.T) {
	t.Helper()
	require.Eventually(t, func() bool {
		topics, ok := listTopics(t)
		if !ok {
			return false
		}
		_, exists := topics[txnStateTopic]
		return exists
	}, 120*time.Second, 2*time.Second, "the broker did not come back with a loaded transaction-state log")
}

// listTopics lists the cluster's topics through a throwaway admin client,
// reporting failure rather than failing the test.
//
// A fresh client per call is deliberate: one built before a restart holds stale
// metadata and a dead connection, so reusing it would keep reporting the
// pre-restart view.
func listTopics(t *testing.T) (map[string]sarama.TopicDetail, bool) {
	t.Helper()
	admin, err := sarama.NewClusterAdmin([]string{bootstrapAddr}, baseConfig())
	if err != nil {
		return nil, false
	}
	defer func() { _ = admin.Close() }()
	topics, err := admin.ListTopics()
	if err != nil {
		return nil, false
	}
	return topics, true
}

// ensureInternalTopics makes sure __transaction_state and __consumer_offsets
// exist, by running one full consume-transform-produce transaction.
//
// Neither topic exists on a fresh cluster: the coordinators create them on
// first use. Without them the command's preflight fails and the offsets tail
// reports itself unavailable — both for reasons that have nothing to do with
// whatever a test is actually asserting.
func ensureInternalTopics(t *testing.T) {
	t.Helper()
	createTopics(t, warmupOut, warmupIn)
	produceTxn(t, txnFixture{TxnID: warmupTxn, Produce: []string{warmupOut}, ConsumeTopic: warmupIn, Group: warmupGroup})
	awaitTxnStateLoaded(t)
}

// txnStateTopic is the internal topic the command reads by default.
const txnStateTopic = "__transaction_state"

// topicExists reports whether the broker has topic, using a listing rather than
// a topic-scoped metadata request.
//
// The distinction matters for TestNoTopicCreationOnProbe: a topic-scoped
// metadata request is exactly what can create a topic on a broker with
// auto-create on, so an independent verifier that asked that way could create
// the very topic it is checking for and never notice.
func topicExists(t *testing.T, topic string) bool {
	t.Helper()
	admin := newAdmin(t)
	topics, err := admin.ListTopics()
	require.NoError(t, err, "failed to list topics")
	_, ok := topics[topic]
	return ok
}

// ---------------------------------------------------------------------------
// Running the binary under test
// ---------------------------------------------------------------------------

// repoRoot resolves the checkout root from this package's directory.
func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	require.NoError(t, err)
	root, err := filepath.Abs(filepath.Join(wd, "..", ".."))
	require.NoError(t, err)
	return root
}

// kcpBinary returns the path to the binary the Makefile target built.
func kcpBinary(t *testing.T) string {
	t.Helper()
	path := filepath.Join(repoRoot(t), "kcp")
	_, err := os.Stat(path)
	require.NoError(t, err, "the kcp binary is missing — run `make test-txn-discovery`, which builds it")
	return path
}

// artifact filenames, fixed so every run's assertions look in the same place.
const (
	outFile   = "txn-discovery.yaml"
	statsFile = "txn-discovery-stats.json"
	auditFile = "txn-discovery-audit.jsonl"
	logFile   = "kcp.log"
)

// kcpProc is a discovery run in flight.
type kcpProc struct {
	cmd    *exec.Cmd
	dir    string
	stdout *bytes.Buffer
	stderr *bytes.Buffer
}

// startKCP launches `kcp migration txn-discovery` in its own directory.
//
// The directory is per-run and is where the artifacts AND kcp.log land, since
// the root command writes the log relative to the process working directory.
// That isolation is what lets the kcp.log assertions be about one run.
func startKCP(t *testing.T, args ...string) *kcpProc {
	t.Helper()
	dir := t.TempDir()

	full := append([]string{"migration", "txn-discovery"}, args...)
	cmd := exec.Command(kcpBinary(t), full...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	require.NoError(t, cmd.Start(), "failed to start the kcp binary")

	p := &kcpProc{cmd: cmd, dir: dir, stdout: &stdout, stderr: &stderr}
	t.Cleanup(func() {
		if p.cmd.ProcessState == nil && p.cmd.Process != nil {
			_ = p.cmd.Process.Kill()
			_ = p.cmd.Wait()
		}
	})
	return p
}

// awaitObserving blocks until the run has begun reading the cluster.
//
// The consumer-offsets tail starts at LATEST, so a fixture produced before the
// tail resolved its start offsets is invisible to producer-id correlation. The
// run's own "starting transaction discovery" log line is the closest signal
// available; the extra pause covers the partition discovery that follows it.
func (p *kcpProc) awaitObserving(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(filepath.Join(p.dir, logFile))
		if err == nil && strings.Contains(string(data), "starting transaction discovery") {
			time.Sleep(5 * time.Second)
			return
		}
		if p.cmd.ProcessState != nil {
			t.Fatalf("the run exited before it began observing:\n%s\n%s", p.stdout.String(), p.stderr.String())
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("the run never reported that it had started observing:\n%s\n%s", p.stdout.String(), p.stderr.String())
}

// wait blocks for the run to finish and collects everything it produced.
func (p *kcpProc) wait(t *testing.T) *runResult {
	t.Helper()
	err := p.cmd.Wait()
	code := 0
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		code = exitErr.ExitCode()
	} else if err != nil {
		t.Fatalf("failed to wait for the kcp binary: %v", err)
	}
	return collect(t, p.dir, code, p.stdout.String(), p.stderr.String())
}

// runKCP runs a discovery run to completion with no fixtures produced during it.
func runKCP(t *testing.T, args ...string) *runResult {
	t.Helper()
	return startKCP(t, args...).wait(t)
}

// ---------------------------------------------------------------------------
// Artifacts
// ---------------------------------------------------------------------------

// discoveryDoc mirrors txn-discovery.yaml. It is redeclared here rather than
// imported: the suite is a consumer of the on-disk format, and sharing the
// producer's struct would let a field rename pass unnoticed.
type discoveryDoc struct {
	GeneratedBy          string           `yaml:"generated_by"`
	GeneratedAt          string           `yaml:"generated_at"`
	ObservationWindow    string           `yaml:"observation_window"`
	Groups               []discoveryGroup `yaml:"groups"`
	IndividualTopicCount int              `yaml:"individual_topic_count"`
	IndividualTopics     []string         `yaml:"individual_topics"`
}

type discoveryGroup struct {
	Name             string   `yaml:"name"`
	ReadProcessWrite bool     `yaml:"read_process_write"`
	Warning          string   `yaml:"warning"`
	Topics           []string `yaml:"topics"`
	TransactionalIDs []string `yaml:"transactional_ids"`
}

// statsDoc is the slice of txn-discovery-stats.json this suite reads: the
// keep-up counters that say whether the reader stayed healthy and, for the
// restart scenario, whether it was disrupted at all.
type statsDoc struct {
	Tail struct {
		PartitionsAssigned int   `json:"partitions_assigned"`
		PartitionsRunning  int   `json:"partitions_running"`
		RecordsRead        int64 `json:"records_read"`
		Lag                int64 `json:"lag"`
		DecodeErrors       int64 `json:"decode_errors"`
		LeadershipErrors   int64 `json:"leadership_errors"`
		UnclassifiedErrors int64 `json:"unclassified_errors"`
		OffsetResets       int64 `json:"offset_resets"`
		TransportErrors    int64 `json:"transport_errors"`
	} `json:"tail"`
}

// auditLine mirrors one line of txn-discovery-audit.jsonl.
type auditLine struct {
	Timestamp time.Time `json:"timestamp"`
	TxnID     string    `json:"transactional_id"`
	Source    string    `json:"source"`
	Added     []string  `json:"added"`
	Topics    []string  `json:"topics"`
}

// runResult is everything one run left behind.
type runResult struct {
	dir      string
	exitCode int
	stdout   string
	stderr   string

	// doc is the parsed YAML, nil when the run wrote none.
	doc *discoveryDoc
	// audit is every parsed audit line, nil when no audit log was written.
	audit []auditLine
	// kcpLog is the run's log file, empty when it wrote none.
	kcpLog string
	// stats is the parsed diagnostics document, nil when none was asked for.
	stats *statsDoc

	// files is every name the run left in its directory, and modes their
	// permission bits.
	//
	// Both are captured when the run finishes rather than read on demand,
	// because the directory is a t.TempDir() belonging to whichever test first
	// triggered the shared run and is removed when THAT test returns. Reading a
	// mode later would find nothing there and a "writes nothing" assertion
	// would pass against a deleted directory.
	files []string
	modes map[string]os.FileMode
}

// collect reads a finished run's directory.
func collect(t *testing.T, dir string, code int, stdout, stderr string) *runResult {
	t.Helper()
	r := &runResult{dir: dir, exitCode: code, stdout: stdout, stderr: stderr, modes: map[string]os.FileMode{}}

	entries, err := os.ReadDir(dir)
	require.NoError(t, err, "failed to list the run's directory")
	for _, e := range entries {
		r.files = append(r.files, e.Name())
		info, ierr := e.Info()
		require.NoError(t, ierr, "failed to stat an artifact")
		r.modes[e.Name()] = info.Mode().Perm()
	}

	if data, err := os.ReadFile(filepath.Join(dir, outFile)); err == nil {
		var doc discoveryDoc
		require.NoError(t, yaml.Unmarshal(data, &doc), "the discovery YAML did not parse:\n%s", data)
		r.doc = &doc
	}
	if data, err := os.ReadFile(filepath.Join(dir, auditFile)); err == nil {
		scanner := bufio.NewScanner(bytes.NewReader(data))
		scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" {
				continue
			}
			var al auditLine
			require.NoError(t, json.Unmarshal([]byte(line), &al), "an audit line did not parse: %s", line)
			r.audit = append(r.audit, al)
		}
		require.NoError(t, scanner.Err(), "failed to read the audit log")
	}
	if data, err := os.ReadFile(filepath.Join(dir, statsFile)); err == nil {
		var sd statsDoc
		require.NoError(t, json.Unmarshal(data, &sd), "the stats JSON did not parse:\n%s", data)
		r.stats = &sd
	}
	if data, err := os.ReadFile(filepath.Join(dir, logFile)); err == nil {
		r.kcpLog = string(data)
	}
	return r
}

// requireSucceeded fails the test unless the run exited cleanly and wrote a
// document, quoting the run's own output when it did not.
func (r *runResult) requireSucceeded(t *testing.T) {
	t.Helper()
	require.Equal(t, 0, r.exitCode, "the discovery run failed:\nSTDOUT\n%s\nSTDERR\n%s", r.stdout, r.stderr)
	require.NotNil(t, r.doc, "the run wrote no %s:\nSTDOUT\n%s", outFile, r.stdout)
}

// groupWith returns the group containing topic, or nil.
func (r *runResult) groupWith(topic string) *discoveryGroup {
	for i := range r.doc.Groups {
		for _, tp := range r.doc.Groups[i].Topics {
			if tp == topic {
				return &r.doc.Groups[i]
			}
		}
	}
	return nil
}

// isIndividual reports whether topic was reported as safe to migrate alone.
func (r *runResult) isIndividual(topic string) bool {
	for _, tp := range r.doc.IndividualTopics {
		if tp == topic {
			return true
		}
	}
	return false
}

// observedTopics is every topic the document names, grouped or individual.
func (r *runResult) observedTopics() []string {
	var out []string
	for _, g := range r.doc.Groups {
		out = append(out, g.Topics...)
	}
	return append(out, r.doc.IndividualTopics...)
}

// auditFor returns every audit line naming topic in its resulting set.
func (r *runResult) auditFor(topic string) []auditLine {
	var out []auditLine
	for _, l := range r.audit {
		for _, tp := range l.Topics {
			if tp == topic {
				out = append(out, l)
				break
			}
		}
	}
	return out
}

// auditForTxn returns every audit line for one transactional id.
func (r *runResult) auditForTxn(txnID string) []auditLine {
	var out []auditLine
	for _, l := range r.audit {
		if l.TxnID == txnID {
			out = append(out, l)
		}
	}
	return out
}

// describe renders the run for a failure message.
func (r *runResult) describe() string {
	var b strings.Builder
	fmt.Fprintf(&b, "exit=%d\n--- STDOUT ---\n%s", r.exitCode, r.stdout)
	if r.stderr != "" {
		fmt.Fprintf(&b, "--- STDERR ---\n%s", r.stderr)
	}
	if r.doc != nil {
		fmt.Fprintf(&b, "--- GROUPS ---\n")
		for _, g := range r.doc.Groups {
			fmt.Fprintf(&b, "  %s rpw=%v topics=%v txns=%v\n", g.Name, g.ReadProcessWrite, g.Topics, g.TransactionalIDs)
		}
		fmt.Fprintf(&b, "--- INDIVIDUAL ---\n  %v\n", r.doc.IndividualTopics)
	}
	return b.String()
}

// ---------------------------------------------------------------------------
// The shared observation run
// ---------------------------------------------------------------------------

// Fixture names. Every one carries the fixturePrefix.
const (
	// warm-up: creates __transaction_state and __consumer_offsets so that a
	// scenario whose own fixture is missing fails on its assertion rather than
	// on a preflight that could not find the transaction-state log.
	warmupTxn   = fixturePrefix + "warmup-txn"
	warmupOut   = fixturePrefix + "warmup-out"
	warmupIn    = fixturePrefix + "warmup-in"
	warmupGroup = fixturePrefix + "warmup-cg"

	// AE1 transitive union: two transactions overlapping on unionShared.
	unionTxnA   = fixturePrefix + "union-txn-a"
	unionTxnB   = fixturePrefix + "union-txn-b"
	unionAlpha  = fixturePrefix + "union-alpha"
	unionShared = fixturePrefix + "union-shared"
	unionGamma  = fixturePrefix + "union-gamma"

	// AE2 isolated topic: a transaction touching exactly one topic.
	soloTxn   = fixturePrefix + "solo-txn"
	soloTopic = fixturePrefix + "solo-topic"

	// AE3 Streams naming recovery. streamsTxn follows the Kafka Streams
	// convention "<application.id>-<taskId>" over streamsGroup, which is what
	// lets enrichment join the group's committed input to the transaction's
	// produced output.
	streamsGroup = fixturePrefix + "streams-app"
	streamsTxn   = streamsGroup + "-0_0"
	streamsIn    = fixturePrefix + "streams-in"
	streamsOut   = fixturePrefix + "streams-out"

	// AE4 exact correlation where naming fails. The group id and the
	// transactional id are deliberately unrelated words: "orders" shares no
	// prefix with "quibble", so correlateByStreamsConvention cannot match them
	// and only the record-batch producer id can.
	ordersTxn   = fixturePrefix + "orders-txn-77"
	ordersGroup = fixturePrefix + "quibble-consumer"
	ordersIn    = fixturePrefix + "orders-in"
	ordersOut   = fixturePrefix + "orders-out"

	// AE5 internal-topic filtering: a second, entirely unrelated exactly-once
	// workload that also commits its offsets transactionally.
	billingTxn   = fixturePrefix + "billing-txn-88"
	billingGroup = fixturePrefix + "wobble-consumer"
	billingIn    = fixturePrefix + "billing-in"
	billingOut   = fixturePrefix + "billing-out"

	// AE8 audit growth-only. One transactional id, one output topic, several
	// transactions: an unchanging topic set observed over and over.
	repeatTxn   = fixturePrefix + "repeat-txn"
	repeatTopic = fixturePrefix + "repeat-only"

	// repeatRuns is how many transactions the repeat fixture runs. Each writes
	// at least an Ongoing and a PrepareCommit record to the transaction-state
	// log, and both carry the footprint — so the reader observes this topic set
	// many times over.
	repeatRuns = 3

	// Aborted transaction. Its consumed input must never be credited.
	abortedTxn   = fixturePrefix + "aborted-txn"
	abortedGroup = fixturePrefix + "abort-consumer"
	abortedIn    = fixturePrefix + "aborted-in"
	abortedOut   = fixturePrefix + "aborted-out"

	// AE6/AE7. A transaction-state topic name that does not exist and must not
	// come to exist.
	ghostTxnStateTopic = fixturePrefix + "ghost-txn-state"

	// Broker restart. Transactions are produced in a phase before the restart
	// and a phase after it, with deterministic ids so the run's output can be
	// checked as a ledger.
	restartPhaseTxns = 3
)

// TestReaderResumesAcrossBrokerRestart is R11's broker half: restarting the
// broker mid-run leaves the reader re-resolving and resuming from its last
// offset, with no gap and no duplicate group.
//
// The assertions are LEDGER-based, and deliberately so. The obvious ones are
// all satisfied by a reader that simply died at the restart: the accumulator is
// in-memory and append-only, so "nothing observed before the restart was lost"
// holds trivially for a reader that never resumed, and "no duplicate group"
// holds most easily of all for a reader that stopped. Only transactions
// produced strictly AFTER the restart distinguish recovery from a silent stall,
// so every phase is enumerated and every id must be present.
func TestReaderResumesAcrossBrokerRestart(t *testing.T) {
	awaitBrokerUp(t)
	// The transaction-state log does not exist on a fresh cluster — the
	// coordinator creates it on the first transaction — so it has to be brought
	// into being before the command can be asked to read it.
	ensureInternalTopics(t)

	pre := restartFixtures(t, "pre")
	post := restartFixtures(t, "post")
	createTopics(t, append(topicsOf(pre), topicsOf(post)...)...)

	proc := startKCP(t,
		"--source-bootstrap", bootstrapAddr,
		"--use-unauthenticated-plaintext",
		"--duration", "150s",
		"--interval", "5s",
		"--out", outFile,
		"--stats-out", statsFile,
		"--audit-log-out", auditFile,
	)
	proc.awaitObserving(t)

	for _, f := range pre {
		produceTxn(t, f)
	}
	// Let the pre-restart phase be read before the broker goes away, so a
	// failure afterwards is unambiguously about resumption.
	time.Sleep(10 * time.Second)

	restartBroker(t)

	// The reader's backoff is capped at five seconds and a metadata refresh has
	// to follow the restart, so give it room to re-resolve before judging it on
	// records produced after.
	time.Sleep(10 * time.Second)
	for _, f := range post {
		produceTxn(t, f)
	}

	r := proc.wait(t)
	r.requireSucceeded(t)

	// Non-vacuity, and the whole point of the scenario: the restart must
	// actually have disrupted the reader. A run that sailed through without a
	// single transport or leadership error did not exercise recovery, and the
	// ledger below would then be proving nothing.
	require.NotNil(t, r.stats, "the run wrote no stats document, so the disruption cannot be confirmed")
	disruptions := r.stats.Tail.TransportErrors + r.stats.Tail.LeadershipErrors
	require.Positive(t, disruptions,
		"the broker restart caused no transport or leadership error, so the reader was never disrupted and this test proves nothing (tail=%+v)", r.stats.Tail)

	// The ledger. Every transaction from both phases must be in the document —
	// the post-restart ones are what separate a resumed reader from a dead one.
	for _, f := range append(append([]txnFixture{}, pre...), post...) {
		g := r.groupWith(f.Produce[0])
		require.NotNil(t, g, "transaction %s is missing from the output entirely\n%s", f.TxnID, r.describe())
		assert.ElementsMatch(t, f.Produce, g.Topics,
			"transaction %s did not produce its own group of topics\n%s", f.TxnID, r.describe())
		assert.Contains(t, g.TransactionalIDs, f.TxnID,
			"the group is not credited to transaction %s\n%s", f.TxnID, r.describe())
	}

	// No duplicate group: a reconnect that re-read an offset range would show up
	// as the same topic appearing in two groups, or as a repeated group name.
	seenNames := map[string]int{}
	for _, g := range r.doc.Groups {
		seenNames[g.Name]++
	}
	for name, n := range seenNames {
		assert.Equal(t, 1, n, "group name %s appears %d times", name, n)
	}
	for _, f := range append(append([]txnFixture{}, pre...), post...) {
		var in []string
		for _, g := range r.doc.Groups {
			for _, tp := range g.Topics {
				if tp == f.Produce[0] {
					in = append(in, g.Name)
				}
			}
		}
		assert.Len(t, in, 1, "topic of %s appears in %v, so the reconnect duplicated it", f.TxnID, in)
	}

	// No gap: every partition assigned at startup must still be running at the
	// end. A partition loop that gave up on the restart reads as zero lag and is
	// otherwise indistinguishable from a healthy idle one.
	assert.Equal(t, r.stats.Tail.PartitionsAssigned, r.stats.Tail.PartitionsRunning,
		"%d of %d partitions stopped across the restart, so part of the window went unobserved",
		r.stats.Tail.PartitionsAssigned-r.stats.Tail.PartitionsRunning, r.stats.Tail.PartitionsAssigned)
	assert.Zero(t, r.stats.Tail.DecodeErrors, "records failed to decode across the restart")
}

// restartFixtures builds one phase's transactions, each writing its own pair of
// topics so it forms its own group and can be found by name in the output.
func restartFixtures(t *testing.T, phase string) []txnFixture {
	t.Helper()
	out := make([]txnFixture, 0, restartPhaseTxns)
	for i := 0; i < restartPhaseTxns; i++ {
		base := fmt.Sprintf("%srestart-%s-%d", fixturePrefix, phase, i)
		out = append(out, txnFixture{
			TxnID:   base + "-txn",
			Produce: []string{base + "-a", base + "-b"},
		})
	}
	return out
}

// topicsOf flattens the topics a set of fixtures produces to.
func topicsOf(fs []txnFixture) []string {
	var out []string
	for _, f := range fs {
		out = append(out, f.Produce...)
	}
	return out
}

// TestPreflightDeniesUnreadableTxnStateAndWritesNothing is AE6: when the
// transaction-state log cannot be read the command fails fast, exits non-zero,
// and leaves no artifact behind.
//
// Writing nothing is the half that matters. A zero-group txn-discovery.yaml
// from a run that never read anything is indistinguishable from a genuine
// finding that the cluster has no transactional coupling — and an operator
// acting on it would migrate coupled topics apart.
//
// The denial is provoked by naming a topic that does not exist rather than by
// revoking an ACL: this broker runs no authorizer, so an existing-but-forbidden
// __transaction_state is not expressible here. See the note in the package
// README of scenarios — the ACL branch of the preflight is covered by U11's
// unit tests instead.
func TestPreflightDeniesUnreadableTxnStateAndWritesNothing(t *testing.T) {
	require.False(t, topicExists(t, ghostTxnStateTopic),
		"the fixture topic already exists, so the preflight would not be denied")

	r := runKCP(t,
		"--source-bootstrap", bootstrapAddr,
		"--use-unauthenticated-plaintext",
		"--duration", "30s",
		"--interval", "5s",
		"--txn-state-topic", ghostTxnStateTopic,
		"--out", outFile,
		"--stats-out", statsFile,
		"--audit-log-out", auditFile,
	)

	assert.NotEqual(t, 0, r.exitCode, "an unreadable transaction-state log did not fail the run\n%s", r.describe())

	// It must fail FAST — at the preflight, not after holding the window open.
	// The run above asked for 30 seconds; a preflight that ran anyway would be
	// visible as a summary.
	assert.NotContains(t, r.stdout, "Transaction discovery summary",
		"the run printed a summary despite failing preflight\n%s", r.describe())

	// The message has to tell the operator which of the two causes it was. An
	// unknown topic is a typo in a flag; an authorization failure is a missing
	// grant. Reporting both the same way sends them to the wrong fix.
	combined := r.stdout + r.stderr
	assert.Contains(t, combined, "--txn-state-topic",
		"the failure does not name the flag responsible\n%s", r.describe())
	assert.Contains(t, combined, "does not exist",
		"the failure does not distinguish an unknown topic from an authorization problem\n%s", r.describe())

	for _, name := range []string{outFile, statsFile, auditFile} {
		assert.NotContains(t, r.files, name,
			"a failed preflight still wrote %s (files: %v)", name, r.files)
	}
	// kcp.log is the exception, and its presence is also what proves the
	// assertions above are about a run that actually happened.
	assert.Contains(t, r.files, logFile, "the run wrote no kcp.log, so it may not have run at all")
}

// TestNoTopicCreationOnProbe is AE7: probing a topic that does not exist must
// not bring it into existence, on a broker whose own auto-create is ON.
//
// sarama defaults Metadata.AllowAutoTopicCreation to true and kcp never set it,
// so any topic-scoped metadata request could materialise the topic it asked
// about. For a command whose entire job is to probe internal topic names by
// name, that turns a read-only discovery tool into one that mutates the cluster
// it is inspecting. U1 closed it in the shared client construction; this is the
// proof against a real broker.
func TestNoTopicCreationOnProbe(t *testing.T) {
	// Non-vacuity, and the reason compose leaves auto-create ON. If the broker
	// would refuse to create a topic anyway, the whole scenario proves nothing
	// about kcp's client-side guard.
	requireBrokerAutoCreatesTopics(t)

	require.False(t, topicExists(t, ghostTxnStateTopic),
		"the probe target already exists, so its absence afterwards would prove nothing")

	r := runKCP(t,
		"--source-bootstrap", bootstrapAddr,
		"--use-unauthenticated-plaintext",
		"--duration", "30s",
		"--interval", "5s",
		"--txn-state-topic", ghostTxnStateTopic,
		"--out", outFile,
	)
	require.NotEqual(t, 0, r.exitCode, "the probe did not fail, so the topic may simply have been created and read\n%s", r.describe())

	// Checked with an independent admin client, and by listing rather than by
	// asking about the name: a topic-scoped metadata request is exactly the
	// thing that can create a topic, so a verifier that asked that way could
	// create the very topic it is checking for.
	assert.False(t, topicExists(t, ghostTxnStateTopic),
		"probing a nonexistent topic created it on a broker with auto-create enabled")
}

// requireBrokerAutoCreatesTopics proves the broker under test really does
// create a topic in response to a topic-scoped metadata request, by doing
// exactly that with a client that permits it.
func requireBrokerAutoCreatesTopics(t *testing.T) {
	t.Helper()

	canary := fmt.Sprintf("%sautocreate-canary-%d", fixturePrefix, time.Now().UnixNano())
	require.False(t, topicExists(t, canary), "the canary topic already exists")

	cfg := baseConfig()
	cfg.Metadata.AllowAutoTopicCreation = true
	client, err := sarama.NewClient([]string{bootstrapAddr}, cfg)
	require.NoError(t, err, "failed to create the auto-create canary client")
	defer func() { _ = client.Close() }()

	// A topic-scoped metadata request. The error is ignored: an unknown topic
	// is reported as an error on the same response that causes the creation.
	_ = client.RefreshMetadata(canary)

	require.Eventually(t, func() bool { return topicExists(t, canary) }, 20*time.Second, 500*time.Millisecond,
		"the broker did NOT auto-create a topic from a metadata request, so the no-auto-create scenario would be vacuous — check KAFKA_AUTO_CREATE_TOPICS_ENABLE in docker-compose.yml")
}

// TestSummaryCarriesNoTopicNames is R16's abuse case: the terminal reports
// counts, never names.
//
// A topic name like acme.payments.settlements discloses a customer's business
// structure, and a summary is the thing that gets pasted into a ticket or
// screen-shared in a workshop. The names live in the 0600 YAML instead.
func TestSummaryCarriesNoTopicNames(t *testing.T) {
	r := sharedRun(t)

	// Non-vacuity: there must have been names available to leak. Against an
	// empty run this assertion would hold for the wrong reason.
	require.NotEmpty(t, r.observedTopics(), "the run discovered no topics, so this test proves nothing")
	require.Contains(t, strings.Join(r.observedTopics(), ","), fixturePrefix,
		"the run's own artifacts carry no fixture-prefixed name, so this test proves nothing")

	assert.NotContains(t, r.stdout, fixturePrefix,
		"a topic name or transactional id reached the terminal summary\n--- STDOUT ---\n%s", r.stdout)
	assert.NotContains(t, r.stderr, fixturePrefix,
		"a topic name or transactional id reached stderr\n--- STDERR ---\n%s", r.stderr)

	// The summary is still expected to say something: an empty stdout would
	// satisfy the assertions above and report nothing to the operator.
	assert.Contains(t, r.stdout, "Transaction discovery summary", "the summary was not printed at all")
	assert.Contains(t, r.stdout, "Transaction groups", "the group table was not printed at all")
}

// TestKcpLogCarriesNoNames is the same abuse case applied to the fourth sink.
//
// kcp.log's file leg is unconditionally Debug+ with no level control, and it is
// the file operators attach to support tickets. A single Debug line carrying a
// topic name defeats the counts-only terminal entirely — and under --verbose
// those same lines reach the console.
func TestKcpLogCarriesNoNames(t *testing.T) {
	r := sharedRun(t)

	require.NotEmpty(t, r.kcpLog, "the run wrote no kcp.log, so this test proves nothing")
	require.Contains(t, r.kcpLog, "transaction discovery",
		"kcp.log does not contain this command's own log narrative, so it is not the file under test")

	assert.NotContains(t, r.kcpLog, fixturePrefix,
		"a topic name or transactional id reached kcp.log\n--- KCP.LOG ---\n%s", r.kcpLog)
}

// TestArtifactsAreOwnerOnly is the artifact-permission abuse case: all three
// files are written 0600, verified by stat rather than by inspection.
//
// The YAML and the audit log enumerate customer topic names, and the stats
// document is included because it is not a counts-only file — it carries the
// full per-transaction footprints, transactional ids and all. The POC wrote
// that one 0644; the mode must not port across.
func TestArtifactsAreOwnerOnly(t *testing.T) {
	r := sharedRun(t)

	for _, name := range []string{outFile, statsFile, auditFile} {
		mode, ok := r.modes[name]
		require.True(t, ok, "%s was never written, so its mode cannot be checked (files: %v)", name, r.files)
		assert.Equal(t, os.FileMode(0600), mode,
			"%s is mode %#o, not 0600: it enumerates customer topic names", name, mode)
	}
}

// TestAbortedTransactionInputIsNotGrouped is the abuse case behind the abort
// filter: an offset commit belonging to a rolled-back transaction must not be
// credited as a consumed input.
//
// ReadCommitted does NOT remove aborted records at the wire level — it bounds
// the fetch at the last stable offset and attaches an AbortedTransactions list,
// leaving the discarding to the client. A raw fetch loop inherits none of that
// from sarama's consumer, so without an explicit filter the aborted commit
// reads exactly like a committed one. Zombie-fenced and rebalance-aborted
// transactions are routine on an exactly-once cluster, so this is the normal
// case, not a corner one: the consequence would be a migration group built
// around a topic the application never actually consumed.
func TestAbortedTransactionInputIsNotGrouped(t *testing.T) {
	r := sharedRun(t)

	// The premise first. A PrepareAbort record carries a footprint, so the
	// aborted transaction's PRODUCED topic must be visible — that is what proves
	// the transaction was observed at all. Without this guard the test would
	// pass just as well against a fixture that never ran.
	require.Contains(t, r.observedTopics(), abortedOut,
		"the aborted transaction was never observed, so this test cannot show its input was filtered\n%s", r.describe())

	assert.NotContains(t, r.observedTopics(), abortedIn,
		"the consumed input of an ABORTED transaction was credited as a real input\n%s", r.describe())
	assert.Nil(t, r.groupWith(abortedIn),
		"the consumed input of an ABORTED transaction was grouped\n%s", r.describe())
}

// TestAuditLogExplainsAUnion is F2, the flow the audit log exists for: an
// operator looking at a group in txn-discovery.yaml asks why two topics ended
// up together, filters the trace by one of the topic names, and gets back the
// transaction that coupled them.
//
// The terminal deliberately reports counts only, so this file is the ONLY
// artifact that answers "why" — and by the time the question is asked the
// __transaction_state records behind the answer may well have been compacted
// away, which is what makes reconstructing it after the fact impossible.
func TestAuditLogExplainsAUnion(t *testing.T) {
	r := sharedRun(t)
	require.NotEmpty(t, r.audit, "the run wrote no audit log at all\n%s", r.describe())

	lines := r.auditFor(unionShared)
	require.NotEmpty(t, lines,
		"filtering the audit trail by a grouped topic name yielded nothing, so a union cannot be traced")

	// Both transactions that touched the shared topic must be recoverable from
	// the trace: one line naming only one of them would explain half a union.
	var txns []string
	for _, l := range lines {
		txns = append(txns, l.TxnID)
	}
	assert.Contains(t, txns, unionTxnA, "the trace does not name the first coupling transaction: %+v", lines)
	assert.Contains(t, txns, unionTxnB, "the trace does not name the second coupling transaction: %+v", lines)

	for _, l := range lines {
		assert.NotEmpty(t, l.Source, "an audit line does not say which phase observed the edge: %+v", l)
		assert.NotEmpty(t, l.Added, "a growth line records no added topic: %+v", l)
		assert.False(t, l.Timestamp.IsZero(), "an audit line carries no timestamp: %+v", l)
	}
}

// TestAuditLogRecordsGrowthOnly is AE8: a transaction observed repeatedly with
// an unchanged topic set produces exactly ONE audit line, written when the set
// was first populated.
//
// R19 is not deduplication for tidiness. The audit log exists so an operator
// can find the edge that coupled two topics; a line per observation would bury
// those edges under thousands of restatements of what was already known, and on
// a run measured in hours the file would grow with elapsed time rather than
// with the number of distinct couplings.
func TestAuditLogRecordsGrowthOnly(t *testing.T) {
	r := sharedRun(t)
	require.NotEmpty(t, r.audit, "the run wrote no audit log at all\n%s", r.describe())

	lines := r.auditForTxn(repeatTxn)
	require.Len(t, lines, 1,
		"a transaction observed %d times over with an unchanged topic set wrote %d audit lines, not one: %+v",
		repeatRuns, len(lines), lines)

	assert.Equal(t, []string{repeatTopic}, lines[0].Added,
		"the single line does not report the topic it added")
	assert.Equal(t, []string{repeatTopic}, lines[0].Topics,
		"the single line does not report the resulting topic set")
}

// TestUnrelatedExactlyOnceWorkloadsStaySeparate is AE5: two exactly-once
// workloads that share nothing but __consumer_offsets must stay in separate
// groups.
//
// Every exactly-once application on a cluster commits offsets to that one
// internal topic, so it appears in every such transaction's footprint. Union it
// like an ordinary topic and the transitive closure chains the entire estate
// into a single group — a "result" that says migrate everything at once, which
// is no result at all. Internal topics are therefore dropped BEFORE the union,
// and this is the scenario that notices if that ordering is ever reversed.
func TestUnrelatedExactlyOnceWorkloadsStaySeparate(t *testing.T) {
	r := sharedRun(t)

	orders := r.groupWith(ordersOut)
	require.NotNil(t, orders, "the first workload's output topic is in no group\n%s", r.describe())
	billing := r.groupWith(billingOut)
	require.NotNil(t, billing, "the second workload's output topic is in no group\n%s", r.describe())

	assert.NotEqual(t, orders.Name, billing.Name,
		"two unrelated exactly-once workloads chained into one group through __consumer_offsets\n%s", r.describe())
	assert.NotContains(t, orders.Topics, billingIn, "the second workload's input leaked into the first workload's group")
	assert.NotContains(t, orders.Topics, billingOut, "the second workload's output leaked into the first workload's group")
	assert.NotContains(t, billing.Topics, ordersIn, "the first workload's input leaked into the second workload's group")
	assert.NotContains(t, billing.Topics, ordersOut, "the first workload's output leaked into the second workload's group")

	// The internal topic every exactly-once app shares must not be reported as
	// something to migrate at all.
	assert.NotContains(t, r.observedTopics(), "__consumer_offsets",
		"an internal topic was reported as migratable\n%s", r.describe())
}

// TestProducerIDRecoversConsumedInput is AE4: a non-Streams exactly-once
// application whose group id bears no naming relationship to its transactional
// id still has its consumed input folded into the produced group, joined on the
// record-batch producer id.
//
// This is the scenario the whole raw-Broker.Fetch decision exists for: sarama's
// consumer API discards the batch header, so nothing above it can see the
// producer id that makes this join possible.
func TestProducerIDRecoversConsumedInput(t *testing.T) {
	r := sharedRun(t)

	// Guard the premise rather than assume it: if the names DID correlate, the
	// test would pass through the naming path and prove nothing about
	// producer-id correlation.
	require.False(t, strings.HasPrefix(ordersTxn, ordersGroup+"-"),
		"the fixture names correlate by the Streams convention, so this test cannot isolate producer-id correlation")

	g := r.groupWith(ordersOut)
	require.NotNil(t, g, "the produced output topic is in no group\n%s", r.describe())

	assert.Contains(t, g.Topics, ordersIn,
		"the consumed input was not recovered by producer-id correlation\n%s", r.describe())
	assert.True(t, g.ReadProcessWrite,
		"a group whose inputs came from a transactional offset commit is not marked read-process-write\n%s", r.describe())
	assert.Contains(t, g.Warning, "producer-id correlation",
		"the group's warning does not credit producer-id correlation\n%s", r.describe())
}

// TestStreamsNamingRecoversConsumedInput is AE3: a transaction's footprint
// names only what it PRODUCED, so the consumed input has to be recovered from
// the consumer group whose id the transactional id is derived from.
func TestStreamsNamingRecoversConsumedInput(t *testing.T) {
	r := sharedRun(t)

	g := r.groupWith(streamsOut)
	require.NotNil(t, g, "the produced output topic is in no group\n%s", r.describe())

	assert.Contains(t, g.Topics, streamsIn,
		"the consumed input was not folded into the produced group by the Streams naming convention\n%s", r.describe())
	assert.True(t, g.ReadProcessWrite,
		"a group whose inputs came from a consumer group is not marked read-process-write\n%s", r.describe())
	assert.Contains(t, g.Warning, "naming convention",
		"the group's warning does not name the mechanism that recovered its inputs\n%s", r.describe())
}

// TestIsolatedTopicIsIndividual is AE2: a topic that only ever appeared alone
// in a transaction is reported as individually migratable, not as a group.
//
// The negative half is the load-bearing one. A one-topic "group" would satisfy
// "the topic appears in the output" while telling an operator to batch a
// migration they do not need to batch.
func TestIsolatedTopicIsIndividual(t *testing.T) {
	r := sharedRun(t)

	assert.True(t, r.isIndividual(soloTopic),
		"a topic seen alone in a transaction was not reported as individually migratable\n%s", r.describe())
	assert.Nil(t, r.groupWith(soloTopic),
		"a topic seen alone in a transaction was reported as coupled to others\n%s", r.describe())
}

// TestTransitiveUnion is AE1: coupling is transitive, so two transactions that
// share a single topic collapse into ONE group of three rather than two groups
// of two.
func TestTransitiveUnion(t *testing.T) {
	r := sharedRun(t)

	g := r.groupWith(unionShared)
	require.NotNil(t, g, "the topic shared by two transactions is in no group\n%s", r.describe())

	assert.ElementsMatch(t, []string{unionAlpha, unionShared, unionGamma}, g.Topics,
		"the two transactions sharing a topic did not collapse into one group of three\n%s", r.describe())
	assert.ElementsMatch(t, []string{unionTxnA, unionTxnB}, g.TransactionalIDs,
		"the group does not credit both transactions that formed it\n%s", r.describe())

	// The two non-shared topics must be in the SAME group, not merely in some
	// group each: two groups of two would satisfy a weaker assertion.
	assert.Same(t, g, r.groupWith(unionAlpha), "the first transaction's topic landed in a different group")
	assert.Same(t, g, r.groupWith(unionGamma), "the second transaction's topic landed in a different group")
}

// Shared-window timings. The live rounds finish at roughly liveRounds *
// liveRoundGap into a sharedWindow-long run, leaving the rest of the window as
// catch-up time for the transaction-state reader.
const (
	sharedWindow = 60 * time.Second
	liveRounds   = 4
	liveRoundGap = 6 * time.Second
)

var (
	sharedOnce sync.Once
	shared     *runResult
)

// sharedRun performs one observation window covering every scenario whose
// fixtures can coexist, and returns its artifacts.
//
// One window rather than one per scenario, because each window costs its full
// --duration. The fixtures are independent by construction: distinct topic and
// transactional-id namespaces, so grouping cannot join them and each test's
// assertion is about its own names only. The scenarios that cannot share a run
// — a preflight that must fail, a broker that must restart — have their own.
func sharedRun(t *testing.T) *runResult {
	t.Helper()
	sharedOnce.Do(func() { shared = doSharedRun(t) })
	require.NotNil(t, shared, "the shared discovery run did not complete; see the first failing test in this package")
	return shared
}

func doSharedRun(t *testing.T) *runResult {
	t.Helper()

	seedBeforeWindow(t)

	proc := startKCP(t,
		"--source-bootstrap", bootstrapAddr,
		"--use-unauthenticated-plaintext",
		"--duration", sharedWindow.String(),
		"--interval", "5s",
		"--out", outFile,
		"--stats-out", statsFile,
		"--audit-log-out", auditFile,
	)
	proc.awaitObserving(t)

	// Produced in rounds rather than once, and finishing well before the window
	// closes. Producer-id correlation needs BOTH halves of a join to have been
	// observed — the transactional offset commit on the consumer-offsets log,
	// which the tail reads from latest, and the producer-id-to-transaction
	// mapping from the transaction-state log, which the reader works towards
	// from the beginning across every partition. A fixture produced close to the
	// end can have its commit seen and its transaction not yet reached, and the
	// final flush cannot resolve what the catalog does not hold. Repeating an
	// identical transaction cannot create a second group, so the redundancy is
	// free; the quiet tail of the window is what makes the correlation land.
	for round := 0; round < liveRounds; round++ {
		seedDuringWindow(t)
		time.Sleep(liveRoundGap)
	}

	res := proc.wait(t)
	res.requireSucceeded(t)
	return res
}

// seedBeforeWindow produces the fixtures the transaction-state log carries.
//
// They can precede the run because that log is read from the BEGINNING: a
// transaction committed before the window opened is still discovered.
func seedBeforeWindow(t *testing.T) {
	t.Helper()

	ensureInternalTopics(t)

	// AE1. Two transactions overlapping on unionShared, which the union-find
	// must collapse into one group of three.
	createTopics(t, unionAlpha, unionShared, unionGamma)
	produceTxn(t, txnFixture{TxnID: unionTxnA, Produce: []string{unionAlpha, unionShared}})
	produceTxn(t, txnFixture{TxnID: unionTxnB, Produce: []string{unionShared, unionGamma}})

	// AE2. One transaction, one topic: nothing to couple it to.
	createTopics(t, soloTopic)
	produceTxn(t, txnFixture{TxnID: soloTxn, Produce: []string{soloTopic}})

	// AE3. The offsets are committed OUTSIDE the transaction, so producer-id
	// correlation cannot see them and only the naming convention can join the
	// group's input to the transaction's output.
	createTopics(t, streamsIn, streamsOut)
	commitGroupOffsets(t, streamsGroup, streamsIn)
	produceTxn(t, txnFixture{TxnID: streamsTxn, Produce: []string{streamsOut}})

	// The during-window fixtures' topics are created here so the observation
	// window carries transactions rather than topic-creation churn. An unused
	// topic is invisible to discovery, which reports only what it observed.
	// AE8. One transactional id writing the SAME single topic several times
	// over. Each transaction contributes at least an Ongoing and a
	// PrepareCommit record and both carry the footprint, so the reader observes
	// this set many times — and exactly one of those observations grew it.
	//
	// One topic on one partition, deliberately. A transaction's Ongoing record
	// is written as partitions are added to it, so a two-topic fixture could
	// legitimately produce a footprint of {a} followed by {a,b} — two growth
	// events, and an exact line count at the mercy of broker timing.
	createTopics(t, repeatTopic)
	for i := 0; i < repeatRuns; i++ {
		produceTxn(t, txnFixture{TxnID: repeatTxn, Produce: []string{repeatTopic}})
	}

	createTopics(t, ordersIn, ordersOut, billingIn, billingOut, abortedIn, abortedOut)
}

// seedDuringWindow produces the fixtures that must land inside the observation
// window because the consumer-offsets tail starts at latest.
func seedDuringWindow(t *testing.T) {
	t.Helper()

	// AE4. The offsets are committed INSIDE the transaction, so the resulting
	// __consumer_offsets record is transactional and carries the producer id
	// that ties it back to ordersTxn.
	produceTxn(t, txnFixture{
		TxnID:        ordersTxn,
		Produce:      []string{ordersOut},
		ConsumeTopic: ordersIn,
		Group:        ordersGroup,
	})

	// AE5. A second exactly-once workload sharing nothing with the first except
	// the __consumer_offsets both commit into.
	produceTxn(t, txnFixture{
		TxnID:        billingTxn,
		Produce:      []string{billingOut},
		ConsumeTopic: billingIn,
		Group:        billingGroup,
	})

	// An exactly-once workload that rolls back. Its offset commit still reaches
	// __consumer_offsets — that is what the abort filter has to discard.
	produceTxn(t, txnFixture{
		TxnID:        abortedTxn,
		Produce:      []string{abortedOut},
		ConsumeTopic: abortedIn,
		Group:        abortedGroup,
		Abort:        true,
	})
}
