//go:build e2e

package retries_e2e

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Step banners and lines kcp prints, which the tests wait for to time a fault. They are
// matched as substrings, without the emoji that precedes them.
const (
	lineInitialize     = "Initializing TBM migration"
	lineFence          = "Fencing batch"
	lineVerifyFence    = "Verifying fence"
	lineMonitorSource  = "Monitoring source offsets" // printed after the check's first read, before its 10 s wait
	linePromoteRequest = "mirror topics..."          // "Promoting N mirror topics...", printed just before the request
	linePromoteAccept  = "promotion accepted"

	// Step names, as the step banners print them.
	stepVerifyFence = "Verifying fence"
	stepPromote     = "Promoting batch"

	// retryLine is the warning every retry logs; giveUpLine is logged when the budget runs out.
	retryLine  = "connection error, retrying"
	giveUpLine = "giving up after connection errors"
)

// stepWait bounds how long a test waits for kcp to reach a step.
const stepWait = 3 * time.Minute

// detectProducers makes the verify-fence step run its 10-second unrouted-producer check,
// which is off unless the manifest asks for it.
const detectProducers = "    policies:\n      detectUnroutedProducersDuration: 10s\n"

func executeArgs(manifest string) []string {
	return []string{"migration", "cutover", "execute", "--migration-yaml", manifest}
}

// requireCompleted asserts a run exited cleanly and said it completed.
func requireCompleted(t *testing.T, out string, err error) {
	t.Helper()
	require.NoError(t, err, "the run must complete")
	require.Contains(t, strings.ToLower(out), "migration complete", "execute must report completion")
}

// requireRetried asserts kcp retried operation (a retry.Do operation name such as
// kafka.list_offsets): the fault really reached a call kcp retries, and the run did not
// pass by luck of timing.
func requireRetried(t *testing.T, out, operation string) {
	t.Helper()
	require.Contains(t, out, retryLine, "kcp must have logged a retry")
	require.Contains(t, out, operation, "the retried call must be %s", operation)
}

// begin is what every test starts with: its own env, the cleanup that removes any fault it
// leaves behind, and a check the slice of topics it migrates is untouched.
func begin(t *testing.T, lo, hi int) (*env, context.Context, []string) {
	t.Helper()
	e := newEnv()
	e.flushFaults(t)
	ctx := context.Background()
	topics := e.topicRange(lo, hi)
	e.requirePristine(t, ctx, topics)
	return e, ctx, topics
}

// A blip is a fault against one cluster that kcp must ride out: it is held until kcp has
// logged a retry (so the fault demonstrably reached a call kcp retries), kept a little
// longer, then lifted. The run must complete with the world migrated.
//
// Where a fault lands is set by when the test starts it. Steps that finish in
// milliseconds (the lag wait, the zero-lag check) cannot be hit by a fault timed to
// their own banner, which arrives too late, so those faults start at an earlier,
// slower step (the fence takes several seconds to reach every gateway pod) and the
// test asserts which step kcp retried in.

// blipHold is how long a fault is kept after kcp's first retry.
const blipHold = 3 * time.Second

func TestBlip_SourceResetsAtTheStartOfTheMigration(t *testing.T) {
	e, ctx, topics := begin(t, 46, 50)
	mani := e.writeManifest(t, "source-reset-start", topics, manifestOpts{})
	e.snapshot(t, ctx, "before", "BEFORE", topics)

	run := e.startKCP(t, executeArgs(mani)...)
	require.True(t, run.WaitFor(lineInitialize, stepWait), "kcp must start the migration")
	step := e.blipUntilRetried(t, run, e.sourceKafka(), faultReset, blipHold)
	out, err := run.Wait(e, "kcp-run-1-execute.log")

	t.Logf("the source reset was retried in the %q step", step)
	requireCompleted(t, out, err)
	requireRetried(t, out, "kafka.list_offsets")
	assert.NotContains(t, out, giveUpLine)
	e.requireMigrated(t, ctx, topics)
	e.snapshot(t, ctx, "after", "AFTER", topics)
}

func TestBlip_SourceResetsDuringTheUnroutedProducerCheck(t *testing.T) {
	e, ctx, topics := begin(t, 6, 10)
	mani := e.writeManifest(t, "source-reset-producer-check", topics, manifestOpts{policies: detectProducers})
	e.snapshot(t, ctx, "before", "BEFORE", topics)

	run := e.startKCP(t, executeArgs(mani)...)
	// The check reads the source, prints this line, waits 10 seconds, then reads it again:
	// the second read is what the fault hits.
	require.True(t, run.WaitFor(lineMonitorSource, stepWait), "kcp must reach the producer check's wait")
	step := e.blipUntilRetried(t, run, e.sourceKafka(), faultReset, blipHold)
	out, err := run.Wait(e, "kcp-run-1-execute.log")

	assert.Contains(t, step, stepVerifyFence, "the retry must be in the fence check")
	requireCompleted(t, out, err)
	requireRetried(t, out, "kafka.list_offsets")
	assert.Contains(t, out, "no unrouted producers detected", "the check must have run to its verdict")
	assert.NotContains(t, out, "removing fence", "a reset the retries ride out must not roll the fence back")
	assert.NotContains(t, out, giveUpLine)
	e.requireMigrated(t, ctx, topics)
	e.snapshot(t, ctx, "after", "AFTER", topics)
}

func TestBlip_DestinationResetsBeforePromote(t *testing.T) {
	e, ctx, topics := begin(t, 11, 15)
	mani := e.writeManifest(t, "destination-reset-promote", topics, manifestOpts{})
	e.snapshot(t, ctx, "before", "BEFORE", topics)

	run := e.startKCP(t, executeArgs(mani)...)
	// Cut while the fence is being applied: the first destination read afterwards is the
	// zero-lag check at the start of the promote step.
	require.True(t, run.WaitFor(lineFence, stepWait), "kcp must reach the fence step")
	step := e.blipUntilRetried(t, run, e.destKafka(), faultReset, blipHold)
	out, err := run.Wait(e, "kcp-run-1-execute.log")

	assert.Contains(t, step, stepPromote, "the retry must be in the promote step")
	requireCompleted(t, out, err)
	requireRetried(t, out, "kafka.list_offsets")
	assert.NotContains(t, out, giveUpLine)
	e.requireMigrated(t, ctx, topics)
	e.snapshot(t, ctx, "after", "AFTER", topics)
}

func TestBlip_RESTResetsBeforePromote(t *testing.T) {
	e, ctx, topics := begin(t, 16, 20)
	mani := e.writeManifest(t, "rest-reset-promote", topics, manifestOpts{})
	e.snapshot(t, ctx, "before", "BEFORE", topics)

	run := e.startKCP(t, executeArgs(mani)...)
	// kcp makes no REST call while it fences, so the first one after the cut is the promote request.
	require.True(t, run.WaitFor(lineFence, stepWait), "kcp must reach the fence step")
	step := e.blipUntilRetried(t, run, e.destREST(), faultReset, blipHold)
	out, err := run.Wait(e, "kcp-run-1-execute.log")

	assert.Contains(t, step, stepPromote, "the retry must be in the promote step")
	requireCompleted(t, out, err)
	requireRetried(t, out, "rest.")
	assert.NotContains(t, out, giveUpLine)
	e.requireMigrated(t, ctx, topics)
	e.snapshot(t, ctx, "after", "AFTER", topics)
}

// The promote request reaches the cluster and is accepted, but its answer is lost and the
// connection resets. kcp must not resend blindly: it reads the mirror status, finds the
// topics already promoted and carries on.
func TestBlip_PromoteIsAcceptedButTheReplyIsLost(t *testing.T) {
	e, ctx, topics := begin(t, 21, 25)
	mani := e.writeManifest(t, "promote-reply-lost", topics, manifestOpts{})
	e.snapshot(t, ctx, "before", "BEFORE", topics)

	run := e.startKCP(t, executeArgs(mani)...)
	require.True(t, run.WaitFor(lineFence, stepWait), "kcp must reach the fence step")
	// No REST reply reaches kcp from here on. kcp makes no REST call while it fences, so
	// none is lost before the promote request; the rule is in place well before it.
	restoreReplies := e.dropReplies(t, e.destREST())
	defer restoreReplies()
	if !run.WaitFor(linePromoteRequest, time.Minute) {
		restoreReplies()
		require.FailNow(t, "kcp never sent the promote request")
	}
	time.Sleep(1500 * time.Millisecond) // the request has reached the cluster and been accepted
	restoreReset := e.cut(t, e.destREST(), faultReset)
	time.Sleep(3 * time.Second)
	restoreReset()
	restoreReplies()
	out, err := run.Wait(e, "kcp-run-1-execute.log")

	requireCompleted(t, out, err)
	requireRetried(t, out, "rest.promote")
	assert.NotContains(t, out, giveUpLine)
	e.requireMigrated(t, ctx, topics)
	e.snapshot(t, ctx, "after", "AFTER", topics)
}

// An outage is a fault that outlasts the retry budget. kcp must give up, exit non-zero and
// leave a world a plain re-run completes.

// The source disappears during the fence check, before anything is promoted: kcp gives up,
// removes the fence it put up, and a re-run once the source is back completes.
func TestOutage_SourceGoneDuringTheUnroutedProducerCheck(t *testing.T) {
	e, ctx, topics := begin(t, 26, 30)
	mani := e.writeManifest(t, "source-gone-producer-check", topics, manifestOpts{policies: detectProducers})
	e.snapshot(t, ctx, "before", "BEFORE", topics)

	run := e.startKCP(t, executeArgs(mani)...)
	require.True(t, run.WaitFor(lineVerifyFence, stepWait), "kcp must reach the fence check")
	restore := e.cut(t, e.sourceKafka(), faultReset)
	out1, err1 := run.Wait(e, "kcp-run-1-outage.log")
	restore()

	require.Error(t, err1, "a source gone for longer than the retry budget must fail the run")
	assert.Contains(t, out1, giveUpLine, "kcp must say it gave up")
	assert.Contains(t, out1, "kafka.list_offsets")
	assert.Contains(t, out1, "removing fence", "kcp must take its fence down: nothing was promoted")
	e.snapshot(t, ctx, "after-outage", "AFTER the outage (expect unfenced, mirrors ACTIVE, route → source)", topics)
	cr := e.readCR(t, ctx)
	ms := e.mirrorStatus(t, ctx)
	for _, tp := range topics {
		assert.Falsef(t, e.isFenced(cr, tp), "%s must not stay fenced: nothing was promoted, so kcp takes its fence down", tp)
		assert.Falsef(t, e.isSwitchedToTarget(cr, tp), "%s must not be switched", tp)
		assert.Equalf(t, "ACTIVE", ms[tp], "%s must still be an ACTIVE mirror", tp)
	}

	out2, err2 := e.runKCP(t, "kcp-run-2-rerun.log", executeArgs(mani)...)
	requireCompleted(t, out2, err2)
	e.requireMigrated(t, ctx, topics)
	e.snapshot(t, ctx, "after-rerun", "AFTER the re-run", topics)
}

// The destination REST endpoint disappears once promotion has started: past that point
// kcp cannot take the fence down without splitting the data between the clusters, so it
// gives up with the fence in place, and a re-run rolls the migration forward.
func TestOutage_RESTGoneAfterPromotionStarted(t *testing.T) {
	e, ctx, topics := begin(t, 51, 55)
	mani := e.writeManifest(t, "rest-gone-after-promote", topics, manifestOpts{})
	e.snapshot(t, ctx, "before", "BEFORE", topics)

	run := e.startKCP(t, executeArgs(mani)...)
	require.True(t, run.WaitFor(linePromoteAccept, stepWait), "kcp must get a promote request accepted")
	restore := e.cut(t, e.destREST(), faultReset)
	out1, err1 := run.Wait(e, "kcp-run-1-outage.log")
	restore()

	require.Error(t, err1, "a REST endpoint gone for longer than the retry budget must fail the run")
	assert.Contains(t, out1, giveUpLine, "kcp must say it gave up")
	assert.NotContains(t, out1, "removing fence", "kcp must not take its fence down: promotion has started")
	e.snapshot(t, ctx, "after-outage", "AFTER the outage (expect fenced, route → source, promotion started)", topics)
	cr := e.readCR(t, ctx)
	ms := e.mirrorStatus(t, ctx)
	promoted := 0
	for _, tp := range topics {
		assert.Truef(t, e.isFenced(cr, tp), "%s must stay fenced: promotion has started, so taking the fence down would split the data", tp)
		assert.Falsef(t, e.isSwitchedToTarget(cr, tp), "%s must not be switched yet", tp)
		if ms[tp] == "PENDING_STOPPED" || ms[tp] == "STOPPED" {
			promoted++
		}
	}
	assert.Positive(t, promoted, "at least one mirror must have been promoted before the outage")

	out2, err2 := e.runKCP(t, "kcp-run-2-rerun.log", executeArgs(mani)...)
	requireCompleted(t, out2, err2)
	e.requireMigrated(t, ctx, topics)
	e.snapshot(t, ctx, "after-rerun", "AFTER the re-run", topics)
}

// The reconnect storm: every cluster resets in short pulses for the whole run, as when a
// fence drains a gateway's clients and they all reconnect at once.
func TestStorm_EveryClusterResetsInPulses(t *testing.T) {
	e, ctx, topics := begin(t, 36, 40)
	mani := e.writeManifest(t, "storm", topics, manifestOpts{policies: detectProducers})
	e.snapshot(t, ctx, "before", "BEFORE", topics)

	run := e.startKCP(t, executeArgs(mani)...)
	var wg sync.WaitGroup
	pulses := 0
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-run.done:
				return
			case <-time.After(3 * time.Second):
			}
			var restores []func()
			for _, tg := range []target{e.sourceKafka(), e.destKafka(), e.destREST()} {
				restore, err := e.tryCut(t, tg, faultReset)
				if err != nil {
					t.Errorf("storm pulse: %v", err)
					continue
				}
				restores = append(restores, restore)
			}
			pulses++
			select {
			case <-run.done:
			case <-time.After(3 * time.Second):
			}
			for _, restore := range restores {
				restore()
			}
		}
	}()
	out, err := run.Wait(e, "kcp-run-1-execute.log")
	wg.Wait()

	t.Logf("storm: %d pulses against the source, the destination and its REST endpoint", pulses)
	requireCompleted(t, out, err)
	assert.GreaterOrEqual(t, pulses, 2, "the storm must have hit the run more than once")
	require.Contains(t, out, retryLine, "kcp must have logged a retry")
	assert.NotContains(t, out, giveUpLine)
	e.requireMigrated(t, ctx, topics)
	e.snapshot(t, ctx, "after", "AFTER", topics)
}

// A failed login is not a connection failure: no retry can fix it, so kcp must fail at
// once instead of spending the retry budget.
func TestLoginFailure_IsNotRetried(t *testing.T) {
	e, ctx, topics := begin(t, 41, 45)
	mani := e.writeManifest(t, "wrong-credentials", topics, manifestOpts{destCreds: "dest-kafka-creds-wrong.yaml"})
	e.snapshot(t, ctx, "before", "BEFORE", topics)

	start := time.Now()
	out, err := e.runKCP(t, "kcp-run-1-execute.log", executeArgs(mani)...)

	require.Error(t, err, "wrong credentials must fail the run")
	assert.Less(t, time.Since(start), time.Minute, "a failed login must fail at once, not after the retry budget")
	assert.NotContains(t, out, retryLine, "a failed login must not be retried")
	assert.NotContains(t, out, giveUpLine)
	e.requirePristine(t, ctx, topics)
}
