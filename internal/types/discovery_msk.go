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

// mergeClusterPreservingAdminInfo returns newCluster with its KafkaAdminClientInformation
// merged from existing (new discoveries take precedence; old scan-acquired values such as
// ACLs / self-managed connectors are preserved when the new value is empty/nil).
//
// MSK connectors live on AWSClientInformation, which MergeFrom does not reach, so they are
// merged explicitly here: a denied or empty ListConnectors re-run must not wipe connectors
// already in state. Only Connectors is merge-preserved; every other AWSClientInformation
// field keeps its wholesale-replace semantics.
func mergeClusterPreservingAdminInfo(existing, newCluster DiscoveredCluster) DiscoveredCluster {
	newCluster.KafkaAdminClientInformation.MergeFrom(existing.KafkaAdminClientInformation)
	newCluster.AWSClientInformation.Connectors = mergeConnectors(
		newCluster.AWSClientInformation.Connectors,
		existing.AWSClientInformation.Connectors,
	)
	return newCluster
}

// mergeConnectors merges MSK connectors, with new taking precedence for duplicates
// (by ConnectorName). Mirrors mergeSelfManagedConnectors: a nil/empty new set preserves
// the old set (so a denied/empty re-run does not wipe prior connectors), and a nil/empty
// old set yields the new set.
func mergeConnectors(newConns, oldConns []ConnectorSummary) []ConnectorSummary {
	if len(newConns) == 0 {
		return oldConns
	}
	if len(oldConns) == 0 {
		return newConns
	}

	connectorsByName := make(map[string]ConnectorSummary, len(oldConns)+len(newConns))
	for _, c := range oldConns {
		connectorsByName[c.ConnectorName] = c
	}
	for _, c := range newConns {
		connectorsByName[c.ConnectorName] = c // new takes precedence
	}

	merged := make([]ConnectorSummary, 0, len(connectorsByName))
	for _, c := range connectorsByName {
		merged = append(merged, c)
	}
	return merged
}

// RefreshClusters replaces the cluster list but merges KafkaAdminClientInformation from existing clusters
// New discoveries take precedence over old values (only uses old values when new values are empty/nil)
func (dr *DiscoveredRegion) RefreshClusters(newClusters []DiscoveredCluster) {
	existingByArn := make(map[string]DiscoveredCluster)
	for _, existingCluster := range dr.Clusters {
		existingByArn[existingCluster.Arn] = existingCluster
	}

	dr.Clusters = newClusters

	for i := range dr.Clusters {
		if existing, exists := existingByArn[dr.Clusters[i].Arn]; exists {
			dr.Clusters[i] = mergeClusterPreservingAdminInfo(existing, dr.Clusters[i])
		}
	}
}

// UpsertCluster creates or replaces a single cluster by ARN, preserving every other
// cluster in the region and merging the targeted cluster's scan-acquired admin info.
func (dr *DiscoveredRegion) UpsertCluster(newCluster DiscoveredCluster) {
	for i := range dr.Clusters {
		if dr.Clusters[i].Arn == newCluster.Arn {
			dr.Clusters[i] = mergeClusterPreservingAdminInfo(dr.Clusters[i], newCluster)
			return
		}
	}
	dr.Clusters = append(dr.Clusters, newCluster)
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

	slog.Info("🔍 found broker addresses", "visibility", visibility, "authType", authType)
	slog.Debug("found broker addresses", "visibility", visibility, "authType", authType, "addresses", brokerList)

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

	slog.Info("🔍 found broker addresses", "authType", authType)
	slog.Debug("found broker addresses", "authType", authType, "addresses", brokerList)

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
