package healthcheck

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/confluentinc/kcp/internal/build_info"
	"github.com/confluentinc/kcp/internal/services/markdown"
	"github.com/confluentinc/kcp/internal/sources"
	"github.com/confluentinc/kcp/internal/types"
)

// RenderClusterHealthcheck builds a markdown report for a single cluster's
// healthcheck scan result. It is a pure function — same inputs always
// produce the same output, which makes it straightforward to test.
//
// The timestamp is taken as a parameter (rather than read from time.Now()
// inside the function) so tests can pin it to a deterministic value.
func RenderClusterHealthcheck(cluster sources.ClusterScanResult, timestamp time.Time) *markdown.Markdown {
	m := markdown.New()
	info := cluster.KafkaAdminInfo

	// --- Header ---
	m.AddHeading(fmt.Sprintf("Healthcheck Report — %s", cluster.Identifier.Name), 1)
	m.AddParagraph(fmt.Sprintf("**Generated:** %s", timestamp.UTC().Format(time.RFC3339)))
	m.AddParagraph(fmt.Sprintf("**KCP version:** %s (%s)", build_info.Version, build_info.Commit))
	m.AddParagraph(fmt.Sprintf("**Bootstrap servers:** %s", strings.Join(cluster.Identifier.BootstrapServers, ", ")))
	m.AddParagraph(fmt.Sprintf("**Auth:** %s", describeAuth(info)))

	// --- Cluster ---
	m.AddHeading("Cluster", 2)
	m.AddParagraph(fmt.Sprintf("**Cluster ID:** `%s`", info.ClusterID))
	m.AddParagraph(fmt.Sprintf("**Broker count:** %d", len(info.DiscoveredBrokers)))
	if len(info.DiscoveredBrokers) > 0 {
		m.AddParagraph("**Discovered brokers:**")
		m.AddList(info.DiscoveredBrokers)
	}

	// --- Topics ---
	m.AddHeading("Topics", 2)
	renderTopicsSection(m, info)

	// --- ACLs ---
	m.AddHeading("ACLs", 2)
	renderAclsSection(m, info)

	return m
}

// describeAuth produces a short human-readable auth descriptor from the
// scan result. KafkaAdminClientInformation only records SaslMechanism, so
// for non-SASL auth types we fall back to a generic label.
func describeAuth(info *types.KafkaAdminClientInformation) string {
	if info.SaslMechanism != "" {
		return fmt.Sprintf("SASL (%s)", info.SaslMechanism)
	}
	return "unspecified"
}

func renderTopicsSection(m *markdown.Markdown, info *types.KafkaAdminClientInformation) {
	if info.Topics == nil || len(info.Topics.Details) == 0 {
		m.AddParagraph("_No topics found._")
		return
	}

	summary := info.Topics.Summary
	m.AddList([]string{
		fmt.Sprintf("User topics: **%d**", summary.Topics),
		fmt.Sprintf("Internal topics: **%d**", summary.InternalTopics),
		fmt.Sprintf("Total user partitions: **%d**", summary.TotalPartitions),
		fmt.Sprintf("Total internal partitions: **%d**", summary.TotalInternalPartitions),
		fmt.Sprintf("Compact topics (user): **%d**", summary.CompactTopics),
		fmt.Sprintf("Remote-storage topics: **%d**", summary.RemoteStorageTopics),
	})

	// Sort topics by name for deterministic output.
	topics := make([]types.TopicDetails, len(info.Topics.Details))
	copy(topics, info.Topics.Details)
	sort.Slice(topics, func(i, j int) bool { return topics[i].Name < topics[j].Name })

	headers := []string{"Name", "Partitions", "Replication factor", "Internal"}
	rows := make([][]string, 0, len(topics))
	for _, t := range topics {
		internal := "no"
		if strings.HasPrefix(t.Name, "__") {
			internal = "yes"
		}
		rows = append(rows, []string{
			t.Name,
			strconv.Itoa(t.Partitions),
			strconv.Itoa(t.ReplicationFactor),
			internal,
		})
	}
	m.AddTable(headers, rows)
}

func renderAclsSection(m *markdown.Markdown, info *types.KafkaAdminClientInformation) {
	if len(info.Acls) == 0 {
		m.AddParagraph("_No ACLs found._")
		return
	}

	byPrincipal := make(map[string]int)
	byResource := make(map[string]int)
	for _, acl := range info.Acls {
		byPrincipal[acl.Principal]++
		byResource[acl.ResourceType]++
	}

	m.AddParagraph(fmt.Sprintf("**Total ACLs:** %d", len(info.Acls)))

	m.AddHeading("By principal", 3)
	m.AddTable([]string{"Principal", "Count"}, mapToSortedRows(byPrincipal))

	m.AddHeading("By resource type", 3)
	m.AddTable([]string{"Resource type", "Count"}, mapToSortedRows(byResource))

	// Sort ACLs deterministically by (principal, resource type, resource name, operation).
	acls := make([]types.Acls, len(info.Acls))
	copy(acls, info.Acls)
	sort.Slice(acls, func(i, j int) bool {
		if acls[i].Principal != acls[j].Principal {
			return acls[i].Principal < acls[j].Principal
		}
		if acls[i].ResourceType != acls[j].ResourceType {
			return acls[i].ResourceType < acls[j].ResourceType
		}
		if acls[i].ResourceName != acls[j].ResourceName {
			return acls[i].ResourceName < acls[j].ResourceName
		}
		return acls[i].Operation < acls[j].Operation
	})

	m.AddHeading("Full ACL list", 3)
	headers := []string{"Principal", "Resource type", "Resource name", "Pattern", "Operation", "Permission", "Host"}
	rows := make([][]string, 0, len(acls))
	for _, a := range acls {
		rows = append(rows, []string{
			a.Principal,
			a.ResourceType,
			a.ResourceName,
			a.ResourcePatternType,
			a.Operation,
			a.PermissionType,
			a.Host,
		})
	}
	m.AddTable(headers, rows)
}

// mapToSortedRows converts a count map into table rows sorted by key for
// deterministic output.
func mapToSortedRows(counts map[string]int) [][]string {
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	rows := make([][]string, 0, len(keys))
	for _, k := range keys {
		rows = append(rows, []string{k, strconv.Itoa(counts[k])})
	}
	return rows
}
