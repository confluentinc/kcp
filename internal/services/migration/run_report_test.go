package migration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// readRunReport loads and decodes a run report written to disk.
func readRunReport(t *testing.T, path string) RunReport {
	t.Helper()

	data, err := os.ReadFile(path)
	require.NoError(t, err, "run report should exist at %s", path)

	var report RunReport
	require.NoError(t, json.Unmarshal(data, &report), "run report should be valid JSON")
	return report
}

// stageEvents returns the recorded stage events in order, for comparing against
// the workflow sequence without asserting on timings.
func stageEvents(report RunReport) []string {
	events := make([]string, 0, len(report.Stages))
	for _, s := range report.Stages {
		events = append(events, s.Event)
	}
	return events
}

// TestRunReport_NilRecorderIsInert covers the disabled path: NewRunReportRecorder
// returns nil for an empty path, and every method must tolerate that, because the
// orchestrator calls them unconditionally.
func TestRunReport_NilRecorderIsInert(t *testing.T) {
	r := NewRunReportRecorder("", "migration-1", 3, 0, StateUninitialized)
	require.Nil(t, r, "an empty path should disable reporting")

	// None of these may panic.
	r.StageStarted(EventFence, StateLagsOk, StateFenced)
	r.StageEnded(StateFenced)
	r.StageSkipped(EventInitialize)
	r.StageFailed(errors.New("boom"))
	r.Finish(StateFenced, nil)
}

// TestRunReport_FullWorkflow drives a real orchestrator run and asserts the
// report describes it: every forward stage, in workflow order, with a completed
// outcome. This tests the wiring, not just the writer — the recorder is only
// useful if the orchestrator actually calls it at the right points.
func TestRunReport_FullWorkflow(t *testing.T) {
	orch, config, _ := newHappyPathOrchestrator(t, StateUninitialized, nil)
	reportPath := filepath.Join(t.TempDir(), "run-report.json")

	recorder := NewRunReportRecorder(reportPath, config.MigrationId, len(config.Topics), 42, config.CurrentState)
	require.NotNil(t, recorder)
	orch.SetRunReportRecorder(recorder)

	err := orch.Execute(context.Background(), 42, "api-key", "api-secret")
	require.NoError(t, err)
	recorder.Finish(config.CurrentState, nil)

	// Surface the artifact itself: this document is a consumer-facing contract,
	// so `go test -v -run TestRunReport_FullWorkflow` should show what it emits.
	raw, err := os.ReadFile(reportPath)
	require.NoError(t, err)
	t.Logf("run report:\n%s", raw)

	report := readRunReport(t, reportPath)

	assert.Equal(t, config.MigrationId, report.MigrationId)
	assert.Equal(t, len(config.Topics), report.Topics)
	assert.Equal(t, int64(42), report.LagThreshold)
	assert.Equal(t, StateSwitched, report.FinalState)
	assert.Equal(t, RunOutcomeCompleted, report.Outcome)
	assert.Empty(t, report.Error)
	assert.Empty(t, report.SkippedStages, "a run from uninitialized skips nothing")

	// Every forward transition in the canonical workflow should be recorded, in order.
	expected := make([]string, 0, len(canonicalWorkflow))
	for _, step := range canonicalWorkflow {
		expected = append(expected, step.Event)
	}
	assert.Equal(t, expected, stageEvents(report))

	// Each stage must carry real timings and the edge it traversed.
	for i, stage := range report.Stages {
		assert.Equal(t, canonicalWorkflow[i].FromState, stage.From, "stage %s from-state", stage.Event)
		assert.Equal(t, canonicalWorkflow[i].ToState, stage.To, "stage %s to-state", stage.Event)
		assert.False(t, stage.Started.IsZero(), "stage %s should have a start time", stage.Event)
		assert.False(t, stage.Ended.IsZero(), "stage %s should have an end time", stage.Event)
		assert.GreaterOrEqual(t, stage.DurationMs, int64(0), "stage %s duration", stage.Event)
		assert.False(t, stage.Failed, "stage %s should not be marked failed", stage.Event)
	}

	// The run's duration must cover the stages it contains.
	assert.False(t, report.EndedAt.IsZero())
	assert.GreaterOrEqual(t, report.DurationMs, int64(0))
	assert.NotEmpty(t, report.KcpVersion, "the report should stamp the writing binary's version")
}

// TestRunReport_ResumeRecordsSkippedStages verifies that a resumed run
// distinguishes stages it ran from stages it passed over. Skipped stages are
// named in SkippedStages rather than appearing in Stages with zero timings, so a
// consumer never has to test for a zero timestamp.
func TestRunReport_ResumeRecordsSkippedStages(t *testing.T) {
	orch, config, _ := newHappyPathOrchestrator(t, StateLagsOk, nil)
	reportPath := filepath.Join(t.TempDir(), "run-report.json")

	recorder := NewRunReportRecorder(reportPath, config.MigrationId, len(config.Topics), 0, config.CurrentState)
	orch.SetRunReportRecorder(recorder)

	require.NoError(t, orch.Execute(context.Background(), 0, "api-key", "api-secret"))
	recorder.Finish(config.CurrentState, nil)

	report := readRunReport(t, reportPath)

	// Resuming at lags_ok means initialize and wait_for_lags were already done.
	assert.Equal(t, []string{EventInitialize, EventWaitForLags}, report.SkippedStages)
	assert.NotContains(t, stageEvents(report), EventInitialize)
	assert.NotContains(t, stageEvents(report), EventWaitForLags)
	assert.Equal(t, EventFence, report.Stages[0].Event, "the first stage run should be the fence")
	assert.Equal(t, RunOutcomeCompleted, report.Outcome)
}

// TestRunReport_FailedRunIsRecorded is the case the perf rig depends on: a run
// that does not complete must still leave a report naming the stage that failed.
// A failure is a result, not an absence of one.
func TestRunReport_FailedRunIsRecorded(t *testing.T) {
	overrides := orchestratorOverrides{
		applyGatewayYAMLFn: func(ctx context.Context, namespace, name string, yaml []byte) error {
			return fmt.Errorf("apply gateway failed: forbidden")
		},
	}
	orch, config, _ := newHappyPathOrchestrator(t, StateUninitialized, nil, overrides)
	reportPath := filepath.Join(t.TempDir(), "run-report.json")

	recorder := NewRunReportRecorder(reportPath, config.MigrationId, len(config.Topics), 0, config.CurrentState)
	orch.SetRunReportRecorder(recorder)

	execErr := orch.Execute(context.Background(), 0, "api-key", "api-secret")
	require.Error(t, execErr)
	recorder.Finish(config.CurrentState, execErr)

	report := readRunReport(t, reportPath)

	assert.Equal(t, RunOutcomeFailed, report.Outcome)
	assert.Contains(t, report.Error, "forbidden")
	assert.Equal(t, StateLagsOk, report.FinalState, "the fence failed, so the run ends at lags_ok")

	// The fence stage should be present and flagged, not silently dropped.
	fence := report.Stages[len(report.Stages)-1]
	assert.Equal(t, EventFence, fence.Event)
	assert.True(t, fence.Failed, "the failing stage must be marked failed")
	assert.Contains(t, fence.Error, "forbidden")
	assert.False(t, fence.Ended.IsZero(), "a failed stage still needs an end time")

	// The stages that succeeded before it must be recorded as successes.
	assert.Equal(t, []string{EventInitialize, EventWaitForLags, EventFence}, stageEvents(report))
	assert.False(t, report.Stages[0].Failed)
	assert.False(t, report.Stages[1].Failed)
}

// TestRunReport_WrittenBeforeAnyStage covers the case that actually happened: a loaded
// migration was killed after 18 minutes in wait_for_lags — its first stage — so no stage
// ever completed. Without an initial write the report would not exist at all, and a
// missing file cannot be told apart from the flag never having been passed. The file has
// to say "this run started and got no further".
func TestRunReport_WrittenBeforeAnyStage(t *testing.T) {
	reportPath := filepath.Join(t.TempDir(), "run-report.json")
	r := NewRunReportRecorder(reportPath, "migration-1", 10, 200, StateInitialized)
	require.NotNil(t, r)

	report := readRunReport(t, reportPath)
	assert.Equal(t, "migration-1", report.MigrationId)
	assert.Equal(t, 10, report.Topics)
	assert.Equal(t, int64(200), report.LagThreshold)
	assert.Equal(t, StateInitialized, report.FinalState)
	assert.Empty(t, report.Stages, "no stage has run yet")
	assert.Empty(t, report.Outcome, "an unfinished run must not claim an outcome")
	assert.False(t, report.StartedAt.IsZero(), "the start time is the useful fact here")
}

// TestRunReport_FlushesIncrementally covers the kill case. Lag checking and
// promotion have no convergence bound of their own, so an external deadline
// killing the process is an expected ending — and the stages that completed
// before the kill must already be on disk. A report with stages but no outcome
// is precisely the signal that this happened.
func TestRunReport_FlushesIncrementally(t *testing.T) {
	reportPath := filepath.Join(t.TempDir(), "run-report.json")
	r := NewRunReportRecorder(reportPath, "migration-1", 2, 0, StateUninitialized)

	r.StageStarted(EventInitialize, StateUninitialized, StateInitialized)
	r.StageEnded(StateInitialized)

	// Simulate the kill: a second stage opens but never closes, and Finish is
	// never reached.
	r.StageStarted(EventWaitForLags, StateInitialized, StateLagsOk)

	report := readRunReport(t, reportPath)
	require.Len(t, report.Stages, 1, "the completed stage should already be on disk")
	assert.Equal(t, EventInitialize, report.Stages[0].Event)
	assert.Equal(t, StateInitialized, report.FinalState)
	assert.Empty(t, report.Outcome, "an unfinished run must not claim an outcome")
}

// TestRunReport_FinishClosesAnOpenStage guards the path where the run ends with
// a stage still open, so its timing is preserved rather than dropped.
func TestRunReport_FinishClosesAnOpenStage(t *testing.T) {
	reportPath := filepath.Join(t.TempDir(), "run-report.json")
	r := NewRunReportRecorder(reportPath, "migration-1", 2, 0, StateUninitialized)

	r.StageStarted(EventWaitForLags, StateInitialized, StateLagsOk)
	r.Finish(StateInitialized, errors.New("context deadline exceeded"))

	report := readRunReport(t, reportPath)
	require.Len(t, report.Stages, 1, "the open stage should be closed, not dropped")
	assert.Equal(t, EventWaitForLags, report.Stages[0].Event)
	assert.True(t, report.Stages[0].Failed)
	assert.Contains(t, report.Stages[0].Error, "deadline")
	assert.Equal(t, RunOutcomeFailed, report.Outcome)
}

// TestRunReport_WrittenAt0600 keeps the report at the same permissions as the
// migration state file it sits beside.
func TestRunReport_WrittenAt0600(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits are not enforced on Windows")
	}

	reportPath := filepath.Join(t.TempDir(), "run-report.json")
	r := NewRunReportRecorder(reportPath, "migration-1", 1, 0, StateUninitialized)
	r.Finish(StateUninitialized, nil)

	info, err := os.Stat(reportPath)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}

// TestRunReport_NoCredentialsOrTopicNames pins the document's blast radius: it is
// meant to be collected and archived, so it must not carry credentials, and it
// records only a topic count rather than the topic names themselves.
func TestRunReport_NoCredentialsOrTopicNames(t *testing.T) {
	orch, config, _ := newHappyPathOrchestrator(t, StateUninitialized, []string{"secret-topic-name"})
	reportPath := filepath.Join(t.TempDir(), "run-report.json")

	recorder := NewRunReportRecorder(reportPath, config.MigrationId, len(config.Topics), 0, config.CurrentState)
	orch.SetRunReportRecorder(recorder)

	require.NoError(t, orch.Execute(context.Background(), 0, "api-key", "api-secret"))
	recorder.Finish(config.CurrentState, nil)

	raw, err := os.ReadFile(reportPath)
	require.NoError(t, err)
	contents := string(raw)

	assert.NotContains(t, contents, "api-key")
	assert.NotContains(t, contents, "api-secret")
	assert.NotContains(t, contents, "secret-topic-name")
	assert.Contains(t, contents, `"topics": 1`, "the topic count is kept")
}
