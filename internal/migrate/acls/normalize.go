package acls

import (
	"fmt"
	"regexp"

	"github.com/confluentinc/kcp/internal/types"
)

// Diagnostic reports a decision NormalizeForCC made while shaping an ACL set
// for a Confluent Cloud target — a fidelity loss (host normalization), an
// outright drop (drop-list, invalid op×resource combo, redundant implied
// op), or informational context. Level is one of "info", "warn", "error".
// Consumed by dry-run output and apply gating (design §5/§10).
type Diagnostic struct {
	Level   string
	Message string
}

// resourceKey identifies the (resource, principal, host) tuple that
// operation-implication de-dup groups ACLs by. Computed AFTER host
// normalization, so two ACLs that only differed by source host collapse
// onto the same key once CC forces both to host "*" — which is the correct
// behaviour: on the target, a Describe granted at one original host is
// exactly as redundant against a Read granted at a different original host,
// because CC has no host scoping at all.
type resourceKey struct {
	resourceType        string
	resourceName        string
	resourcePatternType string
	principal           string
	host                string
}

func keyOf(a types.Acls) resourceKey {
	return resourceKey{
		resourceType:        a.ResourceType,
		resourceName:        a.ResourceName,
		resourcePatternType: a.ResourcePatternType,
		principal:           a.Principal,
		host:                a.Host,
	}
}

// mskBrokerPrincipalPattern recognizes an MSK broker's own mTLS identity:
// the certificate CN is the broker's bootstrap hostname, e.g.
// "b-1.mytestcluster.abcde.c2.kafka.us-east-1.amazonaws.com"
// (plans/acl-research/aws-msk.md). ACLs granted to this identity are
// inter-broker plumbing, not a customer principal, so they never migrate.
//
// Generic self-managed super.users is a broker-config list of ARBITRARY
// principal names (e.g. "User:Bob") and cannot be distinguished from an
// ordinary customer principal by the ACL tuple alone — that membership
// lives in broker config this pure function never sees. Only the
// pattern-recognizable MSK broker-hostname shape is dropped here; a
// customer-supplied super.users allow-list would need to be threaded in as
// extra input to close that gap (not required by Phase 1a).
var mskBrokerPrincipalPattern = regexp.MustCompile(`(?i)^User:CN=b-\d+\..*\.kafka\..*$`)

func isBrokerPrincipal(principal string) bool {
	return mskBrokerPrincipalPattern.MatchString(principal)
}

// ccValidOperations lists the operations Confluent Cloud's Kafka REST v3
// ACL API accepts for a resource type that is NOT already fully open.
// Absence from this map (Topic, Cluster) means "standard operation set,
// unrestricted" (design §5 rule 5: "Cluster/Topic: allow the standard
// ops"). Group and TransactionalID are restricted.
//
// Keyed on "TransactionalID" (capital I and D) to match sarama's
// AclResourceType.String() — the form produced by both ReadNativeACLs
// (read.go) and scanKafkaAcls (internal/services/kafka/kafka_service.go).
var ccValidOperations = map[string]map[string]bool{
	"Group":           {"Read": true, "Describe": true, "Delete": true},
	"TransactionalID": {"Describe": true, "Write": true},
}

// NormalizeForCC transforms a canonical MSK-sourced ACL set into a
// Confluent-Cloud-ready set, applying the target-aware rules from design §5:
//
//  1. Drop-list — ClusterAction, DelegationToken, MSK broker principals.
//  2. CC operation×resource validity — Group/TransactionalID only accept a
//     restricted operation set; an invalid combo is dropped.
//  3. Host normalization — any non-"*" host is forced to "*" (CC has no
//     host scoping); the lost host is reported.
//  4. Operation-implication de-dup — an ALLOW Describe/DescribeConfigs is
//     redundant (and dropped) once a broader ALLOW already covers the same
//     resource+principal+host. DENY is never touched by this rule.
//
// PermissionType (DENY vs ALLOW) and ResourcePatternType (Literal vs
// Prefixed) are preserved verbatim on every surviving ACL. Pure function —
// no cluster calls.
func NormalizeForCC(in []types.Acls) (out []types.Acls, diags []Diagnostic) {
	// Stage 1: drop-list.
	stage1 := make([]types.Acls, 0, len(in))
	for _, a := range in {
		switch {
		case a.Operation == "ClusterAction":
			diags = append(diags, Diagnostic{Level: "info", Message: fmt.Sprintf(
				"dropped ClusterAction ACL for principal %q on %s %q: inter-broker operation, not applicable to a CC client",
				a.Principal, a.ResourceType, a.ResourceName)})
			continue
		case a.ResourceType == "DelegationToken":
			diags = append(diags, Diagnostic{Level: "info", Message: fmt.Sprintf(
				"dropped DelegationToken ACL for principal %q: delegation tokens have no CC equivalent",
				a.Principal)})
			continue
		case isBrokerPrincipal(a.Principal):
			diags = append(diags, Diagnostic{Level: "info", Message: fmt.Sprintf(
				"dropped ACL for principal %q: matches an MSK broker identity, not a customer principal",
				a.Principal)})
			continue
		}
		stage1 = append(stage1, a)
	}

	// Stage 2: CC operation×resource validity.
	stage2 := make([]types.Acls, 0, len(stage1))
	for _, a := range stage1 {
		if valid, restricted := ccValidOperations[a.ResourceType]; restricted && !valid[a.Operation] {
			diags = append(diags, Diagnostic{Level: "warn", Message: fmt.Sprintf(
				"dropped %s %s ACL for principal %q on %s %q: operation %q is not valid for resource type %s on Confluent Cloud",
				a.PermissionType, a.ResourceType, a.Principal, a.ResourceType, a.ResourceName, a.Operation, a.ResourceType)})
			continue
		}
		stage2 = append(stage2, a)
	}

	// Stage 3: host normalization.
	stage3 := make([]types.Acls, 0, len(stage2))
	for _, a := range stage2 {
		if a.Host != "*" {
			diags = append(diags, Diagnostic{Level: "warn", Message: fmt.Sprintf(
				"host %q for principal %q on %s %q normalized to \"*\": Confluent Cloud does not support host-scoped ACLs",
				a.Host, a.Principal, a.ResourceType, a.ResourceName)})
			a.Host = "*"
		}
		stage3 = append(stage3, a)
	}

	// Stage 4: operation-implication de-dup (ALLOW only).
	impliesDescribe := map[resourceKey]bool{}
	impliesDescribeConfigs := map[resourceKey]bool{}
	for _, a := range stage3 {
		if a.PermissionType != "Allow" {
			continue
		}
		switch a.Operation {
		case "Read", "Write", "Alter", "Delete":
			impliesDescribe[keyOf(a)] = true
		case "AlterConfigs":
			impliesDescribeConfigs[keyOf(a)] = true
		}
	}

	for _, a := range stage3 {
		if a.PermissionType == "Allow" && a.Operation == "Describe" && impliesDescribe[keyOf(a)] {
			diags = append(diags, Diagnostic{Level: "info", Message: fmt.Sprintf(
				"dropped redundant Allow Describe ACL for principal %q on %s %q: implied by an existing Allow Read/Write/Alter/Delete",
				a.Principal, a.ResourceType, a.ResourceName)})
			continue
		}
		if a.PermissionType == "Allow" && a.Operation == "DescribeConfigs" && impliesDescribeConfigs[keyOf(a)] {
			diags = append(diags, Diagnostic{Level: "info", Message: fmt.Sprintf(
				"dropped redundant Allow DescribeConfigs ACL for principal %q on %s %q: implied by an existing Allow AlterConfigs",
				a.Principal, a.ResourceType, a.ResourceName)})
			continue
		}
		out = append(out, a)
	}

	return out, diags
}
