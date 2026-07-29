package acls

import (
	"context"
	"fmt"
	"slices"
	"strings"

	iamservice "github.com/confluentinc/kcp/internal/services/iam"
	"github.com/confluentinc/kcp/internal/types"
)

// PrincipalPolicyFetcher fetches one IAM principal's inline and attached
// policies. ReadIAMACLs takes this as a seam rather than an *iam.Client
// directly: constructing an AWS IAM client is the command's job (the next
// task); this package must stay testable without ever talking to AWS.
type PrincipalPolicyFetcher func(ctx context.Context, principalArn string) (*iamservice.PrincipalPolicies, error)

// NormalizePrincipalARNs normalizes a list of discovered IAM principal ARNs
// into deduplicated role/user ARN form, mirroring create-asset's
// evaluatePrincipal (cmd/create_asset/migrate_acls/iam_acls/cmd_migrate_iam_acls.go):
//
//   - An STS assumed-role ARN's "arn:aws:sts::" prefix is rewritten to
//     "arn:aws:iam::" and its ":assumed-role/" segment to ":role/", so
//     "arn:aws:sts::111122223333:assumed-role/AppRole/i-0abc" becomes
//     "arn:aws:iam::111122223333:role/AppRole/i-0abc".
//   - The result is trimmed to its first two "/"-delimited segments
//     ("arn:aws:iam::111122223333:role" + "AppRole"), dropping any trailing
//     session-name/instance-id segment a temporary-credentials ARN carries.
//   - Already-normalized role/user ARNs pass through unchanged (they have
//     no third segment to trim).
//   - Duplicates (e.g. multiple STS sessions for the same role) are
//     dropped, preserving first-seen order.
func NormalizePrincipalARNs(in []string) []string {
	var out []string

	for _, principal := range in {
		arn := strings.Replace(principal, "arn:aws:sts::", "arn:aws:iam::", 1)
		arn = strings.Replace(arn, ":assumed-role/", ":role/", 1)

		// KNOWN LIMITATION (Finding 3 / task-7, inherited from create-asset's
		// evaluatePrincipal — not changed here): this trims to the first two
		// "/"-segments unconditionally, so a PATH-BEARING role ARN like
		// "arn:aws:iam::acct:role/team/AppRole" loses "AppRole" and normalizes
		// to "arn:aws:iam::acct:role/team". Roles-with-paths are uncommon in
		// practice; fixing this would need to distinguish "session-name
		// segment on an STS-derived ARN" (drop it) from "path segment on an
		// already-role/user ARN" (keep it), which the ARN string alone doesn't
		// disambiguate.
		parts := strings.Split(arn, "/")
		if len(parts) > 2 {
			arn = strings.Join(parts[:2], "/")
		}

		if slices.Contains(out, arn) {
			continue
		}
		out = append(out, arn)
	}

	return out
}

// GatherExplicit fetches the IAM policies for a fixed set of
// explicitly-named principals: it normalizes principalArns, then fetches
// each principal's policies via fetch.
//
// principalArns is not deduplicated by any implicit discovery step upstream
// (unlike the native ACL reader) — an operator names these explicitly in the
// migration manifest, so a fetch failure for any one of them fails the whole
// gather rather than being silently skipped.
func GatherExplicit(ctx context.Context, fetch PrincipalPolicyFetcher, principalArns []string) ([]iamservice.PrincipalPolicies, error) {
	var out []iamservice.PrincipalPolicies

	for _, principalArn := range NormalizePrincipalARNs(principalArns) {
		policies, err := fetch(ctx, principalArn)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch IAM policies for principal %s: %w", principalArn, err)
		}

		out = append(out, *policies)
	}

	return out, nil
}

// TranslatePrincipalPolicies runs translateStatements (iam_translate.go)
// over every inline and attached policy document of each gathered
// PrincipalPolicies, concatenating all results.
//
// Cross-principal duplicate ACLs (or duplicates against the
// natively-read ACLs) are left for the reconciler's
// map[types.Acls]struct{} dedupe stage; TranslatePrincipalPolicies itself
// performs no deduplication.
func TranslatePrincipalPolicies(clusterArn string, pps []iamservice.PrincipalPolicies) []types.Acls {
	var out []types.Acls

	for _, pp := range pps {
		for _, policy := range pp.InlinePolicies {
			out = append(out, translateStatements(pp.PrincipalArn, clusterArn, policy.PolicyDocument)...)
		}
		for _, policy := range pp.AttachedPolicies {
			out = append(out, translateStatements(pp.PrincipalArn, clusterArn, policy.PolicyDocument)...)
		}
	}

	return out
}

// RolePolicyEnumerator yields ALL of an AWS account's IAM roles as
// PrincipalPolicies (PrincipalArn set to the role's own ARN, with its
// inline and attached policy documents already fetched) — the enumeration
// counterpart to PrincipalPolicyFetcher's fixed-list lookup. Constructing an
// AWS IAM client and paging ListRoles/policies is the command's job (a later
// task); this package must stay testable without ever talking to AWS.
type RolePolicyEnumerator func(ctx context.Context) ([]iamservice.PrincipalPolicies, error)

// GatherEnumerated fetches every account role's policies via enumerate, then
// drops roles isExcludedIAMRole identifies as non-workload (AWS
// service-linked roles, SSO-provisioned roles) — a customer's account
// always carries these, and neither can ever hold a customer-authored Kafka
// grant worth migrating.
//
// Cluster-scoping is deliberately NOT applied here: a survivor may still
// carry only out-of-cluster grants, which translateStatements/
// clusterArnMatches (via TranslatePrincipalPolicies, reused verbatim from
// GatherExplicit's pipeline) will translate to nothing. GatherEnumerated's
// only job is filtering out roles that were never workload roles to begin
// with.
func GatherEnumerated(ctx context.Context, enumerate RolePolicyEnumerator) ([]iamservice.PrincipalPolicies, error) {
	all, err := enumerate(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to enumerate IAM account roles: %w", err)
	}

	var out []iamservice.PrincipalPolicies
	for _, pp := range all {
		if isExcludedIAMRole(pp.PrincipalArn) {
			continue
		}
		out = append(out, pp)
	}

	return out, nil
}

// isExcludedIAMRole reports whether an IAM role ARN denotes a non-workload
// role that enumeration must never surface as a migration candidate,
// regardless of what its policies grant:
//
//   - an AWS service-linked role, whose path carries the
//     "aws-service-role/" segment AWS itself reserves for these (e.g.
//     "arn:...:role/aws-service-role/kafka.amazonaws.com/AWSServiceRoleForKafka");
//   - an AWS IAM Identity Center (SSO) provisioned role, identified by
//     EITHER its reserved "aws-reserved/sso.amazonaws.com/" path segment, OR
//     its role name (the final "/"-delimited ARN segment) starting with the
//     "AWSReservedSSO_" prefix SSO always assigns — some accounts provision
//     these outside the reserved path, so the name-prefix check is
//     independent of, not gated by, the path check.
//
// A normal workload role — including one that merely has an
// MSK-Connect-suggestive name at an ordinary path, or a role name that
// happens to CONTAIN "aws-reserved"/"aws-service-role" as a substring
// without actually sitting under that reserved path segment — is never
// excluded.
func isExcludedIAMRole(principalArn string) bool {
	if strings.Contains(principalArn, ":role/aws-service-role/") {
		return true
	}
	if strings.Contains(principalArn, ":role/aws-reserved/sso.amazonaws.com/") {
		return true
	}

	parts := strings.Split(principalArn, "/")
	roleName := parts[len(parts)-1]
	return strings.HasPrefix(roleName, "AWSReservedSSO_")
}

// ReadIAMACLs reads the IAM-derived ACL equivalents for a fixed set of
// explicitly-named principals against one source cluster: GatherExplicit
// fetches each principal's policies, then TranslatePrincipalPolicies
// translates every inline and attached policy document into its ACL
// equivalents, concatenating all results.
func ReadIAMACLs(ctx context.Context, fetch PrincipalPolicyFetcher, principalArns []string, clusterArn string) ([]types.Acls, error) {
	pps, err := GatherExplicit(ctx, fetch, principalArns)
	if err != nil {
		return nil, err
	}

	return TranslatePrincipalPolicies(clusterArn, pps), nil
}
