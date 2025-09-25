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

type ClustersScannerKafkaService interface {
	ScanKafkaResources(clusterType kafkatypes.ClusterType) (*types.KafkaAdminClientInformation, error)
}

type ClustersScanner struct {
	StateFile   string
	Credentials types.Credentials
	State       *types.State
}

type ClustersScannerOpts struct {
	StateFile   string
	Credentials types.Credentials
}

func NewClustersScanner(opts ClustersScannerOpts) *ClustersScanner {
	return &ClustersScanner{
		StateFile:   opts.StateFile,
		Credentials: opts.Credentials,
		State:       &types.State{},
	}
}

func (cs *ClustersScanner) Run() error {
	if err := cs.State.LoadStateFile(cs.StateFile); err != nil {
		return fmt.Errorf("❌ failed to load state: %v", err)
	}

	for _, regionAuth := range cs.Credentials.Regions {
		for _, clusterAuth := range regionAuth.Clusters {
			if err := cs.scanCluster(regionAuth.Name, clusterAuth); err != nil {
				slog.Error("failed to scan cluster", "cluster", clusterAuth.Arn, "error", err)
				continue
			}
		}
	}

	if err := cs.State.PersistStateFile(cs.StateFile); err != nil {
		return fmt.Errorf("❌ failed to save discovery state: %v", err)
	}

	return nil
}

func (cs *ClustersScanner) scanCluster(region string, clusterAuth types.ClusterAuth) error {
	discoveredCluster, err := cs.getClusterFromDiscovery(region, clusterAuth.Arn)
	if err != nil {
		return fmt.Errorf("❌ failed to get cluster from discovery state: %v", err)
	}

	authType, err := clusterAuth.GetSelectedAuthType()
	if err != nil {
		return fmt.Errorf("❌ failed to determine auth type for cluster: %s in region: %s: %v", clusterAuth.Arn, region, err)
	}

	slog.Info(fmt.Sprintf("🚀 starting broker scan for %s using %s authentication", clusterAuth.Arn, authType))

	brokerAddresses, err := discoveredCluster.AWSClientInformation.GetBootstrapBrokersForAuthType(authType)
	if err != nil {
		return fmt.Errorf("❌ failed to get broker addresses for cluster: %s in region: %s: %v", clusterAuth.Arn, region, err)
	}

	clientBrokerEncryptionInTransit := utils.GetClientBrokerEncryptionInTransit(discoveredCluster.AWSClientInformation.MskClusterConfig)
	kafkaVersion := utils.GetKafkaVersion(discoveredCluster.AWSClientInformation)

	kafkaAdmin, err := createKafkaAdmin(authType, brokerAddresses, clientBrokerEncryptionInTransit, region, kafkaVersion, clusterAuth)
	if err != nil {
		return fmt.Errorf("❌ failed to create Kafka admin: %v", err)
	}

	kafkaService := kafkaservice.NewKafkaService(*kafkaAdmin, kafkaservice.KafkaServiceOpts{
		AuthType:   authType,
		ClusterArn: clusterAuth.Arn,
	})

	if err := cs.scanKafkaResources(discoveredCluster, kafkaService); err != nil {
		return fmt.Errorf("❌ failed to scan Kafka resources: %v", err)
	}

	slog.Info(fmt.Sprintf("✅ broker scan complete for %s", clusterAuth.Arn))

	return nil
}

func (cs *ClustersScanner) scanKafkaResources(discoveredCluster *types.DiscoveredCluster, kafkaService ClustersScannerKafkaService) error {
	clusterType := discoveredCluster.AWSClientInformation.MskClusterConfig.ClusterType

	kafkaAdminClientInformation, err := kafkaService.ScanKafkaResources(clusterType)
	if err != nil {
		return fmt.Errorf("❌ failed to scan Kafka resources: %v", err)
	}
	discoveredCluster.KafkaAdminClientInformation = *kafkaAdminClientInformation

	return nil
}

func (cs *ClustersScanner) getClusterFromDiscovery(region, clusterArn string) (*types.DiscoveredCluster, error) {
	for i, currentRegion := range cs.State.Regions {
		if currentRegion.Name == region {
			for j, currentCluster := range currentRegion.Clusters {
				if currentCluster.Arn == clusterArn {
					return &cs.State.Regions[i].Clusters[j], nil
				}
			}
		}
	}

	return nil, fmt.Errorf("cluster %s not found in region %s", clusterArn, region)
}

// todo can this be moved?
func createKafkaAdmin(authType types.AuthType, brokerAddresses []string, clientBrokerEncryptionInTransit kafkatypes.ClientBroker, region string, kafkaVersion string, clusterAuth types.ClusterAuth) (*client.KafkaAdmin, error) {
	var kafkaAdmin client.KafkaAdmin
	var err error
	switch authType {
	case types.AuthTypeIAM:
		kafkaAdmin, err = client.NewKafkaAdmin(brokerAddresses, clientBrokerEncryptionInTransit, region, kafkaVersion, client.WithIAMAuth())
	case types.AuthTypeSASLSCRAM:
		kafkaAdmin, err = client.NewKafkaAdmin(brokerAddresses, clientBrokerEncryptionInTransit, region, kafkaVersion, client.WithSASLSCRAMAuth(clusterAuth.AuthMethod.SASLScram.Username, clusterAuth.AuthMethod.SASLScram.Password))
	case types.AuthTypeUnauthenticatedTLS:
		kafkaAdmin, err = client.NewKafkaAdmin(brokerAddresses, clientBrokerEncryptionInTransit, region, kafkaVersion, client.WithUnauthenticatedTlsAuth())
	case types.AuthTypeUnauthenticatedPlaintext:
		kafkaAdmin, err = client.NewKafkaAdmin(brokerAddresses, clientBrokerEncryptionInTransit, region, kafkaVersion, client.WithUnauthenticatedPlaintextAuth())
	case types.AuthTypeTLS:
		kafkaAdmin, err = client.NewKafkaAdmin(brokerAddresses, clientBrokerEncryptionInTransit, region, kafkaVersion, client.WithTLSAuth(clusterAuth.AuthMethod.TLS.CACert, clusterAuth.AuthMethod.TLS.ClientCert, clusterAuth.AuthMethod.TLS.ClientKey))
	default:
		return nil, fmt.Errorf("❌ Auth type: %v not yet supported", authType)
	}

	if err != nil {
		return nil, fmt.Errorf("❌ failed to create Kafka admin: %v", err)
	}

	return &kafkaAdmin, nil
}
