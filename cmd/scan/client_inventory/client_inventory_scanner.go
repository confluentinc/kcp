package client_inventory

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"github.com/confluentinc/kcp/internal/types"
)

var (
	// lines that match this pattern will be parsed by kafka trace line parser
	KafkaApiTracePattern = regexp.MustCompile(`^\[.*\] TRACE \[KafkaApi-\d+\].*\(kafka\.server\.KafkaApis\)$`)
)

type ClientInventoryScannerOpts struct {
	S3Uri       string
	Region      string
	ClusterName string
	StateFile   string
}

type ClientInventoryScanner struct {
	source               LogSource
	kafkaTraceLineParser *KafkaApiTraceLineParser
	state                types.State
	opts                 ClientInventoryScannerOpts
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

func NewClientInventoryScanner(source LogSource, state types.State, opts ClientInventoryScannerOpts) (*ClientInventoryScanner, error) {
	return &ClientInventoryScanner{
		source:               source,
		kafkaTraceLineParser: &KafkaApiTraceLineParser{},
		state:                state,
		opts:                 opts,
	}, nil
}

func (cis *ClientInventoryScanner) Run() error {
	fmt.Printf("🚀 Starting client inventory scan for %s\n", cis.source.Name())
	slog.Info("🔍 scanning client inventory", "source", cis.source.Name(), "region", cis.opts.Region, "cluster", cis.opts.ClusterName)

	ctx := context.Background()

	chunks, err := cis.source.ListChunks(ctx)
	if err != nil {
		return fmt.Errorf("failed to list log chunks: %w", err)
	}
	slog.Info("🔍 found log files", "count", len(chunks))

	if len(chunks) == 0 {
		fmt.Printf("  ⏭️  No log files found to process\n")
		slog.Info("⏭️ no log files found; skipping")
		return nil
	}

	discoveredClients := cis.handleLogFiles(ctx, chunks)

	if err := cis.state.UpsertDiscoveredClients(cis.opts.Region, cis.opts.ClusterName, discoveredClients); err != nil {
		return fmt.Errorf("failed to upsert discovered clients: %w", err)
	}

	if err := cis.state.PersistStateFile(cis.opts.StateFile); err != nil {
		return fmt.Errorf("failed to persist state file: %w", err)
	}

	slog.Info("✅ client inventory scan complete", "region", cis.opts.Region, "cluster", cis.opts.ClusterName, "discovered_clients", len(discoveredClients))
	return nil
}

func (cis *ClientInventoryScanner) handleLogFiles(ctx context.Context, chunks []LogChunk) []types.DiscoveredClient {
	requestMetadataByCompositeKey := make(map[string]*RequestMetadata)

	for _, chunk := range chunks {
		requestsMetadata, err := cis.handleLogFile(ctx, chunk)
		if err != nil {
			slog.Error("failed to extract API requests", "file", chunk.ID, "error", err)
			continue
		}

		fmt.Printf("  🔍 Parsed log file %s: found %d matching log lines\n", chunk.ID, len(requestsMetadata))
		slog.Debug("🔍 parsed log file", "file", chunk.ID, "matching_lines", len(requestsMetadata))

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

func (cis *ClientInventoryScanner) handleLogFile(ctx context.Context, chunk LogChunk) ([]RequestMetadata, error) {
	content, err := cis.source.ReadChunk(ctx, chunk)
	if err != nil {
		return nil, fmt.Errorf("failed to read log chunk: %w", err)
	}

	var requestsMetadata []RequestMetadata
	scanner := bufio.NewScanner(strings.NewReader(string(content)))
	lineNumber := 0

	for scanner.Scan() {
		line := scanner.Text()

		switch {
		case KafkaApiTracePattern.MatchString(line):
			metadata, err := cis.kafkaTraceLineParser.Parse(line, lineNumber, chunk.ID)
			if err != nil {
				// slog.Debug("failed to parse Kafka API line", "line", line, "error", err)
				continue
			}
			requestsMetadata = append(requestsMetadata, *metadata)
		default:
			continue
		}

		lineNumber++
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error reading file content: %w", err)
	}

	return requestsMetadata, nil
}
