package healthcheck

import (
	"strings"
	"testing"
	"time"

	"github.com/confluentinc/kcp/internal/sources"
	"github.com/confluentinc/kcp/internal/types"
)

// fixedTime is used across tests so the rendered Generated timestamp is
// deterministic.
var fixedTime = time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)

// stringPtr is a small helper for building TopicDetails.Configurations entries.
func stringPtr(s string) *string { return &s }

func makeCluster(name string, info *types.KafkaAdminClientInformation, bootstrap []string) sources.ClusterScanResult {
	return sources.ClusterScanResult{
		Identifier: sources.ClusterIdentifier{
			Name:             name,
			UniqueID:         name,
			BootstrapServers: bootstrap,
		},
		KafkaAdminInfo: info,
	}
}

func buildKafkaAdminInfo(topics []types.TopicDetails, acls []types.Acls, brokers []string, clusterID, saslMechanism string) *types.KafkaAdminClientInformation {
	info := &types.KafkaAdminClientInformation{
		ClusterID:         clusterID,
		DiscoveredBrokers: brokers,
		SaslMechanism:     saslMechanism,
		Acls:              acls,
	}
	if topics != nil {
		info.SetTopics(topics)
	}
	return info
}

func TestRenderClusterHealthcheck_EmptyCluster(t *testing.T) {
	info := buildKafkaAdminInfo(nil, nil, []string{"broker1:9092"}, "test-cluster-id", "")
	cluster := makeCluster("empty-cluster", info, []string{"broker1:9092"})

	out := RenderClusterHealthcheck(cluster, fixedTime).String()

	wantContains := []string{
		"# Healthcheck Report — empty-cluster",
		"**Cluster ID:** `test-cluster-id`",
		"**Broker count:** 1",
		"_No topics found._",
		"_No ACLs found._",
		"**Auth:** unspecified",
		"2026-06-18T12:00:00Z",
	}
	for _, want := range wantContains {
		if !strings.Contains(out, want) {
			t.Errorf("expected output to contain %q\n---\n%s", want, out)
		}
	}
}

func TestRenderClusterHealthcheck_TopicsAndAcls(t *testing.T) {
	topics := []types.TopicDetails{
		{
			Name:              "orders",
			Partitions:        6,
			ReplicationFactor: 3,
			Configurations:    map[string]*string{"cleanup.policy": stringPtr("compact")},
		},
		{
			Name:              "events",
			Partitions:        3,
			ReplicationFactor: 1,
			Configurations:    map[string]*string{},
		},
		{
			Name:              "__consumer_offsets",
			Partitions:        50,
			ReplicationFactor: 3,
			Configurations:    map[string]*string{"cleanup.policy": stringPtr("compact")},
		},
	}

	acls := []types.Acls{
		{Principal: "User:alice", ResourceType: "Topic", ResourceName: "orders", ResourcePatternType: "LITERAL", Operation: "Read", PermissionType: "Allow", Host: "*"},
		{Principal: "User:alice", ResourceType: "Topic", ResourceName: "orders", ResourcePatternType: "LITERAL", Operation: "Describe", PermissionType: "Allow", Host: "*"},
		{Principal: "User:bob", ResourceType: "Group", ResourceName: "bob-consumer", ResourcePatternType: "LITERAL", Operation: "Read", PermissionType: "Allow", Host: "*"},
	}

	info := buildKafkaAdminInfo(topics, acls, []string{"broker1:9092", "broker2:9092"}, "real-cluster-id", "SHA256")
	cluster := makeCluster("prod-kafka", info, []string{"broker1:9092", "broker2:9092"})

	out := RenderClusterHealthcheck(cluster, fixedTime).String()

	wantContains := []string{
		"# Healthcheck Report — prod-kafka",
		"**Auth:** SASL (SHA256)",
		"**Broker count:** 2",
		// Topic summary stats — 2 user, 1 internal, 9 user partitions
		"User topics: **2**",
		"Internal topics: **1**",
		"Total user partitions: **9**",
		"Compact topics (user): **1**",
		// Topic table rows (alphabetical: __consumer_offsets, events, orders)
		"| __consumer_offsets | 50 | 3 | yes |",
		"| events | 3 | 1 | no |",
		"| orders | 6 | 3 | no |",
		// ACL counts
		"**Total ACLs:** 3",
		"| User:alice | 2 |",
		"| User:bob | 1 |",
		"| Group | 1 |",
		"| Topic | 2 |",
		// Full ACL list rows
		"| User:alice | Topic | orders | LITERAL | Describe | Allow | * |",
		"| User:alice | Topic | orders | LITERAL | Read | Allow | * |",
		"| User:bob | Group | bob-consumer | LITERAL | Read | Allow | * |",
	}
	for _, want := range wantContains {
		if !strings.Contains(out, want) {
			t.Errorf("expected output to contain %q\n---\n%s", want, out)
		}
	}
}

func TestRenderClusterHealthcheck_IsDeterministic(t *testing.T) {
	// Same input twice should produce identical output (no map iteration leak).
	topics := []types.TopicDetails{
		{Name: "b-topic", Partitions: 1, ReplicationFactor: 1},
		{Name: "a-topic", Partitions: 1, ReplicationFactor: 1},
		{Name: "c-topic", Partitions: 1, ReplicationFactor: 1},
	}
	acls := []types.Acls{
		{Principal: "User:z", ResourceType: "Topic", ResourceName: "z", Operation: "Read", PermissionType: "Allow", Host: "*", ResourcePatternType: "LITERAL"},
		{Principal: "User:a", ResourceType: "Group", ResourceName: "a", Operation: "Read", PermissionType: "Allow", Host: "*", ResourcePatternType: "LITERAL"},
		{Principal: "User:m", ResourceType: "Topic", ResourceName: "m", Operation: "Read", PermissionType: "Allow", Host: "*", ResourcePatternType: "LITERAL"},
	}
	info := buildKafkaAdminInfo(topics, acls, []string{"broker1:9092"}, "det-cluster", "")
	cluster := makeCluster("det", info, []string{"broker1:9092"})

	first := RenderClusterHealthcheck(cluster, fixedTime).String()
	for i := 0; i < 5; i++ {
		again := RenderClusterHealthcheck(cluster, fixedTime).String()
		if again != first {
			t.Fatalf("render output is not deterministic\nfirst:\n%s\nlater (iteration %d):\n%s", first, i, again)
		}
	}
}
