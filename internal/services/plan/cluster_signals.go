package plan

import (
	"github.com/confluentinc/kcp/internal/types"

	kafkatypes "github.com/aws/aws-sdk-go-v2/service/kafka/types"
)

// defaultTargetCloud is the assumed target when `target_cloud` is unset
// in plan-inputs.yaml. MSK is AWS-native, so AWS is the implicit default.
const defaultTargetCloud = "aws"

// targetCloud returns the customer's `target_cloud` plan-input or the
// default ("aws") when unset. Centralizes the empty-string fallback so
// every decision rule reads it the same way.
func targetCloud(inputs types.PlanInputsResolved) string {
	if inputs.TargetCloud == "" {
		return defaultTargetCloud
	}
	return inputs.TargetCloud
}

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
// kafka_service.go now initializes Acls as a non-nil empty slice on a
// successful scan (so `len(Acls) == 0` is a real "scan ran with 0
// ACLs"), which means nil unambiguously signals "scan didn't run" —
// either because the scan didn't happen at all or because the customer
// passed `--skip-acls`. Serverless clusters don't expose ACLs via this
// API and are excluded.
func aclScanRan(c types.ProcessedCluster) bool {
	if isServerless(c) {
		return false
	}
	return c.KafkaAdminClientInformation.Acls != nil
}

// brokerInventoryGap reports whether the broker inventory is suspect (an
// actual scan gap rather than an expected-empty state). Serverless
// clusters have no broker nodes by design, so emptiness on Serverless is
// not a gap; the gap is "MSK PROVISIONED cluster with no Nodes
// populated" — that's almost certainly a missing or incomplete discover
// run.
func brokerInventoryGap(c types.ProcessedCluster) bool {
	if isServerless(c) {
		return false
	}
	return len(c.AWSClientInformation.Nodes) == 0
}

// scanIncomplete reports whether the source scan is too incomplete to
// ship a sizing / cluster-type / networking verdict for this cluster.
// kcp scan clusters always writes a non-nil Topics object on success
// (zero topics is still a valid scan), so `Topics == nil` is the load-
// bearing "scan didn't run" signal. Serverless clusters have a different
// scan path, so this gate doesn't apply to them.
func scanIncomplete(c types.ProcessedCluster) bool {
	if isServerless(c) {
		return false
	}
	return c.KafkaAdminClientInformation.Topics == nil
}
