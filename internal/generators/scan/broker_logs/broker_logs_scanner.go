package broker_logs

import (
	"bufio"
	"context"
	"encoding/csv"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var (
	// lines that match this pattern will be parsed by kafka trace line parser
	KafkaApiTracePattern = regexp.MustCompile(`^\[.*\] TRACE \[KafkaApi-\d+\].*\(kafka\.server\.KafkaApis\)$`)
)

type BrokerLogsScannerOpts struct {
	S3Uri  string
	Region string
}

type BrokerLogsScanner struct {
	s3Service            S3Service
	kafkaTraceLineParser *KafkaApiTraceLineParser
	s3Uri                string
	region               string
}

type S3Service interface {
	ParseS3URI(s3Uri string) (string, string, error)
	ListLogFiles(ctx context.Context, bucket, prefix string) ([]string, error)
	DownloadAndDecompressLogFile(ctx context.Context, bucket, key string) ([]byte, error)
}

type RequestMetadata struct {
	CompositeKey string
	ClientId     string
	ClientType   string
	Topic        string
	Role         string
	GroupId      string
	Principal    string
	Auth         string
	IPAddress    string
	ApiKey       string
	Timestamp    time.Time
	FileName     string
	LineNumber   int
	LogLine      string
}

func NewBrokerLogsScanner(s3Service S3Service, opts BrokerLogsScannerOpts) (*BrokerLogsScanner, error) {
	return &BrokerLogsScanner{
		s3Service:            s3Service,
		kafkaTraceLineParser: &KafkaApiTraceLineParser{},
		s3Uri:                opts.S3Uri,
		region:               opts.Region,
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

	requestMetadataByCompositeKey := bs.handleLogFiles(ctx, bucket, logFiles)

	if err := bs.generateCSV(requestMetadataByCompositeKey); err != nil {
		slog.Error("failed to write CSV file", "error", err)
	}

	return nil
}

func (bs *BrokerLogsScanner) handleLogFiles(ctx context.Context, bucket string, logFiles []string) map[string]*RequestMetadata {
	requestMetadataByCompositeKey := make(map[string]*RequestMetadata)

	for _, file := range logFiles {
		requestsMetadata, err := bs.handleLogFile(ctx, bucket, file)
		if err != nil {
			slog.Error("failed to extract API requests", "file", file, "error", err)
			continue
		}

		slog.Info("found API requests", "file", file, "count", len(requestsMetadata))

		for _, metadata := range requestsMetadata {
			// we cannot guarantee that the client id is unique as it may not be set on clients, s
			// a composite key is used to try to deduplicate requests
			compositeKey := metadata.CompositeKey
			slog.Info("composite key", "composite_key", compositeKey)
			existingRequestMetadata, exists := requestMetadataByCompositeKey[compositeKey]
			if !exists {
				// first time we've seen this client
				requestMetadataByCompositeKey[compositeKey] = &metadata
				continue
			}

			// store the most recent request
			if metadata.Timestamp.After(existingRequestMetadata.Timestamp) {
				requestMetadataByCompositeKey[compositeKey] = &metadata
			}
		}
	}

	return requestMetadataByCompositeKey
}

func (bs *BrokerLogsScanner) handleLogFile(ctx context.Context, bucket, key string) ([]RequestMetadata, error) {
	//  temp thing output folder to write all the the files to
	outputFolder := "log_output"
	if err := os.MkdirAll(outputFolder, 0755); err != nil {
		return nil, fmt.Errorf("failed to create output folder: %w", err)
	}

	content, err := bs.s3Service.DownloadAndDecompressLogFile(ctx, bucket, key)
	if err != nil {
		return nil, fmt.Errorf("failed to download and decompress file: %w", err)
	}

	// this is temporary to help with debugging
	fileName := filepath.Base(strings.TrimSuffix(filepath.Base(key), ".gz"))
	filePath := filepath.Join(outputFolder, fileName)
	if err := os.WriteFile(filePath, content, 0644); err != nil {
		return nil, fmt.Errorf("failed to write file: %w", err)
	}

	var requestsMetadata []RequestMetadata
	scanner := bufio.NewScanner(strings.NewReader(string(content)))
	lineNumber := 0

	for scanner.Scan() {
		line := scanner.Text()

		switch {
		case KafkaApiTracePattern.MatchString(line):
			metadata, err := bs.kafkaTraceLineParser.Parse(line, lineNumber, key)
			if err != nil {
				slog.Debug("failed to parse Kafka API line", "line", line, "error", err)
				continue
			}
			requestsMetadata = append(requestsMetadata, *metadata)
		default:
			slog.Debug("not a log line we want to process", "line", line)
			continue
		}

		lineNumber++
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error reading file content: %w", err)
	}

	return requestsMetadata, nil
}

type csvColumn struct {
	header    string
	extractor func(*RequestMetadata) string
}

func (bs *BrokerLogsScanner) generateCSV(requestMetadataByClientId map[string]*RequestMetadata) error {
	fileName := "broker_logs_scan_results.csv"

	file, err := os.Create(fileName)
	if err != nil {
		return fmt.Errorf("failed to create CSV file: %w", err)
	}
	defer file.Close()

	writer := csv.NewWriter(file)
	defer writer.Flush()

	columns := []csvColumn{
		{"Client ID", func(m *RequestMetadata) string { return m.ClientId }},
		{"Client Type", func(m *RequestMetadata) string { return m.ClientType }},
		{"Role", func(m *RequestMetadata) string { return m.Role }},
		{"Topic", func(m *RequestMetadata) string { return m.Topic }},
		{"IP Address", func(m *RequestMetadata) string { return m.IPAddress }},
		{"Auth", func(m *RequestMetadata) string { return m.Auth }},
		{"Principal", func(m *RequestMetadata) string { return m.Principal }},
		{"Timestamp", func(m *RequestMetadata) string { return m.Timestamp.Format("2006-01-02 15:04:05") }},

		// this is temporary just for debugging
		{"File Name", func(m *RequestMetadata) string { return m.FileName }},
		{"Line Number", func(m *RequestMetadata) string { return fmt.Sprintf("%d", m.LineNumber) }},
		{"Log Line", func(m *RequestMetadata) string { return m.LogLine }},
		{"Composite Key", func(m *RequestMetadata) string { return m.CompositeKey }},
	}

	header := make([]string, len(columns))
	for i, col := range columns {
		header[i] = col.header
	}
	if err := writer.Write(header); err != nil {
		return fmt.Errorf("failed to write CSV header: %w", err)
	}

	if len(requestMetadataByClientId) == 0 {
		slog.Info("no requests to write to CSV")
		return nil
	}

	for _, metadata := range requestMetadataByClientId {
		record := make([]string, len(columns))
		for i, col := range columns {
			record[i] = col.extractor(metadata)
		}
		if err := writer.Write(record); err != nil {
			return fmt.Errorf("failed to write CSV record: %w", err)
		}
	}

	return nil
}
