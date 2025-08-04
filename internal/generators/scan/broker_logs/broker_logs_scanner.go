package broker_logs

import (
	"context"
	"fmt"
	"log/slog"
)

type BrokerLogsScannerOpts struct {
	S3Uri  string
	Region string
}

type BrokerLogsScanner struct {
	s3Uri     string
	region    string
	s3Service S3Service
}

type S3Service interface {
	ParseS3URI(s3Uri string) (string, string, error)
	ListLogFiles(ctx context.Context, bucket, prefix string) ([]string, error)
	// DownloadAndDecompressLogFile(ctx context.Context, bucket, key string) ([]byte, error)
}

func NewBrokerLogsScanner(s3Service S3Service, opts BrokerLogsScannerOpts) (*BrokerLogsScanner, error) {
	return &BrokerLogsScanner{
		s3Uri:     opts.S3Uri,
		region:    opts.Region,
		s3Service: s3Service,
	}, nil
}

func (bs *BrokerLogsScanner) Run() error {
	slog.Info("🚀 starting broker logs scan", "s3_uri", bs.s3Uri)

	ctx := context.Background()

	bucket, prefix, err := bs.s3Service.ParseS3URI(bs.s3Uri)
	if err != nil {
		return fmt.Errorf("failed to parse S3 URI: %w", err)
	}

	slog.Info("parsed S3 URI", "bucket", bucket, "prefix", prefix)

	logFiles, err := bs.s3Service.ListLogFiles(ctx, bucket, prefix)
	if err != nil {
		return fmt.Errorf("failed to list log files: %w", err)
	}

	slog.Info("found log files", "count", len(logFiles))
	for i, file := range logFiles {
		slog.Info("log file", "index", i+1, "file", file)
	}

	// TODO: Next steps will be to process each file
	return nil
}
