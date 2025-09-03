package clusters

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	kafkatypes "github.com/aws/aws-sdk-go-v2/service/kafka/types"
	"github.com/goccy/go-yaml"

	"github.com/confluentinc/kcp/internal/client"
	kafkaservice "github.com/confluentinc/kcp/internal/services/kafka"
	"github.com/confluentinc/kcp/internal/services/msk"
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
	
	mskService := msk.NewMSKService(nil)
	brokerAddresses, err := mskService.ParseBrokerAddresses(clusterInfo.BootstrapBrokers, authType)
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

	if err := cs.scanKafkaResources(&clusterInfo, kafkaService, brokerAddresses); err != nil {
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

func (cs *ClustersScanner) scanKafkaResources(clusterInfo *types.ClusterInformation, kafkaService *kafkaservice.KafkaService, brokerAddresses []string) error {
	clientBrokerEncryptionInTransit := types.GetClientBrokerEncryptionInTransit(clusterInfo.Cluster)
	kafkaVersion := kafkaService.GetKafkaVersion(clusterInfo)

	kafkaAdmin, err := kafkaService.CreateKafkaAdmin(brokerAddresses, clientBrokerEncryptionInTransit, kafkaVersion)
	if err != nil {
		return fmt.Errorf("❌ Failed to setup admin client: %v", err)
	}

	defer kafkaAdmin.Close()

	clusterMetadata, err := kafkaService.DescribeKafkaCluster(kafkaAdmin)
	if err != nil {
		return fmt.Errorf("❌ Failed to describe kafka cluster: %v", err)
	}

	clusterInfo.ClusterID = clusterMetadata.ClusterID

	topics, err := kafkaService.ScanClusterTopics(kafkaAdmin)
	if err != nil {
		return fmt.Errorf("❌ Failed to list topics: %v", err)
	}

	for _, topic := range topics {
		clusterInfo.Topics = append(clusterInfo.Topics, topic)
	}

	// Use KafkaService's ACL scanning logic instead of duplicating it
	if clusterInfo.Cluster.ClusterType == kafkatypes.ClusterTypeProvisioned {
		acls, err := kafkaService.ScanKafkaAcls(kafkaAdmin)
		if err != nil {
			return err
		}
		clusterInfo.Acls = acls
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
