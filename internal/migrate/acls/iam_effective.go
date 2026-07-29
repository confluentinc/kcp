package acls

import (
	"context"
	"fmt"
	"sort"
	"strings"

	iamservice "github.com/confluentinc/kcp/internal/services/iam"
	"github.com/confluentinc/kcp/internal/types"
)

// EffectiveAccessChecker resolves, for one IAM principal, whether each of a
// batch of (kafka-cluster action, resource ARN) candidates is EFFECTIVELY
// allowed once full IAM evaluation is taken into account — SCPs, permission
// boundaries, explicit denies elsewhere in the account, and policy
// conditions — not merely what the principal's own policy documents
// nominally grant. Implementations wrap iam:SimulatePrincipalPolicy; the
// cmd layer owns the AWS client and call, this type is purely the
// injectable seam FilterEffective needs to stay testable without AWS
// (verifyEffectiveAccess, task-4 brief).
//
// actions and resources are the principal's DISTINCT action and resource
// lists (not aligned pairs) — mirroring SimulatePrincipalPolicy's own
// ActionNames/ResourceArns batching, and letting FilterEffective make ONE
// call per principal rather than one per pair.
//
// The returned map is keyed action+"|"+resource, one entry per (action,
// resource) combination FilterEffective needs a decision for; true means
// effectively allowed. A checker may also populate the full cross product
// or extra entries — FilterEffective only ever reads the keys it computed,
// and treats a missing key as not-allowed (deny by default).
type EffectiveAccessChecker func(ctx context.Context, principalArn string, actions, resources []string) (map[string]bool, error)

// FilterEffective drops IAM grants that are not actually exercisable once
// IAM evaluation is considered, so TranslatePrincipalPolicies only ever
// translates grants a principal can truly use.
//
// It operates on the ORIGINAL kafka-cluster actions and resource ARNs,
// pre-translate: that is the action/resource shape SimulatePrincipalPolicy
// itself understands, and it must run BEFORE translateStatements ever sees
// the policy documents.
//
// kafka-cluster:* agreement (the primary risk of this task): a
// "kafka-cluster:*" statement is expanded to the concrete AclMap actions
// eligible for each resource's denoted type via expandAction below, which
// calls arnDenotedResourceType — the EXACT function translateStatements
// (iam_translate.go) uses for the very same wildcard. Each concrete action
// is what gets simulated and, if allowed, what survives into the rebuilt
// statement (as an explicit action, never the literal "kafka-cluster:*"
// string). Expanding both FilterEffective and translateStatements through
// the identical function is what guarantees they can never disagree on
// what a wildcard statement denotes for a given resource — simulating the
// literal wildcard string instead would let the two diverge (e.g.
// FilterEffective clearing a whole wildcard statement as "allowed" while
// translateStatements independently expands it to an operation that was
// actually denied).
//
// Only Allow-effect statements are treated as grants subject to
// verification: their surviving (action, resource) pairs are checked and
// rebuilt as described below. Deny-effect statements are restrictions, not
// grants — there is nothing to "verify effective access" for a statement
// that already forbids something — so they pass through UNCHANGED, and
// their pairs are never sent to check. A statement with a value in
// "Effect" that translateStatements itself would not recognize (i.e.
// canonicalPermissionType returns "") is dropped: translateStatements
// would produce nothing from it either, so dropping it here is behaviour-
// preserving.
//
// One check call is made per principal — the union of all its Allow
// statements' distinct actions and resources — never one call per pair.
//
// Per principal, Allow statements are rebuilt to keep only the (action,
// resource) combos check reports allowed: a statement with N resources
// becomes up to N statements (one per resource, since a wildcard action's
// surviving set can differ per resource), each carrying only its surviving
// concrete actions; a resource with no surviving actions contributes no
// statement at all. Deny statements are carried over verbatim. A principal
// left with no statements across all its policies (inline and attached) is
// dropped from the result entirely. A principal whose grants all pass is
// still returned — its statements are re-expressed (wildcards resolved to
// explicit actions, one statement per resource) but denote the identical
// (action, resource) set, so TranslatePrincipalPolicies derives the same
// ACLs as it would without filtering.
func FilterEffective(ctx context.Context, check EffectiveAccessChecker, pps []iamservice.PrincipalPolicies) ([]iamservice.PrincipalPolicies, error) {
	out := make([]iamservice.PrincipalPolicies, 0, len(pps))

	for _, pp := range pps {
		pairs := extractAllowPairs(pp)
		if len(pairs) == 0 {
			// No kafka-cluster Allow grants at all: nothing for check to
			// verify and nothing translateStatements would ever produce
			// from Allow statements. Deny-only principals carry no
			// migratable grant either, so the principal is dropped.
			continue
		}

		actions, resources := distinctActionsAndResources(pairs)
		allowed, err := check(ctx, pp.PrincipalArn, actions, resources)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve effective IAM access for principal %q: %w", pp.PrincipalArn, err)
		}

		filtered := pp
		filtered.InlinePolicies = rebuildInlinePolicies(pp.InlinePolicies, allowed)
		filtered.AttachedPolicies = rebuildAttachedPolicies(pp.AttachedPolicies, allowed)

		if len(filtered.InlinePolicies) == 0 && len(filtered.AttachedPolicies) == 0 {
			continue
		}
		out = append(out, filtered)
	}

	return out, nil
}

// pair is one (concrete kafka-cluster action, resource ARN) combination —
// always post kafka-cluster:* expansion (expandAction), so it's already in
// the action-space both check and translateStatements agree on.
type pair struct {
	action   string
	resource string
}

// expandAction resolves one statement action into the concrete
// "kafka-cluster:<Verb>" actions it denotes for one specific resource ARN,
// in EXACTLY the same terms translateStatements uses:
//   - "kafka-cluster:*" expands via arnDenotedResourceType(resource) — the
//     same helper translateStatements calls — to every types.AclMap action
//     whose ResourceType matches the resource's own denoted type (or every
//     AclMap action at all, when the resource wildcards match-all).
//   - any other "kafka-cluster:" action expands to itself, but only if it
//     is a known types.AclMap key — an unmapped action denotes nothing,
//     just as translateStatements silently skips it.
//   - a non-"kafka-cluster:" action denotes nothing — translateStatements
//     ignores those entirely.
//
// Output is sorted for determinism (types.AclMap is a Go map; iteration
// order is otherwise unspecified).
func expandAction(action, resourceArn string) []string {
	if !strings.HasPrefix(action, "kafka-cluster:") {
		return nil
	}

	if action == "kafka-cluster:*" {
		resourceType, matchAll := arnDenotedResourceType(resourceArn)
		var out []string
		for candidate, mapping := range types.AclMap {
			if !matchAll && mapping.ResourceType != resourceType {
				continue
			}
			out = append(out, candidate)
		}
		sort.Strings(out)
		return out
	}

	if _, found := types.AclMap[action]; !found {
		return nil
	}
	return []string{action}
}

// extractAllowPairs collects every distinct (concrete action, resource)
// pair denoted by a principal's Allow-effect statements, across all its
// inline and attached policy documents, with kafka-cluster:* already
// expanded via expandAction. Deny statements and statements with an
// unrecognized Effect are not visited: see FilterEffective's doc comment
// for why.
func extractAllowPairs(pp iamservice.PrincipalPolicies) []pair {
	seen := make(map[pair]bool)
	var out []pair

	visit := func(doc map[string]any) {
		for _, stmt := range statementsOf(doc) {
			if canonicalPermissionType(stmt["Effect"]) != "Allow" {
				continue
			}
			actions := toStringSlice(stmt["Action"])
			resources := toStringSlice(stmt["Resource"])
			for _, action := range actions {
				for _, resource := range resources {
					for _, concrete := range expandAction(action, resource) {
						p := pair{action: concrete, resource: resource}
						if seen[p] {
							continue
						}
						seen[p] = true
						out = append(out, p)
					}
				}
			}
		}
	}

	for _, policy := range pp.InlinePolicies {
		visit(policy.PolicyDocument)
	}
	for _, policy := range pp.AttachedPolicies {
		visit(policy.PolicyDocument)
	}

	return out
}

// statementsOf returns a policy document's "Statement" list as
// []map[string]any, skipping any non-map entries — mirroring
// translateStatements' own tolerant parsing of the same field.
func statementsOf(doc map[string]any) []map[string]any {
	raw, _ := doc["Statement"].([]any)
	out := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		if stmt, ok := item.(map[string]any); ok {
			out = append(out, stmt)
		}
	}
	return out
}

// distinctActionsAndResources flattens a pair list into the two independent
// (deduped, sorted) lists EffectiveAccessChecker expects, matching how
// SimulatePrincipalPolicy itself is called: one ActionNames list, one
// ResourceArns list, not aligned pairs.
func distinctActionsAndResources(pairs []pair) (actions, resources []string) {
	actionSet := make(map[string]bool)
	resourceSet := make(map[string]bool)
	for _, p := range pairs {
		actionSet[p.action] = true
		resourceSet[p.resource] = true
	}
	actions = sortedKeys(actionSet)
	resources = sortedKeys(resourceSet)
	return actions, resources
}

func sortedKeys(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// rebuildInlinePolicies rebuilds each inline policy's Statement list via
// rebuildStatements, dropping any policy left with none.
func rebuildInlinePolicies(policies []iamservice.InlinePolicy, allowed map[string]bool) []iamservice.InlinePolicy {
	var out []iamservice.InlinePolicy
	for _, policy := range policies {
		statements := rebuildStatements(statementsOf(policy.PolicyDocument), allowed)
		if len(statements) == 0 {
			continue
		}
		out = append(out, iamservice.InlinePolicy{
			PolicyName:     policy.PolicyName,
			PolicyDocument: withStatements(policy.PolicyDocument, statements),
		})
	}
	return out
}

// rebuildAttachedPolicies is rebuildInlinePolicies' AttachedPolicy
// counterpart (same rebuild, different struct shape to preserve — PolicyArn
// and Description alongside PolicyName).
func rebuildAttachedPolicies(policies []iamservice.AttachedPolicy, allowed map[string]bool) []iamservice.AttachedPolicy {
	var out []iamservice.AttachedPolicy
	for _, policy := range policies {
		statements := rebuildStatements(statementsOf(policy.PolicyDocument), allowed)
		if len(statements) == 0 {
			continue
		}
		out = append(out, iamservice.AttachedPolicy{
			PolicyName:     policy.PolicyName,
			PolicyArn:      policy.PolicyArn,
			Description:    policy.Description,
			PolicyDocument: withStatements(policy.PolicyDocument, statements),
		})
	}
	return out
}

// withStatements shallow-copies a policy document, replacing its
// "Statement" entry — never mutating the caller's original doc (pps is the
// caller's data; FilterEffective must not alias into it).
func withStatements(doc map[string]any, statements []any) map[string]any {
	out := make(map[string]any, len(doc))
	for k, v := range doc {
		out[k] = v
	}
	out["Statement"] = statements
	return out
}

// rebuildStatements is FilterEffective's per-document core: it walks a
// policy document's original statements and returns the filtered
// replacement list.
//
//   - A statement whose Effect canonicalizes to "" (unrecognized) is
//     dropped: translateStatements would produce nothing from it either.
//   - A Deny statement passes through VERBATIM (see FilterEffective's doc
//     comment for why Deny is never simulated or filtered).
//   - An Allow statement is split into up to len(resources) replacement
//     statements, one per original resource, each keeping only that
//     resource's surviving concrete actions (survivingActions) with
//     "Effect" preserved from the original. A resource with zero surviving
//     actions contributes no replacement statement.
func rebuildStatements(statements []map[string]any, allowed map[string]bool) []any {
	var out []any
	for _, stmt := range statements {
		effect := stmt["Effect"]
		switch canonicalPermissionType(effect) {
		case "":
			continue
		case "Deny":
			out = append(out, stmt)
			continue
		}

		actions := toStringSlice(stmt["Action"])
		resources := toStringSlice(stmt["Resource"])
		for _, resource := range resources {
			survivors := survivingActions(actions, resource, allowed)
			if len(survivors) == 0 {
				continue
			}
			out = append(out, map[string]any{
				"Effect":   effect,
				"Action":   toAnySlice(survivors),
				"Resource": resource,
			})
		}
	}
	return out
}

// survivingActions expands actions against one resource (expandAction) and
// keeps only the concrete actions allowed reports as effectively allowed
// for that (action, resource) pair, deduped and in a stable (sorted, via
// expandAction) order.
func survivingActions(actions []string, resource string, allowed map[string]bool) []string {
	seen := make(map[string]bool)
	var out []string
	for _, action := range actions {
		for _, concrete := range expandAction(action, resource) {
			if seen[concrete] {
				continue
			}
			if !allowed[concrete+"|"+resource] {
				continue
			}
			seen[concrete] = true
			out = append(out, concrete)
		}
	}
	return out
}

// toAnySlice re-wraps a []string as []any: policy document fields are
// map[string]any-typed JSON, and toStringSlice (iam_translate.go) only
// recognizes []any (its own decoded-JSON shape) for a multi-value field —
// a plain []string would silently fail that type switch and read back as
// no actions at all.
func toAnySlice(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}
