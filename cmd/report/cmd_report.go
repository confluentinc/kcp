package report

import (
	"github.com/confluentinc/kcp/cmd/report/costs"
	"github.com/confluentinc/kcp/cmd/report/metrics"
	"github.com/spf13/cobra"
)

func NewReportCmd() *cobra.Command {
	reportCmd := &cobra.Command{
		Use:           "report",
		Short:         "Generate a report of costs for given region(s)",
		Long:          "Generate a report of costs for the given region(s) based on the data collected by `kcp discover`",
		SilenceErrors: true,
		Args:          cobra.NoArgs,
	}

	reportCmd.AddCommand(costs.NewReportCostsCmd())
	reportCmd.AddCommand(metrics.NewReportMetricsCmd())

	return reportCmd
}

// func preRunReport(cmd *cobra.Command, args []string) error {
// 	if err := utils.BindEnvToFlags(cmd); err != nil {
// 		return err
// 	}
// 	return nil
// }

// func runReport(cmd *cobra.Command, args []string) error {
// 	opts, err := parseReportOpts()
// 	if err != nil {
// 		return fmt.Errorf("failed to parse report opts: %v", err)
// 	}

// 	reportService := rservice.NewReportService()

// 	markdownService := markdown.New()

// 	reporter := NewReporter(reportService, *markdownService, *opts)
// 	if err := reporter.Run(); err != nil {
// 		return fmt.Errorf("❌ failed to scan clusters: %v", err)
// 	}
// 	return nil
// }

// func parseReportOpts() (*ReporterOpts, error) {
// 	if _, err := os.Stat(stateFile); os.IsNotExist(err) {
// 		return nil, fmt.Errorf("❌ state file does not exist: %s", stateFile)
// 	}

// 	file, err := os.ReadFile(stateFile)
// 	if err != nil {
// 		return nil, fmt.Errorf("failed to read state file: %v", err)
// 	}

// 	var state types.State
// 	if err := json.Unmarshal(file, &state); err != nil {
// 		return nil, fmt.Errorf("failed to unmarshal state: %v", err)
// 	}

// 	opts := ReporterOpts{
// 		State: state,
// 	}

// 	return &opts, nil
// }
