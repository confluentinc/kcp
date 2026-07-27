// Package txnlog decodes records from Kafka's internal __transaction_state and
// __consumer_offsets topics.
//
// There is no off-the-shelf Go decoder for these formats: TransactionLogKey /
// TransactionLogValue and the __consumer_offsets record keys are broker-internal
// record schemas, not part of the client wire protocol, so no Kafka client library
// for Go exposes them. These are hand ports of Kafka's TransactionLogKey.json,
// TransactionLogValue.json, OffsetCommitKey.json and GroupMetadataKey.json schemas.
//
// The broker is an untrusted input source here: these records are not covered by
// the stable client protocol and can drift across broker versions. Every read is
// bounds-checked and every unknown schema version is rejected, so malformed or
// truncated input produces an error rather than a panic or a large allocation.
package txnlog

import (
	"encoding/binary"
	"errors"
	"fmt"
)

var errTruncated = errors.New("txnlog: truncated record")

// Key is a decoded __transaction_state record key.
type Key struct {
	Version         int16
	TransactionalID string
}

// Value is a decoded __transaction_state record value.
type Value struct {
	Version       int16
	ProducerID    int64
	ProducerEpoch int16
	TimeoutMs     int32
	Status        TxnStatus
	Partitions    []TopicPartitions
	LastUpdateMs  int64
	StartMs       int64
}

// TopicPartitions is one topic and its partitions enrolled in the transaction.
type TopicPartitions struct {
	Topic      string
	Partitions []int32
}

// Topics returns just the topic names in the footprint.
func (v Value) Topics() []string {
	out := make([]string, 0, len(v.Partitions))
	for _, p := range v.Partitions {
		out = append(out, p.Topic)
	}
	return out
}

// TxnStatus is the transaction state stored in a log record.
type TxnStatus int8

const (
	StatusEmpty             TxnStatus = 0
	StatusOngoing           TxnStatus = 1
	StatusPrepareCommit     TxnStatus = 2
	StatusPrepareAbort      TxnStatus = 3
	StatusCompleteCommit    TxnStatus = 4
	StatusCompleteAbort     TxnStatus = 5
	StatusDead              TxnStatus = 6
	StatusPrepareEpochFence TxnStatus = 7
)

// HasFootprint reports whether records in this state carry a topic-partition set.
// Once a transaction completes (or is empty/dead) the coordinator clears the set.
func (s TxnStatus) HasFootprint() bool {
	switch s {
	case StatusOngoing, StatusPrepareCommit, StatusPrepareAbort:
		return true
	default:
		return false
	}
}

func (s TxnStatus) String() string {
	switch s {
	case StatusEmpty:
		return "Empty"
	case StatusOngoing:
		return "Ongoing"
	case StatusPrepareCommit:
		return "PrepareCommit"
	case StatusPrepareAbort:
		return "PrepareAbort"
	case StatusCompleteCommit:
		return "CompleteCommit"
	case StatusCompleteAbort:
		return "CompleteAbort"
	case StatusDead:
		return "Dead"
	case StatusPrepareEpochFence:
		return "PrepareEpochFence"
	default:
		return fmt.Sprintf("Unknown(%d)", int8(s))
	}
}

// DecodeKey decodes a __transaction_state record key. Only key version 0 exists.
func DecodeKey(b []byte) (Key, error) {
	r := newReader(b)
	var k Key
	k.Version = r.int16()
	if k.Version != 0 {
		return k, fmt.Errorf("txnlog: unsupported key version %d", k.Version)
	}
	k.TransactionalID = r.str()
	if r.err != nil {
		return k, r.err
	}
	return k, nil
}

// DecodeValue decodes a __transaction_state record value (schema v0 or v1).
//
// Two encodings exist. v0 is the classic (non-flexible) encoding; v1 is "flexible" —
// compact (varint-prefixed) strings and arrays plus a trailing tagged-fields section
// on every struct. Both are decoded, reading only the normal fields (the footprint)
// and skipping tagged fields. The leading int16 schema version selects the encoding.
func DecodeValue(b []byte) (Value, error) {
	r := newReader(b)
	var v Value
	v.Version = r.int16()
	if v.Version < 0 || v.Version > 1 {
		// This is the format-drift signal: a version we don't understand means the
		// internal schema changed, so we fail loudly rather than mis-parse.
		return v, fmt.Errorf("txnlog: unsupported value version %d", v.Version)
	}
	flexible := v.Version >= 1

	v.ProducerID = r.int64()
	v.ProducerEpoch = r.int16()
	v.TimeoutMs = r.int32()
	v.Status = TxnStatus(r.int8())
	v.Partitions = r.partitions(flexible)
	v.LastUpdateMs = r.int64()
	v.StartMs = r.int64()
	if flexible {
		r.skipTaggedFields() // top-level struct
	}

	if r.err != nil {
		return v, r.err
	}
	return v, nil
}

// --- primitive reader ---

// reader is a sticky-error, bounds-checked cursor over a broker-supplied byte
// slice. Once a read runs past the end of the buffer the error latches and every
// subsequent read is a no-op returning a zero value, so a decoder can read a whole
// record straight through and check err once at the end instead of after every
// field. Sibling decoders in this package share it.
type reader struct {
	b   []byte
	pos int
	err error
}

func newReader(b []byte) *reader { return &reader{b: b} }

func (r *reader) fail() {
	if r.err == nil {
		r.err = errTruncated
	}
}

// need reports whether n more bytes are available, latching the truncation error
// when they are not. Every sized read goes through it.
//
// The comparison is written as `n > len(r.b)-r.pos` rather than the more obvious
// `r.pos+n > len(r.b)` because n is broker-supplied and, in the flexible encoding,
// arrives from a uvarint rather than an int16 — so it reaches 2^63-1, `r.pos+n`
// overflows to a NEGATIVE number, and the obvious form reads that as "in bounds"
// and lets the caller slice with a negative index. Subtraction cannot overflow
// here: pos never exceeds len(b), which holds because every site that advances pos
// is gated by this function (the sole exception, uvarint, advances by however much
// binary.Uvarint consumed from r.b[r.pos:], which is bounded by the same length).
func (r *reader) need(n int) bool {
	if r.err != nil {
		return false
	}
	if n < 0 || n > len(r.b)-r.pos {
		r.fail()
		return false
	}
	return true
}

func (r *reader) int8() int8 {
	if !r.need(1) {
		return 0
	}
	v := int8(r.b[r.pos])
	r.pos++
	return v
}

func (r *reader) int16() int16 {
	if !r.need(2) {
		return 0
	}
	v := int16(binary.BigEndian.Uint16(r.b[r.pos:]))
	r.pos += 2
	return v
}

func (r *reader) int32() int32 {
	if !r.need(4) {
		return 0
	}
	v := int32(binary.BigEndian.Uint32(r.b[r.pos:]))
	r.pos += 4
	return v
}

func (r *reader) int64() int64 {
	if !r.need(8) {
		return 0
	}
	v := int64(binary.BigEndian.Uint64(r.b[r.pos:]))
	r.pos += 8
	return v
}

// str reads a classic (non-flexible) string: int16 length then bytes (-1 = null).
func (r *reader) str() string {
	n := int(r.int16())
	if r.err != nil || n < 0 {
		return ""
	}
	if !r.need(n) {
		return ""
	}
	s := string(r.b[r.pos : r.pos+n])
	r.pos += n
	return s
}

func (r *reader) uvarint() uint64 {
	if r.err != nil {
		return 0
	}
	v, n := binary.Uvarint(r.b[r.pos:])
	if n <= 0 {
		r.fail()
		return 0
	}
	r.pos += n
	return v
}

// compactStr reads a flexible compact string: uvarint(len+1) then bytes (0 = null).
func (r *reader) compactStr() string {
	c := r.uvarint()
	if r.err != nil || c == 0 {
		return ""
	}
	n := int(c) - 1
	if !r.need(n) {
		return ""
	}
	s := string(r.b[r.pos : r.pos+n])
	r.pos += n
	return s
}

func (r *reader) int32Slice(count int) []int32 {
	// Each int32 is 4 bytes, so a count larger than the remaining bytes is corrupt.
	// Checked before the make() so a corrupt count cannot size an allocation.
	if r.err != nil || count < 0 || count > (len(r.b)-r.pos)/4 {
		r.fail()
		return nil
	}
	out := make([]int32, 0, count)
	for i := 0; i < count; i++ {
		out = append(out, r.int32())
	}
	return out
}

// partitions reads the nullable TransactionPartitions array in either encoding.
func (r *reader) partitions(flexible bool) []TopicPartitions {
	var n int
	if flexible {
		c := r.uvarint()
		if r.err != nil || c == 0 { // 0 = null
			return nil
		}
		n = int(c) - 1
	} else {
		ln := r.int32()
		if r.err != nil || ln < 0 { // -1 = null
			return nil
		}
		n = int(ln)
	}
	// Every entry costs at least one byte on the wire, so a count exceeding the bytes
	// left is corrupt. Checked before the make() so a corrupt count cannot size an
	// allocation.
	if n < 0 || n > len(r.b)-r.pos {
		r.fail()
		return nil
	}

	out := make([]TopicPartitions, 0, n)
	for i := 0; i < n && r.err == nil; i++ {
		var tp TopicPartitions
		if flexible {
			tp.Topic = r.compactStr()
			c := r.uvarint()
			if c > 0 {
				tp.Partitions = r.int32Slice(int(c) - 1)
			}
			r.skipTaggedFields() // PartitionsSchema struct
		} else {
			tp.Topic = r.str()
			tp.Partitions = r.int32Slice(int(r.int32()))
		}
		out = append(out, tp)
	}
	return out
}

// skipTaggedFields consumes a flexible-version tagged-fields section: a uvarint count
// followed by that many (tag uvarint, size uvarint, size bytes) entries. We keep only
// the normal fields, so tagged fields are skipped.
func (r *reader) skipTaggedFields() {
	count := r.uvarint()
	for i := uint64(0); i < count && r.err == nil; i++ {
		_ = r.uvarint() // tag
		size := int(r.uvarint())
		if !r.need(size) {
			return
		}
		r.pos += size
	}
}
