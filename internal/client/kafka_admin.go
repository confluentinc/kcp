package client

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/aws/aws-msk-iam-sasl-signer-go/signer"
	kafkatypes "github.com/aws/aws-sdk-go-v2/service/kafka/types"
	"github.com/confluentinc/confluent-kafka-go/v2/kafka"
	"github.com/confluentinc/kcp/internal/types"
)

// AdminConfig holds the configuration for creating a Kafka admin client
type AdminConfig struct {
	authType        types.AuthType
	username        string
	password        string
	awsAccessKey    string
	awsAccessSecret string
	caCertFile      string
	clientCertFile  string
	clientKeyFile   string
}

// AdminOption is a function type for configuring the Kafka admin client
type AdminOption func(*AdminConfig)

// WithIAMAuth configures the admin client to use IAM authentication
func WithIAMAuth() AdminOption {
	return func(config *AdminConfig) {
		config.authType = types.AuthTypeIAM
	}
}

// WithSASLSCRAMAuth configures the admin client to use SASL/SCRAM authentication
func WithSASLSCRAMAuth(username, password string) AdminOption {
	return func(config *AdminConfig) {
		config.authType = types.AuthTypeSASLSCRAM
		config.username = username
		config.password = password
	}
}

func WithUnauthenticatedTlsAuth() AdminOption {
	return func(config *AdminConfig) {
		config.authType = types.AuthTypeUnauthenticatedTLS
	}
}

func WithUnauthenticatedPlaintextAuth() AdminOption {
	return func(config *AdminConfig) {
		config.authType = types.AuthTypeUnauthenticatedPlaintext
	}
}

func WithTLSAuth(caCertFile string, clientCertFile string, clientKeyFile string) AdminOption {
	return func(config *AdminConfig) {
		config.authType = types.AuthTypeTLS
		config.caCertFile = caCertFile
		config.clientCertFile = clientCertFile
		config.clientKeyFile = clientKeyFile
	}
}

// ClusterKafkaMetadata represents cluster information including brokers, controller, and cluster ID
type ClusterKafkaMetadata struct {
	Brokers      []types.BrokerInfo
	ControllerID int32
	ClusterID    string
}

// KafkaAdmin interface defines the Kafka admin operations we need
type KafkaAdmin interface {
	ListTopicsWithConfigs() ([]types.TopicDetails, error)
	GetClusterKafkaMetadata() (*ClusterKafkaMetadata, error)
	DescribeConfig() ([]types.BrokerConfigEntry, error)
	ListAcls() ([]types.Acls, error)
	GetAllMessagesWithKeyFilter(topicName string, keyPrefix string) (map[string]string, error)
	GetConnectorStatusMessages(topicName string) (map[string]string, error)
	Close() error
}

// KafkaAdminClient wraps confluent-kafka-go AdminClient to implement our KafkaAdmin interface
type KafkaAdminClient struct {
	admin           *kafka.AdminClient
	region          string
	config          AdminConfig
	configMap       kafka.ConfigMap
	brokerAddresses []string
}

// buildConfigMap creates a kafka.ConfigMap from broker addresses and admin config
func buildConfigMap(brokerAddresses []string, config AdminConfig) (kafka.ConfigMap, error) {
	bootstrapServers := strings.Join(brokerAddresses, ",")
	configMap := kafka.ConfigMap{
		"bootstrap.servers": bootstrapServers,
		"client.id":         "kcp-cli",
	}

	switch config.authType {
	case types.AuthTypeIAM:
		_ = configMap.SetKey("security.protocol", "SASL_SSL")
		_ = configMap.SetKey("sasl.mechanisms", "OAUTHBEARER")
	case types.AuthTypeSASLSCRAM:
		_ = configMap.SetKey("security.protocol", "SASL_SSL")
		_ = configMap.SetKey("sasl.mechanisms", "SCRAM-SHA-512")
		_ = configMap.SetKey("sasl.username", config.username)
		_ = configMap.SetKey("sasl.password", config.password)
	case types.AuthTypeUnauthenticatedTLS:
		_ = configMap.SetKey("security.protocol", "SSL")
	case types.AuthTypeUnauthenticatedPlaintext:
		_ = configMap.SetKey("security.protocol", "PLAINTEXT")
	case types.AuthTypeTLS:
		_ = configMap.SetKey("security.protocol", "SSL")
		_ = configMap.SetKey("ssl.ca.location", config.caCertFile)
		_ = configMap.SetKey("ssl.certificate.location", config.clientCertFile)
		_ = configMap.SetKey("ssl.key.location", config.clientKeyFile)
	default:
		return nil, fmt.Errorf("auth type: %v not yet supported", config.authType)
	}

	return configMap, nil
}

// handleOAuthTokenRefresh handles OAuthBearer token refresh for IAM auth.
// It runs as a goroutine, polling the admin client for token refresh events
// and generating new tokens using the AWS MSK IAM signer.
func handleOAuthTokenRefresh(adminClient *kafka.AdminClient, region string) {
	// AdminClient doesn't expose Poll() or Events() channels for OAuthBearerTokenRefresh events.
	// We proactively set the token and refresh it periodically.
	for {
		token, expirationTimeMs, err := signer.GenerateAuthToken(context.TODO(), region)
		if err != nil {
			_ = adminClient.SetOAuthBearerTokenFailure(err.Error())
			slog.Error("failed to generate IAM auth token", "error", err)
			time.Sleep(10 * time.Second)
			continue
		}
		oauthToken := kafka.OAuthBearerToken{
			TokenValue: token,
			Expiration: time.UnixMilli(expirationTimeMs),
		}
		err = adminClient.SetOAuthBearerToken(oauthToken)
		if err != nil {
			slog.Error("failed to set OAuthBearer token", "error", err)
		}

		// Refresh the token at 80% of its lifetime
		lifetime := time.Until(time.UnixMilli(expirationTimeMs))
		refreshIn := time.Duration(float64(lifetime) * 0.8)
		if refreshIn < 10*time.Second {
			refreshIn = 10 * time.Second
		}
		time.Sleep(refreshIn)
	}
}

// NewKafkaAdmin creates a new Kafka admin client for the given broker addresses and region
func NewKafkaAdmin(brokerAddresses []string, clientBrokerEncryptionInTransit kafkatypes.ClientBroker, region string, kafkaVersion string, opts ...AdminOption) (KafkaAdmin, error) {
	config := AdminConfig{
		authType: types.AuthTypeIAM,
	}
	for _, opt := range opts {
		opt(&config)
	}

	configMap, err := buildConfigMap(brokerAddresses, config)
	if err != nil {
		return nil, fmt.Errorf("❌ Failed to build config: %v", err)
	}

	admin, err := kafka.NewAdminClient(&configMap)
	if err != nil {
		return nil, fmt.Errorf("❌ Failed to create admin client: authType=%v brokerAddresses=%v error=%v", config.authType, brokerAddresses, err)
	}

	// Handle OAuthBearer token refresh for IAM auth
	if config.authType == types.AuthTypeIAM {
		go handleOAuthTokenRefresh(admin, region)
	}

	return &KafkaAdminClient{
		admin:           admin,
		region:          region,
		config:          config,
		configMap:       configMap,
		brokerAddresses: brokerAddresses,
	}, nil
}

func (k *KafkaAdminClient) ListTopicsWithConfigs() ([]types.TopicDetails, error) {
	// Get metadata for all topics
	metadata, err := k.admin.GetMetadata(nil, true, 15000)
	if err != nil {
		return nil, fmt.Errorf("failed to get metadata: %w", err)
	}

	if len(metadata.Topics) == 0 && len(metadata.Brokers) > 0 {
		slog.Warn("⚠️ no topics found in metadata response")
	}

	// Build ConfigResource list for DescribeConfigs
	var configResources []kafka.ConfigResource
	for topicName := range metadata.Topics {
		configResources = append(configResources, kafka.ConfigResource{
			Type: kafka.ResourceTopic,
			Name: topicName,
		})
	}

	// Describe configs for all topics
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	configResults, err := k.admin.DescribeConfigs(ctx, configResources)
	if err != nil {
		return nil, fmt.Errorf("failed to describe configs: %w", err)
	}

	// Build a map of topic name -> config entries
	topicConfigs := make(map[string]map[string]kafka.ConfigEntryResult)
	for _, result := range configResults {
		if result.Error.Code() != kafka.ErrNoError {
			continue
		}
		topicConfigs[result.Name] = result.Config
	}

	// Build the result
	var result []types.TopicDetails
	for topicName, topicMeta := range metadata.Topics {
		numPartitions := len(topicMeta.Partitions)
		var replicationFactor int
		if numPartitions > 0 {
			replicationFactor = len(topicMeta.Partitions[0].Replicas)
		}

		configurations := make(map[string]*string)
		if configs, ok := topicConfigs[topicName]; ok {
			for _, entry := range configs {
				value := entry.Value
				configurations[entry.Name] = &value
			}
		}

		result = append(result, types.TopicDetails{
			Name:              topicName,
			Partitions:        numPartitions,
			ReplicationFactor: replicationFactor,
			Configurations:    configurations,
		})
	}

	return result, nil
}

func (k *KafkaAdminClient) DescribeConfig() ([]types.BrokerConfigEntry, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	configResults, err := k.admin.DescribeConfigs(ctx, []kafka.ConfigResource{
		{Type: kafka.ResourceBroker, Name: "1"},
	})
	if err != nil {
		return nil, err
	}

	var result []types.BrokerConfigEntry
	for _, configResult := range configResults {
		if configResult.Error.Code() != kafka.ErrNoError {
			return nil, fmt.Errorf("failed to describe broker config: %v", configResult.Error)
		}
		for _, entry := range configResult.Config {
			result = append(result, types.BrokerConfigEntry{
				Name:      entry.Name,
				Value:     entry.Value,
				IsDefault: entry.IsDefault,
			})
		}
	}
	return result, nil
}

func (k *KafkaAdminClient) GetClusterKafkaMetadata() (*ClusterKafkaMetadata, error) {
	metadata, err := k.admin.GetMetadata(nil, false, 15000)
	if err != nil {
		return nil, err
	}

	// Get cluster ID
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	clusterID, err := k.admin.ClusterID(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get cluster ID: %w", err)
	}

	var brokerInfos []types.BrokerInfo
	for _, broker := range metadata.Brokers {
		brokerInfos = append(brokerInfos, types.BrokerInfo{
			ID:      broker.ID,
			Address: fmt.Sprintf("%s:%d", broker.Host, broker.Port),
		})
	}

	// The originating broker is the controller for our purposes
	controllerID := metadata.OriginatingBroker.ID

	return &ClusterKafkaMetadata{
		Brokers:      brokerInfos,
		ControllerID: controllerID,
		ClusterID:    clusterID,
	}, nil
}

func (k *KafkaAdminClient) ListAcls() ([]types.Acls, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	aclFilter := kafka.ACLBindingFilter{
		Type:                kafka.ResourceAny,
		ResourcePatternType: kafka.ResourcePatternTypeAny,
		Operation:           kafka.ACLOperationAny,
		PermissionType:      kafka.ACLPermissionTypeAny,
	}

	describeResult, err := k.admin.DescribeACLs(ctx, aclFilter)
	if err != nil {
		return nil, fmt.Errorf("failed to list ACLs: %w", err)
	}

	if describeResult.Error.Code() != kafka.ErrNoError {
		return nil, fmt.Errorf("failed to list ACLs: %v", describeResult.Error)
	}

	var result []types.Acls
	for _, binding := range describeResult.ACLBindings {
		result = append(result, types.Acls{
			ResourceType:        binding.Type.String(),
			ResourceName:        binding.Name,
			ResourcePatternType: binding.ResourcePatternType.String(),
			Principal:           binding.Principal,
			Host:                binding.Host,
			Operation:           binding.Operation.String(),
			PermissionType:      binding.PermissionType.String(),
		})
	}
	return result, nil
}

func (k *KafkaAdminClient) Close() error {
	k.admin.Close()
	return nil
}

// newConsumer creates a new confluent-kafka-go Consumer using the same auth config
func (k *KafkaAdminClient) newConsumer() (*kafka.Consumer, error) {
	// Copy the config map and add consumer-specific settings
	consumerConfig := kafka.ConfigMap{}
	for key, value := range k.configMap {
		_ = consumerConfig.SetKey(key, value)
	}
	_ = consumerConfig.SetKey("group.id", "kcp-cli-consumer-"+fmt.Sprintf("%d", time.Now().UnixNano()))
	_ = consumerConfig.SetKey("auto.offset.reset", "earliest")
	_ = consumerConfig.SetKey("enable.auto.commit", false)

	consumer, err := kafka.NewConsumer(&consumerConfig)
	if err != nil {
		return nil, err
	}

	// Handle OAuthBearer token for the consumer if using IAM auth
	if k.config.authType == types.AuthTypeIAM {
		token, expirationTimeMs, err := signer.GenerateAuthToken(context.TODO(), k.region)
		if err != nil {
			consumer.Close()
			return nil, fmt.Errorf("failed to generate IAM auth token for consumer: %w", err)
		}
		oauthToken := kafka.OAuthBearerToken{
			TokenValue: token,
			Expiration: time.UnixMilli(expirationTimeMs),
		}
		if err := consumer.SetOAuthBearerToken(oauthToken); err != nil {
			consumer.Close()
			return nil, fmt.Errorf("failed to set OAuthBearer token for consumer: %w", err)
		}
	}

	return consumer, nil
}

// GetAllMessagesWithKeyFilter retrieves all messages from a specific topic across all partitions
// that have keys starting with the specified prefix
// Returns a map of connector names to their configuration JSON strings
func (k *KafkaAdminClient) GetAllMessagesWithKeyFilter(topicName string, keyPrefix string) (map[string]string, error) {
	consumer, err := k.newConsumer()
	if err != nil {
		return nil, fmt.Errorf("failed to create consumer: %w", err)
	}
	defer consumer.Close()

	// Get metadata to discover partitions
	topicMeta, err := consumer.GetMetadata(&topicName, false, 10000)
	if err != nil {
		return nil, fmt.Errorf("failed to get metadata for topic %s: %w", topicName, err)
	}

	topicInfo, ok := topicMeta.Topics[topicName]
	if !ok || len(topicInfo.Partitions) == 0 {
		return nil, fmt.Errorf("topic %s has no partitions", topicName)
	}

	// Assign all partitions starting from the beginning
	var partitions []kafka.TopicPartition
	for _, p := range topicInfo.Partitions {
		partitions = append(partitions, kafka.TopicPartition{
			Topic:     &topicName,
			Partition: p.ID,
			Offset:    kafka.OffsetBeginning,
		})
	}

	if err := consumer.Assign(partitions); err != nil {
		return nil, fmt.Errorf("failed to assign partitions: %w", err)
	}

	connectorConfigs := make(map[string]string)
	overallTimeout := time.After(30 * time.Second)
	lastMessageTime := time.Now()

	for {
		select {
		case <-overallTimeout:
			return connectorConfigs, nil
		default:
		}

		msg, err := consumer.ReadMessage(5 * time.Second)
		if err != nil {
			// Check if it's a timeout (no more messages)
			kafkaErr, ok := err.(kafka.Error)
			if ok && kafkaErr.Code() == kafka.ErrTimedOut {
				if time.Since(lastMessageTime) >= 5*time.Second {
					break
				}
				continue
			}
			// Other errors - break out
			break
		}

		lastMessageTime = time.Now()
		if len(msg.Key) > 0 {
			keyStr := string(msg.Key)
			if len(keyStr) >= len(keyPrefix) && keyStr[:len(keyPrefix)] == keyPrefix {
				connectorConfigs[keyStr] = string(msg.Value)
			}
		}
	}

	return connectorConfigs, nil
}

// GetConnectorStatusMessages retrieves status messages from the connect-status topic
// by consuming messages from each partition and tracking the most recent status
// for each connector based on message timestamp
func (k *KafkaAdminClient) GetConnectorStatusMessages(topicName string) (map[string]string, error) {
	consumer, err := k.newConsumer()
	if err != nil {
		return nil, fmt.Errorf("failed to create consumer: %w", err)
	}
	defer consumer.Close()

	// Get metadata to discover partitions
	topicMeta, err := consumer.GetMetadata(&topicName, false, 10000)
	if err != nil {
		return nil, fmt.Errorf("failed to get metadata for topic %s: %w", topicName, err)
	}

	topicInfo, ok := topicMeta.Topics[topicName]
	if !ok || len(topicInfo.Partitions) == 0 {
		return nil, fmt.Errorf("topic %s has no partitions", topicName)
	}

	// Assign all partitions starting from the beginning
	var partitions []kafka.TopicPartition
	for _, p := range topicInfo.Partitions {
		partitions = append(partitions, kafka.TopicPartition{
			Topic:     &topicName,
			Partition: p.ID,
			Offset:    kafka.OffsetBeginning,
		})
	}

	if err := consumer.Assign(partitions); err != nil {
		return nil, fmt.Errorf("failed to assign partitions: %w", err)
	}

	connectorStatuses := make(map[string]string)
	connectorTimestamps := make(map[string]int64)
	overallTimeout := time.After(30 * time.Second)
	lastMessageTime := time.Now()

	for {
		select {
		case <-overallTimeout:
			return connectorStatuses, nil
		default:
		}

		msg, err := consumer.ReadMessage(5 * time.Second)
		if err != nil {
			kafkaErr, ok := err.(kafka.Error)
			if ok && kafkaErr.Code() == kafka.ErrTimedOut {
				if time.Since(lastMessageTime) >= 5*time.Second {
					break
				}
				continue
			}
			break
		}

		lastMessageTime = time.Now()
		if len(msg.Key) > 0 {
			keyStr := string(msg.Key)
			if len(keyStr) >= 17 && keyStr[:17] == "status-connector-" {
				connectorName := keyStr[17:]

				var msgTimestamp int64
				if msg.Timestamp.IsZero() {
					msgTimestamp = int64(msg.TopicPartition.Offset)
				} else {
					msgTimestamp = msg.Timestamp.UnixMilli()
				}

				if existingTimestamp, exists := connectorTimestamps[connectorName]; !exists || msgTimestamp > existingTimestamp {
					connectorStatuses[connectorName] = string(msg.Value)
					connectorTimestamps[connectorName] = msgTimestamp
				}
			}
		}
	}

	return connectorStatuses, nil
}
