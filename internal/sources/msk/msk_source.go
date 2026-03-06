package msk

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/confluentinc/kcp/internal/sources"
	"github.com/confluentinc/kcp/internal/types"
)

// MSKSource implements the Source interface for AWS MSK clusters
type MSKSource struct {
	credentials *types.Credentials
	state       *types.State
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

// Scan performs scanning of MSK clusters
// This is a wrapper that will delegate to existing MSK scanning logic
func (s *MSKSource) Scan(ctx context.Context, opts sources.ScanOptions) (*sources.ScanResult, error) {
	if s.credentials == nil {
		return nil, fmt.Errorf("credentials not loaded")
	}

	slog.Info("starting MSK cluster scan")

	// TODO: Delegate to existing MSK scanner in cmd/scan/clusters
	// This will be implemented when we update the scan clusters command

	result := &sources.ScanResult{
		SourceType: types.SourceTypeMSK,
		Clusters:   make([]sources.ClusterScanResult, 0),
	}

	return result, nil
}

// SetState sets the existing state (for incremental scans)
func (s *MSKSource) SetState(state *types.State) {
	s.state = state
}
