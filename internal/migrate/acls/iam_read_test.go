package acls

import (
	"context"
	"errors"
	"testing"

	iamservice "github.com/confluentinc/kcp/internal/services/iam"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/stretchr/testify/require"
)

const testCluster = "arn:aws:kafka:us-east-1:111122223333:cluster/mymsk/abc-5"

// TestReadIAMACLs_InlinePolicy exercises the single-principal, single-inline-
// policy path end to end: normalize -> fetch -> translateStatements,
// asserting the exact canonical types.Acls tuple (see iam_translate.go's
// CANONICALIZATION note) rather than a loose subset check.
func TestReadIAMACLs_InlinePolicy(t *testing.T) {
	fake := func(ctx context.Context, arn string) (*iamservice.PrincipalPolicies, error) {
		return &iamservice.PrincipalPolicies{
			PrincipalArn: arn, PrincipalName: "AppRole", PrincipalType: "role",
			InlinePolicies: []iamservice.InlinePolicy{{PolicyName: "p", PolicyDocument: map[string]any{
				"Statement": []any{map[string]any{"Effect": "Allow", "Action": "kafka-cluster:ReadData",
					"Resource": "arn:aws:kafka:us-east-1:111122223333:topic/mymsk/abc-5/orders"}},
			}}},
		}, nil
	}

	got, err := ReadIAMACLs(context.Background(), fake, []string{"arn:aws:iam::111122223333:role/AppRole"}, testCluster)

	require.NoError(t, err)
	require.Equal(t, []types.Acls{
		{ResourceType: "Topic", ResourceName: "orders", ResourcePatternType: "Literal", Principal: "User:AppRole", Host: "*", Operation: "Read", PermissionType: "Allow"},
	}, got)
}

// TestReadIAMACLs_InlineAndAttachedPolicies covers a principal with BOTH an
// inline and an attached policy: both documents must be translated and their
// results concatenated, not just the inline (or just the attached) set.
func TestReadIAMACLs_InlineAndAttachedPolicies(t *testing.T) {
	fake := func(ctx context.Context, arn string) (*iamservice.PrincipalPolicies, error) {
		return &iamservice.PrincipalPolicies{
			PrincipalArn: arn, PrincipalName: "AppRole", PrincipalType: "role",
			InlinePolicies: []iamservice.InlinePolicy{{PolicyName: "inline", PolicyDocument: map[string]any{
				"Statement": []any{map[string]any{"Effect": "Allow", "Action": "kafka-cluster:ReadData",
					"Resource": "arn:aws:kafka:us-east-1:111122223333:topic/mymsk/abc-5/orders"}},
			}}},
			AttachedPolicies: []iamservice.AttachedPolicy{{PolicyName: "attached", PolicyArn: "arn:aws:iam::111122223333:policy/attached", PolicyDocument: map[string]any{
				"Statement": []any{map[string]any{"Effect": "Allow", "Action": "kafka-cluster:WriteData",
					"Resource": "arn:aws:kafka:us-east-1:111122223333:topic/mymsk/abc-5/payments"}},
			}}},
		}, nil
	}

	got, err := ReadIAMACLs(context.Background(), fake, []string{"arn:aws:iam::111122223333:role/AppRole"}, testCluster)

	require.NoError(t, err)
	require.ElementsMatch(t, []types.Acls{
		{ResourceType: "Topic", ResourceName: "orders", ResourcePatternType: "Literal", Principal: "User:AppRole", Host: "*", Operation: "Read", PermissionType: "Allow"},
		{ResourceType: "Topic", ResourceName: "payments", ResourcePatternType: "Literal", Principal: "User:AppRole", Host: "*", Operation: "Write", PermissionType: "Allow"},
	}, got)
}

// TestReadIAMACLs_MultiplePrincipals covers two distinct principals: each is
// fetched and translated independently, and the results are concatenated
// (no dedupe here — that happens downstream in the reconciler).
func TestReadIAMACLs_MultiplePrincipals(t *testing.T) {
	fake := func(ctx context.Context, arn string) (*iamservice.PrincipalPolicies, error) {
		var policyDoc map[string]any
		var name string
		switch arn {
		case "arn:aws:iam::111122223333:role/AppRole":
			name = "AppRole"
			policyDoc = map[string]any{"Statement": []any{map[string]any{"Effect": "Allow", "Action": "kafka-cluster:ReadData",
				"Resource": "arn:aws:kafka:us-east-1:111122223333:topic/mymsk/abc-5/orders"}}}
		case "arn:aws:iam::111122223333:user/AppUser":
			name = "AppUser"
			policyDoc = map[string]any{"Statement": []any{map[string]any{"Effect": "Allow", "Action": "kafka-cluster:WriteData",
				"Resource": "arn:aws:kafka:us-east-1:111122223333:topic/mymsk/abc-5/payments"}}}
		default:
			t.Fatalf("unexpected principal ARN: %s", arn)
		}
		return &iamservice.PrincipalPolicies{
			PrincipalArn: arn, PrincipalName: name,
			InlinePolicies: []iamservice.InlinePolicy{{PolicyName: "p", PolicyDocument: policyDoc}},
		}, nil
	}

	got, err := ReadIAMACLs(context.Background(), fake, []string{
		"arn:aws:iam::111122223333:role/AppRole",
		"arn:aws:iam::111122223333:user/AppUser",
	}, testCluster)

	require.NoError(t, err)
	require.ElementsMatch(t, []types.Acls{
		{ResourceType: "Topic", ResourceName: "orders", ResourcePatternType: "Literal", Principal: "User:AppRole", Host: "*", Operation: "Read", PermissionType: "Allow"},
		{ResourceType: "Topic", ResourceName: "payments", ResourcePatternType: "Literal", Principal: "User:AppUser", Host: "*", Operation: "Write", PermissionType: "Allow"},
	}, got)
}

// TestReadIAMACLs_FetchError asserts a fetch failure for any one principal
// fails the whole read (wrapped) rather than being silently skipped — the
// operator named this principal explicitly, so a failure to read its
// policies must surface, not vanish into a partial result.
func TestReadIAMACLs_FetchError(t *testing.T) {
	fake := func(ctx context.Context, arn string) (*iamservice.PrincipalPolicies, error) {
		return nil, errors.New("access denied")
	}

	_, err := ReadIAMACLs(context.Background(), fake, []string{"arn:aws:iam::1:role/R"}, "arn:aws:kafka:us-east-1:1:cluster/m/abc-5")

	require.ErrorContains(t, err, "access denied")
}

// TestNormalizePrincipalARNs mirrors create-asset's evaluatePrincipal:
// STS assumed-role ARNs normalize to their iam role-arn form, and an
// already-normalized duplicate is deduped away.
func TestNormalizePrincipalARNs(t *testing.T) {
	got := NormalizePrincipalARNs([]string{
		"arn:aws:sts::111122223333:assumed-role/AppRole/i-0abc",
		"arn:aws:iam::111122223333:role/AppRole",
	})
	require.Equal(t, []string{"arn:aws:iam::111122223333:role/AppRole"}, got)
}

// TestNormalizePrincipalARNs_UserArnUnchanged covers an IAM user ARN (no
// assumed-role/session suffix to trim), which must pass through unchanged.
func TestNormalizePrincipalARNs_UserArnUnchanged(t *testing.T) {
	got := NormalizePrincipalARNs([]string{"arn:aws:iam::111122223333:user/AppUser"})
	require.Equal(t, []string{"arn:aws:iam::111122223333:user/AppUser"}, got)
}

// TestNormalizePrincipalARNs_DedupePreservesOrder covers deduping multiple
// STS sessions of the same role (e.g. repeated assume-role calls with
// different session names) down to one entry, preserving first-seen order
// against a distinct second principal.
func TestNormalizePrincipalARNs_DedupePreservesOrder(t *testing.T) {
	got := NormalizePrincipalARNs([]string{
		"arn:aws:sts::111122223333:assumed-role/AppRole/session-1",
		"arn:aws:sts::111122223333:assumed-role/OtherRole/session-2",
		"arn:aws:sts::111122223333:assumed-role/AppRole/session-3",
	})
	require.Equal(t, []string{
		"arn:aws:iam::111122223333:role/AppRole",
		"arn:aws:iam::111122223333:role/OtherRole",
	}, got)
}
