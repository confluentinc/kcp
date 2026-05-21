package plan

import (
	"github.com/confluentinc/kcp/internal/types"

	kafkatypes "github.com/aws/aws-sdk-go-v2/service/kafka/types"
)

// Single source of truth for "what do null / empty values mean for this
// cluster?" Both the rule evaluator (cluster_type.go) and the
// Open-Question detector (plan_service.go) read these primitives so the
// SERVERLESS-vs-PROVISIONED distinction lives in exactly one place.

// isServerless reports whether the cluster is MSK Serverless. Serverless
// clusters don't have broker nodes and don't expose ACLs through the
// admin API path used by `kcp scan clusters`, so several "looks like an
// incomplete scan" signals are actually expected emptiness.
func isServerless(c types.ProcessedCluster) bool {
	return c.AWSClientInformation.MskClusterConfig.ClusterType == kafkatypes.ClusterTypeServerless
}

// aclScanRan reports whether the ACL list represents a successful scan.
// kcp initializes Acls as a nil slice and appends, so a successful scan
// with 0 ACLs renders identically to "scan didn't run". Topics populated
// + not-Serverless is the disambiguation.
func aclScanRan(c types.ProcessedCluster) bool {
	if c.KafkaAdminClientInformation.Topics == nil {
		return false
	}
	if isServerless(c) {
		return false
	}
	return true
}
