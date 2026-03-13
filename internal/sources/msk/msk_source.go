package msk

import (
	"context"
	"fmt"
	"log/slog"

	kafkatypes "github.com/aws/aws-sdk-go-v2/service/kafka/types"
	"github.com/confluentinc/kcp/internal/client"
	kafkaservice "github.com/confluentinc/kcp/internal/services/kafka"
	"github.com/confluentinc/kcp/internal/sources"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/confluentinc/kcp/internal/utils"
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

// Scan performs scanning of all MSK clusters.
// opts.State must be non-nil — it contains broker addresses populated by kcp discover.
func (s *MSKSource) Scan(ctx context.Context, opts sources.ScanOptions) (*sources.ScanResult, error) {
	if s.credentials == nil {
		return nil, fmt.Errorf("credentials not loaded")
	}
	if opts.State == nil {
		return nil, fmt.Errorf("state is required for MSK scanning; run 'kcp discover' first")
	}

	slog.Info("starting MSK cluster scan")

	result := &sources.ScanResult{
		SourceType: types.SourceTypeMSK,
		Clusters:   make([]sources.ClusterScanResult, 0),
	}

	for _, regionAuth := range s.credentials.Regions {
		for _, clusterAuth := range regionAuth.Clusters {
			clusterResult, err := s.scanCluster(regionAuth.Name, clusterAuth, opts)
			if err != nil {
				slog.Info("skipping cluster", "cluster", clusterAuth.Name, "error", err)
				continue
			}
			result.Clusters = append(result.Clusters, *clusterResult)
		}
	}

	slog.Info("MSK scan complete", "scanned", len(result.Clusters))
	return result, nil
}

func (s *MSKSource) scanCluster(region string, clusterAuth types.ClusterAuth, opts sources.ScanOptions) (*sources.ClusterScanResult, error) {
	discoveredCluster, err := s.getClusterFromDiscovery(opts.State, region, clusterAuth.Arn)
	if err != nil {
		return nil, fmt.Errorf("failed to get cluster from discovery state: %v", err)
	}

	authType, err := clusterAuth.GetSelectedAuthType()
	if err != nil {
		return nil, fmt.Errorf("failed to determine auth type for cluster: %s in region: %s: %v", clusterAuth.Arn, region, err)
	}

	slog.Info(fmt.Sprintf("starting broker scan for %s using %s authentication", clusterAuth.Arn, authType))

	brokerAddresses, err := discoveredCluster.AWSClientInformation.GetBootstrapBrokersForAuthType(authType)
	if err != nil {
		return nil, fmt.Errorf("failed to get broker addresses for cluster: %s in region: %s: %v", clusterAuth.Arn, region, err)
	}

	clientBrokerEncryptionInTransit := utils.GetClientBrokerEncryptionInTransit(discoveredCluster.AWSClientInformation.MskClusterConfig)
	kafkaVersion := utils.GetKafkaVersion(discoveredCluster.AWSClientInformation)

	kafkaAdmin, err := createKafkaAdmin(authType, brokerAddresses, clientBrokerEncryptionInTransit, region, kafkaVersion, clusterAuth)
	if err != nil {
		return nil, fmt.Errorf("failed to create Kafka admin: %v", err)
	}

	ks := kafkaservice.NewKafkaService(*kafkaAdmin, kafkaservice.KafkaServiceOpts{
		AuthType:   authType,
		ClusterArn: clusterAuth.Arn,
		SkipTopics: opts.SkipTopics,
		SkipACLs:   opts.SkipACLs,
	})

	clusterType := discoveredCluster.AWSClientInformation.MskClusterConfig.ClusterType
	kafkaAdminInfo, err := ks.ScanKafkaResources(clusterType)
	if err != nil {
		return nil, fmt.Errorf("failed to scan Kafka resources: %v", err)
	}

	slog.Info(fmt.Sprintf("broker scan complete for %s", clusterAuth.Arn))

	return &sources.ClusterScanResult{
		Identifier: sources.ClusterIdentifier{
			Name:     discoveredCluster.Name,
			UniqueID: clusterAuth.Arn,
		},
		KafkaAdminInfo:     kafkaAdminInfo,
		SourceSpecificData: discoveredCluster.AWSClientInformation,
	}, nil
}

func (s *MSKSource) getClusterFromDiscovery(state *types.State, region, clusterArn string) (*types.DiscoveredCluster, error) {
	if state.MSKSources == nil {
		return nil, fmt.Errorf("no MSK sources found in state file")
	}
	for i, currentRegion := range state.MSKSources.Regions {
		if currentRegion.Name == region {
			for j, currentCluster := range currentRegion.Clusters {
				if currentCluster.Arn == clusterArn {
					return &state.MSKSources.Regions[i].Clusters[j], nil
				}
			}
		}
	}
	return nil, fmt.Errorf("cluster %s not found in region %s", clusterArn, region)
}

func createKafkaAdmin(authType types.AuthType, brokerAddresses []string, clientBrokerEncryptionInTransit kafkatypes.ClientBroker, region string, kafkaVersion string, clusterAuth types.ClusterAuth) (*client.KafkaAdmin, error) {
	var kafkaAdmin client.KafkaAdmin
	var err error
	switch authType {
	case types.AuthTypeIAM:
		kafkaAdmin, err = client.NewKafkaAdmin(brokerAddresses, clientBrokerEncryptionInTransit, region, kafkaVersion, client.WithIAMAuth())
	case types.AuthTypeSASLSCRAM:
		kafkaAdmin, err = client.NewKafkaAdmin(brokerAddresses, clientBrokerEncryptionInTransit, region, kafkaVersion, client.WithSASLSCRAMAuth(
			clusterAuth.AuthMethod.SASLScram.Username,
			clusterAuth.AuthMethod.SASLScram.Password,
			clusterAuth.AuthMethod.SASLScram.Mechanism,
		))
	case types.AuthTypeUnauthenticatedTLS:
		kafkaAdmin, err = client.NewKafkaAdmin(brokerAddresses, clientBrokerEncryptionInTransit, region, kafkaVersion, client.WithUnauthenticatedTlsAuth())
	case types.AuthTypeUnauthenticatedPlaintext:
		kafkaAdmin, err = client.NewKafkaAdmin(brokerAddresses, clientBrokerEncryptionInTransit, region, kafkaVersion, client.WithUnauthenticatedPlaintextAuth())
	case types.AuthTypeTLS:
		kafkaAdmin, err = client.NewKafkaAdmin(brokerAddresses, clientBrokerEncryptionInTransit, region, kafkaVersion, client.WithTLSAuth(clusterAuth.AuthMethod.TLS.CACert, clusterAuth.AuthMethod.TLS.ClientCert, clusterAuth.AuthMethod.TLS.ClientKey))
	default:
		return nil, fmt.Errorf("Auth type: %v not yet supported", authType)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to create Kafka admin: %v", err)
	}
	return &kafkaAdmin, nil
}
