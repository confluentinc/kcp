package client_inventory

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"github.com/confluentinc/kcp/internal/types"
)

// maxLogLineBytes bounds a single broker log line; CloudWatch caps events at 1 MiB.
const maxLogLineBytes = 16 * 1024 * 1024

var (
	// lines that match this pattern will be parsed by kafka trace line parser
	KafkaApiTracePattern = regexp.MustCompile(`^\[.*\] TRACE \[KafkaApi-\d+\].*\(kafka\.server\.KafkaApis\)$`)
)

type ClientInventoryScannerOpts struct {
	S3Uri       string
	Region      string
	ClusterName string
	StateFile   string

	// CloudWatch-mode fields (empty/zero in S3 mode).
	LogGroupName string
	StartTime    time.Time
	EndTime      time.Time
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
	fmt.Printf("Starting client inventory scan for %s\n", cis.source.Name())
	slog.Info("scanning client inventory", "source", cis.source.Name(), "region", cis.opts.Region, "cluster", cis.opts.ClusterName)

	ctx := context.Background()

	chunks, err := cis.source.ListChunks(ctx)
	if err != nil {
		return fmt.Errorf("failed to list log chunks: %w", err)
	}
	slog.Info("found log files", "count", len(chunks))

	if len(chunks) == 0 {
		fmt.Printf("  No log files found to process\n")
		slog.Info("no log files found; skipping")
		return nil
	}

	discoveredClients, chunkErrs := cis.handleLogFiles(ctx, chunks)

	// A chunk that could not be read is a failed scan, not an empty one. Without this a wrong
	// region, a missing log group or a window outside retention would look like a cluster
	// with no clients.
	var failure error
	if len(chunkErrs) > 0 {
		failure = fmt.Errorf("%d of %d log chunks failed to read: %w", len(chunkErrs), len(chunks), errors.Join(chunkErrs...))
	}

	if len(discoveredClients) == 0 {
		if failure != nil {
			return failure
		}
		fmt.Printf("  No clients discovered; leaving state unchanged\n")
		slog.Info("no clients discovered; skipping state update")
		return nil
	}

	// Clients from the chunks that did read are still saved (the upsert only adds), then the
	// run reports the failure so the exit code is non-zero.
	if err := cis.state.UpsertDiscoveredClients(cis.opts.Region, cis.opts.ClusterName, discoveredClients); err != nil {
		return fmt.Errorf("failed to upsert discovered clients: %w", err)
	}

	if err := cis.state.PersistStateFile(cis.opts.StateFile); err != nil {
		return fmt.Errorf("failed to persist state file: %w", err)
	}

	slog.Info("client inventory scan complete", "region", cis.opts.Region, "cluster", cis.opts.ClusterName, "discovered_clients", len(discoveredClients))
	return failure
}

// handleLogFiles reads every chunk and returns the de-duplicated clients plus one error per chunk
// that could not be read. A failing chunk does not stop the others.
func (cis *ClientInventoryScanner) handleLogFiles(ctx context.Context, chunks []LogChunk) ([]types.DiscoveredClient, []error) {
	requestMetadataByCompositeKey := make(map[string]*RequestMetadata)
	var chunkErrs []error

	for _, chunk := range chunks {
		requestsMetadata, err := cis.handleLogFile(ctx, chunk)
		if err != nil {
			slog.Error("failed to extract API requests", "file", chunk.ID, "error", err)
			chunkErrs = append(chunkErrs, fmt.Errorf("%s: %w", chunk.ID, err))
			continue
		}

		fmt.Printf("  Parsed log file %s: found %d matching log lines\n", chunk.ID, len(requestsMetadata))
		slog.Debug("parsed log file", "file", chunk.ID, "matching_lines", len(requestsMetadata))

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

	return discoveredClients, chunkErrs
}

func (cis *ClientInventoryScanner) handleLogFile(ctx context.Context, chunk LogChunk) ([]RequestMetadata, error) {
	content, err := cis.source.ReadChunk(ctx, chunk)
	if err != nil {
		return nil, fmt.Errorf("failed to read log chunk: %w", err)
	}

	var requestsMetadata []RequestMetadata
	scanner := bufio.NewScanner(strings.NewReader(string(content)))
	// MSK truncates very large TRACE response lines at exactly 64 KiB, which with the
	// trailing newline overruns bufio's default token limit and would abort the whole chunk.
	scanner.Buffer(make([]byte, 0, 64*1024), maxLogLineBytes)
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
