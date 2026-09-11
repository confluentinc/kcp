package migration_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/confluentinc/kcp/internal/manifest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// baselineOpts is the manifest shape every converted e2e scenario starts from:
// a plaintext CFK source, a CFK destination on SASL/PLAIN over a self-signed CA,
// and a plain-HTTP REST endpoint. Values mirror what setup.sh emits into .env.
// The credential paths are placeholders good enough for structural validation;
// tests that RESOLVE credentials call withRenderedCreds to point them at real
// files.
func baselineOpts() manifestOpts {
	return manifestOpts{
		MetadataName:      "e2e-baseline",
		SourceBootstrap:   "source-kafka.confluent.svc.cluster.local:9071",
		DestBootstrap:     "destination-kafka.confluent.svc.cluster.local:9071",
		DestClusterID:     "abc123def456",
		RestEndpoint:      "http://destination-kafka.confluent.svc.cluster.local:8090",
		ClusterLinkName:   "e2e-link-baseline",
		APIKey:            "testuser",
		APISecret:         "testpassword",
		Namespace:         "confluent",
		GatewayName:       "migration-gateway-baseline",
		FenceRoutes:       []fenceRouteOpts{{Name: "migration-route", SwitchoverDomainName: "destination-kafka-cluster"}},
		KubePath:          "/workspace/kubeconfig",
		SourceCredPath:    "/workspace/source-creds.yaml",
		DestKafkaCredPath: "/workspace/dest-kafka-creds.yaml",
		LinkCredPath:      "/workspace/link-creds.yaml",
	}
}

// withRenderedCreds writes the three credentials files into a temp dir and points
// opts' path fields at them, so the rendered manifest resolves every leg — the
// local-filesystem equivalent of what writeManifestToPod does in the pod.
func withRenderedCreds(t *testing.T, opts manifestOpts) manifestOpts {
	t.Helper()
	files, err := renderCredentialFiles(opts)
	require.NoError(t, err)
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		require.NoError(t, os.WriteFile(p, []byte(body), 0600))
		return p
	}
	opts.SourceCredPath = write("source-creds.yaml", files.Source)
	opts.DestKafkaCredPath = write("dest-kafka-creds.yaml", files.DestKafka)
	opts.LinkCredPath = write("link-creds.yaml", files.Link)
	return opts
}

// TestRenderGatewayMigration_ParsesValidatesAndResolves is the guard that moves
// "the fixture is a valid manifest" from minute 30 of a Minikube run to a
// millisecond. It resolves all three credential legs from the rendered files —
// the same three resolvers cmd_migration_init.go and cmd_migration_execute.go call.
func TestRenderGatewayMigration_ParsesValidatesAndResolves(t *testing.T) {
	rendered, err := renderGatewayMigration(withRenderedCreds(t, baselineOpts()))
	require.NoError(t, err, "rendering the fixture must not fail")

	g, err := manifest.ParseGatewayMigration([]byte(rendered))
	require.NoError(t, err, "the rendered fixture must parse under the strict parser")
	require.Empty(t, g.Validate(), "the rendered fixture must validate")

	_, srcErrs := g.SourceCredentials()
	require.Empty(t, srcErrs, "spec.source.credentials must resolve")

	_, dstErrs := g.DestinationKafkaCredentials()
	require.Empty(t, dstErrs, "spec.target.kafka.clusterCredentials must resolve")

	_, restErr := g.RestCredentials()
	require.NoError(t, restErr, "the cluster-link REST leg must resolve from clusterLink.linkCredentials")
}

// TestRenderGatewayMigration_TopologyMatchesOpts guards against a transposition
// in the template — two fields of the same YAML type swapped still parses,
// validates and resolves, so nothing above would catch it.
func TestRenderGatewayMigration_TopologyMatchesOpts(t *testing.T) {
	opts := withRenderedCreds(t, baselineOpts())

	rendered, err := renderGatewayMigration(opts)
	require.NoError(t, err)
	g, err := manifest.ParseGatewayMigration([]byte(rendered))
	require.NoError(t, err)

	assert.Equal(t, opts.MetadataName, g.Metadata.Name)
	assert.Equal(t, []string{opts.SourceBootstrap}, g.Spec.Source.BootstrapServers)
	assert.Equal(t, "apache-kafka", g.Spec.Source.Type)

	assert.Equal(t, "confluent-platform", g.Spec.Target.Type)
	assert.Equal(t, opts.DestClusterID, g.Spec.Target.ClusterID)
	require.NotNil(t, g.Spec.Target.Kafka)
	assert.Equal(t, []string{opts.DestBootstrap}, g.Spec.Target.Kafka.BootstrapServers)
	assert.Equal(t, opts.RestEndpoint, g.Spec.Target.Kafka.RestEndpoint)

	assert.Equal(t, opts.ClusterLinkName, g.Spec.ClusterLink.Name)
	assert.Equal(t, []string{opts.DestBootstrap}, g.Spec.ClusterLink.BootstrapServers)
	assert.Equal(t, opts.Namespace, g.Spec.Gateway.Namespace)
	assert.Equal(t, opts.KubePath, g.Spec.Gateway.Kubeconfig)
	assert.Equal(t, opts.GatewayName, g.Spec.Gateway.CrName)
	require.Len(t, g.Spec.TopicGroup, len(opts.FenceRoutes))
	for i, want := range opts.FenceRoutes {
		got := g.Spec.TopicGroup[i]
		assert.Equal(t, want.Name, got.Route)
		assert.Equal(t, want.SwitchoverDomainName, got.TargetStreamingDomain)
		// The id is derived from the live CR at init, not carried in the
		// manifest, and a match-all pattern selects every active mirror topic.
		require.NotNil(t, got.TopicPatterns)
		assert.Equal(t, []string{".*"}, *got.TopicPatterns)
	}

	// The destination Kafka leg carries the API key/secret and the only
	// load-bearing insecure-skip in the suite (CFK's cert is signed by the
	// self-signed CA setup.sh creates).
	dst, errs := g.DestinationKafkaCredentials()
	require.Empty(t, errs)
	require.NotNil(t, dst.SASLPlain)
	assert.Equal(t, opts.APIKey, dst.SASLPlain.Username)
	assert.Equal(t, opts.APISecret, dst.SASLPlain.Password)
	assert.True(t, dst.InsecureSkipTLSVerify,
		"the destination Kafka leg must skip verification — CFK's cert is signed by a self-signed CA")

	// The cluster-link REST leg is its own file, reusing the same key/secret.
	rest, err := g.RestCredentials()
	require.NoError(t, err)
	assert.Equal(t, opts.APIKey, rest.APIKey, "REST api_key comes from linkCredentials")
	assert.Equal(t, opts.APISecret, rest.APISecret, "REST api_secret comes from linkCredentials")

	// The source is plaintext, so insecure-skip is meaningless there and is
	// deliberately not written on that leg.
	src, errs := g.SourceCredentials()
	require.Empty(t, errs)
	require.NotNil(t, src.UnauthenticatedPlaintext)
	assert.False(t, src.InsecureSkipTLSVerify,
		"the plaintext source leg must not carry a meaningless insecure-skip")
}

// TestRenderGatewayMigration_OmitsZeroPolicy pins that a scenario with no policy
// renders no policy keys at all. Writing a zero as `0` rather than omitting it
// would break the schema's duration pattern, which requires a unit — and five of
// the nine execute sites want exactly this (lagThreshold 0, detect 0).
func TestRenderGatewayMigration_OmitsZeroPolicy(t *testing.T) {
	rendered, err := renderGatewayMigration(baselineOpts())
	require.NoError(t, err)

	assert.NotContains(t, rendered, "defaultPolicies:", "a scenario with no policy must render no policy block")
	assert.NotContains(t, rendered, "lagThreshold")
	assert.NotContains(t, rendered, "detectUnroutedProducersDuration")

	g, err := manifest.ParseGatewayMigration([]byte(rendered))
	require.NoError(t, err)
	require.Empty(t, g.Validate())
	assert.Equal(t, manifest.DefaultPolicies{}, g.Spec.DefaultPolicies, "an omitted policy block must parse to the zero DefaultPolicies")
}

// TestRenderGatewayMigration_PolicyRoundTrips covers the per-scenario policy the
// nine execute sites vary. Durations must render as duration STRINGS ("10s"), not
// as the integer nanosecond count goccy would marshal a time.Duration to.
func TestRenderGatewayMigration_PolicyRoundTrips(t *testing.T) {
	opts := baselineOpts()
	opts.Policy = policyOpts{
		PromoteBatchSize:        1,
		DetectUnroutedProducers: 10 * time.Second,
		ConsumerOffsetSyncDrain: 15 * time.Second,
	}

	rendered, err := renderGatewayMigration(opts)
	require.NoError(t, err)
	assert.Contains(t, rendered, "detectUnroutedProducersDuration: 10s",
		"durations must render as duration strings, not nanosecond integers")

	g, err := manifest.ParseGatewayMigration([]byte(rendered))
	require.NoError(t, err)
	require.Empty(t, g.Validate())

	assert.Equal(t, 1, g.Spec.DefaultPolicies.PromoteBatchSize)
	assert.Equal(t, 10*time.Second, g.Spec.DefaultPolicies.DetectUnroutedProducersDuration)
	assert.Equal(t, 15*time.Second, g.Spec.DefaultPolicies.ConsumerOffsetSyncDrainDuration)
	// lagThreshold stays omitted at zero even when siblings are set.
	assert.Equal(t, 0, g.Spec.DefaultPolicies.LagThreshold)
}

// TestRenderGatewayMigration_PauseConsumerOffsetSync covers the 6 init sites that
// appended --pause-consumer-offset-sync. It is drift-compared at execute, so an
// omitted-vs-false slip would surface as a spec-change refusal mid-suite.
func TestRenderGatewayMigration_PauseConsumerOffsetSync(t *testing.T) {
	opts := baselineOpts()
	opts.PauseConsumerOffsetSync = true

	rendered, err := renderGatewayMigration(opts)
	require.NoError(t, err)
	g, err := manifest.ParseGatewayMigration([]byte(rendered))
	require.NoError(t, err)
	require.Empty(t, g.Validate())
	assert.True(t, g.Spec.ClusterLink.PauseConsumerOffsetSync)
}

// TestRenderCredentialFiles_ValuesCannotInjectYAML is the abuse case for the
// hazard the renderer introduces: it substitutes secrets into YAML credentials
// files as TEXT, before the parser sees them. A value carrying a newline, a
// ": ", or a leading indicator can restructure the document; an embedded quote or
// backslash can close the scalar early.
//
// Containment is observable as "resolves back byte-exact". The "/\\ and non-ASCII
// cases force the escaping to be generic rather than a list of the handful of
// characters enumerated here — an allowlist would pass its own test while leaving
// the two characters that actually terminate a double-quoted scalar unhandled.
func TestRenderCredentialFiles_ValuesCannotInjectYAML(t *testing.T) {
	nasty := map[string]string{
		"newline and a forged key": "pass\nusername: attacker",
		"colon space":              "pass: word",
		"leading anchor":           "&anchor",
		"leading alias":            "*alias",
		"leading tag":              "!tag",
		"leading comment":          "#comment",
		"trailing tab":             "pass\t", // goccy silently drops tabs in PLAIN scalars
		"embedded quote and slash": `pa"ss\word`,
		"non-ascii":                "pässwörd✓",
		"dollars must survive":     "p@$$w0rd",
		"only a quote":             `"`,
		"only a backslash":         `\`,
	}

	for name, secret := range nasty {
		t.Run(name, func(t *testing.T) {
			opts := baselineOpts()
			opts.APISecret = secret

			rendered, err := renderGatewayMigration(withRenderedCreds(t, opts))
			require.NoError(t, err)

			g, err := manifest.ParseGatewayMigration([]byte(rendered))
			require.NoError(t, err, "a hostile credential value must not break the document")
			require.Empty(t, g.Validate())

			dst, errs := g.DestinationKafkaCredentials()
			require.Empty(t, errs)
			require.NotNil(t, dst.SASLPlain)
			assert.Equal(t, secret, dst.SASLPlain.Password,
				"the credential must survive the render/parse round-trip byte-exact")

			// The forged key must not have become a real one.
			assert.Equal(t, opts.APIKey, dst.SASLPlain.Username,
				"a newline-bearing secret must not be able to overwrite a sibling field")
		})
	}
}

// TestRenderCredentialFiles_RefusesValuesKcpCannotDecode is the other half of the
// escaping contract, and it is not theoretical: strconv.Quote is a valid YAML 1.2
// double-quoted scalar, but goccy's decode does not implement the \xNN / \uNNNN
// escapes. The values are fail-closed, so this is robustness rather than a
// vulnerability — but the natural trigger is a copy-pasted secret carrying a BOM
// or zero-width character, whose error points at a bare column number with the
// source excerpt stripped; the debugging instinct is to disable the stripper or
// dump the file, either of which WOULD leak. Refusing at render time prevents it.
func TestRenderCredentialFiles_RefusesValuesKcpCannotDecode(t *testing.T) {
	unreadable := map[string]string{
		"BOM":                "\ufeffpass",
		"control char":       "pass\x01word",
		"DEL":                "pass\x7f",
		"non-breaking space": "pass\u00a0word",
		"line separator":     "pass\u2028word",
		"invalid utf-8":      "pass\xffword",
	}

	for name, secret := range unreadable {
		t.Run(name, func(t *testing.T) {
			opts := baselineOpts()
			opts.APISecret = secret

			_, err := renderCredentialFiles(opts)
			require.Error(t, err,
				"rendering must refuse a value kcp cannot decode rather than emit a file that dies mid-run")
			assert.NotContains(t, err.Error(), secret,
				"the refusal must name the problem without echoing the credential")
		})
	}
}

// TestRenderCredentialFiles_NeverInterpolated pins that the fixture stays
// literal. There is no environment-variable substitution anywhere in the
// credentials-loading path, so a credential containing "${" must resolve to
// itself rather than an undefined-variable hard error.
func TestRenderCredentialFiles_NeverInterpolated(t *testing.T) {
	opts := baselineOpts()
	opts.APISecret = "${MSK_PASSWORD}"

	rendered, err := renderGatewayMigration(withRenderedCreds(t, opts))
	require.NoError(t, err)

	g, err := manifest.ParseGatewayMigration([]byte(rendered))
	require.NoError(t, err)
	require.Empty(t, g.Validate())

	dst, errs := g.DestinationKafkaCredentials()
	require.Empty(t, errs)
	require.NotNil(t, dst.SASLPlain)
	assert.Equal(t, "${MSK_PASSWORD}", dst.SASLPlain.Password,
		"a credential that looks like a variable reference must stay literal")
}

// TestManifestForLog_RedactsCredentialFileSecrets covers the leak path t.Logf
// opens: the suite logs the credential files it writes to the pod to make a
// failed run diagnosable, and test output is CI output.
func TestManifestForLog_RedactsCredentialFileSecrets(t *testing.T) {
	opts := baselineOpts()
	opts.APIKey = "e2e-key-value"
	opts.APISecret = "e2e-secret-value"

	files, err := renderCredentialFiles(opts)
	require.NoError(t, err)

	for _, body := range []string{files.DestKafka, files.Link} {
		logged := manifestForLog(body)
		assert.NotContains(t, logged, opts.APIKey, "the API key must not reach the log")
		assert.NotContains(t, logged, opts.APISecret, "the API secret must not reach the log")
	}

	// Non-secret keys survive, so a logged file stays diagnosable.
	assert.Contains(t, manifestForLog(files.DestKafka), "tls: true")
}

// TestManifestForLog_RedactsByKeyPathNotValue is what forces key-path redaction.
// A value-substring implementation would destroy a non-secret line whenever a
// credential happened to share its value — and, worse, leak any credential field
// added later that nobody remembered to pass in.
func TestManifestForLog_RedactsByKeyPathNotValue(t *testing.T) {
	opts := baselineOpts()
	// The secret is deliberately identical to a non-secret token in the file.
	opts.APISecret = "true"

	files, err := renderCredentialFiles(opts)
	require.NoError(t, err)
	logged := manifestForLog(files.DestKafka)

	assert.Contains(t, logged, "tls: true",
		"redaction must key off the field path, not the value — tls is not a secret")
	assert.NotContains(t, logged, `password: "true"`,
		"the password line must still be redacted")
}

// TestPodWriteCommand_TransportsOverStdinAtMode0600 pins the three properties that
// keep a secret-bearing file out of the process table and off a group-readable
// file.
func TestPodWriteCommand_TransportsOverStdinAtMode0600(t *testing.T) {
	const podPath = "/workspace/gateway-migration-baseline.yaml"
	argv := podWriteCommand("minikube", "confluent", "kcp-runner", podPath)

	// 1. Stdin transport. Without -i the content would have to travel as an
	//    argument, putting a secret in the container's process table and in the
	//    host kubectl command line.
	assert.Contains(t, argv, "-i", "the content must travel over stdin, not argv")
	assert.Contains(t, argv, "exec")

	// 2. umask before the redirect. `cat >` does not change an existing file's
	//    mode, and a chmod afterwards would leave a window at 0644.
	joined := strings.Join(argv, " ")
	assert.Contains(t, joined, "umask 077",
		"credential-bearing files must be created 0600")
	assert.Contains(t, joined, `rm -f "$1"`,
		"a re-render must not inherit the mode of a previous create")

	// 3. The path arrives as a positional parameter, NOT interpolated into the
	//    shell script. Assert it on the script element specifically.
	var script string
	for i, a := range argv {
		if a == "-c" && i+1 < len(argv) {
			script = argv[i+1]
			break
		}
	}
	require.NotEmpty(t, script, "expected an sh -c script in the argv")
	assert.NotContains(t, script, podPath,
		"the path must be passed as $1, not spliced into the script")
	assert.Equal(t, podPath, argv[len(argv)-1],
		"the path must be the trailing positional argument")
}
