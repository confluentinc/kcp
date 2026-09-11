package manifest

import (
	"bytes"
	"log/slog"
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

// topicGroupBlock is the topicGroup stanza in the canonical doc; tests replace
// it to vary spec.topicGroup.
const topicGroupBlock = `  topicGroup:
    - topics:
        - t1.order
        - t1.inventory
      route: migration-route
      targetStreamingDomain: confluent-cloud
`

// validGatewayDoc is the canonical manifest from the design §4, minus the
// optional blocks. Every credentials slot is a file path; validation does no
// I/O, so these placeholder paths satisfy the structural rules. Tests that
// RESOLVE credentials use gatewayDocWithCreds to point the slots at real files.
const validGatewayDoc = `apiVersion: kcp.confluent.io/v1alpha1
kind: GatewayMigration
metadata:
  name: msk-prod-to-cc-batch-1
spec:
  source:
    type: msk
    bootstrapServers:
      - b-1.msk-prod.abc123.c2.kafka.us-east-1.amazonaws.com:9096
    credentials: ./source-creds.yaml
  target:
    type: confluent-cloud
    clusterId: lkc-abc123
    kafka:
      bootstrapServers:
        - pkc-xxxxx.us-east-1.aws.confluent.cloud:9092
      restEndpoint: https://pkc-xxxxx.us-east-1.aws.confluent.cloud:443
      clusterCredentials: ./dest-kafka-creds.yaml
  clusterLink:
    name: msk-to-cc
    bootstrapServers:
      - pkc-xxxxx.us-east-1.aws.confluent.cloud:9092
    linkCredentials: ./link-creds.yaml
  gateway:
    namespace: confluent
    cr-name: gateway-initial
` + topicGroupBlock

// Default credential-file bodies for the canonical document.
const (
	defaultSourceCreds   = "sasl_scram:\n  username: admin\n  password: secret\n  mechanism: SHA512\n"
	defaultDestKafkaCred = "sasl_plain:\n  username: CC_KEY\n  password: CC_SECRET\n  tls: true\n"
	defaultLinkCred      = "api_key: CC_KEY\napi_secret: CC_SECRET\n"
)

func parseGateway(t *testing.T, doc string) *GatewayMigration {
	t.Helper()
	g, err := ParseGatewayMigration([]byte(doc))
	require.NoError(t, err)
	return g
}

// gatewayDocWithCreds writes the three credential files into a temp dir (using
// the given bodies, or the canonical defaults for empty ones) and returns a
// validGatewayDoc whose slots point at them, so the resolver methods can read
// them. Slots a test does not resolve keep working from the default files.
func gatewayDocWithCreds(t *testing.T, source, destKafka, link string) string {
	t.Helper()
	dir := t.TempDir()
	write := func(name, body, def string) string {
		if body == "" {
			body = def
		}
		p := filepath.Join(dir, name)
		require.NoError(t, os.WriteFile(p, []byte(body), 0600))
		return p
	}
	doc := validGatewayDoc
	doc = strings.Replace(doc, "credentials: ./source-creds.yaml",
		"credentials: "+write("source.yaml", source, defaultSourceCreds), 1)
	doc = strings.Replace(doc, "clusterCredentials: ./dest-kafka-creds.yaml",
		"clusterCredentials: "+write("dest-kafka.yaml", destKafka, defaultDestKafkaCred), 1)
	doc = strings.Replace(doc, "linkCredentials: ./link-creds.yaml",
		"linkCredentials: "+write("link.yaml", link, defaultLinkCred), 1)
	return doc
}

// withField replaces the first occurrence of old with new in the canonical doc.
func withField(t *testing.T, old, new string) string {
	t.Helper()
	require.Contains(t, validGatewayDoc, old, "fixture drift: %q not in the canonical doc", old)
	return strings.Replace(validGatewayDoc, old, new, 1)
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
	assert.Equal(t, []string{"pkc-xxxxx.us-east-1.aws.confluent.cloud:9092"}, g.Spec.ClusterLink.BootstrapServers)
	assert.Equal(t, "./link-creds.yaml", g.Spec.ClusterLink.LinkCredentials.Path)
	assert.Equal(t, "./dest-kafka-creds.yaml", g.Spec.Target.Kafka.ClusterCredentials.Path)
	assert.Equal(t, "confluent", g.Spec.Gateway.Namespace)
	assert.Equal(t, "gateway-initial", g.Spec.Gateway.CrName)
	require.Len(t, g.Spec.TopicGroup, 1)
	assert.Equal(t, "migration-route", g.Spec.TopicGroup[0].Route)
	assert.Equal(t, "confluent-cloud", g.Spec.TopicGroup[0].TargetStreamingDomain)
	require.NotNil(t, g.Spec.TopicGroup[0].Topics)
	assert.Equal(t, []string{"t1.order", "t1.inventory"}, *g.Spec.TopicGroup[0].Topics)
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
// working configuration. The gate runs when the source credentials resolve
// (Validate does no I/O), so it surfaces on SourceCredentials().
func TestGateway_RejectsIAMOnApacheKafkaSource(t *testing.T) {
	doc := gatewayDocWithCreds(t, "iam:\n  region: us-east-1\n", "", "")
	doc = strings.Replace(doc, "    type: msk", "    type: apache-kafka", 1)
	_, errs := parseGateway(t, doc).SourceCredentials()
	requireErrContains(t, errs, "iam")
}

func TestGateway_AcceptsIAMOnMSKSource(t *testing.T) {
	doc := gatewayDocWithCreds(t, "iam:\n  region: us-east-1\n", "", "")
	_, errs := parseGateway(t, doc).SourceCredentials()
	require.Empty(t, errs)
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
	doc := strings.Replace(validGatewayDoc, "    credentials: ./source-creds.yaml", "    credentials: \"\"", 1)
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

func TestGateway_RequiresTargetKafkaClusterCredentials(t *testing.T) {
	doc := strings.Replace(validGatewayDoc,
		"      clusterCredentials: ./dest-kafka-creds.yaml", "      clusterCredentials: \"\"", 1)
	g := parseGateway(t, doc)
	requireErrContains(t, g.Validate(), "spec.target.kafka.clusterCredentials")
}

// TestGateway_AcceptsEveryDestinationMethodExceptIAM. All the auth/TLS
// machinery on the destination Kafka leg is routed through
// AdminOptionForAuthMethod, the same mapper the source leg uses — so sasl_scram,
// mtls, and both unauthenticated forms resolve like any other leg.
func TestGateway_AcceptsEveryDestinationMethodExceptIAM(t *testing.T) {
	certDir := t.TempDir()
	cert := filepath.Join(certDir, "client.pem")
	key := filepath.Join(certDir, "client-key.pem")
	require.NoError(t, os.WriteFile(cert, []byte("cert"), 0600))
	require.NoError(t, os.WriteFile(key, []byte("key"), 0600))

	for name, block := range map[string]string{
		"sasl_scram":                "sasl_scram:\n  username: u\n  password: p\n  mechanism: SHA512\n",
		"mtls":                      "mtls:\n  client_cert: " + cert + "\n  client_key: " + key + "\n",
		"unauthenticated_plaintext": "unauthenticated_plaintext: {}\n",
		"unauthenticated_tls":       "unauthenticated_tls: {}\n",
	} {
		t.Run(name, func(t *testing.T) {
			doc := gatewayDocWithCreds(t, "", block, "")
			g := parseGateway(t, doc)
			require.Empty(t, g.Validate())
			_, errs := g.DestinationKafkaCredentials()
			require.Empty(t, errs)
		})
	}
}

// TestGateway_RejectsIAMDestination. iam is MSK-only (SigV4 signing against
// AWS), and the destination is Confluent Cloud or Confluent Platform — never
// MSK — so it would otherwise fail opaquely at connection time.
func TestGateway_RejectsIAMDestination(t *testing.T) {
	doc := gatewayDocWithCreds(t, "", "iam:\n  region: us-east-1\n", "")
	_, errs := parseGateway(t, doc).DestinationKafkaCredentials()
	requireErrContains(t, errs, "iam")
}

// TestGateway_AllowsDestinationSASLPlainCACert — a private-CA sasl_plain
// destination now that createDestinationOffset routes through
// AdminOptionForAuthMethod instead of a hardcoded empty-CA client.
func TestGateway_AllowsDestinationSASLPlainCACert(t *testing.T) {
	ca := filepath.Join(t.TempDir(), "dest-ca.pem")
	require.NoError(t, os.WriteFile(ca, []byte("pem"), 0600))

	doc := gatewayDocWithCreds(t, "",
		"sasl_plain:\n  username: CC_KEY\n  password: CC_SECRET\n  ca_cert: "+ca+"\n", "")
	mc, errs := parseGateway(t, doc).DestinationKafkaCredentials()
	require.Empty(t, errs)
	assert.Equal(t, ca, mc.SASLPlain.CACert)
}

// --- cluster-link REST credentials (linkCredentials) ---

// TestGateway_RequiresLinkCredentials — the cluster-link REST credential is
// always required; there is no derivation from the Kafka leg (R4).
func TestGateway_RequiresLinkCredentials(t *testing.T) {
	doc := strings.Replace(validGatewayDoc,
		"    linkCredentials: ./link-creds.yaml", "    linkCredentials: \"\"", 1)
	g := parseGateway(t, doc)
	requireErrContains(t, g.Validate(), "spec.clusterLink.linkCredentials")
}

// TestGateway_LinkCredentialsRequiredEvenUnderSaslPlain — the old shortcut that
// let restCredentials be omitted when the Kafka leg was sasl_plain is gone: an
// omitted linkCredentials fails validation regardless of the Kafka leg (AE3).
func TestGateway_LinkCredentialsRequiredEvenUnderSaslPlain(t *testing.T) {
	doc := strings.Replace(validGatewayDoc,
		"    linkCredentials: ./link-creds.yaml", "    linkCredentials: \"\"", 1)
	// The default target Kafka leg is sasl_plain; omitting linkCredentials must
	// still fail — no auto-derivation fallback.
	g := parseGateway(t, doc)
	requireErrContains(t, g.Validate(), "spec.clusterLink.linkCredentials")
}

// TestGateway_RestCredentialsResolveFromClusterLink — RestCredentials() reads
// spec.clusterLink.linkCredentials, never anything under spec.target.kafka (AE2).
func TestGateway_RestCredentialsResolveFromClusterLink(t *testing.T) {
	// A distinctive link credential, and a target Kafka leg that is NOT
	// api_key-shaped, so a value read from target.kafka could not masquerade as
	// the REST leg.
	doc := gatewayDocWithCreds(t,
		"", // source default
		"sasl_scram:\n  username: u\n  password: p\n  mechanism: SHA512\n",
		"api_key: LINK_KEY\napi_secret: LINK_SECRET\n")
	rest, err := parseGateway(t, doc).RestCredentials()
	require.NoError(t, err)
	assert.Equal(t, "LINK_KEY", rest.APIKey)
	assert.Equal(t, "LINK_SECRET", rest.APISecret)
}

// TestGateway_AcceptsEveryRestCredentialsForm. targets.Credentials implements
// Authenticator/HTTPClient for basic, bearer, mtls and api_key, so a CP
// destination behind RBAC or mTLS can be reached via linkCredentials.
func TestGateway_AcceptsEveryRestCredentialsForm(t *testing.T) {
	certDir := t.TempDir()
	cert := filepath.Join(certDir, "client.pem")
	key := filepath.Join(certDir, "client-key.pem")
	require.NoError(t, os.WriteFile(cert, []byte("cert"), 0600))
	require.NoError(t, os.WriteFile(key, []byte("key"), 0600))

	for name, body := range map[string]string{
		"api_key": "api_key: K\napi_secret: S\n",
		"bearer":  "bearer:\n  token: TOK\n",
		"basic":   "basic:\n  username: u\n  password: p\n",
		"mtls":    "mtls:\n  client_cert: " + cert + "\n  client_key: " + key + "\n",
	} {
		t.Run(name, func(t *testing.T) {
			g := parseGateway(t, gatewayDocWithCreds(t, "", "", body))
			require.Empty(t, g.Validate())
			_, err := g.RestCredentials()
			require.NoError(t, err)
		})
	}
}

// TestGateway_RestCredentialsAcceptPrivateCA — a linkCredentials api_key form
// with a private CA resolves and carries the CA through.
func TestGateway_RestCredentialsAcceptPrivateCA(t *testing.T) {
	ca := filepath.Join(t.TempDir(), "dest-ca.pem")
	require.NoError(t, os.WriteFile(ca, []byte("pem"), 0600))

	doc := gatewayDocWithCreds(t, "", "", "api_key: K\napi_secret: S\nca_cert: "+ca+"\n")
	rest, err := parseGateway(t, doc).RestCredentials()
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

func TestGateway_RequiresCrName(t *testing.T) {
	g := parseGateway(t, strings.Replace(validGatewayDoc, "    cr-name: gateway-initial\n", "", 1))
	requireErrContains(t, g.Validate(), "spec.gateway.cr-name")
}

// TestGateway_RejectsRetiredKeys — the old crs/routes/topics shape is removed
// from the struct, so a stale manifest that still uses any of them fails at the
// strict decode with an unknown-field error. This is where the retired
// crs.switchover rejection now lives: a manifest setting it trips the crs
// unknown-field error rather than a bespoke migration hint (an accepted UX
// downgrade — see the plan's Risks).
func TestGateway_RejectsRetiredKeys(t *testing.T) {
	withGatewayKey := func(block string) string {
		return strings.Replace(validGatewayDoc, "    cr-name: gateway-initial\n",
			"    cr-name: gateway-initial\n"+block, 1)
	}
	for name, doc := range map[string]string{
		"crs":            withGatewayKey("    crs:\n      initial: gateway-initial\n"),
		"crs.switchover": withGatewayKey("    crs:\n      switchover: /etc/kcp/switchover.yaml\n"),
		"gateway.routes": withGatewayKey("    routes:\n      - name: migration-route\n"),
		"spec.topics":    validGatewayDoc + "  topics: ['t1.order']\n",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := ParseGatewayMigration([]byte(doc))
			require.Error(t, err, "a retired key must fail the strict decode")
		})
	}
}

// --- topicGroup ---

// TestGateway_RequiresExactlyOneTopicGroupEntry — the doc pins one route, one
// mode per migration, so both an absent block and more than one entry are
// rejected. (>1 entry is future multi-route work, not this piece.)
func TestGateway_RequiresExactlyOneTopicGroupEntry(t *testing.T) {
	t.Run("omitted", func(t *testing.T) {
		g := parseGateway(t, strings.Replace(validGatewayDoc, topicGroupBlock, "", 1))
		requireErrContains(t, g.Validate(), "spec.topicGroup")
	})
	t.Run("empty list", func(t *testing.T) {
		g := parseGateway(t, strings.Replace(validGatewayDoc, topicGroupBlock, "  topicGroup: []\n", 1))
		requireErrContains(t, g.Validate(), "spec.topicGroup")
	})
	t.Run("more than one", func(t *testing.T) {
		second := "    - topics:\n        - t2.orders\n      route: second-route\n      targetStreamingDomain: confluent-cloud\n"
		g := parseGateway(t, strings.Replace(validGatewayDoc, topicGroupBlock, topicGroupBlock+second, 1))
		requireErrContains(t, g.Validate(), "spec.topicGroup")
	})
}

// TestGateway_RejectsBlankRoute — a blank route name matches nothing and would
// fail at fence time; catch it at parse.
func TestGateway_RejectsBlankRoute(t *testing.T) {
	g := parseGateway(t, strings.Replace(validGatewayDoc,
		"      route: migration-route\n", "      route: \"\"\n", 1))
	requireErrContains(t, g.Validate(), "spec.topicGroup")
}

// TestGateway_RejectsBlankTargetStreamingDomain — an empty target domain name
// matches nothing in the initial CR and would fail at switch time; catch it at
// parse.
func TestGateway_RejectsBlankTargetStreamingDomain(t *testing.T) {
	g := parseGateway(t, strings.Replace(validGatewayDoc,
		"      targetStreamingDomain: confluent-cloud\n", "      targetStreamingDomain: \"\"\n", 1))
	requireErrContains(t, g.Validate(), "spec.topicGroup")
}

// TestGateway_RejectsEntryWithNeitherTopicsNorPatterns requires at least one of
// topics/topicPatterns is required on every entry (both modes). Both absent is
// a structural error, no mode knowledge needed.
func TestGateway_RejectsEntryWithNeitherTopicsNorPatterns(t *testing.T) {
	block := "  topicGroup:\n    - route: migration-route\n      targetStreamingDomain: confluent-cloud\n"
	g := parseGateway(t, strings.Replace(validGatewayDoc, topicGroupBlock, block, 1))
	requireErrContains(t, g.Validate(), "spec.topicGroup")
}

// TestGateway_TopicsPresentButEmptyIsRejected — an explicitly empty topics list
// means the opposite of "all topics" and is rejected, matching the old
// spec.topics semantics.
func TestGateway_TopicsPresentButEmptyIsRejected(t *testing.T) {
	block := "  topicGroup:\n    - topics: []\n      route: migration-route\n      targetStreamingDomain: confluent-cloud\n"
	g := parseGateway(t, strings.Replace(validGatewayDoc, topicGroupBlock, block, 1))
	requireErrContains(t, g.Validate(), "spec.topicGroup")
}

func TestGateway_RejectsBlankTopicName(t *testing.T) {
	block := "  topicGroup:\n    - topics:\n        - t1.order\n        - '  '\n      route: migration-route\n      targetStreamingDomain: confluent-cloud\n"
	g := parseGateway(t, strings.Replace(validGatewayDoc, topicGroupBlock, block, 1))
	requireErrContains(t, g.Validate(), "spec.topicGroup")
}

// TestGateway_TopicsOnlyValidates — topics set, topicPatterns omitted, is the
// canonical shape and must validate clean.
func TestGateway_TopicsOnlyValidates(t *testing.T) {
	g := parseGateway(t, validGatewayDoc)
	require.Empty(t, g.Validate())
	require.NotNil(t, g.Spec.TopicGroup[0].Topics)
	assert.Nil(t, g.Spec.TopicGroup[0].TopicPatterns)
}

// TestGateway_TopicPatternsOnlyValidates — topicPatterns set, topics omitted,
// satisfies the at-least-one rule on its own.
func TestGateway_TopicPatternsOnlyValidates(t *testing.T) {
	block := "  topicGroup:\n    - topicPatterns:\n        - 'orders\\..*'\n      route: migration-route\n      targetStreamingDomain: confluent-cloud\n"
	g := parseGateway(t, strings.Replace(validGatewayDoc, topicGroupBlock, block, 1))
	require.Empty(t, g.Validate())
	require.NotNil(t, g.Spec.TopicGroup[0].TopicPatterns)
	assert.Nil(t, g.Spec.TopicGroup[0].Topics)
}

// TestGateway_TopicPatternsPresentButEmptyIsRejected mirrors the topics case.
func TestGateway_TopicPatternsPresentButEmptyIsRejected(t *testing.T) {
	block := "  topicGroup:\n    - topicPatterns: []\n      route: migration-route\n      targetStreamingDomain: confluent-cloud\n"
	g := parseGateway(t, strings.Replace(validGatewayDoc, topicGroupBlock, block, 1))
	requireErrContains(t, g.Validate(), "spec.topicGroup")
}

func TestGateway_RejectsBlankTopicPattern(t *testing.T) {
	block := "  topicGroup:\n    - topicPatterns:\n        - 'orders\\..*'\n        - '  '\n      route: migration-route\n      targetStreamingDomain: confluent-cloud\n"
	g := parseGateway(t, strings.Replace(validGatewayDoc, topicGroupBlock, block, 1))
	requireErrContains(t, g.Validate(), "spec.topicGroup")
}

// TestGateway_RejectsInvalidTopicPatternRegex — each pattern must compile
// as an anchored RE2 full-match, a cheap guard mirroring the Gateway's
// parse-time rejection. A bare `*` has nothing to repeat and fails to compile.
func TestGateway_RejectsInvalidTopicPatternRegex(t *testing.T) {
	block := "  topicGroup:\n    - topicPatterns:\n        - '*'\n      route: migration-route\n      targetStreamingDomain: confluent-cloud\n"
	g := parseGateway(t, strings.Replace(validGatewayDoc, topicGroupBlock, block, 1))
	requireErrContains(t, g.Validate(), "spec.topicGroup")
}

// TestGateway_RejectsAnchorEscapingPattern rejects a
// pattern carrying an unbalanced paren (e.g. "foo)|(evil") must be rejected —
// not silently spliced into \A(?:…)\z where its ")" closes the wrapper group and
// promotes a top-level alternation, escaping the intended full-match anchor. The
// pattern is validated on its own terms first, so a malformed one is rejected.
func TestGateway_RejectsAnchorEscapingPattern(t *testing.T) {
	block := "  topicGroup:\n    - topicPatterns:\n        - 'foo)|(evil'\n      route: migration-route\n      targetStreamingDomain: confluent-cloud\n"
	g := parseGateway(t, strings.Replace(validGatewayDoc, topicGroupBlock, block, 1))
	requireErrContains(t, g.Validate(), "spec.topicGroup")
}

// TestAnchoredPattern_IsAFullMatch pins the anchoring: a compiled topicPattern
// must match the whole topic name, never a prefix or suffix, and a pattern that
// tries to break out of the wrapper group must be refused rather than compiled
// into a partial match.
func TestAnchoredPattern_IsAFullMatch(t *testing.T) {
	re, err := anchoredPattern("orders")
	require.NoError(t, err)
	assert.True(t, re.MatchString("orders"))
	assert.False(t, re.MatchString("orders.v2"), "must not match a superstring")
	assert.False(t, re.MatchString("my-orders"), "must not match a prefix-extended string")

	_, err = anchoredPattern("foo)|(evil")
	require.Error(t, err, "an anchor-escaping pattern must be refused")
}

// TestGateway_MatchAllTopicPatternCompiles — the "all topics" token is `.*`
// which must compile cleanly (a bare `*` would not).
func TestGateway_MatchAllTopicPatternCompiles(t *testing.T) {
	block := "  topicGroup:\n    - topicPatterns:\n        - '.*'\n      route: migration-route\n      targetStreamingDomain: confluent-cloud\n"
	g := parseGateway(t, strings.Replace(validGatewayDoc, topicGroupBlock, block, 1))
	require.Empty(t, g.Validate())
}

// TestGateway_BothTopicsAndPatternsIsAllowed — both set is a combined set,
// not a structural error, on either mode.
func TestGateway_BothTopicsAndPatternsIsAllowed(t *testing.T) {
	block := "  topicGroup:\n    - topics:\n        - t1.order\n      topicPatterns:\n        - 'orders\\..*'\n      route: migration-route\n      targetStreamingDomain: confluent-cloud\n"
	g := parseGateway(t, strings.Replace(validGatewayDoc, topicGroupBlock, block, 1))
	require.Empty(t, g.Validate())
	assert.NotNil(t, g.Spec.TopicGroup[0].Topics)
	assert.NotNil(t, g.Spec.TopicGroup[0].TopicPatterns)
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

func TestGateway_PolicyDurationsParseAsDurationStrings(t *testing.T) {
	doc := validGatewayDoc + `  defaultPolicies:
    lagThreshold: 0
    promoteBatchSize: 100
    rolloutTimeout: 10m
    detectUnroutedProducersDuration: 30s
    consumerOffsetSyncDrainDuration: 15s
`
	g := parseGateway(t, doc)
	require.Empty(t, g.Validate())
	assert.Equal(t, 100, g.Spec.DefaultPolicies.PromoteBatchSize)
	assert.Equal(t, 10*time.Minute, g.Spec.DefaultPolicies.RolloutTimeout)
	assert.Equal(t, 30*time.Second, g.Spec.DefaultPolicies.DetectUnroutedProducersDuration)
	assert.Equal(t, 15*time.Second, g.Spec.DefaultPolicies.ConsumerOffsetSyncDrainDuration)
}

// TestGateway_PolicyOmittedIsZeroValued — every policy knob is optional and 0
// carries meaning (0 = promote all at once / no deadline / check skipped).
func TestGateway_PolicyOmittedIsZeroValued(t *testing.T) {
	g := parseGateway(t, validGatewayDoc)
	require.Empty(t, g.Validate())
	assert.Equal(t, DefaultPolicies{}, g.Spec.DefaultPolicies)
}

// TestGateway_RejectsSubTenSecondDetectDuration mirrors the retired flag's
// documented minimum, which existed because a shorter window cannot observe a
// producer's metadata refresh.
func TestGateway_RejectsSubTenSecondDetectDuration(t *testing.T) {
	g := parseGateway(t, validGatewayDoc+"  defaultPolicies:\n    detectUnroutedProducersDuration: 5s\n")
	requireErrContains(t, g.Validate(), "detectUnroutedProducersDuration")
}

func TestGateway_AcceptsZeroDetectDurationMeaningSkipped(t *testing.T) {
	g := parseGateway(t, validGatewayDoc+"  defaultPolicies:\n    detectUnroutedProducersDuration: 0s\n")
	require.Empty(t, g.Validate())
}

func TestGateway_RejectsNegativePolicyNumbers(t *testing.T) {
	for _, line := range []string{"    lagThreshold: -1", "    promoteBatchSize: -1"} {
		t.Run(strings.TrimSpace(line), func(t *testing.T) {
			g := parseGateway(t, validGatewayDoc+"  defaultPolicies:\n"+line+"\n")
			require.NotEmpty(t, g.Validate())
		})
	}
}

// --- no environment substitution (the interpolate mechanism is removed) ---

// TestGateway_RejectsTopLevelInterpolateKey — the retired top-level interpolate:
// field is now an unknown field, so a stale manifest that still sets it fails the
// strict decode rather than being silently ignored.
func TestGateway_RejectsTopLevelInterpolateKey(t *testing.T) {
	doc := strings.Replace(validGatewayDoc, "kind: GatewayMigration",
		"kind: GatewayMigration\ninterpolate: true", 1)
	_, err := ParseGatewayMigration([]byte(doc))
	require.Error(t, err, "interpolate: is no longer a valid manifest field")
}

// TestGateway_NoEnvSubstitution — a literal ${VAR}-shaped string in a manifest
// field is kept verbatim; there is no environment-variable substitution.
func TestGateway_NoEnvSubstitution(t *testing.T) {
	t.Setenv("LINK_NAME", "msk-to-cc-prod")
	g := parseGateway(t, strings.Replace(validGatewayDoc, "    name: msk-to-cc", "    name: ${LINK_NAME}", 1))
	assert.Equal(t, "${LINK_NAME}", g.Spec.ClusterLink.Name)
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

// TestGateway_ResolvedCredentialsAreNotOnTheManifestStruct is a high-value
// regression guard (§7.2): a resolved secret must never be reachable from the
// manifest struct a caller might persist into migration-state.json. Credentials
// are file paths, so the struct only ever holds the path — this test fails if
// CredentialsRef ever regains a field that caches resolved secret material.
func TestGateway_ResolvedCredentialsAreNotOnTheManifestStruct(t *testing.T) {
	doc := gatewayDocWithCreds(t,
		"sasl_scram:\n  username: admin\n  password: resolved-secret-value\n  mechanism: SHA512\n", "", "")
	g := parseGateway(t, doc)
	creds, errs := g.SourceCredentials()
	require.Empty(t, errs)
	require.Equal(t, "resolved-secret-value", creds.SASLScram.Password, "resolution must have happened")

	rendered, err := yaml.Marshal(g)
	require.NoError(t, err)
	assert.NotContains(t, string(rendered), "resolved-secret-value",
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

// TestGateway_OmittedScramMechanismIsRejected — the gateway kind follows the
// same rule as kcp migrate: an omitted mechanism is rejected rather than
// silently defaulted. Inferring SHA512 hid a wrong guess (a SHA256 source)
// behind an opaque auth failure, so the config file must state the mechanism.
//
// The rejection surfaces on SourceCredentials() (which reads the file), not
// Validate(), which does no I/O.
func TestGateway_OmittedScramMechanismIsRejected(t *testing.T) {
	doc := gatewayDocWithCreds(t, "sasl_scram:\n  username: admin\n  password: secret\n", "", "")
	_, errs := parseGateway(t, doc).SourceCredentials()
	require.NotEmpty(t, errs, "an omitted mechanism must be rejected")
}

// TestGateway_ExplicitScramMechanismIsAccepted — an explicit mechanism resolves
// through unchanged.
func TestGateway_ExplicitScramMechanismIsAccepted(t *testing.T) {
	doc := gatewayDocWithCreds(t, "sasl_scram:\n  username: admin\n  password: secret\n  mechanism: SHA256\n", "", "")
	creds, errs := parseGateway(t, doc).SourceCredentials()
	require.Empty(t, errs)
	assert.Equal(t, "SHA256", creds.SASLScram.Mechanism)
}

// TestGateway_InvalidScramMechanismIsStillRejected — an unsupported mechanism
// is rejected; this is one of execute's three ported preRunE errors.
func TestGateway_InvalidScramMechanismIsStillRejected(t *testing.T) {
	doc := gatewayDocWithCreds(t, "sasl_scram:\n  username: admin\n  password: secret\n  mechanism: SHA1\n", "", "")
	_, errs := parseGateway(t, doc).SourceCredentials()
	require.NotEmpty(t, errs)
}

// TestMigrateKind_RequiresAnExplicitMechanism — the migrate kind requires an
// explicit mechanism, the same rule the gateway kind now follows.
func TestMigrateKind_RequiresAnExplicitMechanism(t *testing.T) {
	p := filepath.Join(t.TempDir(), "creds.yaml")
	require.NoError(t, os.WriteFile(p,
		[]byte("sasl_scram:\n  username: admin\n  password: secret\n"), 0600))
	_, errs := types.LoadMigrateClusterCredentials(p)
	require.NotEmpty(t, errs)
}

// --- security review F2/F4: TLS trust is per-leg, not one global boolean ---

// TestGateway_InsecureSkipIsPerLeg. Each leg is its own credentials file, so
// relaxing verification for a self-signed SOURCE must not disable it on the
// destination legs, which carry the destination API key.
func TestGateway_InsecureSkipIsPerLeg(t *testing.T) {
	doc := gatewayDocWithCreds(t,
		"insecure_skip_tls_verify: true\nsasl_scram:\n  username: admin\n  password: secret\n  mechanism: SHA512\n",
		"", "")
	g := parseGateway(t, doc)

	src, errs := g.SourceCredentials()
	require.Empty(t, errs)
	assert.True(t, src.InsecureSkipTLSVerify)

	dst, errs := g.DestinationKafkaCredentials()
	require.Empty(t, errs)
	assert.False(t, dst.InsecureSkipTLSVerify, "the source's relaxation must not reach the destination Kafka leg")

	rest, err := g.RestCredentials()
	require.NoError(t, err)
	assert.False(t, rest.InsecureSkipVerify, "nor the destination REST leg")
}

// --- security review F7: the permission warning must cover every reader ---

// TestLoadGatewayMigrationFile_WarnsOnLoosePermissions. The same secret-bearing
// manifest is read by init, execute and lag-check; a warning wired into only one
// of them misses an operator who tightens permissions after init, or who only
// ever runs lag-check.
func TestLoadGatewayMigrationFile_WarnsOnLoosePermissions(t *testing.T) {
	p := filepath.Join(t.TempDir(), "gateway-migration.yaml")
	require.NoError(t, os.WriteFile(p, []byte(validGatewayDoc), 0644))

	var buf bytes.Buffer
	restore := captureSlog(t, &buf)
	g, err := LoadGatewayMigrationFile(p)
	restore()

	require.NoError(t, err)
	require.NotNil(t, g)
	assert.Contains(t, buf.String(), "group- or world-readable")
}

func TestLoadGatewayMigrationFile_QuietOnTightPermissions(t *testing.T) {
	p := filepath.Join(t.TempDir(), "gateway-migration.yaml")
	require.NoError(t, os.WriteFile(p, []byte(validGatewayDoc), 0600))

	var buf bytes.Buffer
	restore := captureSlog(t, &buf)
	_, err := LoadGatewayMigrationFile(p)
	restore()

	require.NoError(t, err)
	assert.NotContains(t, buf.String(), "group- or world-readable")
}

// TestLoadGatewayMigrationFile_ReturnsAllValidationProblems — an operator fixes
// the file in one pass rather than one error per run.
func TestLoadGatewayMigrationFile_ReturnsAllValidationProblems(t *testing.T) {
	doc := strings.Replace(validGatewayDoc, "    name: msk-to-cc", "    name: \"\"", 1)
	doc = strings.Replace(doc, "    namespace: confluent", "    namespace: \"\"", 1)
	p := filepath.Join(t.TempDir(), "gateway-migration.yaml")
	require.NoError(t, os.WriteFile(p, []byte(doc), 0600))

	_, err := LoadGatewayMigrationFile(p)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "spec.clusterLink.name")
	assert.Contains(t, err.Error(), "spec.gateway.namespace")
}

func TestLoadGatewayMigrationFile_RejectsWrongKind(t *testing.T) {
	p := filepath.Join(t.TempDir(), "migration.yaml")
	require.NoError(t, os.WriteFile(p, []byte(
		"apiVersion: kcp.confluent.io/v1alpha1\nkind: Migration\nmetadata:\n  name: x\n"), 0600))
	_, err := LoadGatewayMigrationFile(p)
	require.Error(t, err)
	assert.Contains(t, err.Error(), KindGatewayMigration)
}

// captureSlog redirects the default logger into buf and returns a restore func.
func captureSlog(t *testing.T, buf *bytes.Buffer) func() {
	t.Helper()
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	return func() { slog.SetDefault(prev) }
}
