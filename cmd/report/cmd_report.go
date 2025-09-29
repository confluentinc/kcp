package report

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/confluentinc/kcp/internal/services/markdown"
	rservice "github.com/confluentinc/kcp/internal/services/report"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/confluentinc/kcp/internal/utils"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

var (
	stateFile string
)


func NewReportCmd() *cobra.Command {
	reportCmd := &cobra.Command{
		Use:           "report",
		Short:         "Generate a report of the data collected by `kcp discover`",
		Long:          "Generate a report of the data collected by `kcp discover`",
		SilenceErrors: true,
		PreRunE:       preRunReport,
		RunE:          runReport,
		// todo - just hiding this for now until we know if we want to invest the time in it or not.
		Hidden: true,
	}

	groups := map[*pflag.FlagSet]string{}

	requiredFlags := pflag.NewFlagSet("required", pflag.ExitOnError)
	requiredFlags.SortFlags = false
	requiredFlags.StringVar(&stateFile, "state-file", "", "The path to the kcp state file where the MSK cluster discovery reports have been written to.")
	reportCmd.Flags().AddFlagSet(requiredFlags)
	groups[requiredFlags] = "Required Flags"

	reportCmd.SetUsageFunc(func(c *cobra.Command) error {
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

	reportCmd.MarkFlagRequired("state-file")

	return reportCmd
}

func preRunReport(cmd *cobra.Command, args []string) error {
	if err := utils.BindEnvToFlags(cmd); err != nil {
		return err
	}
	return nil
}

func runReport(cmd *cobra.Command, args []string) error {
	opts, err := parseReportOpts()
	if err != nil {
		return fmt.Errorf("failed to parse report opts: %v", err)
	}

	reportService := rservice.NewReportService()

	markdownService := markdown.New()

	reporter := NewReporter(reportService, *markdownService, *opts)
	if err := reporter.Run(); err != nil {
		return fmt.Errorf("❌ failed to scan clusters: %v", err)
	}
	return nil
}

func parseReportOpts() (*ReporterOpts, error) {
	if _, err := os.Stat(stateFile); os.IsNotExist(err) {
		return nil, fmt.Errorf("❌ state file does not exist: %s", stateFile)
	}

	file, err := os.ReadFile(stateFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read state file: %v", err)
	}

	var state types.State
	if err := json.Unmarshal(file, &state); err != nil {
		return nil, fmt.Errorf("failed to unmarshal state: %v", err)
	}

	opts := ReporterOpts{
		State: state,
	}

	return &opts, nil
}
