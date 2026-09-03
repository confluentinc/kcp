package manifest

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/confluentinc/kcp/internal/targets"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/confluentinc/kcp/internal/yamlsafe"
	"github.com/goccy/go-yaml"
)

// KindGatewayMigration is the gateway-orchestrated cutover manifest driving
// `kcp migration`. It shares apiVersion, sub-types and parser with
// KindMigration and is discriminated only by kind.
const KindGatewayMigration = "GatewayMigration"

// minDetectUnroutedProducersDuration mirrors the retired flag's documented
// minimum: a shorter window cannot span a producer's metadata refresh, so a
// clean result would mean "we did not look long enough", not "nothing found".
const minDetectUnroutedProducersDuration = 10 * time.Second

// GatewayMigration is the declarative form of a `kcp migration` run: the
// topology, the cluster link, the gateway CRs, and the execute-time policy that
// today are spread across 64 flags on four commands.
type GatewayMigration struct {
	APIVersion string      `yaml:"apiVersion" json:"apiVersion"`
	Kind       string      `yaml:"kind" json:"kind"`
	Metadata   Metadata    `yaml:"metadata" json:"metadata"`
	Spec       GatewaySpec `yaml:"spec" json:"spec"`

	// Interpolate opts this file in to ${ENV_VAR} resolution. Top level rather
	// than under spec because credentials files need the same key and have no
	// envelope, and because a --interpolate flag could not express "this file
	// yes, that referenced file no".
	//
	// What the opt-in defends against is ACCIDENTAL expansion — a secret that
	// legitimately contains "${" staying literal, and an already-shipped
	// credentials file being read exactly as before. It is not a defence
	// against a hostile manifest: the opt-in lives in the file itself, so a
	// manifest is a trust boundary equal to a shell script.
	Interpolate bool `yaml:"interpolate,omitempty" json:"interpolate,omitempty"`
}

type GatewaySpec struct {
	Source      Source             `yaml:"source" json:"source"`
	Target      GatewayTarget      `yaml:"target" json:"target"`
	ClusterLink GatewayClusterLink `yaml:"clusterLink" json:"clusterLink"`
	Gateway     Gateway            `yaml:"gateway" json:"gateway"`
	// Topics is a flat list of LITERAL topic names, exact-matched against the
	// link's active mirror topics — not globs. A pointer so that omitted ("every
	// active mirror topic") stays distinguishable from an explicitly empty list,
	// which means the opposite and is rejected.
	Topics *[]string `yaml:"topics,omitempty" json:"topics,omitempty"`
	// TopicGroup is the new unified shape that replaces both spec.topics and
	// gateway.routes: each entry pairs a topic selection (literal names and/or
	// anchored regex patterns) with the route it migrates and the target
	// streaming domain that route switches to. Exactly one entry is supported
	// today (one route, one mode per migration). The bootstrap server id is NOT
	// carried here — it is derived from the live CR at init (D1) — and there is
	// no mode field: mode is resolved from the CR's route-scoped binding (D5).
	TopicGroup []TopicGroupEntry `yaml:"topicGroup" json:"topicGroup"`
	// DefaultPolicies is read fresh on every execute and never snapshotted, which
	// is what lets a caller vary execute-time policy between init and execute.
	// Each field is a DEFAULT: `kcp migration execute` exposes a per-policy flag
	// (e.g. --detect-unrouted-producers-duration) that overrides the value here
	// for a single run.
	DefaultPolicies DefaultPolicies `yaml:"defaultPolicies,omitempty" json:"defaultPolicies,omitempty"`
}

// GatewayTarget is the destination envelope. It is deliberately NOT
// manifest.Target: that type carries clusterCredentials, cloudCredentials,
// schemaRegistry and connect, none of which this kind defines, and a strict
// parser that accepted them would make the manifest a superset of its spec.
// TargetKafka itself IS shared, extended additively.
type GatewayTarget struct {
	Type string `yaml:"type" json:"type"`
	// ClusterID is required for BOTH target types here. kind: Migration forbids
	// it on confluent-platform because kcp migrate always discovers it; this
	// kind never discovers it, and pinning the id an operator already knows is
	// the safer default ahead of an irreversible cutover.
	ClusterID string       `yaml:"clusterId" json:"clusterId"`
	Kafka     *TargetKafka `yaml:"kafka" json:"kafka"`
}

type GatewayClusterLink struct {
	// Name identifies an ALREADY EXISTING cluster link; this kind never creates one.
	Name                    string `yaml:"name" json:"name"`
	PauseConsumerOffsetSync bool   `yaml:"pauseConsumerOffsetSync,omitempty" json:"pauseConsumerOffsetSync,omitempty"`
}

// TopicGroupEntry pairs a topic selection with the route it migrates and the
// target streaming domain that route switches to. The field set is identical
// for both migration modes (static all-at-once and dynamic topic-based); mode
// only changes whether the id is derived (static) and how patterns expand.
//
// Topics and TopicPatterns are pointers so nil (omitted) stays distinct from
// [] (present but empty, rejected), matching the old spec.topics semantics. At
// least one of the two is required (D2). No bootstrapServerId — kcp derives it
// from the live CR at init (D1). No mode — resolved from the CR (D5).
type TopicGroupEntry struct {
	Topics                *[]string `yaml:"topics,omitempty" json:"topics,omitempty"`
	TopicPatterns         *[]string `yaml:"topicPatterns,omitempty" json:"topicPatterns,omitempty"`
	Route                 string    `yaml:"route" json:"route"`
	TargetStreamingDomain string    `yaml:"targetStreamingDomain" json:"targetStreamingDomain"`
}

type Gateway struct {
	Namespace string `yaml:"namespace" json:"namespace"`
	// Kubeconfig is the one field in the manifest where a leading ~/ is
	// expanded — nothing else in the repo expands ~, and client-go's loader
	// does not either.
	Kubeconfig string `yaml:"kubeconfig,omitempty" json:"kubeconfig,omitempty"`
	// CrName is the Kubernetes object NAME of the initial gateway CR, read live
	// from the cluster at init. It flattens the old crs.initial nesting (O2).
	CrName string     `yaml:"cr-name" json:"cr-name"`
	CRs    GatewayCRs `yaml:"crs" json:"crs"`
	// Routes names the route(s) kcp fences and switches over at cutover, each
	// paired with the streaming domain it switches to. There is no fenced-CR
	// or switchover-CR file: kcp reads the live initial CR, injects the fence
	// block onto each named route to block traffic, and — derived the same
	// way from the same live CR — later flips each route's streamingDomain to
	// its declared target. Fence and its rollback (which re-applies the same
	// initial CR) are therefore exact inverses, and there is no second list
	// to keep in sync with this one (D1).
	Routes []GatewayRoute `yaml:"routes" json:"routes"`
}

// GatewayCRs holds the gateway CR the migration reads live, plus a detector
// for the retired switchover-file field.
type GatewayCRs struct {
	// Initial is a Kubernetes object NAME, read live from the cluster at init.
	Initial string `yaml:"initial" json:"initial"`
	// Switchover is retired (D2): the switchover CR is now a derived inline
	// update to the initial CR (see Gateway.Routes), not an operator-authored
	// file. This field stays as a plain string ONLY so Validate can detect a
	// manifest that still sets it and reject with a migration hint — goccy
	// silently drops unknown keys, so removing the field outright would turn a
	// stale manifest into a silent no-op instead of a loud, actionable error.
	// Nothing else reads it.
	Switchover string `yaml:"switchover,omitempty" json:"switchover,omitempty"`
}

// GatewayRoute is one route kcp fences and switches over at cutover, paired
// with the streaming domain it switches to. The pairing is structural (D4): a
// route cannot be named here without also declaring where it switches,
// because the FSM only supports init -> fence -> switch, with no
// fence-without-switch path. Each entry must have a non-blank, unique route
// name that exists in the initial CR, and a non-blank streaming domain
// target.
//
// Because the route's security.cluster already carries pre-staged
// ("redundant") auth for the target domain, the switch is a plain field flip
// with no secret or auth change (see checkRedundantAuthStaged, which proves
// that staging holds before any route is fenced).
type GatewayRoute struct {
	Name            string                    `yaml:"name" json:"name"`
	StreamingDomain GatewayStreamingDomainRef `yaml:"streamingDomain" json:"streamingDomain"`
}

// GatewayStreamingDomainRef names a streaming domain declared in the gateway
// CR's spec.streamingDomains, and the bootstrap server id on that domain to
// bind the route to.
type GatewayStreamingDomainRef struct {
	Name              string `yaml:"name" json:"name"`
	BootstrapServerId string `yaml:"bootstrapServerId" json:"bootstrapServerId"`
}

// RouteNames projects the gateway's routes to their plain names, the shape the
// rest of kcp (state snapshots, drift detection) has always worked with.
func (g Gateway) RouteNames() []string {
	names := make([]string, len(g.Routes))
	for i, r := range g.Routes {
		names[i] = r.Name
	}
	return names
}

// DefaultPolicies is the execute-time knobs. Every value is optional and zero
// carries meaning: 0 promotes all at once / imposes no deadline / skips the
// check. Each field is a default that `kcp migration execute` can override with
// a per-policy flag for a single run.
type DefaultPolicies struct {
	LagThreshold     int `yaml:"lagThreshold,omitempty" json:"lagThreshold,omitempty"`
	PromoteBatchSize int `yaml:"promoteBatchSize,omitempty" json:"promoteBatchSize,omitempty"`
	// RolloutTimeout bounds the gateway rollout. 0 means no deadline.
	RolloutTimeout time.Duration `yaml:"rolloutTimeout,omitempty" json:"rolloutTimeout,omitempty"`
	// DetectUnroutedProducersDuration opts in to the pre-switchover unrouted
	// producer check; a detected increase aborts before switchover. 0 SKIPS the
	// check entirely; when set the minimum is 10s.
	DetectUnroutedProducersDuration time.Duration `yaml:"detectUnroutedProducersDuration,omitempty" json:"detectUnroutedProducersDuration,omitempty"`
	// ConsumerOffsetSyncDrainDuration waits after disabling consumer offset
	// sync. 0 means no wait; it has no effect unless pauseConsumerOffsetSync.
	ConsumerOffsetSyncDrainDuration time.Duration `yaml:"consumerOffsetSyncDrainDuration,omitempty" json:"consumerOffsetSyncDrainDuration,omitempty"`
	// HotReloadTimeout bounds the per-pod configId verification used when the
	// gateway supports hot-reload. 0 uses gateway.DefaultHotReloadTimeout; unlike
	// RolloutTimeout this is never unbounded, since a hot-reload moves no
	// Kubernetes signal to wait on.
	HotReloadTimeout time.Duration `yaml:"hotReloadTimeout,omitempty" json:"hotReloadTimeout,omitempty"`
	// GatewayConfigPort is the port serving the gateway's /config endpoint,
	// polled per pod to confirm a config revision was applied. 0 uses the
	// persisted value, falling back to the gateway default (9180).
	GatewayConfigPort int `yaml:"gatewayConfigPort,omitempty" json:"gatewayConfigPort,omitempty"`
}

// envelope is the minimum needed to discriminate one kind from another. It is
// deliberately NOT strict: the body belongs to whichever kind claims it, and a
// wrong-kind file must yield "this is a kcp migrate manifest" rather than a
// field-by-field unknown-key dump.
type envelope struct {
	APIVersion string `yaml:"apiVersion"`
	Kind       string `yaml:"kind"`
}

// ParseKind peeks at the envelope and returns the manifest's kind. It is the
// dispatch point that keeps a file from reaching the wrong command.
func ParseKind(data []byte) (string, error) {
	var e envelope
	if err := yaml.Unmarshal(data, &e); err != nil {
		// This decodes the ENTIRE manifest, which is secret-bearing when
		// credentials are inline, and it runs before the strict decode — so a
		// YAML *syntax* error (an indentation slip, say) is caught here and
		// never reaches the sites downstream. Its excerpt window is wider than
		// a strict error's, because syntax errors carry more context.
		return "", fmt.Errorf("reading manifest envelope: %w", yamlsafe.StripSourceExcerpt(err))
	}
	if strings.TrimSpace(e.Kind) == "" {
		return "", fmt.Errorf("manifest has no kind: (expected %q or %q)", KindGatewayMigration, KindMigration)
	}
	return e.Kind, nil
}

// wrongKindError explains a kind mismatch in terms of which command wants which
// file, which is the only thing the operator can act on.
func wrongKindError(got, want string) error {
	switch got {
	case KindMigration:
		return fmt.Errorf("this is a %q manifest (used by `kcp migrate`), but %q was expected — `kcp migration` reads a %s file",
			KindMigration, want, KindGatewayMigration)
	case KindGatewayMigration:
		return fmt.Errorf("this is a %q manifest (used by `kcp migration`), but %q was expected — `kcp migrate` reads a %s file",
			KindGatewayMigration, want, KindMigration)
	default:
		return fmt.Errorf("unsupported kind %q (expected %q)", got, want)
	}
}

// ParseGatewayMigration decodes a GatewayMigration manifest with strict
// decoding, then resolves ${ENV_VAR} references if the file opted in.
//
// Resolution runs after the parse and before any use of the values, and it does
// NOT reach inline credential blocks: those are held as raw bytes and resolved
// on demand, which keeps every block to exactly one resolution pass.
func ParseGatewayMigration(data []byte) (*GatewayMigration, error) {
	kind, err := ParseKind(data)
	if err != nil {
		return nil, err
	}
	if kind != KindGatewayMigration {
		return nil, wrongKindError(kind, KindGatewayMigration)
	}

	var g GatewayMigration
	if err := parseStrict(data, &g); err != nil {
		return nil, fmt.Errorf("parsing gateway migration manifest: %w", yamlsafe.StripSourceExcerpt(err))
	}
	if g.Interpolate {
		if err := interpolateInto(&g); err != nil {
			return nil, fmt.Errorf("resolving gateway migration manifest: %w", err)
		}
	}
	return &g, nil
}

// Validate performs structural validation and returns ALL problems found, each
// tagged with its field path.
//
// It does no I/O, so a credentials slot spelled as a path is checked for
// presence only; the rules that need the block's contents (source auth gating,
// the destination iam rejection) run here for an inline block and again
// in SourceCredentials / DestinationKafkaCredentials for both spellings.
func (g *GatewayMigration) Validate() []error {
	var errs []error
	add := func(format string, args ...any) {
		errs = append(errs, fmt.Errorf(format, args...))
	}

	if g.APIVersion != SupportedAPIVersion {
		add("apiVersion: must be %q, got %q", SupportedAPIVersion, g.APIVersion)
	}
	if g.Kind != KindGatewayMigration {
		add("kind: must be %q, got %q", KindGatewayMigration, g.Kind)
	}
	if blank(g.Metadata.Name) {
		add("metadata.name: must not be empty")
	}

	// --- source ---
	if err := validateEnum("spec.source.type", g.Spec.Source.Type, SourceMSK, SourceApacheKafka); err != nil {
		errs = append(errs, err)
	}
	errs = append(errs, validateBootstrapServers("spec.source.bootstrapServers", g.Spec.Source.BootstrapServers)...)
	if blankRef(g.Spec.Source.Credentials) {
		add("spec.source.credentials: must not be empty")
	} else if g.Spec.Source.Credentials.IsInline() {
		if mc, ok := peekMigrateCreds(g.Spec.Source.Credentials); ok {
			errs = append(errs, checkSourceAuthAgainstType(mc, g.Spec.Source.Type)...)
		}
	}

	// --- target ---
	switch g.Spec.Target.Type {
	case TargetConfluentCloud, TargetConfluentPlatform:
		if blank(g.Spec.Target.ClusterID) {
			add("spec.target.clusterId: required for target type %q", g.Spec.Target.Type)
		}
	case "":
		add("spec.target.type: must not be empty")
	default:
		add("spec.target.type: unsupported value %q (supported: %s, %s)",
			g.Spec.Target.Type, TargetConfluentCloud, TargetConfluentPlatform)
	}

	if k := g.Spec.Target.Kafka; k == nil {
		add("spec.target.kafka: required")
	} else {
		errs = append(errs, validateBootstrapServers("spec.target.kafka.bootstrapServers", k.BootstrapServers)...)
		if blank(k.RestEndpoint) {
			add("spec.target.kafka.restEndpoint: must not be empty")
		}
		if blankRef(k.Credentials) {
			add("spec.target.kafka.credentials: must not be empty")
		} else if k.Credentials.IsInline() {
			if mc, ok := peekMigrateCreds(k.Credentials); ok {
				errs = append(errs, checkDestinationKafkaAuth(mc)...)
				if k.RestCredentials == nil && mc.SASLPlain == nil {
					add("spec.target.kafka.restCredentials: required — it can only be derived from spec.target.kafka.credentials when that block is sasl_plain")
				}
			}
		}
		if k.RestCredentials != nil {
			if blankRef(*k.RestCredentials) {
				add("spec.target.kafka.restCredentials: present but empty — fill it in, or omit it entirely to derive from credentials (only possible when that block is sasl_plain)")
			}
		}
	}

	// --- cluster link ---
	if blank(g.Spec.ClusterLink.Name) {
		add("spec.clusterLink.name: must not be empty (the link must already exist)")
	}

	// --- gateway ---
	if blank(g.Spec.Gateway.Namespace) {
		add("spec.gateway.namespace: must not be empty")
	}
	if blank(g.Spec.Gateway.CrName) {
		add("spec.gateway.cr-name: must not be empty (a Kubernetes object name, read live)")
	}

	// --- topicGroup ---
	errs = append(errs, validateTopicGroup(g.Spec.TopicGroup)...)

	// --- defaultPolicies ---
	errs = append(errs, g.Spec.DefaultPolicies.Validate()...)

	return errs
}

// validateTopicGroup applies the structural rules for spec.topicGroup: exactly
// one entry, a non-blank route and target streaming domain, and at least one of
// topics/topicPatterns (D2) with each pattern compiling as an anchored RE2
// full-match (O3). It carries no mode knowledge and does no I/O — mode (static
// vs dynamic) and the bootstrap server id are both resolved from the live CR at
// init (D1/D5), not the manifest.
func validateTopicGroup(entries []TopicGroupEntry) []error {
	var errs []error
	add := func(format string, args ...any) {
		errs = append(errs, fmt.Errorf(format, args...))
	}

	if len(entries) != 1 {
		add("spec.topicGroup: must have exactly one entry (got %d)", len(entries))
		if len(entries) == 0 {
			return errs
		}
	}

	e := entries[0]
	if blank(e.Route) {
		add("spec.topicGroup[0].route: must not be blank")
	}
	if blank(e.TargetStreamingDomain) {
		add("spec.topicGroup[0].targetStreamingDomain: must not be blank")
	}
	if e.Topics == nil && e.TopicPatterns == nil {
		add("spec.topicGroup[0]: at least one of topics or topicPatterns is required")
	}
	if e.Topics != nil {
		if len(*e.Topics) == 0 {
			add("spec.topicGroup[0].topics: must not be an empty list")
		}
		for i, name := range *e.Topics {
			if blank(name) {
				add("spec.topicGroup[0].topics[%d]: must not be blank", i)
			}
		}
	}
	if e.TopicPatterns != nil {
		if len(*e.TopicPatterns) == 0 {
			add("spec.topicGroup[0].topicPatterns: must not be an empty list")
		}
		for i, pat := range *e.TopicPatterns {
			if blank(pat) {
				add("spec.topicGroup[0].topicPatterns[%d]: must not be blank", i)
				continue
			}
			if _, err := regexp.Compile(anchoredPattern(pat)); err != nil {
				add("spec.topicGroup[0].topicPatterns[%d]: not a valid regular expression: %v", i, err)
			}
		}
	}
	return errs
}

// anchoredPattern wraps a topicPatterns entry as an anchored RE2 full-match
// (O3): the Gateway matches patterns Java-style (anchored full-match), but Go's
// regexp default is unanchored/partial, so kcp must anchor. \A…\z pins both
// ends and the (?:…) group keeps a top-level alternation from binding only one
// branch. RE2 is linear-time, so there is no ReDoS surface in this compile.
func anchoredPattern(p string) string {
	return `\A(?:` + p + `)\z`
}

// Validate checks the policy block. It is exported because `kcp migration
// execute` re-validates after applying command-line overrides, which can
// introduce a value the manifest itself never carried.
func (p DefaultPolicies) Validate() []error {
	var errs []error
	if p.LagThreshold < 0 {
		errs = append(errs, fmt.Errorf("spec.defaultPolicies.lagThreshold: must not be negative"))
	}
	if p.PromoteBatchSize < 0 {
		errs = append(errs, fmt.Errorf("spec.defaultPolicies.promoteBatchSize: must not be negative (0 promotes all at once)"))
	}
	if p.RolloutTimeout < 0 {
		errs = append(errs, fmt.Errorf("spec.defaultPolicies.rolloutTimeout: must not be negative (0 means no deadline)"))
	}
	if p.ConsumerOffsetSyncDrainDuration < 0 {
		errs = append(errs, fmt.Errorf("spec.defaultPolicies.consumerOffsetSyncDrainDuration: must not be negative (0 means no wait)"))
	}
	if p.DetectUnroutedProducersDuration < 0 {
		errs = append(errs, fmt.Errorf("spec.defaultPolicies.detectUnroutedProducersDuration: must not be negative (0 skips the check)"))
	} else if p.DetectUnroutedProducersDuration > 0 && p.DetectUnroutedProducersDuration < minDetectUnroutedProducersDuration {
		errs = append(errs, fmt.Errorf(
			"spec.defaultPolicies.detectUnroutedProducersDuration: must be at least %s when set (0 skips the check) — a shorter window cannot span a producer's metadata refresh",
			minDetectUnroutedProducersDuration))
	}
	if p.HotReloadTimeout < 0 {
		errs = append(errs, fmt.Errorf("spec.defaultPolicies.hotReloadTimeout: must not be negative (0 uses the built-in default)"))
	}
	if p.GatewayConfigPort < 0 {
		errs = append(errs, fmt.Errorf("spec.defaultPolicies.gatewayConfigPort: must not be negative (0 uses the default port)"))
	}
	return errs
}

// peekMigrateCreds decodes an inline credentials block for validation without
// I/O. A decode failure is not reported here: the same block is decoded again
// with a proper error by SourceCredentials.
func peekMigrateCreds(ref CredentialsRef) (types.MigrateClusterCredentials, bool) {
	var mc types.MigrateClusterCredentials
	if err := yaml.Unmarshal(ref.Inline, &mc); err != nil {
		return mc, false
	}
	return mc, true
}

// checkSourceAuthAgainstType gates iam to an MSK source. Only MSK serves IAM,
// so declaring it against an apache-kafka source is a typo that would otherwise
// surface as an opaque auth failure mid-run.
func checkSourceAuthAgainstType(mc types.MigrateClusterCredentials, sourceType string) []error {
	if mc.IAM != nil && sourceType != SourceMSK {
		return []error{fmt.Errorf(
			"spec.source.credentials.iam: only valid when spec.source.type is %q (got %q)", SourceMSK, sourceType)}
	}
	return nil
}

// checkDestinationKafkaAuth rejects iam on the destination Kafka leg. iam is
// MSK-only (SigV4 token signing against AWS), and the destination is Confluent
// Cloud or Confluent Platform — never MSK — so it would be accepted here and
// then fail opaquely at connection time. Every other method (sasl_plain,
// sasl_scram, mtls, unauthenticated_tls, unauthenticated_plaintext) is honoured
// end-to-end via AdminOptionForAuthMethod, the same mapper the source leg
// already uses — including a custom ca_cert on sasl_plain, now that
// createDestinationOffset routes through the mapper instead of a hardcoded
// empty-CA client.
func checkDestinationKafkaAuth(mc types.MigrateClusterCredentials) []error {
	if mc.IAM != nil {
		return []error{fmt.Errorf(
			"spec.target.kafka.credentials.iam: not supported for the destination (the destination is Confluent Cloud/Platform, never MSK)")}
	}
	return nil
}

// SourceCredentials resolves the source Kafka leg.
func (g *GatewayMigration) SourceCredentials() (types.MigrateClusterCredentials, []error) {
	mc, errs := g.Spec.Source.Credentials.ResolveMigrateCluster(g.Interpolate)
	if len(errs) > 0 {
		return mc, errs
	}
	if errs := checkSourceAuthAgainstType(mc, g.Spec.Source.Type); len(errs) > 0 {
		return mc, errs
	}
	return mc, nil
}

// DestinationKafkaCredentials resolves the destination Kafka leg. Any auth
// method is accepted except iam — the destination is Confluent Cloud/Platform,
// never MSK.
func (g *GatewayMigration) DestinationKafkaCredentials() (types.MigrateClusterCredentials, []error) {
	if g.Spec.Target.Kafka == nil {
		return types.MigrateClusterCredentials{}, []error{fmt.Errorf("spec.target.kafka: required")}
	}
	mc, errs := g.Spec.Target.Kafka.Credentials.ResolveMigrateCluster(g.Interpolate)
	if len(errs) > 0 {
		return mc, errs
	}
	if errs := checkDestinationKafkaAuth(mc); len(errs) > 0 {
		return mc, errs
	}
	return mc, nil
}

// RestCredentials resolves the destination REST leg.
//
// When spec.target.kafka.restCredentials is omitted AND the Kafka leg is
// sasl_plain, it is DERIVED, in full, from that leg: one flag pair feeds both
// destination legs today, so requiring both blocks would make the operator
// type the same secret twice for the overwhelmingly common case. Derivation
// is full-or-nothing — a block that is present is used exactly as written,
// because a block that reads as complete while silently acquiring fields from
// elsewhere is worse than either. For every other Kafka auth method there is
// no principal to derive a REST credential from, so restCredentials becomes
// required.
func (g *GatewayMigration) RestCredentials() (*targets.Credentials, error) {
	if g.Spec.Target.Kafka == nil {
		return nil, fmt.Errorf("spec.target.kafka: required")
	}
	if ref := g.Spec.Target.Kafka.RestCredentials; ref != nil {
		return ref.ResolveTarget(g.Interpolate)
	}

	mc, errs := g.DestinationKafkaCredentials()
	if len(errs) > 0 {
		return nil, fmt.Errorf("deriving spec.target.kafka.restCredentials from credentials: %w", errs[0])
	}
	if mc.SASLPlain == nil {
		return nil, fmt.Errorf("spec.target.kafka.restCredentials: required — it can only be derived from spec.target.kafka.credentials when that block is sasl_plain")
	}
	derived := &targets.Credentials{
		APIKey:    mc.SASLPlain.Username,
		APISecret: mc.SASLPlain.Password,
		// Inherited so the single fan-out --insecure-skip-tls-verify (and, now,
		// a private destination CA) has today is preserved: a derived REST leg
		// must not silently verify — or trust a different CA — while the Kafka
		// leg does not.
		CACert:             mc.SASLPlain.CACert,
		InsecureSkipVerify: mc.InsecureSkipTLSVerify,
	}
	if err := targets.ValidateCredentials(derived); err != nil {
		return nil, fmt.Errorf("deriving spec.target.kafka.restCredentials from credentials: %w", err)
	}
	return derived, nil
}

// KubeconfigPath returns spec.gateway.kubeconfig with a leading ~/ expanded.
func (g *GatewayMigration) KubeconfigPath() (string, error) {
	p := g.Spec.Gateway.Kubeconfig
	if p == "" || !strings.HasPrefix(p, "~/") {
		return p, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("expanding %q in spec.gateway.kubeconfig: %w", "~/", err)
	}
	return filepath.Join(home, p[2:]), nil
}

// LoadGatewayMigrationFile reads, parses and structurally validates a manifest
// from disk. Every command that reads a manifest goes through here, so the
// secret-bearing-file warning cannot be wired into one command and forgotten in
// the others.
//
// Credentials are NOT resolved: callers that need them ask for the specific leg,
// which keeps the safety refusals that do not need credentials reachable when
// resolution would fail.
func LoadGatewayMigrationFile(path string) (*GatewayMigration, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading migration manifest: %w", err)
	}
	warnIfGroupOrWorldReadable(path)

	g, err := ParseGatewayMigration(data)
	if err != nil {
		return nil, err
	}
	if errs := g.Validate(); len(errs) > 0 {
		return nil, JoinProblems("the migration manifest", errs)
	}
	return g, nil
}

// warnIfGroupOrWorldReadable flags a secret-bearing manifest with loose
// permissions. A warning rather than an error: the file may legitimately be a
// read-only Kubernetes projected volume, and refusing to read it would break
// the in-cluster path entirely.
func warnIfGroupOrWorldReadable(path string) {
	info, err := os.Stat(path)
	if err != nil {
		return
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		slog.Warn("⚠️ migration manifest is group- or world-readable and may contain credentials",
			"path", path, "mode", fmt.Sprintf("%#o", perm))
	}
}

// JoinProblems renders all problems at once, so an operator fixes the file in
// one pass rather than one error per run.
func JoinProblems(what string, errs []error) error {
	msgs := make([]string, len(errs))
	for i, e := range errs {
		msgs[i] = "  - " + e.Error()
	}
	return fmt.Errorf("%d problem(s) found in %s:\n%s", len(errs), what, strings.Join(msgs, "\n"))
}
