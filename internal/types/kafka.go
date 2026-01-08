package types

import (
	"strings"
)

type TopicSummary struct {
	Topics                    int `json:"topics"`
	InternalTopics            int `json:"internal_topics"`
	TotalPartitions           int `json:"total_partitions"`
	TotalInternalPartitions   int `json:"total_internal_partitions"`
	CompactTopics             int `json:"compact_topics"`
	CompactInternalTopics     int `json:"compact_internal_topics"`
	CompactPartitions         int `json:"compact_partitions"`
	CompactInternalPartitions int `json:"compact_internal_partitions"`
	RemoteStorageTopics       int `json:"remote_storage_topics"`
}

type TopicDetails struct {
	Name              string             `json:"name"`
	Partitions        int                `json:"partitions"`
	ReplicationFactor int                `json:"replication_factor"`
	Configurations    map[string]*string `json:"configurations"`
}

type Topics struct {
	Summary TopicSummary   `json:"summary"`
	Details []TopicDetails `json:"details"`
}

// Preferred over sarama.ResourceAcls because it is flattened vs sarama's nested structure.
type Acls struct {
	ResourceType        string `json:"ResourceType"`
	ResourceName        string `json:"ResourceName"`
	ResourcePatternType string `json:"ResourcePatternType"`
	Principal           string `json:"Principal"`
	Host                string `json:"Host"`
	Operation           string `json:"Operation"`
	PermissionType      string `json:"PermissionType"`
}

type ACLMapping struct {
	Operation       string
	ResourceType    string
	RequiresPattern bool
}

// https://docs.aws.amazon.com/service-authorization/latest/reference/list_apachekafkaapisforamazonmskclusters.html
// https://docs.confluent.io/cloud/current/security/access-control/acl.html#acl-resources-and-operations-for-ccloud-summary
var AclMap = map[string]ACLMapping{
	"kafka-cluster:AlterCluster": {
		Operation:       "Alter",
		ResourceType:    "Cluster",
		RequiresPattern: false,
	},
	"kafka-cluster:AlterClusterDynamicConfiguration": {
		Operation:       "AlterConfigs",
		ResourceType:    "Cluster",
		RequiresPattern: false,
	},
	"kafka-cluster:AlterGroup": {
		Operation:       "Read",
		ResourceType:    "Group",
		RequiresPattern: true,
	},
	"kafka-cluster:AlterTopic": {
		Operation:       "Alter",
		ResourceType:    "Topic",
		RequiresPattern: true,
	},
	"kafka-cluster:AlterTopicDynamicConfiguration": {
		Operation:       "AlterConfigs",
		ResourceType:    "Topic",
		RequiresPattern: true,
	},
	"kafka-cluster:AlterTransactionalId": {
		Operation:       "Write",
		ResourceType:    "TransactionalId",
		RequiresPattern: true,
	},
	"kafka-cluster:CreateTopic": {
		Operation:       "Create",
		ResourceType:    "Topic",
		RequiresPattern: true,
	},
	"kafka-cluster:DeleteGroup": {
		Operation:       "Delete",
		ResourceType:    "Group",
		RequiresPattern: true,
	},
	"kafka-cluster:DeleteTopic": {
		Operation:       "Delete",
		ResourceType:    "Topic",
		RequiresPattern: true,
	},
	"kafka-cluster:DescribeCluster": {
		Operation:       "Describe",
		ResourceType:    "Cluster",
		RequiresPattern: false,
	},
	"kafka-cluster:DescribeClusterDynamicConfiguration": {
		Operation:       "DescribeConfigs",
		ResourceType:    "Cluster",
		RequiresPattern: false,
	},
	"kafka-cluster:DescribeGroup": {
		Operation:       "Describe",
		ResourceType:    "Group",
		RequiresPattern: true,
	},
	"kafka-cluster:DescribeTopic": {
		Operation:       "Describe",
		ResourceType:    "Topic",
		RequiresPattern: true,
	},
	"kafka-cluster:DescribeTopicDynamicConfiguration": {
		Operation:       "DescribeConfigs",
		ResourceType:    "Topic",
		RequiresPattern: true,
	},
	"kafka-cluster:DescribeTransactionalId": {
		Operation:       "Describe",
		ResourceType:    "TransactionalId",
		RequiresPattern: true,
	},
	"kafka-cluster:ReadData": {
		Operation:       "Read",
		ResourceType:    "Topic",
		RequiresPattern: true,
	},
	"kafka-cluster:WriteData": {
		Operation:       "Write",
		ResourceType:    "Topic",
		RequiresPattern: true,
	},
	"kafka-cluster:WriteDataIdempotently": {
		Operation:       "IdempotentWrite",
		ResourceType:    "Cluster",
		RequiresPattern: true,
	},
}

func CalculateTopicSummaryFromDetails(topicDetails []TopicDetails) TopicSummary {
	summary := TopicSummary{}

	for _, topic := range topicDetails {
		isInternal := strings.HasPrefix(topic.Name, "__")

		// Check if cleanup.policy exists and is not nil before dereferencing
		var isCompact bool
		if cleanupPolicy, exists := topic.Configurations["cleanup.policy"]; exists && cleanupPolicy != nil {
			isCompact = strings.Contains(*cleanupPolicy, "compact")
		}

		var isRemoteStorage bool
		if remoteStorage, exists := topic.Configurations["remote.storage.enable"]; exists && remoteStorage != nil {
			isRemoteStorage = strings.Contains(*remoteStorage, "true")
		}

		if isInternal {
			summary.InternalTopics++
			summary.TotalInternalPartitions += topic.Partitions
			if isCompact {
				summary.CompactInternalTopics++
				summary.CompactInternalPartitions += topic.Partitions
			}

			if isRemoteStorage {
				summary.RemoteStorageTopics++
			}
		} else {
			summary.Topics++
			summary.TotalPartitions += topic.Partitions
			if isCompact {
				summary.CompactTopics++
				summary.CompactPartitions += topic.Partitions
			}

			if isRemoteStorage {
				summary.RemoteStorageTopics++
			}
		}
	}

	return summary
}
