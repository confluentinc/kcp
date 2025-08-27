package scan

import (
	"fmt"
	"github.com/confluentinc/kcp/internal/generators/scan"
	"github.com/confluentinc/kcp/internal/cli/scan/client_inventory"
	"github.com/confluentinc/kcp/internal/cli/scan/cluster"
	"github.com/confluentinc/kcp/internal/cli/scan/region"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

var (
	regions    []string
	clusterCmd *cobra.Command
	regionCmd  *cobra.Command
)

func NewScanCmd() *cobra.Command {
	scanCmd := &cobra.Command{
		Use:           "scan",
		Short:         "Multi-region, multi cluster scan of AWS MSK",
		Long:          "Performs a full Discovery of all MSK clusters across multiple regions, and their associated resources, costs and metrics",
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		RunE:          runScan,
	}

	clusterCmd = cluster.NewScanClusterCmd()
	regionCmd = region.NewScanRegionCmd()
	scanCmd.AddCommand(
		cluster.NewScanClusterCmd(),
		region.NewScanRegionCmd(),
		client_inventory.NewScanClientInventoryCmd(),
	)

	groups := map[*pflag.FlagSet]string{}

	// Required flags.
	requiredFlags := pflag.NewFlagSet("required", pflag.ExitOnError)
	requiredFlags.SortFlags = false
	requiredFlags.StringSliceVar(&regions, "region", []string{}, "AWS regions to scan (comma separated list or repeated flag)")
	scanCmd.Flags().AddFlagSet(requiredFlags)
	groups[requiredFlags] = "Required Flags"

	scanCmd.SetUsageFunc(func(c *cobra.Command) error {
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

	scanCmd.MarkFlagRequired("region")

	return scanCmd
}

func runScan(cmd *cobra.Command, args []string) error {

	opts, err := parseScanOpts()
	if err != nil {
		return fmt.Errorf("failed to parse scan region opts: %v", err)
	}

	regionScanner := scan.NewScanner(*opts)
	if err := regionScanner.Run(); err != nil {
		return fmt.Errorf("failed to scan: %v", err)
	}

	return nil

}

func parseScanOpts() (*scan.ScanOpts, error) {
	opts := scan.ScanOpts{
		Regions: regions,
	}

	return &opts, nil
}
