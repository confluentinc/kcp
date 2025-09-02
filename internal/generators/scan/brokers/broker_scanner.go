package brokers

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	kafkatypes "github.com/aws/aws-sdk-go-v2/service/kafka/types"

	"github.com/confluentinc/kcp/internal/client"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/confluentinc/kcp/internal/utils"
)

type KafkaAdminFactory func(brokerAddresses []string, clientBrokerEncryptionInTransit kafkatypes.ClientBroker, kafkaVersion string) (client.KafkaAdmin, error)

type BrokerScannerOpts struct {
	AuthType          types.AuthType
	ClusterName       string
	BootstrapServer   string
	SASLScramUsername string
	SASLScramPassword string
	TLSCACert         string
	TLSClientCert     string
	TLSClientKey      string
}

type BrokerScanner struct {
	kafkaAdminFactory KafkaAdminFactory
	clusterInfo       types.ClusterInformation
	opts              *BrokerScannerOpts
}

func NewBrokerScanner(kafkaAdminFactory KafkaAdminFactory, clusterInfo types.ClusterInformation, opts *BrokerScannerOpts) *BrokerScanner {
	return &BrokerScanner{
		kafkaAdminFactory: kafkaAdminFactory,
		clusterInfo:       clusterInfo,
		opts:              opts,
	}
}

func (bs *BrokerScanner) Run() error {
	if bs.opts.BootstrapServer == "" {
		return fmt.Errorf("no bootstrap server found, skipping the broker scan")
	}

	slog.Info(fmt.Sprintf("🚀 starting broker scan for %s using %s authentication", bs.opts.ClusterName, bs.opts.AuthType))	

	ctx := context.TODO()

	brokerInfo, err := bs.ScanBroker(ctx)
	if err != nil {
		return err
	}

	if err := brokerInfo.WriteAsJson(); err != nil {
		return fmt.Errorf("❌ Failed to write broker info to file: %v", err)
	}

	if err := brokerInfo.WriteAsMarkdown(true); err != nil {
		return fmt.Errorf("❌ Failed to write broker info to markdown file: %v", err)
	}

	slog.Info(fmt.Sprintf("✅ broker scan complete for %s", bs.opts.ClusterName))

	return nil
}

func (bs *BrokerScanner) ScanBroker(ctx context.Context) (*types.ClusterInformation, error) {
	if err := bs.scanKafkaResources(&bs.clusterInfo); err != nil {
		return nil, err
	}

	return &bs.clusterInfo, nil
}

func (bs *BrokerScanner) scanKafkaResources(clusterInfo *types.ClusterInformation) error {
	clientBrokerEncryptionInTransit := types.GetClientBrokerEncryptionInTransit(clusterInfo.Cluster)
	kafkaVersion := bs.getKafkaVersion(clusterInfo)

	bootstrapServers := strings.Split(bs.opts.BootstrapServer, ",")
	admin, err := bs.kafkaAdminFactory(bootstrapServers, clientBrokerEncryptionInTransit, kafkaVersion)
	if err != nil {
		return fmt.Errorf("❌ Failed to setup admin client: %v", err)
	}

	defer admin.Close()

	// Get cluster metadata including broker information and ClusterID
	clusterMetadata, err := bs.describeKafkaCluster(admin)
	if err != nil {
		return err
	}

	clusterInfo.ClusterID = clusterMetadata.ClusterID

	topics, err := bs.scanClusterTopics(admin)
	if err != nil {
		return err
	}
	clusterInfo.Topics = topics

	// Serverless clusters do not support Kafka Admin API and instead returns an EOF error - this should be handled gracefully
	if clusterInfo.Cluster.ClusterType == kafkatypes.ClusterTypeProvisioned {
		acls, err := bs.scanKafkaAcls(admin)
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
func (bs *BrokerScanner) describeKafkaCluster(admin client.KafkaAdmin) (*client.ClusterKafkaMetadata, error) {
	slog.Info(fmt.Sprintf("🔍 retrieving cluster ID for cluster %s", bs.opts.ClusterName))

	clusterMetadata, err := admin.GetClusterKafkaMetadata()
	if err != nil {
		return nil, fmt.Errorf("❌ Failed to describe kafka cluster: %v", err)
	}
	return clusterMetadata, nil
}

func (bs *BrokerScanner) scanClusterTopics(admin client.KafkaAdmin) ([]string, error) {
	slog.Info(fmt.Sprintf("🔍 scanning for topics in cluster %s", bs.opts.ClusterName))

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

func (bs *BrokerScanner) scanKafkaAcls(admin client.KafkaAdmin) ([]types.Acls, error) {
	slog.Info(fmt.Sprintf("🔍 scanning for ACLs in cluster %s", bs.opts.ClusterName))

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

func (bs *BrokerScanner) getKafkaVersion(clusterInfo *types.ClusterInformation) string {
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
