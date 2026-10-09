package routeconversion

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// The fixtures are real kafka-verifiable-producer / kafka-verifiable-consumer
// JSON lines (the shapes in d2s-client-rejoin-evidence/run2-two-step-clients),
// with timestamps rebased so the windows are easy to read: the fence lands at
// t=100s and the route is static at t=160s.
const (
	fenceAt  int64 = 100_000
	switchAt int64 = 160_000
)

var fixtureWindows = Windows{FenceOnsetMs: fenceAt, SwitchDoneMs: switchAt}

const producerFixture = `{"timestamp":1000,"name":"startup_complete"}
[2026-10-08 10:50:14,634] WARN [Producer clientId=producer-1] Connection to node 0 terminated during authentication. (org.apache.kafka.clients.NetworkClient)
{"timestamp":90000,"name":"producer_send_success","key":null,"value":"7.0","offset":754,"topic":"rc-topic-001","partition":0}
{"timestamp":91000,"name":"producer_send_success","key":null,"value":"7.1","offset":755,"topic":"rc-topic-001","partition":0}
{"timestamp":99000,"name":"producer_send_success","key":null,"value":"7.2","offset":756,"topic":"rc-topic-001","partition":0}
{"timestamp":170000,"name":"producer_send_success","key":null,"value":"7.3","offset":757,"topic":"rc-topic-001","partition":0}
{"timestamp":172000,"name":"shutdown_complete"}
{"timestamp":172001,"name":"tool_data","sent":4,"acked":4,"target_throughput":20,"avg_throughput":20.0}
`

// producerWithSendErrorFixture is producerFixture plus one failed send (7.4)
// after the switch.
const producerWithSendErrorFixture = `{"timestamp":1000,"name":"startup_complete"}
{"timestamp":90000,"name":"producer_send_success","key":null,"value":"7.0","offset":754,"topic":"rc-topic-001","partition":0}
{"timestamp":91000,"name":"producer_send_success","key":null,"value":"7.1","offset":755,"topic":"rc-topic-001","partition":0}
{"timestamp":99000,"name":"producer_send_success","key":null,"value":"7.2","offset":756,"topic":"rc-topic-001","partition":0}
{"timestamp":170000,"name":"producer_send_success","key":null,"value":"7.3","offset":757,"topic":"rc-topic-001","partition":0}
{"timestamp":171000,"name":"producer_send_error","topic":"rc-topic-001","key":null,"value":"7.4","exception":"class org.apache.kafka.common.errors.NotEnoughReplicasException","message":"Messages are rejected since there are fewer in-sync replicas than required."}
{"timestamp":172000,"name":"shutdown_complete"}
{"timestamp":172001,"name":"tool_data","sent":5,"acked":4,"target_throughput":20,"avg_throughput":20.0}
`

// producerBeforeSwitchFixture acknowledges records only before the switch:
// producing through the converted route was never shown to work.
const producerBeforeSwitchFixture = `{"timestamp":1000,"name":"startup_complete"}
{"timestamp":90000,"name":"producer_send_success","key":null,"value":"7.0","offset":754,"topic":"rc-topic-001","partition":0}
{"timestamp":91000,"name":"producer_send_success","key":null,"value":"7.1","offset":755,"topic":"rc-topic-001","partition":0}
{"timestamp":99000,"name":"producer_send_success","key":null,"value":"7.2","offset":756,"topic":"rc-topic-001","partition":0}
{"timestamp":172000,"name":"shutdown_complete"}
`

func consumed(ts int64, value string, partition int32, offset int64) string {
	return fmt.Sprintf(`{"recordts":%d,"timestamp":%d,"name":"record_data","key":null,"value":%q,"topic":"rc-topic-001","partition":%d,"offset":%d}`,
		ts-5, ts, value, partition, offset)
}

func batch(ts int64, count int) string {
	return fmt.Sprintf(`{"timestamp":%d,"name":"records_consumed","count":%d,"partitions":[{"topic":"rc-topic-001","partition":0,"count":%d,"minOffset":0,"maxOffset":0}]}`, ts, count, count)
}

func committed(ts int64, ok bool) string {
	if ok {
		return fmt.Sprintf(`{"timestamp":%d,"name":"offsets_committed","offsets":[{"topic":"rc-topic-001","partition":0,"offset":1}],"success":true}`, ts)
	}
	return fmt.Sprintf(`{"timestamp":%d,"name":"offsets_committed","offsets":[{"topic":"rc-topic-001","partition":0,"offset":1}],"error":"The coordinator is not aware of this member.","success":false}`, ts)
}

const assigned = `{"timestamp":2000,"name":"partitions_assigned","partitions":[{"topic":"rc-topic-001","partition":0}]}`
const revoked = `{"timestamp":161500,"name":"partitions_revoked","partitions":[{"topic":"rc-topic-001","partition":0}]}`

func lines(ls ...string) string { return strings.Join(ls, "\n") + "\n" }

func mustProducer(t *testing.T, s string) ProducerLog {
	t.Helper()
	p, err := ParseProducerLog(strings.NewReader(s))
	require.NoError(t, err)
	return p
}

func mustConsumer(t *testing.T, member, s string) ConsumerLog {
	t.Helper()
	c, err := ParseConsumerLog(member, strings.NewReader(s))
	require.NoError(t, err)
	return c
}

func check(t *testing.T, auto bool, b Bounds, w Windows, producer string, members map[string]string) GroupResult {
	t.Helper()
	in := CheckInput{Group: "ct-manual", Topic: "rc-topic-001", ValuePrefix: "7", AutoCommit: auto,
		Producers: []ProducerLog{mustProducer(t, producer)}, Windows: w, Bounds: b}
	for m, s := range members {
		in.Consumers = append(in.Consumers, mustConsumer(t, m, s))
	}
	return Check(in)
}

func TestParseProducerLog_ReadsAckedAndFailedSends(t *testing.T) {
	p := mustProducer(t, producerFixture)
	require.Len(t, p.Acked, 4)
	require.Equal(t, ProducedRecord{Value: "7.2", Partition: 0, Offset: 756, TimestampMs: 99000}, p.Acked["7.2"])
	require.Empty(t, p.SendErrors)
	require.Equal(t, 1, p.Skipped, "the log4j line is skipped, not an error")

	p = mustProducer(t, producerWithSendErrorFixture)
	require.Len(t, p.Acked, 4)
	require.Equal(t, SendError{Value: "7.4", TimestampMs: 171000,
		Exception: "class org.apache.kafka.common.errors.NotEnoughReplicasException",
		Message:   "Messages are rejected since there are fewer in-sync replicas than required."}, p.SendErrors["7.4"])
}

func TestParseConsumerLog_CountsEveryEventKind(t *testing.T) {
	c := mustConsumer(t, "a", lines(assigned, consumed(95000, "7.0", 0, 754), batch(95001, 1), committed(95002, true),
		committed(161000, false), revoked))
	require.Len(t, c.Records, 1)
	require.Equal(t, ConsumedRecord{Topic: "rc-topic-001", Value: "7.0", Read: Read{TimestampMs: 95000, Member: "a", Partition: 0, Offset: 754}}, c.Records[0])
	require.Equal(t, []Batch{{TimestampMs: 95001, Count: 1}}, c.Batches)
	require.Equal(t, 1, c.Assignments)
	require.Equal(t, 1, c.Revocations)
	require.Equal(t, 1, c.FailedCommits)
}

func TestCheck_EveryRecordReadOncePasses(t *testing.T) {
	r := check(t, false, DefaultBounds(), fixtureWindows, producerFixture, map[string]string{
		"a": lines(assigned, consumed(90100, "7.0", 0, 754), consumed(91100, "7.1", 0, 755), batch(91101, 2)),
		"b": lines(assigned, consumed(99100, "7.2", 0, 756), consumed(170100, "7.3", 0, 757), batch(170101, 2)),
	})
	require.True(t, r.OK(), r.String())
	require.Equal(t, 4, r.Acked)
	require.Equal(t, 1, r.AckedAfterSwitch)
	require.Zero(t, r.SendErrors)
	require.Equal(t, 4, r.Reads)
	require.Empty(t, r.Missed)
	require.Empty(t, r.Duplicates)
	require.Contains(t, r.String(), "RESULT: PASS")
}

func TestCheck_AMissedRecordFails(t *testing.T) {
	r := check(t, false, DefaultBounds(), fixtureWindows, producerFixture, map[string]string{
		"a": lines(consumed(90100, "7.0", 0, 754), consumed(91100, "7.1", 0, 755)),
	})
	require.False(t, r.OK())
	require.Len(t, r.Missed, 2)
	require.Equal(t, "7.2", r.Missed[0].Value, "missed records are listed in ack order")
	require.Contains(t, strings.Join(r.Failures, "\n"), "2 acknowledged record(s) never read by group ct-manual")
}

func TestCheck_FenceOnsetReReadWithinOneBatchPasses(t *testing.T) {
	// 7.1 and 7.2 are read in one batch 2s before the fence, their commit is
	// blocked, and the group reads them again after the switch.
	r := check(t, false, DefaultBounds(), fixtureWindows, producerFixture, map[string]string{
		"a": lines(consumed(90100, "7.0", 0, 754), batch(90101, 1),
			consumed(98000, "7.1", 0, 755), consumed(98000, "7.2", 0, 756), batch(98001, 2),
			consumed(163000, "7.1", 0, 755), consumed(163000, "7.2", 0, 756), consumed(170100, "7.3", 0, 757)),
	})
	require.True(t, r.OK(), r.String())
	require.Equal(t, 2, r.ByClass[DupFenceOnset])
	require.Zero(t, r.ByClass[DupUnexplained])
}

func TestCheck_FenceOnsetReReadsBeyondTheBatchFail(t *testing.T) {
	// Two fence-onset re-reads but the member's largest batch around the fence
	// is one record: more than one batch was lost.
	r := check(t, false, DefaultBounds(), fixtureWindows, producerFixture, map[string]string{
		"a": lines(consumed(98000, "7.1", 0, 755), batch(98001, 1), consumed(99000, "7.2", 0, 756), batch(99001, 1),
			consumed(90100, "7.0", 0, 754), consumed(163000, "7.1", 0, 755), consumed(163000, "7.2", 0, 756), consumed(170100, "7.3", 0, 757)),
	})
	require.False(t, r.OK())
	require.Contains(t, strings.Join(r.Failures, "\n"), "member a re-read 2 record(s) consumed at the fence onset, more than 1")
}

func TestCheck_AutoCommitFenceOnsetBoundIsIntervalTimesRate(t *testing.T) {
	b := DefaultBounds()
	b.AutoCommitInterval, b.ProduceRatePerSec = time.Second, 1 // at most 1 re-read
	members := map[string]string{
		"a": lines(consumed(90100, "7.0", 0, 754), consumed(98000, "7.1", 0, 755), consumed(98500, "7.2", 0, 756), batch(98501, 2),
			consumed(163000, "7.1", 0, 755), consumed(163000, "7.2", 0, 756), consumed(170100, "7.3", 0, 757)),
	}
	r := check(t, true, b, fixtureWindows, producerFixture, members)
	require.False(t, r.OK())
	require.Contains(t, strings.Join(r.Failures, "\n"), "more than 1")

	b.ProduceRatePerSec = 2
	require.True(t, check(t, true, b, fixtureWindows, producerFixture, members).OK())
}

func TestCheck_SwitchReReadUpToOnePollPasses(t *testing.T) {
	// 7.3 is fetched from the destination right after the switch, the commit
	// fails (unknown member), and the member reads it again after rejoining.
	r := check(t, false, DefaultBounds(), fixtureWindows, producerFixture, map[string]string{
		"a": lines(consumed(90100, "7.0", 0, 754), consumed(91100, "7.1", 0, 755), consumed(99100, "7.2", 0, 756),
			consumed(161000, "7.3", 0, 757), committed(161001, false), revoked, consumed(164000, "7.3", 0, 757)),
	})
	require.True(t, r.OK(), r.String())
	require.Equal(t, 1, r.ByClass[DupSwitch])
	require.Equal(t, 1, r.FailedCommits)
}

func TestCheck_SwitchReReadsBeyondOnePollFail(t *testing.T) {
	b := DefaultBounds()
	b.MaxPollRecords = 1
	r := check(t, false, b, fixtureWindows, producerFixture, map[string]string{
		"a": lines(consumed(90100, "7.0", 0, 754), consumed(91100, "7.1", 0, 755),
			consumed(161000, "7.2", 0, 756), consumed(161000, "7.3", 0, 757),
			consumed(164000, "7.2", 0, 756), consumed(164000, "7.3", 0, 757)),
	})
	require.False(t, r.OK())
	require.Contains(t, strings.Join(r.Failures, "\n"), "member a re-read 2 record(s) after the switch, more than one poll (1)")
}

func TestCheck_ReReadOutsideBothWindowsFails(t *testing.T) {
	// A re-read 40s before the fence: neither the fence nor the switch explains it.
	r := check(t, false, DefaultBounds(), fixtureWindows, producerFixture, map[string]string{
		"a": lines(consumed(60000, "7.0", 0, 754), consumed(61000, "7.0", 0, 754),
			consumed(91100, "7.1", 0, 755), consumed(99100, "7.2", 0, 756), consumed(170100, "7.3", 0, 757)),
	})
	require.False(t, r.OK())
	require.Equal(t, 1, r.ByClass[DupUnexplained])
	require.Contains(t, strings.Join(r.Failures, "\n"), "re-read outside the fence-onset and switch windows (e.g. 7.0:")
}

func TestCheck_AThirdReadIsUnexplained(t *testing.T) {
	r := check(t, false, DefaultBounds(), fixtureWindows, producerFixture, map[string]string{
		"a": lines(consumed(98000, "7.2", 0, 756), batch(98001, 1), consumed(161000, "7.2", 0, 756), consumed(164000, "7.2", 0, 756),
			consumed(90100, "7.0", 0, 754), consumed(91100, "7.1", 0, 755), consumed(170100, "7.3", 0, 757)),
	})
	require.False(t, r.OK())
	require.Equal(t, 1, r.ByClass[DupUnexplained])
}

func TestCheck_IgnoresOtherRunsAndTopicsAndTruncatedLines(t *testing.T) {
	// Records from earlier tests (prefix 6) and another topic are not this run's;
	// the final line was cut short when the consumer was stopped.
	other := strings.Replace(consumed(90500, "7.0", 0, 754), "rc-topic-001", "rc-topic-002", 1)
	r := check(t, false, DefaultBounds(), fixtureWindows, producerFixture, map[string]string{
		"a": lines(consumed(80000, "6.0", 0, 1), consumed(80001, "6.0", 0, 1), other,
			consumed(90100, "7.0", 0, 754), consumed(91100, "7.1", 0, 755), consumed(99100, "7.2", 0, 756),
			consumed(170100, "7.3", 0, 757), consumed(170200, "7.4", 0, 758), `{"timestamp":170300,"name":"record_da`),
	})
	require.True(t, r.OK(), r.String())
	require.Equal(t, 1, r.NotAcked, "7.4 was read although it was never acked")
	require.Equal(t, 2, r.Skipped, "the producer's log4j line and the consumer's cut-off line")
	require.Empty(t, r.Duplicates, "a re-read of another run's record is not this run's duplicate")
}

func TestCheck_NoProducerOutputFailsInsteadOfPassingVacuously(t *testing.T) {
	r := check(t, false, DefaultBounds(), fixtureWindows, `{"timestamp":1000,"name":"startup_complete"}`+"\n", map[string]string{
		"a": lines(consumed(90100, "7.0", 0, 754)),
	})
	require.False(t, r.OK())
	require.Contains(t, strings.Join(r.Failures, "\n"), "no acknowledged record")
}

func TestCheck_MissingWindowsFail(t *testing.T) {
	members := map[string]string{"a": lines(consumed(90100, "7.0", 0, 754), consumed(91100, "7.1", 0, 755),
		consumed(99100, "7.2", 0, 756), consumed(170100, "7.3", 0, 757))}
	r := check(t, false, DefaultBounds(), Windows{SwitchDoneMs: switchAt}, producerFixture, members)
	require.False(t, r.OK())
	require.Contains(t, strings.Join(r.Failures, "\n"), "never saw kcp's conversion fence")
	r = check(t, false, DefaultBounds(), Windows{FenceOnsetMs: fenceAt}, producerFixture, members)
	require.False(t, r.OK())
	require.Contains(t, strings.Join(r.Failures, "\n"), "never saw the route static")
}

func TestCheck_SwitchReReadOfARecordFirstReadLongBeforeTheSwitchFails(t *testing.T) {
	// 7.0 is first read 20s before the switch (outside the fence-onset window
	// too) and again just after it: the switch does not explain that.
	r := check(t, false, DefaultBounds(), fixtureWindows, producerFixture, map[string]string{
		"a": lines(consumed(switchAt-20_000, "7.0", 0, 754), consumed(91100, "7.1", 0, 755), consumed(99100, "7.2", 0, 756),
			consumed(170100, "7.3", 0, 757), consumed(switchAt+3000, "7.0", 0, 754)),
	})
	require.False(t, r.OK())
	require.Equal(t, 1, r.ByClass[DupUnexplained])
	require.Zero(t, r.ByClass[DupSwitch])
}

func TestCheck_FenceOnsetReReadSevenSecondsAfterOnsetPasses(t *testing.T) {
	// The CR shows the fence before the pods apply it: 7.1 is read 7s after
	// onset, its commit blocked, one record in the batch, and re-read after the switch.
	r := check(t, false, DefaultBounds(), fixtureWindows, producerFixture, map[string]string{
		"a": lines(consumed(90100, "7.0", 0, 754), consumed(99100, "7.2", 0, 756),
			consumed(fenceAt+7000, "7.1", 0, 755), batch(fenceAt+7001, 1),
			consumed(163000, "7.1", 0, 755), consumed(170100, "7.3", 0, 757)),
	})
	require.True(t, r.OK(), r.String())
	require.Equal(t, 1, r.ByClass[DupFenceOnset])
}

func TestCheck_NoRecordAckedAfterTheSwitchFails(t *testing.T) {
	// Every acked record predates the switch and is read: nothing shows that
	// producing through the converted route worked.
	r := check(t, false, DefaultBounds(), fixtureWindows, producerBeforeSwitchFixture, map[string]string{
		"a": lines(consumed(90100, "7.0", 0, 754), consumed(91100, "7.1", 0, 755), consumed(99100, "7.2", 0, 756)),
	})
	require.False(t, r.OK())
	require.Empty(t, r.Missed)
	require.Zero(t, r.AckedAfterSwitch)
	require.Contains(t, strings.Join(r.Failures, "\n"),
		"no record acknowledged after the switch: producing through the converted route was not shown to work")
}

func TestCheck_TooFewRecordsAckedAfterTheSwitchFail(t *testing.T) {
	b := DefaultBounds()
	b.MinAckedAfterSwitch = 2
	r := check(t, false, b, fixtureWindows, producerFixture, map[string]string{
		"a": lines(consumed(90100, "7.0", 0, 754), consumed(91100, "7.1", 0, 755), consumed(99100, "7.2", 0, 756),
			consumed(170100, "7.3", 0, 757)),
	})
	require.False(t, r.OK())
	require.Contains(t, strings.Join(r.Failures, "\n"), "only 1 record(s) acknowledged after the switch, fewer than 2")
}

func TestCheck_ASendErrorFails(t *testing.T) {
	// Every acked record is read, but one send failed: with the producer's
	// retries, any send error is a real failure.
	r := check(t, false, DefaultBounds(), fixtureWindows, producerWithSendErrorFixture, map[string]string{
		"a": lines(consumed(90100, "7.0", 0, 754), consumed(91100, "7.1", 0, 755), consumed(99100, "7.2", 0, 756),
			consumed(170100, "7.3", 0, 757)),
	})
	require.False(t, r.OK())
	require.Equal(t, 1, r.SendErrors)
	f := strings.Join(r.Failures, "\n")
	require.Contains(t, f, "1 producer send error(s)")
	require.Contains(t, f, "7.4 at 171000: class org.apache.kafka.common.errors.NotEnoughReplicasException: Messages are rejected since there are fewer in-sync replicas than required.")
}

// switchReReads is a producer and one member's log in which n records are read
// just before the switch, read again 5s after it (one switch re-read each), and
// one more record is acked and read once after the switch.
func switchReReads(n int) (producer, member string) {
	var p, first, second []string
	for i := 0; i < n; i++ {
		v := fmt.Sprintf("7.%d", i)
		p = append(p, fmt.Sprintf(`{"timestamp":%d,"name":"producer_send_success","key":null,"value":%q,"offset":%d,"topic":"rc-topic-001","partition":0}`,
			switchAt-4000, v, i))
		first = append(first, consumed(switchAt-3000, v, 0, int64(i)))
		second = append(second, consumed(switchAt+5000, v, 0, int64(i)))
	}
	last := fmt.Sprintf("7.%d", n)
	p = append(p, fmt.Sprintf(`{"timestamp":%d,"name":"producer_send_success","key":null,"value":%q,"offset":%d,"topic":"rc-topic-001","partition":0}`,
		switchAt+10000, last, n))
	first = append(first, second...)
	first = append(first, consumed(switchAt+10100, last, 0, int64(n)))
	return lines(p...), lines(first...)
}

// A 35s fence: the auto-commit member's switch bound is
// 20/s x (35s + 5s) + 500 = 1300 records.
var fence35sWindows = Windows{FenceOnsetMs: switchAt - 35_000, SwitchDoneMs: switchAt}

func TestCheck_AutoCommitSwitchReReadsUpToTheFenceBacklogPass(t *testing.T) {
	// The live finding: an auto-commit member keeps polling after the switch
	// until its next heartbeat or auto-commit, re-reading the fence's backlog.
	producer, member := switchReReads(716)
	r := check(t, true, DefaultBounds(), fence35sWindows, producer, map[string]string{"a": member})
	require.True(t, r.OK(), r.String())
	require.Equal(t, 716, r.ByClass[DupSwitch])
}

func TestCheck_AutoCommitSwitchReReadsBeyondTheFenceBacklogFail(t *testing.T) {
	producer, member := switchReReads(1301)
	r := check(t, true, DefaultBounds(), fence35sWindows, producer, map[string]string{"a": member})
	require.False(t, r.OK())
	require.Contains(t, strings.Join(r.Failures, "\n"), "member a re-read 1301 record(s) after the switch, more than 1300")
}

func TestCheck_ManualCommitSwitchBoundStaysOnePoll(t *testing.T) {
	producer, member := switchReReads(716)
	r := check(t, false, DefaultBounds(), fence35sWindows, producer, map[string]string{"a": member})
	require.False(t, r.OK())
	require.Contains(t, strings.Join(r.Failures, "\n"), "member a re-read 716 record(s) after the switch, more than one poll (500)")

	producer, member = switchReReads(500)
	require.True(t, check(t, false, DefaultBounds(), fence35sWindows, producer, map[string]string{"a": member}).OK())
}
