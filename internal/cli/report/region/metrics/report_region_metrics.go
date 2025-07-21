package region

import (
	"fmt"
	"time"

	"github.com/confluentinc/kcp-internal/internal/client"
	rrm "github.com/confluentinc/kcp-internal/internal/generators/report/region/metrics"
	"github.com/confluentinc/kcp-internal/internal/services/metrics"
	"github.com/confluentinc/kcp-internal/internal/services/msk"
	"github.com/confluentinc/kcp-internal/internal/utils"

	"github.com/spf13/cobra"
)

var (
	region string
	start  string
	end    string
)

func NewReportRegionMetricsCmd() *cobra.Command {
	regionCmd := &cobra.Command{
		Use:   "metrics",
		Short: "Generate metrics report on an AWS region",
		Long: `Generate a metrics report on an AWS region.

All flags can be provided via environment variables (uppercase, with underscores):

FLAG                     | ENV_VAR
-------------------------|---------------------------
--region                 | REGION=us-east-1
--start                  | START=2024-01-01
--end                    | END=2024-01-02
`,
		SilenceErrors: true,
		PreRunE:       preRunReportRegionMetrics,
		RunE:          runReportRegionMetrics,
	}

	regionCmd.Flags().StringVar(&region, "region", "", "The AWS region")
	regionCmd.Flags().StringVar(&start, "start", "", "inclusive start date for metrics report (YYYY-MM-DD format)")
	regionCmd.Flags().StringVar(&end, "end", "", "exclusive end date for metrics report (YYYY-MM-DD format)")

	regionCmd.MarkFlagRequired("region")
	regionCmd.MarkFlagRequired("start")
	regionCmd.MarkFlagRequired("end")

	return regionCmd
}

// sets flag values from corresponding environment variables if flags weren't explicitly provided
func preRunReportRegionMetrics(cmd *cobra.Command, args []string) error {
	if err := utils.BindEnvToFlags(cmd); err != nil {
		return err
	}

	return nil
}

func runReportRegionMetrics(cmd *cobra.Command, args []string) error {
	opts, err := parseReportRegionMetricsOpts()
	if err != nil {
		return fmt.Errorf("failed to parse region report opts: %v", err)
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

	regionMetrics := rrm.NewRegionMetrics(mskService, metricService, *opts)
	if err := regionMetrics.Run(); err != nil {
		return fmt.Errorf("failed to report region metrics: %v", err)
	}

	return nil
}

func parseReportRegionMetricsOpts() (*rrm.RegionMetricsOpts, error) {
	const dateFormat = "2006-01-02"

	startDate, err := time.Parse(dateFormat, start)
	if err != nil {
		return nil, fmt.Errorf("invalid start date format '%s': expected YYYY-MM-DD", start)
	}

	endDate, err := time.Parse(dateFormat, end)
	if err != nil {
		return nil, fmt.Errorf("invalid end date format '%s': expected YYYY-MM-DD", end)
	}

	if startDate.After(endDate) {
		return nil, fmt.Errorf("start date '%s' cannot be after end date '%s'", start, end)
	}

	opts := rrm.RegionMetricsOpts{
		Region:    region,
		StartDate: startDate,
		EndDate:   endDate,
	}

	return &opts, nil
}
