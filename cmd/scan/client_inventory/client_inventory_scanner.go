package client_inventory

import (
	"bufio"
	"context"
	"encoding/csv"
	"fmt"
	"log/slog"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/confluentinc/kcp/internal/types"
)

var (
	// lines that match this pattern will be parsed by kafka trace line parser
	KafkaApiTracePattern = regexp.MustCompile(`^\[.*\] TRACE \[KafkaApi-\d+\].*\(kafka\.server\.KafkaApis\)$`)
)

type ClientInventoryScannerOpts struct {
	S3Uri  string
	Region string
}

type ClientInventoryScanner struct {
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
	Topic        string
	Role         string
	Principal    string
	Auth         string
	ApiKey       string
	Timestamp    time.Time
}

func NewClientInventoryScanner(s3Service S3Service, opts ClientInventoryScannerOpts) (*ClientInventoryScanner, error) {
	return &ClientInventoryScanner{
		s3Service:            s3Service,
		kafkaTraceLineParser: &KafkaApiTraceLineParser{},
		s3Uri:                opts.S3Uri,
		region:               opts.Region,
	}, nil
}

func (cis *ClientInventoryScanner) Run() error {
	slog.Info("🚀 starting client inventory scan", "s3_uri", cis.s3Uri)

	ctx := context.Background()

	bucket, prefix, err := cis.s3Service.ParseS3URI(cis.s3Uri)
	if err != nil {
		return fmt.Errorf("failed to parse S3 URI: %w", err)
	}

	logFiles, err := cis.s3Service.ListLogFiles(ctx, bucket, prefix)
	if err != nil {
		return fmt.Errorf("failed to list log files: %w", err)
	}

	if len(logFiles) == 0 {
		slog.Info("no log files found to process")
		return nil
	}

	// write this to the state file
	discoveredClients := cis.handleLogFiles(ctx, bucket, logFiles)

	_ = discoveredClients

	return nil
}

func (cis *ClientInventoryScanner) handleLogFiles(ctx context.Context, bucket string, logFiles []string) []types.DiscoveredClient {
	requestMetadataByCompositeKey := make(map[string]*RequestMetadata)

	for _, file := range logFiles {
		requestsMetadata, err := cis.handleLogFile(ctx, bucket, file)
		if err != nil {
			slog.Error("failed to extract API requests", "file", file, "error", err)
			continue
		}

		slog.Info(fmt.Sprintf("parsed log file %s: found %d matching log lines", file, len(requestsMetadata)))

		for _, metadata := range requestsMetadata {
			// we cannot guarantee that the client id is unique as it may not be set on clients
			// a composite key is used to try to deduplicate requests
			compositeKey := metadata.CompositeKey
			existingRequestMetadata, exists := requestMetadataByCompositeKey[compositeKey]
			if !exists {
				// first time we've seen this composite key
				requestMetadataByCompositeKey[compositeKey] = &metadata
				continue
			}

			// store the most recent request
			if metadata.Timestamp.After(existingRequestMetadata.Timestamp) {
				requestMetadataByCompositeKey[compositeKey] = &metadata
			}
		}
	}

	discoveredClients := []types.DiscoveredClient{}
	for _, metadata := range requestMetadataByCompositeKey {
		discoveredClient := types.DiscoveredClient{
			CompositeKey: metadata.CompositeKey,
			ClientId:     metadata.ClientId,
			Role:         metadata.Role,
			Topic:        metadata.Topic,
			Auth:         metadata.Auth,
			Principal:    metadata.Principal,
			Timestamp:    metadata.Timestamp,
		}

		discoveredClients = append(discoveredClients, discoveredClient)
	}

	return discoveredClients
}

func (cis *ClientInventoryScanner) handleLogFile(ctx context.Context, bucket, key string) ([]RequestMetadata, error) {
	content, err := cis.s3Service.DownloadAndDecompressLogFile(ctx, bucket, key)
	if err != nil {
		return nil, fmt.Errorf("failed to download and decompress file: %w", err)
	}

	var requestsMetadata []RequestMetadata
	scanner := bufio.NewScanner(strings.NewReader(string(content)))
	lineNumber := 0

	for scanner.Scan() {
		line := scanner.Text()

		switch {
		case KafkaApiTracePattern.MatchString(line):
			metadata, err := cis.kafkaTraceLineParser.Parse(line, lineNumber, key)
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
	header        string
	extractorFunc func(*RequestMetadata) string
}

func (cis *ClientInventoryScanner) generateCSV(requestMetadataByCompositeKey map[string]*RequestMetadata) error {
	fileName := "client_inventory_scan_results.csv"

	file, err := os.Create(fileName)
	if err != nil {
		return fmt.Errorf("failed to create CSV file: %w", err)
	}
	defer file.Close()

	writer := csv.NewWriter(file)
	defer writer.Flush()

	columns := []csvColumn{
		{
			header:        "Client ID",
			extractorFunc: func(m *RequestMetadata) string { return m.ClientId },
		},
		{
			header:        "Role",
			extractorFunc: func(m *RequestMetadata) string { return m.Role },
		},
		{
			header:        "Topic",
			extractorFunc: func(m *RequestMetadata) string { return m.Topic },
		},
		{
			header:        "Auth",
			extractorFunc: func(m *RequestMetadata) string { return m.Auth },
		},
		{
			header:        "Principal",
			extractorFunc: func(m *RequestMetadata) string { return m.Principal },
		},
		{
			header:        "Timestamp",
			extractorFunc: func(m *RequestMetadata) string { return m.Timestamp.Format("2006-01-02 15:04:05") },
		},
	}

	header := make([]string, len(columns))
	for i, col := range columns {
		header[i] = col.header
	}
	if err := writer.Write(header); err != nil {
		return fmt.Errorf("failed to write CSV header: %w", err)
	}

	if len(requestMetadataByCompositeKey) == 0 {
		slog.Info("no requests to write to CSV")
		return nil
	}

	// Convert map to slice and sort by timestamp
	var allMetadata []*RequestMetadata
	for _, metadata := range requestMetadataByCompositeKey {
		allMetadata = append(allMetadata, metadata)
	}

	sort.Slice(allMetadata, func(i, j int) bool {
		return allMetadata[i].Timestamp.Before(allMetadata[j].Timestamp)
	})

	for _, metadata := range allMetadata {
		record := make([]string, len(columns))
		for i, col := range columns {
			record[i] = col.extractorFunc(metadata)
		}
		if err := writer.Write(record); err != nil {
			return fmt.Errorf("failed to write CSV record: %w", err)
		}
	}

	return nil
}
