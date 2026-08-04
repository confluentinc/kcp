package types

import (
	"fmt"
	"time"
)

type KafkaAdminClientInformation struct {
	ClusterID         string           `json:"cluster_id"`
	DiscoveredBrokers []string         `json:"discovered_brokers,omitempty"`
	SaslMechanism     string           `json:"sasl_mechanism,omitempty"`
	Topics            *Topics          `json:"topics"`
	Acls              []Acls           `json:"acls"`
	ConnectClusters   []ConnectCluster `json:"connect_clusters,omitempty"`
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

	// Merge ConnectClusters: match by ConnectRestURL, then connectors by name (new wins);
	// endpoints not seen this run are preserved.
	c.ConnectClusters = mergeConnectClusters(c.ConnectClusters, other.ConnectClusters)
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

// SetConnectCluster finds-or-creates the ConnectCluster for connectRestURL and
// replaces its Connectors (a scan returns the complete listing for one endpoint),
// preserving that entry's own Metrics.
func (c *KafkaAdminClientInformation) SetConnectCluster(connectRestURL string, connectors []Connector) {
	for i := range c.ConnectClusters {
		if c.ConnectClusters[i].ConnectRestURL == connectRestURL {
			c.ConnectClusters[i].Connectors = connectors
			return
		}
	}
	c.ConnectClusters = append(c.ConnectClusters, ConnectCluster{ConnectRestURL: connectRestURL, Connectors: connectors})
}

// SetConnectClusterMetrics finds-or-creates the ConnectCluster for connectRestURL
// and sets its cluster-level Metrics, leaving Connectors untouched.
func (c *KafkaAdminClientInformation) SetConnectClusterMetrics(connectRestURL string, metrics *ConnectClusterMetrics) {
	for i := range c.ConnectClusters {
		if c.ConnectClusters[i].ConnectRestURL == connectRestURL {
			c.ConnectClusters[i].Metrics = metrics
			return
		}
	}
	c.ConnectClusters = append(c.ConnectClusters, ConnectCluster{ConnectRestURL: connectRestURL, Metrics: metrics})
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

// mergeConnectClusters merges by ConnectRestURL: new entries added, existing entries'
// connectors merged by name (new wins) and metrics prefer-new-fall-back-to-old;
// endpoints absent from newClusters are preserved untouched.
func mergeConnectClusters(newClusters, oldClusters []ConnectCluster) []ConnectCluster {
	if oldClusters == nil {
		return newClusters
	}
	if newClusters == nil {
		return oldClusters
	}
	oldByURL := make(map[string]ConnectCluster, len(oldClusters))
	for _, cc := range oldClusters {
		oldByURL[cc.ConnectRestURL] = cc
	}
	merged := make([]ConnectCluster, 0, len(oldClusters)+len(newClusters))
	seen := make(map[string]bool)
	for _, newCC := range newClusters {
		seen[newCC.ConnectRestURL] = true
		oldCC, existed := oldByURL[newCC.ConnectRestURL]
		if !existed {
			merged = append(merged, newCC)
			continue
		}
		merged = append(merged, ConnectCluster{
			ConnectRestURL: newCC.ConnectRestURL,
			Connectors:     mergeConnectorList(newCC.Connectors, oldCC.Connectors),
			Metrics:        preferMetrics(newCC.Metrics, oldCC.Metrics),
		})
	}
	for _, oldCC := range oldClusters {
		if !seen[oldCC.ConnectRestURL] {
			merged = append(merged, oldCC)
		}
	}
	return merged
}

// mergeConnectorList merges connectors by Name, new taking precedence.
func mergeConnectorList(newConnectors, oldConnectors []Connector) []Connector {
	byName := make(map[string]Connector, len(oldConnectors)+len(newConnectors))
	for _, c := range oldConnectors {
		byName[c.Name] = c
	}
	for _, c := range newConnectors {
		byName[c.Name] = c
	}
	merged := make([]Connector, 0, len(byName))
	for _, c := range byName {
		merged = append(merged, c)
	}
	return merged
}

// preferMetrics keeps the new run's metrics if present, else the old run's.
func preferMetrics(newMetrics, oldMetrics *ConnectClusterMetrics) *ConnectClusterMetrics {
	if newMetrics != nil {
		return newMetrics
	}
	return oldMetrics
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
