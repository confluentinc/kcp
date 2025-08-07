package broker_logs

import (
	"bufio"
	"context"
	"encoding/csv"
	"fmt"
	"log/slog"
	"os"
	"regexp"
	"strings"
	"time"
)

var (
	KafkaApiTracePattern = regexp.MustCompile(`^\[.*\] TRACE \[KafkaApi-\d+\].*\(kafka\.server\.KafkaApis\)$`)
)

type BrokerLogsScannerOpts struct {
	S3Uri  string
	Region string
}

type BrokerLogsScanner struct {
	s3Service S3Service
	s3Uri     string
	region    string
}

type S3Service interface {
	ParseS3URI(s3Uri string) (string, string, error)
	ListLogFiles(ctx context.Context, bucket, prefix string) ([]string, error)
	DownloadAndDecompressLogFile(ctx context.Context, bucket, key string) ([]byte, error)
}

type ApiRequest struct {
	Timestamp  time.Time
	ApiKey     string
	ClientId   string
	Topic      string
	Auth       string
	Principal  string
	IPAddress  string
	FileName   string
	LineNumber int
	LogLine    string
}

func NewBrokerLogsScanner(s3Service S3Service, opts BrokerLogsScannerOpts) (*BrokerLogsScanner, error) {
	return &BrokerLogsScanner{
		s3Service: s3Service,
		s3Uri:     opts.S3Uri,
		region:    opts.Region,
	}, nil
}

func (bs *BrokerLogsScanner) Run() error {
	slog.Info("🚀 starting broker logs scan", "s3_uri", bs.s3Uri)

	ctx := context.Background()

	bucket, prefix, err := bs.s3Service.ParseS3URI(bs.s3Uri)
	if err != nil {
		return fmt.Errorf("failed to parse S3 URI: %w", err)
	}

	logFiles, err := bs.s3Service.ListLogFiles(ctx, bucket, prefix)
	if err != nil {
		return fmt.Errorf("failed to list log files: %w", err)
	}

	if len(logFiles) == 0 {
		slog.Info("no log files found to process")
		return nil
	}

	var allApiRequests []ApiRequest
	errorCount := 0

	for _, file := range logFiles {
		apiRequests, err := bs.extractKafkaApiRequests(ctx, bucket, file)
		if err != nil {
			slog.Error("failed to extract API requests", "file", file, "error", err)
			errorCount++
			continue
		}

		slog.Info("found API requests", "file", file, "count", len(apiRequests))
		allApiRequests = append(allApiRequests, apiRequests...)
	}

	// Write results to CSV
	if len(allApiRequests) > 0 {
		if err := bs.writeToCSV(allApiRequests); err != nil {
			slog.Error("failed to write CSV file", "error", err)
		}
	}

	if errorCount > 0 {
		slog.Warn("processing completed with errors", "total_files", len(logFiles), "errors", errorCount)
	} else {
		slog.Info("successfully processed all log files", "total_files", len(logFiles))
	}

	return nil
}

func (bs *BrokerLogsScanner) extractKafkaApiRequests(ctx context.Context, bucket, key string) ([]ApiRequest, error) {
	content, err := bs.s3Service.DownloadAndDecompressLogFile(ctx, bucket, key)
	if err != nil {
		return nil, fmt.Errorf("failed to download and decompress file: %w", err)
	}

	var apiRequests []ApiRequest
	scanner := bufio.NewScanner(strings.NewReader(string(content)))
	lineNumber := 0

	for scanner.Scan() {
		line := scanner.Text()

		switch {
		case KafkaApiTracePattern.MatchString(line):
			kafkaTraceLineParser := &KafkaApiTraceLineParser{}
			apiRequest, err := kafkaTraceLineParser.Parse(line, lineNumber, key)
			if err != nil {
				slog.Debug("failed to parse Kafka API line", "line", line, "error", err)
				continue
			}
			apiRequests = append(apiRequests, *apiRequest)
		default:
			slog.Debug("not a log line we want to process", "line", line)
			continue
		}

		lineNumber++
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error reading file content: %w", err)
	}

	return apiRequests, nil
}

func (bs *BrokerLogsScanner) writeToCSV(apiRequests []ApiRequest) error {
	fileName := "broker_logs_scan_results.csv"

	file, err := os.Create(fileName)
	if err != nil {
		return fmt.Errorf("failed to create CSV file: %w", err)
	}
	defer file.Close()

	writer := csv.NewWriter(file)
	defer writer.Flush()

	// Write CSV header
	header := []string{
		"Timestamp",
		"API Key",
		"Client ID",
		"Topic",
		"IP Address",
		"Auth",
		"Principal",
		"File Name",
		"Line Number",
		"Log Line",
	}
	if err := writer.Write(header); err != nil {
		return fmt.Errorf("failed to write CSV header: %w", err)
	}

	// Write data rows
	for _, req := range apiRequests {
		record := []string{
			req.Timestamp.Format("2006-01-02 15:04:05"),
			req.ApiKey,
			req.ClientId,
			req.Topic,
			req.IPAddress,
			req.Auth,
			req.Principal,
			req.FileName,
			fmt.Sprintf("%d", req.LineNumber),
			req.LogLine,
		}
		if err := writer.Write(record); err != nil {
			return fmt.Errorf("failed to write CSV record: %w", err)
		}
	}

	return nil
}
