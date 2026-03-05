package osk

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/confluentinc/kcp/internal/sources"
	"github.com/confluentinc/kcp/internal/types"
)

// OSKSource implements the Source interface for Open Source Kafka clusters
type OSKSource struct {
	credentials *types.OSKCredentials
}

// NewOSKSource creates a new OSK source
func NewOSKSource() *OSKSource {
	return &OSKSource{}
}

// Type returns the source type
func (s *OSKSource) Type() sources.SourceType {
	return sources.SourceTypeOSK
}

// LoadCredentials loads OSK credentials from a file
func (s *OSKSource) LoadCredentials(credentialsPath string) error {
	creds, errs := types.NewOSKCredentialsFromFile(credentialsPath)
	if len(errs) > 0 {
		return fmt.Errorf("failed to load OSK credentials: %v", errs)
	}
	s.credentials = creds
	slog.Info("loaded OSK credentials", "clusters", len(creds.Clusters))
	return nil
}

// GetClusters returns the list of clusters from credentials
func (s *OSKSource) GetClusters() []sources.ClusterIdentifier {
	if s.credentials == nil {
		return nil
	}

	clusters := make([]sources.ClusterIdentifier, len(s.credentials.Clusters))
	for i, cluster := range s.credentials.Clusters {
		clusters[i] = sources.ClusterIdentifier{
			Name:             cluster.ID, // OSK uses ID as name
			UniqueID:         cluster.ID,
			BootstrapServers: cluster.BootstrapServers,
		}
	}
	return clusters
}

// Scan performs scanning of all OSK clusters
func (s *OSKSource) Scan(ctx context.Context, opts sources.ScanOptions) (*sources.ScanResult, error) {
	if s.credentials == nil {
		return nil, fmt.Errorf("credentials not loaded")
	}

	slog.Info("starting OSK cluster scan", "clusters", len(s.credentials.Clusters))

	result := &sources.ScanResult{
		SourceType: sources.SourceTypeOSK,
		Clusters:   make([]sources.ClusterScanResult, 0),
	}

	var scanErrors []error

	for _, clusterCreds := range s.credentials.Clusters {
		slog.Info("scanning OSK cluster", "id", clusterCreds.ID)

		clusterResult, err := s.scanCluster(ctx, clusterCreds, opts)
		if err != nil {
			// Log error but continue with other clusters
			slog.Error("failed to scan OSK cluster",
				"id", clusterCreds.ID,
				"error", err)
			scanErrors = append(scanErrors, fmt.Errorf("cluster '%s': %w",
				clusterCreds.ID, err))
			continue
		}

		result.Clusters = append(result.Clusters, *clusterResult)
		slog.Info("successfully scanned OSK cluster",
			"id", clusterCreds.ID,
			"topics", len(clusterResult.KafkaAdminInfo.Topics.Details),
			"acls", len(clusterResult.KafkaAdminInfo.Acls))
	}

	// If ALL clusters failed, return error
	if len(result.Clusters) == 0 && len(scanErrors) > 0 {
		return nil, fmt.Errorf("failed to scan any clusters: %v", scanErrors)
	}

	// If SOME clusters failed, log warnings but return partial results
	if len(scanErrors) > 0 {
		slog.Warn("some clusters failed to scan",
			"failed", len(scanErrors),
			"succeeded", len(result.Clusters))
	}

	return result, nil
}

// scanCluster scans a single OSK cluster using Kafka Admin API
func (s *OSKSource) scanCluster(ctx context.Context, clusterCreds types.OSKClusterAuth, opts sources.ScanOptions) (*sources.ClusterScanResult, error) {
	// TODO: Implement Kafka Admin API scanning
	// This will be implemented in next task

	// For now, return stub
	metadata := types.OSKClusterMetadata{
		Environment: clusterCreds.Metadata.Environment,
		Location:    clusterCreds.Metadata.Location,
		Labels:      clusterCreds.Metadata.Labels,
		LastScanned: time.Now(),
	}

	return &sources.ClusterScanResult{
		Identifier: sources.ClusterIdentifier{
			Name:             clusterCreds.ID,
			UniqueID:         clusterCreds.ID,
			BootstrapServers: clusterCreds.BootstrapServers,
		},
		KafkaAdminInfo:     &types.KafkaAdminClientInformation{},
		SourceSpecificData: metadata,
	}, nil
}
