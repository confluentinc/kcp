package tail

import (
	"encoding/binary"
	"fmt"
	"sort"

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
		return 0, fmt.Errorf("broker did not advertise the Fetch API, so no fetch version could be negotiated")
	}
	if advertised < MinFetchVersion {
		return 0, fmt.Errorf("broker advertises a maximum Fetch API version of %d, but reading committed transactional data needs at least v%d (isolation level, last stable offset and the aborted-transaction list)", advertised, MinFetchVersion)
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
