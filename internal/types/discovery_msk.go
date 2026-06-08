package types

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/kafka"
	kafkatypes "github.com/aws/aws-sdk-go-v2/service/kafka/types"
	kafkaconnecttypes "github.com/aws/aws-sdk-go-v2/service/kafkaconnect/types"
)

// MSKSourcesState contains all MSK-specific data
type MSKSourcesState struct {
	Regions []DiscoveredRegion `json:"regions"`
}

type DiscoveredRegion struct {
	Name           string                                      `json:"name"`
	Configurations []kafka.DescribeConfigurationRevisionOutput `json:"configurations"`
	Costs          CostInformation                             `json:"costs"`
	Clusters       []DiscoveredCluster                         `json:"clusters"`
	// internal only - exclude from JSON output
	ClusterArns []string `json:"-"`
}

// RefreshClusters replaces the cluster list but merges KafkaAdminClientInformation from existing clusters
// New discoveries take precedence over old values (only uses old values when new values are empty/nil)
func (dr *DiscoveredRegion) RefreshClusters(newClusters []DiscoveredCluster) {
	// build map of ARN -> KafkaAdminClientInformation from existing clusters
	adminInfoByArn := make(map[string]KafkaAdminClientInformation)
	for _, existingCluster := range dr.Clusters {
		adminInfoByArn[existingCluster.Arn] = existingCluster.KafkaAdminClientInformation
	}

	// replace cluster list with new discoveries
	dr.Clusters = newClusters

	// merge admin info: new discoveries take precedence, only use old values when new is empty/nil
	for i := range dr.Clusters {
		if oldAdminInfo, exists := adminInfoByArn[dr.Clusters[i].Arn]; exists {
			newAdminInfo := &dr.Clusters[i].KafkaAdminClientInformation
			newAdminInfo.MergeFrom(oldAdminInfo)
		}
	}
}

type DiscoveredCluster struct {
	Name                        string                      `json:"name"`
	Arn                         string                      `json:"arn"`
	Region                      string                      `json:"region"`
	ClusterMetrics              ClusterMetrics              `json:"metrics"`
	AWSClientInformation        AWSClientInformation        `json:"aws_client_information"`
	KafkaAdminClientInformation KafkaAdminClientInformation `json:"kafka_admin_client_information"`
	DiscoveredClients           []DiscoveredClient          `json:"discovered_clients"`
}

type AWSClientInformation struct {
	MskClusterConfig     kafkatypes.Cluster                     `json:"msk_cluster_config"`
	ClientVpcConnections []kafkatypes.ClientVpcConnection       `json:"client_vpc_connections"`
	ClusterOperations    []kafkatypes.ClusterOperationV2Summary `json:"cluster_operations"`
	Nodes                []kafkatypes.NodeInfo                  `json:"nodes"`
	ScramSecrets         []string                               `json:"ScramSecrets"`
	BootstrapBrokers     kafka.GetBootstrapBrokersOutput        `json:"bootstrap_brokers"`
	Policy               kafka.GetClusterPolicyOutput           `json:"policy"`
	CompatibleVersions   kafka.GetCompatibleKafkaVersionsOutput `json:"compatible_versions"`
	ClusterNetworking    ClusterNetworking                      `json:"cluster_networking"`
	Connectors           []ConnectorSummary                     `json:"connectors"`
}

// Returns only one bootstrap broker per authentication type.
func (c *AWSClientInformation) GetBootstrapBrokersForAuthType(authType AuthType) ([]string, error) {
	var brokerList string
	var visibility string
	slog.Info("🔍 parsing broker addresses", "authType", authType)

	switch authType {
	case AuthTypeIAM:
		brokerList = aws.ToString(c.BootstrapBrokers.BootstrapBrokerStringPublicSaslIam)
		visibility = "PUBLIC"
		if brokerList == "" {
			brokerList = aws.ToString(c.BootstrapBrokers.BootstrapBrokerStringSaslIam)
			visibility = "PRIVATE"
		}
		if brokerList == "" {
			return nil, fmt.Errorf("no SASL/IAM brokers found in the cluster")
		}
	case AuthTypeSASLSCRAM:
		brokerList = aws.ToString(c.BootstrapBrokers.BootstrapBrokerStringPublicSaslScram)
		visibility = "PUBLIC"
		if brokerList == "" {
			brokerList = aws.ToString(c.BootstrapBrokers.BootstrapBrokerStringSaslScram)
			visibility = "PRIVATE"
		}
		if brokerList == "" {
			return nil, fmt.Errorf("no SASL/SCRAM brokers found in the cluster")
		}
	case AuthTypeUnauthenticatedTLS:
		brokerList = aws.ToString(c.BootstrapBrokers.BootstrapBrokerStringTls)
		visibility = "PRIVATE"
		if brokerList == "" {
			return nil, fmt.Errorf("no unauthenticated (TLS encryption) brokers found in the cluster")
		}
	case AuthTypeUnauthenticatedPlaintext:
		brokerList = aws.ToString(c.BootstrapBrokers.BootstrapBrokerString)
		visibility = "PRIVATE"
		if brokerList == "" {
			return nil, fmt.Errorf("no unauthenticated (plaintext) brokers found in the cluster")
		}
	case AuthTypeTLS:
		brokerList = aws.ToString(c.BootstrapBrokers.BootstrapBrokerStringPublicTls)
		visibility = "PUBLIC"
		if brokerList == "" {
			brokerList = aws.ToString(c.BootstrapBrokers.BootstrapBrokerStringTls)
			visibility = "PRIVATE"
		}
		if brokerList == "" {
			return nil, fmt.Errorf("no TLS brokers found in the cluster")
		}
	default:
		return nil, fmt.Errorf("auth type: %v not yet supported", authType)
	}

	slog.Info("🔍 found broker addresses", "visibility", visibility, "authType", authType, "addresses", brokerList)

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

// Returns all bootstrap brokers for a given auth type.
func (c *AWSClientInformation) GetAllBootstrapBrokersForAuthType(authType AuthType) ([]string, error) {
	var brokerList []string
	slog.Info("🔍 parsing broker addresses", "authType", authType)

	switch authType {
	case AuthTypeIAM:
		brokerList = append(brokerList, aws.ToString(c.BootstrapBrokers.BootstrapBrokerStringPublicSaslIam))
		brokerList = append(brokerList, aws.ToString(c.BootstrapBrokers.BootstrapBrokerStringSaslIam))
	case AuthTypeSASLSCRAM:
		brokerList = append(brokerList, aws.ToString(c.BootstrapBrokers.BootstrapBrokerStringPublicSaslScram))
		brokerList = append(brokerList, aws.ToString(c.BootstrapBrokers.BootstrapBrokerStringSaslScram))
	case AuthTypeUnauthenticatedTLS:
		brokerList = append(brokerList, aws.ToString(c.BootstrapBrokers.BootstrapBrokerStringTls))
	case AuthTypeUnauthenticatedPlaintext:
		brokerList = append(brokerList, aws.ToString(c.BootstrapBrokers.BootstrapBrokerString))
	case AuthTypeTLS:
		brokerList = append(brokerList, aws.ToString(c.BootstrapBrokers.BootstrapBrokerStringPublicTls))
		brokerList = append(brokerList, aws.ToString(c.BootstrapBrokers.BootstrapBrokerStringTls))
	default:
		return nil, fmt.Errorf("auth type: %v not yet supported", authType)
	}

	slog.Info("🔍 found broker addresses", "authType", authType, "addresses", brokerList)

	rawAddresses := strings.Split(strings.Join(brokerList, ","), ",")
	addresses := make([]string, 0, len(rawAddresses))
	for _, addr := range rawAddresses {
		trimmedAddr := strings.TrimSpace(addr)
		if trimmedAddr != "" {
			addresses = append(addresses, trimmedAddr)
		}
	}
	return addresses, nil
}

type ClusterNetworking struct {
	VpcId          string       `json:"vpc_id"`
	SubnetIds      []string     `json:"subnet_ids"`
	SecurityGroups []string     `json:"security_groups"`
	Subnets        []SubnetInfo `json:"subnets"`
}

type SubnetInfo struct {
	SubnetMskBrokerId int    `json:"subnet_msk_broker_id"`
	SubnetId          string `json:"subnet_id"`
	AvailabilityZone  string `json:"availability_zone"`
	PrivateIpAddress  string `json:"private_ip_address"`
	CidrBlock         string `json:"cidr_block"`
}

type ConnectorSummary struct {
	ConnectorArn                     string                                                        `json:"connector_arn"`
	ConnectorName                    string                                                        `json:"connector_name"`
	ConnectorState                   string                                                        `json:"connector_state"`
	CreationTime                     string                                                        `json:"creation_time"`
	KafkaCluster                     kafkaconnecttypes.ApacheKafkaClusterDescription               `json:"kafka_cluster"`
	KafkaClusterClientAuthentication kafkaconnecttypes.KafkaClusterClientAuthenticationDescription `json:"kafka_cluster_client_authentication"`
	Capacity                         kafkaconnecttypes.CapacityDescription                         `json:"capacity"`
	Plugins                          []kafkaconnecttypes.PluginDescription                         `json:"plugins"`
	ConnectorConfiguration           map[string]string                                             `json:"connector_configuration"`
}
