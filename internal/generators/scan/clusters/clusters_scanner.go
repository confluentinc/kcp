package clusters

import (
	"fmt"
	"log/slog"
	"strings"

	kafkatypes "github.com/aws/aws-sdk-go-v2/service/kafka/types"

	// "github.com/confluentinc/kcp/internal/build_info"
	"github.com/confluentinc/kcp/internal/client"
	kafkaservice "github.com/confluentinc/kcp/internal/services/kafka"
	"github.com/confluentinc/kcp/internal/types"
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
	}
}

func (cs *ClustersScanner) Run() error {
	if cs.Discovery == nil {
		cs.Discovery = &types.Discovery{}
	}

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
	clusterName, err := cs.getClusterName(clusterEntry.Arn)
	if err != nil {
		return fmt.Errorf("❌ failed to get cluster name from cluster ARN: %v", err)
	}

	discoveredCluster, err := cs.getClusterFromDiscovery(region, clusterName)
	if err != nil {
		return fmt.Errorf("❌ failed to get cluster from discovery state: %v", err)
	}

	// clusterInfo.KcpBuildInfo = types.KcpBuildInfo{
	// 	Version: build_info.Version,
	// 	Commit:  build_info.Commit,
	// 	Date:    build_info.Date,
	// }

	authType, err := clusterEntry.GetSelectedAuthType()
	if err != nil {
		return fmt.Errorf("❌ failed to determine auth type for cluster: %s in region: %s: %v", clusterName, region, err)
	}

	slog.Info(fmt.Sprintf("🚀 starting broker scan for %s using %s authentication", clusterName, authType))

	brokerAddresses, err := discoveredCluster.AWSClientInformation.GetBootstrapBrokersForAuthType(authType)
	if err != nil {
		return fmt.Errorf("❌ failed to get broker addresses for cluster: %s in region: %s: %v", clusterName, region, err)
	}

	kafkaAdminFactory := func(brokerAddresses []string, clientBrokerEncryptionInTransit kafkatypes.ClientBroker, kafkaVersion string) (client.KafkaAdmin, error) {
		switch authType {
		case types.AuthTypeIAM:
			return client.NewKafkaAdmin(brokerAddresses, clientBrokerEncryptionInTransit, region, kafkaVersion, client.WithIAMAuth())
		case types.AuthTypeSASLSCRAM:
			return client.NewKafkaAdmin(brokerAddresses, clientBrokerEncryptionInTransit, region, kafkaVersion, client.WithSASLSCRAMAuth(clusterEntry.AuthMethod.SASLScram.Username, clusterEntry.AuthMethod.SASLScram.Password))
		case types.AuthTypeUnauthenticated:
			return client.NewKafkaAdmin(brokerAddresses, clientBrokerEncryptionInTransit, region, kafkaVersion, client.WithUnauthenticatedAuth())
		case types.AuthTypeTLS:
			return client.NewKafkaAdmin(brokerAddresses, clientBrokerEncryptionInTransit, region, kafkaVersion, client.WithTLSAuth(clusterEntry.AuthMethod.TLS.CACert, clusterEntry.AuthMethod.TLS.ClientCert, clusterEntry.AuthMethod.TLS.ClientKey))
		default:
			return nil, fmt.Errorf("❌ Auth type: %v not yet supported", authType)
		}
	}

	kafkaService := kafkaservice.NewKafkaService(kafkaservice.KafkaServiceOpts{
		KafkaAdminFactory: kafkaAdminFactory,
		AuthType:          authType,
		ClusterArn:        clusterEntry.Arn,
	})

	if err := cs.scanKafkaResources(discoveredCluster, kafkaService, brokerAddresses); err != nil {
		return fmt.Errorf("❌ failed to scan Kafka resources: %v", err)
	}

	slog.Info(fmt.Sprintf("✅ broker scan complete for %s", clusterName))

	return nil
}

func (cs *ClustersScanner) scanKafkaResources(discoveredCluster *types.DiscoveredCluster, kafkaService *kafkaservice.KafkaService, brokerAddresses []string) error {
	clientBrokerEncryptionInTransit := types.GetClientBrokerEncryptionInTransit(discoveredCluster.AWSClientInformation.MskClusterConfig)
	kafkaVersion := kafkaService.GetKafkaVersion(discoveredCluster.AWSClientInformation)

	kafkaAdmin, err := kafkaService.CreateKafkaAdmin(brokerAddresses, clientBrokerEncryptionInTransit, kafkaVersion)
	if err != nil {
		return fmt.Errorf("❌ Failed to setup admin client: %v", err)
	}

	defer kafkaAdmin.Close()

	clusterMetadata, err := kafkaService.DescribeKafkaCluster(kafkaAdmin)
	if err != nil {
		return fmt.Errorf("❌ Failed to describe kafka cluster: %v", err)
	}
	discoveredCluster.KafkaAdminClientInformation.ClusterID = clusterMetadata.ClusterID

	topics, err := kafkaService.ScanClusterTopics(kafkaAdmin)
	if err != nil {
		return fmt.Errorf("❌ Failed to list topics: %v", err)
	}

	for _, topic := range topics {
		discoveredCluster.KafkaAdminClientInformation.Topics.Details = append(discoveredCluster.KafkaAdminClientInformation.Topics.Details, topic)
	}

	// Use KafkaService's ACL scanning logic instead of duplicating it
	if discoveredCluster.AWSClientInformation.MskClusterConfig.ClusterType == kafkatypes.ClusterTypeProvisioned {
		acls, err := kafkaService.ScanKafkaAcls(kafkaAdmin)
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

func (cs *ClustersScanner) getClusterName(arn string) (string, error) {
	arnParts := strings.Split(arn, "/")
	if len(arnParts) < 2 {
		return "", fmt.Errorf("invalid cluster ARN format: %s", arn)
	}

	clusterName := arnParts[1]
	if clusterName == "" {
		return "", fmt.Errorf("cluster name not found in cluster ARN: %s", arn)
	}

	return clusterName, nil
}

func (cs *ClustersScanner) getClusterFromDiscovery(region, clusterName string) (*types.DiscoveredCluster, error) {
	for i, discoveryRegion := range cs.Discovery.Regions {
		if discoveryRegion.Name == region {
			for j, discoveryCluster := range discoveryRegion.Clusters {
				if discoveryCluster.Name == clusterName {
					return &cs.Discovery.Regions[i].Clusters[j], nil
				}
			}
		}
	}

	return nil, fmt.Errorf("cluster %s not found in region %s", clusterName, region)
}
