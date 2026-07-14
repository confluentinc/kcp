// Package acls implements the source-read stage of the native-ACL migration
// plane: flattening sarama's ACL listing into the canonical types.Acls tuple
// and reading the MSK "allow.everyone.if.no.acl.found" server property.
package acls

import (
	"fmt"
	"regexp"

	"github.com/IBM/sarama"
	"github.com/confluentinc/kcp/internal/types"
)

// aclLister is the minimal surface ReadNativeACLs needs from a Kafka admin
// client. client.KafkaAdminClient satisfies it via ListAcls.
type aclLister interface {
	ListAcls() ([]sarama.ResourceAcls, error)
}

// resourcePatternTypeStrings maps sarama's AclResourcePatternType to the
// screaming-case form Confluent Cloud's ACL surfaces use (LITERAL/PREFIXED).
// This intentionally diverges from sarama's own titlecase String() (and from
// the scanKafkaAcls report path, which keeps that titlecase form for human
// display) because this reader feeds the CC-bound migration write path.
var resourcePatternTypeStrings = map[sarama.AclResourcePatternType]string{
	sarama.AclPatternUnknown:  "UNKNOWN",
	sarama.AclPatternAny:      "ANY",
	sarama.AclPatternMatch:    "MATCH",
	sarama.AclPatternLiteral:  "LITERAL",
	sarama.AclPatternPrefixed: "PREFIXED",
}

// ReadNativeACLs lists ACLs via the Kafka Admin API and flattens sarama's
// nested ResourceAcls into the flat types.Acls tuple. ResourceType,
// Operation, and PermissionType mirror the exact string forms scanKafkaAcls
// already produces (internal/services/kafka/kafka_service.go), via sarama's
// enum String() methods. ResourcePatternType is normalized to the
// screaming-case LITERAL/PREFIXED form Confluent Cloud expects.
func ReadNativeACLs(adm aclLister) ([]types.Acls, error) {
	resourceAcls, err := adm.ListAcls()
	if err != nil {
		return nil, fmt.Errorf("failed to list acls: %v", err)
	}

	var flattenedAcls []types.Acls
	for _, resourceAcl := range resourceAcls {
		resourceType := resourceAcl.ResourceType
		patternType, ok := resourcePatternTypeStrings[resourceAcl.ResourcePatternType]
		if !ok {
			patternType = resourcePatternTypeStrings[sarama.AclPatternUnknown]
		}
		for _, acl := range resourceAcl.Acls {
			operation := acl.Operation
			permissionType := acl.PermissionType
			flattenedAcls = append(flattenedAcls, types.Acls{
				ResourceType:        resourceType.String(),
				ResourceName:        resourceAcl.ResourceName,
				ResourcePatternType: patternType,
				Principal:           acl.Principal,
				Host:                acl.Host,
				Operation:           operation.String(),
				PermissionType:      permissionType.String(),
			})
		}
	}

	return flattenedAcls, nil
}

// allowEveryonePattern matches the MSK/Kafka server property
// allow.everyone.if.no.acl.found, tolerating surrounding whitespace around
// the "=" the way IsFetchFromFollowerEnabled does for replica.selector.class
// (internal/services/msk/msk_service.go).
var allowEveryonePattern = regexp.MustCompile(`allow\.everyone\.if\.no\.acl\.found\s*=\s*(true|false)`)

// AllowEveryoneIfNoACLFound regex-matches allow.everyone.if.no.acl.found in
// the plain-text server.properties content and returns its parsed boolean
// value. When the property is absent, it returns true — the MSK default.
func AllowEveryoneIfNoACLFound(serverProperties []byte) bool {
	match := allowEveryonePattern.FindSubmatch(serverProperties)
	if match == nil {
		return true
	}
	return string(match[1]) == "true"
}
