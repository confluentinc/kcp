package region

import (
	"fmt"

	"github.com/confluentinc/kcp/internal/client"
	rs "github.com/confluentinc/kcp/internal/generators/scan/region"
	"github.com/confluentinc/kcp/internal/utils"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

var (
	region string
)

func NewScanRegionCmd() *cobra.Command {
	regionCmd := &cobra.Command{
		Use:           "region",
		Short:         "Scan an AWS region for MSK clusters",
		Long:          "Scan an AWS region for MSK clusters and gather information about them",
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		PreRunE:       preRunScanRegion,
		RunE:          runScanRegion,
	}

	groups := map[*pflag.FlagSet]string{}

	// Required flags.
	requiredFlags := pflag.NewFlagSet("required", pflag.ExitOnError)
	requiredFlags.SortFlags = false
	requiredFlags.StringVar(&region, "region", "", "AWS region")
	regionCmd.Flags().AddFlagSet(requiredFlags)
	groups[requiredFlags] = "Required Flags"

	regionCmd.SetUsageFunc(func(c *cobra.Command) error {
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

	regionCmd.MarkFlagRequired("region")

	return regionCmd
}

// sets flag values from corresponding environment variables if flags weren't explicitly provided
func preRunScanRegion(cmd *cobra.Command, args []string) error {
	if err := utils.BindEnvToFlags(cmd); err != nil {
		return err
	}

	return nil
}

func runScanRegion(cmd *cobra.Command, args []string) error {
	opts, err := parseScanRegionOpts()
	if err != nil {
		return fmt.Errorf("failed to parse scan region opts: %v", err)
	}

	mskClient, err := client.NewMSKClient(opts.Region)
	if err != nil {
		return fmt.Errorf("failed to create msk client: %v", err)
	}

	regionScanner := rs.NewRegionScanner(mskClient, *opts)
	if err := regionScanner.Run(); err != nil {
		return fmt.Errorf("failed to scan region: %v", err)
	}

	return nil
}

func parseScanRegionOpts() (*rs.ScanRegionOpts, error) {
	opts := rs.ScanRegionOpts{
		Region: region,
	}

	return &opts, nil
}
