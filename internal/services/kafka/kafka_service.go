package kafka

import (
	"fmt"
	"log/slog"

	"github.com/IBM/sarama"
	kafkatypes "github.com/aws/aws-sdk-go-v2/service/kafka/types"
	"github.com/confluentinc/kcp/internal/client"
	"github.com/confluentinc/kcp/internal/types"
)

type KafkaService struct {
	client             client.KafkaAdmin
	groupScanner       client.ConsumerGroupScanner
	authType           types.AuthType
	clusterArn         string
	skipTopics         bool
	skipACLs           bool
	skipConsumerGroups bool
}

type KafkaServiceOpts struct {
	AuthType           types.AuthType
	ClusterArn         string
	SkipTopics         bool
	SkipACLs           bool
	SkipConsumerGroups bool
}

func NewKafkaService(kafkaAdmin client.KafkaAdmin, groupScanner client.ConsumerGroupScanner, opts KafkaServiceOpts) *KafkaService {
	return &KafkaService{
		client:             kafkaAdmin,
		groupScanner:       groupScanner,
		authType:           opts.AuthType,
		clusterArn:         opts.ClusterArn,
		skipTopics:         opts.SkipTopics,
		skipACLs:           opts.SkipACLs,
		skipConsumerGroups: opts.SkipConsumerGroups,
	}
}

// ScanKafkaResources scans all Kafka-related resources and populates the cluster information
func (ks *KafkaService) ScanKafkaResources(clusterType kafkatypes.ClusterType) (*types.KafkaAdminClientInformation, error) {
	kafkaAdminClientInformation := &types.KafkaAdminClientInformation{}
	// Get cluster metadata including broker information and ClusterID
	clusterMetadata, err := ks.describeKafkaCluster()
	if err != nil {
		return nil, err
	}

	kafkaAdminClientInformation.ClusterID = clusterMetadata.ClusterID

	// Store discovered broker addresses
	brokerAddrs := make([]string, 0, len(clusterMetadata.Brokers))
	for _, broker := range clusterMetadata.Brokers {
		brokerAddrs = append(brokerAddrs, broker.Addr())
	}
	kafkaAdminClientInformation.DiscoveredBrokers = brokerAddrs

	if !ks.skipTopics {
		topics, err := ks.scanClusterTopics()
		if err != nil {
			return nil, err
		}
		kafkaAdminClientInformation.SetTopics(topics)
	}

	// Serverless clusters do not support Kafka Admin API and instead returns an EOF error - this should be handled gracefully
	if clusterType == kafkatypes.ClusterTypeServerless {
		slog.Warn("⚠️ MSK Serverless cluster; skipping ACLs scan (Kafka Admin API unsupported on serverless)")
		return kafkaAdminClientInformation, nil
	}

	if !ks.skipACLs {
		acls, err := ks.scanKafkaAcls()
		if err != nil {
			return nil, err
		}
		kafkaAdminClientInformation.Acls = acls
	}

	if !ks.skipConsumerGroups && ks.groupScanner != nil {
		groups, err := ks.scanConsumerGroups()
		if err != nil {
			// Degrade, don't abort: a group-discovery failure (e.g. missing
			// DescribeGroup authz) must not sink an otherwise-good scan (design §8).
			slog.Warn("⚠️ failed to scan consumer groups; recording none", "error", err)
			kafkaAdminClientInformation.SetConsumerGroups(&types.ConsumerGroups{Details: []types.ConsumerGroupDetails{}})
		} else {
			kafkaAdminClientInformation.SetConsumerGroups(groups)
		}
	}

	return kafkaAdminClientInformation, nil
}

// scanClusterTopics scans for topics in the Kafka cluster
func (ks *KafkaService) scanClusterTopics() ([]types.TopicDetails, error) {
	slog.Info("🔍 scanning for cluster topics")
	slog.Debug("🔍 scanning for cluster topics", "clusterArn", ks.clusterArn)

	topics, err := ks.client.ListTopicsWithConfigs()
	if err != nil {
		return nil, fmt.Errorf("failed to list topics with configs: %v", err)
	}

	slog.Info("🔍 found topics", "count", len(topics))

	var topicDetails []types.TopicDetails
	for topicName, topic := range topics {
		configurations := make(map[string]*string)
		for key, valuePtr := range topic.ConfigEntries {
			if valuePtr != nil {
				configurations[key] = valuePtr
			}
		}

		topicDetails = append(topicDetails, types.TopicDetails{
			Name:              topicName,
			Partitions:        int(topic.NumPartitions),
			ReplicationFactor: int(topic.ReplicationFactor),
			Configurations:    configurations,
		})
	}

	return topicDetails, nil
}

// describeKafkaCluster gets cluster metadata and returns the cluster ID along with logging information
func (ks *KafkaService) describeKafkaCluster() (*client.ClusterKafkaMetadata, error) {
	slog.Info("🔍 describing kafka cluster")
	slog.Debug("🔍 describing kafka cluster", "clusterArn", ks.clusterArn)

	clusterMetadata, err := ks.client.GetClusterKafkaMetadata()
	if err != nil {
		return nil, fmt.Errorf("failed to describe kafka cluster: %v", err)
	}
	return clusterMetadata, nil
}

// scanKafkaAcls scans for Kafka ACLs in the cluster
func (ks *KafkaService) scanKafkaAcls() ([]types.Acls, error) {
	slog.Info("🔍 scanning for kafka acls")
	slog.Debug("🔍 scanning for kafka acls", "clusterArn", ks.clusterArn)

	acls, err := ks.client.ListAcls()
	if err != nil {
		return nil, fmt.Errorf("failed to list acls: %v", err)
	}

	// Flatten the ACLs for easier processing.
	var flattenedAcls []types.Acls
	for _, resourceAcl := range acls {
		for _, acl := range resourceAcl.Acls {
			flattenedAcl := types.Acls{
				ResourceType:        resourceAcl.ResourceType.String(),
				ResourceName:        resourceAcl.ResourceName,
				ResourcePatternType: resourceAcl.ResourcePatternType.String(),
				Principal:           acl.Principal,
				Host:                acl.Host,
				Operation:           acl.Operation.String(),
				PermissionType:      acl.PermissionType.String(),
			}
			flattenedAcls = append(flattenedAcls, flattenedAcl)
		}
	}

	return flattenedAcls, nil
}

// isClassicDescribable reports whether a group of the given type can be
// meaningfully described via the classic DescribeGroups API (Kafka API key 15).
// Only classic groups can. An empty type comes from a pre-3.8 broker
// (ListGroups v4, no type field) where every group is classic-protocol anyway,
// so it is describable too. The KIP-848 consumer/share/streams types are NOT:
// asked about one, a 4.x broker answers API 15 with a spec-compliant but useless
// result — state "Dead", no members, no error — whose only correct source is the
// type-specific ConsumerGroupDescribe (API 69), which this client cannot send.
func isClassicDescribable(groupType string) bool {
	return groupType == "" || groupType == types.ConsumerGroupTypeClassic
}

// scanConsumerGroups lists groups with their KIP-848 type and describes them.
// Type-dispatch (design §14.5, corrected): DescribeGroups (API 15) is called
// ONLY for classic (and unknown-type) groups. For consumer/share/streams groups
// we deliberately do NOT call DescribeGroups — its "Dead"/empty answer would
// overwrite the correct state ListGroupsWithType already reported (verified live
// against MSK 4.0). Those groups keep their ListGroups v5 state, get no member
// detail, and are flagged DetailComplete=false (their real detail needs API 69).
func (ks *KafkaService) scanConsumerGroups() (*types.ConsumerGroups, error) {
	slog.Info("🔍 scanning for consumer groups")
	slog.Debug("🔍 scanning for consumer groups", "clusterArn", ks.clusterArn)

	listings, err := ks.groupScanner.ListGroupsWithType()
	if err != nil {
		return nil, fmt.Errorf("failed to list consumer groups: %v", err)
	}
	allIDs := make([]string, 0, len(listings))
	describableIDs := make([]string, 0, len(listings))
	for _, l := range listings {
		allIDs = append(allIDs, l.GroupID)
		if isClassicDescribable(l.Type) {
			describableIDs = append(describableIDs, l.GroupID)
		}
	}
	// Only classic/unknown groups are described; non-classic groups are omitted
	// so their accurate ListGroups v5 state is preserved by MapConsumerGroups.
	descriptions, err := ks.groupScanner.DescribeGroups(describableIDs)
	if err != nil {
		return nil, fmt.Errorf("failed to describe consumer groups: %v", err)
	}
	// Track groups whose describe was denied/errored so their detail is flagged
	// incomplete — a per-group DescribeGroups error (e.g. missing DescribeGroup
	// authz) returns an empty members list that must NOT be presented as complete.
	describeErr := make(map[string]bool)
	for _, gd := range descriptions {
		if gd != nil && gd.Err != sarama.ErrNoError {
			slog.Warn("⚠️ failed to describe a consumer group; recording it without full detail", "group", gd.GroupId, "error", gd.Err)
			describeErr[gd.GroupId] = true
		}
	}
	coordinators := ks.groupScanner.Coordinators(allIDs)

	groups := BuildConsumerGroups(listings, descriptions, coordinators)
	for i := range groups.Details {
		d := &groups.Details[i]
		// DetailComplete only when the group is classic-describable AND its own
		// describe actually succeeded.
		d.DetailComplete = isClassicDescribable(d.Type) && !describeErr[d.GroupID]
	}
	slog.Info("✅ found consumer groups", "count", len(groups.Details))
	return groups, nil
}
