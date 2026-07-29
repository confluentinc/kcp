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
// that already forbids something — so their pairs are never sent to check;
// they are instead carried over EXPANDED-BUT-UNFILTERED (see
// rebuildStatements). A statement with a value in "Effect" that
// translateStatements itself would not recognize (i.e.
// canonicalPermissionType returns "") is dropped: translateStatements
// would produce nothing from it either, so dropping it here is behaviour-
// preserving.
//
// A Deny statement is itself a migratable grant: a source IAM Deny
// translates to a native Kafka DENY ACL (translateStatements), and a Kafka
// DENY overrides a broader Allow — so a principal whose ONLY kafka-cluster
// statements are Deny (no Allow anywhere) must still survive filtering.
// Dropping it would silently widen the target's effective access beyond
// the source's, which is why FilterEffective never short-circuits on "no
// Allow pairs" before rebuildStatements has had a chance to find a
// surviving Deny.
//
// One check call is made per principal — the union of all its IN-CLUSTER
// Allow statements' distinct actions and resources — never one call per
// pair. A principal with no in-cluster Allow pairs at all skips the call
// entirely (nothing for it to verify) rather than invoking check with empty
// lists. This is also what keeps a principal whose kafka-cluster grants are
// entirely on some OTHER cluster (e.g. an irrelevant account role) from ever
// reaching check at all — clusterArn scoping happens BEFORE simulation, not
// after (see the "scope-before-verify" note below).
//
// Per principal, Allow statements are rebuilt to keep only the (action,
// resource) combos check reports allowed: a statement with N resources
// becomes up to N statements (one per resource, since a wildcard action's
// surviving set can differ per resource), each carrying only its surviving
// concrete actions; a resource with no surviving actions, OR that is not
// in-cluster, contributes no statement at all. Deny statements are rebuilt
// the same way but WITHOUT consulting check — every concrete kafka-cluster
// (action, resource) pair the statement denotes (post kafka-cluster:*
// expansion) survives unconditionally PROVIDED the resource is in-cluster;
// a resource with no kafka-cluster actions at all (e.g. a Deny naming only
// non-kafka-cluster actions), or that is off-cluster, contributes no
// statement, same as an all-denied Allow. A principal left with no
// statements across all its policies (inline and attached) is dropped from
// the result entirely — whether that's because every Allow was denied,
// every remaining resource was off-cluster, every statement named no
// kafka-cluster action, or some combination. A principal whose grants all
// pass is still returned — its statements are re-expressed (wildcards
// resolved to explicit actions, one statement per resource) but denote the
// identical (action, resource) set, so TranslatePrincipalPolicies derives
// the same ACLs as it would without filtering.
//
// scope-before-verify: clusterArn is the target source cluster ARN, and
// BOTH extractAllowPairs (what gets sent to check) and rebuildStatements
// (what's kept in the output) scope resources through the SAME predicate
// TranslatePrincipalPolicies itself uses (clusterArnMatches,
// iam_translate.go) — moving that scoping to before simulation rather than
// after it (translate has always scoped this way; only when it runs
// relative to check is new). This is provably equivalent to scoping after
// translate: SimulatePrincipalPolicy evaluates each resource independently,
// so a resource's allowed/denied decision never depends on which OTHER
// resources shared its batch — scoping first just means fewer resources
// (and, for a principal with no in-cluster grants at all, zero calls) ever
// reach check, with the identical survivors. clusterArnMatches("*",
// clusterArn) is true (a bare Resource: "*" grant is inherently
// cluster-agnostic, so it counts as in-cluster) — it is still simulated as
// the literal "*" resource string.
func FilterEffective(ctx context.Context, check EffectiveAccessChecker, clusterArn string, pps []iamservice.PrincipalPolicies) ([]iamservice.PrincipalPolicies, error) {
	out := make([]iamservice.PrincipalPolicies, 0, len(pps))

	for _, pp := range pps {
		pairs := extractAllowPairs(pp, clusterArn)

		// allowed stays an empty (nil-safe) map when there are no in-cluster
		// Allow pairs to verify — no pointless check call — but
		// rebuildStatements still runs: a Deny-only principal's in-cluster
		// kafka-cluster Deny statements must get their chance to survive
		// (see FilterEffective's doc comment). allowed[anything] then reads
		// false for every key, so an Allow statement's own pairs (if
		// extractAllowPairs somehow found none but rebuildStatements still
		// sees one — it can't, but this keeps the invariant explicit) never
		// survive without a real check.
		allowed := map[string]bool{}
		if len(pairs) > 0 {
			actions, resources := distinctActionsAndResources(pairs)
			var err error
			allowed, err = check(ctx, pp.PrincipalArn, actions, resources)
			if err != nil {
				return nil, fmt.Errorf("failed to resolve effective IAM access for principal %q: %w", pp.PrincipalArn, err)
			}
		}

		filtered := pp
		filtered.InlinePolicies = rebuildInlinePolicies(pp.InlinePolicies, allowed, clusterArn)
		filtered.AttachedPolicies = rebuildAttachedPolicies(pp.AttachedPolicies, allowed, clusterArn)

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
// for why. A resource that does not satisfy clusterArnMatches(resource,
// clusterArn) is skipped entirely — off-cluster grants must never reach the
// checker (scope-before-verify, see FilterEffective's doc comment).
func extractAllowPairs(pp iamservice.PrincipalPolicies, clusterArn string) []pair {
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
					if !clusterArnMatches(resource, clusterArn) {
						continue
					}
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
func rebuildInlinePolicies(policies []iamservice.InlinePolicy, allowed map[string]bool, clusterArn string) []iamservice.InlinePolicy {
	var out []iamservice.InlinePolicy
	for _, policy := range policies {
		statements := rebuildStatements(statementsOf(policy.PolicyDocument), allowed, clusterArn)
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
func rebuildAttachedPolicies(policies []iamservice.AttachedPolicy, allowed map[string]bool, clusterArn string) []iamservice.AttachedPolicy {
	var out []iamservice.AttachedPolicy
	for _, policy := range policies {
		statements := rebuildStatements(statementsOf(policy.PolicyDocument), allowed, clusterArn)
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
//   - A resource that does not satisfy clusterArnMatches(resource,
//     clusterArn) is skipped entirely — for BOTH Allow and Deny — so the
//     rebuilt output only ever names in-cluster resources (scope-before-
//     verify, see FilterEffective's doc comment).
//   - Both Allow and Deny statements are split into up to len(resources)
//     replacement statements, one per original IN-CLUSTER resource, each
//     keeping only that resource's surviving concrete kafka-cluster
//     actions, with "Effect" preserved from the original. A resource with
//     zero surviving actions contributes no replacement statement —
//     including when the resource's actions were never kafka-cluster
//     actions to begin with (e.g. a Deny naming only "s3:*"), which is what
//     keeps a non-kafka-grant statement from single-handedly keeping a
//     principal alive.
//   - Allow's surviving actions are those check reported effectively
//     allowed (survivingActions). Deny's surviving actions are every
//     concrete kafka-cluster action the statement denotes at all
//     (expandDistinctActions) — Deny is never simulated or filtered (see
//     FilterEffective's doc comment for why), so nothing here consults
//     allowed for it.
func rebuildStatements(statements []map[string]any, allowed map[string]bool, clusterArn string) []any {
	var out []any
	for _, stmt := range statements {
		effect := stmt["Effect"]
		permissionType := canonicalPermissionType(effect)
		if permissionType == "" {
			continue
		}

		actions := toStringSlice(stmt["Action"])
		resources := toStringSlice(stmt["Resource"])
		for _, resource := range resources {
			if !clusterArnMatches(resource, clusterArn) {
				continue
			}
			var survivors []string
			if permissionType == "Deny" {
				survivors = expandDistinctActions(actions, resource)
			} else {
				survivors = survivingActions(actions, resource, allowed)
			}
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

// expandDistinctActions expands a statement's actions against one resource
// (expandAction) into the deduped set of concrete kafka-cluster actions
// they denote for it, in a stable (first-seen) order. An action with no
// kafka-cluster meaning for this resource (expandAction returns nil)
// contributes nothing — this is the same expansion Allow grants go through
// before being checked, factored out so Deny statements (which skip the
// check step entirely) agree with Allow and with translateStatements on
// what a wildcard denotes.
func expandDistinctActions(actions []string, resource string) []string {
	seen := make(map[string]bool)
	var out []string
	for _, action := range actions {
		for _, concrete := range expandAction(action, resource) {
			if seen[concrete] {
				continue
			}
			seen[concrete] = true
			out = append(out, concrete)
		}
	}
	return out
}

// survivingActions is expandDistinctActions' Allow counterpart: it expands
// actions against one resource and keeps only the concrete actions allowed
// reports as effectively allowed for that (action, resource) pair.
func survivingActions(actions []string, resource string, allowed map[string]bool) []string {
	var out []string
	for _, concrete := range expandDistinctActions(actions, resource) {
		if !allowed[concrete+"|"+resource] {
			continue
		}
		out = append(out, concrete)
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
