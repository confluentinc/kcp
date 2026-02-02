package clusters

import (
	"fmt"
	"log/slog"
	"strconv"

	kafkatypes "github.com/aws/aws-sdk-go-v2/service/kafka/types"

	"github.com/confluentinc/kcp/internal/client"
	kafkaservice "github.com/confluentinc/kcp/internal/services/kafka"
	"github.com/confluentinc/kcp/internal/services/markdown"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/confluentinc/kcp/internal/utils"
)

type ClustersScannerKafkaService interface {
	ScanKafkaResources(clusterType kafkatypes.ClusterType) (*types.KafkaAdminClientInformation, error)
}

type ClustersScanner struct {
	StateFile   string
	State       types.State
	Credentials types.Credentials
}

type ClustersScannerOpts struct {
	StateFile   string
	State       types.State
	Credentials types.Credentials
}

func NewClustersScanner(opts ClustersScannerOpts) *ClustersScanner {
	return &ClustersScanner{
		StateFile:   opts.StateFile,
		State:       opts.State,
		Credentials: opts.Credentials,
	}
}

func (cs *ClustersScanner) Run() error {
	for _, regionAuth := range cs.Credentials.Regions {
		for _, clusterAuth := range regionAuth.Clusters {
			if err := cs.scanCluster(regionAuth.Name, clusterAuth); err != nil {
				slog.Info("⏭️ skipping cluster", "cluster", clusterAuth.Name, "error", err)
				continue
			}
		}
	}

	if err := cs.State.PersistStateFile(cs.StateFile); err != nil {
		return fmt.Errorf("❌ failed to save discovery state: %v", err)
	}

	if err := cs.outputExecutiveSummary(); err != nil {
		slog.Warn("failed to output executive summary", "error", err)
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

func (cs *ClustersScanner) outputExecutiveSummary() error {
	// Only include clusters that have had been hit against the KAfka Admin API.
	allClusters := []types.DiscoveredCluster{}
	for _, region := range cs.State.Regions {
		for _, cluster := range region.Clusters {
			if cluster.KafkaAdminClientInformation.ClusterID != "" {
				allClusters = append(allClusters, cluster)
			}
		}
	}

	if len(allClusters) == 0 {
		return nil
	}

	md := markdown.New()
	md.AddHeading("Scan Summary", 1)
	md.AddParagraph("This report shows a summary of scanned Kafka resources across all clusters. More detailed information can be found in the `kcp ui`.")

	headers := []string{"Cluster Name", "Topics", "Internal Topics", "Total Partitions", "Total Internal Partitions", "Compact Topics", "Compact Partitions", "Tiered Storage Topics"}
	data := [][]string{}
	for _, cluster := range allClusters {
		if cluster.KafkaAdminClientInformation.Topics != nil {
			summary := cluster.KafkaAdminClientInformation.Topics.Summary
			data = append(data, []string{
				cluster.Name,
				strconv.Itoa(summary.Topics),
				strconv.Itoa(summary.InternalTopics),
				strconv.Itoa(summary.TotalPartitions),
				strconv.Itoa(summary.TotalInternalPartitions),
				strconv.Itoa(summary.CompactTopics),
				strconv.Itoa(summary.CompactPartitions),
				strconv.Itoa(summary.RemoteStorageTopics),
			})
		}
	}
	// NOTE: In theory, there should always be topics because of the internal topics, but we don't have a test cluster availabe to prove this.
	if len(data) > 0 {
		md.AddHeading("Topics", 2)
		md.AddTable(headers, data)

		md.AddParagraph("Note: Missing topics may indicate insufficient user permissions to describe the cluster or topics.")
	}

	for _, cluster := range allClusters {
		md.AddHeading("Principals & ACLs - " + cluster.Name, 3)
		headers = []string{"Principal", "Total ACLs"}
		aclsByPrincipal := make(map[string]int)

		for _, acl := range cluster.KafkaAdminClientInformation.Acls {
			aclsByPrincipal[acl.Principal]++
		}
		
		if len(aclsByPrincipal) > 0 {
			data = [][]string{}
		
			for principal, count := range aclsByPrincipal {
				data = append(data, []string{principal, strconv.Itoa(count)})
			}
		
			md.AddTable(headers, data)
		}
	}

	connectorsByState := cs.getConnectorsByState(allClusters)
	if len(connectorsByState) > 0 {
		md.AddHeading("Self-Managed Connectors", 2)
		headers := []string{"State", "Count"}
		data := [][]string{}
		for state, count := range connectorsByState {
			data = append(data, []string{state, strconv.Itoa(count)})
		}
		md.AddTable(headers, data)
	}

	return md.Print(markdown.PrintOptions{ToTerminal: true, ToFile: ""})
}

func (cs *ClustersScanner) getACLsByPrincipal(clusters []types.DiscoveredCluster) map[string]int {
	aclsByPrincipal := make(map[string]int)
	for _, cluster := range clusters {
		for _, acl := range cluster.KafkaAdminClientInformation.Acls {
			aclsByPrincipal[acl.Principal]++
		}
	}
	return aclsByPrincipal
}

func (cs *ClustersScanner) getConnectorsByState(clusters []types.DiscoveredCluster) map[string]int {
	connectorsByState := make(map[string]int)
	for _, cluster := range clusters {
		if cluster.KafkaAdminClientInformation.SelfManagedConnectors != nil {
			for _, connector := range cluster.KafkaAdminClientInformation.SelfManagedConnectors.Connectors {
				state := connector.State
				if state == "" {
					state = "unknown state"
				}
				connectorsByState[state]++
			}
		}
	}
	return connectorsByState
}
