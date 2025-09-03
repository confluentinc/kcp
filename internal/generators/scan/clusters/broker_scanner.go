package clusters

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	kafkatypes "github.com/aws/aws-sdk-go-v2/service/kafka/types"

	"github.com/confluentinc/kcp/internal/client"
	kafkaservice "github.com/confluentinc/kcp/internal/services/kafka"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/confluentinc/kcp/internal/utils"
)

type ClustersScannerOpts struct {
	AuthType          types.AuthType
	ClusterName       string
	BootstrapServer   string
	SASLScramUsername string
	SASLScramPassword string
	TLSCACert         string
	TLSClientCert     string
	TLSClientKey      string
}

type ClustersScanner struct {
	kafkaAdminFactory kafkaservice.KafkaAdminFactory
	clusterInfo       types.ClusterInformation
	opts              *ClustersScannerOpts
}

func NewClustersScanner(kafkaAdminFactory kafkaservice.KafkaAdminFactory, clusterInfo types.ClusterInformation, opts *ClustersScannerOpts) *ClustersScanner {
	return &ClustersScanner{
		kafkaAdminFactory: kafkaAdminFactory,
		clusterInfo:       clusterInfo,
		opts:              opts,
	}
}

func (cs *ClustersScanner) Run() error {
	if cs.opts.BootstrapServer == "" {
		return fmt.Errorf("no bootstrap server found, skipping the broker scan")
	}

	slog.Info(fmt.Sprintf("🚀 starting broker scan for %s using %s authentication", cs.opts.ClusterName, cs.opts.AuthType))

	ctx := context.TODO()

	brokerInfo, err := cs.ScanClusters(ctx)
	if err != nil {
		return err
	}

	if err := brokerInfo.WriteAsJson(); err != nil {
		return fmt.Errorf("❌ Failed to write broker info to file: %v", err)
	}

	if err := brokerInfo.WriteAsMarkdown(true); err != nil {
		return fmt.Errorf("❌ Failed to write broker info to markdown file: %v", err)
	}

	slog.Info(fmt.Sprintf("✅ broker scan complete for %s", cs.opts.ClusterName))

	return nil
}

func (cs *ClustersScanner) ScanClusters(ctx context.Context) (*types.ClusterInformation, error) {
	if err := cs.scanKafkaResources(&cs.clusterInfo); err != nil {
		return nil, err
	}

	return &cs.clusterInfo, nil
}

func (cs *ClustersScanner) scanKafkaResources(clusterInfo *types.ClusterInformation) error {
	clientBrokerEncryptionInTransit := types.GetClientBrokerEncryptionInTransit(clusterInfo.Cluster)
	kafkaVersion := cs.getKafkaVersion(clusterInfo)

	bootstrapServers := strings.Split(cs.opts.BootstrapServer, ",")
	admin, err := cs.kafkaAdminFactory(bootstrapServers, clientBrokerEncryptionInTransit, kafkaVersion)
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

	// Serverless clusters do not support Kafka Admin API and instead returns an EOF error - this should be handled gracefully
	if clusterInfo.Cluster.ClusterType == kafkatypes.ClusterTypeProvisioned {
		acls, err := cs.scanKafkaAcls(admin)
		if err != nil {
			return err
		}
		clusterInfo.Acls = acls
	} else {
		slog.Warn("⚠️ Serverless clusters do not support querying Kafka ACLs, skipping ACLs scan")
	}

	return nil
}

// retrieveClusterId gets cluster metadata and returns the cluster ID along with logging information
func (cs *ClustersScanner) describeKafkaCluster(admin client.KafkaAdmin) (*client.ClusterKafkaMetadata, error) {
	slog.Info(fmt.Sprintf("🔍 retrieving cluster ID for cluster %s", cs.opts.ClusterName))

	clusterMetadata, err := admin.GetClusterKafkaMetadata()
	if err != nil {
		return nil, fmt.Errorf("❌ Failed to describe kafka cluster: %v", err)
	}
	return clusterMetadata, nil
}

func (cs *ClustersScanner) scanClusterTopics(admin client.KafkaAdmin) ([]string, error) {
	slog.Info(fmt.Sprintf("🔍 scanning for topics in cluster %s", cs.opts.ClusterName))

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

func (cs *ClustersScanner) scanKafkaAcls(admin client.KafkaAdmin) ([]types.Acls, error) {
	slog.Info(fmt.Sprintf("🔍 scanning for ACLs in cluster %s", cs.opts.ClusterName))

	acls, err := admin.ListAcls()
	if err != nil {
		return nil, fmt.Errorf("❌ Failed to list acls: %v", err)
	}

	// Flatten the ACLs for easier processing
	var flattenedAcls []types.Acls
	for _, resourceAcl := range acls {
		for _, acl := range resourceAcl.Acls {
			flattenedAcl := types.Acls{
				ResourceType:        resourceAcl.Resource.ResourceType.String(),
				ResourceName:        resourceAcl.Resource.ResourceName,
				ResourcePatternType: resourceAcl.Resource.ResourcePatternType.String(),
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

func (cs *ClustersScanner) getKafkaVersion(clusterInfo *types.ClusterInformation) string {
	switch clusterInfo.Cluster.ClusterType {
	case kafkatypes.ClusterTypeProvisioned:
		return utils.ConvertKafkaVersion(clusterInfo.Cluster.Provisioned.CurrentBrokerSoftwareInfo.KafkaVersion)
	case kafkatypes.ClusterTypeServerless:
		slog.Warn("⚠️ Serverless clusters do not return a Kafka version, defaulting to 4.0.0")
		return "4.0.0"
	default:
		slog.Warn(fmt.Sprintf("⚠️ Unknown cluster type: %v, defaulting to 4.0.0", clusterInfo.Cluster.ClusterType))
		return "4.0.0"
	}
}
