package client_inventory

import (
	"fmt"

	"github.com/confluentinc/kcp/internal/client"
	"github.com/confluentinc/kcp/internal/services/s3"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/confluentinc/kcp/internal/utils"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

var (
	s3Uri     string
	stateFile string
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
	requiredFlags.StringVar(&s3Uri, "s3-uri", "", "The S3 URI to the broker logs folder (e.g., s3://my-bucket/kafka-logs/2025-08-04-06/)")
	requiredFlags.StringVar(&stateFile, "state-file", "", "The path to the kcp state file where the client inventory reports have been written to.")

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

	clientInventoryCmd.MarkFlagRequired("s3-uri")
	clientInventoryCmd.MarkFlagRequired("state-file")

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

	state, err := types.NewStateFromFile(stateFile)
	if err != nil {
		return fmt.Errorf("failed to load existing state file: %v", err)
	}

	s3Client, err := client.NewS3Client(opts.Region)
	if err != nil {
		return fmt.Errorf("failed to create S3 client: %w", err)
	}

	s3Service := s3.NewS3Service(s3Client)

	clientInventoryScanner, err := NewClientInventoryScanner(s3Service, *state, *opts)
	if err != nil {
		return fmt.Errorf("failed to create client inventory scanner: %v", err)
	}

	if err := clientInventoryScanner.Run(); err != nil {
		return err
	}

	return nil
}

func parseScanClientInventoryOpts() (*ClientInventoryScannerOpts, error) {
	region, err := utils.ExtractRegionFromS3Uri(s3Uri)
	if err != nil {
		return nil, fmt.Errorf("failed to extract region from S3 URI: %w", err)
	}

	clusterName, err := utils.ExtractClusterNameFromS3Uri(s3Uri)
	if err != nil {
		return nil, fmt.Errorf("failed to extract cluster name from S3 URI: %w", err)
	}

	opts := ClientInventoryScannerOpts{
		S3Uri:       s3Uri,
		Region:      region,
		ClusterName: clusterName,
		StateFile:   stateFile,
	}

	return &opts, nil
}
