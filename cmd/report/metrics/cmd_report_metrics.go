package metrics

import (
	"github.com/confluentinc/kcp/cmd/report/costs"
	"github.com/confluentinc/kcp/internal/utils"
	"github.com/spf13/cobra"
)

var (
	stateFile string
)

func NewReportMetricsCmd() *cobra.Command {
	reportCmd := &cobra.Command{
		Use:           "metrics",
		Short:         "Generate a report of metrics for given cluster(s)",
		Long:          "Generate a report of metrics for the given cluster(s) based on the data collected by `kcp discover`",
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		PreRunE:       preRunReportMetrics,
		RunE:          runReportMetrics,
	}

	// groups := map[*pflag.FlagSet]string{}

	// requiredFlags := pflag.NewFlagSet("required", pflag.ExitOnError)
	// requiredFlags.SortFlags = false
	// requiredFlags.StringVar(&stateFile, "state-file", "", "The path to the kcp state file where the MSK cluster discovery reports have been written to.")
	// reportCmd.Flags().AddFlagSet(requiredFlags)
	// groups[requiredFlags] = "Required Flags"

	// reportCmd.SetUsageFunc(func(c *cobra.Command) error {
	// 	fmt.Printf("%s\n\n", c.Short)

	// 	flagOrder := []*pflag.FlagSet{requiredFlags}
	// 	groupNames := []string{"Required Flags"}

	// 	for i, fs := range flagOrder {
	// 		usage := fs.FlagUsages()
	// 		if usage != "" {
	// 			fmt.Printf("%s:\n%s\n", groupNames[i], usage)
	// 		}
	// 	}

	// 	fmt.Println("All flags can be provided via environment variables (uppercase, with underscores).")

	// 	return nil
	// })

	// reportCmd.MarkFlagRequired("state-file")

	reportCmd.AddCommand(costs.NewReportCostsCmd())

	return reportCmd
}

func preRunReportMetrics(cmd *cobra.Command, args []string) error {
	if err := utils.BindEnvToFlags(cmd); err != nil {
		return err
	}
	return nil
}

func runReportMetrics(cmd *cobra.Command, args []string) error {

	return nil
}

// func parseReportMetricsOpts() (*ReporterMetricsOpts, error) {
// if _, err := os.Stat(stateFile); os.IsNotExist(err) {
// 	return nil, fmt.Errorf("❌ state file does not exist: %s", stateFile)
// }

// file, err := os.ReadFile(stateFile)
// if err != nil {
// 	return nil, fmt.Errorf("failed to read state file: %v", err)
// }

// var state types.State
// if err := json.Unmarshal(file, &state); err != nil {
// 	return nil, fmt.Errorf("failed to unmarshal state: %v", err)
// }

// opts := ReporterMetricsOpts{
// 	State: state,
// }

// 	return &opts, nil
// }
