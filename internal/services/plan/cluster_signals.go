package plan

import (
	"strconv"
	"strings"

	"github.com/confluentinc/kcp/internal/services/report"
	"github.com/confluentinc/kcp/internal/types"

	kafkatypes "github.com/aws/aws-sdk-go-v2/service/kafka/types"
)

// topicsHaveCustomSettings reports, from the scanned per-topic configs, whether
// any topic uses non-default settings — a replication factor other than 3,
// retention over 7 days, a max message size over 2 MB, or a cleanup policy that
// combines compact and delete. Returns nil when no topics were scanned (so the
// plan asks the customer instead).
func topicsHaveCustomSettings(c report.ProcessedCluster) *string {
	t := c.KafkaAdminClientInformation.Topics
	if t == nil || len(t.Details) == 0 {
		return nil
	}
	const (
		defaultRetentionMs int64 = 7 * 24 * 3600 * 1000 // 7 days
		maxMessageCap      int64 = 2 * 1024 * 1024      // 2 MB
	)
	yes := "Yes"
	no := "No"
	for _, td := range t.Details {
		if td.ReplicationFactor != 0 && td.ReplicationFactor != 3 {
			return &yes
		}
		cfg := td.Configurations
		if v := cfg["cleanup.policy"]; v != nil && strings.Contains(*v, "compact") && strings.Contains(*v, "delete") {
			return &yes
		}
		if v := cfg["retention.ms"]; v != nil {
			if ms, err := strconv.ParseInt(strings.TrimSpace(*v), 10, 64); err == nil && (ms < 0 || ms > defaultRetentionMs) {
				return &yes
			}
		}
		if v := cfg["max.message.bytes"]; v != nil {
			if b, err := strconv.ParseInt(strings.TrimSpace(*v), 10, 64); err == nil && b > maxMessageCap {
				return &yes
			}
		}
	}
	return &no
}

// hasLongRetention reports whether any scanned topic keeps history well beyond
// the default local window — infinite retention, or retention.ms past the
// threshold below — which is a real backfill volume even on a non-tiered cluster.
// Returns nil when no topics were scanned (retention unknown).
func hasLongRetention(c report.ProcessedCluster) *string {
	t := c.KafkaAdminClientInformation.Topics
	if t == nil || len(t.Details) == 0 {
		return nil
	}
	const longRetentionMs int64 = 30 * 24 * 3600 * 1000 // 30 days — "well beyond" the 7-day default
	yes, no := "Yes", "No"
	for _, td := range t.Details {
		v := td.Configurations["retention.ms"]
		if v == nil {
			continue
		}
		ms, err := strconv.ParseInt(strings.TrimSpace(*v), 10, 64)
		if err != nil {
			continue
		}
		if ms < 0 || ms > longRetentionMs { // -1 = infinite
			return &yes
		}
	}
	return &no
}

// storedGB returns the cluster's measured retained data (local + tiered), in GB,
// from the scanned storage aggregates. The collector already sums these across
// brokers into a cluster total, so this is the peak retained volume. Returns nil
// when neither metric was scanned (metrics collection wasn't run).
func storedGB(c report.ProcessedCluster) *float64 {
	aggs := c.ClusterMetrics.Aggregates
	local, okL := pickPercentile(aggs, "TotalLocalStorageUsage(GB)", "max")
	remote, okR := pickPercentile(aggs, "TotalRemoteStorageUsage(GB)", "max")
	if !okL && !okR {
		return nil
	}
	total := local + remote
	return &total
}

// Single source of truth for "what do null / empty values mean for this
// cluster?" The profile builder (engine_adapter.go) reads these primitives when
// it translates a scan into an engine Profile, so the SERVERLESS-vs-PROVISIONED
// distinction lives in exactly one place.

// isServerless reports whether the cluster is MSK Serverless. Serverless
// clusters don't have broker nodes and don't expose ACLs through the
// admin API path used by `kcp scan clusters`, so several "looks like an
// incomplete scan" signals are actually expected emptiness.
func isServerless(c report.ProcessedCluster) bool {
	return c.AWSClientInformation.MskClusterConfig.ClusterType == kafkatypes.ClusterTypeServerless
}

// clusterStorageMode returns the cluster's StorageMode enum
// (`LOCAL` / `TIERED` / empty). The profile builder (engine_adapter.go) reads it
// to set the tiered-storage signal on the Profile — the same MskClusterConfig
// pointer-chase as the other Provisioned-only cluster facts, so it lives here
// next to its peers.
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

// Source-auth tokens. Stable strings — the profile builder (engine_adapter.go)
// and the plan renderer map on them to describe how the source authenticates.
// Keep them in sync with the source-auth answer vocabulary.
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
// unrecognised mechanisms — the caller then leaves the source auths
// empty, so the plan asks the source_auth question instead.
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
