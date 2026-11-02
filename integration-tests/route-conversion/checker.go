package routeconversion

// The continuous-client checker: a Go port of
// d2s-client-rejoin-evidence/scripts/analyze.py. It reads the JSON lines
// kafka-verifiable-producer and kafka-verifiable-consumer print and matches
// records by value, never by offset: after promotion the source and the
// destination can give one offset to different records.
//
// The guarantee it enforces is at-least-once: every acknowledged record is read.
// Re-reads are classified and reported but never fail a check; how many a
// consumer group makes depends on where its commit cycle falls against the fence.

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
)

// DupClass is why a record was read more than once.
type DupClass string

const (
	// DupFenceOnset is a record first read just before the fence landed: the
	// fence blocked its commit, so the group re-reads it after the switch.
	DupFenceOnset DupClass = "fence-onset"
	// DupSwitch is a record a member fetched from the destination after the
	// switch, before its first commit failed and it rejoined.
	DupSwitch DupClass = "switch"
	// DupUnexplained is any other re-read.
	DupUnexplained DupClass = "unexplained"
)

// Bounds are the windows re-reads are classified by, and the minimum evidence
// a group must show.
type Bounds struct {
	// FenceBefore and FenceAfter bound a fence-onset re-read's first read
	// around the watcher's fence onset.
	FenceBefore, FenceAfter time.Duration
	// SwitchBefore and SwitchAfter bound a switch re-read: its first read is no
	// earlier than SwitchBefore before the watcher's switch time, and its
	// second read no later than SwitchAfter after it.
	SwitchBefore, SwitchAfter time.Duration
	// MinAckedAfterSwitch is how many records a group's producer must have
	// acknowledged after the switch: without them, producing through the
	// converted route was not shown to work.
	MinAckedAfterSwitch int
}

func DefaultBounds() Bounds {
	return Bounds{
		FenceBefore:  10 * time.Second,
		FenceAfter:   15 * time.Second, // the CR shows the fence before the pods apply it
		SwitchBefore: 5 * time.Second,
		SwitchAfter:  60 * time.Second,

		MinAckedAfterSwitch: 1,
	}
}

// Windows are the transition watcher's times, in Unix milliseconds (the clock
// the client tools log in). Zero means never seen.
type Windows struct {
	FenceOnsetMs int64
	SwitchDoneMs int64
}

// ProducedRecord is one acknowledged send.
type ProducedRecord struct {
	Value       string
	Partition   int32
	Offset      int64
	TimestampMs int64
}

// SendError is one producer_send_error event.
type SendError struct {
	Value              string
	TimestampMs        int64
	Exception, Message string
}

// ProducerLog is one kafka-verifiable-producer's output.
type ProducerLog struct {
	Acked      map[string]ProducedRecord
	SendErrors map[string]SendError
	// Skipped counts non-empty lines that were not JSON events (log4j noise,
	// or a line cut short when the tool was stopped).
	Skipped int
}

// Read is one record_data event.
type Read struct {
	TimestampMs int64
	Member      string
	Partition   int32
	Offset      int64
}

// ConsumedRecord is one record_data event with its topic and value.
type ConsumedRecord struct {
	Topic string
	Value string
	Read
}

// ConsumerLog is one kafka-verifiable-consumer member's output.
type ConsumerLog struct {
	Member        string
	Records       []ConsumedRecord
	Assignments   int
	Revocations   int
	FailedCommits int
	Skipped       int
}

// event is the subset of the tools' JSON lines the checker reads.
type event struct {
	Name      string  `json:"name"`
	Timestamp int64   `json:"timestamp"`
	Value     *string `json:"value"`
	Topic     string  `json:"topic"`
	Partition int32   `json:"partition"`
	Offset    int64   `json:"offset"`
	Success   *bool   `json:"success"`
	Exception string  `json:"exception"`
	Message   string  `json:"message"`
}

// scanEvents calls fn for every JSON event line in r. Lines that are not JSON
// objects, or do not parse, are counted and skipped, as analyze.py does.
func scanEvents(r io.Reader, fn func(event)) (int, error) {
	skipped := 0
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var e event
		if !strings.HasPrefix(line, "{") || json.Unmarshal([]byte(line), &e) != nil {
			skipped++
			continue
		}
		fn(e)
	}
	return skipped, sc.Err()
}

// ParseProducerLog reads one kafka-verifiable-producer's JSON lines.
func ParseProducerLog(r io.Reader) (ProducerLog, error) {
	pl := ProducerLog{Acked: map[string]ProducedRecord{}, SendErrors: map[string]SendError{}}
	skipped, err := scanEvents(r, func(e event) {
		if e.Value == nil {
			return
		}
		switch e.Name {
		case "producer_send_success":
			pl.Acked[*e.Value] = ProducedRecord{Value: *e.Value, Partition: e.Partition, Offset: e.Offset, TimestampMs: e.Timestamp}
		case "producer_send_error":
			pl.SendErrors[*e.Value] = SendError{Value: *e.Value, TimestampMs: e.Timestamp, Exception: e.Exception, Message: e.Message}
		}
	})
	pl.Skipped = skipped
	return pl, err
}

// ParseConsumerLog reads one kafka-verifiable-consumer member's JSON lines
// (--verbose, so every record is logged).
func ParseConsumerLog(member string, r io.Reader) (ConsumerLog, error) {
	cl := ConsumerLog{Member: member}
	skipped, err := scanEvents(r, func(e event) {
		switch e.Name {
		case "record_data":
			if e.Value != nil {
				cl.Records = append(cl.Records, ConsumedRecord{Topic: e.Topic, Value: *e.Value,
					Read: Read{TimestampMs: e.Timestamp, Member: member, Partition: e.Partition, Offset: e.Offset}})
			}
		case "partitions_assigned":
			cl.Assignments++
		case "partitions_revoked":
			cl.Revocations++
		case "offsets_committed":
			if e.Success != nil && !*e.Success {
				cl.FailedCommits++
			}
		}
	})
	cl.Skipped = skipped
	return cl, err
}

// CheckInput is one consumer group on one topic, with the producer that wrote
// to it.
type CheckInput struct {
	Group, Topic string
	// ValuePrefix is the producer's --value-prefix: only values "<prefix>.<n>"
	// are this run's, so records left on the topic by earlier tests are ignored.
	ValuePrefix string
	Producers   []ProducerLog
	Consumers   []ConsumerLog
	Windows     Windows
	Bounds      Bounds
}

// Duplicate is one record read more than once, with every read in time order.
type Duplicate struct {
	Value string
	Reads []Read
	Class DupClass
}

// GroupResult is the checker's verdict for one group.
type GroupResult struct {
	Group, Topic string
	Acked        int
	// AckedAfterSwitch counts acked records with an ack after the switch.
	AckedAfterSwitch int
	SendErrors       int
	// FirstSendError is the earliest send error, when there is one.
	FirstSendError *SendError
	Reads          int
	Distinct       int
	NotAcked       int
	Missed         []ProducedRecord
	Duplicates     []Duplicate
	ByClass        map[DupClass]int
	Assignments    map[string]int
	Revocations    map[string]int
	FailedCommits  int
	Skipped        int
	Windows        Windows
	Failures       []string
}

// OK reports whether the group passed: records acked after the switch, no send
// error, and every acknowledged record read at least once.
func (r GroupResult) OK() bool { return len(r.Failures) == 0 }

// Check matches the producers' acknowledged records against the group's reads.
func Check(in CheckInput) GroupResult {
	prefix := in.ValuePrefix + "."
	res := GroupResult{Group: in.Group, Topic: in.Topic, ByClass: map[DupClass]int{},
		Assignments: map[string]int{}, Revocations: map[string]int{}, Windows: in.Windows}

	acked := map[string]ProducedRecord{}
	for _, p := range in.Producers {
		for v, rec := range p.Acked {
			if strings.HasPrefix(v, prefix) {
				acked[v] = rec
			}
		}
		for v, se := range p.SendErrors {
			if !strings.HasPrefix(v, prefix) {
				continue
			}
			res.SendErrors++
			if f := res.FirstSendError; f == nil || se.TimestampMs < f.TimestampMs ||
				(se.TimestampMs == f.TimestampMs && se.Value < f.Value) {
				se := se
				res.FirstSendError = &se
			}
		}
		res.Skipped += p.Skipped
	}
	res.Acked = len(acked)
	if in.Windows.SwitchDoneMs > 0 {
		for _, rec := range acked {
			if rec.TimestampMs > in.Windows.SwitchDoneMs {
				res.AckedAfterSwitch++
			}
		}
	}

	reads := map[string][]Read{}
	for _, c := range in.Consumers {
		res.Assignments[c.Member] += c.Assignments
		res.Revocations[c.Member] += c.Revocations
		res.FailedCommits += c.FailedCommits
		res.Skipped += c.Skipped
		for _, rec := range c.Records {
			if rec.Topic != in.Topic || !strings.HasPrefix(rec.Value, prefix) {
				continue
			}
			reads[rec.Value] = append(reads[rec.Value], rec.Read)
			res.Reads++
		}
	}
	res.Distinct = len(reads)
	for v := range reads {
		if _, ok := acked[v]; !ok {
			res.NotAcked++
		}
	}

	for v, rec := range acked {
		if _, ok := reads[v]; !ok {
			res.Missed = append(res.Missed, rec)
		}
	}
	sort.Slice(res.Missed, func(i, j int) bool {
		if res.Missed[i].TimestampMs != res.Missed[j].TimestampMs {
			return res.Missed[i].TimestampMs < res.Missed[j].TimestampMs
		}
		return res.Missed[i].Value < res.Missed[j].Value
	})

	b := in.Bounds
	fenceLo := in.Windows.FenceOnsetMs - b.FenceBefore.Milliseconds()
	fenceHi := in.Windows.FenceOnsetMs + b.FenceAfter.Milliseconds()
	switchLo := in.Windows.SwitchDoneMs - b.SwitchBefore.Milliseconds()
	switchHi := in.Windows.SwitchDoneMs + b.SwitchAfter.Milliseconds()
	for v, rs := range reads {
		if len(rs) < 2 {
			continue
		}
		sort.SliceStable(rs, func(i, j int) bool { return rs[i].TimestampMs < rs[j].TimestampMs })
		d := Duplicate{Value: v, Reads: rs, Class: DupUnexplained}
		switch {
		case len(rs) > 2:
			// A third read is neither the fence's nor the switch's re-read.
		case in.Windows.FenceOnsetMs > 0 && rs[0].TimestampMs >= fenceLo && rs[0].TimestampMs <= fenceHi:
			d.Class = DupFenceOnset
		case in.Windows.SwitchDoneMs > 0 && rs[0].TimestampMs >= switchLo && rs[1].TimestampMs <= switchHi:
			d.Class = DupSwitch
		}
		res.ByClass[d.Class]++
		res.Duplicates = append(res.Duplicates, d)
	}
	sort.Slice(res.Duplicates, func(i, j int) bool {
		if res.Duplicates[i].Reads[0].TimestampMs != res.Duplicates[j].Reads[0].TimestampMs {
			return res.Duplicates[i].Reads[0].TimestampMs < res.Duplicates[j].Reads[0].TimestampMs
		}
		return res.Duplicates[i].Value < res.Duplicates[j].Value
	})

	fail := func(format string, a ...any) { res.Failures = append(res.Failures, fmt.Sprintf(format, a...)) }
	if in.Windows.FenceOnsetMs == 0 {
		fail("the transition watcher never saw kcp's conversion fence, so no re-read can be classified")
	}
	if in.Windows.SwitchDoneMs == 0 {
		fail("the transition watcher never saw the route static, so no re-read can be classified")
	}
	if res.Acked == 0 {
		fail("no acknowledged record with prefix %q: the producer never ran, so the check would pass vacuously", prefix)
	}
	if in.Windows.SwitchDoneMs > 0 && res.AckedAfterSwitch < b.MinAckedAfterSwitch {
		if res.AckedAfterSwitch == 0 {
			fail("no record acknowledged after the switch: producing through the converted route was not shown to work")
		} else {
			fail("only %d record(s) acknowledged after the switch, fewer than %d: producing through the converted route was not shown to work",
				res.AckedAfterSwitch, b.MinAckedAfterSwitch)
		}
	}
	if f := res.FirstSendError; f != nil {
		fail("%d producer send error(s); with the producer's retries any send error is a real failure (first: %s at %d: %s: %s)",
			res.SendErrors, f.Value, f.TimestampMs, f.Exception, f.Message)
	}
	if n := len(res.Missed); n > 0 {
		m := res.Missed[0]
		fail("%d acknowledged record(s) never read by group %s (first: %s p%d@%d, acked at %d)", n, in.Group, m.Value, m.Partition, m.Offset, m.TimestampMs)
	}
	return res
}

func describeReads(d Duplicate) string {
	parts := make([]string, len(d.Reads))
	for i, r := range d.Reads {
		parts[i] = fmt.Sprintf("%s p%d@%d t=%d", r.Member, r.Partition, r.Offset, r.TimestampMs)
	}
	return d.Value + ": " + strings.Join(parts, "; ")
}

// String renders the verdict the way analyze.py prints it, for the test log
// and checker.txt.
func (r GroupResult) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "== group %s on %s\n", r.Group, r.Topic)
	fmt.Fprintf(&b, "   windows: fence onset %d, switch %d (Unix ms)\n", r.Windows.FenceOnsetMs, r.Windows.SwitchDoneMs)
	fmt.Fprintf(&b, "   produced: %d acked (%d after the switch), %d send errors (never acked)\n", r.Acked, r.AckedAfterSwitch, r.SendErrors)
	fmt.Fprintf(&b, "   consumed: %d reads of %d distinct records; assignments %v, revocations %v, failed commits %d\n",
		r.Reads, r.Distinct, r.Assignments, r.Revocations, r.FailedCommits)
	fmt.Fprintf(&b, "   MISSED (acked, never read): %d\n", len(r.Missed))
	for i, m := range r.Missed {
		if i == 10 {
			break
		}
		fmt.Fprintf(&b, "      %s p%d@%d acked at %d\n", m.Value, m.Partition, m.Offset, m.TimestampMs)
	}
	fmt.Fprintf(&b, "   DUPLICATED (read more than once): %d (fence-onset %d, switch %d, unexplained %d)\n",
		len(r.Duplicates), r.ByClass[DupFenceOnset], r.ByClass[DupSwitch], r.ByClass[DupUnexplained])
	for i, d := range r.Duplicates {
		if i == 5 {
			break
		}
		fmt.Fprintf(&b, "      e.g. [%s] %s\n", d.Class, describeReads(d))
	}
	fmt.Fprintf(&b, "   read but not acked (written despite a send error, or a retry duplicate): %d\n", r.NotAcked)
	fmt.Fprintf(&b, "   skipped non-JSON lines: %d\n", r.Skipped)
	if r.OK() {
		b.WriteString("   RESULT: PASS — records acked after the switch, no send error; every acked record read at least once\n")
	} else {
		b.WriteString("   RESULT: FAIL\n")
		for _, f := range r.Failures {
			fmt.Fprintf(&b, "      - %s\n", f)
		}
	}
	return b.String()
}
