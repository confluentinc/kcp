package routeconversion

// The continuous-client checker: a Go port of
// d2s-client-rejoin-evidence/scripts/analyze.py plus the bounded re-read
// classification of the plan-4a spec (section 2.5). It reads the JSON lines
// kafka-verifiable-producer and kafka-verifiable-consumer print and matches
// records by value, never by offset: after promotion the source and the
// destination can give one offset to different records.

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"math"
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
	// DupUnexplained is any other re-read; it fails the check.
	DupUnexplained DupClass = "unexplained"
)

// Bounds are the re-read windows and limits the checker accepts.
type Bounds struct {
	// FenceBefore and FenceAfter bound a fence-onset re-read's first read
	// around the watcher's fence onset.
	FenceBefore, FenceAfter time.Duration
	// SwitchBefore and SwitchAfter bound a switch re-read: its first read is no
	// earlier than SwitchBefore before the watcher's switch time, and its
	// second read no later than SwitchAfter after it.
	SwitchBefore, SwitchAfter time.Duration
	// MaxPollRecords caps a manual-commit member's switch re-reads (one poll);
	// an auto-commit member's cap is autoCommitSwitchLimit.
	MaxPollRecords int
	// AutoCommitInterval and ProduceRatePerSec cap an auto-commit member's
	// fence-onset re-reads: what it can consume between two commits.
	AutoCommitInterval time.Duration
	ProduceRatePerSec  int
	// BatchSlack widens the window a manual-commit member's batch is looked
	// for in: the tool logs a batch's records_consumed line after its records.
	BatchSlack time.Duration
	// MinAckedAfterSwitch is how many records a group's producer must have
	// acknowledged after the switch: without them, producing through the
	// converted route was not shown to work.
	MinAckedAfterSwitch int
}

// DefaultBounds are the spec's first estimates (2.5); tune them here, and
// record the change, if the first real runs show they are wrong.
func DefaultBounds() Bounds {
	return Bounds{
		FenceBefore:        10 * time.Second,
		FenceAfter:         15 * time.Second, // the CR shows the fence before the pods apply it
		SwitchBefore:       5 * time.Second,
		SwitchAfter:        60 * time.Second,
		MaxPollRecords:     500,
		AutoCommitInterval: 5 * time.Second,
		ProduceRatePerSec:  20,
		BatchSlack:         time.Second,

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

// Batch is one records_consumed event: one poll's worth of records.
type Batch struct {
	TimestampMs int64
	Count       int
}

// ConsumerLog is one kafka-verifiable-consumer member's output.
type ConsumerLog struct {
	Member        string
	Records       []ConsumedRecord
	Batches       []Batch
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
	Count     int     `json:"count"`
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
		case "records_consumed":
			cl.Batches = append(cl.Batches, Batch{TimestampMs: e.Timestamp, Count: e.Count})
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
	AutoCommit  bool
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
// error, nothing missed, every re-read explained and within its bound.
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
	batches := map[string][]Batch{}
	for _, c := range in.Consumers {
		res.Assignments[c.Member] += c.Assignments
		res.Revocations[c.Member] += c.Revocations
		res.FailedCommits += c.FailedCommits
		res.Skipped += c.Skipped
		batches[c.Member] = append(batches[c.Member], c.Batches...)
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
	fenceByMember, switchByMember := map[string]int{}, map[string]int{}
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
			fenceByMember[rs[0].Member]++
		case in.Windows.SwitchDoneMs > 0 && rs[0].TimestampMs >= switchLo && rs[1].TimestampMs <= switchHi:
			d.Class = DupSwitch
			switchByMember[rs[1].Member]++
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
	if n := res.ByClass[DupUnexplained]; n > 0 {
		for _, d := range res.Duplicates {
			if d.Class == DupUnexplained {
				fail("%d record(s) re-read outside the fence-onset and switch windows (e.g. %s)", n, describeReads(d))
				break
			}
		}
	}
	for _, m := range sortedKeys(fenceByMember) {
		limit := fenceOnsetLimit(in.AutoCommit, b, batches[m], fenceLo, fenceHi)
		if n := fenceByMember[m]; n > limit {
			fail("member %s re-read %d record(s) consumed at the fence onset, more than %d", m, n, limit)
		}
	}
	for _, m := range sortedKeys(switchByMember) {
		n := switchByMember[m]
		if !in.AutoCommit {
			if n > b.MaxPollRecords {
				fail("member %s re-read %d record(s) after the switch, more than one poll (%d)", m, n, b.MaxPollRecords)
			}
			continue
		}
		if limit := autoCommitSwitchLimit(b, in.Windows); n > limit {
			fail("member %s re-read %d record(s) after the switch, more than %d (the fence's backlog plus one auto-commit interval at %d/s, plus one poll)",
				m, n, limit, b.ProduceRatePerSec)
		}
	}
	return res
}

// autoCommitSwitchLimit is how many switch re-reads an auto-commit member may
// have. A manual-commit member commits after every batch, so its first commit
// after the switch fails at once and it re-reads at most one poll. An
// auto-commit member keeps polling until its next auto-commit or heartbeat
// fails, so it can re-read the backlog the fence built (it consumed past its
// blocked commits) plus what it fetches in one more auto-commit interval, plus
// the poll in flight: rate x ((switch - fence onset) + interval) + one poll.
func autoCommitSwitchLimit(b Bounds, w Windows) int {
	fenced := time.Duration(w.SwitchDoneMs-w.FenceOnsetMs) * time.Millisecond
	if fenced < 0 {
		fenced = 0
	}
	return int(math.Ceil((fenced+b.AutoCommitInterval).Seconds()*float64(b.ProduceRatePerSec))) + b.MaxPollRecords
}

// fenceOnsetLimit is how many fence-onset re-reads a member may have: what it
// consumes between two auto-commits, or (manual commit, a commit after every
// poll) its largest batch around the fence onset.
func fenceOnsetLimit(autoCommit bool, b Bounds, batches []Batch, lo, hi int64) int {
	if autoCommit {
		return int(math.Ceil(b.AutoCommitInterval.Seconds() * float64(b.ProduceRatePerSec)))
	}
	largest := 0
	for _, bt := range batches {
		if bt.TimestampMs >= lo && bt.TimestampMs <= hi+b.BatchSlack.Milliseconds() && bt.Count > largest {
			largest = bt.Count
		}
	}
	return largest
}

func describeReads(d Duplicate) string {
	parts := make([]string, len(d.Reads))
	for i, r := range d.Reads {
		parts[i] = fmt.Sprintf("%s p%d@%d t=%d", r.Member, r.Partition, r.Offset, r.TimestampMs)
	}
	return d.Value + ": " + strings.Join(parts, "; ")
}

func sortedKeys(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
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
		b.WriteString("   RESULT: PASS — records acked after the switch, no send error; every acked record read at least once; every re-read bounded\n")
	} else {
		b.WriteString("   RESULT: FAIL\n")
		for _, f := range r.Failures {
			fmt.Fprintf(&b, "      - %s\n", f)
		}
	}
	return b.String()
}
