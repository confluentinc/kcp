package manifest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/confluentinc/kcp/internal/types"
	"github.com/goccy/go-yaml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// validGatewayDoc is the canonical manifest from the design §4, minus the
// optional blocks. Tests mutate a copy of it to exercise one rule at a time.
const validGatewayDoc = `apiVersion: kcp.confluent.io/v1alpha1
kind: GatewayMigration
metadata:
  name: msk-prod-to-cc-batch-1
spec:
  source:
    type: msk
    bootstrapServers:
      - b-1.msk-prod.abc123.c2.kafka.us-east-1.amazonaws.com:9096
    credentials:
      sasl_scram:
        username: admin
        password: secret
        mechanism: SHA512
  target:
    type: confluent-cloud
    clusterId: lkc-abc123
    kafka:
      bootstrapServers:
        - pkc-xxxxx.us-east-1.aws.confluent.cloud:9092
      restEndpoint: https://pkc-xxxxx.us-east-1.aws.confluent.cloud:443
      credentials:
        sasl_plain:
          username: CC_KEY
          password: CC_SECRET
          tls: true
  clusterLink:
    name: msk-to-cc
  gateway:
    namespace: confluent
    crs:
      initial: gateway-initial
      fenced: /etc/kcp/gateway-fenced.yaml
      switchover: /etc/kcp/gateway-switchover.yaml
`

func parseGateway(t *testing.T, doc string) *GatewayMigration {
	t.Helper()
	g, err := ParseGatewayMigration([]byte(doc))
	require.NoError(t, err)
	return g
}

// withField replaces the first occurrence of old with new in the canonical doc.
func withField(t *testing.T, old, new string) string {
	t.Helper()
	require.Contains(t, validGatewayDoc, old, "fixture drift: %q not in the canonical doc", old)
	return strings.Replace(validGatewayDoc, old, new, 1)
}

// withRestCredentials splices a restCredentials block into spec.target.kafka,
// which is where it belongs — appending to the document would land it under
// spec:, several levels too high.
func withRestCredentials(t *testing.T, block string) string {
	t.Helper()
	const anchor = "  clusterLink:\n"
	require.Contains(t, validGatewayDoc, anchor)
	return strings.Replace(validGatewayDoc, anchor, block+anchor, 1)
}

func TestGateway_CanonicalManifestValidates(t *testing.T) {
	g := parseGateway(t, validGatewayDoc)
	require.Empty(t, g.Validate(), "the design's canonical manifest must validate clean")
}

func TestGateway_ParsesEveryField(t *testing.T) {
	g := parseGateway(t, validGatewayDoc)
	assert.Equal(t, "msk-prod-to-cc-batch-1", g.Metadata.Name)
	assert.Equal(t, SourceMSK, g.Spec.Source.Type)
	assert.Equal(t, []string{"b-1.msk-prod.abc123.c2.kafka.us-east-1.amazonaws.com:9096"}, g.Spec.Source.BootstrapServers)
	assert.Equal(t, "lkc-abc123", g.Spec.Target.ClusterID)
	assert.Equal(t, "msk-to-cc", g.Spec.ClusterLink.Name)
	assert.Equal(t, "confluent", g.Spec.Gateway.Namespace)
	assert.Equal(t, "gateway-initial", g.Spec.Gateway.CRs.Initial)
	assert.Equal(t, "/etc/kcp/gateway-fenced.yaml", g.Spec.Gateway.CRs.Fenced)
	assert.Equal(t, "/etc/kcp/gateway-switchover.yaml", g.Spec.Gateway.CRs.Switchover)
}

func TestGateway_RejectsUnknownFields(t *testing.T) {
	_, err := ParseGatewayMigration([]byte(validGatewayDoc + "  typoSection: x\n"))
	require.Error(t, err)
}

// TestGateway_RejectsWrongKind — the kind check fires at parse time (see
// TestParseGatewayMigration_RejectsMigrationKindWithAUsefulMessage). Validate
// keeps its own check as a defence for a struct assembled without the parser.
func TestGateway_RejectsWrongKind(t *testing.T) {
	_, err := ParseGatewayMigration([]byte(withField(t, "kind: GatewayMigration", "kind: Migration")))
	require.Error(t, err)

	g := parseGateway(t, validGatewayDoc)
	g.Kind = KindMigration
	requireErrContains(t, g.Validate(), "kind")
}

func TestGateway_RejectsWrongAPIVersion(t *testing.T) {
	g := parseGateway(t, withField(t, "apiVersion: kcp.confluent.io/v1alpha1", "apiVersion: kcp.confluent.io/v1"))
	requireErrContains(t, g.Validate(), "apiVersion")
}

func TestGateway_RequiresMetadataName(t *testing.T) {
	g := parseGateway(t, withField(t, "  name: msk-prod-to-cc-batch-1", "  name: \"\""))
	requireErrContains(t, g.Validate(), "metadata.name")
}

// --- source ---

func TestGateway_RequiresSourceType(t *testing.T) {
	g := parseGateway(t, withField(t, "    type: msk", "    type: \"\""))
	requireErrContains(t, g.Validate(), "spec.source.type")
}

func TestGateway_RejectsUnknownSourceType(t *testing.T) {
	g := parseGateway(t, withField(t, "    type: msk", "    type: rabbitmq"))
	requireErrContains(t, g.Validate(), "spec.source.type")
}

// TestGateway_RejectsIAMOnApacheKafkaSource is the §2.4 tightening: iam is
// MSK-only, so declaring it against an apache-kafka source is a typo, not a
// working configuration.
func TestGateway_RejectsIAMOnApacheKafkaSource(t *testing.T) {
	doc := withField(t, "    type: msk", "    type: apache-kafka")
	doc = strings.Replace(doc,
		"      sasl_scram:\n        username: admin\n        password: secret\n        mechanism: SHA512",
		"      iam:\n        region: us-east-1", 1)
	g := parseGateway(t, doc)
	requireErrContains(t, g.Validate(), "iam")
}

func TestGateway_AcceptsIAMOnMSKSource(t *testing.T) {
	doc := strings.Replace(validGatewayDoc,
		"      sasl_scram:\n        username: admin\n        password: secret\n        mechanism: SHA512",
		"      iam:\n        region: us-east-1", 1)
	g := parseGateway(t, doc)
	require.Empty(t, g.Validate())
}

// TestGateway_SourceTypeConfluentPlatformIsNotOffered — the gateway migration
// reads from MSK or Apache Kafka only; the page lists exactly those two.
func TestGateway_SourceTypeConfluentPlatformIsNotOffered(t *testing.T) {
	g := parseGateway(t, withField(t, "    type: msk", "    type: confluent-platform"))
	requireErrContains(t, g.Validate(), "spec.source.type")
}

func TestGateway_RequiresSourceBootstrapServers(t *testing.T) {
	doc := strings.Replace(validGatewayDoc,
		"    bootstrapServers:\n      - b-1.msk-prod.abc123.c2.kafka.us-east-1.amazonaws.com:9096",
		"    bootstrapServers: []", 1)
	g := parseGateway(t, doc)
	requireErrContains(t, g.Validate(), "spec.source.bootstrapServers")
}

func TestGateway_RequiresSourceCredentials(t *testing.T) {
	doc := strings.Replace(validGatewayDoc,
		"    credentials:\n      sasl_scram:\n        username: admin\n        password: secret\n        mechanism: SHA512",
		"    credentials: \"\"", 1)
	g := parseGateway(t, doc)
	requireErrContains(t, g.Validate(), "spec.source.credentials")
}

// --- target ---

// TestGateway_RequiresClusterIDForBothTargetTypes is decision 25: kind:
// Migration FORBIDS clusterId on a confluent-platform target, and
// GatewayMigration must require it, or the e2e's CP destination becomes
// inexpressible.
func TestGateway_RequiresClusterIDForBothTargetTypes(t *testing.T) {
	for _, targetType := range []string{TargetConfluentCloud, TargetConfluentPlatform} {
		t.Run(targetType, func(t *testing.T) {
			doc := withField(t, "    type: confluent-cloud", "    type: "+targetType)
			ok := parseGateway(t, doc)
			require.Empty(t, ok.Validate(), "%s with an explicit clusterId must be legal", targetType)

			missing := parseGateway(t, strings.Replace(doc, "    clusterId: lkc-abc123\n", "", 1))
			requireErrContains(t, missing.Validate(), "spec.target.clusterId")
		})
	}
}

func TestGateway_RequiresTargetKafkaRestEndpoint(t *testing.T) {
	doc := strings.Replace(validGatewayDoc,
		"      restEndpoint: https://pkc-xxxxx.us-east-1.aws.confluent.cloud:443\n", "", 1)
	g := parseGateway(t, doc)
	requireErrContains(t, g.Validate(), "spec.target.kafka.restEndpoint")
}

func TestGateway_RequiresTargetKafkaBootstrapServers(t *testing.T) {
	doc := strings.Replace(validGatewayDoc,
		"      bootstrapServers:\n        - pkc-xxxxx.us-east-1.aws.confluent.cloud:9092\n", "", 1)
	g := parseGateway(t, doc)
	requireErrContains(t, g.Validate(), "spec.target.kafka.bootstrapServers")
}

// TestGateway_RejectsNonSASLPlainDestination is the rule decision 29 forces:
// migration_executor.go dials the destination as SASL/PLAIN unconditionally, so
// any other block would be silently ignored — worse than the flag surface it
// replaces.
func TestGateway_RejectsNonSASLPlainDestination(t *testing.T) {
	for _, block := range []string{
		"        sasl_scram:\n          username: u\n          password: p\n          mechanism: SHA512",
		"        mtls:\n          client_cert: /c.pem\n          client_key: /k.pem",
		"        unauthenticated_plaintext: {}",
		"        unauthenticated_tls: {}",
		"        iam:\n          region: us-east-1",
	} {
		t.Run(strings.TrimSpace(strings.SplitN(block, ":", 2)[0]), func(t *testing.T) {
			doc := strings.Replace(validGatewayDoc,
				"        sasl_plain:\n          username: CC_KEY\n          password: CC_SECRET\n          tls: true",
				block, 1)
			g := parseGateway(t, doc)
			requireErrContains(t, g.Validate(), "sasl_plain")
		})
	}
}

// --- restCredentials derivation (decision 29) ---

// TestGateway_DerivesRestCredentialsWhenOmitted: one flag pair feeds both
// destination legs today, so omitting restCredentials must not mean "no REST
// credentials".
func TestGateway_DerivesRestCredentialsWhenOmitted(t *testing.T) {
	g := parseGateway(t, validGatewayDoc)
	require.Empty(t, g.Validate())

	rest, err := g.RestCredentials()
	require.NoError(t, err)
	assert.Equal(t, "CC_KEY", rest.APIKey)
	assert.Equal(t, "CC_SECRET", rest.APISecret)
}

// TestGateway_DerivedRestCredentialsInheritInsecureSkip preserves today's
// single-flag fan-out: --insecure-skip-tls-verify reaches all three legs, so a
// derived REST leg must not silently verify while the Kafka leg does not.
func TestGateway_DerivedRestCredentialsInheritInsecureSkip(t *testing.T) {
	doc := strings.Replace(validGatewayDoc,
		"      credentials:\n        sasl_plain:",
		"      credentials:\n        insecure_skip_tls_verify: true\n        sasl_plain:", 1)
	g := parseGateway(t, doc)
	require.Empty(t, g.Validate())

	rest, err := g.RestCredentials()
	require.NoError(t, err)
	assert.True(t, rest.InsecureSkipVerify)
}

// TestGateway_DerivedEqualsHandWritten is the §7.1 parity test: a derived block
// must produce a byte-identical targets.Credentials to the equivalent explicit one.
func TestGateway_DerivedEqualsHandWritten(t *testing.T) {
	derived, err := parseGateway(t, validGatewayDoc).RestCredentials()
	require.NoError(t, err)

	explicitDoc := withRestCredentials(t, `      restCredentials:
        api_key: CC_KEY
        api_secret: CC_SECRET
`)
	explicit, err := parseGateway(t, explicitDoc).RestCredentials()
	require.NoError(t, err)

	assert.Equal(t, explicit, derived)
}

// TestGateway_ExplicitRestCredentialsSurviveUntouched — full-or-nothing, never
// per-field: a present block is used exactly as written even when it differs.
func TestGateway_ExplicitRestCredentialsSurviveUntouched(t *testing.T) {
	doc := withRestCredentials(t, `      restCredentials:
        api_key: DIFFERENT_KEY
        api_secret: DIFFERENT_SECRET
`)
	g := parseGateway(t, doc)
	require.Empty(t, g.Validate())

	rest, err := g.RestCredentials()
	require.NoError(t, err)
	assert.Equal(t, "DIFFERENT_KEY", rest.APIKey)
	assert.Equal(t, "DIFFERENT_SECRET", rest.APISecret)
	assert.False(t, rest.InsecureSkipVerify)
}

// TestGateway_ExplicitRestCredentialsAreNotPartiallyDerived — a present block
// that omits insecure_skip_verify does NOT acquire it from the Kafka leg.
// Partial derivation would mean a block that reads as complete but silently
// acquires fields from elsewhere.
func TestGateway_ExplicitRestCredentialsAreNotPartiallyDerived(t *testing.T) {
	doc := strings.Replace(validGatewayDoc,
		"      credentials:\n        sasl_plain:",
		"      credentials:\n        insecure_skip_tls_verify: true\n        sasl_plain:", 1)
	doc = strings.Replace(doc, "  clusterLink:\n", `      restCredentials:
        api_key: K
        api_secret: S
`+"  clusterLink:\n", 1)
	g := parseGateway(t, doc)
	rest, err := g.RestCredentials()
	require.NoError(t, err)
	assert.False(t, rest.InsecureSkipVerify, "an explicit block is used exactly as written")
}

// TestGateway_RestCredentialsAcceptPrivateCA is §2.1(a): the one case
// derivation cannot cover.
func TestGateway_RestCredentialsAcceptPrivateCA(t *testing.T) {
	ca := filepath.Join(t.TempDir(), "dest-ca.pem")
	require.NoError(t, os.WriteFile(ca, []byte("pem"), 0600))

	doc := withRestCredentials(t, `      restCredentials:
        api_key: K
        api_secret: S
        ca_cert: `+ca+`
`)
	g := parseGateway(t, doc)
	require.Empty(t, g.Validate())
	rest, err := g.RestCredentials()
	require.NoError(t, err)
	assert.Equal(t, ca, rest.CACert)
}

// --- cluster link, gateway, topics, policy ---

func TestGateway_RequiresClusterLinkName(t *testing.T) {
	g := parseGateway(t, withField(t, "    name: msk-to-cc", "    name: \"\""))
	requireErrContains(t, g.Validate(), "spec.clusterLink.name")
}

func TestGateway_PauseConsumerOffsetSyncDefaultsFalse(t *testing.T) {
	g := parseGateway(t, validGatewayDoc)
	assert.False(t, g.Spec.ClusterLink.PauseConsumerOffsetSync)
}

func TestGateway_RequiresGatewayNamespace(t *testing.T) {
	g := parseGateway(t, withField(t, "    namespace: confluent", "    namespace: \"\""))
	requireErrContains(t, g.Validate(), "spec.gateway.namespace")
}

func TestGateway_RequiresAllThreeCRs(t *testing.T) {
	for field, line := range map[string]string{
		"spec.gateway.crs.initial":    "      initial: gateway-initial\n",
		"spec.gateway.crs.fenced":     "      fenced: /etc/kcp/gateway-fenced.yaml\n",
		"spec.gateway.crs.switchover": "      switchover: /etc/kcp/gateway-switchover.yaml\n",
	} {
		t.Run(field, func(t *testing.T) {
			g := parseGateway(t, strings.Replace(validGatewayDoc, line, "", 1))
			requireErrContains(t, g.Validate(), field)
		})
	}
}

// TestGateway_KubeconfigTildeIsExpanded — the one place in the repo where a
// leading ~/ is expanded; client-go's loader does not do it.
func TestGateway_KubeconfigTildeIsExpanded(t *testing.T) {
	doc := strings.Replace(validGatewayDoc,
		"    namespace: confluent\n",
		"    namespace: confluent\n    kubeconfig: ~/.kube/config\n", 1)
	g := parseGateway(t, doc)
	require.Empty(t, g.Validate())

	home, err := os.UserHomeDir()
	require.NoError(t, err)
	got, err := g.KubeconfigPath()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(home, ".kube", "config"), got)
}

func TestGateway_KubeconfigEmptyStaysEmpty(t *testing.T) {
	got, err := parseGateway(t, validGatewayDoc).KubeconfigPath()
	require.NoError(t, err)
	assert.Empty(t, got)
}

// TestGateway_TopicsOmittedIsDistinctFromEmpty — omitted means "every active
// mirror topic"; an explicitly empty list means the opposite, so they must not
// collapse onto each other.
func TestGateway_TopicsOmittedIsDistinctFromEmpty(t *testing.T) {
	omitted := parseGateway(t, validGatewayDoc)
	assert.Nil(t, omitted.Spec.Topics)
	require.Empty(t, omitted.Validate())

	empty := parseGateway(t, validGatewayDoc+"  topics: []\n")
	require.NotNil(t, empty.Spec.Topics)
	requireErrContains(t, empty.Validate(), "spec.topics")
}

func TestGateway_TopicsAreLiteralNames(t *testing.T) {
	g := parseGateway(t, validGatewayDoc+"  topics: ['t1.order', 't1.inventory']\n")
	require.Empty(t, g.Validate())
	assert.Equal(t, []string{"t1.order", "t1.inventory"}, *g.Spec.Topics)
}

func TestGateway_RejectsBlankTopicName(t *testing.T) {
	g := parseGateway(t, validGatewayDoc+"  topics: ['t1.order', '  ']\n")
	requireErrContains(t, g.Validate(), "spec.topics")
}

func TestGateway_PolicyDurationsParseAsDurationStrings(t *testing.T) {
	doc := validGatewayDoc + `  policy:
    lagThreshold: 0
    promoteBatchSize: 100
    rolloutTimeout: 10m
    detectUnroutedProducersDuration: 30s
    consumerOffsetSyncDrainDuration: 15s
`
	g := parseGateway(t, doc)
	require.Empty(t, g.Validate())
	assert.Equal(t, 100, g.Spec.Policy.PromoteBatchSize)
	assert.Equal(t, 10*time.Minute, g.Spec.Policy.RolloutTimeout)
	assert.Equal(t, 30*time.Second, g.Spec.Policy.DetectUnroutedProducersDuration)
	assert.Equal(t, 15*time.Second, g.Spec.Policy.ConsumerOffsetSyncDrainDuration)
}

// TestGateway_PolicyOmittedIsZeroValued — every policy knob is optional and 0
// carries meaning (0 = promote all at once / no deadline / check skipped).
func TestGateway_PolicyOmittedIsZeroValued(t *testing.T) {
	g := parseGateway(t, validGatewayDoc)
	require.Empty(t, g.Validate())
	assert.Equal(t, Policy{}, g.Spec.Policy)
}

// TestGateway_RejectsSubTenSecondDetectDuration mirrors the retired flag's
// documented minimum, which existed because a shorter window cannot observe a
// producer's metadata refresh.
func TestGateway_RejectsSubTenSecondDetectDuration(t *testing.T) {
	g := parseGateway(t, validGatewayDoc+"  policy:\n    detectUnroutedProducersDuration: 5s\n")
	requireErrContains(t, g.Validate(), "detectUnroutedProducersDuration")
}

func TestGateway_AcceptsZeroDetectDurationMeaningSkipped(t *testing.T) {
	g := parseGateway(t, validGatewayDoc+"  policy:\n    detectUnroutedProducersDuration: 0s\n")
	require.Empty(t, g.Validate())
}

func TestGateway_RejectsNegativePolicyNumbers(t *testing.T) {
	for _, line := range []string{"    lagThreshold: -1", "    promoteBatchSize: -1"} {
		t.Run(strings.TrimSpace(line), func(t *testing.T) {
			g := parseGateway(t, validGatewayDoc+"  policy:\n"+line+"\n")
			require.NotEmpty(t, g.Validate())
		})
	}
}

// --- interpolation on the manifest itself ---

func TestGateway_InterpolatesInlineCredentialsWhenOptedIn(t *testing.T) {
	t.Setenv("MSK_USERNAME", "admin")
	t.Setenv("MSK_PASSWORD", "s3cret")
	doc := strings.Replace(validGatewayDoc, "kind: GatewayMigration",
		"kind: GatewayMigration\ninterpolate: true", 1)
	doc = strings.Replace(doc, "        username: admin\n        password: secret",
		"        username: ${MSK_USERNAME}\n        password: ${MSK_PASSWORD}", 1)

	g := parseGateway(t, doc)
	require.Empty(t, g.Validate())
	creds, errs := g.SourceCredentials()
	require.Empty(t, errs)
	assert.Equal(t, "admin", creds.SASLScram.Username)
	assert.Equal(t, "s3cret", creds.SASLScram.Password)
}

func TestGateway_NoInterpolationByDefault(t *testing.T) {
	t.Setenv("MSK_PASSWORD", "s3cret")
	doc := strings.Replace(validGatewayDoc, "        password: secret",
		"        password: ${MSK_PASSWORD}", 1)
	g := parseGateway(t, doc)
	creds, errs := g.SourceCredentials()
	require.Empty(t, errs)
	assert.Equal(t, "${MSK_PASSWORD}", creds.SASLScram.Password)
}

// TestGateway_InterpolatesTopLevelStringFields — the reflective pass reaches
// ordinary manifest strings too, not only credentials.
func TestGateway_InterpolatesTopLevelStringFields(t *testing.T) {
	t.Setenv("LINK_NAME", "msk-to-cc-prod")
	doc := strings.Replace(validGatewayDoc, "kind: GatewayMigration",
		"kind: GatewayMigration\ninterpolate: true", 1)
	doc = strings.Replace(doc, "    name: msk-to-cc", "    name: ${LINK_NAME}", 1)
	g := parseGateway(t, doc)
	assert.Equal(t, "msk-to-cc-prod", g.Spec.ClusterLink.Name)
}

func TestGateway_InterpolateUndefinedVariableIsParseError(t *testing.T) {
	doc := strings.Replace(validGatewayDoc, "kind: GatewayMigration",
		"kind: GatewayMigration\ninterpolate: true", 1)
	doc = strings.Replace(doc, "    name: msk-to-cc", "    name: ${KCP_UNSET_LINK}", 1)
	_, err := ParseGatewayMigration([]byte(doc))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "KCP_UNSET_LINK")
}

// --- ParseKind dispatch ---

func TestParseKind_IdentifiesGatewayMigration(t *testing.T) {
	kind, err := ParseKind([]byte(validGatewayDoc))
	require.NoError(t, err)
	assert.Equal(t, KindGatewayMigration, kind)
}

func TestParseKind_IdentifiesMigration(t *testing.T) {
	kind, err := ParseKind(readFixture(t, "valid_cc.yaml"))
	require.NoError(t, err)
	assert.Equal(t, KindMigration, kind)
}

// TestParseKind_IgnoresUnknownFields — the envelope peek must not care about
// the body, or it could not produce a useful message for a wrong-kind file.
func TestParseKind_IgnoresUnknownFields(t *testing.T) {
	kind, err := ParseKind([]byte("apiVersion: kcp.confluent.io/v1alpha1\nkind: Migration\nanything: goes\n"))
	require.NoError(t, err)
	assert.Equal(t, KindMigration, kind)
}

func TestParseKind_ErrorsOnMissingKind(t *testing.T) {
	_, err := ParseKind([]byte("apiVersion: kcp.confluent.io/v1alpha1\n"))
	require.Error(t, err)
}

func TestParseKind_ErrorsOnMalformedYAML(t *testing.T) {
	_, err := ParseKind([]byte("\tnot: yaml: at all\n"))
	require.Error(t, err)
}

// TestParseGatewayMigration_RejectsMigrationKindWithAUsefulMessage — pointing
// `kcp migration init -f` at a kcp migrate manifest must say so, not produce a
// field-by-field unknown-key dump.
func TestParseGatewayMigration_RejectsMigrationKindWithAUsefulMessage(t *testing.T) {
	_, err := ParseGatewayMigration(readFixture(t, "valid_cc.yaml"))
	require.Error(t, err)
	msg := err.Error()
	assert.Contains(t, msg, KindMigration)
	assert.Contains(t, msg, KindGatewayMigration)
	assert.NotContains(t, msg, "unknown field", "a wrong-kind file must not produce an unknown-field dump")
}

func TestParse_RejectsGatewayMigrationKindWithAUsefulMessage(t *testing.T) {
	_, err := Parse([]byte(validGatewayDoc))
	require.Error(t, err)
	msg := err.Error()
	assert.Contains(t, msg, KindGatewayMigration)
	assert.NotContains(t, msg, "unknown field")
}

// --- credential persistence boundary ---

// TestGateway_ResolvedCredentialsAreNotOnTheManifestStruct is the highest-value
// test in the change (§7.2): inlining puts credentials one struct-copy away
// from being persisted into migration-state.json. Nothing that a caller may
// serialise may hold a resolved secret.
func TestGateway_ResolvedCredentialsAreNotOnTheManifestStruct(t *testing.T) {
	t.Setenv("MSK_PASSWORD", "resolved-secret-value")
	doc := strings.Replace(validGatewayDoc, "kind: GatewayMigration",
		"kind: GatewayMigration\ninterpolate: true", 1)
	doc = strings.Replace(doc, "        password: secret", "        password: ${MSK_PASSWORD}", 1)

	g := parseGateway(t, doc)
	creds, errs := g.SourceCredentials()
	require.Empty(t, errs)
	require.Equal(t, "resolved-secret-value", creds.SASLScram.Password, "resolution must have happened")

	rendered, err := renderStruct(g)
	require.NoError(t, err)
	assert.NotContains(t, rendered, "resolved-secret-value",
		"a resolved secret must never be reachable from the manifest struct a caller might persist")
}

func requireErrContains(t *testing.T, errs []error, want string) {
	t.Helper()
	require.NotEmpty(t, errs, "expected a validation error mentioning %q", want)
	var joined []string
	for _, e := range errs {
		joined = append(joined, e.Error())
	}
	assert.Contains(t, strings.Join(joined, "; "), want)
}

// renderStruct serialises everything reachable from the manifest struct,
// including the raw bytes of inline credential blocks, so the persistence-
// boundary test can assert a resolved secret appears nowhere in it.
func renderStruct(g *GatewayMigration) (string, error) {
	b, err := yaml.Marshal(g)
	if err != nil {
		return "", err
	}
	var sb strings.Builder
	sb.Write(b)
	sb.Write(g.Spec.Source.Credentials.Inline)
	if k := g.Spec.Target.Kafka; k != nil {
		sb.Write(k.Credentials.Inline)
		if k.RestCredentials != nil {
			sb.Write(k.RestCredentials.Inline)
		}
	}
	return sb.String(), nil
}

// TestGateway_OmittedScramMechanismDefaultsToSHA512 is a parity rule with a
// sharp edge. The retired --sasl-scram-mechanism defaulted to SHA512, so a
// source declared with --use-sasl-scram and no mechanism worked. The shared
// migrate credentials loader, by contrast, REQUIRES an explicit mechanism,
// because for a hand-written kcp migrate file the wrong default surfaces only
// as an opaque auth failure.
//
// Rejecting an omitted mechanism here would be a tightening: something
// accepted today would stop being accepted. So this kind defaults it — and
// SHA512 is also the only mechanism MSK serves.
func TestGateway_OmittedScramMechanismDefaultsToSHA512(t *testing.T) {
	doc := strings.Replace(validGatewayDoc, "        mechanism: SHA512\n", "", 1)
	g := parseGateway(t, doc)
	require.Empty(t, g.Validate())

	creds, errs := g.SourceCredentials()
	require.Empty(t, errs, "an omitted mechanism must not be rejected")
	assert.Equal(t, "SHA512", creds.SASLScram.Mechanism)
}

// TestGateway_ExplicitScramMechanismWins — the default only fills a gap.
func TestGateway_ExplicitScramMechanismWins(t *testing.T) {
	doc := strings.Replace(validGatewayDoc, "        mechanism: SHA512", "        mechanism: SHA256", 1)
	creds, errs := parseGateway(t, doc).SourceCredentials()
	require.Empty(t, errs)
	assert.Equal(t, "SHA256", creds.SASLScram.Mechanism)
}

// TestGateway_InvalidScramMechanismIsStillRejected — defaulting must not
// weaken the check; this is one of execute's three ported preRunE errors.
func TestGateway_InvalidScramMechanismIsStillRejected(t *testing.T) {
	doc := strings.Replace(validGatewayDoc, "        mechanism: SHA512", "        mechanism: SHA1", 1)
	_, errs := parseGateway(t, doc).SourceCredentials()
	require.NotEmpty(t, errs)
}

// TestGateway_ScramDefaultAppliesToAReferencedFileToo — the two spellings must
// not diverge, which is the whole point of the parse/validate split.
func TestGateway_ScramDefaultAppliesToAReferencedFileToo(t *testing.T) {
	p := filepath.Join(t.TempDir(), "creds.yaml")
	require.NoError(t, os.WriteFile(p,
		[]byte("sasl_scram:\n  username: admin\n  password: secret\n"), 0600))

	doc := strings.Replace(validGatewayDoc,
		"    credentials:\n      sasl_scram:\n        username: admin\n        password: secret\n        mechanism: SHA512",
		"    credentials: "+p, 1)
	creds, errs := parseGateway(t, doc).SourceCredentials()
	require.Empty(t, errs)
	assert.Equal(t, "SHA512", creds.SASLScram.Mechanism)
}

// TestMigrateKind_StillRequiresAnExplicitMechanism — the default is scoped to
// this kind; kcp migrate's stricter rule is unchanged.
func TestMigrateKind_StillRequiresAnExplicitMechanism(t *testing.T) {
	p := filepath.Join(t.TempDir(), "creds.yaml")
	require.NoError(t, os.WriteFile(p,
		[]byte("sasl_scram:\n  username: admin\n  password: secret\n"), 0600))
	_, errs := types.LoadMigrateClusterCredentials(p)
	require.NotEmpty(t, errs)
}
