package osk

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	kafkatypes "github.com/aws/aws-sdk-go-v2/service/kafka/types"
	"github.com/confluentinc/kcp/internal/client"
	kafkaservice "github.com/confluentinc/kcp/internal/services/kafka"
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
func (s *OSKSource) Type() types.SourceType {
	return types.SourceTypeOSK
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
		SourceType: types.SourceTypeOSK,
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
		if clusterResult == nil {
			// Cluster was intentionally skipped (all auth methods disabled)
			continue
		}

		result.Clusters = append(result.Clusters, *clusterResult)
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
	// Skip clusters with all auth methods disabled
	enabledMethods := clusterCreds.GetAuthMethods()
	if len(enabledMethods) == 0 {
		slog.Info("skipping disabled cluster (all auth methods set to use: false)",
			"cluster", clusterCreds.ID)
		return nil, nil
	}

	// Get the selected auth type
	authType, err := clusterCreds.GetSelectedAuthType()
	if err != nil {
		return nil, fmt.Errorf("failed to determine auth type for cluster %s: %w", clusterCreds.ID, err)
	}

	slog.Info("starting Kafka Admin API scan for OSK cluster",
		"cluster", clusterCreds.ID,
		"auth_type", authType,
		"bootstrap_servers", clusterCreds.BootstrapServers)

	kafkaAdmin, err := s.createKafkaAdmin(clusterCreds, authType)
	if err != nil {
		return nil, fmt.Errorf("failed to create Kafka admin client: %w", err)
	}
	defer func() { _ = kafkaAdmin.Close() }()

	kafkaService := kafkaservice.NewKafkaService(kafkaAdmin, kafkaservice.KafkaServiceOpts{
		AuthType:   authType,
		ClusterArn: clusterCreds.ID,
		SkipTopics: opts.SkipTopics,
		SkipACLs:   opts.SkipACLs,
	})

	// OSK clusters are always provisioned (never serverless)
	kafkaAdminInfo, err := kafkaService.ScanKafkaResources(kafkatypes.ClusterTypeProvisioned)
	if err != nil {
		return nil, fmt.Errorf("failed to scan Kafka resources: %w", err)
	}

	// Store the SASL mechanism used to connect (if applicable)
	if authType == types.AuthTypeSASLSCRAM && clusterCreds.AuthMethod.SASLScram != nil {
		kafkaAdminInfo.SaslMechanism = types.NormalizeSaslMechanism(clusterCreds.AuthMethod.SASLScram.Mechanism)
	}

	// Store the SASL mechanism for SASL/PLAIN
	if authType == types.AuthTypeSASLPlain {
		kafkaAdminInfo.SaslMechanism = "PLAIN"
	}

	metadata := types.OSKClusterMetadata{
		Environment: clusterCreds.Metadata.Environment,
		Location:    clusterCreds.Metadata.Location,
		Labels:      clusterCreds.Metadata.Labels,
		LastScanned: time.Now(),
	}

	bootstrapServers := clusterCreds.BootstrapServers
	if len(kafkaAdminInfo.DiscoveredBrokers) > 0 {
		bootstrapServers = kafkaAdminInfo.DiscoveredBrokers
	}

	return &sources.ClusterScanResult{
		Identifier: sources.ClusterIdentifier{
			Name:             clusterCreds.ID,
			UniqueID:         clusterCreds.ID,
			BootstrapServers: bootstrapServers,
		},
		KafkaAdminInfo:     kafkaAdminInfo,
		SourceSpecificData: metadata,
	}, nil
}

// createKafkaAdmin creates a Kafka Admin client for the OSK cluster
func (s *OSKSource) createKafkaAdmin(clusterCreds types.OSKClusterAuth, authType types.AuthType) (client.KafkaAdmin, error) {
	// OSK clusters don't have AWS-specific encryption settings, so we default to TLS
	// For unauthenticated plaintext, the client will handle disabling TLS
	clientBrokerEncryptionInTransit := kafkatypes.ClientBrokerTls

	// Default Kafka version for OSK clusters (can be overridden if needed)
	kafkaVersion := "3.6.0"

	// Region is not applicable for OSK, use empty string
	region := ""

	// Create admin client with appropriate auth options
	var kafkaAdmin client.KafkaAdmin
	var err error

	switch authType {
	case types.AuthTypeSASLSCRAM:
		kafkaAdmin, err = client.NewKafkaAdmin(
			clusterCreds.BootstrapServers,
			clientBrokerEncryptionInTransit,
			region,
			kafkaVersion,
			client.WithSASLSCRAMAuth(
				clusterCreds.AuthMethod.SASLScram.Username,
				clusterCreds.AuthMethod.SASLScram.Password,
				clusterCreds.AuthMethod.SASLScram.Mechanism,
				clusterCreds.InsecureSkipTLSVerify,
			),
		)
	case types.AuthTypeSASLPlain:
		kafkaAdmin, err = client.NewKafkaAdmin(
			clusterCreds.BootstrapServers,
			kafkatypes.ClientBrokerPlaintext,
			region,
			kafkaVersion,
			client.WithSASLPlainAuthNoTLS(
				clusterCreds.AuthMethod.SASLPlain.Username,
				clusterCreds.AuthMethod.SASLPlain.Password,
			),
		)
	case types.AuthTypeUnauthenticatedTLS:
		kafkaAdmin, err = client.NewKafkaAdmin(
			clusterCreds.BootstrapServers,
			clientBrokerEncryptionInTransit,
			region,
			kafkaVersion,
			client.WithUnauthenticatedTlsAuth(),
		)
	case types.AuthTypeUnauthenticatedPlaintext:
		kafkaAdmin, err = client.NewKafkaAdmin(
			clusterCreds.BootstrapServers,
			kafkatypes.ClientBrokerPlaintext,
			region,
			kafkaVersion,
			client.WithUnauthenticatedPlaintextAuth(),
		)
	case types.AuthTypeTLS:
		kafkaAdmin, err = client.NewKafkaAdmin(
			clusterCreds.BootstrapServers,
			clientBrokerEncryptionInTransit,
			region,
			kafkaVersion,
			client.WithTLSAuth(
				clusterCreds.AuthMethod.TLS.CACert,
				clusterCreds.AuthMethod.TLS.ClientCert,
				clusterCreds.AuthMethod.TLS.ClientKey,
			),
		)
	default:
		return nil, fmt.Errorf("unsupported auth type for OSK: %v", authType)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to create Kafka admin client: %w", err)
	}

	return kafkaAdmin, nil
}
