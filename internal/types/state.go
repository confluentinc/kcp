package types

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	cloudwatchtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
	costexplorertypes "github.com/aws/aws-sdk-go-v2/service/costexplorer/types"
	"github.com/aws/aws-sdk-go-v2/service/kafka"
	kafkatypes "github.com/aws/aws-sdk-go-v2/service/kafka/types"
	kafkaconnecttypes "github.com/aws/aws-sdk-go-v2/service/kafkaconnect/types"
	"github.com/confluentinc/confluent-kafka-go/v2/schemaregistry"
	"github.com/confluentinc/kcp/internal/build_info"
)

// MSKSourcesState contains all MSK-specific data
type MSKSourcesState struct {
	Regions []DiscoveredRegion `json:"regions"`
}

// OSKSourcesState contains all OSK-specific data
type OSKSourcesState struct {
	Clusters []OSKDiscoveredCluster `json:"clusters"`
}

// OSKDiscoveredCluster represents a discovered OSK cluster
type OSKDiscoveredCluster struct {
	ID                          string                      `json:"id"`
	BootstrapServers            []string                    `json:"bootstrap_servers"`
	KafkaAdminClientInformation KafkaAdminClientInformation `json:"kafka_admin_client_information"`
	JMXMetrics                  *JMXMetrics                 `json:"jmx_metrics,omitempty"`
	DiscoveredClients           []DiscoveredClient          `json:"discovered_clients"`
	Metadata                    OSKClusterMetadata          `json:"metadata"`
}

// OSKClusterMetadata contains optional metadata about OSK clusters
type OSKClusterMetadata struct {
	Environment  string            `json:"environment,omitempty"`
	Location     string            `json:"location,omitempty"`
	KafkaVersion string            `json:"kafka_version,omitempty"`
	Labels       map[string]string `json:"labels,omitempty"`
	LastScanned  time.Time         `json:"last_scanned"`
}

// State represents the unified state file (kcp-state.json)
type State struct {
	MSKSources       *MSKSourcesState            `json:"msk_sources,omitempty"`
	OSKSources       *OSKSourcesState            `json:"osk_sources,omitempty"`
	SchemaRegistries []SchemaRegistryInformation `json:"schema_registries"`
	KcpBuildInfo     KcpBuildInfo                `json:"kcp_build_info"`
	Timestamp        time.Time                   `json:"timestamp"`
}

func NewStateFrom(fromState *State) *State {
	// Always create with fresh metadata for the current discovery run
	workingState := &State{
		KcpBuildInfo: KcpBuildInfo{
			Version: build_info.Version,
			Commit:  build_info.Commit,
			Date:    build_info.Date,
		},
		Timestamp: time.Now(),
	}

	if fromState == nil {
		// Initialize both sources with empty arrays
		workingState.MSKSources = &MSKSourcesState{
			Regions: []DiscoveredRegion{},
		}
		workingState.OSKSources = &OSKSourcesState{
			Clusters: []OSKDiscoveredCluster{},
		}
	} else {
		// Copy existing MSK data or initialize empty
		if fromState.MSKSources != nil {
			mskSources := &MSKSourcesState{
				Regions: make([]DiscoveredRegion, len(fromState.MSKSources.Regions)),
			}
			copy(mskSources.Regions, fromState.MSKSources.Regions)
			workingState.MSKSources = mskSources
		} else {
			workingState.MSKSources = &MSKSourcesState{
				Regions: []DiscoveredRegion{},
			}
		}

		// Copy existing OSK data or initialize empty
		if fromState.OSKSources != nil {
			oskSources := &OSKSourcesState{
				Clusters: make([]OSKDiscoveredCluster, len(fromState.OSKSources.Clusters)),
			}
			copy(oskSources.Clusters, fromState.OSKSources.Clusters)
			workingState.OSKSources = oskSources
		} else {
			workingState.OSKSources = &OSKSourcesState{
				Clusters: []OSKDiscoveredCluster{},
			}
		}
	}

	return workingState
}

func NewStateFromFile(stateFile string) (*State, error) {
	file, err := os.ReadFile(stateFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read state file: %v", err)
	}

	var state State
	if err := json.Unmarshal(file, &state); err != nil {
		return nil, fmt.Errorf("failed to unmarshal state: %v", err)
	}

	return &state, nil
}

func (s *State) WriteToFile(filePath string) error {
	data, err := json.Marshal(s)
	if err != nil {
		return fmt.Errorf("failed to marshal state: %v", err)
	}
	return os.WriteFile(filePath, data, 0644)
}

func (s *State) WriteReportCommands(filePath string, stateFilePath string) error {
	regionCommands := []string{"# Report region costs commands"}
	clusterCommands := []string{"# Report cluster metrics commands"}

	// Loop through regions
	if s.MSKSources != nil {
		for _, region := range s.MSKSources.Regions {
		// Output command for report costs for this region
		regionCommand := []string{fmt.Sprintf("# region: %s", region.Name)}
		regionCommand = append(regionCommand, fmt.Sprintf("kcp report costs --state-file %s --region %s --start <YYYY-MM-DD> --end <YYYY-MM-DD>\n", stateFilePath, region.Name))
		regionCommands = append(regionCommands, strings.Join(regionCommand, "\n"))

		// Loop through clusters in this region
		for _, cluster := range region.Clusters {
			clusterCommand := []string{fmt.Sprintf("# cluster: %s", cluster.Name)}
			clusterCommand = append(clusterCommand, fmt.Sprintf("kcp report metrics --state-file %s --cluster-arn %s --start <YYYY-MM-DD> --end <YYYY-MM-DD>\n", stateFilePath, cluster.Arn))
			clusterCommands = append(clusterCommands, strings.Join(clusterCommand, "\n"))
		}
		}
	}

	// Combine all commands and write to file
	regionLines := strings.Join(regionCommands, "\n") + "\n"
	clusterLines := strings.Join(clusterCommands, "\n")
	allLines := regionLines + "\n" + clusterLines + "\n"

	err := os.WriteFile(filePath, []byte(allLines), 0644)
	if err != nil {
		return fmt.Errorf("failed to write commands to file: %v", err)
	}
	return nil
}

func (s *State) PersistStateFile(stateFile string) error {
	if s == nil {
		return fmt.Errorf("discovery state is nil")
	}

	return s.WriteToFile(stateFile)
}

// UpsertRegion inserts a new region or updates an existing one by name
// Automatically preserves KafkaAdminClientInformation from existing clusters
func (s *State) UpsertRegion(newRegion DiscoveredRegion) {
	if s.MSKSources == nil {
		s.MSKSources = &MSKSourcesState{
			Regions: []DiscoveredRegion{},
		}
	}
	for i, existingRegion := range s.MSKSources.Regions {
		if existingRegion.Name == newRegion.Name {
			discoveredClusters := newRegion.Clusters
			newRegion.Clusters = existingRegion.Clusters
			// set discovered clusters and refresh into state (preserves KafkaAdminClientInformation)
			newRegion.RefreshClusters(discoveredClusters)
			s.MSKSources.Regions[i] = newRegion
			return
		}
	}
	s.MSKSources.Regions = append(s.MSKSources.Regions, newRegion)
}

func (s *State) UpsertDiscoveredClients(regionName string, clusterName string, discoveredClients []DiscoveredClient) error {
	slog.Info("🔍 looking for region and cluster in state file", "region", regionName, "cluster_name", clusterName)
	if s.MSKSources == nil {
		return fmt.Errorf("no MSK sources found in state file")
	}
	for i := range s.MSKSources.Regions {
		region := &s.MSKSources.Regions[i]
		if region.Name == regionName {
			for j := range region.Clusters {
				cluster := &region.Clusters[j]
				if cluster.Name == clusterName {
					// Merge existing clients from state with newly discovered clients
					allClients := append(cluster.DiscoveredClients, discoveredClients...)
					cluster.DiscoveredClients = dedupDiscoveredClients(allClients)
					return nil
				}
			}
		}
	}
	return fmt.Errorf("cluster '%s' not found in region '%s'", clusterName, regionName)
}

func dedupDiscoveredClients(discoveredClients []DiscoveredClient) []DiscoveredClient {
	// Deduplicate by composite key, keeping the client with the most recent timestamp
	clientsByCompositeKey := make(map[string]DiscoveredClient)

	for _, currentClient := range discoveredClients {
		existingClient, exists := clientsByCompositeKey[currentClient.CompositeKey]

		if !exists || currentClient.Timestamp.After(existingClient.Timestamp) {
			clientsByCompositeKey[currentClient.CompositeKey] = currentClient
		}
	}

	dedupedClients := make([]DiscoveredClient, 0, len(clientsByCompositeKey))
	for _, client := range clientsByCompositeKey {
		dedupedClients = append(dedupedClients, client)
	}

	return dedupedClients
}

func (s *State) GetClusterByArn(clusterArn string) (*DiscoveredCluster, error) {
	if s.MSKSources != nil {
		for i := range s.MSKSources.Regions {
			for j := range s.MSKSources.Regions[i].Clusters {
				if s.MSKSources.Regions[i].Clusters[j].Arn == clusterArn {
					return &s.MSKSources.Regions[i].Clusters[j], nil
				}
			}
		}
	}

	return nil, fmt.Errorf("cluster with ARN %s not found in state file", clusterArn)
}

// GetOSKClusterByID looks up an OSK cluster by the user-provided ID from credentials
func (s *State) GetOSKClusterByID(id string) (*OSKDiscoveredCluster, error) {
	if s.OSKSources == nil {
		return nil, fmt.Errorf("no OSK sources in state file")
	}

	for i := range s.OSKSources.Clusters {
		if s.OSKSources.Clusters[i].ID == id {
			return &s.OSKSources.Clusters[i], nil
		}
	}

	return nil, fmt.Errorf("OSK cluster with ID '%s' not found in state file", id)
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

// MergeFrom merges values from another KafkaAdminClientInformation
// New discoveries are added, old data is preserved, duplicates are merged (new takes precedence)
func (c *KafkaAdminClientInformation) MergeFrom(other KafkaAdminClientInformation) {
	// Only use old ClusterID if new one is empty
	if c.ClusterID == "" {
		c.ClusterID = other.ClusterID
	}

	// Only use old SaslMechanism if new one is empty
	if c.SaslMechanism == "" {
		c.SaslMechanism = other.SaslMechanism
	}

	// Merge Topics: new topics take precedence, old topics preserved if not re-discovered
	c.Topics = mergeTopics(c.Topics, other.Topics)

	// Merge ACLs: combine both, deduplicate
	c.Acls = mergeAcls(c.Acls, other.Acls)

	// Merge SelfManagedConnectors: new connectors take precedence, old preserved if not re-discovered
	c.SelfManagedConnectors = mergeSelfManagedConnectors(c.SelfManagedConnectors, other.SelfManagedConnectors)
}

// mergeTopics merges two Topics, with newTopics taking precedence for duplicates (by name)
func mergeTopics(newTopics, oldTopics *Topics) *Topics {
	// If no old topics, just return new (even if empty)
	if oldTopics == nil || len(oldTopics.Details) == 0 {
		return newTopics
	}

	// If no new topics, preserve old
	if newTopics == nil || len(newTopics.Details) == 0 {
		return oldTopics
	}

	// Merge: start with old, update/add with new
	topicsByName := make(map[string]TopicDetails)
	for _, topic := range oldTopics.Details {
		topicsByName[topic.Name] = topic
	}
	for _, topic := range newTopics.Details {
		topicsByName[topic.Name] = topic // new takes precedence
	}

	// Convert back to slice
	mergedDetails := make([]TopicDetails, 0, len(topicsByName))
	for _, topic := range topicsByName {
		mergedDetails = append(mergedDetails, topic)
	}

	return &Topics{
		Details: mergedDetails,
		Summary: CalculateTopicSummaryFromDetails(mergedDetails),
	}
}

// mergeAcls merges two ACL slices, deduplicating by all fields
func mergeAcls(newAcls, oldAcls []Acls) []Acls {
	if len(oldAcls) == 0 {
		return newAcls
	}
	if len(newAcls) == 0 {
		return oldAcls
	}

	// Use composite key for deduplication
	aclKey := func(a Acls) string {
		return fmt.Sprintf("%s|%s|%s|%s|%s|%s|%s",
			a.ResourceType, a.ResourceName, a.ResourcePatternType,
			a.Principal, a.Host, a.Operation, a.PermissionType)
	}

	aclsByKey := make(map[string]Acls)
	for _, acl := range oldAcls {
		aclsByKey[aclKey(acl)] = acl
	}
	for _, acl := range newAcls {
		aclsByKey[aclKey(acl)] = acl // new takes precedence
	}

	merged := make([]Acls, 0, len(aclsByKey))
	for _, acl := range aclsByKey {
		merged = append(merged, acl)
	}
	return merged
}

// mergeSelfManagedConnectors merges connectors, with new taking precedence for duplicates (by name)
func mergeSelfManagedConnectors(newConnectors, oldConnectors *SelfManagedConnectors) *SelfManagedConnectors {
	if oldConnectors == nil || len(oldConnectors.Connectors) == 0 {
		return newConnectors
	}
	if newConnectors == nil || len(newConnectors.Connectors) == 0 {
		return oldConnectors
	}

	connectorsByName := make(map[string]SelfManagedConnector)
	for _, c := range oldConnectors.Connectors {
		connectorsByName[c.Name] = c
	}
	for _, c := range newConnectors.Connectors {
		connectorsByName[c.Name] = c // new takes precedence
	}

	merged := make([]SelfManagedConnector, 0, len(connectorsByName))
	for _, c := range connectorsByName {
		merged = append(merged, c)
	}

	return &SelfManagedConnectors{Connectors: merged}
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
			return nil, fmt.Errorf("No SASL/IAM brokers found in the cluster")
		}
	case AuthTypeSASLSCRAM:
		brokerList = aws.ToString(c.BootstrapBrokers.BootstrapBrokerStringPublicSaslScram)
		visibility = "PUBLIC"
		if brokerList == "" {
			brokerList = aws.ToString(c.BootstrapBrokers.BootstrapBrokerStringSaslScram)
			visibility = "PRIVATE"
		}
		if brokerList == "" {
			return nil, fmt.Errorf("No SASL/SCRAM brokers found in the cluster")
		}
	case AuthTypeUnauthenticatedTLS:
		brokerList = aws.ToString(c.BootstrapBrokers.BootstrapBrokerStringTls)
		visibility = "PRIVATE"
		if brokerList == "" {
			return nil, fmt.Errorf("No Unauthenticated (TLS Encryption) brokers found in the cluster")
		}
	case AuthTypeUnauthenticatedPlaintext:
		brokerList = aws.ToString(c.BootstrapBrokers.BootstrapBrokerString)
		visibility = "PRIVATE"
		if brokerList == "" {
			return nil, fmt.Errorf("No Unauthenticated (Plaintext) brokers found in the cluster")
		}
	case AuthTypeTLS:
		brokerList = aws.ToString(c.BootstrapBrokers.BootstrapBrokerStringPublicTls)
		visibility = "PUBLIC"
		if brokerList == "" {
			brokerList = aws.ToString(c.BootstrapBrokers.BootstrapBrokerStringTls)
			visibility = "PRIVATE"
		}
		if brokerList == "" {
			return nil, fmt.Errorf("No TLS brokers found in the cluster")
		}
	default:
		return nil, fmt.Errorf("Auth type: %v not yet supported", authType)
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
		return nil, fmt.Errorf("Auth type: %v not yet supported", authType)
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

type SelfManagedConnector struct {
	Name        string         `json:"name"`
	Config      map[string]any `json:"config"`
	State       string         `json:"state,omitempty"`
	ConnectHost string         `json:"connect_host,omitempty"`
}

type SelfManagedConnectors struct {
	Connectors []SelfManagedConnector `json:"connectors"`
}

type KafkaAdminClientInformation struct {
	ClusterID             string                 `json:"cluster_id"`
	DiscoveredBrokers     []string               `json:"discovered_brokers,omitempty"`
	SaslMechanism         string                 `json:"sasl_mechanism,omitempty"`
	Topics                *Topics                `json:"topics"`
	Acls                  []Acls                 `json:"acls"`
	SelfManagedConnectors *SelfManagedConnectors `json:"self_managed_connectors"`
}

func (c *KafkaAdminClientInformation) CalculateTopicSummary() TopicSummary {
	if c.Topics == nil {
		return TopicSummary{}
	}
	return CalculateTopicSummaryFromDetails(c.Topics.Details)
}

func (c *KafkaAdminClientInformation) SetTopics(topicDetails []TopicDetails) {
	c.Topics = &Topics{
		Details: topicDetails,
		Summary: CalculateTopicSummaryFromDetails(topicDetails),
	}
}

func (c *KafkaAdminClientInformation) SetSelfManagedConnectors(connectors []SelfManagedConnector) {
	c.SelfManagedConnectors = &SelfManagedConnectors{
		Connectors: connectors,
	}
}

type DiscoveredClient struct {
	CompositeKey string    `json:"composite_key"`
	ClientId     string    `json:"client_id"`
	Role         string    `json:"role"`
	Topic        string    `json:"topic"`
	Auth         string    `json:"auth"`
	Principal    string    `json:"principal"`
	Timestamp    time.Time `json:"timestamp"`
}

// ----- metrics -----
type BrokerType string

const (
	BrokerTypeExpress  BrokerType = "express"
	BrokerTypeStandard BrokerType = "standard"
)

type ClusterMetrics struct {
	MetricMetadata MetricMetadata                     `json:"metadata"`
	Results        []cloudwatchtypes.MetricDataResult `json:"results"`
	QueryInfo      []MetricQueryInfo                  `json:"query_info"`
}

type MetricMetadata struct {
	ClusterType          string    `json:"cluster_type"`
	NumberOfBrokerNodes  int       `json:"number_of_broker_nodes"`
	KafkaVersion         string    `json:"kafka_version"`
	BrokerAzDistribution string    `json:"broker_az_distribution"`
	EnhancedMonitoring   string    `json:"enhanced_monitoring"`
	StartDate            time.Time `json:"start_date"`
	EndDate              time.Time `json:"end_date"`
	Period               int32     `json:"period"`

	FollowerFetching bool       `json:"follower_fetching"`
	InstanceType     string     `json:"instance_type"`
	TieredStorage    bool       `json:"tiered_storage"`
	BrokerType       BrokerType `json:"broker_type"`
}

type CloudWatchTimeWindow struct {
	StartTime time.Time
	EndTime   time.Time
	Period    int32
}

type MetricQueryInfo struct {
	MetricName        string `json:"metric_name"`
	Namespace         string `json:"namespace"`
	Dimensions        string `json:"dimensions"`
	Statistic         string `json:"statistic"`
	Period            int32  `json:"period"`
	SearchExpression  string `json:"search_expression"`
	MathExpression    string `json:"math_expression"`
	AWSCLICommand     string `json:"aws_cli_command"`
	ConsoleSourceJSON string `json:"console_source_json"`
	AggregationNote   string `json:"aggregation_note"`
}

// ----- costs -----
type CostQueryTimePeriod struct {
	Start string `json:"start"`
	End   string `json:"end"`
}

type CostQueryInfo struct {
	TimePeriod      CostQueryTimePeriod `json:"time_period"`
	Granularity     string              `json:"granularity"`
	Services        []string            `json:"services"`
	Regions         []string            `json:"regions"`
	GroupBy         []string            `json:"group_by"`
	Metrics         []string            `json:"metrics"`
	Tags            map[string][]string `json:"tags,omitempty"`
	AWSCLICommand   string              `json:"aws_cli_command"`
	ConsoleURL      string              `json:"console_url"`
	AggregationNote string              `json:"aggregation_note"`
}

type CostInformation struct {
	CostMetadata CostMetadata                     `json:"metadata"`
	CostResults  []costexplorertypes.ResultByTime `json:"results"`
	QueryInfo    CostQueryInfo                    `json:"query_info"`
}

type CostMetadata struct {
	StartDate   time.Time           `json:"start_date"`
	EndDate     time.Time           `json:"end_date"`
	Granularity string              `json:"granularity"`
	Tags        map[string][]string `json:"tags"`
	Services    []string            `json:"services"`
}

type KcpBuildInfo struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
	Date    string `json:"date"`
}

type SchemaRegistryInformation struct {
	Type                 string                       `json:"type"`
	URL                  string                       `json:"url"`
	DefaultCompatibility schemaregistry.Compatibility `json:"default_compatibility"`
	Contexts             []string                     `json:"contexts"`
	Subjects             []Subject                    `json:"subjects"`
}

type Subject struct {
	Name          string                          `json:"name"`
	SchemaType    string                          `json:"schema_type"`
	Compatibility string                          `json:"compatibility,omitempty"`
	Versions      []schemaregistry.SchemaMetadata `json:"versions"`
	Latest        schemaregistry.SchemaMetadata   `json:"latest_schema"`
}

// ProcessedState represents the transformed output data structure
// This is what comes OUT of the frontend/API after processing the raw State data
// Same structure as State but with costs and metrics flattened for easier frontend consumption
type ProcessedState struct {
	Sources          []ProcessedSource           `json:"sources"`
	SchemaRegistries []SchemaRegistryInformation `json:"schema_registries"`
	KcpBuildInfo     interface{}                 `json:"kcp_build_info,omitempty"`
	Timestamp        time.Time                   `json:"timestamp"`
}

// ProcessedRegion mirrors DiscoveredRegion but with flattened costs and simplified clusters
type ProcessedRegion struct {
	Name           string                                      `json:"name"`
	Configurations []kafka.DescribeConfigurationRevisionOutput `json:"configurations"`
	Costs          ProcessedRegionCosts                        `json:"costs"`    // Flattened from raw AWS Cost Explorer data
	Clusters       []ProcessedCluster                          `json:"clusters"` // Simplified from full DiscoveredCluster data
}

type ProcessedRegionCosts struct {
	Region     string              `json:"region"`
	Metadata   CostMetadata        `json:"metadata"`
	Results    []ProcessedCost     `json:"results"`
	Aggregates ProcessedAggregates `json:"aggregates"`
	QueryInfo  CostQueryInfo       `json:"query_info"`
}

// AWS service name constants — single source of truth for Cost Explorer service filters.
// Frontend constants (cmd/ui/frontend/src/constants/index.ts AWS_SERVICES) should mirror these.
const (
	ServiceAWSCertificateManager = "AWS Certificate Manager"
	ServiceMSK                   = "Amazon Managed Streaming for Apache Kafka"
	ServiceEC2Other              = "EC2 - Other"
	ServiceELB                   = "Amazon Elastic Load Balancing"
	ServiceVPC                   = "Amazon Virtual Private Cloud"
)

// newServiceCostAggregates creates a ServiceCostAggregates with all maps initialized
func newServiceCostAggregates() ServiceCostAggregates {
	return ServiceCostAggregates{
		UnblendedCost:    make(map[string]any),
		BlendedCost:      make(map[string]any),
		AmortizedCost:    make(map[string]any),
		NetAmortizedCost: make(map[string]any),
		NetUnblendedCost: make(map[string]any),
	}
}

// ForService returns a pointer to the ServiceCostAggregates for the given service name,
// or nil if the service is not recognized.
func (a *ProcessedAggregates) ForService(name string) *ServiceCostAggregates {
	switch name {
	case ServiceAWSCertificateManager:
		return &a.AWSCertificateManager
	case ServiceMSK:
		return &a.AmazonManagedStreamingForApacheKafka
	case ServiceEC2Other:
		return &a.EC2Other
	case ServiceELB:
		return &a.ElasticLoadBalancing
	case ServiceVPC:
		return &a.AmazonVPC
	}
	return nil
}

// ProcessedAggregates represents the specific services we query
type ProcessedAggregates struct {
	AWSCertificateManager                ServiceCostAggregates `json:"AWS Certificate Manager"`
	AmazonManagedStreamingForApacheKafka ServiceCostAggregates `json:"Amazon Managed Streaming for Apache Kafka"`
	EC2Other                             ServiceCostAggregates `json:"EC2 - Other"`
	ElasticLoadBalancing                 ServiceCostAggregates `json:"Amazon Elastic Load Balancing"`
	AmazonVPC                            ServiceCostAggregates `json:"Amazon Virtual Private Cloud"`
}

// NewProcessedAggregates creates a new ProcessedAggregates with all maps initialized
func NewProcessedAggregates() ProcessedAggregates {
	return ProcessedAggregates{
		AWSCertificateManager:                newServiceCostAggregates(),
		AmazonManagedStreamingForApacheKafka: newServiceCostAggregates(),
		EC2Other:                             newServiceCostAggregates(),
		ElasticLoadBalancing:                 newServiceCostAggregates(),
		AmazonVPC:                            newServiceCostAggregates(),
	}
}

type ProcessedCost struct {
	Start     string                 `json:"start"`
	End       string                 `json:"end"`
	Service   string                 `json:"service"`
	UsageType string                 `json:"usage_type"`
	Values    ProcessedCostBreakdown `json:"values"`
}

type ProcessedCostBreakdown struct {
	UnblendedCost    float64 `json:"unblended_cost"`
	BlendedCost      float64 `json:"blended_cost"`
	AmortizedCost    float64 `json:"amortized_cost"`
	NetAmortizedCost float64 `json:"net_amortized_cost"`
	NetUnblendedCost float64 `json:"net_unblended_cost"`
}

// ProcessedCluster contains the complete cluster data with flattened metrics
// This is the full cluster information with processed metrics, unlike the simplified version in types.go
type ProcessedCluster struct {
	Name                        string                      `json:"name"`
	Arn                         string                      `json:"arn"`
	Region                      string                      `json:"region"`
	ClusterMetrics              ProcessedClusterMetrics     `json:"metrics"` // Flattened from raw CloudWatch metrics
	AWSClientInformation        AWSClientInformation        `json:"aws_client_information"`
	KafkaAdminClientInformation KafkaAdminClientInformation `json:"kafka_admin_client_information"`
	DiscoveredClients           []DiscoveredClient          `json:"discovered_clients"`
}

type ProcessedClusterMetrics struct {
	Region     string                     `json:"region"`
	ClusterArn string                     `json:"cluster_arn"`
	Metadata   MetricMetadata             `json:"metadata"`
	Metrics    []ProcessedMetric          `json:"results"`
	Aggregates map[string]MetricAggregate `json:"aggregates"`
	QueryInfo  []MetricQueryInfo          `json:"query_info"`
}

type ProcessedMetric struct {
	Start string   `json:"start"`
	End   string   `json:"end"`
	Label string   `json:"label"`
	Value *float64 `json:"value"`
}

type MetricAggregate struct {
	Average *float64 `json:"avg"`
	Maximum *float64 `json:"max"`
	Minimum *float64 `json:"min"`
}

type CostAggregate struct {
	Sum     *float64 `json:"sum"`
	Average *float64 `json:"avg"`
	Maximum *float64 `json:"max"`
	Minimum *float64 `json:"min"`
}

// ServiceCostAggregates represents cost aggregates for a single service
// Uses explicit fields for each metric type instead of a map
type ServiceCostAggregates struct {
	UnblendedCost    map[string]any `json:"unblended_cost"`
	BlendedCost      map[string]any `json:"blended_cost"`
	AmortizedCost    map[string]any `json:"amortized_cost"`
	NetAmortizedCost map[string]any `json:"net_amortized_cost"`
	NetUnblendedCost map[string]any `json:"net_unblended_cost"`
}

// SourceType represents the type of Kafka source
type SourceType string

const (
	SourceTypeMSK SourceType = "msk"
	SourceTypeOSK SourceType = "osk"
)

// ProcessedSource represents a unified source (MSK or OSK) with discriminated union
type ProcessedSource struct {
	Type    SourceType           `json:"type"`
	MSKData *ProcessedMSKSource  `json:"msk_data,omitempty"`
	OSKData *ProcessedOSKSource  `json:"osk_data,omitempty"`
}

// ProcessedMSKSource contains processed MSK data (regions)
type ProcessedMSKSource struct {
	Regions []ProcessedRegion `json:"regions"`
}

// ProcessedOSKSource contains processed OSK data (flat cluster array)
type ProcessedOSKSource struct {
	Clusters []ProcessedOSKCluster `json:"clusters"`
}

// ProcessedOSKCluster represents an OSK cluster in the API response
type ProcessedOSKCluster struct {
	ID                          string                      `json:"id"`
	BootstrapServers            []string                    `json:"bootstrap_servers"`
	KafkaAdminClientInformation KafkaAdminClientInformation `json:"kafka_admin_client_information"`
	JMXMetrics                  *JMXMetrics                 `json:"jmx_metrics,omitempty"`
	DiscoveredClients           []DiscoveredClient          `json:"discovered_clients"`
	Metadata                    OSKClusterMetadata          `json:"metadata"`
}
