package types

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestAclMap_MatchesAuthoritativeTable is an assert-only pin of AclMap's
// semantics against the authoritative source: the "Semantics of IAM
// authorization policy actions and resources" table reproduced in
// ~/claude-plans/kcp/direct-api-migration/plans/acl-research/aws-msk.md
// (§3, "Full list of MSK IAM data-plane actions (kafka-cluster:*)"), which
// is itself sourced from:
//   - https://docs.aws.amazon.com/msk/latest/developerguide/kafka-actions.html
//   - https://docs.aws.amazon.com/service-authorization/latest/reference/list_apachekafkaapisforamazonmskclusters.html
//
// types.AclMap is a SHIPPED dependency of `create-asset migrate-acls` — this
// test intentionally does NOT modify it. It exists to (a) pin AclMap's
// current behaviour so future edits are caught, and (b) surface any
// semantic mismatch against the authoritative table for a human to triage.
//
// Casing note: the authoritative table's action names use AWS's own
// spelling. AclMap keys already match that spelling. Where AclMap's
// ResourceType value differs only in casing from a hypothetical
// "TransactionalID" spelling (it uses "TransactionalId"), that is a KNOWN,
// INTENTIONAL casing choice resolved at a later translation-layer task —
// NOT a semantic bug — so `want` below uses AclMap's own casing throughout
// and must not fail because of it.
//
// kafka-cluster:Connect is deliberately excluded: per the authoritative
// table its "Kafka ACL equivalent" is "(connect + authenticate)" — a bare
// connection/authentication permission with no Kafka ACL Operation/
// ResourceType equivalent. It legitimately has no AclMap entry.
func TestAclMap_MatchesAuthoritativeTable(t *testing.T) {
	t.Parallel()

	want := map[string]ACLMapping{
		// --- Cluster-scoped ---
		"kafka-cluster:DescribeCluster": {
			Operation: "Describe", ResourceType: "Cluster", RequiresPattern: false,
		},
		"kafka-cluster:AlterCluster": {
			Operation: "Alter", ResourceType: "Cluster", RequiresPattern: false,
		},
		"kafka-cluster:DescribeClusterDynamicConfiguration": {
			Operation: "DescribeConfigs", ResourceType: "Cluster", RequiresPattern: false,
		},
		"kafka-cluster:AlterClusterDynamicConfiguration": {
			Operation: "AlterConfigs", ResourceType: "Cluster", RequiresPattern: false,
		},
		// DISCREPANCY (observation, not a triage-blocking one): aws-msk.md maps
		// this to "IDEMPOTENT_WRITE CLUSTER" — same Cluster-scoped shape as the
		// four entries above, all of which have RequiresPattern: false (a
		// cluster is a single fixed-name resource; AclMap's own generator,
		// cmd/create_asset/migrate_acls/iam_acls/iam_acls_generator.go, always
		// resolves Cluster to the literal resource name "kafka-cluster"
		// regardless of this flag, so behaviour is unaffected either way).
		// AclMap's actual value is RequiresPattern: true, which breaks that
		// pattern. aws-msk.md's table has no explicit "requires pattern"
		// column to check this against directly (Operation and ResourceType
		// — the two fields this task is scoped to police — are both
		// correct), so this is pinned to AclMap's real value rather than
		// "fixed", and is flagged here for visibility.
		"kafka-cluster:WriteDataIdempotently": {
			Operation: "IdempotentWrite", ResourceType: "Cluster", RequiresPattern: true,
		},

		// --- Topic-scoped ---
		"kafka-cluster:CreateTopic": {
			Operation: "Create", ResourceType: "Topic", RequiresPattern: true,
		},
		"kafka-cluster:DescribeTopic": {
			Operation: "Describe", ResourceType: "Topic", RequiresPattern: true,
		},
		"kafka-cluster:AlterTopic": {
			Operation: "Alter", ResourceType: "Topic", RequiresPattern: true,
		},
		"kafka-cluster:DeleteTopic": {
			Operation: "Delete", ResourceType: "Topic", RequiresPattern: true,
		},
		"kafka-cluster:DescribeTopicDynamicConfiguration": {
			Operation: "DescribeConfigs", ResourceType: "Topic", RequiresPattern: true,
		},
		"kafka-cluster:AlterTopicDynamicConfiguration": {
			Operation: "AlterConfigs", ResourceType: "Topic", RequiresPattern: true,
		},
		// Non-obvious per aws-msk.md: ReadData (consume) also requires
		// AlterGroup ("Required actions" column) because consuming implies
		// joining a group — that's an IAM co-requirement, not something
		// AclMap itself encodes (AclMap maps one IAM action to one ACL, it
		// does not expand required-action bundles), so it's out of scope here.
		"kafka-cluster:ReadData": {
			Operation: "Read", ResourceType: "Topic", RequiresPattern: true,
		},
		"kafka-cluster:WriteData": {
			Operation: "Write", ResourceType: "Topic", RequiresPattern: true,
		},

		// --- Group-scoped ---
		"kafka-cluster:DescribeGroup": {
			Operation: "Describe", ResourceType: "Group", RequiresPattern: true,
		},
		// Non-obvious per aws-msk.md: "AlterGroup" == Kafka READ GROUP (i.e.
		// "join a consumer group"), NOT "alter group config". AclMap correctly
		// encodes this as Operation: "Read", not "Alter".
		"kafka-cluster:AlterGroup": {
			Operation: "Read", ResourceType: "Group", RequiresPattern: true,
		},
		"kafka-cluster:DeleteGroup": {
			Operation: "Delete", ResourceType: "Group", RequiresPattern: true,
		},

		// --- TransactionalId-scoped ---
		"kafka-cluster:DescribeTransactionalId": {
			Operation: "Describe", ResourceType: "TransactionalId", RequiresPattern: true,
		},
		// Non-obvious per aws-msk.md: "AlterTransactionalId" == Kafka WRITE
		// TRANSACTIONAL_ID (not "Alter"). AclMap correctly encodes this as
		// Operation: "Write".
		"kafka-cluster:AlterTransactionalId": {
			Operation: "Write", ResourceType: "TransactionalId", RequiresPattern: true,
		},
	}

	// Every kafka-cluster: action in AclMap must be accounted for above -
	// this guards against silently missing coverage if AclMap grows.
	require.Len(t, AclMap, len(want),
		"AclMap has a different number of entries than this test's authoritative want map; "+
			"update this test to cover every kafka-cluster: action (see aws-msk.md)")

	for action, w := range want {
		got, ok := AclMap[action]
		require.True(t, ok, "AclMap missing %s (present in aws-msk.md's authoritative table)", action)
		require.Equal(t, w, got, "AclMap[%s] disagrees with aws-msk.md", action)
	}
}
