package metrics

import (
	"log/slog"
	"time"

	"github.com/confluentinc/kcp/internal/types"
)

type ReportService interface {
	ProcessState(state types.State) types.ProcessedState
}

type MetricReporterOpts struct {
	ClusterArns []string
	State       *types.State
	StartDate   time.Time
	EndDate     time.Time
}

type MetricReporter struct {
	reportService ReportService

	clusterArns []string
	state       *types.State
	startDate   time.Time
	endDate     time.Time
}

func NewMetricReporter(reportService ReportService, opts MetricReporterOpts) *MetricReporter {
	return &MetricReporter{
		reportService: reportService,

		clusterArns: opts.ClusterArns,
		state:       opts.State,
		startDate:   opts.StartDate,
		endDate:     opts.EndDate,
	}
}

func (r *MetricReporter) Run() error {
	processedState := r.reportService.ProcessState(*r.state)

	_ = processedState

	slog.Info("🔍 running metric reporter", "clusterArns", r.clusterArns, "startDate", r.startDate, "endDate", r.endDate)
	return nil
}
