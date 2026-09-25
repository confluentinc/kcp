package engine

// Profile is the engine's sole input: a plain, flat description of one source
// cluster's migration-relevant facts. Every decision reads Profile and nothing
// else.
//
// Field conventions:
//   - Raw numeric answers are *float64 / *int: nil means "not answered", distinct
//     from a zero value. Use the f helper to set one.
//   - Categorical/enum answers are strings: "" means "not answered".
//
// In kcp these fields are populated by buildProfile() from scanned state
// (raw numbers, auth, serverless), plan-inputs.yaml (declared requirements), and
// defaults — see the outer plan package. The struct grows one module at a time;
// today it carries what sizingBand reads.
type Profile struct {
	// Source cluster.
	SourceType         SourceType // MSK (default when "") | ApacheKafka | ConfluentPlatform
	SourcePlatform     string     // e.g. "Amazon MSK", "Apache Kafka", "Confluent Platform"
	MSKClusterType     string     // "Provisioned" | "Serverless" | "" (MSK only)
	SourcePublicAccess string     // "Yes" (source exposes public broker endpoints) | "No" | "" (unknown; treated as private)
	SourceCloud        string
	TargetCloud        string // "AWS" | "Azure" | "GCP" | ""; MSK defaults to AWS

	// Scanless is true for a questionnaire-only run (no --state-file). It lets the
	// source-selection question render even before a source platform is chosen, so a
	// scanless user can pick Apache Kafka / Confluent Platform (a scan run knows the
	// source already). Same scanless signal the *Answered flags below are built from.
	Scanless bool

	// Sizing — raw numbers (preferred; kcp fills these from scanned metrics and
	// topic counts) OR banded labels (a declared/plan-inputs answer).
	PartitionsExact    *float64
	PeakIngressMbps    *float64
	PeakEgressMbps     *float64
	PeakRequestsPerSec *float64

	PartitionBand   string // e.g. "2,500–30,000"
	IngressBand     string
	EgressBand      string
	RequestRateBand string

	// Exact secondary limits, enforced by tierFit only when a figure is given
	// (no intake sets these today, but the ceilings are real).
	ACLCountExact          *float64
	ClientConnectionsExact *float64

	// Networking requirement — the public/private fork. public_endpoints_ok is
	// read first; requires_private and the legacy source_accessibility remain
	// supported. All default to private (MSK is private-first).
	PublicEndpointsOK    string // "Yes" | "No" | ""
	RequiresPrivateField string // "Yes" | "No" | ""  (legacy requires_private)
	SourceAccessibility  string // "Private" | "Public" | ""  (legacy)

	// Auth.
	SourceAuthTypes     []string // e.g. ["SASL/SCRAM"], [authMTLS]; Serverless is forced to [authAWSIAM]
	TargetIdentityModel []string // multi-select; "mTLS" as a target gates the tier

	// Adaptive ceiling declarations. exceeds_standard_limits is read only on
	// Band 1, exceeds_enterprise_limits only on Band 2. "No"/unanswered are
	// equivalent (No is the pre-selected default).
	ExceedsStandardLimits   string // "Yes" | "No" | ""
	ExceedsEnterpriseLimits string // "Yes" | "No" | ""

	// Networking — how the customer connects today, and outbound reachability.
	ConnectsToday    string // "Same VPC" | "Peered" | "PrivateLink" | "Other" | ""
	SourceTopology   string // legacy; normalised into ConnectsToday
	CCEgressRequired string // "Yes" | "" — Confluent-managed connectors/consumers reach into the customer network

	// Data migration — shapes the switchover mechanism (and, via a Cluster Link,
	// the egress endpoint). needs_data_migration defaults to "needs data"
	// (anything but an explicit "No"). any_app_needs_data_migration is the
	// fleet/infra-level equivalent; "" means unset (falls back to
	// needs_data_migration).
	NeedsDataMigration       string // "No" | "Yes" | ""
	AnyAppNeedsDataMigration string // "No" | "Yes" | ""

	// Source Kafka version facts — the Cluster Linking floor.
	KafkaVersion        string // "Older than 2.4" | "2.4-2.9" | "3.0 or newer" | ""
	InterBrokerProtocol string // "No" means IBP < 2.8 (blocks CL on 2.4-2.9)

	// Switchover — cutover style and its escalation inputs.
	DowntimeTolerance        string   // a styleMap key; required with no default (holds the switchover verdict)
	ClientCoordinationBurden string   // "Hard (many clients and teams)" escalates to Gateway-mediated
	EosStreams               []string // exactly-once / Kafka Streams state that Cluster Linking can't carry

	// Sizing review — which assumed dimensions the customer actually stood behind.
	SizingReviewed []string // field names, e.g. "peak_ingress_mbps"

	// Human assist.
	UseCaseBreadth string // "Shared fabric across many apps and teams" hands off

	// Schema (Block 6).
	SourceSRType                  string // a sourceSR value
	SchemaStrategy                string // a schemaStrategy value
	SourceSROutboundReachableToCC string // "Yes" | "No" | ""
	// SourceSRDetected is the scan-detected SR kind ("glue" | "confluent" | ""),
	// used only to gate which schema question is asked (decisions read SourceSRType).
	SourceSRDetected string
	TargetIsGovCloud string // "Yes" | "" (or set via a future bool signal)

	// Connectors (Block 7). Pointers to distinguish unanswered (nil) from "No".
	MSKConnectPresent     *string // "Yes" | "No" | nil
	SelfManagedConnectors *string // "Yes" | "No" | nil
	ConnectorDestination  string  // "Move to Confluent-managed" | "Keep self-managed" | ""

	// Topics (Block 8) & historical data (Block 9).
	StorageMode                *string // "Yes" (tiered) | "No" | nil
	TopicRemediationFlag       *string // "Yes" | "No" | nil
	ConsumerHistoryRequirement string  // "Required" | "Not required" | ""
	// LongRetention flags history kept well beyond the default local window (long
	// or infinite retention.ms), which — like tiered storage — is a real backfill
	// volume even when the cluster isn't tiered. "Yes" | "No" | nil (not scanned).
	LongRetention *string
	// RetainedDataGB is the measured retained data (local + tiered), in GB, from
	// scanned storage metrics. The direct backfill-volume signal; nil when metrics
	// weren't scanned (fall back to StorageMode / LongRetention).
	RetainedDataGB *float64

	// Provenance of the overridable scan facts: true when the value came from a
	// customer answer (an override, or a value the scan never captured) rather than
	// the scan itself, so a verdict's "Based on your scan / your answer" lead reads
	// correctly whichever way the value arrived.
	AuthAnswered       bool
	TieredAnswered     bool
	ConnectorsAnswered bool
	TopicsAnswered     bool
	// The same provenance signal for the remaining overridable scan facts: the
	// partition count / sizing band, the Schema Registry type, the source Kafka
	// version (the Cluster-Linking floor), and the source cluster type
	// (provisioned vs Serverless). True when the value came from an answer rather
	// than the scan — in pure scanless mode every one of these is answered.
	PartitionsAnswered        bool
	SchemaAnswered            bool
	KafkaVersionAnswered      bool
	SourceClusterTypeAnswered bool
}

// f returns a *float64 for a literal — a small helper so tests and buildProfile
// read cleanly (fp := f(42)).
func f(v float64) *float64 { return &v }

// isMSK reports whether the source is Amazon MSK. The zero-value SourceType is
// treated as MSK so a profile that never sets the axis behaves as it always did.
func (p Profile) isMSK() bool {
	return p.SourceType == "" || p.SourceType == SourceMSK
}

// isOSKorCP reports whether the source is self-managed Apache Kafka or Confluent
// Platform. These share every behaviour and every verdict; only display copy
// differs (Apache Kafka vs Confluent Platform, plus the two CP-only notes gated
// by isCP).
func (p Profile) isOSKorCP() bool {
	return p.SourceType == SourceApacheKafka || p.SourceType == SourceConfluentPlatform
}

// isCP reports whether the source is a self-managed Confluent Platform cluster.
// CP behaves like Apache Kafka for every verdict; this only gates copy that is
// specifically true of Confluent Platform (it already ships Replicator and
// Confluent connectors).
func (p Profile) isCP() bool {
	return p.SourceType == SourceConfluentPlatform
}

// isServerless reports whether the source is MSK Serverless. Serverless is
// IAM-only and capped below Band 1 on every published quota, which the sizing
// and auth logic special-case. Serverless exists only on MSK, so this implies
// isMSK — a non-MSK source is never Serverless whatever its cluster-type field.
func (p Profile) isServerless() bool {
	return p.isMSK() && p.MSKClusterType == MSKServerless
}
