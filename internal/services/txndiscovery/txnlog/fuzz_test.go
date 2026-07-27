package txnlog

import (
	"bytes"
	"math"
	"runtime"
	"testing"
)

// Fuzz targets for the three broker-internal record decoders.
//
// These decoders are the package's whole untrusted-input surface: the records are not
// covered by the stable client protocol, they drift across broker versions, and whoever
// controls the broker's bytes controls every length prefix and array count in them.
//
// Each target asserts the two invariants the package documents, and NOT that decoding
// succeeds — a decode error is the correct outcome for almost all random input, so
// asserting success would only teach the fuzzer to stop exploring:
//
//  1. No panic. Nothing in kcp calls recover() and these decode on a reader goroutine,
//     so a panic is an unconditionally fatal end to a multi-hour observation window.
//  2. Allocation bounded by INPUT SIZE. This is the property an error-only assertion
//     silently passes: the pre-fix decoder returned a perfectly good "truncated record"
//     error while allocating 40 MiB from a 1 MiB input, because the guard bounded the
//     array COUNT and the count sized an allocation of 40 bytes an entry.

const (
	// fuzzAllocFloor is a fixed allowance added to every budget, covering two things.
	// First, the decoder's own size-independent ceiling: the topic-array prealloc is
	// capped at 1024 entries x 40 bytes ~= 40 KiB regardless of input. Second, noise —
	// runtime.MemStats.TotalAlloc is process-wide and cumulative, so under `go test
	// -fuzz` the fuzzing engine's own goroutines contribute to the delta. A floor this
	// far above the decoder's ceiling keeps the target from flaking on that noise while
	// still leaving the per-byte term to do the actual work on large inputs.
	fuzzAllocFloor = 1 << 20

	// valueAllocPerInputByte bounds DecodeValue's growth in input size.
	//
	// Chosen at 12 to sit between the two things that have to be separated. Below it:
	// the fixed decoder ceiling and every legitimate footprint, which allocate in the
	// tens of kilobytes. Above it: the finding-2 regression, which allocated 40.0x its
	// input (41943280 bytes from 1048597) — reverting that fix has to make this target
	// fail, and at 12 it does, with room to spare.
	//
	// It is deliberately NOT the decoder's structural worst case. A TopicPartitions
	// costs 40 bytes in memory against as few as 3 bytes on the wire, and append's
	// regrowth multiplies the total by a further ~5, so a large input consisting
	// ENTIRELY of minimal well-formed entries can still exceed this. That residual is
	// real and is called out in the fix for finding 2; this constant is set to catch the
	// count-driven amplification, not to certify the structural ratio.
	valueAllocPerInputByte = 12

	// keyAllocPerInputByte bounds the two key decoders, which allocate only the strings
	// they copy out — never more than the input itself. 4 leaves room for the string
	// headers and the size-class rounding on a short copy without leaving room for an
	// allocation driven by anything other than the bytes present.
	keyAllocPerInputByte = 4
)

// assertGracefulAndBounded runs decode on in and asserts the two invariants. Any panic
// propagates as a fuzz failure with the input recorded, which is the behaviour wanted:
// the panic itself is the defect, and re-raising it keeps the stack intact.
func assertGracefulAndBounded(t *testing.T, in []byte, perByte uint64, decode func([]byte)) {
	t.Helper()

	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	decode(in)
	runtime.ReadMemStats(&after)

	got := after.TotalAlloc - before.TotalAlloc
	budget := uint64(fuzzAllocFloor) + perByte*uint64(len(in))
	if got > budget {
		t.Fatalf("decoding %d bytes allocated %d, over the %d budget (%d + %d/byte): "+
			"allocation is being driven by a broker-supplied count rather than by the bytes present",
			len(in), got, budget, fuzzAllocFloor, perByte)
	}
}

// --- shared seeds ---

// pocUvarintOverflowCompactStr is the finding-1 reproduction for the compactStr path: a
// flexible v1 value whose topic length is a uvarint near MaxInt64, so `pos + n` overflows
// int64, the bounds guard reads the negative sum as in-bounds, and the slice panics.
func pocUvarintOverflowCompactStr() []byte {
	return concat(
		be16(1), be64(1), be16(0), be32(0), []byte{1},
		uvar(2), // compact array: one topic entry
		uvar(math.MaxInt64),
	)
}

// pocUvarintOverflowTaggedField is the finding-1 reproduction for the skipTaggedFields
// path, where the guard passes, `pos += size` drives pos negative, and the NEXT read is
// what panics.
func pocUvarintOverflowTaggedField() []byte {
	return concat(
		be16(1), be64(1), be16(0), be32(0), []byte{1},
		uvar(2), cstr("t"), uvar(0),
		uvar(1), uvar(0), uvar(math.MaxInt64),
	)
}

// pocCountAmplification is the finding-2 reproduction: a large record whose topic-array
// count is merely PROPORTIONAL to its own size, so it passes the "every entry costs a
// byte on the wire" guard while sizing a 40-bytes-an-entry prealloc.
func pocCountAmplification() []byte {
	filler := bytes.Repeat([]byte{0xFF}, 1<<20)
	return concat(be16(0), be64(1), be16(0), be32(0), []byte{1}, be32(int32(len(filler))), filler)
}

// validValueV0 is the classic-encoding fixture from decode_test.go, including the
// internal topic that makes a footprint read-process-write.
func validValueV0() []byte {
	return concat(
		be16(0), be64(42), be16(5), be32(60000), []byte{1},
		be32(2),
		kstr("t1"), be32(1), be32(0),
		kstr("__consumer_offsets"), be32(1), be32(12),
		be64(1000), be64(900),
	)
}

// validValueV1 is the flexible-encoding fixture: compact lengths plus a tagged-fields
// section on every struct.
func validValueV1() []byte {
	return concat(
		be16(1), be64(7), be16(1), be32(30000), []byte{2},
		uvar(1+1),
		cstr("a"), uvar(1+1), be32(3), uvar(0),
		be64(2000), be64(1900), uvar(0),
	)
}

// addPrefixes seeds truncated prefixes of b. A record cut short is the ordinary case
// this decoder meets on a real cluster — a fetch response ends at a partition boundary —
// so every prefix has to end in a counted error rather than a panic.
func addPrefixes(f *testing.F, b []byte) {
	for _, n := range []int{0, 1, 2, 3, 5, 8, 13, 17, 21, 30} {
		if n <= len(b) {
			f.Add(b[:n])
		}
	}
}

// --- targets ---

func FuzzDecodeValue(f *testing.F) {
	f.Add(pocUvarintOverflowCompactStr())
	f.Add(pocUvarintOverflowTaggedField())
	f.Add(pocCountAmplification())
	f.Add(validValueV0())
	f.Add(validValueV1())
	addPrefixes(f, validValueV0())
	addPrefixes(f, validValueV1())

	f.Fuzz(func(t *testing.T, b []byte) {
		assertGracefulAndBounded(t, b, valueAllocPerInputByte, func(in []byte) {
			v, err := DecodeValue(in)
			if err == nil {
				// Touch the decoded footprint so a decoder that returned success while
				// leaving a slice header pointing somewhere impossible is caught here
				// rather than in the caller that reads it.
				_ = v.Topics()
			}
		})
	})
}

func FuzzDecodeKey(f *testing.F) {
	valid := concat(be16(0), kstr("my-app-0_0"))
	f.Add(valid)
	f.Add(concat(be16(0), be16(math.MaxInt16)))       // length past the end
	f.Add(concat(be16(0), be16(-1)))                  // null string marker
	f.Add(concat(be16(1), kstr("x")))                 // unsupported version
	f.Add(concat(be16(0), be16(0), be16(0), be16(0))) // a control-marker-shaped key
	addPrefixes(f, valid)

	f.Fuzz(func(t *testing.T, b []byte) {
		assertGracefulAndBounded(t, b, keyAllocPerInputByte, func(in []byte) {
			_, _ = DecodeKey(in)
		})
	})
}

func FuzzDecodeOffsetKey(f *testing.F) {
	commitV0 := concat(be16(0), kstr("payments-eos"), kstr("orders.in"), be32(7))
	commitV1 := concat(be16(1), kstr("inventory-eos"), kstr("stock.updates"), be32(3))
	groupMetaV2 := concat(be16(2), kstr("payments-eos"))

	f.Add(commitV0)
	f.Add(commitV1)
	f.Add(groupMetaV2)
	f.Add(concat(be16(3), kstr("g")))                      // unsupported version
	f.Add(concat(be16(0), be16(math.MaxInt16), kstr("t"))) // group length past the end
	f.Add(concat(be16(0), kstr("g"), be16(math.MaxInt16))) // topic length past the end
	addPrefixes(f, commitV0)
	addPrefixes(f, groupMetaV2)

	f.Fuzz(func(t *testing.T, b []byte) {
		assertGracefulAndBounded(t, b, keyAllocPerInputByte, func(in []byte) {
			_, _ = DecodeOffsetKey(in)
		})
	})
}
