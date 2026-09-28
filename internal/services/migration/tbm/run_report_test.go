package tbm

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/confluentinc/kcp/internal/services/clusterlink"
	"github.com/confluentinc/kcp/internal/services/migplan"
	"github.com/confluentinc/kcp/internal/services/migration"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The TBM orchestrator shares migration.RunReportRecorder with the AAO one, so
// these tests only prove the TBM Execute loop calls it at the right points; the
// recorder's own behaviour is covered in internal/services/migration.

func readTBMRunReport(t *testing.T, path string) migration.RunReport {
	t.Helper()
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	var report migration.RunReport
	require.NoError(t, json.Unmarshal(raw, &report))
	return report
}

func tbmStageEvents(report migration.RunReport) []string {
	events := make([]string, 0, len(report.Stages))
	for _, s := range report.Stages {
		events = append(events, s.Event)
	}
	return events
}

// attachRecorder wires a recorder onto o the way cmd/migration/execute does,
// returning it (for the test to Finish) and the path it writes to.
func attachRecorder(t *testing.T, o *TBMOrchestrator, config *migration.MigrationConfig) (*migration.RunReportRecorder, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "run-report.json")
	recorder := migration.NewRunReportRecorder(path, config.MigrationId, len(config.Topics), 10, config.CurrentState)
	require.NotNil(t, recorder)
	o.SetRunReportRecorder(recorder)
	return recorder, path
}

// TestTBMRunReport_FullWorkflow — a fresh registration (no topics until
// initialize reconciles them) walks every canonical step; each is recorded in
// order with the edge it traversed, and the topic count is the reconciled one.
func TestTBMRunReport_FullWorkflow(t *testing.T) {
	orchestrator, config, _ := newTestOrchestrator(t, StateUninitialized)
	recorder, path := attachRecorder(t, orchestrator, config)

	err := orchestrator.Execute(context.Background(), realisticReconcileResult(), 10, 0, clusterlink.BasicAuth{})
	require.NoError(t, err)
	recorder.Finish(config.CurrentState, nil)

	// Surface the artifact itself, as the AAO test does: `go test -v -run
	// TestTBMRunReport_FullWorkflow` shows what a dynamic-mode run emits.
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	t.Logf("run report:\n%s", raw)

	report := readTBMRunReport(t, path)
	expected := make([]string, 0, len(canonicalWorkflow))
	for _, step := range canonicalWorkflow {
		expected = append(expected, step.Event)
	}
	assert.Equal(t, expected, tbmStageEvents(report))
	for i, stage := range report.Stages {
		assert.Equal(t, canonicalWorkflow[i].FromState, stage.From, "stage %s from-state", stage.Event)
		assert.Equal(t, canonicalWorkflow[i].ToState, stage.To, "stage %s to-state", stage.Event)
		assert.False(t, stage.Started.IsZero(), "stage %s start", stage.Event)
		assert.False(t, stage.Ended.IsZero(), "stage %s end", stage.Event)
		assert.False(t, stage.Failed, "stage %s failed", stage.Event)
	}
	assert.Empty(t, report.SkippedStages)
	assert.Equal(t, 1, report.Topics, "the count must be the one initialize reconciled, not the zero the recorder started with")
	assert.Equal(t, StateSwitched, report.FinalState)
	assert.Equal(t, migration.RunOutcomeCompleted, report.Outcome)
}

// TestTBMRunReport_ResumeListsSkippedStages — steps the migration had already
// passed are named in SkippedStages, and only the steps that ran are timed.
func TestTBMRunReport_ResumeListsSkippedStages(t *testing.T) {
	orchestrator, config, _ := newTestOrchestrator(t, StateLagsOk)
	recorder, path := attachRecorder(t, orchestrator, config)

	require.NoError(t, orchestrator.Execute(context.Background(), &migplan.Result{}, 10, 0, clusterlink.BasicAuth{}))
	recorder.Finish(config.CurrentState, nil)

	report := readTBMRunReport(t, path)
	assert.Equal(t, []string{EventInitialize, EventWaitForLags}, report.SkippedStages)
	assert.Equal(t, []string{EventFence, EventVerifyFence, EventPromote, EventSwitch}, tbmStageEvents(report))
	assert.Equal(t, migration.RunOutcomeCompleted, report.Outcome)
}

// TestTBMRunReport_FailedStageIsRecorded — a step that fails is recorded as a
// failed stage carrying its error, and the run's outcome is failed.
func TestTBMRunReport_FailedStageIsRecorded(t *testing.T) {
	orchestrator, config, _ := newTestOrchestrator(t, StateUninitialized)
	orchestrator.actions.gatewayService.(*mockGatewayService).waitForGatewayAcceptedFn = func(context.Context, string, string, time.Duration, time.Duration) error {
		return errors.New("gateway never accepted")
	}
	recorder, path := attachRecorder(t, orchestrator, config)

	err := orchestrator.Execute(context.Background(), realisticReconcileResult(), 10, 0, clusterlink.BasicAuth{})
	require.Error(t, err)
	recorder.Finish(config.CurrentState, err)

	report := readTBMRunReport(t, path)
	require.NotEmpty(t, report.Stages)
	last := report.Stages[len(report.Stages)-1]
	assert.Equal(t, EventFence, last.Event)
	assert.True(t, last.Failed)
	assert.Contains(t, last.Error, "gateway never accepted")
	assert.Equal(t, []string{EventInitialize, EventWaitForLags, EventFence}, tbmStageEvents(report))
	assert.Equal(t, migration.RunOutcomeFailed, report.Outcome)
	assert.Equal(t, StateLagsOk, report.FinalState, "a failed fence leaves the FSM at its last good state")
}
