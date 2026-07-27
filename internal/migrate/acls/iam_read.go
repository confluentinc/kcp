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

// ReadIAMACLs reads the IAM-derived ACL equivalents for a fixed set of
// explicitly-named principals against one source cluster: it normalizes
// principalArns, then for each principal fetches its policies and runs
// translateStatements (iam_translate.go) over every inline and attached
// policy document, concatenating all results.
//
// principalArns is not deduplicated by any implicit discovery step upstream
// (unlike the native ACL reader) — an operator names these explicitly in the
// migration manifest, so a fetch failure for any one of them fails the whole
// read rather than being silently skipped. Cross-principal duplicate ACLs
// (or duplicates against the natively-read ACLs) are left for the
// reconciler's map[types.Acls]struct{} dedupe stage; ReadIAMACLs itself
// performs no deduplication.
func ReadIAMACLs(ctx context.Context, fetch PrincipalPolicyFetcher, principalArns []string, clusterArn string) ([]types.Acls, error) {
	var out []types.Acls

	for _, principalArn := range NormalizePrincipalARNs(principalArns) {
		policies, err := fetch(ctx, principalArn)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch IAM policies for principal %s: %w", principalArn, err)
		}

		for _, policy := range policies.InlinePolicies {
			out = append(out, translateStatements(principalArn, clusterArn, policy.PolicyDocument)...)
		}
		for _, policy := range policies.AttachedPolicies {
			out = append(out, translateStatements(principalArn, clusterArn, policy.PolicyDocument)...)
		}
	}

	return out, nil
}
