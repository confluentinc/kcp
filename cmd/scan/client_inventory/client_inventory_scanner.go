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
	s3Service            S3Service
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

func NewClientInventoryScanner(s3Service S3Service, state types.State, opts ClientInventoryScannerOpts) (*ClientInventoryScanner, error) {
	return &ClientInventoryScanner{
		s3Service:            s3Service,
		kafkaTraceLineParser: &KafkaApiTraceLineParser{},
		state:                state,
		opts:                 opts,
	}, nil
}

func (cis *ClientInventoryScanner) Run() error {
	slog.Info("🚀 starting client inventory scan", "s3_uri", cis.opts.S3Uri)

	ctx := context.Background()

	bucket, prefix, err := cis.s3Service.ParseS3URI(cis.opts.S3Uri)
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

	discoveredClients := cis.handleLogFiles(ctx, bucket, logFiles)

	if err := cis.state.UpsertDiscoveredClients(cis.opts.Region, cis.opts.ClusterName, discoveredClients); err != nil {
		return fmt.Errorf("failed to upsert discovered clients: %w", err)
	}

	if err := cis.state.PersistStateFile(cis.opts.StateFile); err != nil {
		return fmt.Errorf("failed to persist state file: %w", err)
	}

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

// func (cis *ClientInventoryScanner) addDiscoveredClientsToState(discoveredClients []types.DiscoveredClient) {
// 	slog.Info("🔍 looking for region and cluster in state file", "region", cis.opts.Region, "cluster_name", cis.opts.ClusterName)
// 	for i := range cis.state.Regions {
// 		region := &cis.state.Regions[i]
// 		if region.Name == cis.opts.Region {
// 			for j := range region.Clusters {
// 				cluster := &region.Clusters[j]
// 				if cluster.Name == cis.opts.ClusterName {
// 					// Merge existing clients from state with newly discovered clients
// 					allClients := append(cluster.DiscoveredClients, discoveredClients...)
// 					cluster.DiscoveredClients = dedupDiscoveredClients(allClients)
// 					break
// 				}
// 			}
// 		}
// 	}
// }

// func dedupDiscoveredClients(discoveredClients []types.DiscoveredClient) []types.DiscoveredClient {
// 	// Deduplicate by composite key, keeping the client with the most recent timestamp
// 	clientsByCompositeKey := make(map[string]types.DiscoveredClient)

// 	for _, currentClient := range discoveredClients {
// 		existingClient, exists := clientsByCompositeKey[currentClient.CompositeKey]

// 		if !exists || currentClient.Timestamp.After(existingClient.Timestamp) {
// 			clientsByCompositeKey[currentClient.CompositeKey] = currentClient
// 		}
// 	}

// 	dedupedClients := make([]types.DiscoveredClient, 0, len(clientsByCompositeKey))
// 	for _, client := range clientsByCompositeKey {
// 		dedupedClients = append(dedupedClients, client)
// 	}

// 	return dedupedClients
// }
