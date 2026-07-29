package acls

import (
	"context"
	"errors"
	"sort"
	"testing"

	iamservice "github.com/confluentinc/kcp/internal/services/iam"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/stretchr/testify/require"
)

const effCluster = "arn:aws:kafka:us-east-1:111122223333:cluster/mymsk/abc-5"

func inlineDoc(statements ...map[string]any) map[string]any {
	stmts := make([]any, len(statements))
	for i, s := range statements {
		stmts[i] = s
	}
	return map[string]any{"Statement": stmts}
}

func stmt(effect string, actions, resources any) map[string]any {
	return map[string]any{"Effect": effect, "Action": actions, "Resource": resources}
}

// capturingChecker builds an EffectiveAccessChecker backed by a fixed
// allow-map, additionally recording every call's arguments so tests can
// assert what FilterEffective actually asked to have verified (batching,
// exact pairs).
type capturingChecker struct {
	allowed map[string]bool // "action|resource" -> allowed
	calls   []checkerCall
	err     error
}

type checkerCall struct {
	principalArn string
	actions      []string
	resources    []string
}

func (c *capturingChecker) check(_ context.Context, principalArn string, actions, resources []string) (map[string]bool, error) {
	// Copy + sort so callers can assert deterministically regardless of the
	// order FilterEffective happened to build them in.
	a := append([]string(nil), actions...)
	r := append([]string(nil), resources...)
	sort.Strings(a)
	sort.Strings(r)
	c.calls = append(c.calls, checkerCall{principalArn: principalArn, actions: a, resources: r})

	if c.err != nil {
		return nil, c.err
	}
	return c.allowed, nil
}

const (
	topicA = "arn:aws:kafka:us-east-1:111122223333:topic/mymsk/abc-5/topic-a"
	topicB = "arn:aws:kafka:us-east-1:111122223333:topic/mymsk/abc-5/topic-b"
)

var principalA = "arn:aws:iam::111122223333:role/AppRole"

// TestFilterEffective_AllowedAndDeniedPair is brief case (a): Read on
// topic-a is allowed, Write on topic-b is denied. Only the Read/topic-a
// grant should survive filtering, and TranslatePrincipalPolicies must then
// derive only the corresponding ACL — proving the filtered doc is in a
// shape translateStatements can still consume correctly.
func TestFilterEffective_AllowedAndDeniedPair(t *testing.T) {
	pps := []iamservice.PrincipalPolicies{{
		PrincipalArn: principalA,
		InlinePolicies: []iamservice.InlinePolicy{{
			PolicyName: "p",
			PolicyDocument: inlineDoc(
				stmt("Allow", "kafka-cluster:ReadData", topicA),
				stmt("Allow", "kafka-cluster:WriteData", topicB),
			),
		}},
	}}
	checker := &capturingChecker{allowed: map[string]bool{
		"kafka-cluster:ReadData|" + topicA:  true,
		"kafka-cluster:WriteData|" + topicB: false,
	}}

	got, err := FilterEffective(context.Background(), checker.check, pps)
	require.NoError(t, err)
	require.Len(t, got, 1)

	acls := TranslatePrincipalPolicies(effCluster, got)
	require.Equal(t, []types.Acls{
		{ResourceType: "Topic", ResourceName: "topic-a", ResourcePatternType: "Literal", Principal: "User:AppRole", Host: "*", Operation: "Read", PermissionType: "Allow"},
	}, acls)
}

// TestFilterEffective_AllDenied_PrincipalDropped is brief case (b): every
// pair a principal has is denied, so it must not appear in the output at
// all (not present-with-zero-statements — absent).
func TestFilterEffective_AllDenied_PrincipalDropped(t *testing.T) {
	pps := []iamservice.PrincipalPolicies{{
		PrincipalArn: principalA,
		InlinePolicies: []iamservice.InlinePolicy{{
			PolicyName:     "p",
			PolicyDocument: inlineDoc(stmt("Allow", "kafka-cluster:ReadData", topicA)),
		}},
	}}
	checker := &capturingChecker{allowed: map[string]bool{
		"kafka-cluster:ReadData|" + topicA: false,
	}}

	got, err := FilterEffective(context.Background(), checker.check, pps)
	require.NoError(t, err)
	require.Empty(t, got)

	acls := TranslatePrincipalPolicies(effCluster, got)
	require.Empty(t, acls)
}

// TestFilterEffective_MissingKeyDeniedByDefault documents that a pair the
// checker didn't populate an entry for is treated as not-allowed, not as
// allowed — deny-by-default is the safe failure mode.
func TestFilterEffective_MissingKeyDeniedByDefault(t *testing.T) {
	pps := []iamservice.PrincipalPolicies{{
		PrincipalArn: principalA,
		InlinePolicies: []iamservice.InlinePolicy{{
			PolicyName:     "p",
			PolicyDocument: inlineDoc(stmt("Allow", "kafka-cluster:ReadData", topicA)),
		}},
	}}
	checker := &capturingChecker{allowed: map[string]bool{}} // no entries at all

	got, err := FilterEffective(context.Background(), checker.check, pps)
	require.NoError(t, err)
	require.Empty(t, got)
}

// TestFilterEffective_CallsCheckerWithActualPairs is brief case (c): the
// checker must be invoked with the principal's real distinct actions and
// resources, batched into ONE call per principal (not one call per pair).
func TestFilterEffective_CallsCheckerWithActualPairs(t *testing.T) {
	pps := []iamservice.PrincipalPolicies{{
		PrincipalArn: principalA,
		InlinePolicies: []iamservice.InlinePolicy{{
			PolicyName: "p",
			PolicyDocument: inlineDoc(
				stmt("Allow", "kafka-cluster:ReadData", topicA),
				stmt("Allow", "kafka-cluster:WriteData", topicB),
			),
		}},
	}}
	checker := &capturingChecker{allowed: map[string]bool{
		"kafka-cluster:ReadData|" + topicA:  true,
		"kafka-cluster:WriteData|" + topicB: true,
	}}

	_, err := FilterEffective(context.Background(), checker.check, pps)
	require.NoError(t, err)

	require.Len(t, checker.calls, 1, "expected exactly one batched check call for the principal")
	call := checker.calls[0]
	require.Equal(t, principalA, call.principalArn)
	require.Equal(t, []string{"kafka-cluster:ReadData", "kafka-cluster:WriteData"}, call.actions)
	require.Equal(t, []string{topicA, topicB}, call.resources)
}

// TestFilterEffective_WildcardExpansion_AgreesWithTranslate is brief case
// (d), the primary cross-cutting risk check: a "kafka-cluster:*" statement
// on a topic ARN, with the fake denying exactly one concrete op
// (DeleteTopic). translateStatements' own kafka-cluster:* expansion for a
// topic ARN produces {Alter, AlterConfigs, Create, Delete, Describe,
// DescribeConfigs, Read, Write} (see TestTranslateStatements' "kafka-
// cluster:* on a topic ARN" case) — so denying kafka-cluster:DeleteTopic
// must remove exactly the "Delete" operation from the downstream ACLs and
// leave every other operation intact. If FilterEffective simulated the
// literal "kafka-cluster:*" string instead of expanding it the same way
// translateStatements does, this would either simulate nothing meaningful
// or filter the wrong thing — this test fails in that scenario.
func TestFilterEffective_WildcardExpansion_AgreesWithTranslate(t *testing.T) {
	pps := []iamservice.PrincipalPolicies{{
		PrincipalArn: principalA,
		InlinePolicies: []iamservice.InlinePolicy{{
			PolicyName:     "p",
			PolicyDocument: inlineDoc(stmt("Allow", "kafka-cluster:*", topicA)),
		}},
	}}

	allowed := map[string]bool{}
	for action := range types.AclMap {
		allowed[action+"|"+topicA] = true
	}
	allowed["kafka-cluster:DeleteTopic|"+topicA] = false // the one denied op
	checker := &capturingChecker{allowed: allowed}

	got, err := FilterEffective(context.Background(), checker.check, pps)
	require.NoError(t, err)
	require.Len(t, got, 1)

	acls := TranslatePrincipalPolicies(effCluster, got)

	require.ElementsMatch(t, []types.Acls{
		{ResourceType: "Topic", ResourceName: "topic-a", ResourcePatternType: "Literal", Principal: "User:AppRole", Host: "*", Operation: "Alter", PermissionType: "Allow"},
		{ResourceType: "Topic", ResourceName: "topic-a", ResourcePatternType: "Literal", Principal: "User:AppRole", Host: "*", Operation: "AlterConfigs", PermissionType: "Allow"},
		{ResourceType: "Topic", ResourceName: "topic-a", ResourcePatternType: "Literal", Principal: "User:AppRole", Host: "*", Operation: "Create", PermissionType: "Allow"},
		{ResourceType: "Topic", ResourceName: "topic-a", ResourcePatternType: "Literal", Principal: "User:AppRole", Host: "*", Operation: "Describe", PermissionType: "Allow"},
		{ResourceType: "Topic", ResourceName: "topic-a", ResourcePatternType: "Literal", Principal: "User:AppRole", Host: "*", Operation: "DescribeConfigs", PermissionType: "Allow"},
		{ResourceType: "Topic", ResourceName: "topic-a", ResourcePatternType: "Literal", Principal: "User:AppRole", Host: "*", Operation: "Read", PermissionType: "Allow"},
		{ResourceType: "Topic", ResourceName: "topic-a", ResourcePatternType: "Literal", Principal: "User:AppRole", Host: "*", Operation: "Write", PermissionType: "Allow"},
	}, acls)

	for _, acl := range acls {
		require.NotEqual(t, "Delete", acl.Operation, "the denied op must not survive translation")
	}
}

// TestFilterEffective_AllPass_UnchangedTranslation is brief case (5): a
// principal whose grants are ALL allowed must translate to exactly what it
// would have without any filtering at all.
func TestFilterEffective_AllPass_UnchangedTranslation(t *testing.T) {
	pps := []iamservice.PrincipalPolicies{{
		PrincipalArn: principalA,
		InlinePolicies: []iamservice.InlinePolicy{{
			PolicyName: "p",
			PolicyDocument: inlineDoc(
				stmt("Allow", "kafka-cluster:ReadData", topicA),
				stmt("Allow", "kafka-cluster:WriteData", topicB),
			),
		}},
	}}
	checker := &capturingChecker{allowed: map[string]bool{
		"kafka-cluster:ReadData|" + topicA:  true,
		"kafka-cluster:WriteData|" + topicB: true,
	}}

	got, err := FilterEffective(context.Background(), checker.check, pps)
	require.NoError(t, err)

	wantUnfiltered := TranslatePrincipalPolicies(effCluster, pps)
	gotFiltered := TranslatePrincipalPolicies(effCluster, got)
	require.ElementsMatch(t, wantUnfiltered, gotFiltered)
}

// TestFilterEffective_DenyStatementPassesThroughUnfiltered documents the
// Deny-handling design decision (see FilterEffective's doc comment): a Deny
// statement is a restriction, not a grant, so it is never simulated or
// filtered — it must survive verbatim, and its own action/resource must
// NOT be sent to the checker at all (only Allow statements' pairs are
// checked).
func TestFilterEffective_DenyStatementPassesThroughUnfiltered(t *testing.T) {
	pps := []iamservice.PrincipalPolicies{{
		PrincipalArn: principalA,
		InlinePolicies: []iamservice.InlinePolicy{{
			PolicyName: "p",
			PolicyDocument: inlineDoc(
				stmt("Allow", "kafka-cluster:ReadData", topicA),
				stmt("Deny", "kafka-cluster:WriteData", topicB),
			),
		}},
	}}
	checker := &capturingChecker{allowed: map[string]bool{
		"kafka-cluster:ReadData|" + topicA: true,
	}}

	got, err := FilterEffective(context.Background(), checker.check, pps)
	require.NoError(t, err)

	// The Deny pair must never have been asked about.
	require.Len(t, checker.calls, 1)
	require.NotContains(t, checker.calls[0].actions, "kafka-cluster:WriteData")

	acls := TranslatePrincipalPolicies(effCluster, got)
	require.ElementsMatch(t, []types.Acls{
		{ResourceType: "Topic", ResourceName: "topic-a", ResourcePatternType: "Literal", Principal: "User:AppRole", Host: "*", Operation: "Read", PermissionType: "Allow"},
		{ResourceType: "Topic", ResourceName: "topic-b", ResourcePatternType: "Literal", Principal: "User:AppRole", Host: "*", Operation: "Write", PermissionType: "Deny"},
	}, acls)
}

// TestFilterEffective_MultiplePrincipals_OneCallEach checks batching holds
// per-principal across a multi-principal input: two principals, two calls,
// each scoped to its own principal's pairs — never merged, never repeated.
func TestFilterEffective_MultiplePrincipals_OneCallEach(t *testing.T) {
	principalB := "arn:aws:iam::111122223333:role/OtherRole"
	pps := []iamservice.PrincipalPolicies{
		{
			PrincipalArn: principalA,
			InlinePolicies: []iamservice.InlinePolicy{{
				PolicyName:     "p",
				PolicyDocument: inlineDoc(stmt("Allow", "kafka-cluster:ReadData", topicA)),
			}},
		},
		{
			PrincipalArn: principalB,
			InlinePolicies: []iamservice.InlinePolicy{{
				PolicyName:     "p",
				PolicyDocument: inlineDoc(stmt("Allow", "kafka-cluster:WriteData", topicB)),
			}},
		},
	}
	checker := &capturingChecker{allowed: map[string]bool{
		"kafka-cluster:ReadData|" + topicA:  true,
		"kafka-cluster:WriteData|" + topicB: true,
	}}

	got, err := FilterEffective(context.Background(), checker.check, pps)
	require.NoError(t, err)
	require.Len(t, got, 2)
	require.Len(t, checker.calls, 2)

	byPrincipal := map[string]checkerCall{}
	for _, c := range checker.calls {
		byPrincipal[c.principalArn] = c
	}
	require.Equal(t, []string{"kafka-cluster:ReadData"}, byPrincipal[principalA].actions)
	require.Equal(t, []string{topicA}, byPrincipal[principalA].resources)
	require.Equal(t, []string{"kafka-cluster:WriteData"}, byPrincipal[principalB].actions)
	require.Equal(t, []string{topicB}, byPrincipal[principalB].resources)
}

// TestFilterEffective_AttachedPolicy exercises the AttachedPolicy path
// (not just InlinePolicy), including that PolicyArn/Description survive a
// policy that keeps at least one statement.
func TestFilterEffective_AttachedPolicy(t *testing.T) {
	pps := []iamservice.PrincipalPolicies{{
		PrincipalArn: principalA,
		AttachedPolicies: []iamservice.AttachedPolicy{{
			PolicyName:     "attached",
			PolicyArn:      "arn:aws:iam::111122223333:policy/attached",
			Description:    "desc",
			PolicyDocument: inlineDoc(stmt("Allow", "kafka-cluster:ReadData", topicA)),
		}},
	}}
	checker := &capturingChecker{allowed: map[string]bool{
		"kafka-cluster:ReadData|" + topicA: true,
	}}

	got, err := FilterEffective(context.Background(), checker.check, pps)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Len(t, got[0].AttachedPolicies, 1)
	require.Equal(t, "attached", got[0].AttachedPolicies[0].PolicyName)
	require.Equal(t, "arn:aws:iam::111122223333:policy/attached", got[0].AttachedPolicies[0].PolicyArn)
	require.Equal(t, "desc", got[0].AttachedPolicies[0].Description)
}

// TestFilterEffective_CheckerError verifies a checker error is surfaced
// (wrapped, identifying the principal) rather than silently swallowed or
// treated as deny-all.
func TestFilterEffective_CheckerError(t *testing.T) {
	pps := []iamservice.PrincipalPolicies{{
		PrincipalArn: principalA,
		InlinePolicies: []iamservice.InlinePolicy{{
			PolicyName:     "p",
			PolicyDocument: inlineDoc(stmt("Allow", "kafka-cluster:ReadData", topicA)),
		}},
	}}
	checker := &capturingChecker{err: errors.New("simulate boom")}

	got, err := FilterEffective(context.Background(), checker.check, pps)
	require.Error(t, err)
	require.Nil(t, got)
	require.Contains(t, err.Error(), principalA)
	require.ErrorContains(t, err, "simulate boom")
}

// TestFilterEffective_DenyOnlyPrincipal_Survives is the Critical-review fix
// case: a principal whose ONLY statement is a Deny on a kafka-cluster action
// — no Allow anywhere — must NOT be dropped. A source IAM Deny translates to
// a Kafka DENY ACL (translateStatements), and a Kafka DENY overrides a
// broader Allow, so losing it would make the target LESS restrictive than
// the source: a security regression. The checker must not be asked about
// this Deny's own pair (only Allow pairs are ever verified) — with no Allow
// pairs at all, no checker call should happen either.
func TestFilterEffective_DenyOnlyPrincipal_Survives(t *testing.T) {
	pps := []iamservice.PrincipalPolicies{{
		PrincipalArn: principalA,
		InlinePolicies: []iamservice.InlinePolicy{{
			PolicyName:     "p",
			PolicyDocument: inlineDoc(stmt("Deny", "kafka-cluster:ReadData", topicA)),
		}},
	}}
	checker := &capturingChecker{}

	got, err := FilterEffective(context.Background(), checker.check, pps)
	require.NoError(t, err)
	require.Len(t, got, 1, "a Deny-only principal must survive filtering")
	require.Empty(t, checker.calls, "no Allow pairs exist, so the checker must never be called")

	acls := TranslatePrincipalPolicies(effCluster, got)
	require.Equal(t, []types.Acls{
		{ResourceType: "Topic", ResourceName: "topic-a", ResourcePatternType: "Literal", Principal: "User:AppRole", Host: "*", Operation: "Read", PermissionType: "Deny"},
	}, acls)
}

// TestFilterEffective_DenyOnlyPrincipal_WildcardSurvives is the
// kafka-cluster:* variant of the Deny-only case: every expanded topic
// operation should come through as a DENY ACL, none silently dropped.
func TestFilterEffective_DenyOnlyPrincipal_WildcardSurvives(t *testing.T) {
	pps := []iamservice.PrincipalPolicies{{
		PrincipalArn: principalA,
		InlinePolicies: []iamservice.InlinePolicy{{
			PolicyName:     "p",
			PolicyDocument: inlineDoc(stmt("Deny", "kafka-cluster:*", topicA)),
		}},
	}}
	checker := &capturingChecker{}

	got, err := FilterEffective(context.Background(), checker.check, pps)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Empty(t, checker.calls)

	acls := TranslatePrincipalPolicies(effCluster, got)
	require.ElementsMatch(t, []types.Acls{
		{ResourceType: "Topic", ResourceName: "topic-a", ResourcePatternType: "Literal", Principal: "User:AppRole", Host: "*", Operation: "Alter", PermissionType: "Deny"},
		{ResourceType: "Topic", ResourceName: "topic-a", ResourcePatternType: "Literal", Principal: "User:AppRole", Host: "*", Operation: "AlterConfigs", PermissionType: "Deny"},
		{ResourceType: "Topic", ResourceName: "topic-a", ResourcePatternType: "Literal", Principal: "User:AppRole", Host: "*", Operation: "Create", PermissionType: "Deny"},
		{ResourceType: "Topic", ResourceName: "topic-a", ResourcePatternType: "Literal", Principal: "User:AppRole", Host: "*", Operation: "Delete", PermissionType: "Deny"},
		{ResourceType: "Topic", ResourceName: "topic-a", ResourcePatternType: "Literal", Principal: "User:AppRole", Host: "*", Operation: "Describe", PermissionType: "Deny"},
		{ResourceType: "Topic", ResourceName: "topic-a", ResourcePatternType: "Literal", Principal: "User:AppRole", Host: "*", Operation: "DescribeConfigs", PermissionType: "Deny"},
		{ResourceType: "Topic", ResourceName: "topic-a", ResourcePatternType: "Literal", Principal: "User:AppRole", Host: "*", Operation: "Read", PermissionType: "Deny"},
		{ResourceType: "Topic", ResourceName: "topic-a", ResourcePatternType: "Literal", Principal: "User:AppRole", Host: "*", Operation: "Write", PermissionType: "Deny"},
	}, acls)
}

// TestFilterEffective_DenyOnlyNonKafkaAction_StillDropped guards the other
// side of the fix: a principal whose only statement is a Deny on a
// NON-kafka-cluster action (e.g. s3:*) carries no migratable grant either —
// it must still be dropped, not kept alive by the mere presence of a Deny
// statement.
func TestFilterEffective_DenyOnlyNonKafkaAction_StillDropped(t *testing.T) {
	pps := []iamservice.PrincipalPolicies{{
		PrincipalArn: principalA,
		InlinePolicies: []iamservice.InlinePolicy{{
			PolicyName:     "p",
			PolicyDocument: inlineDoc(stmt("Deny", "s3:GetObject", "arn:aws:s3:::bucket/*")),
		}},
	}}
	checker := &capturingChecker{}

	got, err := FilterEffective(context.Background(), checker.check, pps)
	require.NoError(t, err)
	require.Empty(t, got)
	require.Empty(t, checker.calls)
}

// TestFilterEffective_NoKafkaClusterGrants_PrincipalDropped covers a
// principal whose only statements are non-kafka-cluster (irrelevant)
// actions: there is nothing to check and nothing translateStatements would
// ever produce, so no checker call should be made and the principal is
// dropped.
func TestFilterEffective_NoKafkaClusterGrants_PrincipalDropped(t *testing.T) {
	pps := []iamservice.PrincipalPolicies{{
		PrincipalArn: principalA,
		InlinePolicies: []iamservice.InlinePolicy{{
			PolicyName:     "p",
			PolicyDocument: inlineDoc(stmt("Allow", "s3:GetObject", "arn:aws:s3:::bucket/*")),
		}},
	}}
	checker := &capturingChecker{}

	got, err := FilterEffective(context.Background(), checker.check, pps)
	require.NoError(t, err)
	require.Empty(t, got)
	require.Empty(t, checker.calls)
}
