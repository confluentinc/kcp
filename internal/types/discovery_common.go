package types

import (
	"fmt"
	"time"
)

type KafkaAdminClientInformation struct {
	ClusterID             string                 `json:"cluster_id"`
	DiscoveredBrokers     []string               `json:"discovered_brokers,omitempty"`
	SaslMechanism         string                 `json:"sasl_mechanism,omitempty"`
	Topics                *Topics                `json:"topics"`
	Acls                  []Acls                 `json:"acls"`
	SelfManagedConnectors *SelfManagedConnectors `json:"self_managed_connectors"`
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
	// Preserve existing metrics when updating connectors
	var existingMetrics *ProcessedClusterMetrics
	if c.SelfManagedConnectors != nil {
		existingMetrics = c.SelfManagedConnectors.Metrics
	}
	c.SelfManagedConnectors = &SelfManagedConnectors{
		Connectors: connectors,
		Metrics:    existingMetrics,
	}
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
	// Metrics are resolved up front (prefer-new-fall-back-to-old) so neither
	// early return below can silently drop a previously-collected metrics set
	// when one side reports zero connectors (R9).
	metrics := preferConnectorMetrics(newConnectors, oldConnectors)

	if oldConnectors == nil || len(oldConnectors.Connectors) == 0 {
		if newConnectors == nil {
			if metrics == nil {
				return nil
			}
			return &SelfManagedConnectors{Metrics: metrics}
		}
		newConnectors.Metrics = metrics
		return newConnectors
	}
	if newConnectors == nil || len(newConnectors.Connectors) == 0 {
		oldConnectors.Metrics = metrics
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

	return &SelfManagedConnectors{Connectors: merged, Metrics: metrics}
}

// preferConnectorMetrics returns the metrics to keep when merging two
// SelfManagedConnectors: the new run's metrics if present, otherwise the old
// run's, otherwise nil.
func preferConnectorMetrics(newConnectors, oldConnectors *SelfManagedConnectors) *ProcessedClusterMetrics {
	if newConnectors != nil && newConnectors.Metrics != nil {
		return newConnectors.Metrics
	}
	if oldConnectors != nil && oldConnectors.Metrics != nil {
		return oldConnectors.Metrics
	}
	return nil
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

type KcpBuildInfo struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
	Date    string `json:"date"`
}
