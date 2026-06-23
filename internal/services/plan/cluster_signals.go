package plan

import (
	"github.com/confluentinc/kcp/internal/services/report"
	"github.com/confluentinc/kcp/internal/types"

	kafkatypes "github.com/aws/aws-sdk-go-v2/service/kafka/types"
)

// defaultTargetCloud is the assumed target when `target_cloud` is unset
// in plan-inputs.yaml. MSK is AWS-native, so AWS is the implicit default.
const defaultTargetCloud = "aws"

// targetCloud returns the customer's `target_cloud` plan-input or the
// default ("aws") when unset. Centralizes the empty-string fallback so
// every decision rule reads it the same way.
func targetCloud(inputs PlanInputsResolved) string {
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
func isServerless(c report.ProcessedCluster) bool {
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
func aclScanRan(c report.ProcessedCluster) bool {
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
func brokerInventoryGap(c report.ProcessedCluster) bool {
	if isServerless(c) {
		return false
	}
	return len(c.AWSClientInformation.Nodes) == 0
}

// clusterStorageMode returns the cluster's StorageMode enum
// (`LOCAL` / `TIERED` / empty). Consumed by both the Red Flag
// "tiered_storage_in_use" detector and the Tiered Storage
// per-cluster section — same MskClusterConfig pointer-chase as
// `brokerInstanceType` / `kafkaVersionOf`, so it lives here next to
// its peers.
//
// Serverless clusters always return empty — Provisioned is nil on
// Serverless, and StorageMode is a Provisioned-only concept. Callers
// that iterate clusters MUST also guard with `isServerless` if they
// want to skip Serverless explicitly (recommended: silent fall-through
// is fragile if helper semantics change later).
func clusterStorageMode(c report.ProcessedCluster) kafkatypes.StorageMode {
	prov := c.AWSClientInformation.MskClusterConfig.Provisioned
	if prov == nil {
		return ""
	}
	return prov.StorageMode
}

// knownEnum reports whether `value` is one of `valid` (empty value
// always counts as known — it means "default applies"). Used by enum
// validators across decisions (downtime_tolerance, target_auth_method)
// to surface typos as OQs.
func knownEnum(value string, valid ...string) bool {
	if value == "" {
		return true
	}
	for _, v := range valid {
		if value == v {
			return true
		}
	}
	return false
}

// Source-auth tokens. Stable strings — these appear in the rendered
// Plan (AuthDecision.SourceAuths) and key the auth_mapping table in
// plan-config.yaml. Don't rename without bumping a schema doc.
const (
	SourceAuthSCRAM  = "scram"
	SourceAuthIAM    = "iam"
	SourceAuthMTLS   = "mtls"
	SourceAuthUnauth = "unauth"
)

// DiscoveredClientAuth* mirrors the literal strings that
// `kcp scan client-inventory` writes into `DiscoveredClient.Auth`
// (see `cmd/scan/client_inventory/kafka_trace_line_parser.go`).
//
// **Heads-up:** `types.AuthTypeIAM` in `internal/types/types.go`
// resolves to `"SASL/IAM"` — a DIFFERENT string used elsewhere. Use
// these constants when comparing against the persisted client
// inventory, not the `types.AuthType*` constants.
const (
	DiscoveredClientAuthIAM             = "IAM"
	DiscoveredClientAuthSASLSCRAM       = "SASL_SCRAM"
	DiscoveredClientAuthTLS             = "TLS"
	DiscoveredClientAuthUnauthenticated = "UNAUTHENTICATED"
	DiscoveredClientAuthUnknown         = "UNKNOWN"
)

// sourceAuthsDetected returns the set of auth methods enabled on the
// source MSK cluster, as a deterministic insertion-order list (IAM,
// SCRAM, mTLS, Unauth — never alphabetical). Reads pointers from the
// AWS SDK Cluster struct; a nil pointer or false `Enabled` is treated
// as "off". Serverless clusters expose a smaller ClientAuthentication
// shape — handled in a separate branch.
//
// Fallback: when the MSK ClientAuthentication block is empty (partial
// admin scan), consults `KafkaAdminClientInformation.SaslMechanism`
// from the Kafka Admin probe — a last-resort signal so a discover-side
// gap doesn't silently leave source auths empty.
//
// Multiple auths can be enabled simultaneously; the plan renders all
// detected source auths and never picks one when more than one is on.
func sourceAuthsDetected(c report.ProcessedCluster) []string {
	if isServerless(c) {
		return serverlessSourceAuths(c)
	}
	prov := c.AWSClientInformation.MskClusterConfig.Provisioned
	var out []string
	if prov != nil && prov.ClientAuthentication != nil {
		auth := prov.ClientAuthentication
		if auth.Sasl != nil {
			if auth.Sasl.Iam != nil && auth.Sasl.Iam.Enabled != nil && *auth.Sasl.Iam.Enabled {
				out = append(out, SourceAuthIAM)
			}
			if auth.Sasl.Scram != nil && auth.Sasl.Scram.Enabled != nil && *auth.Sasl.Scram.Enabled {
				out = append(out, SourceAuthSCRAM)
			}
		}
		if auth.Tls != nil && auth.Tls.Enabled != nil && *auth.Tls.Enabled {
			out = append(out, SourceAuthMTLS)
		}
		if auth.Unauthenticated != nil && auth.Unauthenticated.Enabled != nil && *auth.Unauthenticated.Enabled {
			out = append(out, SourceAuthUnauth)
		}
	}
	if len(out) == 0 {
		if fallback := authFromSaslMechanism(c.KafkaAdminClientInformation.SaslMechanism); fallback != "" {
			out = append(out, fallback)
		}
	}
	return out
}

// authFromSaslMechanism maps the Kafka Admin probe's `sasl_mechanism`
// field to a source-auth token. Used as a discover-gap fallback when
// the MSK ClientAuthentication block is empty. Returns "" for
// unrecognised mechanisms — the caller leaves SourceAuths empty so the
// auth_posture_unknown OQ still fires.
func authFromSaslMechanism(mech string) string {
	switch types.NormalizeSaslMechanism(mech) {
	case "SCRAM-SHA-256", "SCRAM-SHA-512":
		return SourceAuthSCRAM
	case "AWS_MSK_IAM":
		return SourceAuthIAM
	case "PLAIN":
		return SourceAuthUnauth
	default:
		return ""
	}
}

func serverlessSourceAuths(c report.ProcessedCluster) []string {
	srv := c.AWSClientInformation.MskClusterConfig.Serverless
	if srv == nil || srv.ClientAuthentication == nil || srv.ClientAuthentication.Sasl == nil {
		return nil
	}
	if iam := srv.ClientAuthentication.Sasl.Iam; iam != nil && iam.Enabled != nil && *iam.Enabled {
		return []string{SourceAuthIAM}
	}
	return nil
}

// fleetUsesIAM reports whether any cluster in the processed fleet has
// IAM enabled on the source side. Drives gateway eligibility —
// IAM clients cannot connect to the CC Gateway and must pre-migrate
// to SCRAM or mTLS first.
func fleetUsesIAM(clusters []report.ProcessedCluster) bool {
	for _, c := range clusters {
		for _, auth := range sourceAuthsDetected(c) {
			if auth == SourceAuthIAM {
				return true
			}
		}
	}
	return false
}

// inputsMissing names load-bearing scan signals that weren't available
// when decisions were computed for this cluster. Returned as a stable,
// ordered list of short identifiers so downstream consumers can branch
// (e.g. surface "sizing is best-effort while these are missing"). The
// renderer uses this to mark affected columns provisional rather than
// blanket-deferring the cluster — a verdict driven by a customer-
// declared flag is still valid even when scan signals are missing.
// Serverless clusters are evaluated against a smaller signal set —
// `acls` and `brokers` don't apply there, so the serverless check
// inlines directly rather than going through aclScanRan (which
// returns false for serverless AND for nil-on-provisioned, an
// ambiguity the caller would have to re-disambiguate).
func inputsMissing(c report.ProcessedCluster) []string {
	var missing []string
	// MskClusterConfig.Provisioned shape gap — affects every
	// Provisioned-only helper (kafkaVersionOf, brokerInstanceType,
	// clusterStorageMode, sourceUsesMTLS). Without surfacing this, the
	// cluster silently flows through with empty/false everywhere and
	// no signal back to the customer.
	if !isServerless(c) && hasUnknownClusterType(c) {
		missing = append(missing, "msk_cluster_config")
	}
	if c.KafkaAdminClientInformation.Topics == nil {
		missing = append(missing, "topics")
	}
	if !isServerless(c) && c.KafkaAdminClientInformation.Acls == nil {
		missing = append(missing, "acls")
	}
	if brokerInventoryGap(c) {
		missing = append(missing, "brokers")
	}
	return missing
}

// hasUnknownClusterType reports whether the cluster's discriminator
// is something other than the two known values (`PROVISIONED` /
// `SERVERLESS`), OR is a Provisioned cluster missing its
// `Provisioned` block entirely. Both cases mean the Provisioned-only
// helpers will return empty/false silently. Callers should treat this
// as an inputs-missing gap and surface a cluster-type OQ.
func hasUnknownClusterType(c report.ProcessedCluster) bool {
	ct := c.AWSClientInformation.MskClusterConfig.ClusterType
	if ct == kafkatypes.ClusterTypeServerless {
		return false
	}
	if ct == kafkatypes.ClusterTypeProvisioned {
		// Provisioned discriminator set but the Provisioned block is
		// missing — mid-flight scan failure or pre-0.7 file shape.
		return c.AWSClientInformation.MskClusterConfig.Provisioned == nil
	}
	// Anything else (empty discriminator, future AWS variant) is
	// unrecognised.
	return true
}
