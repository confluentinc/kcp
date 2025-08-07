package costs

import (
	"fmt"
	"strings"
	"time"

	costexplorertypes "github.com/aws/aws-sdk-go-v2/service/costexplorer/types"
	"github.com/confluentinc/kcp/internal/client"
	rrc "github.com/confluentinc/kcp/internal/generators/report/region/costs"
	"github.com/confluentinc/kcp/internal/services/cost"
	"github.com/confluentinc/kcp/internal/utils"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

var (
	region string

	start          string
	end            string
	lastDay        bool
	lastWeek       bool
	lastThirtyDays bool

	hourly  bool
	daily   bool
	monthly bool

	tag []string
)

func NewReportRegionCostsCmd() *cobra.Command {
	regionCmd := &cobra.Command{
		Use:           "costs",
		Short:         "Generate costs report on an AWS region",
		Long:          "Generate a costs report on an AWS region.",
		SilenceErrors: true,
		PreRunE:       preRunReportRegionCosts,
		RunE:          runReportRegionCosts,
	}

	groups := map[*pflag.FlagSet]string{}

	// Required flags.
	requiredFlags := pflag.NewFlagSet("required", pflag.ExitOnError)
	requiredFlags.SortFlags = false
	requiredFlags.StringVar(&region, "region", "", "AWS region the cost report is generated for")
	regionCmd.Flags().AddFlagSet(requiredFlags)
	groups[requiredFlags] = "Required Flags"

	// Time range flags.
	timeRangeFlags := pflag.NewFlagSet("time-range", pflag.ExitOnError)
	timeRangeFlags.SortFlags = false
	timeRangeFlags.StringVar(&start, "start", "", "inclusive start date for cost report (YYYY-MM-DD)")
	timeRangeFlags.StringVar(&end, "end", "", "exclusive end date for cost report (YYYY-MM-DD)")
	timeRangeFlags.BoolVar(&lastDay, "last-day", false, "generate cost report for the previous day")
	timeRangeFlags.BoolVar(&lastWeek, "last-week", false, "generate cost report for the previous 7 days (not including today)")
	timeRangeFlags.BoolVar(&lastThirtyDays, "last-thirty-days", false, "generate cost report for the previous 30 days (not including today)")
	regionCmd.Flags().AddFlagSet(timeRangeFlags)
	groups[timeRangeFlags] = "Time Range Flags"

	// Granularity flags.
	granularityFlags := pflag.NewFlagSet("granularity", pflag.ExitOnError)
	granularityFlags.SortFlags = false
	granularityFlags.BoolVar(&hourly, "hourly", false, "generate hourly cost report")
	granularityFlags.BoolVar(&daily, "daily", false, "generate daily cost report")
	granularityFlags.BoolVar(&monthly, "monthly", false, "generate monthly cost report")
	regionCmd.Flags().AddFlagSet(granularityFlags)
	groups[granularityFlags] = "Granularity Flags (choose one)"

	// Optional flags.
	optionalFlags := pflag.NewFlagSet("optional", pflag.ExitOnError)
	optionalFlags.SortFlags = false
	optionalFlags.StringSliceVar(&tag, "tag", []string{}, "generate cost report for a specific tag(key=value)")
	regionCmd.Flags().AddFlagSet(optionalFlags)
	groups[optionalFlags] = "Optional Flags"

	regionCmd.SetUsageFunc(func(c *cobra.Command) error {
		fmt.Printf("%s\n\n", c.Short)

		flagOrder := []*pflag.FlagSet{requiredFlags, timeRangeFlags, granularityFlags, optionalFlags}
		groupNames := []string{"Required Flags", "Time Range Flags", "Granularity Flags (choose one)", "Optional Flags"}

		for i, fs := range flagOrder {
			usage := fs.FlagUsages()
			if usage != "" {
				fmt.Printf("%s:\n%s\n", groupNames[i], usage)
			}
		}

		fmt.Println("All flags can be provided via environment variables (uppercase, with underscores).")

		return nil
	})

	regionCmd.MarkFlagRequired("region")

	regionCmd.MarkFlagsMutuallyExclusive("start", "last-day", "last-week", "last-thirty-days")
	regionCmd.MarkFlagsOneRequired("start", "last-day", "last-week", "last-thirty-days")
	regionCmd.MarkFlagsRequiredTogether("start", "end")

	regionCmd.MarkFlagsMutuallyExclusive("hourly", "daily", "monthly")
	regionCmd.MarkFlagsOneRequired("hourly", "daily", "monthly")

	return regionCmd
}

// sets flag values from corresponding environment variables if flags weren't explicitly provided
func preRunReportRegionCosts(cmd *cobra.Command, args []string) error {
	if err := utils.BindEnvToFlags(cmd); err != nil {
		return err
	}

	return nil
}

func runReportRegionCosts(cmd *cobra.Command, args []string) error {
	opts, err := parseReportRegionCostsOpts()
	if err != nil {
		return fmt.Errorf("failed to parse region report opts: %v", err)
	}

	costExplorerClient, err := client.NewCostExplorerClient(opts.Region)
	if err != nil {
		return fmt.Errorf("failed to create cost explorer client: %v", err)
	}

	costService := cost.NewCostService(costExplorerClient)

	regionCoster := rrc.NewRegionCoster(costService, *opts)
	if err := regionCoster.Run(); err != nil {
		return fmt.Errorf("failed to report region costs: %v", err)
	}

	return nil
}

func parseReportRegionCostsOpts() (*rrc.RegionCosterOpts, error) {
	for _, t := range tag {
		if !strings.Contains(t, "=") {
			return nil, fmt.Errorf("invalid tag format '%s': expected key=value format", t)
		}
		splitTag := strings.Split(t, "=")
		if splitTag[0] == "" || splitTag[1] == "" {
			return nil, fmt.Errorf("invalid tag format '%s': expected key=value format", t)
		}
	}

	var providedGranularity costexplorertypes.Granularity
	switch {
	case daily:
		providedGranularity = costexplorertypes.GranularityDaily
	case hourly:
		providedGranularity = costexplorertypes.GranularityHourly
	case monthly:
		providedGranularity = costexplorertypes.GranularityMonthly
	default:
		return nil, fmt.Errorf("no granularity flag provided")
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
		startDate = now.AddDate(0, 0, -30).UTC().Truncate(24 * time.Hour)
		endDate = now.UTC().Truncate(24 * time.Hour)
	}

	opts := rrc.RegionCosterOpts{
		Region:      region,
		StartDate:   startDate,
		EndDate:     endDate,
		Granularity: providedGranularity,
		Tag:         tag,
	}

	return &opts, nil
}
