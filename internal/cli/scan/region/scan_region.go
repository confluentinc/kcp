package region

import (
	"fmt"

	"github.com/confluentinc/kcp/internal/client"
	rs "github.com/confluentinc/kcp/internal/generators/scan/region"
	"github.com/confluentinc/kcp/internal/utils"

	"github.com/spf13/cobra"
)

var (
	region string
)

func NewScanRegionCmd() *cobra.Command {
	regionCmd := &cobra.Command{
		Use:   "region",
		Short: "Scan an AWS region for MSK clusters",
		Long: `Scan an AWS region for MSK clusters and gather information about them.

All flags can be provided via environment variables (uppercase, with underscores):

FLAG                     | ENV_VAR
-------------------------|---------------------------
--region                 | REGION=us-east-1
`,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		PreRunE:       preRunScanRegion,
		RunE:          runScanRegion,
	}

	regionCmd.Flags().StringVar(&region, "region", "", "The AWS region")

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
