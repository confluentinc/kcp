package broker_logs

import (
	"fmt"

	"github.com/confluentinc/kcp/internal/generators/scan/broker_logs"
	"github.com/confluentinc/kcp/internal/utils"
	"github.com/spf13/cobra"
)

var (
	s3Uri string
)

func NewScanBrokerLogsCmd() *cobra.Command {
	brokerLogsCmd := &cobra.Command{
		Use:   "broker-logs",
		Short: "Scan the broker logs for the cluster",
		Long: `Scan the broker logs for the cluster for information that will help with migration.

All flags can be provided via environment variables (uppercase, with underscores):

FLAG                        | ENV_VAR
----------------------------|-----------------------------------------------------
Required flags:
`,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		PreRunE:       preRunScanBrokerLogs,
		RunE:          runScanBrokerLogs,
	}

	brokerLogsCmd.Flags().StringVar(&s3Uri, "s3-uri", "", "S3 URI to the broker log file (e.g., s3://bucket/path/to/logs.txt)")
	brokerLogsCmd.MarkFlagRequired("s3-uri")
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

	scanner := broker_logs.NewBrokerLogsScanner(*opts)

	if err := scanner.Run(); err != nil {
		return err
	}

	return nil
}

func parseScanBrokerLogsOpts() (*broker_logs.BrokerLogsScannerOpts, error) {
	opts := broker_logs.BrokerLogsScannerOpts{
		S3Uri: s3Uri,
	}

	return &opts, nil
}
