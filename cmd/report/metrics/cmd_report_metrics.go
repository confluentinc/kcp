package metrics

import (
	"fmt"
	"os"
	"time"

	"github.com/confluentinc/kcp/internal/services/report"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/confluentinc/kcp/internal/utils"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

var (
	stateFile   string
	start       string
	end         string
	clusterArns []string
)

func NewReportMetricsCmd() *cobra.Command {
	reportMetricsCmd := &cobra.Command{
		Use:           "metrics",
		Short:         "Generate a report of metrics for given cluster(s)",
		Long:          "Generate a report of metrics for the given cluster(s) based on the data collected by `kcp discover`",
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		PreRunE:       preRunReportMetrics,
		RunE:          runReportMetrics,
	}

	groups := map[*pflag.FlagSet]string{}

	requiredFlags := pflag.NewFlagSet("required", pflag.ExitOnError)
	requiredFlags.SortFlags = false
	requiredFlags.StringVar(&stateFile, "state-file", "", "The path to the kcp state file where the MSK cluster discovery reports have been written to.")
	requiredFlags.StringVar(&start, "start", "", "inclusive start date for cost report (YYYY-MM-DD)")
	requiredFlags.StringVar(&end, "end", "", "exclusive end date for cost report (YYYY-MM-DD)")
	requiredFlags.StringSliceVar(&clusterArns, "cluster-arn", []string{}, "The AWS cluster ARN(s) to include in the report (comma separated list or repeated flag)")

	reportMetricsCmd.Flags().AddFlagSet(requiredFlags)
	groups[requiredFlags] = "Required Flags"

	reportMetricsCmd.SetUsageFunc(func(c *cobra.Command) error {
		fmt.Printf("%s\n\n", c.Short)

		flagOrder := []*pflag.FlagSet{requiredFlags}
		groupNames := []string{"Required Flags"}

		for i, fs := range flagOrder {
			usage := fs.FlagUsages()
			if usage != "" {
				fmt.Printf("%s:\n%s\n", groupNames[i], usage)
			}
		}

		fmt.Println("All flags can be provided via environment variables (uppercase, with underscores).")

		return nil
	})

	reportMetricsCmd.MarkFlagRequired("state-file")
	reportMetricsCmd.MarkFlagRequired("start")
	reportMetricsCmd.MarkFlagRequired("end")
	reportMetricsCmd.MarkFlagRequired("cluster-arn")

	return reportMetricsCmd
}

func preRunReportMetrics(cmd *cobra.Command, args []string) error {
	if err := utils.BindEnvToFlags(cmd); err != nil {
		return err
	}
	return nil
}

func runReportMetrics(cmd *cobra.Command, args []string) error {
	opts, err := parseMetricReporterOpts()
	if err != nil {
		return fmt.Errorf("❌ failed to parse report opts: %v", err)
	}

	reportService := report.NewReportService()

	metricReporter := NewMetricReporter(reportService, *opts)
	if err := metricReporter.Run(); err != nil {
		return fmt.Errorf("❌ failed to report metrics: %v", err)
	}
	return nil
}

func parseMetricReporterOpts() (*MetricReporterOpts, error) {
	startDate, err := time.Parse("2006-01-02", start)
	if err != nil {
		return nil, fmt.Errorf("invalid start date format '%s': expected YYYY-MM-DD", start)
	}

	endDate, err := time.Parse("2006-01-02", end)
	if err != nil {
		return nil, fmt.Errorf("invalid end date format '%s': expected YYYY-MM-DD", end)
	}

	if endDate.Before(startDate) {
		return nil, fmt.Errorf("end date '%s' cannot be before start date '%s'", end, start)
	}

	if _, err := os.Stat(stateFile); os.IsNotExist(err) {
		return nil, fmt.Errorf("state file does not exist: %s", stateFile)
	}

	state, err := types.NewStateFromFile(stateFile)
	if err != nil {
		return nil, fmt.Errorf("failed to load existing state file: %v", err)
	}

	opts := MetricReporterOpts{
		ClusterArns: clusterArns,
		State:       state,
		StartDate:   startDate,
		EndDate:     endDate,
	}

	return &opts, nil
}
