package client_inventory

import (
	"fmt"

	"github.com/confluentinc/kcp/internal/client"
	"github.com/confluentinc/kcp/internal/generators/scan/client_inventory"
	"github.com/confluentinc/kcp/internal/services/s3"
	"github.com/confluentinc/kcp/internal/utils"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

var (
	s3Uri  string
	region string
)

func NewScanClientInventoryCmd() *cobra.Command {
	clientInventoryCmd := &cobra.Command{
		Use:           "client-inventory",
		Short:         "Scan the broker logs for client activity",
		Long:          "Scan the broker logs in s3 to help identify clients that are using the cluster based on activity",
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		PreRunE:       preRunScanClientInventory,
		RunE:          runScanClientInventory,
	}

	groups := map[*pflag.FlagSet]string{}

	requiredFlags := pflag.NewFlagSet("required", pflag.ExitOnError)
	requiredFlags.SortFlags = false
	requiredFlags.StringVar(&region, "region", "", "The AWS region")
	requiredFlags.StringVar(&s3Uri, "s3-uri", "", "The S3 URI to the broker logs folder (e.g., s3://my-bucket/kafka-logs/2025-08-04-06/)")

	clientInventoryCmd.Flags().AddFlagSet(requiredFlags)

	groups[requiredFlags] = "Required Flags"

	clientInventoryCmd.SetUsageFunc(func(c *cobra.Command) error {
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

	return clientInventoryCmd
}

func preRunScanClientInventory(cmd *cobra.Command, args []string) error {
	if err := utils.BindEnvToFlags(cmd); err != nil {
		return err
	}

	return nil
}

func runScanClientInventory(cmd *cobra.Command, args []string) error {
	opts, err := parseScanClientInventoryOpts()
	if err != nil {
		return fmt.Errorf("failed to parse scan client inventory opts: %v", err)
	}

	s3Client, err := client.NewS3Client(opts.Region)
	if err != nil {
		return fmt.Errorf("failed to create S3 client: %w", err)
	}

	s3Service := s3.NewS3Service(s3Client)

	clientInventoryScanner, err := client_inventory.NewClientInventoryScanner(s3Service, *opts)
	if err != nil {
		return fmt.Errorf("failed to create client inventory scanner: %v", err)
	}

	if err := clientInventoryScanner.Run(); err != nil {
		return err
	}

	return nil
}

func parseScanClientInventoryOpts() (*client_inventory.ClientInventoryScannerOpts, error) {
	opts := client_inventory.ClientInventoryScannerOpts{
		S3Uri:  s3Uri,
		Region: region,
	}

	return &opts, nil
}
