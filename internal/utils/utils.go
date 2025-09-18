package utils

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	kafkatypes "github.com/aws/aws-sdk-go-v2/service/kafka/types"
	"github.com/confluentinc/kcp/internal/types"
)

func ConvertKafkaVersion(kafkaVersion *string) string {
	switch {
	case strings.Contains(*kafkaVersion, "kraft"):
		return strings.ReplaceAll(*kafkaVersion, ".x.kraft", ".0")
	case strings.Contains(*kafkaVersion, "x"):
		return strings.ReplaceAll(*kafkaVersion, ".x", ".0")
	case strings.Contains(*kafkaVersion, "tiered"):
		return strings.ReplaceAll(*kafkaVersion, ".tiered", "")
	case *kafkaVersion == "3.6.0.1":
		return "3.6.0"
	default:
		return *kafkaVersion
	}
}

func StructToMap(s any) (map[string]any, error) {
	jsonBytes, err := json.Marshal(s)
	if err != nil {
		return nil, err
	}

	var result map[string]any
	err = json.Unmarshal(jsonBytes, &result)
	return result, err
}

// getKafkaVersion determines the Kafka version based on cluster type
func GetKafkaVersion(clusterInfo types.AWSClientInformation) string {
	switch clusterInfo.MskClusterConfig.ClusterType {
	case kafkatypes.ClusterTypeProvisioned:
		return ConvertKafkaVersion(clusterInfo.MskClusterConfig.Provisioned.CurrentBrokerSoftwareInfo.KafkaVersion)
	case kafkatypes.ClusterTypeServerless:
		slog.Warn("⚠️ Serverless clusters do not return a Kafka version, defaulting to 4.0.0")
		return "4.0.0"
	default:
		slog.Warn(fmt.Sprintf("⚠️ Unknown cluster type: %v, defaulting to 4.0.0", clusterInfo.MskClusterConfig.ClusterType))
		return "4.0.0"
	}
}

// DefaultClientBrokerEncryptionInTransit is the fallback encryption type when cluster encryption info is not available
const DefaultClientBrokerEncryptionInTransit = kafkatypes.ClientBrokerTls

// GetClientBrokerEncryptionInTransit determines the client broker encryption in transit value for a cluster
// with proper fallback logic when encryption info is not available
func GetClientBrokerEncryptionInTransit(cluster kafkatypes.Cluster) kafkatypes.ClientBroker {
	if cluster.ClusterType == kafkatypes.ClusterTypeProvisioned &&
		cluster.Provisioned != nil &&
		cluster.Provisioned.EncryptionInfo != nil &&
		cluster.Provisioned.EncryptionInfo.EncryptionInTransit != nil {
		return cluster.Provisioned.EncryptionInfo.EncryptionInTransit.ClientBroker
	}
	return DefaultClientBrokerEncryptionInTransit
}

func GetClusterByArn(discovery *types.Discovery, clusterArn string) (*types.DiscoveredCluster, error) {
	for _, region := range discovery.Regions {
		for _, cluster := range region.Clusters {
			if cluster.Arn == clusterArn {
				return &cluster, nil
			}
		}
	}
	
	return nil, fmt.Errorf("cluster with ARN %s not found in discovery data", clusterArn)
}