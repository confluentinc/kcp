package acls

import (
	"testing"

	"github.com/confluentinc/kcp/internal/types"
	"github.com/stretchr/testify/require"
)

// TestTranslateStatements exercises translateStatements against the IAM
// policy shapes an AWS principal policy document can take, asserting the
// exact canonical types.Acls tuple (see CANONICALIZATION note on
// translateStatements) rather than just field-by-field spot checks, so a
// casing regression on any enum field fails loudly.
func TestTranslateStatements(t *testing.T) {
	const cluster = "arn:aws:kafka:us-east-1:111122223333:cluster/mymsk/abc-5"
	principal := "arn:aws:iam::111122223333:role/AppRole"
	stmt := func(effect string, actions, resources any) map[string]any {
		return map[string]any{"Statement": []any{map[string]any{"Effect": effect, "Action": actions, "Resource": resources}}}
	}
	cases := []struct {
		name string
		doc  map[string]any
		want []types.Acls
	}{
		// NOTE canonical enum casing: PatternType "Literal"/"Prefixed", PermissionType
		// "Allow"/"Deny", ResourceType "TransactionalID" (capital D) — must match the
		// native reader (internal/migrate/acls/read.go). See the CANONICALIZATION note above.
		{
			name: "ReadData literal topic on this cluster",
			doc:  stmt("Allow", "kafka-cluster:ReadData", "arn:aws:kafka:us-east-1:111122223333:topic/mymsk/abc-5/orders"),
			want: []types.Acls{{ResourceType: "Topic", ResourceName: "orders", ResourcePatternType: "Literal", Principal: "User:AppRole", Host: "*", Operation: "Read", PermissionType: "Allow"}},
		},
		{
			name: "prefixed topic",
			doc:  stmt("Allow", "kafka-cluster:WriteData", "arn:aws:kafka:us-east-1:111122223333:topic/mymsk/abc-5/ord-*"),
			want: []types.Acls{{ResourceType: "Topic", ResourceName: "ord-", ResourcePatternType: "Prefixed", Principal: "User:AppRole", Host: "*", Operation: "Write", PermissionType: "Allow"}},
		},
		{
			name: "multi-resource emits one ACL per resource (not resources[0])",
			doc:  stmt("Allow", "kafka-cluster:ReadData", []any{"arn:aws:kafka:us-east-1:111122223333:topic/mymsk/abc-5/a", "arn:aws:kafka:us-east-1:111122223333:topic/mymsk/abc-5/b"}),
			want: []types.Acls{
				{ResourceType: "Topic", ResourceName: "a", ResourcePatternType: "Literal", Principal: "User:AppRole", Host: "*", Operation: "Read", PermissionType: "Allow"},
				{ResourceType: "Topic", ResourceName: "b", ResourcePatternType: "Literal", Principal: "User:AppRole", Host: "*", Operation: "Read", PermissionType: "Allow"},
			},
		},
		{
			name: "cross-cluster resource is excluded",
			doc:  stmt("Allow", "kafka-cluster:ReadData", "arn:aws:kafka:us-east-1:111122223333:topic/OTHER/zzz-9/x"),
			want: nil,
		},
		{
			name: "wildcard resource matches",
			doc:  stmt("Allow", "kafka-cluster:DescribeCluster", "*"),
			want: []types.Acls{{ResourceType: "Cluster", ResourceName: "kafka-cluster", ResourcePatternType: "Literal", Principal: "User:AppRole", Host: "*", Operation: "Describe", PermissionType: "Allow"}},
		},
		{
			name: "deny effect -> Deny acl (titlecase)",
			doc:  stmt("Deny", "kafka-cluster:WriteData", "arn:aws:kafka:us-east-1:111122223333:topic/mymsk/abc-5/secret"),
			want: []types.Acls{{ResourceType: "Topic", ResourceName: "secret", ResourcePatternType: "Literal", Principal: "User:AppRole", Host: "*", Operation: "Write", PermissionType: "Deny"}},
		},
		{
			name: "transactional-id canonicalizes to TransactionalID (capital D)",
			doc:  stmt("Allow", "kafka-cluster:AlterTransactionalId", "arn:aws:kafka:us-east-1:111122223333:transactional-id/mymsk/abc-5/txn-1"),
			want: []types.Acls{{ResourceType: "TransactionalID", ResourceName: "txn-1", ResourcePatternType: "Literal", Principal: "User:AppRole", Host: "*", Operation: "Write", PermissionType: "Allow"}},
		},
		{
			name: "kafka-cluster:* on a topic ARN expands only to topic ops (not group/txn)",
			doc:  stmt("Allow", "kafka-cluster:*", "arn:aws:kafka:us-east-1:111122223333:topic/mymsk/abc-5/*"),
			want: []types.Acls{
				{ResourceType: "Topic", ResourceName: "*", ResourcePatternType: "Literal", Principal: "User:AppRole", Host: "*", Operation: "Alter", PermissionType: "Allow"},
				{ResourceType: "Topic", ResourceName: "*", ResourcePatternType: "Literal", Principal: "User:AppRole", Host: "*", Operation: "AlterConfigs", PermissionType: "Allow"},
				{ResourceType: "Topic", ResourceName: "*", ResourcePatternType: "Literal", Principal: "User:AppRole", Host: "*", Operation: "Create", PermissionType: "Allow"},
				{ResourceType: "Topic", ResourceName: "*", ResourcePatternType: "Literal", Principal: "User:AppRole", Host: "*", Operation: "Delete", PermissionType: "Allow"},
				{ResourceType: "Topic", ResourceName: "*", ResourcePatternType: "Literal", Principal: "User:AppRole", Host: "*", Operation: "Describe", PermissionType: "Allow"},
				{ResourceType: "Topic", ResourceName: "*", ResourcePatternType: "Literal", Principal: "User:AppRole", Host: "*", Operation: "DescribeConfigs", PermissionType: "Allow"},
				{ResourceType: "Topic", ResourceName: "*", ResourcePatternType: "Literal", Principal: "User:AppRole", Host: "*", Operation: "Read", PermissionType: "Allow"},
				{ResourceType: "Topic", ResourceName: "*", ResourcePatternType: "Literal", Principal: "User:AppRole", Host: "*", Operation: "Write", PermissionType: "Allow"},
			},
		},
		{
			name: "kafka-cluster:* on the whole cluster (Resource *) expands to every AclMap entry",
			doc:  stmt("Allow", "kafka-cluster:*", "*"),
			want: []types.Acls{
				{ResourceType: "Cluster", ResourceName: "kafka-cluster", ResourcePatternType: "Literal", Principal: "User:AppRole", Host: "*", Operation: "Alter", PermissionType: "Allow"},
				{ResourceType: "Cluster", ResourceName: "kafka-cluster", ResourcePatternType: "Literal", Principal: "User:AppRole", Host: "*", Operation: "AlterConfigs", PermissionType: "Allow"},
				{ResourceType: "Cluster", ResourceName: "kafka-cluster", ResourcePatternType: "Literal", Principal: "User:AppRole", Host: "*", Operation: "Describe", PermissionType: "Allow"},
				{ResourceType: "Cluster", ResourceName: "kafka-cluster", ResourcePatternType: "Literal", Principal: "User:AppRole", Host: "*", Operation: "DescribeConfigs", PermissionType: "Allow"},
				{ResourceType: "Cluster", ResourceName: "kafka-cluster", ResourcePatternType: "Literal", Principal: "User:AppRole", Host: "*", Operation: "IdempotentWrite", PermissionType: "Allow"},
				{ResourceType: "Group", ResourceName: "*", ResourcePatternType: "Literal", Principal: "User:AppRole", Host: "*", Operation: "Read", PermissionType: "Allow"},
				{ResourceType: "Group", ResourceName: "*", ResourcePatternType: "Literal", Principal: "User:AppRole", Host: "*", Operation: "Delete", PermissionType: "Allow"},
				{ResourceType: "Group", ResourceName: "*", ResourcePatternType: "Literal", Principal: "User:AppRole", Host: "*", Operation: "Describe", PermissionType: "Allow"},
				{ResourceType: "Topic", ResourceName: "*", ResourcePatternType: "Literal", Principal: "User:AppRole", Host: "*", Operation: "Alter", PermissionType: "Allow"},
				{ResourceType: "Topic", ResourceName: "*", ResourcePatternType: "Literal", Principal: "User:AppRole", Host: "*", Operation: "AlterConfigs", PermissionType: "Allow"},
				{ResourceType: "Topic", ResourceName: "*", ResourcePatternType: "Literal", Principal: "User:AppRole", Host: "*", Operation: "Create", PermissionType: "Allow"},
				{ResourceType: "Topic", ResourceName: "*", ResourcePatternType: "Literal", Principal: "User:AppRole", Host: "*", Operation: "Delete", PermissionType: "Allow"},
				{ResourceType: "Topic", ResourceName: "*", ResourcePatternType: "Literal", Principal: "User:AppRole", Host: "*", Operation: "Describe", PermissionType: "Allow"},
				{ResourceType: "Topic", ResourceName: "*", ResourcePatternType: "Literal", Principal: "User:AppRole", Host: "*", Operation: "DescribeConfigs", PermissionType: "Allow"},
				{ResourceType: "Topic", ResourceName: "*", ResourcePatternType: "Literal", Principal: "User:AppRole", Host: "*", Operation: "Read", PermissionType: "Allow"},
				{ResourceType: "Topic", ResourceName: "*", ResourcePatternType: "Literal", Principal: "User:AppRole", Host: "*", Operation: "Write", PermissionType: "Allow"},
				{ResourceType: "TransactionalID", ResourceName: "*", ResourcePatternType: "Literal", Principal: "User:AppRole", Host: "*", Operation: "Write", PermissionType: "Allow"},
				{ResourceType: "TransactionalID", ResourceName: "*", ResourcePatternType: "Literal", Principal: "User:AppRole", Host: "*", Operation: "Describe", PermissionType: "Allow"},
			},
		},
		{
			name: "action list ([]any) with a non-kafka-cluster action mixed in is ignored",
			doc:  stmt("Allow", []any{"s3:GetObject", "kafka-cluster:ReadData"}, "arn:aws:kafka:us-east-1:111122223333:topic/mymsk/abc-5/orders"),
			want: []types.Acls{{ResourceType: "Topic", ResourceName: "orders", ResourcePatternType: "Literal", Principal: "User:AppRole", Host: "*", Operation: "Read", PermissionType: "Allow"}},
		},
		{
			name: "unmapped kafka-cluster action is skipped",
			doc:  stmt("Allow", "kafka-cluster:NotARealAction", "arn:aws:kafka:us-east-1:111122223333:topic/mymsk/abc-5/orders"),
			want: nil,
		},
		{
			name: "group literal resource",
			doc:  stmt("Allow", "kafka-cluster:DescribeGroup", "arn:aws:kafka:us-east-1:111122223333:group/mymsk/abc-5/my-group"),
			want: []types.Acls{{ResourceType: "Group", ResourceName: "my-group", ResourcePatternType: "Literal", Principal: "User:AppRole", Host: "*", Operation: "Describe", PermissionType: "Allow"}},
		},
		{
			name: "group prefixed resource",
			doc:  stmt("Allow", "kafka-cluster:DeleteGroup", "arn:aws:kafka:us-east-1:111122223333:group/mymsk/abc-5/svc-*"),
			want: []types.Acls{{ResourceType: "Group", ResourceName: "svc-", ResourcePatternType: "Prefixed", Principal: "User:AppRole", Host: "*", Operation: "Delete", PermissionType: "Allow"}},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := translateStatements(principal, cluster, c.doc)
			require.ElementsMatch(t, c.want, got)
		})
	}
}

// TestClusterArnMatches exercises the wildcard-aware cluster-scoping gate in
// isolation from translation, since a mistake here silently leaks
// cross-cluster ACLs into the migration (or silently drops legitimate
// same-cluster ones).
func TestClusterArnMatches(t *testing.T) {
	const cluster = "arn:aws:kafka:us-east-1:111122223333:cluster/mymsk/abc-5"

	tests := []struct {
		name    string
		grant   string
		cluster string
		want    bool
	}{
		{
			name:    "this cluster literal topic ARN",
			grant:   "arn:aws:kafka:us-east-1:111122223333:topic/mymsk/abc-5/orders",
			cluster: cluster,
			want:    true,
		},
		{
			name:    "this cluster literal, the cluster ARN itself",
			grant:   cluster,
			cluster: cluster,
			want:    true,
		},
		{
			name:    "cross-cluster: different name and uuid",
			grant:   "arn:aws:kafka:us-east-1:111122223333:topic/OTHER/zzz-9/x",
			cluster: cluster,
			want:    false,
		},
		{
			name:    "cross-cluster: same name, different uuid (e.g. cluster recreated)",
			grant:   "arn:aws:kafka:us-east-1:111122223333:topic/mymsk/other-uuid/x",
			cluster: cluster,
			want:    false,
		},
		{
			name:    "bare wildcard resource matches any cluster",
			grant:   "*",
			cluster: cluster,
			want:    true,
		},
		{
			name:    "arn:aws:kafka:*:acct:* style full wildcard matches",
			grant:   "arn:aws:kafka:*:111122223333:*",
			cluster: cluster,
			want:    true,
		},
		{
			name:    "region wildcard with specific resource still matches this cluster",
			grant:   "arn:aws:kafka:*:111122223333:topic/mymsk/abc-5/orders",
			cluster: cluster,
			want:    true,
		},
		{
			name:    "different region, same cluster name+uuid still matches (region is not part of cluster identity)",
			grant:   "arn:aws:kafka:us-west-2:111122223333:topic/mymsk/abc-5/orders",
			cluster: cluster,
			want:    true,
		},
		{
			name:    "malformed grant ARN (no resource path) does not match",
			grant:   "arn:aws:kafka:us-east-1:111122223333:garbage",
			cluster: cluster,
			want:    false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, clusterArnMatches(tt.grant, tt.cluster))
		})
	}
}
