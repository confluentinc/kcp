package clusters

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	kafkatypes "github.com/aws/aws-sdk-go-v2/service/kafka/types"
	"github.com/goccy/go-yaml"

	"github.com/confluentinc/kcp/internal/client"
	kafkaservice "github.com/confluentinc/kcp/internal/services/kafka"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/confluentinc/kcp/internal/utils"
)

type ClustersScannerOpts struct {
	DiscoverDir     string
	CredentialsFile string
}

type ClustersScanner struct {
	opts *ClustersScannerOpts
}

func NewClustersScanner(opts *ClustersScannerOpts) *ClustersScanner {
	return &ClustersScanner{
		opts: opts,
	}
}

func (cs *ClustersScanner) Run() error {
	data, err := os.ReadFile(cs.opts.CredentialsFile)
	if err != nil {
		return fmt.Errorf("failed to read credentials file: %w", err)
	}

	var credsFile types.CredsYaml
	if err := yaml.Unmarshal(data, &credsFile); err != nil {
		return fmt.Errorf("failed to unmarshal credentials YAML: %w", err)
	}

	for region, clusterEntries := range credsFile.Regions {
		for arn, clusterEntry := range clusterEntries.Clusters {
			if err := cs.scanCluster(region, arn, clusterEntry); err != nil {
				slog.Error("failed to scan cluster", "cluster", arn, "error", err)
				continue
			}
		}
	}

	return nil
}

func (cs *ClustersScanner) scanCluster(region, arn string, clusterEntry types.ClusterEntry) error {
	clusterName, err := cs.getClusterName(arn)
	if err != nil {
		return fmt.Errorf("❌ failed to get cluster name: %v", err)
	}

	var clusterInfo types.ClusterInformation
	if cs.opts.DiscoverDir != "" {
		clusterFile := filepath.Join(cs.opts.DiscoverDir, region, clusterName, fmt.Sprintf("%s.json", clusterName))
		file, err := os.ReadFile(clusterFile)
		if err != nil {
			return fmt.Errorf("❌ failed to read cluster file: %v", err)
		}

		if err := json.Unmarshal(file, &clusterInfo); err != nil {
			return fmt.Errorf("❌ failed to unmarshal cluster info: %v", err)
		}
	}

	authType, err := cs.getSelectedAuthType(clusterEntry)
	if err != nil {
		return fmt.Errorf("❌ failed to determine auth type for cluster: %s in region: %s: %v", clusterName, region, err)
	}
	
	slog.Info(fmt.Sprintf("🚀 starting broker scan for %s using %s authentication", clusterName, authType))

	bootstrapServer, err := cs.getBootstrapServer(clusterInfo, authType)
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
		MSKService:        nil,
		KafkaAdminFactory: kafkaAdminFactory,
		AuthType:          authType,
		ClusterArn:        arn,
	})

	if err := cs.scanKafkaResources(&clusterInfo, kafkaService, bootstrapServer); err != nil {
		return fmt.Errorf("❌ failed to scan Kafka resources: %v", err)
	}

	if err := clusterInfo.WriteAsJson(); err != nil {
		return fmt.Errorf("❌ Failed to write broker info to file: %v", err)
	}

	if err := clusterInfo.WriteAsMarkdown(true); err != nil {
		return fmt.Errorf("❌ Failed to write broker info to markdown file: %v", err)
	}

	slog.Info(fmt.Sprintf("✅ broker scan complete for %s", clusterName))

	return nil
}

func (cs *ClustersScanner) scanKafkaResources(clusterInfo *types.ClusterInformation, kafkaService *kafkaservice.KafkaService, bootstrapServer string) error {
	clientBrokerEncryptionInTransit := types.GetClientBrokerEncryptionInTransit(clusterInfo.Cluster)
	kafkaVersion := kafkaService.GetKafkaVersion(clusterInfo)

	bootstrapServers := strings.Split(bootstrapServer, ",")
	admin, err := kafkaService.CreateKafkaAdmin(bootstrapServers, clientBrokerEncryptionInTransit, kafkaVersion)
	if err != nil {
		return fmt.Errorf("❌ Failed to setup admin client: %v", err)
	}

	defer admin.Close()

	clusterMetadata, err := admin.GetClusterKafkaMetadata()
	if err != nil {
		return fmt.Errorf("❌ Failed to describe kafka cluster: %v", err)
	}

	clusterInfo.ClusterID = clusterMetadata.ClusterID

	topics, err := admin.ListTopics()
	if err != nil {
		return fmt.Errorf("❌ Failed to list topics: %v", err)
	}

	topicList := make([]string, 0, len(topics))
	for topic := range topics {
		topicList = append(topicList, topic)
	}
	clusterInfo.Topics = topicList

	if clusterInfo.Cluster.ClusterType == kafkatypes.ClusterTypeProvisioned {
		acls, err := admin.ListAcls()
		if err != nil {
			return fmt.Errorf("❌ Failed to list acls: %v", err)
		}

		// Flatten the ACLs for easier processing
		var flattenedAcls []types.Acls
		for _, resourceAcl := range acls {
			for _, acl := range resourceAcl.Acls {
				flattenedAcl := types.Acls{
					ResourceType:        resourceAcl.Resource.ResourceType.String(),
					ResourceName:        resourceAcl.Resource.ResourceName,
					ResourcePatternType: resourceAcl.Resource.ResourcePatternType.String(),
					Principal:           acl.Principal,
					Host:                acl.Host,
					Operation:           acl.Operation.String(),
					PermissionType:      acl.PermissionType.String(),
				}
				flattenedAcls = append(flattenedAcls, flattenedAcl)
			}
		}
		clusterInfo.Acls = flattenedAcls
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

func (cs *ClustersScanner) getSelectedAuthType(clusterEntry types.ClusterEntry) (types.AuthType, error) {
	enabledMethods := utils.GetAuthMethods(clusterEntry)
	if len(enabledMethods) == 0 {
		return "", fmt.Errorf("no authentication method enabled for cluster")
	}

	authMethod := enabledMethods[0]
	switch authMethod {
	case "unauthenticated":
		return types.AuthTypeUnauthenticated, nil
	case "iam":
		return types.AuthTypeIAM, nil
	case "sasl_scram":
		return types.AuthTypeSASLSCRAM, nil
	case "tls":
		return types.AuthTypeTLS, nil
	default:
		return "", fmt.Errorf("unsupported authentication method: %s", authMethod)
	}
}

func (cs *ClustersScanner) getBootstrapServer(clusterInfo types.ClusterInformation, authType types.AuthType) (string, error) {
	switch authType {
	case types.AuthTypeUnauthenticated:
		return aws.ToString(clusterInfo.BootstrapBrokers.BootstrapBrokerString), nil
	case types.AuthTypeIAM:
		if clusterInfo.BootstrapBrokers.BootstrapBrokerStringPublicSaslIam != nil {
			return aws.ToString(clusterInfo.BootstrapBrokers.BootstrapBrokerStringPublicSaslIam), nil
		} else {
			return aws.ToString(clusterInfo.BootstrapBrokers.BootstrapBrokerStringSaslIam), nil
		}
	case types.AuthTypeSASLSCRAM:
		if clusterInfo.BootstrapBrokers.BootstrapBrokerStringPublicSaslScram != nil {
			return aws.ToString(clusterInfo.BootstrapBrokers.BootstrapBrokerStringPublicSaslScram), nil
		} else {
			return aws.ToString(clusterInfo.BootstrapBrokers.BootstrapBrokerStringSaslScram), nil
		}
	case types.AuthTypeTLS:
		if clusterInfo.BootstrapBrokers.BootstrapBrokerStringPublicTls != nil {
			return aws.ToString(clusterInfo.BootstrapBrokers.BootstrapBrokerStringPublicTls), nil
		} else {
			return aws.ToString(clusterInfo.BootstrapBrokers.BootstrapBrokerStringTls), nil
		}
	default:
		return "", fmt.Errorf("unsupported authentication method: %s", authType)
	}
}