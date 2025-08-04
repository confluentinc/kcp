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
	// KafkaApiPattern  = regexp.MustCompile(`^\[.*\] TRACE \[KafkaApi-\d+\].*\(kafka\.server\.KafkaApis\)$`)
	TimestampPattern = regexp.MustCompile(`^\[([^\]]+)\]`)

	KafkaApiPattern = regexp.MustCompile(`^\[([^\]]+)\] TRACE \[KafkaApi-\d+\].*\(kafka\.server\.KafkaApis\)$`)
	ApiKeyPattern   = regexp.MustCompile(`apiKey=([^,\)]+)`)
	ClientIdPattern = regexp.MustCompile(`clientId=([^,\)]+)`)

	// Producer patterns (PRODUCE operations)
	ProducerTopicPattern     = regexp.MustCompile(`partitionSizes=\[([^-]+)-`)
	ProducerIpPattern        = regexp.MustCompile(`from connection INTERNAL_IP-(\d+\.\d+\.\d+\.\d+):`)
	ProducerAuthPattern      = regexp.MustCompile(`principal:\[([^\]]+)\]:`)
	ProducerPrincipalPattern = regexp.MustCompile(`principal:\[[^\]]+\]:\[([^\]]+)\]`)

	// Consumer patterns (FETCH operations)
	ConsumerTopicPattern     = regexp.MustCompile(`topics=\[([^\]]*)\]`)
	ConsumerIpPattern        = regexp.MustCompile(`from connection ([^;]+);`)
	ConsumerAuthPattern      = regexp.MustCompile(`principal:([^(]+)`)
	ConsumerPrincipalPattern = regexp.MustCompile(`principal:([^(]+)`)
	BrokerFetcherPattern     = regexp.MustCompile(`broker-(\d+)-fetcher-(\d+)`)
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
		apiRequests, err := bs.extractApiRequests(ctx, bucket, file)
		if err != nil {
			slog.Error("failed to extract API requests", "file", file, "error", err)
			errorCount++
			continue
		}

		slog.Info("found API requests", "file", file, "count", len(apiRequests))
		allApiRequests = append(allApiRequests, apiRequests...)
	}

	// Write results to CSV
	fmt.Println("allApiRequests", len(allApiRequests))
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

func (bs *BrokerLogsScanner) extractApiRequests(ctx context.Context, bucket, key string) ([]ApiRequest, error) {
	content, err := bs.s3Service.DownloadAndDecompressLogFile(ctx, bucket, key)
	if err != nil {
		return nil, fmt.Errorf("failed to download and decompress file: %w", err)
	}

	var apiRequests []ApiRequest
	scanner := bufio.NewScanner(strings.NewReader(string(content)))
	lineNumber := 0

	for scanner.Scan() {
		line := scanner.Text()
		lineNumber++

		kafkaApiMatches := KafkaApiPattern.FindStringSubmatch(line)
		if len(kafkaApiMatches) != 2 {
			continue
		}

		timestamp, err := time.Parse("2006-01-02 15:04:05,000", kafkaApiMatches[1])
		if err != nil {
			slog.Debug("failed to parse timestamp", "line", lineNumber, "timestamp", kafkaApiMatches[1], "error", err)
			continue
		}

		apiKey := extractField(line, ApiKeyPattern)
		clientId := extractField(line, ClientIdPattern)

		var topic, ipAddress, auth, principal string

		// Use different patterns based on API key
		switch apiKey {
		case "FETCH":
			topic = extractField(line, ConsumerTopicPattern)
			ipAddress = extractField(line, ConsumerIpPattern)
			auth = extractField(line, ConsumerAuthPattern)
			principal = extractField(line, ConsumerPrincipalPattern)

			// if internal broker thread we don't have information
			if BrokerFetcherPattern.MatchString(clientId) {
				topic = "n/a"
				ipAddress = "n/a"
				auth = "n/a"
				principal = "n/a"
			}
		case "PRODUCE":
			topic = extractField(line, ProducerTopicPattern)
			ipAddress = extractField(line, ProducerIpPattern)
			auth = extractField(line, ProducerAuthPattern)
			principal = extractField(line, ProducerPrincipalPattern)

		default:
			slog.Debug("unknown API key", "apiKey", apiKey)
			continue
		}

		apiRequest := ApiRequest{
			Timestamp:  timestamp,
			ApiKey:     apiKey,
			ClientId:   clientId,
			Topic:      topic, // Topic might be empty for some API calls
			IPAddress:  ipAddress,
			Auth:       auth,
			Principal:  principal,
			FileName:   key,
			LineNumber: lineNumber,
			LogLine:    line,
		}

		apiRequests = append(apiRequests, apiRequest)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error reading file content: %w", err)
	}

	return apiRequests, nil
}

// writeToCSV writes the API requests to a CSV file
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

// extractField extracts a field from a log line using the provided regex pattern
func extractField(line string, pattern *regexp.Regexp) string {
	matches := pattern.FindStringSubmatch(line)
	if len(matches) >= 2 {
		return matches[1]
	}
	return ""
}

type KafkaApiLineProcessor struct{}

func (p *KafkaApiLineProcessor) Parse(line string, lineNumber int, fileName string) (ApiRequest, error) {
	timestampMatches := TimestampPattern.FindStringSubmatch(line)
	if len(timestampMatches) != 2 {
		slog.Debug("not a KafkaApi log line", "line", line)
	}

	timestamp, err := time.Parse("2006-01-02 15:04:05,000", timestampMatches[1])
	fmt.Println("timestamp in here", timestamp)
	if err != nil {
		slog.Debug("failed to parse timestamp", "timestamp", timestampMatches[1], "error", err)
	}

	apiKey := extractField(line, ApiKeyPattern)
	clientId := extractField(line, ClientIdPattern)

	var topic, ipAddress, auth, principal string

	// Use different patterns based on API key
	switch apiKey {
	case "FETCH":
		topic = extractField(line, ConsumerTopicPattern)
		ipAddress = extractField(line, ConsumerIpPattern)
		auth = extractField(line, ConsumerAuthPattern)
		principal = extractField(line, ConsumerPrincipalPattern)

		// if internal broker thread we don't have information
		if BrokerFetcherPattern.MatchString(clientId) {
			topic = "n/a"
			ipAddress = "n/a"
			auth = "n/a"
			principal = "n/a"
		}
	case "PRODUCE":
		topic = extractField(line, ProducerTopicPattern)
		ipAddress = extractField(line, ProducerIpPattern)
		auth = extractField(line, ProducerAuthPattern)
		principal = extractField(line, ProducerPrincipalPattern)

	default:
		slog.Debug("unknown API key", "apiKey", apiKey)
	}

	apiRequest := ApiRequest{
		Timestamp:  timestamp,
		ApiKey:     apiKey,
		ClientId:   clientId,
		Topic:      topic, // Topic might be empty for some API calls
		IPAddress:  ipAddress,
		Auth:       auth,
		Principal:  principal,
		FileName:   fileName,
		LineNumber: lineNumber,
		LogLine:    line,
	}

	return apiRequest, nil
}
