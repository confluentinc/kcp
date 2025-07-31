package broker_logs

import "log/slog"

type BrokerLogsScannerOpts struct {
	S3Uri string
}

type BrokerLogsScanner struct {
	s3Uri string
}

func NewBrokerLogsScanner(opts BrokerLogsScannerOpts) *BrokerLogsScanner {
	return &BrokerLogsScanner{
		s3Uri: opts.S3Uri,
	}
}

func (bs *BrokerLogsScanner) Run() error {
	slog.Info("🚀 starting broker logs scan")

	return nil
}
