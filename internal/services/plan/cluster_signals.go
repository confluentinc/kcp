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
//
// **Known limitation:** the existing scanner persists `Acls = nil` for
// three distinct cases — "admin scan didn't run", "scan ran with 0
// ACLs", and "scan ran with `--skip-acls`". We can't disambiguate from
// the cluster state alone today (the scanner doesn't carry an explicit
// scanned-marker, and the plan layer can't change scanner output
// without breaking backward compatibility with existing state files).
// This helper takes the conservative position that `Acls == nil` means
// "uncertain" → callers should treat the ACL-cap rule as inconclusive
// and surface the `acls_not_scanned` Open Question. The false-positive
// on a legitimate 0-ACL scan is preferable to the false-negative on
// `--skip-acls`, which would recommend Enterprise when Dedicated may
// actually be required. Serverless clusters don't expose ACLs via this
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

// inputsMissing names load-bearing scan signals that weren't available
// when decisions were computed for this cluster. Returned as a stable,
// ordered list of short identifiers so downstream consumers can branch
// (e.g. surface "sizing is best-effort while these are missing"). The
// renderer uses this to mark affected columns provisional rather than
// blanket-deferring the cluster — a verdict driven by a customer-
// declared flag is still valid even when scan signals are missing.
// Serverless clusters are evaluated against a smaller signal set —
// `acls` and `brokers` don't apply there. The `!isServerless(c)` guard
// on the `acls` line is intentional: aclScanRan already returns false
// for serverless, but the explicit guard suppresses the false-positive
// in the surfaced list (we don't want to tell the customer "acls
// missing" for a serverless cluster that never had them).
func inputsMissing(c types.ProcessedCluster) []string {
	var missing []string
	if c.KafkaAdminClientInformation.Topics == nil {
		missing = append(missing, "topics")
	}
	if !aclScanRan(c) && !isServerless(c) {
		missing = append(missing, "acls")
	}
	if brokerInventoryGap(c) {
		missing = append(missing, "brokers")
	}
	return missing
}
