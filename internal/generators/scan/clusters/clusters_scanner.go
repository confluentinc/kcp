package clusters

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	// "path/filepath"
	"strings"

	kafkatypes "github.com/aws/aws-sdk-go-v2/service/kafka/types"

	// "github.com/confluentinc/kcp/internal/build_info"
	"github.com/confluentinc/kcp/internal/client"
	kafkaservice "github.com/confluentinc/kcp/internal/services/kafka"
	"github.com/confluentinc/kcp/internal/types"
)

type ClustersScanner struct {
	StateFile string
	Credentials types.Credentials
}

func NewClustersScanner(stateFile string, credentials types.Credentials) *ClustersScanner {
	return &ClustersScanner{
		StateFile: stateFile,
		Credentials: credentials,
	}
}

func (cs *ClustersScanner) Run() error {
	for _, regionEntry := range cs.Credentials.Regions {
		for _, clusterEntry := range regionEntry.Clusters {
			if err := cs.scanCluster(regionEntry.Name, clusterEntry); err != nil {
				slog.Error("failed to scan cluster", "cluster", clusterEntry.Arn, "error", err)
				continue
			}
		}
	}

	return nil
}

func (cs *ClustersScanner) scanCluster(region string, clusterEntry types.ClusterEntry) error {
	clusterName, err := cs.getClusterName(clusterEntry.Arn)
	if err != nil {
		return fmt.Errorf("❌ failed to get cluster name from cluster ARN: %v", err)
	}
	
	discoveredCluster, err :=cs.getClusterFromStateFile(region, clusterName)
	if err != nil {
		return fmt.Errorf("❌ failed to get cluster from %s: %v", cs.StateFile, err)
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

	// if err := clusterInfo.WriteAsJsonWithBase(cs.DiscoverDir); err != nil {
	// 	return fmt.Errorf("❌ Failed to write broker info to file: %v", err)
	// }

	// if err := clusterInfo.WriteAsMarkdownWithBase(cs.DiscoverDir, true); err != nil {
	// 	return fmt.Errorf("❌ Failed to write broker info to markdown file: %v", err)
	// }

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
		discoveredCluster.KafkaAdminClientInformation.Topics = append(discoveredCluster.KafkaAdminClientInformation.Topics, topic.Name)
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

func (cs *ClustersScanner) getClusterFromStateFile(region, clusterName string) (*types.DiscoveredCluster, error) {
	file, err := os.ReadFile(cs.StateFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read state file: %v", err)
	}

	var discovery types.Discovery
	if err := json.Unmarshal(file, &discovery); err != nil {
		return nil, fmt.Errorf("failed to unmarshal discovery: %v", err)
	}

	for i, discoveredRegion := range discovery.Regions {
		if discoveredRegion.Name == region {
			for j, discoveredCluster := range discoveredRegion.Clusters {
				if discoveredCluster.Name == clusterName {
					return &discovery.Regions[i].Clusters[j], nil
				}
			}
		}
	}

	return nil, fmt.Errorf("cluster %s not found in region %s", clusterName, region)
}
