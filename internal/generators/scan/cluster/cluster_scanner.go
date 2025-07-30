package cluster

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/kafka"
	kafkatypes "github.com/aws/aws-sdk-go-v2/service/kafka/types"
	"github.com/confluentinc/kcp/internal/client"
	"github.com/confluentinc/kcp/internal/types"
)

// ClusterScannerMSKClient defines the MSK client methods used by ClusterScanner
type ClusterScannerMSKClient interface {
	GetCompatibleKafkaVersions(ctx context.Context, params *kafka.GetCompatibleKafkaVersionsInput, optFns ...func(*kafka.Options)) (*kafka.GetCompatibleKafkaVersionsOutput, error)
	GetClusterPolicy(ctx context.Context, params *kafka.GetClusterPolicyInput, optFns ...func(*kafka.Options)) (*kafka.GetClusterPolicyOutput, error)
	GetBootstrapBrokers(ctx context.Context, params *kafka.GetBootstrapBrokersInput, optFns ...func(*kafka.Options)) (*kafka.GetBootstrapBrokersOutput, error)
	DescribeClusterV2(ctx context.Context, params *kafka.DescribeClusterV2Input, optFns ...func(*kafka.Options)) (*kafka.DescribeClusterV2Output, error)
	ListClientVpcConnections(ctx context.Context, params *kafka.ListClientVpcConnectionsInput, optFns ...func(*kafka.Options)) (*kafka.ListClientVpcConnectionsOutput, error)
	ListClusterOperationsV2(ctx context.Context, params *kafka.ListClusterOperationsV2Input, optFns ...func(*kafka.Options)) (*kafka.ListClusterOperationsV2Output, error)
	ListNodes(ctx context.Context, params *kafka.ListNodesInput, optFns ...func(*kafka.Options)) (*kafka.ListNodesOutput, error)
	ListScramSecrets(ctx context.Context, params *kafka.ListScramSecretsInput, optFns ...func(*kafka.Options)) (*kafka.ListScramSecretsOutput, error)
}

// KafkaAdminFactory is a function type that creates a KafkaAdmin client
type KafkaAdminFactory func(brokerAddresses []string, clientBrokerEncryptionInTransit kafkatypes.ClientBroker) (client.KafkaAdmin, error)

type ClusterScannerOpts struct {
	Region            string
	ClusterArn        string
	SkipKafka         bool
	AuthType          types.AuthType
	SASLScramUsername string
	SASLScramPassword string
	TLSCACert         string
	TLSClientCert     string
	TLSClientKey      string
}

type ClusterScanner struct {
	mskClient         ClusterScannerMSKClient
	kafkaAdminFactory KafkaAdminFactory
	region            string
	clusterArn        string
	skipKafka         bool
	authType          types.AuthType
}

// NewClusterScanner creates a new ClusterScanner instance.
func NewClusterScanner(mskClient ClusterScannerMSKClient, kafkaAdminFactory KafkaAdminFactory, opts ClusterScannerOpts) *ClusterScanner {
	return &ClusterScanner{
		mskClient:         mskClient,
		kafkaAdminFactory: kafkaAdminFactory,
		region:            opts.Region,
		clusterArn:        opts.ClusterArn,
		skipKafka:         opts.SkipKafka,
		authType:          opts.AuthType,
	}
}

func (cs *ClusterScanner) Run() error {
	slog.Info("🚀 starting cluster scan", "cluster", cs.clusterArn)

	ctx := context.TODO()

	clusterInfo, err := cs.scanCluster(ctx)
	if err != nil {
		return err
	}

	data, err := json.MarshalIndent(clusterInfo, "", "  ")
	if err != nil {
		return fmt.Errorf("❌ Failed to marshal cluster information: %v", err)
	}

	filePath := fmt.Sprintf("cluster_scan_%s.json", aws.ToString(clusterInfo.Cluster.ClusterName))
	if err := os.WriteFile(filePath, data, 0644); err != nil {
		return fmt.Errorf("❌ Failed to write file: %v", err)
	}

	// Generate markdown report
	mdFilePath := fmt.Sprintf("cluster_scan_%s.md", aws.ToString(clusterInfo.Cluster.ClusterName))
	if err := cs.generateMarkdownReport(clusterInfo, mdFilePath); err != nil {
		return fmt.Errorf("❌ Failed to generate markdown report: %v", err)
	}

	slog.Info("✅ cluster scan complete",
		"cluster", cs.clusterArn,
		"clusterName", clusterInfo.Cluster.ClusterName,
		"topicCount", len(clusterInfo.Topics),
		"filePath", filePath,
		"markdownPath", mdFilePath,
	)

	return nil
}

func (cs *ClusterScanner) scanCluster(ctx context.Context) (*types.ClusterInformation, error) {
	clusterInfo := &types.ClusterInformation{
		Timestamp: time.Now(),
		Region:    cs.region,
	}

	if err := cs.scanAWSResources(ctx, clusterInfo); err != nil {
		return nil, err
	}

	if !cs.skipKafka {
		if err := cs.scanKafkaResources(clusterInfo); err != nil {
			return nil, err
		}
	} else {
		slog.Info("🔍 skipping kafka level cluster scan", "clusterArn", cs.clusterArn)
	}

	return clusterInfo, nil
}

func (cs *ClusterScanner) scanAWSResources(ctx context.Context, clusterInfo *types.ClusterInformation) error {
	maxResults := int32(100)

	cluster, err := cs.describeCluster(ctx, &cs.clusterArn)
	if err != nil {
		return err
	}
	clusterInfo.Cluster = *cluster.ClusterInfo

	brokers, err := cs.getBootstrapBrokers(ctx, &cs.clusterArn)
	if err != nil {
		return err
	}
	clusterInfo.BootstrapBrokers = *brokers

	connections, err := cs.scanClusterVpcConnections(ctx, &cs.clusterArn, maxResults)
	if err != nil {
		return err
	}
	clusterInfo.ClientVpcConnections = connections

	operations, err := cs.scanClusterOperations(ctx, &cs.clusterArn, maxResults)
	if err != nil {
		return err
	}
	clusterInfo.ClusterOperations = operations

	nodes, err := cs.scanClusterNodes(ctx, &cs.clusterArn, maxResults)
	if err != nil {
		return err
	}
	clusterInfo.Nodes = nodes

	scramSecrets, err := cs.scanClusterScramSecrets(ctx, &cs.clusterArn, maxResults)
	if err != nil {
		return err
	}
	clusterInfo.ScramSecrets = scramSecrets

	policy, err := cs.getClusterPolicy(ctx, &cs.clusterArn)
	if err != nil {
		return err
	}
	clusterInfo.Policy = *policy

	versions, err := cs.getCompatibleKafkaVersions(ctx, &cs.clusterArn)
	if err != nil {
		return err
	}
	clusterInfo.CompatibleVersions = *versions

	return nil
}

func (cs *ClusterScanner) getCompatibleKafkaVersions(ctx context.Context, clusterArn *string) (*kafka.GetCompatibleKafkaVersionsOutput, error) {
	slog.Info("🔍 scanning for compatible kafka versions", "clusterArn", cs.clusterArn)

	versions, err := cs.mskClient.GetCompatibleKafkaVersions(ctx, &kafka.GetCompatibleKafkaVersionsInput{
		ClusterArn: clusterArn,
	})
	if err != nil {
		if strings.Contains(err.Error(), "This operation cannot be performed on serverless clusters.") {
			slog.Warn("⚠️ Compatible versions not supported for MSK Serverless clusters, skipping compatible versions scan")
			return new(kafka.GetCompatibleKafkaVersionsOutput), nil
		}
		return nil, fmt.Errorf("❌ Failed to get compatible versions: %v", err)
	}
	return versions, nil
}

func (cs *ClusterScanner) getClusterPolicy(ctx context.Context, clusterArn *string) (*kafka.GetClusterPolicyOutput, error) {
	slog.Info("🔍 scanning for cluster policy", "clusterArn", cs.clusterArn)

	policy, err := cs.mskClient.GetClusterPolicy(ctx, &kafka.GetClusterPolicyInput{
		ClusterArn: clusterArn,
	})
	if err == nil {
		return policy, nil
	}

	var notFoundErr *kafkatypes.NotFoundException
	if errors.As(err, &notFoundErr) {
		return new(kafka.GetClusterPolicyOutput), nil
	}
	return nil, err
}

func (cs *ClusterScanner) getBootstrapBrokers(ctx context.Context, clusterArn *string) (*kafka.GetBootstrapBrokersOutput, error) {
	slog.Info("🔍 scanning for bootstrap brokers", "clusterArn", cs.clusterArn)

	brokers, err := cs.mskClient.GetBootstrapBrokers(ctx, &kafka.GetBootstrapBrokersInput{
		ClusterArn: clusterArn,
	})
	if err != nil {
		return nil, fmt.Errorf("❌ Failed to scan brokers: %v", err)
	}
	return brokers, nil
}

func (cs *ClusterScanner) describeCluster(ctx context.Context, clusterArn *string) (*kafka.DescribeClusterV2Output, error) {
	slog.Info("🔍 describing cluster", "clusterArn", cs.clusterArn)

	cluster, err := cs.mskClient.DescribeClusterV2(ctx, &kafka.DescribeClusterV2Input{
		ClusterArn: clusterArn,
	})
	if err != nil {
		return nil, fmt.Errorf("❌ Failed to describe cluster: %v", err)
	}
	return cluster, nil
}

func (cs *ClusterScanner) scanClusterVpcConnections(ctx context.Context, clusterArn *string, maxResults int32) ([]kafkatypes.ClientVpcConnection, error) {
	slog.Info("🔍 scanning for client vpc connections", "clusterArn", cs.clusterArn)

	var connections []kafkatypes.ClientVpcConnection
	var nextToken *string

	for {
		output, err := cs.mskClient.ListClientVpcConnections(ctx, &kafka.ListClientVpcConnectionsInput{
			MaxResults: &maxResults,
			ClusterArn: clusterArn,
			NextToken:  nextToken,
		})
		if err != nil {
			if strings.Contains(err.Error(), "This Region doesn't currently support VPC connectivity with Amazon MSK Serverless clusters") {
				slog.Warn("⚠️ VPC connectivity not supported for MSK Serverless clusters in this region, skipping VPC connections scan")
				return []kafkatypes.ClientVpcConnection{}, nil
			}
			return nil, fmt.Errorf("❌ Failed listing client vpc connections: %v", err)
		}
		connections = append(connections, output.ClientVpcConnections...)
		if output.NextToken == nil {
			break
		}
		nextToken = output.NextToken
	}

	return connections, nil
}

func (cs *ClusterScanner) scanClusterOperations(ctx context.Context, clusterArn *string, maxResults int32) ([]kafkatypes.ClusterOperationV2Summary, error) {
	slog.Info("🔍 scanning for cluster operations", "clusterArn", cs.clusterArn)

	var operations []kafkatypes.ClusterOperationV2Summary
	var nextToken *string

	for {
		output, err := cs.mskClient.ListClusterOperationsV2(ctx, &kafka.ListClusterOperationsV2Input{
			MaxResults: &maxResults,
			ClusterArn: clusterArn,
			NextToken:  nextToken,
		})
		if err != nil {
			return nil, fmt.Errorf("❌ Failed listing operations: %v", err)
		}
		operations = append(operations, output.ClusterOperationInfoList...)
		if output.NextToken == nil {
			break
		}
		nextToken = output.NextToken
	}

	return operations, nil
}

func (cs *ClusterScanner) scanClusterNodes(ctx context.Context, clusterArn *string, maxResults int32) ([]kafkatypes.NodeInfo, error) {
	slog.Info("🔍 scanning for cluster nodes", "clusterArn", cs.clusterArn)

	var nodes []kafkatypes.NodeInfo
	var nextToken *string

	for {
		output, err := cs.mskClient.ListNodes(ctx, &kafka.ListNodesInput{
			MaxResults: &maxResults,
			ClusterArn: clusterArn,
			NextToken:  nextToken,
		})
		if err != nil {
			if strings.Contains(err.Error(), "This operation cannot be performed on serverless clusters.") {
				slog.Warn("⚠️ Node listing not supported for MSK Serverless clusters, skipping Nodes scan")
				return []kafkatypes.NodeInfo{}, nil
			}
			return nil, fmt.Errorf("❌ Failed listing nodes: %v", err)
		}
		nodes = append(nodes, output.NodeInfoList...)
		if output.NextToken == nil {
			break
		}
		nextToken = output.NextToken
	}

	return nodes, nil
}

func (cs *ClusterScanner) scanClusterScramSecrets(ctx context.Context, clusterArn *string, maxResults int32) ([]string, error) {
	slog.Info("🔍 scanning for cluster scram secrets", "clusterArn", cs.clusterArn)

	var secrets []string
	var nextToken *string

	for {
		output, err := cs.mskClient.ListScramSecrets(ctx, &kafka.ListScramSecretsInput{
			MaxResults: &maxResults,
			ClusterArn: clusterArn,
			NextToken:  nextToken,
		})
		if err != nil {
			if strings.Contains(err.Error(), "This operation cannot be performed on serverless clusters.") {
				slog.Warn("⚠️ Scram secret listing not supported for MSK Serverless clusters, skipping scram secrets scan")
				return []string{}, nil
			}
			return nil, fmt.Errorf("error listing secrets: %v", err)
		}
		secrets = append(secrets, output.SecretArnList...)
		if output.NextToken == nil {
			break
		}
		nextToken = output.NextToken
	}

	return secrets, nil
}

func (cs *ClusterScanner) parseBrokerAddresses(brokers kafka.GetBootstrapBrokersOutput) ([]string, error) {
	var brokerList string
	var visibility string
	slog.Info("🔍 parsing broker addresses", "authType", cs.authType)

	switch cs.authType {
	case types.AuthTypeIAM:
		brokerList = aws.ToString(brokers.BootstrapBrokerStringPublicSaslIam)
		visibility = "PUBLIC"
		if brokerList == "" {
			brokerList = aws.ToString(brokers.BootstrapBrokerStringSaslIam)
			visibility = "PRIVATE"
		}
		if brokerList == "" {
			return nil, fmt.Errorf("❌ No SASL/IAM brokers found in the cluster")
		}
	case types.AuthTypeSASLSCRAM:
		brokerList = aws.ToString(brokers.BootstrapBrokerStringPublicSaslScram)
		visibility = "PUBLIC"
		if brokerList == "" {
			brokerList = aws.ToString(brokers.BootstrapBrokerStringSaslScram)
			visibility = "PRIVATE"
		}
		if brokerList == "" {
			return nil, fmt.Errorf("❌ No SASL/SCRAM brokers found in the cluster")
		}
	case types.AuthTypeUnauthenticated:
		brokerList = aws.ToString(brokers.BootstrapBrokerStringTls)
		visibility = "PRIVATE"
		if brokerList == "" {
			brokerList = aws.ToString(brokers.BootstrapBrokerString)
		}
		if brokerList == "" {
			return nil, fmt.Errorf("❌ No Unauthenticated brokers found in the cluster")
		}
	case types.AuthTypeTLS:
		brokerList = aws.ToString(brokers.BootstrapBrokerStringPublicTls)
		visibility = "PUBLIC"
		if brokerList == "" {
			brokerList = aws.ToString(brokers.BootstrapBrokerStringTls)
			visibility = "PRIVATE"
		}
		if brokerList == "" {
			return nil, fmt.Errorf("❌ No TLS brokers found in the cluster")
		}
	default:
		return nil, fmt.Errorf("❌ Auth type: %v not yet supported", cs.authType)
	}

	slog.Info("🔍 found broker addresses", "visibility", visibility, "authType", cs.authType, "addresses", brokerList)

	// Split by comma and trim whitespace from each address, filter out empty strings
	rawAddresses := strings.Split(brokerList, ",")
	addresses := make([]string, 0, len(rawAddresses))
	for _, addr := range rawAddresses {
		trimmedAddr := strings.TrimSpace(addr)
		if trimmedAddr != "" {
			addresses = append(addresses, trimmedAddr)
		}
	}
	return addresses, nil
}

func (cs *ClusterScanner) scanClusterTopics(admin client.KafkaAdmin) ([]string, error) {
	slog.Info("🔍 scanning for cluster topics", "clusterArn", cs.clusterArn)

	topics, err := admin.ListTopics()
	if err != nil {
		return nil, fmt.Errorf("❌ Failed to list topics: %v", err)
	}

	topicList := make([]string, 0, len(topics))
	for topic := range topics {
		topicList = append(topicList, topic)
	}

	return topicList, nil
}

// retrieveClusterId gets cluster metadata and returns the cluster ID along with logging information
func (cs *ClusterScanner) describeKafkaCluster(admin client.KafkaAdmin) (*client.ClusterKafkaMetadata, error) {
	slog.Info("🔍 describing kafka cluster", "clusterArn", cs.clusterArn)

	clusterMetadata, err := admin.GetClusterKafkaMetadata()
	if err != nil {
		return nil, fmt.Errorf("❌ Failed to describe kafka cluster: %v", err)
	}
	return clusterMetadata, nil
}

func (cs *ClusterScanner) scanKafkaResources(clusterInfo *types.ClusterInformation) error {

	brokerAddresses, err := cs.parseBrokerAddresses(clusterInfo.BootstrapBrokers)
	if err != nil {
		return err
	}

	clientBrokerEncryptionInTransit := types.GetClientBrokerEncryptionInTransit(clusterInfo.Cluster)

	admin, err := cs.kafkaAdminFactory(brokerAddresses, clientBrokerEncryptionInTransit)
	if err != nil {
		return fmt.Errorf("❌ Failed to setup admin client: %v", err)
	}

	defer admin.Close()

	// Get cluster metadata including broker information and ClusterID
	clusterMetadata, err := cs.describeKafkaCluster(admin)
	if err != nil {
		return err
	}

	clusterInfo.ClusterID = clusterMetadata.ClusterID

	topics, err := cs.scanClusterTopics(admin)
	if err != nil {
		return err
	}
	clusterInfo.Topics = topics

	acls, err := cs.scanKafkaAcls(admin)
	if err != nil {
		return err
	}
	clusterInfo.Acls = acls

	return nil
}

func (cs *ClusterScanner) scanKafkaAcls(admin client.KafkaAdmin) ([]types.Acls, error) {
	slog.Info("🔍 scanning for kafka acls", "clusterArn", cs.clusterArn)

	acls, err := admin.ListAcls()
	if err != nil {
		return nil, fmt.Errorf("❌ Failed to list acls: %v", err)
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
