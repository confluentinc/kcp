package broker_logs

import (
	"github.com/confluentinc/kcp/internal/generators/scan/broker_logs"
	"github.com/confluentinc/kcp/internal/utils"
	"github.com/spf13/cobra"
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

	return brokerLogsCmd
}

func runScanBrokerLogs(cmd *cobra.Command, args []string) error {
	opts := broker_logs.BrokerLogsScannerOpts{}
	scanner := broker_logs.NewBrokerLogsScanner(opts)

	if err := scanner.Run(); err != nil {
		return err
	}

	return nil
}

func preRunScanBrokerLogs(cmd *cobra.Command, args []string) error {
	if err := utils.BindEnvToFlags(cmd); err != nil {
		return err
	}

	return nil
}
