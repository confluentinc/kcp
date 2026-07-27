package txnlog

import "fmt"

// OffsetKey is a decoded __consumer_offsets record key.
//
// Two different schemas share the __consumer_offsets topic, and Version says which
// one this record carried. Topic and Partition are only meaningful for an offset
// commit (versions 0 and 1); a group-metadata record (version 2) leaves Topic empty
// and Partition zero. Callers correlating consumed topics must therefore treat an
// empty Topic as "not an offset commit" rather than as partition 0 of an unnamed
// topic.
type OffsetKey struct {
	Version   int16
	Group     string
	Topic     string
	Partition int32
}

// DecodeOffsetKey decodes a __consumer_offsets record key.
//
// Like the __transaction_state schemas in this package, these are broker-internal
// record formats rather than part of the stable client wire protocol, so nothing in
// the Kafka client ecosystem for Go exposes them and they are hand-ported here from
// Kafka's OffsetCommitKey.json and GroupMetadataKey.json.
//
// A leading int16 version selects the schema. Versions 0 and 1 are OffsetCommitKey
// {group, topic, partition} — an actual committed offset, and the record this
// decoder exists to read. Version 2 is GroupMetadataKey {group}: consumer-group
// state, carrying no topic. Both encodings are classic (non-flexible), so strings
// are int16-length-prefixed and there are no tagged-field sections.
//
// Anything else is rejected. The two schemas are structurally similar enough that a
// decoder guessing at an unknown version would return a confident, wrong topic name
// rather than an obvious failure, and that name would be published as a real
// consumed topic. Failing loudly is what makes format drift visible.
func DecodeOffsetKey(b []byte) (OffsetKey, error) {
	r := newReader(b)
	var k OffsetKey
	k.Version = r.int16()
	switch k.Version {
	case 0, 1:
		k.Group = r.str()
		k.Topic = r.str()
		k.Partition = r.int32()
	case 2:
		k.Group = r.str()
	default:
		// A truncated key reads as version 0 with the error already latched, so it
		// falls to the case above and is reported as truncated by the check below,
		// never as an unsupported version.
		return k, fmt.Errorf("txnlog: unsupported offset key version %d", k.Version)
	}
	if r.err != nil {
		return k, r.err
	}
	return k, nil
}
