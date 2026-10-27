package manifest

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/confluentinc/kcp/internal/regexanchor"
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

// DefaultOffsetSyncConcurrency applies when OffsetSyncConcurrency is 0.
const DefaultOffsetSyncConcurrency = 8

// GatewayMigration is the declarative form of a `kcp migration` run: the
// topology, the cluster link, the gateway CRs, and the execute-time policy that
// today are spread across 64 flags on four commands.
type GatewayMigration struct {
	APIVersion string      `yaml:"apiVersion" json:"apiVersion"`
	Kind       string      `yaml:"kind" json:"kind"`
	Metadata   Metadata    `yaml:"metadata" json:"metadata"`
	Spec       GatewaySpec `yaml:"spec" json:"spec"`
}

type GatewaySpec struct {
	Source      Source             `yaml:"source" json:"source"`
	Target      GatewayTarget      `yaml:"target" json:"target"`
	ClusterLink GatewayClusterLink `yaml:"clusterLink" json:"clusterLink"`
	Gateway     Gateway            `yaml:"gateway" json:"gateway"`
	// Exactly one of the three sections is declared; it IS the migration type
	// (see MigrationType). The live Gateway CR's route mode is checked against
	// it by the reconcile preconditions, never used to choose it.
	StaticMigration           *StaticMigration           `yaml:"staticMigration,omitempty" json:"staticMigration,omitempty"`
	DynamicMigration          *DynamicMigration          `yaml:"dynamicMigration,omitempty" json:"dynamicMigration,omitempty"`
	DynamicToStaticConversion *DynamicToStaticConversion `yaml:"dynamicToStaticConversion,omitempty" json:"dynamicToStaticConversion,omitempty"`
}

// GatewayTarget is the destination envelope. It is deliberately NOT
// manifest.Target: that type carries clusterCredentials, cloudCredentials,
// schemaRegistry and connect, none of which this kind defines, and a strict
// parser that accepted them would make the manifest a superset of its spec.
// TargetKafka itself IS shared, extended additively.
type GatewayTarget struct {
	Type string `yaml:"type" json:"type"`
	// ClusterID is required for BOTH target types here. kind: Migration forbids
	// it on confluent-platform because kcp migration resources always discovers it; this
	// kind never discovers it, and pinning the id an operator already knows is
	// the safer default ahead of an irreversible cutover.
	ClusterID string       `yaml:"clusterId" json:"clusterId"`
	Kafka     *TargetKafka `yaml:"kafka" json:"kafka"`
}

// Offset-sync baseline values: the operator-declared state of the cluster
// link's consumer.offset.sync.enable BEFORE the migration started. Required
// when consumerOffsetSync.pause is set; a later run restores sync to this value.
const (
	OffsetSyncBaselineEnabled  = "enabled"
	OffsetSyncBaselineDisabled = "disabled"
)

type GatewayClusterLink struct {
	// Name identifies an ALREADY EXISTING cluster link; this kind never creates one.
	Name string `yaml:"name" json:"name"`
	// BootstrapServers repeats spec.target.kafka.bootstrapServers for manifest
	// self-documentation. It is an intentionally unvalidated duplicate: nothing
	// reads it yet and no cross-field equality check enforces that the two lists
	// match.
	BootstrapServers []string `yaml:"bootstrapServers,omitempty" json:"bootstrapServers,omitempty"`
	// LinkCredentials is the destination REST leg used to drive the cluster link
	// (mirror promotion, offset sync). Always required — there is no derivation
	// from the Kafka leg.
	LinkCredentials CredentialsRef `yaml:"linkCredentials" json:"linkCredentials"`
}

// TopicGroupEntry is a topic selection (literal names and/or anchored regex
// patterns) belonging to a migration section.
//
// Topics and TopicPatterns are pointers so nil (omitted) stays distinct from []
// (present but empty, rejected). At least one of the two must be set.
type TopicGroupEntry struct {
	Topics        *[]string `yaml:"topics,omitempty" json:"topics,omitempty"`
	TopicPatterns *[]string `yaml:"topicPatterns,omitempty" json:"topicPatterns,omitempty"`
}

type Gateway struct {
	Namespace string `yaml:"namespace" json:"namespace"`
	// Kubeconfig is the one field in the manifest where a leading ~/ is
	// expanded — nothing else in the repo expands ~, and client-go's loader
	// does not either. Unset, it defaults as KubeconfigPath describes.
	Kubeconfig string `yaml:"kubeconfig,omitempty" json:"kubeconfig,omitempty"`
	// CrName is the Kubernetes object NAME of the initial gateway CR, read live
	// from the cluster on every run. The route to fence and the domain it switches to
	// live in the declared migration section's route; there is no fenced-CR or switchover-CR file — both
	// are derived from this live CR at cutover.
	CrName string `yaml:"cr-name" json:"cr-name"`
}

// envelope is the minimum needed to discriminate one kind from another. It is
// deliberately NOT strict: the body belongs to whichever kind claims it, and a
// wrong-kind file must yield "this is a Migration manifest" rather than a
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
		return fmt.Errorf("this is a %q manifest (used by `kcp migration resources`), but %q was expected — `kcp migration execute`, `lag-check` and `reconcile` read a %s file",
			KindMigration, want, KindGatewayMigration)
	case KindGatewayMigration:
		return fmt.Errorf("this is a %q manifest (used by `kcp migration execute`, `lag-check` and `reconcile`), but %q was expected — `kcp migration resources` reads a %s file",
			KindGatewayMigration, want, KindMigration)
	default:
		return fmt.Errorf("unsupported kind %q (expected %q)", got, want)
	}
}

// ParseGatewayMigration decodes a GatewayMigration manifest with strict
// decoding. Credentials are referenced files only, resolved on demand by the
// per-leg resolver methods.
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
		cause := yamlsafe.StripSourceExcerpt(err)
		if hint := legacyShapeHint(data); hint != "" {
			return nil, fmt.Errorf("parsing gateway migration manifest: %s: %w", hint, cause)
		}
		return nil, fmt.Errorf("parsing gateway migration manifest: %w", cause)
	}
	return &g, nil
}

// legacyShapeHint recognises a manifest written for the shape that preceded the
// declared migration type and says what replaced it. It is a diagnostic only:
// the manifest is still rejected, never inferred into the new shape. The probe
// decode is lenient (unknown fields ignored) and its errors are discarded — the
// strict decode has already failed and carries the real error.
func legacyShapeHint(data []byte) string {
	var probe struct {
		Spec struct {
			Route           any `yaml:"route"`
			DefaultPolicies any `yaml:"defaultPolicies"`
			ClusterLink     struct {
				Pause    any `yaml:"pauseConsumerOffsetSync"`
				Baseline any `yaml:"consumerOffsetSyncBaseline"`
			} `yaml:"clusterLink"`
		} `yaml:"spec"`
	}
	if err := yaml.Unmarshal(data, &probe); err != nil {
		return ""
	}
	var found []string
	if probe.Spec.Route != nil {
		found = append(found, "spec.route")
	}
	if probe.Spec.DefaultPolicies != nil {
		found = append(found, "spec.defaultPolicies")
	}
	if probe.Spec.ClusterLink.Pause != nil {
		found = append(found, "spec.clusterLink.pauseConsumerOffsetSync")
	}
	if probe.Spec.ClusterLink.Baseline != nil {
		found = append(found, "spec.clusterLink.consumerOffsetSyncBaseline")
	}
	if len(found) == 0 {
		return ""
	}
	return fmt.Sprintf("the manifest shape changed: %s %s no longer valid — declare the migration type by providing exactly one of %s, each carrying its own route, policies and, for migrations, topicGroup (see docs/assets/gateway-examples/)",
		strings.Join(found, ", "), pluralIs(len(found)), strings.Join(sectionKeys, ", "))
}

func pluralIs(n int) string {
	if n == 1 {
		return "is"
	}
	return "are"
}

// Validate performs structural validation and returns ALL problems found, each
// tagged with its field path.
//
// It does no I/O, so a credentials slot spelled as a path is checked for
// presence only; the rules that need the block's contents (source auth gating,
// the destination iam rejection) run only in SourceCredentials /
// DestinationKafkaCredentials, which read and parse the referenced file.
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
		if blankRef(k.ClusterCredentials) {
			add("spec.target.kafka.clusterCredentials: must not be empty")
		}
	}

	// --- cluster link ---
	if blank(g.Spec.ClusterLink.Name) {
		add("spec.clusterLink.name: must not be empty (the link must already exist)")
	}
	if blankRef(g.Spec.ClusterLink.LinkCredentials) {
		add("spec.clusterLink.linkCredentials: must not be empty")
	}

	// --- gateway ---
	if blank(g.Spec.Gateway.Namespace) {
		add("spec.gateway.namespace: must not be empty")
	}
	if blank(g.Spec.Gateway.CrName) {
		add("spec.gateway.cr-name: must not be empty (a Kubernetes object name, read live)")
	}

	// --- the declared migration type: exactly one section, and its rules ---
	errs = append(errs, g.Spec.validateSection()...)

	return errs
}

// anchoredPattern compiles p as an anchored RE2 full-match: the Gateway matches
// topicPatterns Java-style (anchored full-match), but Go's regexp default is
// unanchored/partial. It is the shared regexanchor.Compile — the same hardened
// (compile-standalone-then-wrap) anchoring the reconciliation core uses, so the
// two cannot diverge.
func anchoredPattern(p string) (*regexp.Regexp, error) {
	return regexanchor.Compile(p)
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
// already uses — including a custom ca_cert on sasl_plain (the destination leg
// dials through the mapper; see cmd/migration/execute's destinationConn).
func checkDestinationKafkaAuth(mc types.MigrateClusterCredentials) []error {
	if mc.IAM != nil {
		return []error{fmt.Errorf(
			"spec.target.kafka.clusterCredentials.iam: not supported for the destination (the destination is Confluent Cloud/Platform, never MSK)")}
	}
	return nil
}

// SourceCredentials resolves the source Kafka leg.
func (g *GatewayMigration) SourceCredentials() (types.MigrateClusterCredentials, []error) {
	mc, errs := g.Spec.Source.Credentials.ResolveMigrateCluster()
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
	mc, errs := g.Spec.Target.Kafka.ClusterCredentials.ResolveMigrateCluster()
	if len(errs) > 0 {
		return mc, errs
	}
	if errs := checkDestinationKafkaAuth(mc); len(errs) > 0 {
		return mc, errs
	}
	return mc, nil
}

// RestCredentials resolves the destination REST leg used to drive the cluster
// link. It reads spec.clusterLink.linkCredentials, which is always required —
// there is no derivation from the Kafka leg.
func (g *GatewayMigration) RestCredentials() (*targets.Credentials, error) {
	return g.Spec.ClusterLink.LinkCredentials.ResolveTarget()
}

// KubeconfigPath returns the kubeconfig every Kubernetes client kcp builds from
// this manifest uses: spec.gateway.kubeconfig with a leading ~/ expanded, or,
// when it is unset, the empty path inside a pod (client-go then uses the pod's
// in-cluster service account) and ~/.kube/config anywhere else.
func (g *GatewayMigration) KubeconfigPath() (string, error) {
	p := g.Spec.Gateway.Kubeconfig
	if p == "" {
		if os.Getenv("KUBERNETES_SERVICE_HOST") != "" {
			return "", nil
		}
		p = "~/.kube/config"
	}
	if !strings.HasPrefix(p, "~/") {
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
	warnIfGroupOrWorldReadable(path, "migration manifest")

	g, err := ParseGatewayMigration(data)
	if err != nil {
		return nil, err
	}
	if errs := g.Validate(); len(errs) > 0 {
		return nil, JoinProblems("the migration manifest", errs)
	}
	return g, nil
}

// warnIfGroupOrWorldReadable flags a secret-bearing file (the manifest itself,
// or a credentials file resolved from it) with loose permissions. what names
// the file's role (e.g. "migration manifest", "credentials file") so the
// message identifies what was actually checked instead of assuming the caller
// is always the manifest. A warning rather than an error: the file may
// legitimately be a read-only Kubernetes projected volume, and refusing to
// read it would break the in-cluster path entirely.
func warnIfGroupOrWorldReadable(path, what string) {
	info, err := os.Stat(path)
	if err != nil {
		return
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		slog.Warn(fmt.Sprintf("⚠️ %s is group- or world-readable", what),
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
