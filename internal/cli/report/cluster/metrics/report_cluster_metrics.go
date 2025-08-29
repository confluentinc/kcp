package metrics

import (
	"fmt"
	"strings"
	"time"

	"github.com/confluentinc/kcp/internal/client"
	rrm "github.com/confluentinc/kcp/internal/generators/report/cluster/metrics"
	"github.com/confluentinc/kcp/internal/services/metrics"
	"github.com/confluentinc/kcp/internal/services/msk"
	"github.com/confluentinc/kcp/internal/utils"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

var (
	clusterArn     string
	start          string
	end            string
	lastDay        bool
	lastWeek       bool
	lastThirtyDays bool
)

func NewReportClusterMetricsCmd() *cobra.Command {
	clusterCmd := &cobra.Command{
		Use:           "metrics",
		Short:         "Generate metrics report on an msk cluster",
		Long:          "Generate a metrics report on an msk cluster.",
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		PreRunE:       preRunReportClusterMetrics,
		RunE:          runReportClusterMetrics,
	}

	groups := map[*pflag.FlagSet]string{}
	// Required flags.
	requiredFlags := pflag.NewFlagSet("required", pflag.ExitOnError)
	requiredFlags.SortFlags = false
	requiredFlags.StringVar(&clusterArn, "cluster-arn", "", "cluster arn")
	// requiredFlags.StringVar(&start, "start", "", "inclusive start date for metrics report (YYYY-MM-DD format)")
	// requiredFlags.StringVar(&end, "end", "", "exclusive end date for metrics report (YYYY-MM-DD format)")
	clusterCmd.Flags().AddFlagSet(requiredFlags)
	groups[requiredFlags] = "Required Flags"

	// Time range flags.
	timeRangeFlags := pflag.NewFlagSet("time-range", pflag.ExitOnError)
	timeRangeFlags.SortFlags = false
	timeRangeFlags.StringVar(&start, "start", "", "inclusive start date for cost report (YYYY-MM-DD)")
	timeRangeFlags.StringVar(&end, "end", "", "exclusive end date for cost report (YYYY-MM-DD)")
	timeRangeFlags.BoolVar(&lastDay, "last-day", false, "generate cost report for the previous day")
	timeRangeFlags.BoolVar(&lastWeek, "last-week", false, "generate cost report for the previous 7 days (not including today)")
	timeRangeFlags.BoolVar(&lastThirtyDays, "last-thirty-days", false, "generate cost report for the previous 30 days (not including today)")
	clusterCmd.Flags().AddFlagSet(timeRangeFlags)
	groups[timeRangeFlags] = "Time Range Flags"

	clusterCmd.SetUsageFunc(func(c *cobra.Command) error {
		fmt.Printf("%s\n\n", c.Short)
		flagOrder := []*pflag.FlagSet{requiredFlags, timeRangeFlags}
		groupNames := []string{"Required Flags", "Time Range Flags"}
		for i, fs := range flagOrder {
			usage := fs.FlagUsages()
			if usage != "" {
				fmt.Printf("%s:\n%s\n", groupNames[i], usage)
			}
		}
		fmt.Println("All flags can be provided via environment variables (uppercase, with underscores).")
		return nil
	})
	clusterCmd.MarkFlagRequired("cluster-arn")

	clusterCmd.MarkFlagsMutuallyExclusive("start", "last-day", "last-week", "last-thirty-days")
	clusterCmd.MarkFlagsOneRequired("start", "last-day", "last-week", "last-thirty-days")
	clusterCmd.MarkFlagsRequiredTogether("start", "end")

	return clusterCmd
}

// sets flag values from corresponding environment variables if flags weren't explicitly provided
func preRunReportClusterMetrics(cmd *cobra.Command, args []string) error {
	if err := utils.BindEnvToFlags(cmd); err != nil {
		return err
	}

	return nil
}

func runReportClusterMetrics(cmd *cobra.Command, args []string) error {
	opts, err := parseReportRegionMetricsOpts()
	if err != nil {
		return fmt.Errorf("failed to parse cluster report opts: %v", err)
	}

	mskClient, err := client.NewMSKClient(opts.Region)
	if err != nil {
		return fmt.Errorf("failed to create msk client: %v", err)
	}

	mskService := msk.NewMSKService(mskClient)

	cloudWatchClient, err := client.NewCloudWatchClient(opts.Region)
	if err != nil {
		return fmt.Errorf("failed to create cloudwatch client: %v", err)
	}

	metricService := metrics.NewMetricService(cloudWatchClient, opts.StartDate, opts.EndDate)

	regionMetrics := rrm.NewClusterMetrics(mskService, metricService, *opts)
	if err := regionMetrics.Run(); err != nil {
		return fmt.Errorf("failed to report region metrics: %v", err)
	}

	return nil
}

func parseReportRegionMetricsOpts() (*rrm.ClusterMetricsOpts, error) {

	arnParts := strings.Split(clusterArn, ":")
	if len(arnParts) < 4 {
		return nil, fmt.Errorf("invalid cluster ARN format: %s", clusterArn)
	}
	region := arnParts[3]
	if region == "" {
		return nil, fmt.Errorf("region not found in cluster ARN: %s", clusterArn)
	}

	const dateFormat = "2006-01-02"
	var startDate, endDate time.Time
	var err error

	switch {
	case start != "" && end != "":
		startDate, err = time.Parse(dateFormat, start)
		if err != nil {
			return nil, fmt.Errorf("invalid start date format '%s': expected YYYY-MM-DD", start)
		}

		endDate, err = time.Parse(dateFormat, end)
		if err != nil {
			return nil, fmt.Errorf("invalid end date format '%s': expected YYYY-MM-DD", end)
		}

		if startDate.After(endDate) {
			return nil, fmt.Errorf("start date '%s' cannot be after end date '%s'", start, end)
		}

	case lastDay:
		now := time.Now()
		startDate = now.AddDate(0, 0, -1).UTC().Truncate(24 * time.Hour)
		endDate = now.UTC().Truncate(24 * time.Hour)

	case lastWeek:
		now := time.Now()
		startDate = now.AddDate(0, 0, -8).UTC().Truncate(24 * time.Hour)
		endDate = now.UTC().Truncate(24 * time.Hour)

	case lastThirtyDays:
		now := time.Now()
		startDate = now.AddDate(0, 0, -31).UTC().Truncate(24 * time.Hour)
		endDate = now.UTC().Truncate(24 * time.Hour)
	}

	opts := rrm.ClusterMetricsOpts{
		Region:     region,
		StartDate:  startDate,
		EndDate:    endDate,
		ClusterArn: clusterArn,
	}

	return &opts, nil
}
