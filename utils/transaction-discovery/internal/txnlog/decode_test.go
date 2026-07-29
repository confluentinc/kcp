package txnlog

import (
	"encoding/binary"
	"reflect"
	"testing"
)

// --- format builders (an independent expression of the Kafka wire format, so the
// tests don't just mirror the decoder) ---

func be16(v int16) []byte { b := make([]byte, 2); binary.BigEndian.PutUint16(b, uint16(v)); return b }
func be32(v int32) []byte { b := make([]byte, 4); binary.BigEndian.PutUint32(b, uint32(v)); return b }
func be64(v int64) []byte { b := make([]byte, 8); binary.BigEndian.PutUint64(b, uint64(v)); return b }

func uvar(v uint64) []byte {
	b := make([]byte, binary.MaxVarintLen64)
	return b[:binary.PutUvarint(b, v)]
}

func kstr(s string) []byte { return append(be16(int16(len(s))), s...) }    // classic string
func cstr(s string) []byte { return append(uvar(uint64(len(s))+1), s...) } // compact string
func concat(parts ...[]byte) []byte {
	var out []byte
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

func TestDecodeValue_V0(t *testing.T) {
	// v0 (classic encoding), status Ongoing, two topics — exercises the array loop
	// and topic extraction, including the internal __consumer_offsets topic.
	b := concat(
		be16(0),                      // version
		be64(42),                     // producerId
		be16(5),                      // producerEpoch
		be32(60000),                  // timeoutMs
		[]byte{1},                    // status = Ongoing
		be32(2),                      // partitions array len = 2
		kstr("t1"), be32(1), be32(0), // {t1: [0]}
		kstr("__consumer_offsets"), be32(1), be32(12), // {__consumer_offsets: [12]}
		be64(1000), // lastUpdateMs
		be64(900),  // startMs
	)

	v, err := DecodeValue(b)
	if err != nil {
		t.Fatalf("DecodeValue: %v", err)
	}
	if v.Version != 0 || v.ProducerID != 42 || v.ProducerEpoch != 5 || v.TimeoutMs != 60000 {
		t.Errorf("header fields wrong: %+v", v)
	}
	if v.Status != StatusOngoing {
		t.Errorf("status = %v, want Ongoing", v.Status)
	}
	want := []TopicPartitions{
		{Topic: "t1", Partitions: []int32{0}},
		{Topic: "__consumer_offsets", Partitions: []int32{12}},
	}
	if !reflect.DeepEqual(v.Partitions, want) {
		t.Errorf("partitions = %+v, want %+v", v.Partitions, want)
	}
	if !reflect.DeepEqual(v.Topics(), []string{"t1", "__consumer_offsets"}) {
		t.Errorf("topics = %v", v.Topics())
	}
	if v.LastUpdateMs != 1000 || v.StartMs != 900 {
		t.Errorf("timestamps wrong: %+v", v)
	}
}

func TestDecodeValue_V1_Flexible(t *testing.T) {
	// v1 (flexible/compact encoding) with tagged-field sections (all empty here).
	b := concat(
		be16(1),     // version 1 -> flexible
		be64(7),     // producerId
		be16(1),     // producerEpoch
		be32(30000), // timeoutMs
		[]byte{2},   // status = PrepareCommit
		uvar(1+1),   // compact partitions array len = 1
		cstr("a"),   // topic
		uvar(1+1),   // compact partitionIds len = 1
		be32(3),     // partition 3
		uvar(0),     // PartitionsSchema tagged fields = 0
		be64(2),     // lastUpdateMs
		be64(1),     // startMs
		uvar(0),     // top-level tagged fields = 0
	)

	v, err := DecodeValue(b)
	if err != nil {
		t.Fatalf("DecodeValue: %v", err)
	}
	if v.Version != 1 || v.ProducerID != 7 || v.Status != StatusPrepareCommit {
		t.Errorf("fields wrong: %+v", v)
	}
	want := []TopicPartitions{{Topic: "a", Partitions: []int32{3}}}
	if !reflect.DeepEqual(v.Partitions, want) {
		t.Errorf("partitions = %+v, want %+v", v.Partitions, want)
	}
}

func TestDecodeValue_V1_SkipsNonEmptyTaggedFields(t *testing.T) {
	// A populated top-level tagged field must be skipped without corrupting decode.
	// Empty (present) partitions array plus one opaque tagged field.
	b := concat(
		be16(1),
		be64(9), be16(0), be32(1000),
		[]byte{1}, // Ongoing
		uvar(1+1), // 1 partition
		cstr("x"), uvar(1+1), be32(0), uvar(0),
		be64(0), be64(0),
		uvar(1),            // ONE tagged field at top level
		uvar(99),           // tag = 99
		uvar(2),            // size = 2
		[]byte{0xAB, 0xCD}, // opaque tagged data
	)
	v, err := DecodeValue(b)
	if err != nil {
		t.Fatalf("DecodeValue: %v", err)
	}
	if !reflect.DeepEqual(v.Topics(), []string{"x"}) {
		t.Errorf("topics = %v, want [x]", v.Topics())
	}
}

func TestDecodeKey_V0(t *testing.T) {
	b := concat(be16(0), kstr("my-app-0_0"))
	k, err := DecodeKey(b)
	if err != nil {
		t.Fatalf("DecodeKey: %v", err)
	}
	if k.TransactionalID != "my-app-0_0" {
		t.Errorf("transactionalID = %q", k.TransactionalID)
	}
}

func TestDecodeValue_UnsupportedVersion(t *testing.T) {
	if _, err := DecodeValue(be16(2)); err == nil {
		t.Error("expected error for unsupported version 2")
	}
}

func TestDecodeValue_TruncatedDoesNotPanic(t *testing.T) {
	full := concat(be16(0), be64(42), be16(5), be32(60000), []byte{1}, be32(1), kstr("t1"), be32(1), be32(0), be64(1), be64(2))
	for i := 0; i < len(full); i++ {
		if _, err := DecodeValue(full[:i]); err == nil && i < len(full) {
			// Not every prefix must error, but it must never panic; a nil error on a
			// short buffer only acceptable if it happens to be a valid shorter record,
			// which these prefixes are not.
			t.Errorf("prefix len %d: expected truncation error", i)
		}
	}
}

func TestStatusHasFootprint(t *testing.T) {
	with := []TxnStatus{StatusOngoing, StatusPrepareCommit, StatusPrepareAbort}
	without := []TxnStatus{StatusEmpty, StatusCompleteCommit, StatusCompleteAbort, StatusDead, StatusPrepareEpochFence}
	for _, s := range with {
		if !s.HasFootprint() {
			t.Errorf("%v should have a footprint", s)
		}
	}
	for _, s := range without {
		if s.HasFootprint() {
			t.Errorf("%v should not have a footprint", s)
		}
	}
}
