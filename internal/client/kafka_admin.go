package client

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/IBM/sarama"
	"github.com/aws/aws-msk-iam-sasl-signer-go/signer"
	kafkatypes "github.com/aws/aws-sdk-go-v2/service/kafka/types"
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

func configureSASLTypeOAuthAuthentication(config *sarama.Config, region string) {
	slog.Info("🔍 configuring SASL/OAuth (IAM) authentication")
	config.Net.TLS.Enable = true
	config.Net.TLS.Config = &tls.Config{}
	config.Net.SASL.Enable = true
	config.Net.SASL.Mechanism = sarama.SASLTypeOAuth
	config.Net.SASL.TokenProvider = &MSKAccessTokenProvider{region: region}
}

func configureSASLTypeSCRAMAuthentication(config *sarama.Config, username string, password string) {
	slog.Info("🔍 configuring SASL/SCRAM authentication")
	config.Net.TLS.Enable = true
	config.Net.TLS.Config = &tls.Config{}
	config.Net.SASL.Enable = true
	config.Net.SASL.User = username
	config.Net.SASL.Password = password
	config.Net.SASL.Handshake = true
	config.Net.SASL.SCRAMClientGeneratorFunc = func() sarama.SCRAMClient { return &XDGSCRAMClient{HashGeneratorFcn: SHA512} }
	config.Net.SASL.Mechanism = sarama.SASLTypeSCRAMSHA512
}

func configureUnauthenticatedAuthentication(config *sarama.Config, withTLSEncryption bool) {
	slog.Info("🔍 enabling TLS encryption", "enableTlsEncryption", withTLSEncryption)
	config.Net.TLS.Enable = withTLSEncryption
	config.Net.TLS.Config = &tls.Config{}
}

func configureTLSAuth(config *sarama.Config, caCertFile string, clientCertFile string, clientKeyFile string) error {
	tlsConfig := tls.Config{}

	cert, err := tls.LoadX509KeyPair(clientCertFile, clientKeyFile)
	if err != nil {
		return fmt.Errorf("failed to load client certificate: %v", err)
	}
	tlsConfig.Certificates = []tls.Certificate{cert}

	caCert, err := os.ReadFile(caCertFile)
	if err != nil {
		return fmt.Errorf("failed to read CA certificate file: %v", err)
	}

	caCertPool := x509.NewCertPool()
	if !caCertPool.AppendCertsFromPEM(caCert) {
		return fmt.Errorf("failed to append CA certificate to pool")
	}
	tlsConfig.RootCAs = caCertPool

	config.Net.TLS.Enable = true
	config.Net.TLS.Config = &tlsConfig
	return nil
}

func configureCommonSettings(config *sarama.Config, clientID string, kafkaVersion sarama.KafkaVersion) {
	config.Version = kafkaVersion
	config.ClientID = clientID

	// Network-level timeout configurations
	config.Net.DialTimeout = 10 * time.Second // Connection establishment timeout
	config.Net.ReadTimeout = 30 * time.Second // Socket read operations timeout
	config.Net.KeepAlive = 30 * time.Second   // TCP keep-alive interval

	// Request-specific timeout configurations
	config.Metadata.Timeout = 15 * time.Second // Metadata request timeout

	// Retry configuration with backoff
	config.Metadata.Retry.Max = 3
	config.Metadata.Retry.Backoff = 250 * time.Millisecond
}

// ClusterKafkaMetadata represents cluster information including brokers, controller, and cluster ID
type ClusterKafkaMetadata struct {
	Brokers      []*sarama.Broker
	ControllerID int32
	ClusterID    string
}

// KafkaAdmin interface defines the Kafka admin operations we need
type KafkaAdmin interface {
	ListTopicsWithConfigs() (map[string]sarama.TopicDetail, error)
	GetClusterKafkaMetadata() (*ClusterKafkaMetadata, error)
	DescribeConfig() ([]sarama.ConfigEntry, error)
	ListAcls() ([]sarama.ResourceAcls, error)
	GetAllMessagesWithKeyFilter(topicName string, keyPrefix string) (map[string]string, error)
	GetConnectorStatusMessages(topicName string) (map[string]string, error)
	Close() error
}

// MSKAccessTokenProvider implements sarama.AccessTokenProvider for MSK IAM authentication
type MSKAccessTokenProvider struct {
	region string
}

func (m *MSKAccessTokenProvider) Token() (*sarama.AccessToken, error) {
	token, _, err := signer.GenerateAuthToken(context.TODO(), m.region)

	return &sarama.AccessToken{Token: token}, err
}

// KafkaAdminClient wraps sarama.ClusterAdmin to implement our KafkaAdmin interface
type KafkaAdminClient struct {
	admin           sarama.ClusterAdmin
	region          string
	config          AdminConfig
	saramaConfig    *sarama.Config
	resourceAcls    map[string]sarama.ResourceAcls
	brokerAddresses []string
}

/*
A custom implementation of the ListTopics() function in Sarama that returns all topic configs
instead of just overridden configs. This was done to reduce the number of requests to the broker.
https://github.com/IBM/sarama/blob/main/admin.go#L349
*/
func (k *KafkaAdminClient) ListTopicsWithConfigs() (map[string]sarama.TopicDetail, error) {
	// Get controller to use as a connection broker to avoid opening a new broker connection
	controller, err := k.admin.Controller()
	if err != nil {
		return nil, fmt.Errorf("failed to get controller: %w", err)
	}

	// Send the all-topic MetadataRequest
	metadataReq := sarama.NewMetadataRequest(k.saramaConfig.Version, nil)
	metadataResp, err := controller.GetMetadata(metadataReq)
	if err != nil {
		return nil, fmt.Errorf("failed to get metadata: %w", err)
	}

	topicsDetailsMap := make(map[string]sarama.TopicDetail)
	var describeConfigsResources []*sarama.ConfigResource

	for _, topic := range metadataResp.Topics {
		topicDetails := sarama.TopicDetail{
			NumPartitions: int32(len(topic.Partitions)),
		}
		if len(topic.Partitions) > 0 {
			topicDetails.ReplicaAssignment = map[int32][]int32{}
			for _, partition := range topic.Partitions {
				topicDetails.ReplicaAssignment[partition.ID] = partition.Replicas
			}
			topicDetails.ReplicationFactor = int16(len(topic.Partitions[0].Replicas))
		}
		topicsDetailsMap[topic.Name] = topicDetails

		// we populate the resources we want to describe from the MetadataResponse
		topicResource := &sarama.ConfigResource{
			Type: sarama.TopicResource,
			Name: topic.Name,
		}
		describeConfigsResources = append(describeConfigsResources, topicResource)
	}

	// Send the DescribeConfigsRequest
	describeConfigsReq := &sarama.DescribeConfigsRequest{
		Resources: describeConfigsResources,
	}

	if k.saramaConfig.Version.IsAtLeast(sarama.V1_1_0_0) {
		describeConfigsReq.Version = 1
	}

	if k.saramaConfig.Version.IsAtLeast(sarama.V2_0_0_0) {
		describeConfigsReq.Version = 2
	}

	describeConfigsResp, err := controller.DescribeConfigs(describeConfigsReq)
	if err != nil {
		return nil, fmt.Errorf("failed to describe configs: %w", err)
	}

	for _, resource := range describeConfigsResp.Resources {
		topicDetails := topicsDetailsMap[resource.Name]
		topicDetails.ConfigEntries = make(map[string]*string)

		for _, entry := range resource.Configs {
			// Include ALL configs without filtering (no default/sensitive filtering)
			topicDetails.ConfigEntries[entry.Name] = &entry.Value
		}

		topicsDetailsMap[resource.Name] = topicDetails
	}

	return topicsDetailsMap, nil
}

func (k *KafkaAdminClient) DescribeConfig() ([]sarama.ConfigEntry, error) {
	return k.admin.DescribeConfig(sarama.ConfigResource{
		Type: sarama.ConfigResourceType(sarama.ConfigResourceType(sarama.BrokerResource)),
		Name: "1",
	})
}

func (k *KafkaAdminClient) GetClusterKafkaMetadata() (*ClusterKafkaMetadata, error) {
	brokers, controllerID, err := k.admin.DescribeCluster()
	if err != nil {
		return nil, err
	}

	var clusterID string
	// Get cluster ID by connecting to a broker and requesting metadata
	if len(brokers) > 0 {
		clusterID, err = k.getClusterIDFromBroker(brokers[0])
		if err != nil {
			return nil, err
		}
	}

	return &ClusterKafkaMetadata{
		Brokers:      brokers,
		ControllerID: controllerID,
		ClusterID:    clusterID,
	}, nil
}

// getClusterIDFromBroker establishes a connection to a specific broker and retrieves the cluster ID
func (k *KafkaAdminClient) getClusterIDFromBroker(broker *sarama.Broker) (string, error) {
	// Create a new broker connection
	brokerConn := sarama.NewBroker(broker.Addr())
	err := brokerConn.Open(k.saramaConfig)
	if err != nil {
		return "", fmt.Errorf("failed to open broker connection: %v", err)
	}
	defer brokerConn.Close()

	// Request metadata from the broker
	metadataReq := &sarama.MetadataRequest{Version: 7}
	metadata, err := brokerConn.GetMetadata(metadataReq)

	if err != nil {
		return "", fmt.Errorf("failed to get metadata: %v", err)
	}

	if metadata.ClusterID == nil {
		return "", fmt.Errorf("cluster ID not available in metadata")
	}

	return *metadata.ClusterID, nil
}

func (k *KafkaAdminClient) ListAcls() ([]sarama.ResourceAcls, error) {
	aclFilter := sarama.AclFilter{
		// nil means any resource name, principal, or host.
		ResourceType:              sarama.AclResourceAny,
		ResourceName:              nil,
		ResourcePatternTypeFilter: sarama.AclPatternAny,
		Principal:                 nil,
		Host:                      nil,
		Operation:                 sarama.AclOperationAny,
		PermissionType:            sarama.AclPermissionAny,
	}

	result, err := k.admin.ListAcls(aclFilter)
	if err != nil {
		return nil, fmt.Errorf("failed to list ACLs: %w", err)
	}

	return result, nil
}

func (k *KafkaAdminClient) Close() error {
	return k.admin.Close()
}

// GetAllMessagesWithKeyFilter retrieves all messages from a specific topic across all partitions
// that have keys starting with the specified prefix
// Returns a map of connector names to their configuration JSON strings
func (k *KafkaAdminClient) GetAllMessagesWithKeyFilter(topicName string, keyPrefix string) (map[string]string, error) {
	consumer, err := sarama.NewConsumer(k.brokerAddresses, k.saramaConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create consumer: %w", err)
	}
	defer consumer.Close()

	partitions, err := consumer.Partitions(topicName)
	if err != nil {
		return nil, fmt.Errorf("failed to get partitions for topic %s: %w", topicName, err)
	}

	if len(partitions) == 0 {
		return nil, fmt.Errorf("topic %s has no partitions", topicName)
	}

	controller, err := k.admin.Controller()
	if err != nil {
		return nil, fmt.Errorf("failed to get controller: %w", err)
	}

	connectorConfigs := make(map[string]string)

	// Process each partition
	for _, partition := range partitions {
		// Get oldest offset
		oldestReq := &sarama.OffsetRequest{
			Version: 1,
		}
		oldestReq.AddBlock(topicName, partition, sarama.OffsetOldest, 1)

		oldestResp, err := controller.GetAvailableOffsets(oldestReq)
		if err != nil {
			continue // Skip this partition if we can't get offsets
		}

		oldestBlock := oldestResp.GetBlock(topicName, partition)
		if oldestBlock == nil || len(oldestBlock.Offsets) == 0 {
			continue
		}
		oldestOffset := oldestBlock.Offsets[0]

		// Get newest offset
		newestReq := &sarama.OffsetRequest{
			Version: 1,
		}
		newestReq.AddBlock(topicName, partition, sarama.OffsetNewest, 1)

		newestResp, err := controller.GetAvailableOffsets(newestReq)
		if err != nil {
			continue // Skip this partition if we can't get offsets
		}

		newestBlock := newestResp.GetBlock(topicName, partition)
		if newestBlock == nil || len(newestBlock.Offsets) == 0 {
			continue
		}
		newestOffset := newestBlock.Offsets[0]

		if newestOffset <= oldestOffset {
			continue // No messages in this partition
		}

		// Create a partition consumer for the entire range
		partitionConsumer, err := consumer.ConsumePartition(topicName, partition, oldestOffset)
		if err != nil {
			continue // Skip this partition if we can't consume it
		}

		// Consume messages until we reach the newest offset
		messageCount := 0
	consumeLoop:
		for messageCount < int(newestOffset-oldestOffset) {
			select {
			case msg := <-partitionConsumer.Messages():
				if msg != nil {
					messageCount++
					if len(msg.Key) > 0 {
						keyStr := string(msg.Key)
						// Check if the key starts with the prefix
						if len(keyStr) >= len(keyPrefix) && keyStr[:len(keyPrefix)] == keyPrefix {
							// Use the full key as the connector name
							connectorConfigs[keyStr] = string(msg.Value)
						}
					}
				}
			case err := <-partitionConsumer.Errors():
				if err != nil {
					// Log the error but continue with other partitions
					break consumeLoop
				}
			case <-time.After(10 * time.Second):
				// Timeout after 10 seconds per partition
				break consumeLoop
			}
		}

		partitionConsumer.Close()
	}

	return connectorConfigs, nil
}

// GetConnectorStatusMessages retrieves status messages from the connect-status topic
// by consuming the last 1000 messages from each partition and tracking the most recent status
// for each connector based on message timestamp
func (k *KafkaAdminClient) GetConnectorStatusMessages(topicName string) (map[string]string, error) {
	consumer, err := sarama.NewConsumer(k.brokerAddresses, k.saramaConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create consumer: %w", err)
	}
	defer consumer.Close()

	partitions, err := consumer.Partitions(topicName)
	if err != nil {
		return nil, fmt.Errorf("failed to get partitions for topic %s: %w", topicName, err)
	}

	if len(partitions) == 0 {
		return nil, fmt.Errorf("topic %s has no partitions", topicName)
	}

	// Map to store the latest status for each connector
	connectorStatuses := make(map[string]string)
	// Map to track the latest timestamp for each connector
	connectorTimestamps := make(map[string]int64)

	// Process each partition
	for _, partition := range partitions {
		// Start consuming from the oldest available offset
		partitionConsumer, err := consumer.ConsumePartition(topicName, partition, sarama.OffsetOldest)
		if err != nil {
			continue // Skip this partition if we can't consume it
		}

		messagesRead := 0
		timeout := time.After(5 * time.Second)

	partitionLoop:
		for {
			select {
			case msg := <-partitionConsumer.Messages():
				if msg != nil {
					messagesRead++

					if len(msg.Key) > 0 {
						keyStr := string(msg.Key)

						// Check if the key starts with "status-connector-"
						if len(keyStr) >= 17 && keyStr[:17] == "status-connector-" {
							connectorName := keyStr[17:]

							// Get message timestamp
							var msgTimestamp int64
							if msg.Timestamp.IsZero() {
								msgTimestamp = int64(msg.Offset)
							} else {
								msgTimestamp = msg.Timestamp.UnixMilli()
							}

							// Only update if this is a newer message
							if existingTimestamp, exists := connectorTimestamps[connectorName]; !exists || msgTimestamp > existingTimestamp {
								connectorStatuses[connectorName] = string(msg.Value)
								connectorTimestamps[connectorName] = msgTimestamp
							}
						}
					}

					// Limit to 1000 messages per partition to avoid consuming too much
					if messagesRead >= 1000 {
						break partitionLoop
					}
				}
			case <-partitionConsumer.Errors():
				break partitionLoop
			case <-timeout:
				break partitionLoop
			}
		}

		partitionConsumer.Close()
	}

	return connectorStatuses, nil
}

// NewKafkaAdmin creates a new Kafka admin client for the given broker addresses and region
func NewKafkaAdmin(brokerAddresses []string, clientBrokerEncryptionInTransit kafkatypes.ClientBroker, region string, kafkaVersion string, opts ...AdminOption) (KafkaAdmin, error) {
	// Default configuration
	config := AdminConfig{
		authType: types.AuthTypeIAM, // Default to IAM auth
	}

	// Apply all options
	for _, opt := range opts {
		opt(&config)
	}

	saramaKafkaVersion, err := sarama.ParseKafkaVersion(kafkaVersion)
	if err != nil {
		return nil, fmt.Errorf("❌ Failed to parse Kafka version: %v", err)
	}

	saramaConfig := sarama.NewConfig()
	configureCommonSettings(saramaConfig, "kcp-cli", saramaKafkaVersion)

	switch config.authType {
	case types.AuthTypeIAM:
		configureSASLTypeOAuthAuthentication(saramaConfig, region)
	case types.AuthTypeSASLSCRAM:
		configureSASLTypeSCRAMAuthentication(saramaConfig, config.username, config.password)
	case types.AuthTypeUnauthenticatedTLS:
		configureUnauthenticatedAuthentication(saramaConfig, true)
	case types.AuthTypeUnauthenticatedPlaintext:
		configureUnauthenticatedAuthentication(saramaConfig, false)
	case types.AuthTypeTLS:
		err := configureTLSAuth(saramaConfig, config.caCertFile, config.clientCertFile, config.clientKeyFile)
		if err != nil {
			return nil, fmt.Errorf("❌ Failed to configure TLS authentication: %v", err)
		}
	default:
		return nil, fmt.Errorf("❌ Auth type: %v not yet supported", config.authType)
	}

	admin, err := sarama.NewClusterAdmin(brokerAddresses, saramaConfig)
	if err != nil {
		return nil, fmt.Errorf("❌ Failed to create admin client: authType=%v brokerAddresses=%v error=%v", config.authType, brokerAddresses, err)
	}

	return &KafkaAdminClient{
		admin:           admin,
		region:          region,
		config:          config,
		saramaConfig:    saramaConfig,
		resourceAcls:    make(map[string]sarama.ResourceAcls),
		brokerAddresses: brokerAddresses,
	}, nil
}
