package costs

import (
	"fmt"
	"log/slog"
	"strings"
	"time"

	costexplorertypes "github.com/aws/aws-sdk-go-v2/service/costexplorer/types"
	"github.com/confluentinc/kcp-internal/internal/client"
	rrc "github.com/confluentinc/kcp-internal/internal/generators/report/region/costs"
	"github.com/confluentinc/kcp-internal/internal/services/cost"
	"github.com/confluentinc/kcp-internal/internal/utils"

	"github.com/spf13/cobra"
)

func NewReportRegionCostsCmd() *cobra.Command {
	regionCmd := &cobra.Command{
		Use:   "costs",
		Short: "Generate costs report on an AWS region",
		Long: `Generate a costs report on an AWS region.
Specify exactly one granularity flag: --hourly, --daily, or --monthly.

All flags can be provided via environment variables (uppercase, with underscores):

FLAG                     | ENV_VAR
-------------------------|---------------------------
--region                 | REGION=us-east-1
--start                  | START=2024-01-01
--end                    | END=2024-01-02
--hourly                 | HOURLY=true
--daily                  | DAILY=true
--monthly                | MONTHLY=true
--tag                    | TAG=key=value
`,
		SilenceErrors: true,
		PreRunE:       preRunReportRegionCosts,
		RunE:          runReportRegionCosts,
	}

	regionCmd.Flags().String("region", "", "The AWS region")
	regionCmd.Flags().String("start", "", "inclusive start date for cost report (YYYY-MM-DD format)")
	regionCmd.Flags().String("end", "", "exclusive end date for cost report (YYYY-MM-DD format)")
	regionCmd.Flags().Bool("hourly", false, "generate hourly cost report")
	regionCmd.Flags().Bool("daily", false, "generate daily cost report")
	regionCmd.Flags().Bool("monthly", false, "generate monthly cost report")
	regionCmd.Flags().StringSlice("tag", []string{}, "generate cost report for a specific tag(key=value)")

	regionCmd.MarkFlagRequired("region")
	regionCmd.MarkFlagRequired("start")
	regionCmd.MarkFlagRequired("end")

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
	slog.Info("running report region costs")
	opts, err := parseReportRegionCostsOpts(cmd)
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

func parseReportRegionCostsOpts(cmd *cobra.Command) (*rrc.RegionCosterOpts, error) {
	region, err := cmd.Flags().GetString("region")
	if err != nil {
		return nil, fmt.Errorf("failed to get region: %v", err)
	}

	startDate, err := cmd.Flags().GetString("start")
	if err != nil {
		return nil, fmt.Errorf("failed to get start date: %v", err)
	}

	endDate, err := cmd.Flags().GetString("end")
	if err != nil {
		return nil, fmt.Errorf("failed to get end date: %v", err)
	}

	hourly, err := cmd.Flags().GetBool("hourly")
	if err != nil {
		return nil, fmt.Errorf("failed to get hourly flag: %v", err)
	}

	daily, err := cmd.Flags().GetBool("daily")
	if err != nil {
		return nil, fmt.Errorf("failed to get daily flag: %v", err)
	}

	monthly, err := cmd.Flags().GetBool("monthly")
	if err != nil {
		return nil, fmt.Errorf("failed to get monthly flag: %v", err)
	}

	tag, err := cmd.Flags().GetStringSlice("tag")
	if err != nil {
		return nil, fmt.Errorf("failed to get tag: %v", err)
	}

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

	start, err := time.Parse(dateFormat, startDate)
	if err != nil {
		return nil, fmt.Errorf("invalid start date format '%s': expected YYYY-MM-DD", startDate)
	}

	end, err := time.Parse(dateFormat, endDate)
	if err != nil {
		return nil, fmt.Errorf("invalid end date format '%s': expected YYYY-MM-DD", endDate)
	}

	if start.After(end) {
		return nil, fmt.Errorf("start date '%s' cannot be after end date '%s'", startDate, endDate)
	}

	opts := rrc.RegionCosterOpts{
		Region:      region,
		StartDate:   start,
		EndDate:     end,
		Granularity: providedGranularity,
		Tag:         tag,
	}

	return &opts, nil
}
