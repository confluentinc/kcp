package plan

import (
	kafkatypes "github.com/aws/aws-sdk-go-v2/service/kafka/types"

	"github.com/confluentinc/kcp/internal/services/plan/engine"
	"github.com/confluentinc/kcp/internal/services/report"
)

// IntakeInputs are the customer-declared facts the scan cannot derive — the
// required intake answers that become plan-inputs.yaml fields / Open Questions in
// kcp. Everything else on engine.Profile is filled from scanned state (see
// buildProfile).
//
// Field values use the engine's own vocabulary strings (e.g. downtime_tolerance
// is a styleMap key). Empty means "not declared"; the engine applies its own
// default, and the plan layer raises an Open Question where the value is
// required with no safe default. The struct is never (un)marshalled directly —
// plan-inputs.yaml is parsed through the token catalog (see resolveDeclared),
// which populates these fields via each question's set func — so it carries no
// struct tags.
type IntakeInputs struct {
	// Networking requirement — the public/private fork. "Yes" = public is fine.
	PublicEndpointsOK string
	ConnectsToday     string

	// Human assist.
	UseCaseBreadth string

	// Data movement.
	NeedsDataMigration       string
	AnyAppNeedsDataMigration string
	CCEgressRequired         string

	// Switchover.
	DowntimeTolerance        string
	ClientCoordinationBurden string
	EosStreams               []string

	// Adaptive ceiling declarations (default "No").
	ExceedsStandardLimits   string
	ExceedsEnterpriseLimits string

	// Target overrides.
	TargetCloud         string
	TargetIdentityModel []string

	// Schema.
	SchemaStrategy                string
	SourceSRType                  string
	SourceSROutboundReachableToCC string

	// Topics / historical / connectors.
	ConsumerHistoryRequirement string
	ConnectorDestination       string

	// Scan-fact overrides (optional; blank/empty = use the scanned value). These
	// let a customer correct or supply a fact the scan got wrong or missed. Values
	// are engine strings, resolved from tokens by the scan question catalog.
	OvClusterType   string
	OvKafkaVersion  string
	OvSourceAuth    []string
	OvTiered        string // "Yes" | "No"
	OvMSKConnect    string // "Yes" | "No"
	OvSelfManaged   string // "Yes" | "No"
	OvTopicSettings string // "Yes" | "No" — override the scan-derived custom-topic-settings flag
	// Sizing/mechanism facts that the scan usually supplies but a customer answers
	// when it can't: a partition band (when the scan has no exact count) and the
	// inter-broker protocol relative to the 2.8 Cluster Linking floor.
	OvPartitionBand       string
	OvInterBrokerProtocol string // "Yes" (IBP >= 2.8) | "No" (below 2.8)
}

// mergeInputs layers `over` on top of `base` field-by-field: a set field in
// `over` wins, an unset one falls through to `base`. Used to resolve
// cluster-over-defaults (and app-over-cluster) precedence.
func mergeInputs(base, over IntakeInputs) IntakeInputs {
	s := func(a, b string) string {
		if b != "" {
			return b
		}
		return a
	}
	l := func(a, b []string) []string {
		if len(b) > 0 {
			return b
		}
		return a
	}
	return IntakeInputs{
		PublicEndpointsOK:             s(base.PublicEndpointsOK, over.PublicEndpointsOK),
		ConnectsToday:                 s(base.ConnectsToday, over.ConnectsToday),
		UseCaseBreadth:                s(base.UseCaseBreadth, over.UseCaseBreadth),
		NeedsDataMigration:            s(base.NeedsDataMigration, over.NeedsDataMigration),
		AnyAppNeedsDataMigration:      s(base.AnyAppNeedsDataMigration, over.AnyAppNeedsDataMigration),
		CCEgressRequired:              s(base.CCEgressRequired, over.CCEgressRequired),
		DowntimeTolerance:             s(base.DowntimeTolerance, over.DowntimeTolerance),
		ClientCoordinationBurden:      s(base.ClientCoordinationBurden, over.ClientCoordinationBurden),
		EosStreams:                    l(base.EosStreams, over.EosStreams),
		ExceedsStandardLimits:         s(base.ExceedsStandardLimits, over.ExceedsStandardLimits),
		ExceedsEnterpriseLimits:       s(base.ExceedsEnterpriseLimits, over.ExceedsEnterpriseLimits),
		TargetCloud:                   s(base.TargetCloud, over.TargetCloud),
		TargetIdentityModel:           l(base.TargetIdentityModel, over.TargetIdentityModel),
		SchemaStrategy:                s(base.SchemaStrategy, over.SchemaStrategy),
		SourceSRType:                  s(base.SourceSRType, over.SourceSRType),
		SourceSROutboundReachableToCC: s(base.SourceSROutboundReachableToCC, over.SourceSROutboundReachableToCC),
		ConsumerHistoryRequirement:    s(base.ConsumerHistoryRequirement, over.ConsumerHistoryRequirement),
		ConnectorDestination:          s(base.ConnectorDestination, over.ConnectorDestination),
		OvClusterType:                 s(base.OvClusterType, over.OvClusterType),
		OvKafkaVersion:                s(base.OvKafkaVersion, over.OvKafkaVersion),
		OvSourceAuth:                  l(base.OvSourceAuth, over.OvSourceAuth),
		OvTiered:                      s(base.OvTiered, over.OvTiered),
		OvMSKConnect:                  s(base.OvMSKConnect, over.OvMSKConnect),
		OvSelfManaged:                 s(base.OvSelfManaged, over.OvSelfManaged),
		OvTopicSettings:               s(base.OvTopicSettings, over.OvTopicSettings),
		OvPartitionBand:               s(base.OvPartitionBand, over.OvPartitionBand),
		OvInterBrokerProtocol:         s(base.OvInterBrokerProtocol, over.OvInterBrokerProtocol),
	}
}

// Engine source-auth vocabulary (mirrors the engine's input strings). buildProfile
// translates kcp's scan tokens into these.
const (
	engineAuthAWSIAM = "AWS IAM"
	engineAuthSCRAM  = "SASL/SCRAM"
	engineAuthMTLS   = "TLS client certificates (mTLS)"
	engineAuthUnauth = "None / plaintext"

	// engineSRGlue is the engine's Schema Registry value for AWS Glue (mirrors the
	// schema_registry question's "glue" option).
	engineSRGlue = "AWS Glue Schema Registry"
)

// buildProfile maps one scanned cluster plus the customer-declared IntakeInputs onto
// an engine.Profile. Scan-derivable facts (serverless, auth, Kafka version,
// tiered storage, partition count, peak throughput, connectors) come from the
// ProcessedCluster; the rest come from IntakeInputs. Input seeding an interactive
// intake would apply before the engine runs (e.g. Serverless forcing AWS IAM) is
// reproduced here.
func buildProfile(c report.ProcessedCluster, in IntakeInputs, srKind string, scanless bool) engine.Profile {
	p := engine.Profile{
		SourcePlatform:     "Amazon MSK",
		SourcePublicAccess: yesNo(sourcePublicAccess(c)), // drives whether the migration link needs a private egress path
		SourceCloud:        "AWS",
		TargetCloud:        in.TargetCloud, // engine defaults to source/AWS when ""
	}

	// Source cluster type. Serverless is IAM-only; the engine forces AWS IAM, and
	// we seed it too so downstream reads are consistent. A scan-fact override wins.
	serverless := isServerless(c)
	if in.OvClusterType != "" {
		serverless = in.OvClusterType == engine.MSKServerless
	}
	if serverless {
		p.MSKClusterType = engine.MSKServerless
		p.SourceAuthTypes = []string{engineAuthAWSIAM}
	} else {
		p.MSKClusterType = engine.MSKProvisioned
		p.SourceAuthTypes = translateSourceAuths(sourceAuthsDetected(c))
	}
	if len(in.OvSourceAuth) > 0 {
		p.SourceAuthTypes = in.OvSourceAuth
		p.AuthAnswered = true
	}
	// The source cluster type (provisioned vs Serverless) is scan-derived; answered
	// when overridden, or always in scanless mode.
	p.SourceClusterTypeAnswered = scanless || in.OvClusterType != ""

	// Sizing anchor: the exact partition count from the topic scan drives the band
	// directly (no banded question). Peak throughput from CloudWatch aggregates
	// can raise the band. A dimension the scan actually measured is "reviewed": it's
	// real data the customer stands behind, not an assumption, so it can trigger the
	// unusual-workload-shape handoff when it lands a band above the partition anchor.
	partitionsScanned := userPartitionsOf(c) > 0
	if parts := userPartitionsOf(c); parts > 0 {
		p.PartitionsExact = fptr(float64(parts))
		p.SizingReviewed = append(p.SizingReviewed, "partitions_exact")
	}
	// When the scan captured no partition count, the customer supplies a band.
	if in.OvPartitionBand != "" {
		p.PartitionBand = in.OvPartitionBand
	}
	// The sizing figure is answered when it came from a band rather than a scanned
	// count (and always in scanless mode).
	p.PartitionsAnswered = scanless || !partitionsScanned
	if in, ok := peakMBps(c, "BytesInPerSec"); ok {
		p.PeakIngressMbps = fptr(in)
		p.SizingReviewed = append(p.SizingReviewed, "peak_ingress_mbps")
	}
	if out, ok := peakMBps(c, "BytesOutPerSec"); ok {
		p.PeakEgressMbps = fptr(out)
		p.SizingReviewed = append(p.SizingReviewed, "peak_egress_mbps")
	}

	// Source Kafka version -> the engine's Cluster-Linking-floor buckets. Empty when
	// the scan has no version, so the plan asks for it (kafka_version).
	kafkaScanned := kafkaVersionOf(c) != ""
	p.KafkaVersion = bucketKafkaVersion(kafkaVersionOf(c))
	if in.OvKafkaVersion != "" {
		p.KafkaVersion = in.OvKafkaVersion
	}
	// Answered when overridden, when the scan carried no version (a supplied answer),
	// or always in scanless mode.
	p.KafkaVersionAnswered = scanless || in.OvKafkaVersion != "" || !kafkaScanned

	// Inter-broker protocol relative to the 2.8 Cluster Linking floor: derived from
	// the scanned MSK configuration when available, else supplied by the customer
	// (only asked on the 2.4-2.9 band, where it changes the migration mechanism).
	if c.SourceInterBrokerProtocol != "" {
		p.InterBrokerProtocol = c.SourceInterBrokerProtocol
	}
	if in.OvInterBrokerProtocol != "" {
		p.InterBrokerProtocol = in.OvInterBrokerProtocol
	}

	// Tiered storage (only meaningful on Provisioned).
	tieredScanned := false
	if !serverless {
		if clusterStorageMode(c) == kafkatypes.StorageModeTiered {
			p.StorageMode = sp("Yes")
			tieredScanned = true
		} else if clusterStorageMode(c) != "" {
			p.StorageMode = sp("No")
			tieredScanned = true
		}
	}
	if in.OvTiered != "" {
		p.StorageMode = sp(in.OvTiered)
	}
	// Answered when overridden, or when the value the plan uses came from an answer
	// rather than the scan (tiered not scanned but supplied).
	p.TieredAnswered = in.OvTiered != "" || (p.StorageMode != nil && !tieredScanned)

	// Connectors — scanned presence answers the "do you use ..." questions.
	connScanned := mskConnectScanned(c) || selfManagedScanned(c)
	if mskConnectScanned(c) {
		p.MSKConnectPresent = sp(yesNo(len(c.AWSClientInformation.Connectors) > 0))
	}
	if selfManagedScanned(c) {
		p.SelfManagedConnectors = sp(yesNo(len(c.KafkaAdminClientInformation.ConnectClusters) > 0))
	}
	if in.OvMSKConnect != "" {
		p.MSKConnectPresent = sp(in.OvMSKConnect)
	}
	if in.OvSelfManaged != "" {
		p.SelfManagedConnectors = sp(in.OvSelfManaged)
	}
	p.ConnectorsAnswered = in.OvMSKConnect != "" || in.OvSelfManaged != "" || !connScanned
	p.ConnectorDestination = in.ConnectorDestination

	// Declared facts (the non-derivable required set + overrides).
	p.PublicEndpointsOK = in.PublicEndpointsOK
	p.ConnectsToday = in.ConnectsToday
	p.UseCaseBreadth = in.UseCaseBreadth
	p.NeedsDataMigration = in.NeedsDataMigration
	// The infra-level "will any app move data" defaults to this cluster's own answer;
	// when apps are declared, engine_plan.go derives both from the app union so the
	// shared-infra egress always matches what the applications actually need.
	p.AnyAppNeedsDataMigration = in.AnyAppNeedsDataMigration
	if p.AnyAppNeedsDataMigration == "" {
		p.AnyAppNeedsDataMigration = in.NeedsDataMigration
	}
	p.CCEgressRequired = in.CCEgressRequired
	p.DowntimeTolerance = in.DowntimeTolerance
	p.ClientCoordinationBurden = in.ClientCoordinationBurden
	p.EosStreams = in.EosStreams
	p.ExceedsStandardLimits = in.ExceedsStandardLimits
	p.ExceedsEnterpriseLimits = in.ExceedsEnterpriseLimits
	p.TargetIdentityModel = in.TargetIdentityModel
	p.SchemaStrategy = in.SchemaStrategy

	// Schema Registry — derived from the scan when it ran: Glue is fully known;
	// Confluent's kind is known but its edition (Enterprise 7.1+ vs Community) is
	// not, so only the edition is asked. A declared answer (full question when
	// nothing was detected, or the edition question) overrides.
	switch srKind {
	case "glue":
		p.SourceSRType = engineSRGlue
		p.SourceSRDetected = "glue"
	case "confluent":
		p.SourceSRDetected = "confluent"
	}
	if in.SourceSRType != "" {
		p.SourceSRType = in.SourceSRType
	}
	// Glue is the only Schema Registry kind the scan fully knows; a Confluent kind
	// still needs its edition answered, and an undetected registry is answered
	// outright — so the SR type reads as an answer unless it was scanned as Glue.
	p.SchemaAnswered = scanless || in.SourceSRType != "" || srKind != "glue"
	p.SourceSROutboundReachableToCC = in.SourceSROutboundReachableToCC
	// Custom topic settings — derived from the scanned per-topic configs (RF, cleanup
	// policy, retention, max message size). nil when no topics were scanned, so the
	// plan surfaces it as a question instead. A customer override wins.
	topicsScanned := false
	if flag := topicsHaveCustomSettings(c); flag != nil {
		p.TopicRemediationFlag = flag
		topicsScanned = true
	}
	if in.OvTopicSettings != "" {
		p.TopicRemediationFlag = sp(in.OvTopicSettings)
	}
	p.TopicsAnswered = in.OvTopicSettings != "" || (p.TopicRemediationFlag != nil && !topicsScanned)
	// Backfill volume — the measured retained data (local + tiered) is the primary
	// signal; long/infinite retention is a coarser fallback when metrics weren't scanned.
	p.RetainedDataGB = storedGB(c)
	p.LongRetention = hasLongRetention(c)
	p.ConsumerHistoryRequirement = in.ConsumerHistoryRequirement

	return p
}

// translateSourceAuths maps kcp's scan tokens (scram/iam/mtls/unauth) onto the
// engine's source-auth vocabulary.
func translateSourceAuths(kcpTokens []string) []string {
	var out []string
	for _, tok := range kcpTokens {
		switch tok {
		case SourceAuthIAM:
			out = append(out, engineAuthAWSIAM)
		case SourceAuthSCRAM:
			out = append(out, engineAuthSCRAM)
		case SourceAuthMTLS:
			out = append(out, engineAuthMTLS)
		case SourceAuthUnauth:
			out = append(out, engineAuthUnauth)
		}
	}
	return out
}

// engineAuthToKCP maps the engine's source-auth vocabulary back onto kcp's scan
// tokens (scram/iam/mtls/unauth), the inverse of translateSourceAuths. Used to
// carry the effective (post-answer) source auth to the renderer, which branches on
// kcp tokens.
func engineAuthToKCP(engineAuths []string) []string {
	var out []string
	for _, a := range engineAuths {
		switch a {
		case engineAuthAWSIAM:
			out = append(out, SourceAuthIAM)
		case engineAuthSCRAM:
			out = append(out, SourceAuthSCRAM)
		case engineAuthMTLS:
			out = append(out, SourceAuthMTLS)
		case engineAuthUnauth:
			out = append(out, SourceAuthUnauth)
		}
	}
	return out
}

// bucketKafkaVersion maps a concrete source version ("3.5.1") to the engine's
// Cluster-Linking-floor buckets. An unknown/empty version reads as the safe
// default ("3.0 or newer") — the engine only blocks CL below the floor.
func bucketKafkaVersion(v string) string {
	switch {
	case v == "":
		return "" // no scanned version -> the plan asks (kafka_version)
	case versionAtLeast(v, "3.0"):
		return "3.0 or newer"
	case versionAtLeast(v, "2.4"):
		return "2.4-2.9"
	default:
		return "Older than 2.4"
	}
}

// peakMBps reads a CloudWatch max aggregate (bytes/sec) and converts to MBps.
func peakMBps(c report.ProcessedCluster, metric string) (float64, bool) {
	bytes, ok := pickPercentile(c.ClusterMetrics.Aggregates, metric, "max")
	if !ok {
		return 0, false
	}
	return bytes / bytesPerMBps, true
}

// mskConnectScanned / selfManagedScanned report whether the connector scan ran
// (so nil vs. empty can be distinguished for the engine's asked/not-asked split).
func mskConnectScanned(c report.ProcessedCluster) bool {
	return c.AWSClientInformation.Connectors != nil
}
func selfManagedScanned(c report.ProcessedCluster) bool {
	return c.KafkaAdminClientInformation.ConnectClusters != nil
}

func sp(s string) *string     { return &s }
func fptr(v float64) *float64 { return &v }

func yesNo(b bool) string {
	if b {
		return "Yes"
	}
	return "No"
}
