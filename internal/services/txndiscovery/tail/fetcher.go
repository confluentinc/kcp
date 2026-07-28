package tail

import (
	"encoding/binary"
	"errors"
	"fmt"
	"sort"
	"sync"

	"github.com/IBM/sarama"
)

const (
	// fetchAPIKey is the Kafka protocol API key for Fetch.
	fetchAPIKey int16 = 1
	// MinFetchVersion is the floor for a fetch request. v4 is the first
	// version carrying the isolation level, the last stable offset and the
	// aborted-transaction list, all three of which this reader needs.
	MinFetchVersion int16 = 4
	// DefaultFetchVersionCeiling is the highest version this reader will ask
	// for. v11 is what sarama's own consumer tops out at for the pinned
	// version, so nothing above it is exercised anywhere in sarama.
	DefaultFetchVersionCeiling int16 = 11
)

// ErrFetchVersionUnsupported reports a broker that cannot serve a fetch at the
// minimum version this reader needs. It is fatal at startup rather than
// retriable: no amount of waiting will make an old broker grow the fields.
var ErrFetchVersionUnsupported = errors.New("broker does not support a new enough Fetch API version")

// negotiateFetchVersion picks the Fetch API version to use against one broker:
// the lower of the local ceiling and the broker's advertised maximum, floored
// at MinFetchVersion (KTD3).
//
// Reading the sarama client's configured Kafka version instead would pin the
// request at whatever that constant happens to be — v11 for kcp — which is
// worse against an older broker than simply asking what it supports.
func negotiateFetchVersion(keys []sarama.ApiVersionsResponseKey, ceiling int16) (int16, error) {
	advertised := int16(-1)
	for _, k := range keys {
		if k.ApiKey == fetchAPIKey {
			advertised = k.MaxVersion
			break
		}
	}
	if advertised < 0 {
		return 0, fmt.Errorf("%w: broker did not advertise the Fetch API at all", ErrFetchVersionUnsupported)
	}
	if advertised < MinFetchVersion {
		return 0, fmt.Errorf("%w: broker advertises a maximum Fetch API version of %d, but reading committed transactional data needs at least v%d (isolation level, last stable offset and the aborted-transaction list)", ErrFetchVersionUnsupported, advertised, MinFetchVersion)
	}
	version := ceiling
	if advertised < version {
		version = advertised
	}
	if version < MinFetchVersion {
		version = MinFetchVersion
	}
	return version, nil
}

// buildFetchRequest turns a FetchSpec into a wire request. sarama exposes no
// helper for the version ladder, so it is hand-rolled here to mirror the
// unexported brokerConsumer.fetchNewMessages: MaxBytes from v3, the isolation
// level from v4, and the fetch-session fields from v7.
func buildFetchRequest(spec FetchSpec) *sarama.FetchRequest {
	req := &sarama.FetchRequest{
		Version:     spec.Version,
		MaxWaitTime: spec.MaxWaitMS,
		MinBytes:    spec.MinBytes,
		MaxBytes:    spec.MaxBytes,
		Isolation:   spec.Isolation,
	}
	if spec.Version >= 7 {
		// KIP-227 incremental fetch sessions are not implemented here. Session
		// id 0 with epoch -1 tells the broker not to open a session whose id
		// this reader would only ignore.
		req.SessionID = 0
		req.SessionEpoch = -1
	}
	// A leader epoch of -1 disables the epoch check, so a stale cached epoch
	// cannot fail the fetch after a leader move.
	req.AddBlock(spec.Topic, spec.Partition, spec.Offset, spec.MaxPartitionBytes, -1)
	return req
}

// SaramaClient is the production Client, backed by a sarama.Client. It does
// not own that client and never closes it.
type SaramaClient struct {
	client  sarama.Client
	ceiling int16

	mu sync.Mutex
	// brokers maps a broker id to the open connection Leader resolved, so
	// Fetch can route to it without re-resolving.
	brokers map[int32]*sarama.Broker
	// versions caches the negotiated Fetch version per broker, since a
	// broker's advertised ceiling does not change while it is up.
	versions map[int32]int16
}

// NewSaramaClient adapts a sarama.Client to the seam. A ceiling of zero or
// less uses DefaultFetchVersionCeiling.
func NewSaramaClient(c sarama.Client, ceiling int16) *SaramaClient {
	if ceiling <= 0 {
		ceiling = DefaultFetchVersionCeiling
	}
	return &SaramaClient{
		client:   c,
		ceiling:  ceiling,
		brokers:  make(map[int32]*sarama.Broker),
		versions: make(map[int32]int16),
	}
}

// Partitions lists a topic's partition ids.
func (s *SaramaClient) Partitions(topic string) ([]int32, error) {
	return s.client.Partitions(topic)
}

// Offset resolves a partition's earliest or latest offset.
func (s *SaramaClient) Offset(topic string, partition int32, pos StartPosition) (int64, error) {
	which := sarama.OffsetOldest
	if pos == StartLatest {
		which = sarama.OffsetNewest
	}
	return s.client.GetOffset(topic, partition, which)
}

// Leader resolves the current leader of a partition and the Fetch version
// negotiated against it.
//
// Error text deliberately names the partition and never the topic: every
// command error is logged to kcp.log, which operators attach to support
// tickets, and a topic name there discloses customer business structure.
func (s *SaramaClient) Leader(topic string, partition int32) (Leader, error) {
	b, err := s.client.Leader(topic, partition)
	if err != nil {
		return Leader{}, fmt.Errorf("failed to resolve the leader for partition %d: %w", partition, err)
	}
	version, err := s.fetchVersion(b)
	if err != nil {
		return Leader{}, err
	}
	s.mu.Lock()
	s.brokers[b.ID()] = b
	s.mu.Unlock()
	return Leader{ID: b.ID(), Addr: b.Addr(), FetchVersion: version}, nil
}

// fetchVersion negotiates, and then remembers, the Fetch version for a broker.
func (s *SaramaClient) fetchVersion(b *sarama.Broker) (int16, error) {
	s.mu.Lock()
	if v, ok := s.versions[b.ID()]; ok {
		s.mu.Unlock()
		return v, nil
	}
	s.mu.Unlock()

	resp, err := b.ApiVersions(&sarama.ApiVersionsRequest{Version: 0})
	if err != nil {
		return 0, fmt.Errorf("failed to read the API versions advertised by broker %d: %w", b.ID(), err)
	}
	version, err := negotiateFetchVersion(resp.ApiKeys, s.ceiling)
	if err != nil {
		return 0, fmt.Errorf("broker %d cannot serve this reader: %w", b.ID(), err)
	}

	s.mu.Lock()
	s.versions[b.ID()] = version
	s.mu.Unlock()
	return version, nil
}

// RefreshMetadata forces a metadata refresh for the named topics.
func (s *SaramaClient) RefreshMetadata(topics ...string) error {
	return s.client.RefreshMetadata(topics...)
}

// Fetch performs one fetch round-trip against the broker Leader resolved.
func (s *SaramaClient) Fetch(leader Leader, spec FetchSpec) (*sarama.FetchResponse, error) {
	s.mu.Lock()
	b := s.brokers[leader.ID]
	s.mu.Unlock()
	if b == nil {
		return nil, fmt.Errorf("no open connection to broker %d", leader.ID)
	}
	resp, err := b.Fetch(buildFetchRequest(spec))
	if err != nil {
		s.discard(b)
		return nil, err
	}
	return resp, nil
}

// discard closes and forgets a broker whose round trip failed, so the next
// Leader call dials a new connection instead of reusing the broken one.
//
// This is the half of surviving a broker restart that a metadata refresh cannot
// do. A sarama Broker never recovers from a failed round trip on its own:
// neither the write path nor responseReceiver closes the socket, and once the
// response reader has latched an error every later request on that Broker fails
// with it. Nor does refreshing help by itself — client.updateMetadata keeps the
// existing *Broker whenever the address is unchanged, which is precisely what a
// broker restarting in place looks like. Closing it here is what makes the Open
// inside the next Leader call redial.
//
// Any failed fetch qualifies, not just an obviously networky one, because any
// error surfaced from a sarama round trip leaves the Broker in that latched
// state. The cost of over-discarding — on, say, a version error that never
// reached the wire — is one redial.
func (s *SaramaClient) discard(b *sarama.Broker) {
	// Already-closed brokers report ErrNotConnected, which is the state wanted.
	_ = b.Close()
	s.mu.Lock()
	delete(s.brokers, b.ID())
	// The negotiated version goes with the connection: a broker that comes back
	// may be a different build advertising a different Fetch ceiling.
	delete(s.versions, b.ID())
	s.mu.Unlock()
}

// errorClass names how the fetch loop recovers from a failed fetch.
type errorClass int

const (
	// classLeadership: the partition's leader moved. Refresh metadata, back
	// off, retry from the same offset.
	classLeadership errorClass = iota
	// classOffset: the reader's position is outside the retained range.
	// Reseek to the log start rather than retrying a dead offset forever.
	classOffset
	// classUnclassified: an error code this reader does not recognise. It is
	// counted and surfaced, never a reason to exit the loop.
	classUnclassified
)

// classifyKafkaError maps a partition-level Kafka error to its recovery.
func classifyKafkaError(kerr sarama.KError) errorClass {
	switch kerr {
	case sarama.ErrNotLeaderForPartition,
		sarama.ErrLeaderNotAvailable,
		// A stale cached leader epoch is the routine outcome of a leader move,
		// not a corruption: refresh and retry rather than give up.
		sarama.ErrFencedLeaderEpoch,
		sarama.ErrUnknownLeaderEpoch,
		sarama.ErrReplicaNotAvailable:
		return classLeadership
	case sarama.ErrOffsetOutOfRange:
		return classOffset
	default:
		return classUnclassified
	}
}

// abortTracker maintains the set of producer ids whose in-flight transaction
// was rolled back, for one fetch response.
//
// Isolation: ReadCommitted does not remove aborted records at the wire level.
// It bounds the fetch at the last stable offset and attaches the aborted list;
// discarding the records is client-side work a raw reader inherits. Missing it
// credits rolled-back transactions as real.
//
// Scope is one response, mirroring sarama's own consumer: the broker re-sends
// every aborted transaction that overlaps the records it returns, so a set
// carried across fetches could only go stale, never gain information.
type abortTracker struct {
	pending []*sarama.AbortedTransaction
	active  map[int64]struct{}
}

func newAbortTracker(list []*sarama.AbortedTransaction) *abortTracker {
	pending := make([]*sarama.AbortedTransaction, 0, len(list))
	for _, t := range list {
		if t != nil {
			pending = append(pending, t)
		}
	}
	sort.Slice(pending, func(i, j int) bool { return pending[i].FirstOffset < pending[j].FirstOffset })
	return &abortTracker{pending: pending, active: make(map[int64]struct{}, len(pending))}
}

// advanceTo marks every aborted transaction whose first offset the reader has
// now reached as active.
func (a *abortTracker) advanceTo(lastOffset int64) {
	for len(a.pending) > 0 && a.pending[0].FirstOffset <= lastOffset {
		a.active[a.pending[0].ProducerID] = struct{}{}
		a.pending = a.pending[1:]
	}
}

// isAborted reports whether records from this producer are currently inside a
// rolled-back transaction.
func (a *abortTracker) isAborted(producerID int64) bool {
	_, ok := a.active[producerID]
	return ok
}

// clear drops a producer from the aborted set, called when its abort marker is
// reached. Without it the producer's next, committed transaction would be
// filtered out too.
func (a *abortTracker) clear(producerID int64) {
	delete(a.active, producerID)
}

// controlRecordType decodes the marker carried by a control batch's single
// record key: an int16 version followed by an int16 type. sarama's own decoder
// for this is unexported, so it is hand-rolled here — and, being a
// broker-supplied binary format, it is bounds-checked rather than trusted.
func controlRecordType(rb *sarama.RecordBatch) (sarama.ControlRecordType, error) {
	if rb == nil || len(rb.Records) == 0 || rb.Records[0] == nil {
		return sarama.ControlRecordUnknown, fmt.Errorf("control batch carries no record")
	}
	key := rb.Records[0].Key
	if len(key) < 4 {
		return sarama.ControlRecordUnknown, fmt.Errorf("control record key is %d bytes, want at least 4", len(key))
	}
	switch binary.BigEndian.Uint16(key[2:4]) {
	case 0:
		return sarama.ControlRecordAbort, nil
	case 1:
		return sarama.ControlRecordCommit, nil
	default:
		// The Java client treats an unrecognised control type as ignorable
		// rather than corrupt, and so does sarama.
		return sarama.ControlRecordUnknown, nil
	}
}

// StartPosition selects where a partition's read begins.
type StartPosition int

const (
	// StartEarliest begins at the partition's log start offset.
	StartEarliest StartPosition = iota
	// StartLatest begins at the partition's next-to-be-produced offset.
	StartLatest
)

// TopicSpec names one topic to tail and where its partitions start.
type TopicSpec struct {
	Topic string
	Start StartPosition
}

// Leader identifies the broker currently leading a partition, together with
// the Fetch API version negotiated against that broker (KTD3). Resolution and
// negotiation are paired because a leader move is exactly when the negotiated
// version may change.
type Leader struct {
	ID           int32
	Addr         string
	FetchVersion int16
}

// FetchSpec fully describes one fetch round-trip. It is the value a Client
// turns into a sarama.FetchRequest, and the value a fake asserts on.
type FetchSpec struct {
	Topic             string
	Partition         int32
	Offset            int64
	Version           int16
	MaxWaitMS         int32
	MinBytes          int32
	MaxBytes          int32
	MaxPartitionBytes int32
	Isolation         sarama.IsolationLevel
}

// Client is the seam below the fetch loop (KTD2). Leader, RefreshMetadata and
// Fetch are the three operations the loop needs to survive a leader move;
// Partitions and Offset serve assignment discovery and the reseek that
// ErrOffsetOutOfRange forces.
type Client interface {
	// Partitions lists a topic's partition ids.
	Partitions(topic string) ([]int32, error)
	// Offset resolves a partition's earliest or latest offset.
	Offset(topic string, partition int32, pos StartPosition) (int64, error)
	// Leader resolves the current leader of a partition.
	Leader(topic string, partition int32) (Leader, error)
	// RefreshMetadata forces a metadata refresh for the named topics.
	RefreshMetadata(topics ...string) error
	// Fetch performs one fetch round-trip against the given leader. It takes
	// no context: sarama.Broker.Fetch is not cancellable, so the caller bounds
	// it with MaxWaitMS plus the client's read timeout instead.
	Fetch(leader Leader, spec FetchSpec) (*sarama.FetchResponse, error)
}
