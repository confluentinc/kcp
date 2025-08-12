package cluster

import (
	"fmt"
	"sort"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	kafkatypes "github.com/aws/aws-sdk-go-v2/service/kafka/types"
	"github.com/confluentinc/kcp/internal/services/markdown"
	"github.com/confluentinc/kcp/internal/types"
)

// generateMarkdownReport creates a comprehensive markdown report of the cluster scan results
func (cs *ClusterScanner) generateMarkdownReport(clusterInfo *types.ClusterInformation, filePath string) error {
	md := markdown.New()

	// Title and overview
	md.AddHeading("MSK Cluster Scan Report", 1)
	md.AddParagraph(fmt.Sprintf("This report provides a comprehensive scan of the MSK cluster **%s** in the **%s** region.", aws.ToString(clusterInfo.Cluster.ClusterName), clusterInfo.Region))
	md.AddParagraph(fmt.Sprintf("**Scan Timestamp:** %s", clusterInfo.Timestamp.Format("2006-01-02 15:04:05 UTC")))
	md.AddParagraph(fmt.Sprintf("**Cluster ARN:** %s", aws.ToString(clusterInfo.Cluster.ClusterArn)))
	md.AddParagraph(fmt.Sprintf("**Cluster ID:** %s", clusterInfo.ClusterID))

	// Summary section
	md.AddHeading("Executive Summary", 2)
	cs.addSummarySection(md, clusterInfo)

	// Cluster details section
	md.AddHeading("Cluster Details", 2)
	cs.addClusterDetailsSection(md, clusterInfo)

	// Bootstrap brokers section
	md.AddHeading("Bootstrap Brokers", 2)
	cs.addBootstrapBrokersSection(md, clusterInfo)

	// VPC Connections section
	if len(clusterInfo.ClientVpcConnections) > 0 {
		md.AddHeading("Client VPC Connections", 2)
		cs.addVpcConnectionsSection(md, clusterInfo)
	}

	// Networking section
	if len(clusterInfo.ClusterNetworking.Subnets) > 0 {
		md.AddHeading("Cluster Networking", 2)
		cs.addClusterNetworkingSection(md, clusterInfo)
	}

	// Cluster Operations section
	if len(clusterInfo.ClusterOperations) > 0 {
		md.AddHeading("Cluster Operations", 2)
		cs.addClusterOperationsSection(md, clusterInfo)
	}

	// Nodes section
	if len(clusterInfo.Nodes) > 0 {
		md.AddHeading("Cluster Nodes", 2)
		cs.addNodesSection(md, clusterInfo)
	}

	// SCRAM Secrets section
	if len(clusterInfo.ScramSecrets) > 0 {
		md.AddHeading("SCRAM Secrets", 2)
		cs.addScramSecretsSection(md, clusterInfo)
	}

	// Cluster Policy section
	if clusterInfo.Policy.Policy != nil {
		md.AddHeading("Cluster Policy", 2)
		cs.addClusterPolicySection(md, clusterInfo)
	}

	// Compatible Versions section
	if len(clusterInfo.CompatibleVersions.CompatibleKafkaVersions) > 0 {
		md.AddHeading("Compatible Kafka Versions", 2)
		cs.addCompatibleVersionsSection(md, clusterInfo)
	}

	// Topics section
	if len(clusterInfo.Topics) > 0 {
		md.AddHeading("Kafka Topics", 2)
		cs.addTopicsSection(md, clusterInfo)
	}

	if len(clusterInfo.Acls) > 0 {
		md.AddHeading("Kafka ACLs", 2)
		cs.addAclsSection(md, clusterInfo)
	}

	// Save to file
	return md.Print(markdown.PrintOptions{ToTerminal: true, ToFile: filePath})
}

// addSummarySection adds a summary of the cluster scan results
func (cs *ClusterScanner) addSummarySection(md *markdown.Markdown, clusterInfo *types.ClusterInformation) {
	summaryItems := []string{
		fmt.Sprintf("**Cluster Name:** %s", aws.ToString(clusterInfo.Cluster.ClusterName)),
		fmt.Sprintf("**Cluster Type:** %s", string(clusterInfo.Cluster.ClusterType)),
		fmt.Sprintf("**Status:** %s", string(clusterInfo.Cluster.State)),
		fmt.Sprintf("**Region:** %s", clusterInfo.Region),
		fmt.Sprintf("**Topics:** %d", len(clusterInfo.Topics)),
		fmt.Sprintf("**ACLs:** %d", len(clusterInfo.Acls)),
		fmt.Sprintf("**Client VPC Connections:** %d", len(clusterInfo.ClientVpcConnections)),
		fmt.Sprintf("**VPC ID:** %s", aws.ToString(&clusterInfo.ClusterNetworking.VpcId)),
		fmt.Sprintf("**Cluster Operations:** %d", len(clusterInfo.ClusterOperations)),
		fmt.Sprintf("**Brokers:** %d", *clusterInfo.Cluster.Provisioned.NumberOfBrokerNodes),
		fmt.Sprintf("**SCRAM Secrets:** %d", len(clusterInfo.ScramSecrets)),
	}

	md.AddList(summaryItems)

	// Authentication information
	if clusterInfo.Cluster.ClusterType == kafkatypes.ClusterTypeProvisioned && clusterInfo.Cluster.Provisioned != nil {
		authInfo := cs.getAuthenticationInfo(clusterInfo.Cluster)
		if authInfo != "" {
			md.AddHeading("Authentication", 3)
			md.AddParagraph(authInfo)
		}
	} else if clusterInfo.Cluster.ClusterType == kafkatypes.ClusterTypeServerless && clusterInfo.Cluster.Serverless != nil {
		authInfo := cs.getServerlessAuthenticationInfo(clusterInfo.Cluster)
		if authInfo != "" {
			md.AddHeading("Authentication", 3)
			md.AddParagraph(authInfo)
		}
	}
}

// addClusterDetailsSection adds detailed cluster information
func (cs *ClusterScanner) addClusterDetailsSection(md *markdown.Markdown, clusterInfo *types.ClusterInformation) {
	headers := []string{"Property", "Value"}

	var tableData [][]string

	// Basic cluster info
	tableData = append(tableData, []string{"Cluster Name", aws.ToString(clusterInfo.Cluster.ClusterName)})
	tableData = append(tableData, []string{"Cluster ARN", aws.ToString(clusterInfo.Cluster.ClusterArn)})
	tableData = append(tableData, []string{"Cluster Type", string(clusterInfo.Cluster.ClusterType)})
	tableData = append(tableData, []string{"State", string(clusterInfo.Cluster.State)})
	tableData = append(tableData, []string{"Region", clusterInfo.Region})
	tableData = append(tableData, []string{"Cluster ID", clusterInfo.ClusterID})

	// Provisioned cluster specific info
	if clusterInfo.Cluster.ClusterType == kafkatypes.ClusterTypeProvisioned && clusterInfo.Cluster.Provisioned != nil {
		provisioned := clusterInfo.Cluster.Provisioned
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
func (cs *ClusterScanner) addBootstrapBrokersSection(md *markdown.Markdown, clusterInfo *types.ClusterInformation) {
	headers := []string{"Broker Type", "Addresses"}

	var tableData [][]string

	brokers := clusterInfo.BootstrapBrokers

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
func (cs *ClusterScanner) addVpcConnectionsSection(md *markdown.Markdown, clusterInfo *types.ClusterInformation) {
	headers := []string{"VPC Connection ARN", "Creation Time"}

	var tableData [][]string
	for _, connection := range clusterInfo.ClientVpcConnections {
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
func (cs *ClusterScanner) addClusterNetworkingSection(md *markdown.Markdown, clusterInfo *types.ClusterInformation) {
	headers := []string{"Broker ID", "Subnet ID", "Availability Zone", "CIDR Block"}

	var tableData [][]string
	for _, subnet := range clusterInfo.ClusterNetworking.Subnets {
		row := []string{
			fmt.Sprintf("%d", subnet.SubnetMskBrokerId),
			aws.ToString(&subnet.AvailabilityZone),
			aws.ToString(&subnet.SubnetId),
			aws.ToString(&subnet.CidrBlock),
		}
		tableData = append(tableData, row)
	}

	sort.Slice(tableData, func(i, j int) bool {
		return tableData[i][0] < tableData[j][0]
	})

	md.AddTable(headers, tableData)
}

// addClusterOperationsSection adds cluster operations table
func (cs *ClusterScanner) addClusterOperationsSection(md *markdown.Markdown, clusterInfo *types.ClusterInformation) {
	headers := []string{"Operation ARN", "Operation Type", "Status", "Start Time"}

	operations := make([]kafkatypes.ClusterOperationV2Summary, len(clusterInfo.ClusterOperations))
	copy(operations, clusterInfo.ClusterOperations)

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

	if len(clusterInfo.ClusterOperations) > 5 {
		md.AddParagraph(fmt.Sprintf("**Note:** Only showing 5 of %d most recent cluster operations.", len(clusterInfo.ClusterOperations)))
	}

	md.AddTable(headers, tableData)
}

// addNodesSection adds cluster nodes table
func (cs *ClusterScanner) addNodesSection(md *markdown.Markdown, clusterInfo *types.ClusterInformation) {
	headers := []string{"Node ARN", "Node Type", "Instance Type"}

	var tableData [][]string
	filteredNodes := 0

	for _, node := range clusterInfo.Nodes {
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
func (cs *ClusterScanner) addScramSecretsSection(md *markdown.Markdown, clusterInfo *types.ClusterInformation) {
	md.AddList(clusterInfo.ScramSecrets)
}

// addClusterPolicySection adds cluster policy information
func (cs *ClusterScanner) addClusterPolicySection(md *markdown.Markdown, clusterInfo *types.ClusterInformation) {
	if clusterInfo.Policy.Policy != nil {
		md.AddCodeBlock(*clusterInfo.Policy.Policy, "json")
	}
}

// addCompatibleVersionsSection adds compatible Kafka versions table
func (cs *ClusterScanner) addCompatibleVersionsSection(md *markdown.Markdown, clusterInfo *types.ClusterInformation) {
	headers := []string{"Source Version", "Target Versions"}

	var tableData [][]string
	for _, version := range clusterInfo.CompatibleVersions.CompatibleKafkaVersions {
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
func (cs *ClusterScanner) addTopicsSection(md *markdown.Markdown, clusterInfo *types.ClusterInformation) {
	md.AddList(clusterInfo.Topics)
}

// addAclsSection adds Kafka ACLs in a table format
func (cs *ClusterScanner) addAclsSection(md *markdown.Markdown, clusterInfo *types.ClusterInformation) {
	type aclEntry struct {
		Principal      string
		ResourceType   string
		ResourceName   string
		Host           string
		Operation      string
		PermissionType string
	}

	var aclEntries []aclEntry

	for _, acl := range clusterInfo.Acls {
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
func (cs *ClusterScanner) getAuthenticationInfo(cluster kafkatypes.Cluster) string {
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
func (cs *ClusterScanner) getServerlessAuthenticationInfo(cluster kafkatypes.Cluster) string {
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
