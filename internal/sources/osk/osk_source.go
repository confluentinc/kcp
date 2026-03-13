package osk

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	kafkatypes "github.com/aws/aws-sdk-go-v2/service/kafka/types"
	"github.com/confluentinc/kcp/internal/client"
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

	// Create Kafka Admin client using the same pattern as MSK scanner
	kafkaAdmin, err := s.createKafkaAdmin(clusterCreds, authType)
	if err != nil {
		return nil, fmt.Errorf("failed to create Kafka admin client: %w", err)
	}
	defer kafkaAdmin.Close()

	// Scan Kafka resources
	kafkaAdminInfo, err := s.scanKafkaResources(kafkaAdmin, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to scan Kafka resources: %w", err)
	}

	// Populate metadata
	metadata := types.OSKClusterMetadata{
		Environment: clusterCreds.Metadata.Environment,
		Location:    clusterCreds.Metadata.Location,
		Labels:      clusterCreds.Metadata.Labels,
		LastScanned: time.Now(),
	}

	slog.Info("successfully scanned OSK cluster",
		"cluster", clusterCreds.ID,
		"cluster_id", kafkaAdminInfo.ClusterID,
		"topics", len(kafkaAdminInfo.Topics.Details),
		"acls", len(kafkaAdminInfo.Acls))

	return &sources.ClusterScanResult{
		Identifier: sources.ClusterIdentifier{
			Name:             clusterCreds.ID,
			UniqueID:         clusterCreds.ID,
			BootstrapServers: clusterCreds.BootstrapServers,
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

// scanKafkaResources scans Kafka topics and ACLs using the Kafka Admin API
func (s *OSKSource) scanKafkaResources(kafkaAdmin client.KafkaAdmin, opts sources.ScanOptions) (*types.KafkaAdminClientInformation, error) {
	kafkaAdminInfo := &types.KafkaAdminClientInformation{}

	// Get cluster metadata including cluster ID
	clusterMetadata, err := kafkaAdmin.GetClusterKafkaMetadata()
	if err != nil {
		return nil, fmt.Errorf("failed to get cluster metadata: %w", err)
	}
	kafkaAdminInfo.ClusterID = clusterMetadata.ClusterID

	// Scan topics unless skipped
	if !opts.SkipTopics {
		topics, err := s.scanTopics(kafkaAdmin)
		if err != nil {
			return nil, fmt.Errorf("failed to scan topics: %w", err)
		}
		kafkaAdminInfo.SetTopics(topics)
	}

	// Scan ACLs unless skipped
	if !opts.SkipACLs {
		acls, err := s.scanACLs(kafkaAdmin)
		if err != nil {
			// Log warning but continue - ACLs might not be accessible
			slog.Warn("failed to scan ACLs, continuing without ACL data", "error", err)
			kafkaAdminInfo.Acls = []types.Acls{}
		} else {
			kafkaAdminInfo.Acls = acls
		}
	}

	// Scan for self-managed connectors
	if !opts.SkipTopics && kafkaAdminInfo.Topics != nil {
		connectors, err := s.scanSelfManagedConnectors(kafkaAdmin, kafkaAdminInfo.Topics.Details)
		if err != nil {
			slog.Warn("failed to scan self-managed connectors", "error", err)
		} else {
			kafkaAdminInfo.SetSelfManagedConnectors(connectors)
		}
	}

	return kafkaAdminInfo, nil
}

// scanTopics scans for topics in the Kafka cluster
func (s *OSKSource) scanTopics(kafkaAdmin client.KafkaAdmin) ([]types.TopicDetails, error) {
	slog.Info("scanning for cluster topics")

	topics, err := kafkaAdmin.ListTopicsWithConfigs()
	if err != nil {
		return nil, fmt.Errorf("failed to list topics with configs: %w", err)
	}

	slog.Info("found topics", "count", len(topics))

	var topicDetails []types.TopicDetails
	for topicName, topic := range topics {
		configurations := make(map[string]*string)
		for key, valuePtr := range topic.ConfigEntries {
			if valuePtr != nil {
				configurations[key] = valuePtr
			}
		}

		topicDetails = append(topicDetails, types.TopicDetails{
			Name:              topicName,
			Partitions:        int(topic.NumPartitions),
			ReplicationFactor: int(topic.ReplicationFactor),
			Configurations:    configurations,
		})
	}

	return topicDetails, nil
}

// scanACLs scans for Kafka ACLs in the cluster
func (s *OSKSource) scanACLs(kafkaAdmin client.KafkaAdmin) ([]types.Acls, error) {
	slog.Info("scanning for kafka acls")

	acls, err := kafkaAdmin.ListAcls()
	if err != nil {
		return nil, fmt.Errorf("failed to list acls: %w", err)
	}

	// Flatten the ACLs for easier processing
	var flattenedAcls []types.Acls
	for _, resourceAcl := range acls {
		for _, acl := range resourceAcl.Acls {
			flattenedAcl := types.Acls{
				ResourceType:        resourceAcl.ResourceType.String(),
				ResourceName:        resourceAcl.ResourceName,
				ResourcePatternType: resourceAcl.ResourcePatternType.String(),
				Principal:           acl.Principal,
				Host:                acl.Host,
				Operation:           acl.Operation.String(),
				PermissionType:      acl.PermissionType.String(),
			}
			flattenedAcls = append(flattenedAcls, flattenedAcl)
		}
	}

	return flattenedAcls, nil
}

// scanSelfManagedConnectors scans for self-managed Kafka Connect connectors
func (s *OSKSource) scanSelfManagedConnectors(kafkaAdmin client.KafkaAdmin, topics []types.TopicDetails) ([]types.SelfManagedConnector, error) {
	const (
		configTopicName = "connect-configs"
		statusTopicName = "connect-status"
		keyPrefix       = "connector-"
	)

	// Check if connect-configs topic exists
	existingTopics := make(map[string]bool)
	for _, topic := range topics {
		existingTopics[topic.Name] = true
	}

	if !existingTopics[configTopicName] {
		slog.Debug("skipping connector scan, connect-configs topic does not exist")
		return []types.SelfManagedConnector{}, nil
	}

	slog.Info("found connect-configs topic, attempting to read connector configurations")
	connectorConfigs, err := kafkaAdmin.GetAllMessagesWithKeyFilter(configTopicName, keyPrefix)
	if err != nil {
		return nil, fmt.Errorf("failed to read connector configurations: %w", err)
	}

	var connectorStatuses map[string]string
	if existingTopics[statusTopicName] {
		slog.Info("found connect-status topic, attempting to read connector status information")
		connectorStatuses, err = kafkaAdmin.GetConnectorStatusMessages(statusTopicName)
		if err != nil {
			slog.Warn("failed to read connector status information", "error", err)
			connectorStatuses = make(map[string]string)
		}
	} else {
		connectorStatuses = make(map[string]string)
	}

	// Parse connector configurations (same logic as kafka_service.go)
	connectors := []types.SelfManagedConnector{}
	for connectorKey, configJSON := range connectorConfigs {
		var rawConfig map[string]any
		if err := json.Unmarshal([]byte(configJSON), &rawConfig); err != nil {
			slog.Warn("failed to parse connector config JSON", "connector", connectorKey, "error", err)
			continue
		}

		var configMap map[string]any
		if properties, ok := rawConfig["properties"].(map[string]any); ok {
			configMap = properties
		}

		// Remove the "connector-" prefix from the key to get the connector name
		connectorName := connectorKey
		if len(connectorKey) > len(keyPrefix) && connectorKey[:len(keyPrefix)] == keyPrefix {
			connectorName = connectorKey[len(keyPrefix):]
		}

		connector := types.SelfManagedConnector{
			Name:   connectorName,
			Config: configMap,
		}

		// Try to retrieve connector status
		if statusJSON, exists := connectorStatuses[connectorName]; exists {
			var statusMap map[string]any
			if err := json.Unmarshal([]byte(statusJSON), &statusMap); err != nil {
				slog.Warn("failed to parse connector status JSON", "connector", connectorName, "error", err)
			} else {
				if state, ok := statusMap["state"].(string); ok {
					connector.State = state
				}
				if connectHost, ok := statusMap["worker_id"].(string); ok {
					connector.ConnectHost = connectHost
				}
			}
		}

		connectors = append(connectors, connector)
	}

	slog.Info("successfully read connector configurations", "count", len(connectors))
	return connectors, nil
}
