package acls

import (
	"testing"

	"github.com/confluentinc/kcp/internal/types"
	"github.com/stretchr/testify/require"
)

// TestNormalizeForCC is table-driven: each case exercises one rule from
// design §5 in isolation (plus a couple of interaction cases), asserting
// both the surviving ACL set and the diagnostics emitted for it.
func TestNormalizeForCC(t *testing.T) {
	tests := []struct {
		name      string
		in        []types.Acls
		wantOut   []types.Acls
		wantDiags []Diagnostic
	}{
		{
			name: "deny preserved unchanged",
			in: []types.Acls{
				{ResourceType: "Topic", ResourceName: "orders", ResourcePatternType: "Literal", Principal: "User:app", Host: "*", Operation: "Read", PermissionType: "Deny"},
			},
			wantOut: []types.Acls{
				{ResourceType: "Topic", ResourceName: "orders", ResourcePatternType: "Literal", Principal: "User:app", Host: "*", Operation: "Read", PermissionType: "Deny"},
			},
			wantDiags: nil,
		},
		{
			name: "non-wildcard host normalized to star with warn",
			in: []types.Acls{
				{ResourceType: "Topic", ResourceName: "orders", ResourcePatternType: "Literal", Principal: "User:app", Host: "1.2.3.4", Operation: "Read", PermissionType: "Allow"},
			},
			wantOut: []types.Acls{
				{ResourceType: "Topic", ResourceName: "orders", ResourcePatternType: "Literal", Principal: "User:app", Host: "*", Operation: "Read", PermissionType: "Allow"},
			},
			wantDiags: []Diagnostic{
				{Level: "warn", Message: `host "1.2.3.4" for principal "User:app" on Topic "orders" normalized to "*": Confluent Cloud does not support host-scoped ACLs`},
			},
		},
		{
			name: "ClusterAction dropped with info diagnostic",
			in: []types.Acls{
				{ResourceType: "Cluster", ResourceName: "kafka-cluster", ResourcePatternType: "Literal", Principal: "User:broker", Host: "*", Operation: "ClusterAction", PermissionType: "Allow"},
			},
			wantOut: nil,
			wantDiags: []Diagnostic{
				{Level: "info", Message: `dropped ClusterAction ACL for principal "User:broker" on Cluster "kafka-cluster": inter-broker operation, not applicable to a CC client`},
			},
		},
		{
			name: "DelegationToken resource type dropped with info diagnostic",
			in: []types.Acls{
				{ResourceType: "DelegationToken", ResourceName: "token-1", ResourcePatternType: "Literal", Principal: "User:app", Host: "*", Operation: "Describe", PermissionType: "Allow"},
			},
			wantOut: nil,
			wantDiags: []Diagnostic{
				{Level: "info", Message: `dropped DelegationToken ACL for principal "User:app": delegation tokens have no CC equivalent`},
			},
		},
		{
			name: "MSK broker principal dropped with info diagnostic",
			in: []types.Acls{
				{ResourceType: "Topic", ResourceName: "orders", ResourcePatternType: "Literal", Principal: "User:CN=b-1.mytestcluster.abcde.c2.kafka.us-east-1.amazonaws.com", Host: "*", Operation: "Read", PermissionType: "Allow"},
			},
			wantOut: nil,
			wantDiags: []Diagnostic{
				{Level: "info", Message: `dropped ACL for principal "User:CN=b-1.mytestcluster.abcde.c2.kafka.us-east-1.amazonaws.com": matches an MSK broker identity, not a customer principal`},
			},
		},
		{
			name: "redundant allow Describe dropped when allow Read exists on same resource+principal+host",
			in: []types.Acls{
				{ResourceType: "Topic", ResourceName: "orders", ResourcePatternType: "Literal", Principal: "User:app", Host: "*", Operation: "Describe", PermissionType: "Allow"},
				{ResourceType: "Topic", ResourceName: "orders", ResourcePatternType: "Literal", Principal: "User:app", Host: "*", Operation: "Read", PermissionType: "Allow"},
			},
			wantOut: []types.Acls{
				{ResourceType: "Topic", ResourceName: "orders", ResourcePatternType: "Literal", Principal: "User:app", Host: "*", Operation: "Read", PermissionType: "Allow"},
			},
			wantDiags: []Diagnostic{
				{Level: "info", Message: `dropped redundant Allow Describe ACL for principal "User:app" on Topic "orders": implied by an existing Allow Read/Write/Alter/Delete`},
			},
		},
		{
			name: "redundant allow DescribeConfigs dropped when allow AlterConfigs exists on same resource+principal+host",
			in: []types.Acls{
				{ResourceType: "Topic", ResourceName: "orders", ResourcePatternType: "Literal", Principal: "User:app", Host: "*", Operation: "DescribeConfigs", PermissionType: "Allow"},
				{ResourceType: "Topic", ResourceName: "orders", ResourcePatternType: "Literal", Principal: "User:app", Host: "*", Operation: "AlterConfigs", PermissionType: "Allow"},
			},
			wantOut: []types.Acls{
				{ResourceType: "Topic", ResourceName: "orders", ResourcePatternType: "Literal", Principal: "User:app", Host: "*", Operation: "AlterConfigs", PermissionType: "Allow"},
			},
			wantDiags: []Diagnostic{
				{Level: "info", Message: `dropped redundant Allow DescribeConfigs ACL for principal "User:app" on Topic "orders": implied by an existing Allow AlterConfigs`},
			},
		},
		{
			name: "deny Describe is never deduped even when allow Read exists on same resource+principal+host",
			in: []types.Acls{
				{ResourceType: "Topic", ResourceName: "orders", ResourcePatternType: "Literal", Principal: "User:app", Host: "*", Operation: "Describe", PermissionType: "Deny"},
				{ResourceType: "Topic", ResourceName: "orders", ResourcePatternType: "Literal", Principal: "User:app", Host: "*", Operation: "Read", PermissionType: "Allow"},
			},
			wantOut: []types.Acls{
				{ResourceType: "Topic", ResourceName: "orders", ResourcePatternType: "Literal", Principal: "User:app", Host: "*", Operation: "Describe", PermissionType: "Deny"},
				{ResourceType: "Topic", ResourceName: "orders", ResourcePatternType: "Literal", Principal: "User:app", Host: "*", Operation: "Read", PermissionType: "Allow"},
			},
			wantDiags: nil,
		},
		{
			name: "Write on Group is CC-invalid: dropped with warn diagnostic",
			in: []types.Acls{
				{ResourceType: "Group", ResourceName: "consumer-1", ResourcePatternType: "Literal", Principal: "User:app", Host: "*", Operation: "Write", PermissionType: "Allow"},
			},
			wantOut: nil,
			wantDiags: []Diagnostic{
				{Level: "warn", Message: `dropped Allow Group ACL for principal "User:app": operation "Write" is not valid for resource type Group on Confluent Cloud`},
			},
		},
		{
			// Each op lives on a distinct resource so operation-implication
			// de-dup (which would otherwise drop Describe once Read is
			// present on the SAME resource+principal+host) doesn't
			// interfere — this case isolates the validity rule only.
			name: "Group allows Read, Describe, Delete individually unchanged",
			in: []types.Acls{
				{ResourceType: "Group", ResourceName: "consumer-1", ResourcePatternType: "Literal", Principal: "User:app", Host: "*", Operation: "Read", PermissionType: "Allow"},
				{ResourceType: "Group", ResourceName: "consumer-2", ResourcePatternType: "Literal", Principal: "User:app", Host: "*", Operation: "Describe", PermissionType: "Allow"},
				{ResourceType: "Group", ResourceName: "consumer-3", ResourcePatternType: "Literal", Principal: "User:app", Host: "*", Operation: "Delete", PermissionType: "Allow"},
			},
			wantOut: []types.Acls{
				{ResourceType: "Group", ResourceName: "consumer-1", ResourcePatternType: "Literal", Principal: "User:app", Host: "*", Operation: "Read", PermissionType: "Allow"},
				{ResourceType: "Group", ResourceName: "consumer-2", ResourcePatternType: "Literal", Principal: "User:app", Host: "*", Operation: "Describe", PermissionType: "Allow"},
				{ResourceType: "Group", ResourceName: "consumer-3", ResourcePatternType: "Literal", Principal: "User:app", Host: "*", Operation: "Delete", PermissionType: "Allow"},
			},
			wantDiags: nil,
		},
		{
			name: "redundant allow Describe dropped on Group too when allow Read exists on the same resource+principal+host",
			in: []types.Acls{
				{ResourceType: "Group", ResourceName: "consumer-1", ResourcePatternType: "Literal", Principal: "User:app", Host: "*", Operation: "Read", PermissionType: "Allow"},
				{ResourceType: "Group", ResourceName: "consumer-1", ResourcePatternType: "Literal", Principal: "User:app", Host: "*", Operation: "Describe", PermissionType: "Allow"},
			},
			wantOut: []types.Acls{
				{ResourceType: "Group", ResourceName: "consumer-1", ResourcePatternType: "Literal", Principal: "User:app", Host: "*", Operation: "Read", PermissionType: "Allow"},
			},
			wantDiags: []Diagnostic{
				{Level: "info", Message: `dropped redundant Allow Describe ACL for principal "User:app" on Group "consumer-1": implied by an existing Allow Read/Write/Alter/Delete`},
			},
		},
		{
			name: "Read on TransactionalId is CC-invalid: dropped with warn diagnostic",
			in: []types.Acls{
				{ResourceType: "TransactionalId", ResourceName: "txn-1", ResourcePatternType: "Literal", Principal: "User:app", Host: "*", Operation: "Read", PermissionType: "Allow"},
			},
			wantOut: nil,
			wantDiags: []Diagnostic{
				{Level: "warn", Message: `dropped Allow TransactionalId ACL for principal "User:app": operation "Read" is not valid for resource type TransactionalId on Confluent Cloud`},
			},
		},
		{
			// Describe and Write live on distinct resources so the
			// operation-implication de-dup rule (Write ALLOW implies
			// Describe redundant on the SAME resource+principal+host)
			// doesn't interfere — this case isolates the validity rule only.
			name: "TransactionalId allows Describe and Write individually unchanged",
			in: []types.Acls{
				{ResourceType: "TransactionalId", ResourceName: "txn-1", ResourcePatternType: "Literal", Principal: "User:app", Host: "*", Operation: "Describe", PermissionType: "Allow"},
				{ResourceType: "TransactionalId", ResourceName: "txn-2", ResourcePatternType: "Literal", Principal: "User:app", Host: "*", Operation: "Write", PermissionType: "Allow"},
			},
			wantOut: []types.Acls{
				{ResourceType: "TransactionalId", ResourceName: "txn-1", ResourcePatternType: "Literal", Principal: "User:app", Host: "*", Operation: "Describe", PermissionType: "Allow"},
				{ResourceType: "TransactionalId", ResourceName: "txn-2", ResourcePatternType: "Literal", Principal: "User:app", Host: "*", Operation: "Write", PermissionType: "Allow"},
			},
			wantDiags: nil,
		},
		{
			name: "redundant allow Describe dropped on TransactionalId too when allow Write exists on the same resource+principal+host",
			in: []types.Acls{
				{ResourceType: "TransactionalId", ResourceName: "txn-1", ResourcePatternType: "Literal", Principal: "User:app", Host: "*", Operation: "Describe", PermissionType: "Allow"},
				{ResourceType: "TransactionalId", ResourceName: "txn-1", ResourcePatternType: "Literal", Principal: "User:app", Host: "*", Operation: "Write", PermissionType: "Allow"},
			},
			wantOut: []types.Acls{
				{ResourceType: "TransactionalId", ResourceName: "txn-1", ResourcePatternType: "Literal", Principal: "User:app", Host: "*", Operation: "Write", PermissionType: "Allow"},
			},
			wantDiags: []Diagnostic{
				{Level: "info", Message: `dropped redundant Allow Describe ACL for principal "User:app" on TransactionalId "txn-1": implied by an existing Allow Read/Write/Alter/Delete`},
			},
		},
		{
			name: "Prefixed pattern type preserved",
			in: []types.Acls{
				{ResourceType: "Topic", ResourceName: "orders-", ResourcePatternType: "Prefixed", Principal: "User:app", Host: "*", Operation: "Write", PermissionType: "Allow"},
			},
			wantOut: []types.Acls{
				{ResourceType: "Topic", ResourceName: "orders-", ResourcePatternType: "Prefixed", Principal: "User:app", Host: "*", Operation: "Write", PermissionType: "Allow"},
			},
			wantDiags: nil,
		},
		{
			name: "Literal pattern type preserved",
			in: []types.Acls{
				{ResourceType: "Topic", ResourceName: "orders", ResourcePatternType: "Literal", Principal: "User:app", Host: "*", Operation: "Write", PermissionType: "Allow"},
			},
			wantOut: []types.Acls{
				{ResourceType: "Topic", ResourceName: "orders", ResourcePatternType: "Literal", Principal: "User:app", Host: "*", Operation: "Write", PermissionType: "Allow"},
			},
			wantDiags: nil,
		},
		{
			name: "Cluster and Topic keep the standard operation set unrestricted (IdempotentWrite on Cluster)",
			in: []types.Acls{
				{ResourceType: "Cluster", ResourceName: "kafka-cluster", ResourcePatternType: "Literal", Principal: "User:app", Host: "*", Operation: "IdempotentWrite", PermissionType: "Allow"},
			},
			wantOut: []types.Acls{
				{ResourceType: "Cluster", ResourceName: "kafka-cluster", ResourcePatternType: "Literal", Principal: "User:app", Host: "*", Operation: "IdempotentWrite", PermissionType: "Allow"},
			},
			wantDiags: nil,
		},
		{
			name: "different hosts on Read vs Describe still collapse to a single ACL after host normalization",
			in: []types.Acls{
				{ResourceType: "Topic", ResourceName: "orders", ResourcePatternType: "Literal", Principal: "User:app", Host: "1.2.3.4", Operation: "Describe", PermissionType: "Allow"},
				{ResourceType: "Topic", ResourceName: "orders", ResourcePatternType: "Literal", Principal: "User:app", Host: "5.6.7.8", Operation: "Read", PermissionType: "Allow"},
			},
			wantOut: []types.Acls{
				{ResourceType: "Topic", ResourceName: "orders", ResourcePatternType: "Literal", Principal: "User:app", Host: "*", Operation: "Read", PermissionType: "Allow"},
			},
			wantDiags: []Diagnostic{
				{Level: "warn", Message: `host "1.2.3.4" for principal "User:app" on Topic "orders" normalized to "*": Confluent Cloud does not support host-scoped ACLs`},
				{Level: "warn", Message: `host "5.6.7.8" for principal "User:app" on Topic "orders" normalized to "*": Confluent Cloud does not support host-scoped ACLs`},
				{Level: "info", Message: `dropped redundant Allow Describe ACL for principal "User:app" on Topic "orders": implied by an existing Allow Read/Write/Alter/Delete`},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotOut, gotDiags := NormalizeForCC(tc.in)
			require.Equal(t, tc.wantOut, gotOut)
			require.Equal(t, tc.wantDiags, gotDiags)
		})
	}
}
