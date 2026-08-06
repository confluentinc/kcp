package migration

import (
	"encoding/json"
	"log/slog"
	"time"

	"github.com/confluentinc/kcp/internal/build_info"
)

// Run outcomes. A run that never reaches the end of the workflow loop is
// "failed"; there is deliberately no "partial" value, because a report found on
// disk with neither outcome set is itself the signal that kcp was killed
// mid-run (see RunReportRecorder's incremental flush).
const (
	RunOutcomeCompleted = "completed"
	RunOutcomeFailed    = "failed"
)

// RunReportStage is one committed (or attempted) workflow transition, timed.
//
// Only forward steps that actually ran appear here — steps skipped because the
// migration was resumed past them are listed by name in RunReport.SkippedStages
// instead, so every entry in Stages carries real timings and a consumer never
// has to test for a zero timestamp.
type RunReportStage struct {
	Event      string    `json:"event"`
	From       string    `json:"from"`
	To         string    `json:"to"`
	Started    time.Time `json:"started"`
	Ended      time.Time `json:"ended"`
	DurationMs int64     `json:"duration_ms"`
	Failed     bool      `json:"failed,omitempty"`
	Error      string    `json:"error,omitempty"`
}

// RunReport is the machine-readable record of one `kcp migration execute` run:
// what it did, in what order, and how long each stage took.
//
// It is deliberately self-contained and free of anything about the environment
// the run happened in — no run identifiers, no infrastructure timings, no
// experiment parameters. Callers that need that context (the migration
// performance rig, for one) wrap this document rather than adding fields to it,
// so it stays meaningful on its own for anyone timing a rehearsal of their own
// migration.
//
// It carries no credentials and no topic names — only the topic count.
type RunReport struct {
	MigrationId   string           `json:"migration_id"`
	KcpVersion    string           `json:"kcp_version"`
	KcpCommit     string           `json:"kcp_commit,omitempty"`
	Topics        int              `json:"topics"`
	LagThreshold  int64            `json:"lag_threshold"`
	StartedAt     time.Time        `json:"started_at"`
	EndedAt       time.Time        `json:"ended_at"`
	DurationMs    int64            `json:"duration_ms"`
	Stages        []RunReportStage `json:"stages"`
	SkippedStages []string         `json:"skipped_stages,omitempty"`
	FinalState    string           `json:"final_state"`
	Outcome       string           `json:"outcome,omitempty"`
	Error         string           `json:"error,omitempty"`
}

// RunReportRecorder accumulates a RunReport and flushes it to disk.
//
// Every method is safe on a nil receiver, so the orchestrator holds one
// unconditionally and the no-report path costs a nil check rather than a
// conditional at each call site.
//
// The report is rewritten after every stage rather than once at the end. Two
// poll loops inside execute have no convergence bound of their own (lag checking
// and promotion), so a caller that imposes an external deadline and kills the
// process is an expected way for a run to end — and an incremental flush leaves
// the stages that did complete on disk, where a single write at exit would leave
// nothing. Each flush is a whole-file atomic replace, so a kill during one
// cannot corrupt the previous version.
type RunReportRecorder struct {
	path    string
	report  RunReport
	current *RunReportStage
}

// NewRunReportRecorder returns a recorder writing to path, or nil if path is
// empty — which is what makes every method's nil-receiver tolerance load-bearing
// rather than defensive.
func NewRunReportRecorder(path, migrationId string, topics int, lagThreshold int64, initialState string) *RunReportRecorder {
	if path == "" {
		return nil
	}
	r := &RunReportRecorder{
		path: path,
		report: RunReport{
			MigrationId:  migrationId,
			KcpVersion:   build_info.Version,
			KcpCommit:    build_info.Commit,
			Topics:       topics,
			LagThreshold: lagThreshold,
			StartedAt:    time.Now(),
			Stages:       []RunReportStage{},
			FinalState:   initialState,
		},
	}
	// Written immediately, before any stage runs. The first stage of a loaded migration is
	// lag checking, which has no convergence bound — so a caller that imposes a deadline
	// and kills the process may do so before ANY stage completes. Without this the file
	// would never exist and the report would be indistinguishable from "the flag was never
	// passed", when in fact it says something specific: the run died waiting on its first
	// stage. Observed on a real run that was killed after 18 minutes in wait_for_lags.
	r.flush()
	return r
}

// StageStarted opens a stage. The from/to states come from the workflow edge
// definition rather than the live FSM, so a stage records the transition it
// attempted even when that transition is the one that failed.
func (r *RunReportRecorder) StageStarted(event, from, to string) {
	if r == nil {
		return
	}
	r.current = &RunReportStage{
		Event:   event,
		From:    from,
		To:      to,
		Started: time.Now(),
	}
}

// StageEnded closes the open stage as successful and flushes.
func (r *RunReportRecorder) StageEnded(finalState string) {
	if r == nil {
		return
	}
	r.closeStage(nil)
	r.report.FinalState = finalState
	r.flush()
}

// StageFailed closes the open stage as failed and flushes. The run's own
// outcome is not set here: a failed stage may still be followed by a
// compensating rollback, and Finish owns the verdict.
func (r *RunReportRecorder) StageFailed(err error) {
	if r == nil {
		return
	}
	r.closeStage(err)
	r.flush()
}

// StageSkipped records a workflow step the run passed over because the
// migration had already advanced beyond it.
func (r *RunReportRecorder) StageSkipped(event string) {
	if r == nil {
		return
	}
	r.report.SkippedStages = append(r.report.SkippedStages, event)
}

// Finish stamps the run's verdict and writes the report a final time. It is
// intended to be deferred, so it runs on the failure path as well — a run that
// did not converge is a result worth recording, not an absence of one.
func (r *RunReportRecorder) Finish(finalState string, runErr error) {
	if r == nil {
		return
	}
	// A stage still open here means the run ended without either closing path
	// having run — a panic, or an early return that bypassed them. Close it so
	// the timings stay consistent rather than dropping the stage entirely.
	r.closeStage(runErr)

	r.report.FinalState = finalState
	r.report.EndedAt = time.Now()
	r.report.DurationMs = r.report.EndedAt.Sub(r.report.StartedAt).Milliseconds()
	if runErr != nil {
		r.report.Outcome = RunOutcomeFailed
		r.report.Error = runErr.Error()
	} else {
		r.report.Outcome = RunOutcomeCompleted
	}
	r.flush()
}

func (r *RunReportRecorder) closeStage(err error) {
	if r.current == nil {
		return
	}
	stage := *r.current
	r.current = nil

	stage.Ended = time.Now()
	stage.DurationMs = stage.Ended.Sub(stage.Started).Milliseconds()
	if err != nil {
		stage.Failed = true
		stage.Error = err.Error()
	}
	r.report.Stages = append(r.report.Stages, stage)
}

// flush replaces the report on disk. A write failure is logged and swallowed:
// the report is an observability artifact, and losing it must never fail a
// migration that is otherwise succeeding.
func (r *RunReportRecorder) flush() {
	data, err := json.MarshalIndent(r.report, "", "  ")
	if err != nil {
		slog.Warn("⚠️ failed to marshal migration run report", "error", err)
		return
	}
	if err := writeFileAtomic(r.path, append(data, '\n'), 0600); err != nil {
		slog.Warn("⚠️ failed to write migration run report", "path", r.path, "error", err)
		return
	}
	slog.Debug("wrote migration run report", "path", r.path, "stages", len(r.report.Stages))
}
