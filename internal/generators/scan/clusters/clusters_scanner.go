package clusters

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/kafka"
	kafkatypes "github.com/aws/aws-sdk-go-v2/service/kafka/types"

	kafkaservice "github.com/confluentinc/kcp/internal/services/kafka"
	"github.com/confluentinc/kcp/internal/types"
)

// MockMSKService is a mock implementation for direct broker connections
type MockMSKService struct{}

func (m *MockMSKService) ParseBrokerAddresses(brokers kafka.GetBootstrapBrokersOutput, authType types.AuthType) ([]string, error) {
	// This is not used for direct broker connections
	return nil, nil
}

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
	kafkaService *kafkaservice.KafkaService
	clusterInfo  types.ClusterInformation
	opts         *ClustersScannerOpts
}

func NewClustersScanner(kafkaAdminFactory kafkaservice.KafkaAdminFactory, clusterInfo types.ClusterInformation, opts *ClustersScannerOpts) *ClustersScanner {
	mockMSKService := &MockMSKService{}

	kafkaService := kafkaservice.NewKafkaService(kafkaservice.KafkaServiceOpts{
		MSKService:        mockMSKService,
		KafkaAdminFactory: kafkaAdminFactory,
		AuthType:          opts.AuthType,
		ClusterArn:        "", // Not applicable for direct broker connections
	})

	return &ClustersScanner{
		kafkaService: kafkaService,
		clusterInfo:  clusterInfo,
		opts:         opts,
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
	if err := cs.scanKafkaResourcesDirectly(&cs.clusterInfo); err != nil {
		return nil, err
	}

	return &cs.clusterInfo, nil
}

// scanKafkaResourcesDirectly handles direct broker connections without using MSK service
func (cs *ClustersScanner) scanKafkaResourcesDirectly(clusterInfo *types.ClusterInformation) error {
	clientBrokerEncryptionInTransit := types.GetClientBrokerEncryptionInTransit(clusterInfo.Cluster)
	kafkaVersion := cs.kafkaService.GetKafkaVersion(clusterInfo)

	bootstrapServers := strings.Split(cs.opts.BootstrapServer, ",")
	admin, err := cs.kafkaService.CreateKafkaAdmin(bootstrapServers, clientBrokerEncryptionInTransit, kafkaVersion)
	if err != nil {
		return fmt.Errorf("❌ Failed to setup admin client: %v", err)
	}

	defer admin.Close()

	clusterMetadata, err := admin.GetClusterKafkaMetadata()
	if err != nil {
		return fmt.Errorf("❌ Failed to describe kafka cluster: %v", err)
	}

	clusterInfo.ClusterID = clusterMetadata.ClusterID

	topics, err := admin.ListTopics()
	if err != nil {
		return fmt.Errorf("❌ Failed to list topics: %v", err)
	}

	topicList := make([]string, 0, len(topics))
	for topic := range topics {
		topicList = append(topicList, topic)
	}
	clusterInfo.Topics = topicList

	if clusterInfo.Cluster.ClusterType == kafkatypes.ClusterTypeProvisioned {
		acls, err := admin.ListAcls()
		if err != nil {
			return fmt.Errorf("❌ Failed to list acls: %v", err)
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
		clusterInfo.Acls = flattenedAcls
	} else {
		slog.Warn("⚠️ Serverless clusters do not support querying Kafka ACLs, skipping ACLs scan")
	}

	return nil
}
