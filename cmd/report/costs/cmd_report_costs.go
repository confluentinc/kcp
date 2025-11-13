package costs

import (
	"fmt"
	"os"
	"time"

	"github.com/confluentinc/kcp/internal/services/markdown"
	"github.com/confluentinc/kcp/internal/services/report"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/confluentinc/kcp/internal/utils"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

var (
	stateFile string
	start     string
	end       string
	regions   []string
)

func NewReportCostsCmd() *cobra.Command {
	reportCostsCmd := &cobra.Command{
		Use:           "costs",
		Short:         "Generate a report of costs for given region(s)",
		Long:          "Generate a report of costs for the given region(s) based on the data collected by `kcp discover`",
		SilenceErrors: true,
		PreRunE:       preRunReportCosts,
		RunE:          runReportCosts,
	}

	groups := map[*pflag.FlagSet]string{}

	requiredFlags := pflag.NewFlagSet("required", pflag.ExitOnError)
	requiredFlags.SortFlags = false
	requiredFlags.StringVar(&stateFile, "state-file", "", "The path to the kcp state file where the MSK cluster discovery reports have been written to.")
	reportCostsCmd.Flags().AddFlagSet(requiredFlags)
	groups[requiredFlags] = "Required Flags"

	optionalFlags := pflag.NewFlagSet("optional", pflag.ExitOnError)
	optionalFlags.SortFlags = false
	optionalFlags.StringSliceVar(&regions, "region", []string{}, "The AWS region(s) to include in the report (comma separated list or repeated flag).  If not provided, all regions in the state file will be included.")
	optionalFlags.StringVar(&start, "start", "", "inclusive start date for cost report (YYYY-MM-DD).  (Defaults to 31 days prior to today)")
	optionalFlags.StringVar(&end, "end", "", "exclusive end date for cost report (YYYY-MM-DD).  (Defaults to today).")
	reportCostsCmd.Flags().AddFlagSet(optionalFlags)
	groups[optionalFlags] = "Optional Flags"

	reportCostsCmd.SetUsageFunc(func(c *cobra.Command) error {
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

	reportCostsCmd.MarkFlagRequired("state-file")
	// optional but if one is provided, the others must be provided
	reportCostsCmd.MarkFlagsRequiredTogether("start", "end", "region")

	return reportCostsCmd
}

func preRunReportCosts(cmd *cobra.Command, args []string) error {
	if err := utils.BindEnvToFlags(cmd); err != nil {
		return err
	}
	return nil
}

func runReportCosts(cmd *cobra.Command, args []string) error {
	opts, err := parseCostReporterOpts()
	if err != nil {
		return fmt.Errorf("❌ failed to parse report opts: %v", err)
	}

	reportService := report.NewReportService()
	markdownService := markdown.New()

	costReporter := NewCostReporter(reportService, *markdownService, *opts)
	if err := costReporter.Run(); err != nil {
		return fmt.Errorf("❌ failed to report costs: %v", err)
	}
	return nil
}

func parseCostReporterOpts() (*CostReporterOpts, error) {
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
		// default to the last 31 days.  Ensures a period of 30 full days ending on the previous day, since end date is exclusive in cloudwatch API.
		now := time.Now()
		start := now.AddDate(0, 0, -31)
		startDate = &start
		endDate = &now
	}

	if len(regions) == 0 {
		// retrieve all regions from state file
		for _, region := range state.Regions {
			regions = append(regions, region.Name)
		}
		if len(regions) == 0 {
			return nil, fmt.Errorf("no regions found in state file")
		}
	}

	opts := CostReporterOpts{
		Regions:   regions,
		State:     state,
		StartDate: startDate,
		EndDate:   endDate,
	}

	return &opts, nil
}
