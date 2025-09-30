package costs

import (
	"time"

	"github.com/confluentinc/kcp/internal/types"
)

type ReportService interface {
	ProcessState(state types.State) types.ProcessedState
}

type CostReporterOpts struct {
	Regions   []string
	State     *types.State
	StartDate time.Time
	EndDate   time.Time
	CostType  string
}

type CostReporter struct {
	reportService ReportService

	regions   []string
	state     *types.State
	startDate time.Time
	endDate   time.Time
	costType  string
}

func NewCostReporter(reportService ReportService, opts CostReporterOpts) *CostReporter {
	return &CostReporter{
		reportService: reportService,

		regions:   opts.Regions,
		state:     opts.State,
		startDate: opts.StartDate,
		endDate:   opts.EndDate,
		costType:  opts.CostType,
	}
}

func (r *CostReporter) Run() error {
	processedState := r.reportService.ProcessState(*r.state)

	// do stuff with processedState

	_ = processedState
	return nil
}
