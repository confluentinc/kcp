package types

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/kafka"
	kafkatypes "github.com/aws/aws-sdk-go-v2/service/kafka/types"
	"github.com/confluentinc/kcp/internal/services/markdown"
)

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

type ClusterInformation struct {
	ClusterID            string                                 `json:"cluster_id"`
	Region               string                                 `json:"region"`
	Timestamp            time.Time                              `json:"timestamp"`
	Cluster              kafkatypes.Cluster                     `json:"cluster"`
	ClientVpcConnections []kafkatypes.ClientVpcConnection       `json:"clientVpcConnections"`
	ClusterOperations    []kafkatypes.ClusterOperationV2Summary `json:"clusterOperations"`
	Nodes                []kafkatypes.NodeInfo                  `json:"nodes"`
	ScramSecrets         []string                               `json:"ScramSecrets"`
	BootstrapBrokers     kafka.GetBootstrapBrokersOutput        `json:"bootstrapBrokers"`
	Policy               kafka.GetClusterPolicyOutput           `json:"policy"`
	CompatibleVersions   kafka.GetCompatibleKafkaVersionsOutput `json:"compatibleVersions"`
	ClusterNetworking    ClusterNetworking                      `json:"cluster_networking"`
	Topics               []string                               `json:"topics"`
	Acls                 []Acls                                 `json:"acls"`
}

func (c *ClusterInformation) GetBootstrapBrokersForAuthType(authType AuthType) ([]string, error) {
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
			return nil, fmt.Errorf("❌ No SASL/IAM brokers found in the cluster")
		}
	case AuthTypeSASLSCRAM:
		brokerList = aws.ToString(c.BootstrapBrokers.BootstrapBrokerStringPublicSaslScram)
		visibility = "PUBLIC"
		if brokerList == "" {
			brokerList = aws.ToString(c.BootstrapBrokers.BootstrapBrokerStringSaslScram)
			visibility = "PRIVATE"
		}
		if brokerList == "" {
			return nil, fmt.Errorf("❌ No SASL/SCRAM brokers found in the cluster")
		}
	case AuthTypeUnauthenticated:
		brokerList = aws.ToString(c.BootstrapBrokers.BootstrapBrokerStringTls)
		visibility = "PRIVATE"
		if brokerList == "" {
			brokerList = aws.ToString(c.BootstrapBrokers.BootstrapBrokerString)
		}
		if brokerList == "" {
			return nil, fmt.Errorf("❌ No Unauthenticated brokers found in the cluster")
		}
	case AuthTypeTLS:
		brokerList = aws.ToString(c.BootstrapBrokers.BootstrapBrokerStringPublicTls)
		visibility = "PUBLIC"
		if brokerList == "" {
			brokerList = aws.ToString(c.BootstrapBrokers.BootstrapBrokerStringTls)
			visibility = "PRIVATE"
		}
		if brokerList == "" {
			return nil, fmt.Errorf("❌ No TLS brokers found in the cluster")
		}
	default:
		return nil, fmt.Errorf("❌ Auth type: %v not yet supported", authType)
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

func (c *ClusterInformation) GetJsonPath() string {
	return filepath.Join(c.GetDirPath(), fmt.Sprintf("%s.json", aws.ToString(c.Cluster.ClusterName)))
}

func (c *ClusterInformation) GetMarkdownPath() string {
	return filepath.Join(c.GetDirPath(), fmt.Sprintf("%s.md", aws.ToString(c.Cluster.ClusterName)))
}

func (c *ClusterInformation) GetDirPath() string {
	return filepath.Join("kcp-scan", c.Region, aws.ToString(c.Cluster.ClusterName))
}

func (c *ClusterInformation) WriteAsJson() error {

	dirPath := c.GetDirPath()
	if err := os.MkdirAll(dirPath, 0755); err != nil {
		return fmt.Errorf("❌ Failed to create directory structure: %v", err)
	}

	filePath := c.GetJsonPath()

	data, err := c.AsJson()
	if err != nil {
		return err
	}

	if err := os.WriteFile(filePath, data, 0644); err != nil {
		return fmt.Errorf("❌ Failed to write file: %v", err)
	}

	return nil
}

func (c *ClusterInformation) AsJson() ([]byte, error) {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("❌ Failed to marshal scan results: %v", err)
	}
	return data, nil
}

func (c *ClusterInformation) WriteAsMarkdown(suppressToTerminal bool) error {
	dirPath := c.GetDirPath()
	if err := os.MkdirAll(dirPath, 0755); err != nil {
		return fmt.Errorf("❌ Failed to create directory structure: %v", err)
	}

	filePath := c.GetMarkdownPath()

	md := c.AsMarkdown()
	return md.Print(markdown.PrintOptions{ToTerminal: !suppressToTerminal, ToFile: filePath})
}

// generateMarkdownReport creates a comprehensive markdown report of the scan results
func (c *ClusterInformation) AsMarkdown() *markdown.Markdown {
	md := markdown.New()

	// Title and overview
	md.AddHeading("MSK Cluster Scan Report", 1)
	md.AddParagraph(fmt.Sprintf("This report provides a comprehensive scan of the MSK cluster **%s** in the **%s** region.", aws.ToString(c.Cluster.ClusterName), c.Region))
	md.AddParagraph(fmt.Sprintf("**Scan Timestamp:** %s", c.Timestamp.Format("2006-01-02 15:04:05 UTC")))
	md.AddParagraph(fmt.Sprintf("**Cluster ARN:** %s", aws.ToString(c.Cluster.ClusterArn)))
	md.AddParagraph(fmt.Sprintf("**Cluster ID:** %s", c.ClusterID))

	// Summary section
	md.AddHeading("Executive Summary", 2)
	c.addSummarySection(md)

	// Cluster details section
	md.AddHeading("Cluster Details", 2)
	c.addClusterDetailsSection(md)

	// Bootstrap brokers section
	md.AddHeading("Bootstrap Brokers", 2)
	c.addBootstrapBrokersSection(md)

	// VPC Connections section
	if len(c.ClientVpcConnections) > 0 {
		md.AddHeading("Client VPC Connections", 2)
		c.addVpcConnectionsSection(md)
	}

	// Networking section
	if len(c.ClusterNetworking.Subnets) > 0 {
		md.AddHeading("Cluster Networking", 2)
		c.addClusterNetworkingSection(md)
	}

	// Cluster Operations section
	if len(c.ClusterOperations) > 0 {
		md.AddHeading("Cluster Operations", 2)
		c.addClusterOperationsSection(md)
	}

	// Nodes section
	if len(c.Nodes) > 0 {
		md.AddHeading("Cluster Nodes", 2)
		c.addNodesSection(md)
	}

	// SCRAM Secrets section
	if len(c.ScramSecrets) > 0 {
		md.AddHeading("SCRAM Secrets", 2)
		c.addScramSecretsSection(md)
	}

	// Cluster Policy section
	if c.Policy.Policy != nil {
		md.AddHeading("Cluster Policy", 2)
		c.addClusterPolicySection(md)
	}

	// Compatible Versions section
	if len(c.CompatibleVersions.CompatibleKafkaVersions) > 0 {
		md.AddHeading("Compatible Kafka Versions", 2)
		c.addCompatibleVersionsSection(md)
	}

	// Topics section
	if len(c.Topics) > 0 {
		md.AddHeading("Kafka Topics", 2)
		c.addTopicsSection(md)
	}

	if len(c.Acls) > 0 {
		md.AddHeading("Kafka ACLs", 2)
		c.addAclsSection(md)
	}

	return md
}

func (c *ClusterInformation) addSummarySection(md *markdown.Markdown) {
	summaryItems := []string{
		fmt.Sprintf("**Cluster Name:** %s", aws.ToString(c.Cluster.ClusterName)),
		fmt.Sprintf("**Cluster Type:** %s", string(c.Cluster.ClusterType)),
		fmt.Sprintf("**Status:** %s", string(c.Cluster.State)),
		fmt.Sprintf("**Region:** %s", c.Region),
		fmt.Sprintf("**Topics:** %d", len(c.Topics)),
		fmt.Sprintf("**ACLs:** %d", len(c.Acls)),
		fmt.Sprintf("**Client VPC Connections:** %d", len(c.ClientVpcConnections)),
		fmt.Sprintf("**VPC ID:** %s", aws.ToString(&c.ClusterNetworking.VpcId)),
		fmt.Sprintf("**Cluster Operations:** %d", len(c.ClusterOperations)),
		func() string {
			if c.Cluster.Provisioned != nil && c.Cluster.Provisioned.NumberOfBrokerNodes != nil {
				return fmt.Sprintf("**Brokers:** %d", *c.Cluster.Provisioned.NumberOfBrokerNodes)
			}
			return "**Brokers:** N/A"
		}(),
		fmt.Sprintf("**SCRAM Secrets:** %d", len(c.ScramSecrets)),
	}

	md.AddList(summaryItems)

	// Authentication information
	if c.Cluster.ClusterType == kafkatypes.ClusterTypeProvisioned && c.Cluster.Provisioned != nil {
		authInfo := c.getAuthenticationInfo(c.Cluster)
		if authInfo != "" {
			md.AddHeading("Authentication", 3)
			md.AddParagraph(authInfo)
		}
	} else if c.Cluster.ClusterType == kafkatypes.ClusterTypeServerless && c.Cluster.Serverless != nil {
		authInfo := c.getServerlessAuthenticationInfo(c.Cluster)
		if authInfo != "" {
			md.AddHeading("Authentication", 3)
			md.AddParagraph(authInfo)
		}
	}
}

// addClusterDetailsSection adds detailed cluster information
func (c *ClusterInformation) addClusterDetailsSection(md *markdown.Markdown) {
	headers := []string{"Property", "Value"}

	var tableData [][]string

	// Basic cluster info
	tableData = append(tableData, []string{"Cluster Name", aws.ToString(c.Cluster.ClusterName)})
	tableData = append(tableData, []string{"Cluster ARN", aws.ToString(c.Cluster.ClusterArn)})
	tableData = append(tableData, []string{"Cluster Type", string(c.Cluster.ClusterType)})
	tableData = append(tableData, []string{"State", string(c.Cluster.State)})
	tableData = append(tableData, []string{"Region", c.Region})
	tableData = append(tableData, []string{"Cluster ID", c.ClusterID})

	// Provisioned cluster specific info
	if c.Cluster.ClusterType == kafkatypes.ClusterTypeProvisioned && c.Cluster.Provisioned != nil {
		provisioned := c.Cluster.Provisioned
		if provisioned.NumberOfBrokerNodes != nil {
			tableData = append(tableData, []string{"Number of Broker Nodes", fmt.Sprintf("%d", *provisioned.NumberOfBrokerNodes)})
		}
		if provisioned.CurrentBrokerSoftwareInfo != nil && provisioned.CurrentBrokerSoftwareInfo.KafkaVersion != nil {
			tableData = append(tableData, []string{"Kafka Version", *provisioned.CurrentBrokerSoftwareInfo.KafkaVersion})
		}
		if provisioned.EnhancedMonitoring != "" {
			tableData = append(tableData, []string{"Enhanced Monitoring", string(provisioned.EnhancedMonitoring)})
		}
		if provisioned.BrokerNodeGroupInfo != nil && provisioned.BrokerNodeGroupInfo.InstanceType != nil {
			tableData = append(tableData, []string{"Instance Type", *provisioned.BrokerNodeGroupInfo.InstanceType})
		}
	}

	md.AddTable(headers, tableData)
}

// addBootstrapBrokersSection adds bootstrap broker information
func (c *ClusterInformation) addBootstrapBrokersSection(md *markdown.Markdown) {
	headers := []string{"Broker Type", "Addresses"}

	var tableData [][]string

	brokers := c.BootstrapBrokers

	// Add different broker types if they exist
	if brokers.BootstrapBrokerString != nil {
		tableData = append(tableData, []string{"Plaintext", *brokers.BootstrapBrokerString})
	}
	if brokers.BootstrapBrokerStringTls != nil {
		tableData = append(tableData, []string{"TLS", *brokers.BootstrapBrokerStringTls})
	}
	if brokers.BootstrapBrokerStringPublicTls != nil {
		tableData = append(tableData, []string{"Public TLS", *brokers.BootstrapBrokerStringPublicTls})
	}
	if brokers.BootstrapBrokerStringSaslScram != nil {
		tableData = append(tableData, []string{"SASL/SCRAM", *brokers.BootstrapBrokerStringSaslScram})
	}
	if brokers.BootstrapBrokerStringPublicSaslScram != nil {
		tableData = append(tableData, []string{"Public SASL/SCRAM", *brokers.BootstrapBrokerStringPublicSaslScram})
	}
	if brokers.BootstrapBrokerStringSaslIam != nil {
		tableData = append(tableData, []string{"SASL/IAM", *brokers.BootstrapBrokerStringSaslIam})
	}
	if brokers.BootstrapBrokerStringPublicSaslIam != nil {
		tableData = append(tableData, []string{"Public SASL/IAM", *brokers.BootstrapBrokerStringPublicSaslIam})
	}

	md.AddTable(headers, tableData)
}

// addVpcConnectionsSection adds VPC connections table
func (c *ClusterInformation) addVpcConnectionsSection(md *markdown.Markdown) {
	headers := []string{"VPC Connection ARN", "Creation Time"}

	var tableData [][]string
	for _, connection := range c.ClientVpcConnections {
		creationTime := "N/A"
		if connection.CreationTime != nil {
			creationTime = connection.CreationTime.Format("2006-01-02 15:04:05")
		}

		row := []string{
			aws.ToString(connection.VpcConnectionArn),
			creationTime,
		}
		tableData = append(tableData, row)
	}

	md.AddTable(headers, tableData)
}

// addClusterNetworkingSection adds cluster networking information
func (c *ClusterInformation) addClusterNetworkingSection(md *markdown.Markdown) {
	headers := []string{"Broker ID", "Subnet ID", "CIDR Block", "Availability Zone"}

	var tableData [][]string
	for _, subnet := range c.ClusterNetworking.Subnets {
		row := []string{
			fmt.Sprintf("%d", subnet.SubnetMskBrokerId),
			aws.ToString(&subnet.SubnetId),
			aws.ToString(&subnet.CidrBlock),
			aws.ToString(&subnet.AvailabilityZone),
		}
		tableData = append(tableData, row)
	}

	sort.Slice(tableData, func(i, j int) bool {
		return tableData[i][0] < tableData[j][0]
	})

	md.AddParagraph(fmt.Sprintf("**VPC ID:** %s", aws.ToString(&c.ClusterNetworking.VpcId)))
	md.AddParagraph(fmt.Sprintf("**Security Groups:** %s", strings.Join(c.ClusterNetworking.SecurityGroups, ", ")))

	md.AddTable(headers, tableData)
}

// addClusterOperationsSection adds cluster operations table
func (c *ClusterInformation) addClusterOperationsSection(md *markdown.Markdown) {
	headers := []string{"Operation ARN", "Operation Type", "Status", "Start Time"}

	operations := make([]kafkatypes.ClusterOperationV2Summary, len(c.ClusterOperations))
	copy(operations, c.ClusterOperations)

	// Sort operations by start time in descending order (most recent first)
	sort.Slice(operations, func(i, j int) bool {
		if operations[i].StartTime == nil && operations[j].StartTime == nil {
			return false
		}
		if operations[i].StartTime == nil {
			return false
		}
		if operations[j].StartTime == nil {
			return true
		}
		return operations[i].StartTime.After(*operations[j].StartTime)
	})

	maxOperations := min(5, len(operations))
	operations = operations[:maxOperations]

	var tableData [][]string
	for _, operation := range operations {
		startTime := "N/A"
		if operation.StartTime != nil {
			startTime = operation.StartTime.Format("2006-01-02 15:04:05")
		}

		operationType := "N/A"
		if operation.OperationType != nil {
			operationType = *operation.OperationType
		}

		operationState := "N/A"
		if operation.OperationState != nil {
			operationState = *operation.OperationState
		}

		row := []string{
			aws.ToString(operation.OperationArn),
			operationType,
			operationState,
			startTime,
		}
		tableData = append(tableData, row)
	}

	if len(c.ClusterOperations) > 5 {
		md.AddParagraph(fmt.Sprintf("**Note:** Only showing 5 of %d most recent cluster operations.", len(c.ClusterOperations)))
	}

	md.AddTable(headers, tableData)
}

// addNodesSection adds cluster nodes table
func (c *ClusterInformation) addNodesSection(md *markdown.Markdown) {
	headers := []string{"Node ARN", "Node Type", "Instance Type"}

	var tableData [][]string
	filteredNodes := 0

	for _, node := range c.Nodes {
		instanceType := "N/A"
		if node.InstanceType != nil {
			instanceType = *node.InstanceType
		}

		nodeARN := aws.ToString(node.NodeARN)

		if nodeARN == "" && instanceType == "N/A" {
			filteredNodes++
			continue
		}

		row := []string{
			nodeARN,
			string(node.NodeType),
			instanceType,
		}
		tableData = append(tableData, row)
	}

	// TODO: Investigate and add further info about what these nodes actually are.
	if filteredNodes > 0 {
		md.AddParagraph(fmt.Sprintf("**Note:** %d nodes with empty ARN and no instance type information are hidden from this table.", filteredNodes))
	}

	md.AddTable(headers, tableData)
}

// addScramSecretsSection adds SCRAM secrets list
func (c *ClusterInformation) addScramSecretsSection(md *markdown.Markdown) {
	md.AddList(c.ScramSecrets)
}

// addClusterPolicySection adds cluster policy information
func (c *ClusterInformation) addClusterPolicySection(md *markdown.Markdown) {
	if c.Policy.Policy != nil {
		md.AddCodeBlock(*c.Policy.Policy, "json")
	}
}

// addCompatibleVersionsSection adds compatible Kafka versions table
func (c *ClusterInformation) addCompatibleVersionsSection(md *markdown.Markdown) {
	headers := []string{"Source Version", "Target Versions"}

	var tableData [][]string
	for _, version := range c.CompatibleVersions.CompatibleKafkaVersions {
		sourceVersion := "N/A"
		if version.SourceVersion != nil {
			sourceVersion = *version.SourceVersion
		}

		targetVersions := "N/A"
		if len(version.TargetVersions) > 0 {
			targetVersions = strings.Join(version.TargetVersions, ", ")
		}

		row := []string{sourceVersion, targetVersions}
		tableData = append(tableData, row)
	}

	md.AddTable(headers, tableData)
}

// addTopicsSection adds Kafka topics list
func (c *ClusterInformation) addTopicsSection(md *markdown.Markdown) {
	md.AddList(c.Topics)
}

// addAclsSection adds Kafka ACLs in a table format
func (c *ClusterInformation) addAclsSection(md *markdown.Markdown) {
	type aclEntry struct {
		Principal      string
		ResourceType   string
		ResourceName   string
		Host           string
		Operation      string
		PermissionType string
	}

	var aclEntries []aclEntry

	for _, acl := range c.Acls {
		entry := aclEntry{
			Principal:      acl.Principal,
			ResourceType:   acl.ResourceType,
			ResourceName:   acl.ResourceName,
			Host:           acl.Host,
			Operation:      acl.Operation,
			PermissionType: acl.PermissionType,
		}
		aclEntries = append(aclEntries, entry)
	}

	if len(aclEntries) == 0 {
		md.AddParagraph("No ACLs found.")
		return
	}

	// Sort entries by principal first, then by resource type, resource name, operation
	sort.Slice(aclEntries, func(i, j int) bool {
		if aclEntries[i].Principal != aclEntries[j].Principal {
			return aclEntries[i].Principal < aclEntries[j].Principal
		}

		if aclEntries[i].ResourceType != aclEntries[j].ResourceType {
			return aclEntries[i].ResourceType < aclEntries[j].ResourceType
		}

		if aclEntries[i].ResourceName != aclEntries[j].ResourceName {
			return aclEntries[i].ResourceName < aclEntries[j].ResourceName
		}
		return aclEntries[i].Operation < aclEntries[j].Operation
	})

	headers := []string{"Principal", "Resource Type", "Resource Name", "Host", "Operation", "Permission Type"}

	var tableData [][]string
	var lastPrincipal string

	for _, entry := range aclEntries {
		principal := entry.Principal
		if principal == lastPrincipal {
			principal = ""
		} else {
			lastPrincipal = entry.Principal
		}

		row := []string{
			principal,
			entry.ResourceType,
			entry.ResourceName,
			entry.Host,
			entry.Operation,
			entry.PermissionType,
		}
		tableData = append(tableData, row)
	}

	md.AddTable(headers, tableData)

	uniquePrincipals := make(map[string]bool)
	for _, entry := range aclEntries {
		uniquePrincipals[entry.Principal] = true
	}
	md.AddParagraph(fmt.Sprintf("**Summary:** %d ACL entries for %d principals", len(aclEntries), len(uniquePrincipals)))
}

// getAuthenticationInfo extracts authentication information for provisioned clusters
func (c *ClusterInformation) getAuthenticationInfo(cluster kafkatypes.Cluster) string {
	if cluster.Provisioned == nil || cluster.Provisioned.ClientAuthentication == nil {
		return "No authentication configured"
	}

	auth := cluster.Provisioned.ClientAuthentication
	authTypes := []string{}

	if auth.Sasl != nil {
		if auth.Sasl.Scram != nil && auth.Sasl.Scram.Enabled != nil && *auth.Sasl.Scram.Enabled {
			authTypes = append(authTypes, "SASL/SCRAM")
		}
		if auth.Sasl.Iam != nil && auth.Sasl.Iam.Enabled != nil && *auth.Sasl.Iam.Enabled {
			authTypes = append(authTypes, "SASL/IAM")
		}
	}

	if auth.Tls != nil && auth.Tls.Enabled != nil && *auth.Tls.Enabled {
		authTypes = append(authTypes, "TLS")
	}

	if auth.Unauthenticated != nil && auth.Unauthenticated.Enabled != nil && *auth.Unauthenticated.Enabled {
		authTypes = append(authTypes, "Unauthenticated")
	}

	if len(authTypes) == 0 {
		return "No authentication configured"
	}

	return strings.Join(authTypes, ", ")
}

// getServerlessAuthenticationInfo extracts authentication information for serverless clusters
func (c *ClusterInformation) getServerlessAuthenticationInfo(cluster kafkatypes.Cluster) string {
	if cluster.Serverless == nil || cluster.Serverless.ClientAuthentication == nil {
		return "No authentication configured"
	}

	auth := cluster.Serverless.ClientAuthentication
	authTypes := []string{}

	if auth.Sasl != nil && auth.Sasl.Iam != nil && auth.Sasl.Iam.Enabled != nil && *auth.Sasl.Iam.Enabled {
		authTypes = append(authTypes, "SASL/IAM")
	}

	if len(authTypes) == 0 {
		return "No authentication configured"
	}

	return strings.Join(authTypes, ", ")
}
