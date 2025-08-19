package broker_logs

import (
	"fmt"

	"github.com/confluentinc/kcp/internal/client"
	"github.com/confluentinc/kcp/internal/generators/scan/broker_logs"
	"github.com/confluentinc/kcp/internal/services/s3"
	"github.com/confluentinc/kcp/internal/utils"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

var (
	s3Uri  string
	region string
)

func NewScanBrokerLogsCmd() *cobra.Command {
	brokerLogsCmd := &cobra.Command{
		Use:           "broker-logs",
		Short:         "Scan the broker logs for client activity",
		Long:          "Scan the broker logs to help identify clients that are using the cluster based on activity in the logs",
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		PreRunE:       preRunScanBrokerLogs,
		RunE:          runScanBrokerLogs,
	}

	groups := map[*pflag.FlagSet]string{}

	requiredFlags := pflag.NewFlagSet("required", pflag.ExitOnError)
	requiredFlags.SortFlags = false
	requiredFlags.StringVar(&region, "region", "", "The AWS region")
	requiredFlags.StringVar(&s3Uri, "s3-uri", "", "The S3 URI to the broker logs folder (e.g., s3://my-bucket/kafka-logs/2025-08-04-06/)")

	brokerLogsCmd.Flags().AddFlagSet(requiredFlags)

	groups[requiredFlags] = "Required Flags"

	brokerLogsCmd.SetUsageFunc(func(c *cobra.Command) error {
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

	return brokerLogsCmd
}

func preRunScanBrokerLogs(cmd *cobra.Command, args []string) error {
	if err := utils.BindEnvToFlags(cmd); err != nil {
		return err
	}

	return nil
}

func runScanBrokerLogs(cmd *cobra.Command, args []string) error {
	opts, err := parseScanBrokerLogsOpts()
	if err != nil {
		return fmt.Errorf("failed to parse scan broker logs opts: %v", err)
	}

	s3Client, err := client.NewS3Client(opts.Region)
	if err != nil {
		return fmt.Errorf("failed to create S3 client: %w", err)
	}

	s3Service := s3.NewS3Service(s3Client)

	brokerLogsScanner, err := broker_logs.NewBrokerLogsScanner(s3Service, *opts)
	if err != nil {
		return fmt.Errorf("failed to create broker logs scanner: %v", err)
	}

	if err := brokerLogsScanner.Run(); err != nil {
		return err
	}

	return nil
}

func parseScanBrokerLogsOpts() (*broker_logs.BrokerLogsScannerOpts, error) {
	opts := broker_logs.BrokerLogsScannerOpts{
		S3Uri:  s3Uri,
		Region: region,
	}

	return &opts, nil
}
