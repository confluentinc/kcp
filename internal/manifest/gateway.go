package manifest

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
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

type Gateway struct {
	Namespace string `yaml:"namespace" json:"namespace"`
	// Kubeconfig is the one field in the manifest where a leading ~/ is
	// expanded — nothing else in the repo expands ~, and client-go's loader
	// does not either.
	Kubeconfig string     `yaml:"kubeconfig,omitempty" json:"kubeconfig,omitempty"`
	CRs        GatewayCRs `yaml:"crs" json:"crs"`
	// Fence names the route(s) kcp fences at cutover. There is no fenced-CR
	// file: kcp reads the live initial CR, injects the fence block onto each
	// named route, and applies the patched CR.
	Fence GatewayFence `yaml:"fence" json:"fence"`
}

// GatewayCRs holds the two gateway CRs the migration reads by different means,
// inherited from --initial-cr-name vs the switchover file path.
type GatewayCRs struct {
	// Initial is a Kubernetes object NAME, read live from the cluster at init.
	Initial string `yaml:"initial" json:"initial"`
	// Switchover is a local FILE path, snapshotted into the state file at init.
	Switchover string `yaml:"switchover" json:"switchover"`
}

// GatewayFence declares which route(s) the fence step blocks. kcp derives the
// fenced CR from the live initial CR by injecting
// fence: {scope: ALL, errorCode: BROKER_NOT_AVAILABLE} onto each named route,
// so fence and its rollback (which re-applies the same initial CR) are exact
// inverses. The switchover CR, which lifts the fence, stays file-based.
type GatewayFence struct {
	// Routes are the spec.routes[].name values to fence. Non-empty; each entry
	// must be a non-blank, unique route name that exists in the initial CR.
	Routes []string `yaml:"routes" json:"routes"`
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
// the sasl_plain-only destination rule) run here for an inline block and again
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
				errs = append(errs, checkDestinationIsSASLPlain(mc)...)
			}
		}
		if k.RestCredentials != nil {
			if blankRef(*k.RestCredentials) {
				add("spec.target.kafka.restCredentials: present but empty — omit it to derive from credentials, or fill it in")
			} else if k.RestCredentials.IsInline() {
				if tc, ok := peekTargetCreds(*k.RestCredentials); ok {
					errs = append(errs, checkRestIsAPIKeyForm(tc)...)
				}
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
	if blank(g.Spec.Gateway.CRs.Initial) {
		add("spec.gateway.crs.initial: must not be empty (a Kubernetes object name, read live)")
	}
	if blank(g.Spec.Gateway.CRs.Switchover) {
		add("spec.gateway.crs.switchover: must not be empty (a local file path)")
	}
	if routes := g.Spec.Gateway.Fence.Routes; len(routes) == 0 {
		add("spec.gateway.fence.routes: must name at least one route to fence")
	} else {
		seen := make(map[string]struct{}, len(routes))
		for i, name := range routes {
			if blank(name) {
				add("spec.gateway.fence.routes[%d]: must not be blank", i)
				continue
			}
			if _, dup := seen[name]; dup {
				add("spec.gateway.fence.routes: %q is listed more than once", name)
			}
			seen[name] = struct{}{}
		}
	}

	// --- topics ---
	if g.Spec.Topics != nil {
		if len(*g.Spec.Topics) == 0 {
			add("spec.topics: must not be an empty list — omit the key entirely to migrate every active mirror topic")
		}
		for i, name := range *g.Spec.Topics {
			if blank(name) {
				add("spec.topics[%d]: must not be blank", i)
			}
		}
	}

	// --- defaultPolicies ---
	errs = append(errs, g.Spec.DefaultPolicies.Validate()...)

	return errs
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

// checkDestinationIsSASLPlain rejects every destination block other than
// sasl_plain. The destination Kafka client is hardcoded to SASL/PLAIN over TLS,
// so any other block would be accepted and then silently ignored — worse than
// the flag surface this replaces.
func checkDestinationIsSASLPlain(mc types.MigrateClusterCredentials) []error {
	if mc.SASLPlain != nil {
		// The destination Kafka client dials SASL/PLAIN over the public trust
		// store — createDestinationOffset hardcodes an empty ca_cert — and a
		// derived REST leg drops ca_cert too. A ca_cert here would therefore be
		// accepted and then silently ignored, so a private-CA destination would
		// read as configured while actually connecting on the system roots.
		// Refuse it, the same call as the sasl_plain-only and api_key-only rules.
		// (tls stays permitted: it names the exact transport the destination
		// already uses, so it is honoured rather than dropped.)
		if strings.TrimSpace(mc.SASLPlain.CACert) != "" {
			return []error{fmt.Errorf(
				"spec.target.kafka.credentials.sasl_plain.ca_cert: a custom CA is not supported for the destination in this release (it dials the public trust store); remove ca_cert")}
		}
		return nil
	}
	if mc.IAM == nil && mc.SASLScram == nil && mc.MTLS == nil &&
		mc.UnauthenticatedTLS == nil && mc.UnauthenticatedPlaintext == nil {
		return nil // no block at all — the shared validator reports that
	}
	return []error{fmt.Errorf(
		"spec.target.kafka.credentials: only sasl_plain is supported for the destination in this release")}
}

// peekTargetCreds decodes an inline REST credentials block for validation
// without I/O.
func peekTargetCreds(ref CredentialsRef) (targets.Credentials, bool) {
	var tc targets.Credentials
	if err := yaml.Unmarshal(ref.Inline, &tc); err != nil {
		return tc, false
	}
	return tc, true
}

// checkRestIsAPIKeyForm rejects every REST credentials form other than the flat
// api_key pair.
//
// targets.Credentials can express basic, bearer and mtls, and its own
// HTTPClient/Authenticator handle all of them — but every kcp migration command
// reads only APIKey/APISecret/CACert/InsecureSkipVerify. A bearer block would
// therefore be accepted, then dropped, and the request would go out as
// anonymous Basic auth with the declared ca_cert ignored. Refusing is the same
// call as the sasl_plain-only rule on the destination Kafka leg: a credential
// that is silently not used is worse than one that is rejected.
func checkRestIsAPIKeyForm(tc targets.Credentials) []error {
	switch {
	case tc.Bearer != nil:
		return []error{fmt.Errorf("spec.target.kafka.restCredentials: only the api_key/api_secret form is supported in this release (got bearer)")}
	case tc.MTLS != nil:
		return []error{fmt.Errorf("spec.target.kafka.restCredentials: only the api_key/api_secret form is supported in this release (got mtls)")}
	case tc.Basic != nil:
		return []error{fmt.Errorf("spec.target.kafka.restCredentials: only the api_key/api_secret form is supported in this release (got basic)")}
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

// DestinationKafkaCredentials resolves the destination Kafka leg — the API
// key/secret used as SASL/PLAIN against the destination bootstrap, not only as
// HTTP basic over REST.
func (g *GatewayMigration) DestinationKafkaCredentials() (types.MigrateClusterCredentials, []error) {
	if g.Spec.Target.Kafka == nil {
		return types.MigrateClusterCredentials{}, []error{fmt.Errorf("spec.target.kafka: required")}
	}
	mc, errs := g.Spec.Target.Kafka.Credentials.ResolveMigrateCluster(g.Interpolate)
	if len(errs) > 0 {
		return mc, errs
	}
	if errs := checkDestinationIsSASLPlain(mc); len(errs) > 0 {
		return mc, errs
	}
	return mc, nil
}

// RestCredentials resolves the destination REST leg.
//
// When spec.target.kafka.restCredentials is omitted it is DERIVED, in full,
// from the Kafka leg: one flag pair feeds both legs today, so requiring both
// blocks would make the operator type the same secret twice for the
// overwhelmingly common case. Derivation is full-or-nothing — a block that is
// present is used exactly as written, because a block that reads as complete
// while silently acquiring fields from elsewhere is worse than either.
func (g *GatewayMigration) RestCredentials() (*targets.Credentials, error) {
	if g.Spec.Target.Kafka == nil {
		return nil, fmt.Errorf("spec.target.kafka: required")
	}
	if ref := g.Spec.Target.Kafka.RestCredentials; ref != nil {
		tc, err := ref.ResolveTarget(g.Interpolate)
		if err != nil {
			return nil, err
		}
		if errs := checkRestIsAPIKeyForm(*tc); len(errs) > 0 {
			return nil, errs[0]
		}
		return tc, nil
	}

	mc, errs := g.DestinationKafkaCredentials()
	if len(errs) > 0 {
		return nil, fmt.Errorf("deriving spec.target.kafka.restCredentials from credentials: %w", errs[0])
	}
	if mc.SASLPlain == nil {
		return nil, fmt.Errorf("spec.target.kafka.credentials: only sasl_plain is supported for the destination in this release")
	}
	derived := &targets.Credentials{
		APIKey:    mc.SASLPlain.Username,
		APISecret: mc.SASLPlain.Password,
		// Inherited so the single fan-out --insecure-skip-tls-verify had today
		// is preserved: a derived REST leg must not silently verify while the
		// Kafka leg does not.
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
