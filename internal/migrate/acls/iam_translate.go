package acls

import (
	"strings"

	"github.com/confluentinc/kcp/internal/types"
)

// translateStatements is the pure translation core of the IAM ACL reader: it
// turns one IAM policy document's "Statement" list into the flat
// types.Acls tuples the same grants would produce as native Kafka ACLs.
//
// principalArn is the IAM principal (user/role) the policy is attached to;
// clusterArn is the source MSK cluster's ARN, used to scope out grants that
// target some other cluster (kafka-cluster IAM policies are frequently
// written with broad wildcards that also cover unrelated clusters).
//
// This ports processPolicy/parseKafkaResourceFromArn/
// determineResourceNameAndPattern/getPrincipalFromArn from
// cmd/create_asset/migrate_acls/iam_acls/iam_acls_generator.go, with three
// structural fixes over that reference:
//
//  1. Iterates ALL of a statement's resources (the reference only ever reads
//     resources[0]) — one ACL per resource.
//  2. Cluster-ARN scoping: a resource is only translated when its ARN
//     resolves to this source cluster (clusterArnMatches) — the reference
//     does no scoping at all, silently importing every grant regardless of
//     which cluster it names.
//  3. "kafka-cluster:*" expands only to the AclMap entries whose
//     ResourceType matches the resource ARN's own denoted type (parsed from
//     its "<restype>/" segment) — the reference cross-products every AclMap
//     entry against every resource, e.g. emitting Group and TransactionalID
//     ACLs for a policy that only ever names topic ARNs.
//
// CANONICALIZATION (adapt-at-boundary decision, see task-4 brief): the
// emitted enum fields are adapted to match the native ACL reader's canonical
// forms (read.go / normalize.go, mirroring sarama's Acl*.String() forms) so
// that IAM-sourced and native-sourced ACLs for the same grant are identical
// types.Acls tuples and dedupe identically downstream:
//   - ResourceType: AclMap's "TransactionalId" -> "TransactionalID" (capital
//     D) via canonicalResourceType.
//   - ResourcePatternType: "LITERAL"/"PREFIXED" -> "Literal"/"Prefixed" via
//     determineResourceNameAndPattern below.
//   - PermissionType: Effect -> "Allow"/"Deny" (titlecase) via
//     canonicalPermissionType, NOT the reference's ALLOW/DENY.
//   - Operation: AclMap's operations (Read/Write/Alter/AlterConfigs/
//     Describe/DescribeConfigs/Create/Delete/IdempotentWrite) already match
//     sarama's AclOperation.String() forms verbatim — no adaptation needed.
//
// types.AclMap itself is never modified; all casing adaptation happens here.
//
// Translation is a direct 1:1 mapping — one ACL per (action, resource) pair.
// It does not synthesize implied companion operations (e.g. a Describe
// alongside a Read): Confluent Cloud already implies Describe from broader
// ops, and NormalizeForCC's de-dup stage (normalize.go) will drop any
// redundant Describe an IAM policy happens to also grant explicitly. Deny
// statements are preserved verbatim; only Allow is ever subject to
// downstream implication.
func translateStatements(principalArn, clusterArn string, doc map[string]any) []types.Acls {
	var out []types.Acls

	statements, _ := doc["Statement"].([]any)
	for _, raw := range statements {
		statement, ok := raw.(map[string]any)
		if !ok {
			continue
		}

		permissionType := canonicalPermissionType(statement["Effect"])
		if permissionType == "" {
			continue
		}

		actions := toStringSlice(statement["Action"])
		resources := toStringSlice(statement["Resource"])

		for _, action := range actions {
			if !strings.HasPrefix(action, "kafka-cluster:") {
				continue
			}

			for _, resourceArn := range resources {
				if !clusterArnMatches(resourceArn, clusterArn) {
					continue
				}

				if action == "kafka-cluster:*" {
					resourceType, matchAll := arnDenotedResourceType(resourceArn)
					for _, mapping := range types.AclMap {
						if !matchAll && mapping.ResourceType != resourceType {
							continue
						}
						out = append(out, buildAcl(principalArn, resourceArn, mapping, permissionType))
					}
					continue
				}

				mapping, found := types.AclMap[action]
				if !found {
					continue
				}
				out = append(out, buildAcl(principalArn, resourceArn, mapping, permissionType))
			}
		}
	}

	return out
}

// buildAcl assembles the canonical types.Acls tuple for one (resource ARN,
// AclMap entry, permission) combination.
func buildAcl(principalArn, resourceArn string, mapping types.ACLMapping, permissionType string) types.Acls {
	resourceName, patternType := resourceNameAndPattern(resourceArn, mapping)
	return types.Acls{
		ResourceType:        canonicalResourceType(mapping.ResourceType),
		ResourceName:        resourceName,
		ResourcePatternType: patternType,
		Principal:           principalFromArn(principalArn),
		Host:                "*",
		Operation:           mapping.Operation,
		PermissionType:      permissionType,
	}
}

// canonicalPermissionType maps an IAM statement's "Effect" value to the
// canonical, titlecase types.Acls.PermissionType the native reader produces
// ("Allow"/"Deny" — sarama's AclPermissionType.String() form), tolerating any
// input casing. Returns "" for anything else, signalling the caller to skip
// the statement rather than emit a nonsensical permission.
func canonicalPermissionType(effect any) string {
	s, ok := effect.(string)
	if !ok {
		return ""
	}
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "allow":
		return "Allow"
	case "deny":
		return "Deny"
	default:
		return ""
	}
}

// canonicalResourceType adapts AclMap's ResourceType spelling to the form the
// native ACL reader produces. Only "TransactionalId" differs (->
// "TransactionalID", capital D, matching sarama's AclResourceType.String());
// Topic/Group/Cluster already match verbatim.
func canonicalResourceType(resourceType string) string {
	if resourceType == "TransactionalId" {
		return "TransactionalID"
	}
	return resourceType
}

// toStringSlice normalizes an IAM statement's "Action"/"Resource" value,
// which per the IAM policy grammar is either a single string or a JSON array
// of strings ([]any once decoded), into a []string. Any non-string element
// in an array is skipped rather than causing a panic on malformed input.
func toStringSlice(v any) []string {
	switch val := v.(type) {
	case string:
		return []string{val}
	case []any:
		out := make([]string, 0, len(val))
		for _, item := range val {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

// principalFromArn derives the Kafka "User:<name>" principal string from an
// IAM principal ARN, taking the ARN's last "/"-delimited path element as the
// name (e.g. "arn:aws:iam::111122223333:role/AppRole" -> "User:AppRole").
//
// This is deliberately NOT run through utils.CleanPrincipalName the way the
// ported reference (getPrincipalFromArn) does when it also builds a
// Terraform-resource-safe name: CleanPrincipalName lowercases and strips the
// "User:" prefix for Terraform identifier purposes, which would corrupt the
// actual case-sensitive Kafka principal string emitted here.
func principalFromArn(principalArn string) string {
	parts := strings.Split(principalArn, "/")
	name := parts[len(parts)-1]
	return "User:" + name
}

// splitArnResource splits an MSK-style ARN
// "arn:aws:kafka:region:account:<restype>/<rest...>" into its resource-type
// segment ("<restype>") and the remaining "/"-delimited path, using the last
// ":"-delimited component of the ARN (so region/account, which may
// themselves be "*" or contain no further structure, never confuse the
// split). ok is false when the ARN has no recognizable "<restype>/<rest>"
// shape after the last colon (e.g. a bare wildcard or malformed ARN).
func splitArnResource(arn string) (restype, rest string, ok bool) {
	idx := strings.LastIndex(arn, ":")
	if idx == -1 {
		return "", "", false
	}
	path := arn[idx+1:]
	slash := strings.Index(path, "/")
	if slash == -1 {
		return "", "", false
	}
	return path[:slash], path[slash+1:], true
}

// arnClusterIdentity extracts the (cluster-name, cluster-uuid) pair from an
// MSK-style resource or cluster ARN
// (arn:aws:kafka:region:account:<restype>/<name>/<uuid>[/<resource-name>]).
// The name+uuid pair is what actually identifies "this cluster" — an MSK
// cluster's UUID is globally unique, so this is meaningful regardless of the
// ARN's region/account fields.
func arnClusterIdentity(arn string) (name, uuid string, ok bool) {
	_, rest, ok := splitArnResource(arn)
	if !ok {
		return "", "", false
	}
	parts := strings.SplitN(rest, "/", 3)
	if len(parts) < 2 {
		return "", "", false
	}
	return parts[0], parts[1], true
}

// clusterArnMatches reports whether an IAM grant's resource ARN denotes (or
// wildcard-covers) the given source cluster ARN, gating which grants are
// eligible for translation at all (fix 2 over the reference, which performs
// no cluster scoping whatsoever).
//
// True when:
//   - grantResourceArn is the bare wildcard "*", or
//   - grantResourceArn contains the substring ":*" anywhere — i.e. some
//     ARN field (region, account, or the resource path itself) is wildcarded
//     immediately after a colon, the shape IAM policies use for
//     "arn:aws:kafka:*:<account>:*"-style broad grants; or
//   - grantResourceArn's own cluster-name+uuid (arnClusterIdentity) matches
//     sourceClusterArn's, regardless of any other field (notably region:
//     region is not part of an MSK cluster's identity, so a grant ARN with a
//     different or wildcarded region but the same name+uuid still matches).
func clusterArnMatches(grantResourceArn, sourceClusterArn string) bool {
	if grantResourceArn == "*" || strings.Contains(grantResourceArn, ":*") {
		return true
	}

	grantName, grantUUID, ok := arnClusterIdentity(grantResourceArn)
	if !ok {
		return false
	}
	sourceName, sourceUUID, ok := arnClusterIdentity(sourceClusterArn)
	if !ok {
		return false
	}
	return grantName == sourceName && grantUUID == sourceUUID
}

// arnRestypeToResourceType maps an ARN's "<restype>/" segment spelling
// (lowercase-hyphen, as AWS writes it: "topic", "group",
// "transactional-id", "cluster") to the ResourceType spelling AclMap keys its
// entries on ("Topic", "Group", "TransactionalId", "Cluster"). known is false
// for any other restype (it denotes nothing in AclMap).
func arnRestypeToResourceType(restype string) (resourceType string, known bool) {
	switch restype {
	case "topic":
		return "Topic", true
	case "group":
		return "Group", true
	case "transactional-id":
		return "TransactionalId", true
	case "cluster":
		return "Cluster", true
	default:
		return "", false
	}
}

// arnDenotedResourceType determines which AclMap ResourceType(s) a
// "kafka-cluster:*" action should expand to for one statement-resource (fix
// 3 over the reference, which cross-products every AclMap entry against
// every resource regardless of type). matchAll is true when the resource ARN
// itself is wildcarded (bare "*", contains ":*", or its restype segment is
// "*") — in which case every AclMap entry is eligible, matching the
// unrestricted "kafka-cluster:*" grant on the whole cluster. Otherwise only
// entries whose ResourceType equals the returned resourceType are eligible;
// an ARN whose restype AWS itself doesn't recognize as one of
// topic/group/transactional-id/cluster denotes no AclMap entries at all.
func arnDenotedResourceType(resourceArn string) (resourceType string, matchAll bool) {
	if resourceArn == "*" || strings.Contains(resourceArn, ":*") {
		return "", true
	}

	restype, _, ok := splitArnResource(resourceArn)
	if !ok || restype == "*" {
		return "", true
	}

	resourceType, known := arnRestypeToResourceType(restype)
	if !known {
		return "", false
	}
	return resourceType, false
}

// resourceNameAndPattern derives the ACL's ResourceName/ResourcePatternType
// for one (resource ARN, AclMap entry) pair.
//
//   - Cluster-type ops always resolve to the fixed "kafka-cluster"/"Literal"
//     pair: the cluster resource has no customer-visible sub-name, so this
//     holds regardless of RequiresPattern or the resource ARN's actual
//     content (including when the ARN itself is the bare wildcard "*" —
//     unlike Topic/Group/TransactionalId, wildcarding the resource here
//     doesn't change what's being named).
//   - Non-pattern ops (RequiresPattern false) default to "*"/"Literal" —
//     there is nothing in the ARN to parse a sub-resource name out of.
//   - Otherwise the resource name is parsed out of the ARN itself
//     (parseResourceNameAndPattern).
func resourceNameAndPattern(resourceArn string, mapping types.ACLMapping) (resourceName, patternType string) {
	if mapping.ResourceType == "Cluster" {
		return "kafka-cluster", "Literal"
	}
	if !mapping.RequiresPattern {
		return "*", "Literal"
	}
	return parseResourceNameAndPattern(resourceArn, mapping.ResourceType)
}

// parseResourceNameAndPattern parses a Topic/Group/TransactionalId resource
// ARN into its (name, pattern) pair. Ported from the reference's
// parseKafkaResourceFromArn + parseTopicFromArn/parseGroupFromArn/
// parseTransactionalIdFromArn, collapsed into one restype-parameterized
// function (they were three copy-pasted bodies differing only in the ARN
// marker they split on), with output casing canonicalized to
// "Literal"/"Prefixed" for determineResourceNameAndPattern.
func parseResourceNameAndPattern(arn, resourceType string) (string, string) {
	if arn == "*" || strings.Contains(arn, ":*") {
		return "*", "Literal"
	}

	var restype string
	switch resourceType {
	case "Topic":
		restype = "topic"
	case "Group":
		restype = "group"
	case "TransactionalId":
		restype = "transactional-id"
	default:
		return "*", "Literal"
	}

	marker := ":" + restype + "/"
	idx := strings.Index(arn, marker)
	if idx == -1 {
		return "*", "Literal"
	}

	segments := strings.Split(arn[idx+len(marker):], "/")
	if len(segments) < 3 {
		return "*", "Literal"
	}

	return determineResourceNameAndPattern(segments[len(segments)-1])
}

// determineResourceNameAndPattern classifies a trailing ARN resource-name
// segment as LITERAL or PREFIXED, canonicalized to "Literal"/"Prefixed".
// Ported verbatim (behaviourally) from the reference: a name ending in "*"
// but not also starting with one is a prefix pattern ("retention-*" ->
// name "retention-", Prefixed); Kafka ACLs support neither a suffix pattern
// nor mid-string wildcards, so every other shape (including a bare "*",
// handled above by its own exact-match branch, and any embedded/leading "*")
// passes through unchanged as Literal.
func determineResourceNameAndPattern(resourceName string) (string, string) {
	if resourceName == "*" {
		return "*", "Literal"
	}
	if strings.HasSuffix(resourceName, "*") && !strings.HasPrefix(resourceName, "*") {
		return strings.TrimSuffix(resourceName, "*"), "Prefixed"
	}
	return resourceName, "Literal"
}
