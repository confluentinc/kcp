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

// TestGatherExplicit_NormalizesAndFetches covers the gather stage in
// isolation: principal ARNs are normalized before fetching, and the fetched
// PrincipalPolicies are returned verbatim (no translation happens here).
func TestGatherExplicit_NormalizesAndFetches(t *testing.T) {
	var gotArns []string
	fake := func(ctx context.Context, arn string) (*iamservice.PrincipalPolicies, error) {
		gotArns = append(gotArns, arn)
		return &iamservice.PrincipalPolicies{
			PrincipalArn: arn, PrincipalName: "AppRole", PrincipalType: "role",
			InlinePolicies: []iamservice.InlinePolicy{{PolicyName: "p", PolicyDocument: map[string]any{
				"Statement": []any{map[string]any{"Effect": "Allow", "Action": "kafka-cluster:ReadData",
					"Resource": "arn:aws:kafka:us-east-1:111122223333:topic/mymsk/abc-5/orders"}},
			}}},
		}, nil
	}

	got, err := GatherExplicit(context.Background(), fake, []string{
		"arn:aws:sts::111122223333:assumed-role/AppRole/i-0abc",
	})

	require.NoError(t, err)
	require.Equal(t, []string{"arn:aws:iam::111122223333:role/AppRole"}, gotArns)
	require.Equal(t, []iamservice.PrincipalPolicies{
		{
			PrincipalArn: "arn:aws:iam::111122223333:role/AppRole", PrincipalName: "AppRole", PrincipalType: "role",
			InlinePolicies: []iamservice.InlinePolicy{{PolicyName: "p", PolicyDocument: map[string]any{
				"Statement": []any{map[string]any{"Effect": "Allow", "Action": "kafka-cluster:ReadData",
					"Resource": "arn:aws:kafka:us-east-1:111122223333:topic/mymsk/abc-5/orders"}},
			}}},
		},
	}, got)
}

// TestGatherExplicit_FetchError asserts a fetch failure for any one principal
// fails the whole gather (wrapped) rather than being silently skipped.
func TestGatherExplicit_FetchError(t *testing.T) {
	fake := func(ctx context.Context, arn string) (*iamservice.PrincipalPolicies, error) {
		return nil, errors.New("access denied")
	}

	_, err := GatherExplicit(context.Background(), fake, []string{"arn:aws:iam::1:role/R"})

	require.ErrorContains(t, err, "access denied")
}

// TestTranslatePrincipalPolicies_InlineAndAttached covers the translate
// stage in isolation: given already-gathered PrincipalPolicies (one
// principal, one inline + one attached policy), it must run
// translateStatements over every document and concatenate the results —
// the same tuples TestReadIAMACLs_InlineAndAttachedPolicies asserts via the
// full pipeline.
func TestTranslatePrincipalPolicies_InlineAndAttached(t *testing.T) {
	pps := []iamservice.PrincipalPolicies{
		{
			PrincipalArn: "arn:aws:iam::111122223333:role/AppRole", PrincipalName: "AppRole",
			InlinePolicies: []iamservice.InlinePolicy{{PolicyName: "inline", PolicyDocument: map[string]any{
				"Statement": []any{map[string]any{"Effect": "Allow", "Action": "kafka-cluster:ReadData",
					"Resource": "arn:aws:kafka:us-east-1:111122223333:topic/mymsk/abc-5/orders"}},
			}}},
			AttachedPolicies: []iamservice.AttachedPolicy{{PolicyName: "attached", PolicyArn: "arn:aws:iam::111122223333:policy/attached", PolicyDocument: map[string]any{
				"Statement": []any{map[string]any{"Effect": "Allow", "Action": "kafka-cluster:WriteData",
					"Resource": "arn:aws:kafka:us-east-1:111122223333:topic/mymsk/abc-5/payments"}},
			}}},
		},
	}

	got := TranslatePrincipalPolicies(testCluster, pps)

	require.ElementsMatch(t, []types.Acls{
		{ResourceType: "Topic", ResourceName: "orders", ResourcePatternType: "Literal", Principal: "User:AppRole", Host: "*", Operation: "Read", PermissionType: "Allow"},
		{ResourceType: "Topic", ResourceName: "payments", ResourcePatternType: "Literal", Principal: "User:AppRole", Host: "*", Operation: "Write", PermissionType: "Allow"},
	}, got)
}

// TestTranslatePrincipalPolicies_MultiplePrincipals covers two distinct
// already-gathered principals: each is translated independently and results
// are concatenated (no dedupe).
func TestTranslatePrincipalPolicies_MultiplePrincipals(t *testing.T) {
	pps := []iamservice.PrincipalPolicies{
		{
			PrincipalArn: "arn:aws:iam::111122223333:role/AppRole", PrincipalName: "AppRole",
			InlinePolicies: []iamservice.InlinePolicy{{PolicyName: "p", PolicyDocument: map[string]any{
				"Statement": []any{map[string]any{"Effect": "Allow", "Action": "kafka-cluster:ReadData",
					"Resource": "arn:aws:kafka:us-east-1:111122223333:topic/mymsk/abc-5/orders"}},
			}}},
		},
		{
			PrincipalArn: "arn:aws:iam::111122223333:user/AppUser", PrincipalName: "AppUser",
			InlinePolicies: []iamservice.InlinePolicy{{PolicyName: "p", PolicyDocument: map[string]any{
				"Statement": []any{map[string]any{"Effect": "Allow", "Action": "kafka-cluster:WriteData",
					"Resource": "arn:aws:kafka:us-east-1:111122223333:topic/mymsk/abc-5/payments"}},
			}}},
		},
	}

	got := TranslatePrincipalPolicies(testCluster, pps)

	require.ElementsMatch(t, []types.Acls{
		{ResourceType: "Topic", ResourceName: "orders", ResourcePatternType: "Literal", Principal: "User:AppRole", Host: "*", Operation: "Read", PermissionType: "Allow"},
		{ResourceType: "Topic", ResourceName: "payments", ResourcePatternType: "Literal", Principal: "User:AppUser", Host: "*", Operation: "Write", PermissionType: "Allow"},
	}, got)
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

// --- GatherEnumerated ---

// TestGatherEnumerated_NormalRoleSurvivesAndTranslates covers a normal
// workload role with an in-cluster kafka-cluster:ReadData topic grant: it
// must survive GatherEnumerated's exclusion filter, and (fed into
// TranslatePrincipalPolicies, the same translate Task 1 built) produce the
// expected canonical ACL tuple.
func TestGatherEnumerated_NormalRoleSurvivesAndTranslates(t *testing.T) {
	roleArn := "arn:aws:iam::111122223333:role/AppRole"
	enumerate := func(ctx context.Context) ([]iamservice.PrincipalPolicies, error) {
		return []iamservice.PrincipalPolicies{
			{
				PrincipalArn: roleArn, PrincipalName: "AppRole", PrincipalType: "role",
				InlinePolicies: []iamservice.InlinePolicy{{PolicyName: "p", PolicyDocument: map[string]any{
					"Statement": []any{map[string]any{"Effect": "Allow", "Action": "kafka-cluster:ReadData",
						"Resource": "arn:aws:kafka:us-east-1:111122223333:topic/mymsk/abc-5/orders"}},
				}}},
			},
		}, nil
	}

	got, err := GatherEnumerated(context.Background(), enumerate)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, roleArn, got[0].PrincipalArn)

	translated := TranslatePrincipalPolicies(testCluster, got)
	require.Equal(t, []types.Acls{
		{ResourceType: "Topic", ResourceName: "orders", ResourcePatternType: "Literal", Principal: "User:AppRole", Host: "*", Operation: "Read", PermissionType: "Allow"},
	}, translated)
}

// TestGatherEnumerated_ExcludesServiceLinkedRole covers a service-linked
// role (path segment "aws-service-role/"): it must be dropped by
// GatherEnumerated before it ever reaches translation, regardless of what
// grants its policies carry.
func TestGatherEnumerated_ExcludesServiceLinkedRole(t *testing.T) {
	enumerate := func(ctx context.Context) ([]iamservice.PrincipalPolicies, error) {
		return []iamservice.PrincipalPolicies{
			{
				PrincipalArn:  "arn:aws:iam::111122223333:role/aws-service-role/kafka.amazonaws.com/AWSServiceRoleForKafka",
				PrincipalName: "AWSServiceRoleForKafka", PrincipalType: "role",
				InlinePolicies: []iamservice.InlinePolicy{{PolicyName: "p", PolicyDocument: map[string]any{
					"Statement": []any{map[string]any{"Effect": "Allow", "Action": "kafka-cluster:ReadData",
						"Resource": "arn:aws:kafka:us-east-1:111122223333:topic/mymsk/abc-5/orders"}},
				}}},
			},
		}, nil
	}

	got, err := GatherEnumerated(context.Background(), enumerate)
	require.NoError(t, err)
	require.Empty(t, got)
}

// TestGatherEnumerated_ExcludesSSORoles covers both SSO exclusion signals:
// the reserved SSO path segment, and a bare-name AWSReservedSSO_* role that
// doesn't happen to sit under that path (isExcludedIAMRole must catch both
// independently).
func TestGatherEnumerated_ExcludesSSORoles(t *testing.T) {
	enumerate := func(ctx context.Context) ([]iamservice.PrincipalPolicies, error) {
		return []iamservice.PrincipalPolicies{
			{
				PrincipalArn:  "arn:aws:iam::111122223333:role/aws-reserved/sso.amazonaws.com/us-east-1/AWSReservedSSO_Admin_abc",
				PrincipalName: "AWSReservedSSO_Admin_abc", PrincipalType: "role",
			},
			{
				PrincipalArn:  "arn:aws:iam::111122223333:role/AWSReservedSSO_Developer_def",
				PrincipalName: "AWSReservedSSO_Developer_def", PrincipalType: "role",
			},
		}, nil
	}

	got, err := GatherEnumerated(context.Background(), enumerate)
	require.NoError(t, err)
	require.Empty(t, got)
}

// TestGatherEnumerated_DifferentClusterSurvivesButTranslatesToNothing covers
// a role whose only grant targets a DIFFERENT cluster: GatherEnumerated
// itself does no cluster scoping (that's translateStatements/
// clusterArnMatches's job downstream), so the role must survive here, but
// translating it against testCluster must yield no ACLs.
func TestGatherEnumerated_DifferentClusterSurvivesButTranslatesToNothing(t *testing.T) {
	roleArn := "arn:aws:iam::111122223333:role/OtherClusterRole"
	enumerate := func(ctx context.Context) ([]iamservice.PrincipalPolicies, error) {
		return []iamservice.PrincipalPolicies{
			{
				PrincipalArn: roleArn, PrincipalName: "OtherClusterRole", PrincipalType: "role",
				InlinePolicies: []iamservice.InlinePolicy{{PolicyName: "p", PolicyDocument: map[string]any{
					"Statement": []any{map[string]any{"Effect": "Allow", "Action": "kafka-cluster:ReadData",
						"Resource": "arn:aws:kafka:us-east-1:111122223333:topic/othermsk/xyz-9/orders"}},
				}}},
			},
		}, nil
	}

	got, err := GatherEnumerated(context.Background(), enumerate)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, roleArn, got[0].PrincipalArn)

	translated := TranslatePrincipalPolicies(testCluster, got)
	require.Empty(t, translated)
}

// TestGatherEnumerated_MSKConnectStyleRoleNotExcluded covers a normal role
// whose name happens to look MSK-Connect-related, sitting at an ordinary
// (non-reserved) path: it must NOT be excluded, since it is a genuine
// workload role, not a service-linked or SSO one.
func TestGatherEnumerated_MSKConnectStyleRoleNotExcluded(t *testing.T) {
	roleArn := "arn:aws:iam::111122223333:role/MSKConnectExecutionRole"
	enumerate := func(ctx context.Context) ([]iamservice.PrincipalPolicies, error) {
		return []iamservice.PrincipalPolicies{
			{
				PrincipalArn: roleArn, PrincipalName: "MSKConnectExecutionRole", PrincipalType: "role",
				InlinePolicies: []iamservice.InlinePolicy{{PolicyName: "p", PolicyDocument: map[string]any{
					"Statement": []any{map[string]any{"Effect": "Allow", "Action": "kafka-cluster:WriteData",
						"Resource": "arn:aws:kafka:us-east-1:111122223333:topic/mymsk/abc-5/connect-configs"}},
				}}},
			},
		}, nil
	}

	got, err := GatherEnumerated(context.Background(), enumerate)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, roleArn, got[0].PrincipalArn)
}

// TestGatherEnumerated_EnumerateError asserts an enumerate failure fails the
// whole gather (wrapped), mirroring GatherExplicit's fetch-error contract.
func TestGatherEnumerated_EnumerateError(t *testing.T) {
	enumerate := func(ctx context.Context) ([]iamservice.PrincipalPolicies, error) {
		return nil, errors.New("access denied")
	}

	_, err := GatherEnumerated(context.Background(), enumerate)
	require.ErrorContains(t, err, "access denied")
}

// --- isExcludedIAMRole ---

// TestIsExcludedIAMRole covers each exclusion pattern individually, plus
// normal roles that must NOT be excluded — including a guard against
// substring over-matching (a role literally named "my-aws-reserved-thing"
// that isn't actually under the reserved path must survive).
func TestIsExcludedIAMRole(t *testing.T) {
	tests := []struct {
		name     string
		arn      string
		excluded bool
	}{
		{
			name:     "service-linked role",
			arn:      "arn:aws:iam::111122223333:role/aws-service-role/kafka.amazonaws.com/AWSServiceRoleForKafka",
			excluded: true,
		},
		{
			name:     "SSO role by reserved path",
			arn:      "arn:aws:iam::111122223333:role/aws-reserved/sso.amazonaws.com/us-east-1/AWSReservedSSO_Admin_abc",
			excluded: true,
		},
		{
			name:     "SSO role by bare name, non-reserved path",
			arn:      "arn:aws:iam::111122223333:role/AWSReservedSSO_Developer_def",
			excluded: true,
		},
		{
			name:     "normal workload role",
			arn:      "arn:aws:iam::111122223333:role/AppRole",
			excluded: false,
		},
		{
			name:     "MSK-Connect-looking role, ordinary path",
			arn:      "arn:aws:iam::111122223333:role/MSKConnectExecutionRole",
			excluded: false,
		},
		{
			name:     "role name merely contains 'aws-reserved' as a substring, not the reserved path",
			arn:      "arn:aws:iam::111122223333:role/my-aws-reserved-thing",
			excluded: false,
		},
		{
			name:     "role name merely contains 'aws-service-role' as a substring, not the reserved path",
			arn:      "arn:aws:iam::111122223333:role/my-aws-service-role-thing",
			excluded: false,
		},
		{
			name:     "role name contains AWSReservedSSO_ only as a mid-string substring, not a prefix of the final segment",
			arn:      "arn:aws:iam::111122223333:role/NotAWSReservedSSO_Prefixed",
			excluded: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.excluded, isExcludedIAMRole(tt.arn))
		})
	}
}
