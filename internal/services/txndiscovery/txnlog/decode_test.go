package txnlog

import (
	"encoding/binary"
	"math"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- wire-format fixture builders ---
//
// These hand-assemble the Kafka broker-internal record layout byte by byte. They
// are deliberately NOT built with a Kafka encoding library: a fixture produced by
// the same library the decoder is written against would only prove the decoder
// agrees with itself, and would keep agreeing after a shared misunderstanding of
// the format. Assembling the bytes independently is what makes these tests able to
// catch a decoder that reads the wrong field width or order.

func be16(v int16) []byte {
	b := make([]byte, 2)
	binary.BigEndian.PutUint16(b, uint16(v))
	return b
}

func be32(v int32) []byte {
	b := make([]byte, 4)
	binary.BigEndian.PutUint32(b, uint32(v))
	return b
}

func be64(v int64) []byte {
	b := make([]byte, 8)
	binary.BigEndian.PutUint64(b, uint64(v))
	return b
}

func uvar(v uint64) []byte {
	b := make([]byte, binary.MaxVarintLen64)
	return b[:binary.PutUvarint(b, v)]
}

// kstr encodes a classic (non-flexible) string: int16 length, then the bytes.
func kstr(s string) []byte { return append(be16(int16(len(s))), s...) }

// cstr encodes a flexible compact string: uvarint(len+1), then the bytes.
func cstr(s string) []byte { return append(uvar(uint64(len(s))+1), s...) }

func concat(parts ...[]byte) []byte {
	var out []byte
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

func TestDecodeKey_V0(t *testing.T) {
	// Key schema v0: int16 version, then a classic string transactional id.
	b := concat(
		be16(0),            // version = 0
		kstr("my-app-0_0"), // transactionalId
	)

	k, err := DecodeKey(b)

	require.NoError(t, err)
	assert.Equal(t, int16(0), k.Version)
	assert.Equal(t, "my-app-0_0", k.TransactionalID)
}

func TestDecodeValue_V0_ClassicEncodingWithMultipleTopics(t *testing.T) {
	// Value schema v0 is the classic (non-flexible) encoding: fixed-width integers,
	// int16-prefixed strings, int32-prefixed arrays, no tagged-field sections. One
	// of the topics is internal, which is what the footprint of a read-process-write
	// transaction looks like on the wire.
	b := concat(
		be16(0),     // version = 0
		be64(42),    // producerId
		be16(5),     // producerEpoch
		be32(60000), // timeoutMs
		[]byte{1},   // status = Ongoing
		be32(2),     // partitions array length = 2

		kstr("t1"),                 // topic
		be32(1),                    // partitionIds length = 1
		be32(0),                    // partition 0
		kstr("__consumer_offsets"), // topic
		be32(1),                    // partitionIds length = 1
		be32(12),                   // partition 12

		be64(1000), // lastUpdateMs
		be64(900),  // startMs
	)

	v, err := DecodeValue(b)

	require.NoError(t, err)
	assert.Equal(t, int16(0), v.Version)
	assert.Equal(t, int64(42), v.ProducerID)
	assert.Equal(t, int16(5), v.ProducerEpoch)
	assert.Equal(t, int32(60000), v.TimeoutMs)
	assert.Equal(t, StatusOngoing, v.Status)
	assert.Equal(t, []TopicPartitions{
		{Topic: "t1", Partitions: []int32{0}},
		{Topic: "__consumer_offsets", Partitions: []int32{12}},
	}, v.Partitions)
	assert.Equal(t, []string{"t1", "__consumer_offsets"}, v.Topics())
	assert.Equal(t, int64(1000), v.LastUpdateMs)
	assert.Equal(t, int64(900), v.StartMs)
}

func TestDecodeValue_V1_FlexibleEncoding(t *testing.T) {
	// Value schema v1 is the "flexible" encoding: strings and arrays carry a
	// uvarint(len+1) prefix instead of a fixed-width one, and every struct ends with
	// a tagged-fields section. Fixed-width integer fields are unaffected.
	b := concat(
		be16(1),     // version = 1 -> flexible
		be64(7),     // producerId
		be16(1),     // producerEpoch
		be32(30000), // timeoutMs
		[]byte{2},   // status = PrepareCommit
		uvar(1+1),   // compact partitions array length = 1

		cstr("a"), // topic
		uvar(1+1), // compact partitionIds length = 1
		be32(3),   // partition 3
		uvar(0),   // PartitionsSchema tagged fields = 0

		be64(2), // lastUpdateMs
		be64(1), // startMs
		uvar(0), // top-level tagged fields = 0
	)

	v, err := DecodeValue(b)

	require.NoError(t, err)
	assert.Equal(t, int16(1), v.Version)
	assert.Equal(t, int64(7), v.ProducerID)
	assert.Equal(t, StatusPrepareCommit, v.Status)
	assert.Equal(t, []TopicPartitions{{Topic: "a", Partitions: []int32{3}}}, v.Partitions)
	assert.Equal(t, int64(2), v.LastUpdateMs)
	assert.Equal(t, int64(1), v.StartMs)
}

func TestDecodeValue_V1_SkipsPopulatedTaggedFields(t *testing.T) {
	// Tagged fields are how Kafka adds optional fields to a flexible schema without
	// a version bump, so a broker upgrade can start populating them at any time. We
	// read none of them, but we must step over them exactly: the first topic below
	// carries a populated tagged-fields section, so the second topic only decodes if
	// the skip consumed the right number of bytes.
	b := concat(
		be16(1),    // version = 1 -> flexible
		be64(9),    // producerId
		be16(0),    // producerEpoch
		be32(1000), // timeoutMs
		[]byte{1},  // status = Ongoing
		uvar(2+1),  // compact partitions array length = 2

		cstr("x"),          // topic
		uvar(1+1),          // compact partitionIds length = 1
		be32(0),            // partition 0
		uvar(1),            // PartitionsSchema tagged fields: ONE entry
		uvar(99),           // tag = 99
		uvar(2),            // size = 2
		[]byte{0xAB, 0xCD}, // opaque tagged payload

		cstr("y"), // topic
		uvar(1+1), // compact partitionIds length = 1
		be32(7),   // partition 7
		uvar(0),   // PartitionsSchema tagged fields = 0

		be64(0), // lastUpdateMs
		be64(0), // startMs

		uvar(1),                  // top-level tagged fields: ONE entry
		uvar(5),                  // tag = 5
		uvar(3),                  // size = 3
		[]byte{0x01, 0x02, 0x03}, // opaque tagged payload
	)

	v, err := DecodeValue(b)

	require.NoError(t, err)
	assert.Equal(t, []string{"x", "y"}, v.Topics())
	assert.Equal(t, []TopicPartitions{
		{Topic: "x", Partitions: []int32{0}},
		{Topic: "y", Partitions: []int32{7}},
	}, v.Partitions)
}

func TestTxnStatus_HasFootprint(t *testing.T) {
	// Only the in-flight states carry a topic-partition set. Once a transaction
	// completes — or is empty, dead, or fenced — the coordinator clears the set, so
	// treating those records as footprints would silently shrink a transaction's
	// topics back to nothing.
	cases := map[TxnStatus]bool{
		StatusEmpty:             false,
		StatusOngoing:           true,
		StatusPrepareCommit:     true,
		StatusPrepareAbort:      true,
		StatusCompleteCommit:    false,
		StatusCompleteAbort:     false,
		StatusDead:              false,
		StatusPrepareEpochFence: false,
	}

	for status, want := range cases {
		assert.Equal(t, want, status.HasFootprint(), "HasFootprint for status %d", int8(status))
	}
}

func TestTxnStatus_String(t *testing.T) {
	// Callers bucket counters by status name, so an unrecognised code has to render
	// as something distinguishable rather than collapsing into a known bucket.
	cases := map[TxnStatus]string{
		StatusEmpty:             "Empty",
		StatusOngoing:           "Ongoing",
		StatusPrepareCommit:     "PrepareCommit",
		StatusPrepareAbort:      "PrepareAbort",
		StatusCompleteCommit:    "CompleteCommit",
		StatusCompleteAbort:     "CompleteAbort",
		StatusDead:              "Dead",
		StatusPrepareEpochFence: "PrepareEpochFence",
		TxnStatus(42):           "Unknown(42)",
	}

	for status, want := range cases {
		assert.Equal(t, want, status.String())
	}
}

// --- abuse cases: the broker is untrusted input ---

func TestDecodeKey_UnsupportedVersionIsRejected(t *testing.T) {
	// Only key version 0 exists today. A future version could reorder or retype
	// fields, so decoding one on the assumption it still looks like v0 would hand
	// back a plausible-looking transactional id that is actually garbage. Fail loud.
	b := concat(
		be16(1),   // version = 1 -> does not exist
		kstr("x"), // a byte sequence that IS a valid v0 body
	)

	_, err := DecodeKey(b)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported key version")
}

func TestDecodeValue_UnsupportedVersionIsRejected(t *testing.T) {
	// Both of these bodies are structurally valid for one of the two encodings, so a
	// decoder that guessed at the encoding from the version would return clean,
	// wrong data. Rejecting the version is what makes format drift loud.
	tests := map[string][]byte{
		"above the known range": concat(
			be16(2), // version = 2 -> unknown
			be64(1), be16(0), be32(0),
			[]byte{1}, // status
			uvar(0),   // compact partitions array = null
			be64(0), be64(0),
			uvar(0), // tagged fields = 0
		),
		"negative": concat(
			be16(-1), // version = -1 -> nonsense
			be64(1), be16(0), be32(0),
			[]byte{1}, // status
			be32(0),   // classic partitions array length = 0
			be64(0), be64(0),
		),
	}

	for name, b := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := DecodeValue(b)

			require.Error(t, err)
			assert.Contains(t, err.Error(), "unsupported value version")
		})
	}
}

func TestDecodeValue_EveryTruncatedPrefixErrorsWithoutPanicking(t *testing.T) {
	// A fetch can be cut at any byte boundary, and a compacted or drifted record can
	// end anywhere. Walking every prefix of a known-good record is the cheapest way
	// to prove no read path indexes past the end of the buffer.
	valid := map[string][]byte{
		"v0 classic": concat(
			be16(0), be64(42), be16(5), be32(60000),
			[]byte{1}, // Ongoing
			be32(1),   // one topic
			kstr("t1"), be32(1), be32(0),
			be64(1), be64(2),
		),
		"v1 flexible with tagged fields": concat(
			be16(1), be64(7), be16(1), be32(30000),
			[]byte{2}, // PrepareCommit
			uvar(1+1), // one topic
			cstr("a"), uvar(1+1), be32(3),
			uvar(1), uvar(99), uvar(2), []byte{0xAB, 0xCD}, // populated tagged fields
			be64(2), be64(1),
			uvar(0),
		),
	}

	for name, full := range valid {
		t.Run(name, func(t *testing.T) {
			// Sanity: the un-truncated record must decode, or the prefixes prove nothing.
			_, err := DecodeValue(full)
			require.NoError(t, err, "fixture itself must be valid")

			for i := 0; i < len(full); i++ {
				var decodeErr error
				require.NotPanics(t, func() { _, decodeErr = DecodeValue(full[:i]) },
					"prefix length %d panicked", i)
				require.Error(t, decodeErr, "prefix length %d decoded without error", i)
			}
		})
	}
}

func TestDecodeKey_EveryTruncatedPrefixErrorsWithoutPanicking(t *testing.T) {
	full := concat(be16(0), kstr("my-app-0_0"))

	_, err := DecodeKey(full)
	require.NoError(t, err, "fixture itself must be valid")

	for i := 0; i < len(full); i++ {
		var decodeErr error
		require.NotPanics(t, func() { _, decodeErr = DecodeKey(full[:i]) },
			"prefix length %d panicked", i)
		require.Error(t, decodeErr, "prefix length %d decoded without error", i)
	}
}

func TestDecodeValue_ArrayCountLargerThanBufferIsRejectedBeforeAllocating(t *testing.T) {
	// An array's element count is broker-supplied, and every element needs at least
	// one byte on the wire, so a count exceeding the bytes left is impossible. Sizing
	// an allocation from it first and discovering the truncation second turns a
	// corrupt 40-byte record into a multi-gigabyte allocation. Erroring is not enough
	// on its own — the rejection has to happen before the make().
	const (
		hugeTopicCount     = 2_000_000  // x40 bytes/element if allocated
		hugePartitionCount = 20_000_000 // x4 bytes/element if allocated
		allocBudget        = 1 << 20    // 1 MiB
	)

	tests := map[string][]byte{
		"classic topic array count exceeds buffer": concat(
			be16(0), be64(1), be16(0), be32(0),
			[]byte{1},            // status
			be32(hugeTopicCount), // array length, with zero bytes following
		),
		"flexible topic array count exceeds buffer": concat(
			be16(1), be64(1), be16(0), be32(0),
			[]byte{1},              // status
			uvar(hugeTopicCount+1), // compact array length, with zero bytes following
		),
		"partition id count exceeds buffer": concat(
			be16(0), be64(1), be16(0), be32(0),
			[]byte{1}, // status
			be32(1),   // one topic
			kstr("t"),
			be32(hugePartitionCount), // partitionIds length, with zero bytes following
		),
		"negative partition id count": concat(
			be16(0), be64(1), be16(0), be32(0),
			[]byte{1}, // status
			be32(1),   // one topic
			kstr("t"),
			be32(-1), // a negative length is not a null marker here
		),
	}

	for name, b := range tests {
		t.Run(name, func(t *testing.T) {
			var before, after runtime.MemStats
			var decodeErr error

			runtime.ReadMemStats(&before)
			require.NotPanics(t, func() { _, decodeErr = DecodeValue(b) })
			runtime.ReadMemStats(&after)

			require.Error(t, decodeErr)
			assert.Less(t, after.TotalAlloc-before.TotalAlloc, uint64(allocBudget),
				"decoding a %d-byte record allocated more than %d bytes, so the count was trusted before it was checked",
				len(b), allocBudget)
		})
	}
}

func TestDecodeValue_UvarintLengthNearMaxInt64DoesNotOverflowTheBoundsGuard(t *testing.T) {
	// The bounds guard used to test `pos + n > len(b)`. In the classic encoding n comes
	// from an int16 and cannot exceed 32767, so the sum never overflows — which is why
	// every existing truncation test passes. In the FLEXIBLE encoding n comes from a
	// uvarint and can be up to 2^63-1, so `pos + n` wraps to a negative number, the
	// guard reads it as "in bounds", and the slice that follows panics:
	//
	//	panic: runtime error: slice bounds out of range [:-9223372036854775783]
	//
	// The package's stated invariant is that malformed broker input yields a counted
	// error, never a panic. Nothing in kcp recovers, and this decodes on a reader
	// goroutine, so a ~30-byte malformed record kills a multi-hour observation window
	// with no YAML, no stats and an unclosed audit log.
	tests := map[string][]byte{
		// compactStr: n := int(uvarint) - 1, straight into need(n) and then a slice.
		"compact string length": concat(
			be16(1),   // version 1 = flexible
			be64(1),   // producerId
			be16(0),   // producerEpoch
			be32(0),   // timeoutMs
			[]byte{1}, // status = Ongoing
			uvar(2),   // compact array: one topic entry
			uvar(math.MaxInt64),
		),
		// skipTaggedFields: need(size) passes, `pos += size` drives pos negative, and
		// the NEXT read panics rather than this one — the corruption outlives the field.
		"tagged field size": concat(
			be16(1),   // version 1 = flexible
			be64(1),   // producerId
			be16(0),   // producerEpoch
			be32(0),   // timeoutMs
			[]byte{1}, // status = Ongoing
			uvar(2),   // compact array: one topic entry
			cstr("t"), // topic
			uvar(0),   // partition ids: null compact array
			uvar(1),   // tagged fields: one entry
			uvar(0),   // tag number
			uvar(math.MaxInt64),
		),
	}

	for name, b := range tests {
		t.Run(name, func(t *testing.T) {
			var decodeErr error
			require.NotPanics(t, func() { _, decodeErr = DecodeValue(b) },
				"a %d-byte malformed record panicked instead of erroring", len(b))
			require.Error(t, decodeErr)
		})
	}
}
