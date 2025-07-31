package broker_logs

import "log/slog"

type BrokerLogsScannerOpts struct {
}

type BrokerLogsScanner struct {
}

func NewBrokerLogsScanner(opts BrokerLogsScannerOpts) *BrokerLogsScanner {
	return &BrokerLogsScanner{}
}

func (bs *BrokerLogsScanner) Run() error {
	slog.Info("🚀 starting broker logs scan")

	return nil
}
