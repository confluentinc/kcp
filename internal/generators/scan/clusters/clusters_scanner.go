package clusters

import (
	"fmt"
	"log/slog"

	kafkatypes "github.com/aws/aws-sdk-go-v2/service/kafka/types"

	"github.com/confluentinc/kcp/internal/client"
	kafkaservice "github.com/confluentinc/kcp/internal/services/kafka"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/confluentinc/kcp/internal/utils"
)

type ClustersScanner struct {
	StateFile   string
	Credentials types.Credentials
	Discovery   *types.Discovery
}

func NewClustersScanner(stateFile string, credentials types.Credentials) *ClustersScanner {
	return &ClustersScanner{
		StateFile:   stateFile,
		Credentials: credentials,
		Discovery:   &types.Discovery{},
	}
}

func (cs *ClustersScanner) Run() error {
	if err := cs.Discovery.LoadStateFile(cs.StateFile); err != nil {
		return fmt.Errorf("❌ failed to load discovery state: %v", err)
	}

	for _, regionEntry := range cs.Credentials.Regions {
		for _, clusterEntry := range regionEntry.Clusters {
			if err := cs.scanCluster(regionEntry.Name, clusterEntry); err != nil {
				slog.Error("failed to scan cluster", "cluster", clusterEntry.Arn, "error", err)
				continue
			}
		}
	}

	if err := cs.Discovery.PersistStateFile(cs.StateFile); err != nil {
		return fmt.Errorf("❌ failed to save discovery state: %v", err)
	}

	return nil
}

func (cs *ClustersScanner) scanCluster(region string, clusterEntry types.ClusterEntry) error {
	discoveredCluster, err := cs.getClusterFromDiscovery(region, clusterEntry.Arn)
	if err != nil {
		return fmt.Errorf("❌ failed to get cluster from discovery state: %v", err)
	}

	authType, err := clusterEntry.GetSelectedAuthType()
	if err != nil {
		return fmt.Errorf("❌ failed to determine auth type for cluster: %s in region: %s: %v", clusterEntry.Arn, region, err)
	}

	slog.Info(fmt.Sprintf("🚀 starting broker scan for %s using %s authentication", clusterEntry.Arn, authType))

	brokerAddresses, err := discoveredCluster.AWSClientInformation.GetBootstrapBrokersForAuthType(authType)
	if err != nil {
		return fmt.Errorf("❌ failed to get broker addresses for cluster: %s in region: %s: %v", clusterEntry.Arn, region, err)
	}

	clientBrokerEncryptionInTransit := utils.GetClientBrokerEncryptionInTransit(discoveredCluster.AWSClientInformation.MskClusterConfig)
	kafkaVersion := utils.GetKafkaVersion(discoveredCluster.AWSClientInformation)

	kafkaAdmin, err := createKafkaAdmin(authType, brokerAddresses, clientBrokerEncryptionInTransit, region, kafkaVersion, clusterEntry)
	if err != nil {
		return fmt.Errorf("❌ failed to create Kafka admin: %v", err)
	}

	kafkaService := kafkaservice.NewKafkaService(*kafkaAdmin, kafkaservice.KafkaServiceOpts{
		AuthType:   authType,
		ClusterArn: clusterEntry.Arn,
	})

	if err := cs.scanKafkaResources(discoveredCluster, kafkaService); err != nil {
		return fmt.Errorf("❌ failed to scan Kafka resources: %v", err)
	}

	slog.Info(fmt.Sprintf("✅ broker scan complete for %s", clusterEntry.Arn))

	return nil
}

func (cs *ClustersScanner) scanKafkaResources(discoveredCluster *types.DiscoveredCluster, kafkaService *kafkaservice.KafkaService) error {
	clusterMetadata, err := kafkaService.DescribeKafkaCluster()
	if err != nil {
		return fmt.Errorf("❌ Failed to describe kafka cluster: %v", err)
	}
	discoveredCluster.KafkaAdminClientInformation.ClusterID = clusterMetadata.ClusterID

	topics, err := kafkaService.ScanClusterTopics()
	if err != nil {
		return fmt.Errorf("❌ Failed to list topics: %v", err)
	}

	discoveredCluster.KafkaAdminClientInformation.SetTopics(topics)

	// Use KafkaService's ACL scanning logic instead of duplicating it
	if discoveredCluster.AWSClientInformation.MskClusterConfig.ClusterType == kafkatypes.ClusterTypeProvisioned {
		acls, err := kafkaService.ScanKafkaAcls()
		if err != nil {
			return err
		}
		discoveredCluster.KafkaAdminClientInformation.Acls = acls
	} else {
		slog.Warn("⚠️ Serverless clusters do not support querying Kafka ACLs, skipping ACLs scan")
	}

	discoveredCluster.KafkaAdminClientInformation.Topics.Summary = discoveredCluster.KafkaAdminClientInformation.CalculateTopicSummary()

	return nil
}

func (cs *ClustersScanner) getClusterFromDiscovery(region, clusterArn string) (*types.DiscoveredCluster, error) {
	for i, discoveryRegion := range cs.Discovery.Regions {
		if discoveryRegion.Name == region {
			for j, discoveryCluster := range discoveryRegion.Clusters {
				if discoveryCluster.Arn == clusterArn {
					return &cs.Discovery.Regions[i].Clusters[j], nil
				}
			}
		}
	}

	return nil, fmt.Errorf("cluster %s not found in region %s", clusterArn, region)
}

func createKafkaAdmin(authType types.AuthType, brokerAddresses []string, clientBrokerEncryptionInTransit kafkatypes.ClientBroker, region string, kafkaVersion string, clusterEntry types.ClusterEntry) (*client.KafkaAdmin, error) {
	var kafkaAdmin client.KafkaAdmin
	var adminError error
	switch authType {
	case types.AuthTypeIAM:
		kafkaAdmin, adminError = client.NewKafkaAdmin(brokerAddresses, clientBrokerEncryptionInTransit, region, kafkaVersion, client.WithIAMAuth())
	case types.AuthTypeSASLSCRAM:
		kafkaAdmin, adminError = client.NewKafkaAdmin(brokerAddresses, clientBrokerEncryptionInTransit, region, kafkaVersion, client.WithSASLSCRAMAuth(clusterEntry.AuthMethod.SASLScram.Username, clusterEntry.AuthMethod.SASLScram.Password))
	case types.AuthTypeUnauthenticated:
		kafkaAdmin, adminError = client.NewKafkaAdmin(brokerAddresses, clientBrokerEncryptionInTransit, region, kafkaVersion, client.WithUnauthenticatedAuth())
	case types.AuthTypeTLS:
		kafkaAdmin, adminError = client.NewKafkaAdmin(brokerAddresses, clientBrokerEncryptionInTransit, region, kafkaVersion, client.WithTLSAuth(clusterEntry.AuthMethod.TLS.CACert, clusterEntry.AuthMethod.TLS.ClientCert, clusterEntry.AuthMethod.TLS.ClientKey))
	default:
		return nil, fmt.Errorf("❌ Auth type: %v not yet supported", authType)
	}

	if adminError != nil {
		return nil, fmt.Errorf("❌ failed to create Kafka admin: %v", adminError)
	}

	return &kafkaAdmin, nil
}
