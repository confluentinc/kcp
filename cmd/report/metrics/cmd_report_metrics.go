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
	reportMetricsCmd.Flags().AddFlagSet(requiredFlags)
	groups[requiredFlags] = "Required Flags"

	optionalFlags := pflag.NewFlagSet("optional", pflag.ExitOnError)
	optionalFlags.SortFlags = false
	optionalFlags.StringSliceVar(&clusterArns, "cluster-arn", []string{}, "The AWS cluster ARN(s) to include in the report (comma separated list or repeated flag).  If not provided, all clusters in the state file will be included.")
	optionalFlags.StringVar(&start, "start", "", "inclusive start date for metrics report (YYYY-MM-DD).  (Defaults to 31 days prior to today)")
	optionalFlags.StringVar(&end, "end", "", "exclusive end date for cost report (YYYY-MM-DD).  (Defaults to today).")
	reportMetricsCmd.Flags().AddFlagSet(optionalFlags)
	groups[optionalFlags] = "Optional Flags"

	reportMetricsCmd.SetUsageFunc(func(c *cobra.Command) error {
		fmt.Printf("%s\n\n", c.Short)

		flagOrder := []*pflag.FlagSet{requiredFlags, optionalFlags}
		groupNames := []string{"Required Flags", "Optional Flags"}

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
	// optional but if one is provided, the other must be provided
	reportMetricsCmd.MarkFlagsRequiredTogether("start", "end", "cluster-arn")

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
	if _, err := os.Stat(stateFile); os.IsNotExist(err) {
		return nil, fmt.Errorf("state file does not exist: %s", stateFile)
	}

	state, err := types.NewStateFromFile(stateFile)
	if err != nil {
		return nil, fmt.Errorf("failed to load existing state file: %v", err)
	}

	// start and end date are optional
	var startDate, endDate *time.Time
	if start != "" {
		parsed, err := time.Parse("2006-01-02", start)
		if err != nil {
			return nil, fmt.Errorf("invalid start date format '%s': expected YYYY-MM-DD", start)
		}
		startDate = &parsed
	}

	if end != "" {
		parsed, err := time.Parse("2006-01-02", end)
		if err != nil {
			return nil, fmt.Errorf("invalid end date format '%s': expected YYYY-MM-DD", end)
		}
		endDate = &parsed
	}

	if startDate != nil && endDate != nil {
		if endDate.Before(*startDate) {
			return nil, fmt.Errorf("end date '%s' cannot be before start date '%s'", end, start)
		}
	}

	if startDate == nil && endDate == nil {
		if len(state.Regions) == 0 {
			return nil, fmt.Errorf("no regions found in state file")
		}

		if len(state.Regions[0].Clusters) == 0 {
			return nil, fmt.Errorf("no clusters found in state file")
		}

		// default to the last 31 days.  Ensures a period of 30 full days ending on the previous day, since end date is exclusive in cloudwatch API.
		now := time.Now()
		start := now.AddDate(0, 0, -31)
		startDate = &start
		endDate = &now
	}

	if len(clusterArns) == 0 {
		// retrieve all cluster ARNs from state file
		for _, region := range state.Regions {
			for _, cluster := range region.Clusters {
				clusterArns = append(clusterArns, cluster.Arn)
			}
		}
		if len(clusterArns) == 0 {
			return nil, fmt.Errorf("no clusters found in state file")
		}
	}

	opts := MetricReporterOpts{
		ClusterArns: clusterArns,
		State:       state,
		StartDate:   startDate,
		EndDate:     endDate,
	}

	return &opts, nil
}
