package cluster

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/kafka"
	kafkatypes "github.com/aws/aws-sdk-go-v2/service/kafka/types"
	"github.com/confluentinc/kcp/internal/client"
	"github.com/confluentinc/kcp/internal/types"
)

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
	mskService        MSKService
	kafkaAdminFactory KafkaAdminFactory
	region            string
	clusterArn        string
	skipKafka         bool
	authType          types.AuthType
}

type MSKService interface {
	GetBootstrapBrokers(ctx context.Context, clusterArn *string) (*kafka.GetBootstrapBrokersOutput, error)
	ParseBrokerAddresses(brokers kafka.GetBootstrapBrokersOutput, authType types.AuthType) ([]string, error)
	GetCompatibleKafkaVersions(ctx context.Context, clusterArn *string) (*kafka.GetCompatibleKafkaVersionsOutput, error)
	GetClusterPolicy(ctx context.Context, clusterArn *string) (*kafka.GetClusterPolicyOutput, error)
	DescribeClusterV2(ctx context.Context, clusterArn *string) (*kafka.DescribeClusterV2Output, error)
	ListClientVpcConnections(ctx context.Context, clusterArn *string) ([]kafkatypes.ClientVpcConnection, error)
	ListClusterOperationsV2(ctx context.Context, clusterArn *string) ([]kafkatypes.ClusterOperationV2Summary, error)
	ListNodes(ctx context.Context, clusterArn *string) ([]kafkatypes.NodeInfo, error)
	ListScramSecrets(ctx context.Context, clusterArn *string) ([]string, error)
}

// NewClusterScanner creates a new ClusterScanner instance.
func NewClusterScanner(mskService MSKService, kafkaAdminFactory KafkaAdminFactory, opts ClusterScannerOpts) *ClusterScanner {
	return &ClusterScanner{
		mskService:        mskService,
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

	dirPath := filepath.Join("kcp-scan", clusterInfo.Region, aws.ToString(clusterInfo.Cluster.ClusterName))
	if err := os.MkdirAll(dirPath, 0755); err != nil {
		return fmt.Errorf("❌ Failed to create directory structure: %v", err)
	}

	filePath := filepath.Join(dirPath, fmt.Sprintf("%s.json", aws.ToString(clusterInfo.Cluster.ClusterName)))
	if err := clusterInfo.WriteAsJson(filePath); err != nil {
		return err
	}

	// Generate markdown report
	mdFilePath := filepath.Join(dirPath, fmt.Sprintf("%s.md", aws.ToString(clusterInfo.Cluster.ClusterName)))
	if err := clusterInfo.WriteAsMarkdown(mdFilePath); err != nil {
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

	connections, err := cs.scanClusterVpcConnections(ctx, &cs.clusterArn)
	if err != nil {
		return err
	}
	clusterInfo.ClientVpcConnections = connections

	operations, err := cs.scanClusterOperations(ctx, &cs.clusterArn)
	if err != nil {
		return err
	}
	clusterInfo.ClusterOperations = operations

	nodes, err := cs.scanClusterNodes(ctx, &cs.clusterArn)
	if err != nil {
		return err
	}
	clusterInfo.Nodes = nodes

	scramSecrets, err := cs.scanClusterScramSecrets(ctx, &cs.clusterArn)
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

	versions, err := cs.mskService.GetCompatibleKafkaVersions(ctx, clusterArn)
	if err != nil {
		// Check if it's an MSK Serverless error - this should be handled gracefully
		if strings.Contains(err.Error(), "This operation cannot be performed on serverless clusters.") {
			slog.Warn("⚠️ Compatible versions not supported for MSK Serverless clusters, skipping compatible versions scan")
			return &kafka.GetCompatibleKafkaVersionsOutput{
				CompatibleKafkaVersions: []kafkatypes.CompatibleKafkaVersion{},
			}, nil
		}
		return nil, fmt.Errorf("❌ Failed to get compatible versions: %v", err)
	}
	return versions, nil
}

func (cs *ClusterScanner) getClusterPolicy(ctx context.Context, clusterArn *string) (*kafka.GetClusterPolicyOutput, error) {
	slog.Info("🔍 scanning for cluster policy", "clusterArn", cs.clusterArn)

	policy, err := cs.mskService.GetClusterPolicy(ctx, clusterArn)
	if err != nil {
		// Check if it's a NotFoundException - this is expected and should be handled gracefully
		var notFoundErr *kafkatypes.NotFoundException
		if errors.As(err, &notFoundErr) {
			// Return empty policy for NotFoundException - this is expected behavior
			return &kafka.GetClusterPolicyOutput{}, nil
		}
		return nil, err
	}
	return policy, nil
}

func (cs *ClusterScanner) getBootstrapBrokers(ctx context.Context, clusterArn *string) (*kafka.GetBootstrapBrokersOutput, error) {
	slog.Info("🔍 scanning for bootstrap brokers", "clusterArn", cs.clusterArn)

	brokers, err := cs.mskService.GetBootstrapBrokers(ctx, clusterArn)
	if err != nil {
		return nil, fmt.Errorf("❌ Failed to scan brokers: %v", err)
	}
	return brokers, nil
}

func (cs *ClusterScanner) describeCluster(ctx context.Context, clusterArn *string) (*kafka.DescribeClusterV2Output, error) {
	slog.Info("🔍 describing cluster", "clusterArn", cs.clusterArn)

	cluster, err := cs.mskService.DescribeClusterV2(ctx, clusterArn)
	if err != nil {
		return nil, fmt.Errorf("❌ Failed to describe cluster: %v", err)
	}
	return cluster, nil
}

func (cs *ClusterScanner) scanClusterVpcConnections(ctx context.Context, clusterArn *string) ([]kafkatypes.ClientVpcConnection, error) {
	slog.Info("🔍 scanning for client vpc connections", "clusterArn", cs.clusterArn)

	connections, err := cs.mskService.ListClientVpcConnections(ctx, clusterArn)
	if err != nil {
		// Check if it's an MSK Serverless VPC connectivity error - this should be handled gracefully
		if strings.Contains(err.Error(), "This Region doesn't currently support VPC connectivity with Amazon MSK Serverless clusters") {
			slog.Warn("⚠️ VPC connectivity not supported for MSK Serverless clusters in this region, skipping VPC connections scan")
			return []kafkatypes.ClientVpcConnection{}, nil
		}
		return nil, fmt.Errorf("❌ Failed listing client vpc connections: %v", err)
	}
	return connections, nil
}

func (cs *ClusterScanner) scanClusterOperations(ctx context.Context, clusterArn *string) ([]kafkatypes.ClusterOperationV2Summary, error) {
	slog.Info("🔍 scanning for cluster operations", "clusterArn", cs.clusterArn)

	operations, err := cs.mskService.ListClusterOperationsV2(ctx, clusterArn)
	if err != nil {
		return nil, fmt.Errorf("❌ Failed listing operations: %v", err)
	}
	return operations, nil
}

func (cs *ClusterScanner) scanClusterNodes(ctx context.Context, clusterArn *string) ([]kafkatypes.NodeInfo, error) {
	slog.Info("🔍 scanning for cluster nodes", "clusterArn", cs.clusterArn)

	nodes, err := cs.mskService.ListNodes(ctx, clusterArn)
	if err != nil {
		// Check if it's an MSK Serverless error - this should be handled gracefully
		if strings.Contains(err.Error(), "This operation cannot be performed on serverless clusters.") {
			slog.Warn("⚠️ Node listing not supported for MSK Serverless clusters, skipping Nodes scan")
			return []kafkatypes.NodeInfo{}, nil
		}
		return nil, fmt.Errorf("❌ Failed listing nodes: %v", err)
	}

	return nodes, nil
}

func (cs *ClusterScanner) scanClusterScramSecrets(ctx context.Context, clusterArn *string) ([]string, error) {
	slog.Info("🔍 scanning for cluster scram secrets", "clusterArn", cs.clusterArn)

	secrets, err := cs.mskService.ListScramSecrets(ctx, clusterArn)
	if err != nil {
		// Check if it's an MSK Serverless error - this should be handled gracefully
		if strings.Contains(err.Error(), "This operation cannot be performed on serverless clusters.") {
			slog.Warn("⚠️ Scram secret listing not supported for MSK Serverless clusters, skipping scram secrets scan")
			return []string{}, nil
		}
		return nil, fmt.Errorf("❌ Failed listing secrets: %v", err)
	}

	return secrets, nil
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

	brokerAddresses, err := cs.mskService.ParseBrokerAddresses(clusterInfo.BootstrapBrokers, cs.authType)
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
