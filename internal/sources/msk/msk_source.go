package msk

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/confluentinc/kcp/internal/services/msk_scanner"
	"github.com/confluentinc/kcp/internal/sources"
	"github.com/confluentinc/kcp/internal/types"
)

// MSKSource implements the Source interface for AWS MSK clusters
type MSKSource struct {
	credentials *types.Credentials
}

// NewMSKSource creates a new MSK source
func NewMSKSource() *MSKSource {
	return &MSKSource{}
}

// Type returns the source type
func (s *MSKSource) Type() types.SourceType {
	return types.SourceTypeMSK
}

// LoadCredentials loads MSK credentials from a file
func (s *MSKSource) LoadCredentials(credentialsPath string) error {
	creds, errs := types.NewCredentialsFromFile(credentialsPath)
	if len(errs) > 0 {
		return fmt.Errorf("failed to load MSK credentials: %v", errs)
	}
	s.credentials = creds
	slog.Info("loaded MSK credentials", "regions", len(creds.Regions))
	return nil
}

// GetClusters returns the list of MSK clusters from credentials
func (s *MSKSource) GetClusters() []sources.ClusterIdentifier {
	if s.credentials == nil {
		return nil
	}

	var clusters []sources.ClusterIdentifier
	for _, region := range s.credentials.Regions {
		for _, cluster := range region.Clusters {
			clusters = append(clusters, sources.ClusterIdentifier{
				Name:             cluster.Name,
				UniqueID:         cluster.Arn,
				BootstrapServers: nil, // Populated from state during scan
			})
		}
	}
	return clusters
}

// Scan performs scanning of MSK clusters by delegating to ClustersScanner.
// opts.State must be non-nil — it contains broker addresses populated by kcp discover.
func (s *MSKSource) Scan(ctx context.Context, opts sources.ScanOptions) (*sources.ScanResult, error) {
	if s.credentials == nil {
		return nil, fmt.Errorf("credentials not loaded")
	}
	if opts.State == nil {
		return nil, fmt.Errorf("state is required for MSK scanning; run 'kcp discover' first")
	}

	slog.Info("starting MSK cluster scan")

	scanner := msk_scanner.NewClustersScanner(msk_scanner.ClustersScannerOpts{
		State:       *opts.State,
		Credentials: *s.credentials,
	})

	if err := scanner.ScanOnly(); err != nil {
		return nil, fmt.Errorf("MSK scan failed: %w", err)
	}

	// Translate scanner results into ScanResult
	result := &sources.ScanResult{
		SourceType: types.SourceTypeMSK,
		Clusters:   make([]sources.ClusterScanResult, 0),
	}

	if scanner.State.MSKSources != nil {
		for _, region := range scanner.State.MSKSources.Regions {
			for i := range region.Clusters {
				cluster := &region.Clusters[i]
				// Only include clusters that were actually scanned
				if cluster.KafkaAdminClientInformation.ClusterID == "" {
					continue
				}
				kafkaInfo := cluster.KafkaAdminClientInformation
				result.Clusters = append(result.Clusters, sources.ClusterScanResult{
					Identifier: sources.ClusterIdentifier{
						Name:     cluster.Name,
						UniqueID: cluster.Arn,
					},
					KafkaAdminInfo:     &kafkaInfo,
					SourceSpecificData: cluster.AWSClientInformation,
				})
			}
		}
	}

	slog.Info("MSK scan complete", "scanned", len(result.Clusters))
	return result, nil
}
