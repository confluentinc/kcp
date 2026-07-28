package txnlog

import (
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The fixtures below are hand-assembled bytes, using the same builders as
// decode_test.go (be16/be32/kstr/concat). They are deliberately NOT produced by a
// Kafka encoding library. The POC this decoder replaces built its fixtures with
// franz-go's kmsg — the very package that also did the decoding — so its tests could
// only ever prove that kmsg agrees with kmsg. Laying the bytes out by hand, against
// Kafka's OffsetCommitKey.json / GroupMetadataKey.json schemas, is what makes these
// tests capable of catching a decoder that reads the wrong field width or order.

func TestDecodeOffsetKey_V0(t *testing.T) {
	// OffsetCommitKey v0: int16 version, then classic (int16-length-prefixed) strings
	// for group and topic, then an int32 partition. Non-flexible: no compact lengths
	// and no tagged-field sections anywhere.
	b := concat(
		be16(0),              // version = 0
		kstr("payments-eos"), // group
		kstr("orders.in"),    // topic
		be32(7),              // partition
	)

	k, err := DecodeOffsetKey(b)

	require.NoError(t, err)
	assert.Equal(t, int16(0), k.Version)
	assert.Equal(t, "payments-eos", k.Group)
	assert.Equal(t, "orders.in", k.Topic)
	assert.Equal(t, int32(7), k.Partition)
}

func TestDecodeOffsetKey_V1(t *testing.T) {
	// OffsetCommitKey v1 is byte-identical in layout to v0 — Kafka bumped the version
	// for the record VALUE (v1 added commit_timestamp / expire_timestamp), and the key
	// schema came along for the ride. The decoder must accept it and read the same
	// three fields; rejecting it would silently drop every offset commit written by a
	// broker using the v1 key, which is most of them.
	b := concat(
		be16(1),               // version = 1
		kstr("inventory-eos"), // group
		kstr("stock.updates"), // topic
		be32(3),               // partition
	)

	k, err := DecodeOffsetKey(b)

	require.NoError(t, err)
	assert.Equal(t, int16(1), k.Version)
	assert.Equal(t, "inventory-eos", k.Group)
	assert.Equal(t, "stock.updates", k.Topic)
	assert.Equal(t, int32(3), k.Partition)
}

func TestDecodeOffsetKey_V2_GroupMetadataCarriesGroupOnly(t *testing.T) {
	// Version 2 is a different schema sharing the same topic: GroupMetadataKey, which
	// records consumer-group state rather than a committed offset. It carries the group
	// and nothing else — no topic, no partition. __consumer_offsets interleaves both
	// schemas, so a decoder that assumed every key was an OffsetCommitKey would read the
	// group-metadata body's trailing bytes as a topic name and invent a consumed topic
	// out of group state. Topic must come back empty so the caller can skip the record.
	b := concat(
		be16(2),              // version = 2 -> GroupMetadataKey
		kstr("payments-eos"), // group, and the whole body
	)

	k, err := DecodeOffsetKey(b)

	require.NoError(t, err)
	assert.Equal(t, int16(2), k.Version)
	assert.Equal(t, "payments-eos", k.Group)
	assert.Empty(t, k.Topic, "group metadata carries no consumed topic")
}

// --- abuse cases: the broker is untrusted input ---

func TestDecodeOffsetKey_UnsupportedVersionIsRejected(t *testing.T) {
	// Every body below is a structurally valid v0 OffsetCommitKey, so a decoder that
	// ignored the version — or that treated anything unrecognised as v0 — would return
	// clean, confident, wrong data. __consumer_offsets key schemas have been extended
	// before (v2 added group metadata), so a v3 is a question of when, not if, and the
	// correlation in U8 keys on the topic name this returns. Rejecting the version is
	// what turns that future format drift into a counted decode error instead of a
	// phantom topic silently folded into a migration group.
	tests := map[string]int16{
		"above the known range": 3,
		"far above":             999,
		"negative":              -1,
	}

	for name, version := range tests {
		t.Run(name, func(t *testing.T) {
			b := concat(
				be16(version),
				kstr("g"), // a byte sequence that IS a valid v0 body
				kstr("t"), //
				be32(0),   //
			)

			_, err := DecodeOffsetKey(b)

			require.Error(t, err)
			assert.Contains(t, err.Error(), "unsupported offset key version")
		})
	}
}

func TestDecodeOffsetKey_ShorterThanVersionPrefixIsRejected(t *testing.T) {
	// The version prefix is the one field read before any routing decision, so a key
	// too short to hold it is the input most likely to reach an unguarded index. An
	// empty key is not hypothetical either: __consumer_offsets carries tombstones, and
	// a fetch can be cut at any byte boundary.
	tests := map[string][]byte{
		"nil":       nil,
		"empty":     {},
		"one byte":  {0x00},
		"half of a": {0xFF},
	}

	for name, b := range tests {
		t.Run(name, func(t *testing.T) {
			var err error
			require.NotPanics(t, func() { _, err = DecodeOffsetKey(b) })
			require.Error(t, err)
		})
	}
}

func TestDecodeOffsetKey_EveryTruncatedPrefixErrorsWithoutPanicking(t *testing.T) {
	// Walking every prefix of a known-good key is the cheapest proof that no read path
	// indexes past the end of the buffer. It covers the cut landing mid-length-prefix,
	// mid-string, and mid-partition — the boundaries a single hand-picked fixture
	// would miss.
	valid := map[string][]byte{
		"v0 offset commit":  concat(be16(0), kstr("payments-eos"), kstr("orders.in"), be32(7)),
		"v1 offset commit":  concat(be16(1), kstr("inventory-eos"), kstr("stock.updates"), be32(3)),
		"v2 group metadata": concat(be16(2), kstr("payments-eos")),
	}

	for name, full := range valid {
		t.Run(name, func(t *testing.T) {
			// Sanity: the un-truncated key must decode, or the prefixes prove nothing.
			_, err := DecodeOffsetKey(full)
			require.NoError(t, err, "fixture itself must be valid")

			for i := 0; i < len(full); i++ {
				var decodeErr error
				require.NotPanics(t, func() { _, decodeErr = DecodeOffsetKey(full[:i]) },
					"prefix length %d panicked", i)
				require.Error(t, decodeErr, "prefix length %d decoded without error", i)
			}
		})
	}
}

func TestDecodeOffsetKey_LengthPrefixBeyondBufferDoesNotOverread(t *testing.T) {
	// A string's length prefix is broker-supplied, so a drifted or truncated key can
	// declare more bytes than it carries. Returning an error is NOT sufficient evidence
	// that this is handled: the record key handed to the decoder is a sub-slice of a
	// much larger shared fetch buffer, so it has capacity well past its length. An
	// unguarded `b[pos : pos+n]` stays inside that capacity and is therefore perfectly
	// legal Go — it silently splices whatever the adjacent record contains into the
	// group or topic name, with no panic and no error to count.
	//
	// That is the failure this test exists to catch, and it is not theoretical: with the
	// reader's bounds check removed, the v2 case below returns the sentinel verbatim.
	// The recovered name would then flow into txn-discovery.yaml and the audit log as a
	// real consumed topic.
	secret := []byte("ADJACENT-RECORD-BYTES-NOT-OURS")

	// The backing array holds the sentinel; the decoder is only handed the prefix up to
	// and including the length field, which declares the sentinel's length.
	backing := concat(be16(2), be16(int16(len(secret))), secret)
	b := backing[:4]
	require.Greater(t, cap(b), len(b), "fixture must have spare capacity or it proves nothing")

	var k OffsetKey
	var err error
	require.NotPanics(t, func() { k, err = DecodeOffsetKey(b) })

	require.Error(t, err, "a group length longer than the key must be rejected")
	assert.NotContains(t, k.Group, string(secret),
		"decoder read past the end of the supplied key into adjacent buffer memory")
}

func TestDecodeOffsetKey_OversizedLengthPrefixIsRejectedBeforeAllocating(t *testing.T) {
	// The companion bound to the over-read test above: a declared length must be
	// checked against the bytes actually present before it is allowed to size an
	// allocation. A classic string length is an int16, so the ceiling here is 32 KiB
	// rather than the gigabytes an int32 array count could ask for — the budget is a
	// backstop that pins the bound if a future field ever carries a wider length, not
	// the primary teeth. The over-read assertion above is what has teeth for this key.
	const allocBudget = 1 << 20 // 1 MiB

	tests := map[string][]byte{
		"group length exceeds buffer": concat(
			be16(0),     // version = 0
			be16(32767), // group length: the int16 maximum, with zero bytes following
		),
		"topic length exceeds buffer": concat(
			be16(0),     // version = 0
			kstr("g"),   // a well-formed group
			be16(32767), // topic length, with zero bytes following
		),
		"group metadata length exceeds buffer": concat(
			be16(2),     // version = 2
			be16(32767), // group length, with zero bytes following
		),
	}

	for name, b := range tests {
		t.Run(name, func(t *testing.T) {
			var before, after runtime.MemStats
			var decodeErr error

			runtime.ReadMemStats(&before)
			require.NotPanics(t, func() { _, decodeErr = DecodeOffsetKey(b) })
			runtime.ReadMemStats(&after)

			require.Error(t, decodeErr)
			assert.Less(t, after.TotalAlloc-before.TotalAlloc, uint64(allocBudget),
				"decoding a %d-byte key allocated more than %d bytes, so the declared length was trusted before it was checked",
				len(b), allocBudget)
		})
	}
}
