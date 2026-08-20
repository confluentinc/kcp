package gateway

import (
	"context"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes"
	kubernetesfake "k8s.io/client-go/kubernetes/fake"
	ktesting "k8s.io/client-go/testing"
)

// ===========================================================================
// Test fixtures
//
// A realistic pair modelled on docs/assets/gateway-switchover: the initial CR
// routes to the source cluster (the fence is derived from it at cutover), and
// the switchover CR drops the source domain and routes to Confluent Cloud.
//
// Secret references are spread across three depths on purpose (secret store
// config, per-bootstrap-server TLS, route cluster authentication) and
// vault-config is referenced by both CRs so deduplication is exercised.
// nodeIdRanges carries integers, which goccy parses as uint64 — the reason the
// tree is unsafe to walk with unstructured.Nested*; see mapField's comment.
// ===========================================================================

const (
	testNamespace = "kcp"
	testGateway   = "migration-gateway"
)

const initialCR = `
apiVersion: platform.confluent.io/v1beta1
kind: Gateway
metadata:
  name: migration-gateway
spec:
  replicas: 3
  secretStores:
    - name: vault-store
      provider:
        type: Vault
        configSecretRef: vault-config
  streamingDomains:
    - name: source-kafka-cluster
      type: kafka
      kafkaCluster:
        bootstrapServers:
          - id: SCRAM
            endpoint: SASL_SSL://msk:9096
            tls:
              secretRef: msk-tls
        nodeIdRanges:
          - name: pool-1
            start: 1
            end: 3
  routes:
    - name: migration-route
      endpoint: gateway:9595
      streamingDomain:
        name: source-kafka-cluster
        bootstrapServerId: SCRAM
      security:
        auth: passthrough
`

const switchoverCR = `
apiVersion: platform.confluent.io/v1beta1
kind: Gateway
metadata:
  name: migration-gateway
spec:
  replicas: 3
  secretStores:
    - name: vault-store
      provider:
        type: Vault
        configSecretRef: vault-config
  streamingDomains:
    - name: confluent-cloud
      type: kafka
      kafkaCluster:
        bootstrapServers:
          - id: SASL_PLAIN
            endpoint: SASL_SSL://ccloud:9092
            tls:
              secretRef: ccloud-tls
        nodeIdRanges:
          - name: pool-1
            start: 0
            end: 17
  routes:
    - name: migration-route
      endpoint: gateway:9595
      streamingDomain:
        name: confluent-cloud
        bootstrapServerId: SASL_PLAIN
      security:
        auth: swap
        secretStore: vault-store
        cluster:
          authentication:
            type: plain
            jaasConfigPassThrough:
              secretRef: plain-jaas
`

// allTestSecrets are every secret the fenced and switchover fixtures reference.
var allTestSecrets = []string{"vault-config", "msk-tls", "ccloud-tls", "plain-jaas"}

func newSecret(namespace, name string) runtime.Object {
	return &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name}}
}

// clientsetWithSecrets returns a fake clientset holding the named secrets in the
// test namespace.
func clientsetWithSecrets(names ...string) kubernetes.Interface {
	objects := make([]runtime.Object, 0, len(names))
	for _, name := range names {
		objects = append(objects, newSecret(testNamespace, name))
	}
	return newFakeClientset(objects...)
}

// defaultFenceRoutes is the route the standard fixtures fence (initialCR and
// switchoverCR both carry a route named migration-route).
var defaultFenceRoutes = []string{"migration-route"}

// validate runs the validator against the standard namespace/gateway with all
// fixture secrets present, fencing the default route, so a test only has to vary
// the CRs it cares about.
func validate(t *testing.T, initial, switchover string) (CRValidationResult, error) {
	t.Helper()
	return validateGatewayCRs(context.Background(), clientsetWithSecrets(allTestSecrets...),
		testNamespace, testGateway, []byte(initial), []byte(switchover), defaultFenceRoutes)
}

// ===========================================================================
// Happy path
// ===========================================================================

func TestValidateGatewayCRs_ValidPair(t *testing.T) {
	result, err := validate(t, initialCR, switchoverCR)

	require.NoError(t, err)
	assert.Empty(t, result.Warnings)
	assert.Empty(t, result.SecretCheckSkipped)
	// Only the switchover CR's secrets are checked now (vault-config, ccloud-tls,
	// plain-jaas); the initial CR is live and its refs resolve by construction,
	// and there is no fenced CR.
	assert.Equal(t, 3, result.SecretRefsChecked)
}

func TestValidateGatewayCRs_ShippedExamplesPass(t *testing.T) {
	dirs, err := filepath.Glob("../../../docs/assets/gateway-switchover/switchover-*")
	require.NoError(t, err)
	require.NotEmpty(t, dirs, "worked examples not found; has docs/assets/gateway-switchover moved?")

	for _, dir := range dirs {
		t.Run(filepath.Base(dir), func(t *testing.T) {
			read := func(name string) []byte {
				data, err := os.ReadFile(filepath.Join(dir, name))
				require.NoError(t, err)
				return data
			}
			initial, switchover := read("gateway_init.yaml"), read("gateway_switchover.yaml")

			// Seed exactly the secrets each example's READMEs tell the operator to
			// create. Deriving the fixture from collectSecretRefs instead would make
			// this untestable: a secret the walker misses would also be absent from
			// the fake cluster, so the test would pass while the check silently did
			// nothing. That is how clientCredentialsRef went unnoticed.
			expected, known := shippedExampleSecrets[filepath.Base(dir)]
			require.True(t, known, "no expected secret list for %s; add one", filepath.Base(dir))

			objects := make([]runtime.Object, 0, len(expected))
			for _, name := range expected {
				objects = append(objects, newSecret(testNamespace, name))
			}

			result, err := validateGatewayCRs(context.Background(), newFakeClientset(objects...),
				testNamespace, testGateway, initial, switchover, []string{"migration-route"})

			require.NoError(t, err, "a shipped example must pass validation")
			assert.Empty(t, result.Warnings)
			assert.Equal(t, len(expected), result.SecretRefsChecked,
				"every secret the switchover CR references must be found and checked")

			// And the walker must find precisely that set — no more, no less.
			refs, unrecognised := map[string]struct{}{}, map[string]struct{}{}
			cr, err := parseGatewayCR(roleSwitchover, switchover)
			require.NoError(t, err)
			collectSecretRefs(cr.obj, refs, unrecognised)
			assert.ElementsMatch(t, expected, slices.Sorted(maps.Keys(refs)))
			assert.Empty(t, unrecognised, "an unrecognised *Ref field means a secret is going unchecked")
		})
	}
}

// shippedExampleSecrets is every Secret the SWITCHOVER CR of each worked example
// references, taken from the CR and cross-checked against the `kubectl create
// secret` commands in each example's README. Only the switchover CR is validated
// (the initial CR is live and the fence is derived from it), so source-side
// secrets that live only in the initial CR (e.g. msk-tls, msk-client-tls) are
// deliberately absent here.
var shippedExampleSecrets = map[string][]string{
	"switchover-mtls-to-mtls": {
		"ccloud-client-tls", "gateway-tls",
	},
	"switchover-mtls-to-oauth": {
		"file-store-config", "file-store-idp-credentials", "gateway-tls", "oauth-jaas", "tls",
	},
	"switchover-mtls-to-sasl-plain": {
		"file-store-ccloud-credentials", "file-store-config", "gateway-tls", "plain-jaas", "tls",
	},
	"switchover-none-to-mtls": {
		"ccloud-client-tls",
	},
	"switchover-none-to-oauth": {
		"file-store-config", "file-store-idp-credentials", "oauth-jaas", "tls",
	},
	"switchover-none-to-saslplain": {
		"file-store-config", "file-store-noauth-credentials", "plain-jaas", "tls",
	},
	"switchover-sasl-scram-to-oauth": {
		"oauth-jaas", "scram-admin-credentials", "tls", "vault-config",
	},
	"switchover-sasl-scram-to-sasl-plain": {
		"plain-jaas", "scram-admin-credentials", "tls", "vault-config",
	},
}

// ===========================================================================
// Parsing — terminal, since every later check reads the parsed tree
// ===========================================================================

func TestValidateGatewayCRs_EmptyCR(t *testing.T) {
	_, err := validate(t, initialCR, "   \n")
	require.ErrorContains(t, err, "the switchover gateway CR is empty")
}

func TestValidateGatewayCRs_MalformedYAML(t *testing.T) {
	_, err := validate(t, initialCR, "kind: Gateway\n  bad: [indent")
	require.ErrorContains(t, err, "failed to parse the switchover gateway CR")
}

func TestValidateGatewayCRs_YAMLIsNotAMapping(t *testing.T) {
	_, err := validate(t, initialCR, "- just\n- a\n- list\n")
	require.ErrorContains(t, err, "switchover gateway CR")
}

// ===========================================================================
// Shape: kind, apiVersion, spec
// ===========================================================================

func TestValidateGatewayCRs_WrongKind(t *testing.T) {
	// The mistake this guards: ApplyGatewayYAML pushes whatever it is handed
	// through the Gateway GVR, so a Deployment would be applied as the gateway.
	_, err := validate(t, initialCR, `
apiVersion: apps/v1
kind: Deployment
metadata:
  name: migration-gateway
spec:
  replicas: 1
`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `the switchover gateway CR has kind "Deployment", expected "Gateway"`)
}

func TestValidateGatewayCRs_WrongAPIGroup(t *testing.T) {
	_, err := validate(t, initialCR, `
apiVersion: platform.example.com/v1beta1
kind: Gateway
metadata:
  name: migration-gateway
spec:
  routes:
    - name: migration-route
`)
	require.ErrorContains(t, err, "expected group \"platform.confluent.io\"")
}

func TestValidateGatewayCRs_UnexpectedAPIVersionIsRefused(t *testing.T) {
	// Server-side apply rejects a patch whose apiVersion disagrees with the
	// endpoint's ("Incorrect version specified in apply patch"), so a mismatch
	// fails the apply mid-migration — refuse it up front rather than warn.
	switchover := replaceFirst(t, switchoverCR,
		"apiVersion: platform.confluent.io/v1beta1",
		"apiVersion: platform.confluent.io/v1alpha1")

	_, err := validate(t, initialCR, switchover)

	require.Error(t, err)
	assert.Contains(t, err.Error(), `declares apiVersion "platform.confluent.io/v1alpha1"`)
}

func TestValidateGatewayCRs_ManagedFieldsIsRefused(t *testing.T) {
	// A file derived from `kubectl get gateway -o yaml`. client-go refuses to
	// apply it outright, before any request leaves the process, so without this
	// check the apply fails after the fence with clients already blocked.
	switchover := replaceFirst(t, switchoverCR, "  name: migration-gateway", `  name: migration-gateway
  resourceVersion: "12345"
  managedFields:
    - manager: confluent-operator
      operation: Update`)

	_, err := validate(t, initialCR, switchover)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "carries metadata.managedFields, which server-side apply refuses")
}

func TestValidateGatewayCRs_InitialCRManagedFieldsAllowed(t *testing.T) {
	// The initial CR always carries managedFields — it is fetched from the
	// cluster — and unfenceGateway strips them before re-applying it.
	initial := replaceFirst(t, initialCR, "  name: migration-gateway", `  name: migration-gateway
  managedFields:
    - manager: confluent-operator
      operation: Update`)

	_, err := validate(t, initial, switchoverCR)

	require.NoError(t, err)
}

func TestValidateGatewayCRs_MissingSpec(t *testing.T) {
	_, err := validate(t, initialCR, `
apiVersion: platform.confluent.io/v1beta1
kind: Gateway
metadata:
  name: migration-gateway
`)
	require.ErrorContains(t, err, "the switchover gateway CR has no spec")
}

// ===========================================================================
// Identity: the file must name the gateway the migration targets
// ===========================================================================

func TestValidateGatewayCRs_NameMismatch(t *testing.T) {
	switchover := replaceFirst(t, switchoverCR, "name: migration-gateway", "name: some-other-gateway")

	_, err := validate(t, initialCR, switchover)

	require.Error(t, err)
	assert.Contains(t, err.Error(), `the switchover gateway CR is named "some-other-gateway" but the migration targets gateway "migration-gateway"`)
}

func TestValidateGatewayCRs_AbsentNameIsAllowed(t *testing.T) {
	// ApplyGatewayYAML fills the name in, so a CR file that omits it works today
	// and must keep working — only a name that is present and wrong is a refusal.
	switchover := replaceFirst(t, switchoverCR, "metadata:\n  name: migration-gateway\n", "metadata:\n  labels:\n    app: gw\n")

	_, err := validate(t, initialCR, switchover)

	require.NoError(t, err)
}

func TestValidateGatewayCRs_NamespaceMismatch(t *testing.T) {
	switchover := replaceFirst(t, switchoverCR, "  name: migration-gateway", "  name: migration-gateway\n  namespace: other-ns")

	_, err := validate(t, initialCR, switchover)

	require.ErrorContains(t, err, `declares namespace "other-ns" but the migration targets namespace "kcp"`)
}

func TestValidateGatewayCRs_InitialCRIdentityNotChecked(t *testing.T) {
	// The initial CR is fetched from the cluster by name, so re-checking its
	// identity would only add noise.
	initial := replaceFirst(t, initialCR, "name: migration-gateway", "name: whatever-the-cluster-said")

	_, err := validate(t, initial, switchoverCR)

	require.NoError(t, err)
}

// ===========================================================================
// Fence blocks
// ===========================================================================

func TestValidateGatewayCRs_FenceRouteNotInInitialCR(t *testing.T) {
	// The route named to fence does not exist in the live initial CR, so
	// FenceRoutes would fail at cutover — catch it at init, before anything is
	// fenced. The single operator-supplied name is echoed: it is their own input,
	// not a discovered resource list.
	_, err := validateGatewayCRs(context.Background(), clientsetWithSecrets(allTestSecrets...),
		testNamespace, testGateway, []byte(initialCR), []byte(switchoverCR), []string{"no-such-route"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), `no route named "no-such-route"`)
}

func TestValidateGatewayCRs_FenceRouteAlreadyFencedInInitialCR(t *testing.T) {
	// The fence is additive: a route the live initial CR already fences cannot be
	// fenced again, and its presence means the "initial" CR is not the clean
	// pre-migration spec kcp derives the fence from.
	initial := replaceFirst(t, initialCR,
		"    - name: migration-route\n      endpoint: gateway:9595\n",
		"    - name: migration-route\n      endpoint: gateway:9595\n      fence:\n        scope: ALL\n")

	_, err := validate(t, initial, switchoverCR)

	require.Error(t, err)
	assert.Contains(t, err.Error(), `already fences route "migration-route"`)
}

func TestValidateGatewayCRs_SwitchoverStillFencesMigrationRoute(t *testing.T) {
	// This is the mirror of the 2026-07-27 outcome: the migration completes and
	// reports success while every client stays blocked.
	_, err := validate(t, initialCR, replaceFirst(t, switchoverCR,
		"    - name: migration-route\n      endpoint: gateway:9595",
		"    - name: migration-route\n      endpoint: gateway:9595\n      fence:\n        scope: ALL"))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "the switchover gateway CR still fences the route(s) the migration fences (migration-route)")
}

func TestValidateGatewayCRs_SwitchoverFencesAnotherRouteWarnsOnly(t *testing.T) {
	// kcp cannot know the intent behind fencing a route the migration never
	// fenced, so this warns rather than refuses.
	switchover := replaceFirst(t, switchoverCR, "  routes:\n", `  routes:
    - name: legacy-route
      endpoint: gateway:9596
      fence:
        scope: ALL
      streamingDomain:
        name: confluent-cloud
        bootstrapServerId: SASL_PLAIN
`)

	result, err := validate(t, initialCR, switchover)

	require.NoError(t, err)
	require.Len(t, result.Warnings, 1)
	assert.Contains(t, result.Warnings[0], "fences route(s) the migration does not fence (legacy-route)")
}

func TestValidateGatewayCRs_ExplicitlyNulledFenceIsNotAFence(t *testing.T) {
	// `fence: null` is how a scripted edit removes a fence — server-side apply
	// deletes a nulled field — so this switchover CR works and must not be
	// refused as "still fences".
	switchover := replaceFirst(t, switchoverCR,
		"    - name: migration-route\n      endpoint: gateway:9595",
		"    - name: migration-route\n      endpoint: gateway:9595\n      fence: null")

	_, err := validate(t, initialCR, switchover)

	require.NoError(t, err)
}

func TestValidateGatewayCRs_UnmatchableSwitchoverFenceWarns(t *testing.T) {
	// A switchover route with a fence but neither a name nor an endpoint: there is
	// nothing to match it against, so say that rather than guess either way.
	switchover := replaceFirst(t, switchoverCR,
		"    - name: migration-route\n      endpoint: gateway:9595\n",
		"    - fence:\n        scope: ALL\n")

	result, err := validate(t, initialCR, switchover)

	require.NoError(t, err)
	require.NotEmpty(t, result.Warnings)
	assert.Contains(t, strings.Join(result.Warnings, "\n"), "neither a name nor an endpoint")
}

// ===========================================================================
// Wrong-file mistakes
// ===========================================================================

func TestValidateGatewayCRs_SwitchoverIdenticalToLiveGateway(t *testing.T) {
	// Passing gateway_init.yaml as --switchover-cr-yaml: the apply is a no-op, so
	// it does not even bump metadata.generation for the acceptance check to spot.
	_, err := validate(t, initialCR, initialCR)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "identical to the live gateway's")
}

// ===========================================================================
// Streaming domain wiring
// ===========================================================================

func TestValidateGatewayCRs_DanglingStreamingDomain(t *testing.T) {
	switchover := replaceFirst(t, switchoverCR, "        name: confluent-cloud\n        bootstrapServerId", "        name: typo-cloud\n        bootstrapServerId")

	_, err := validate(t, initialCR, switchover)

	require.Error(t, err)
	assert.Contains(t, err.Error(), `route migration-route points at streaming domain "typo-cloud", which the CR does not declare (declared: confluent-cloud)`)
}

func TestValidateGatewayCRs_UnknownBootstrapServerID(t *testing.T) {
	switchover := replaceFirst(t, switchoverCR, "bootstrapServerId: SASL_PLAIN", "bootstrapServerId: NOPE")

	_, err := validate(t, initialCR, switchover)

	require.Error(t, err)
	assert.Contains(t, err.Error(), `points at bootstrapServerId "NOPE", which streaming domain "confluent-cloud" does not define (defined: SASL_PLAIN)`)
}

func TestValidateGatewayCRs_RouteWithoutStreamingDomainIsAllowed(t *testing.T) {
	// Only what the file states is checked; an absent field is not assumed
	// mandatory.
	switchover := replaceFirst(t, switchoverCR,
		"      streamingDomain:\n        name: confluent-cloud\n        bootstrapServerId: SASL_PLAIN\n", "")

	_, err := validate(t, initialCR, switchover)

	require.NoError(t, err)
}

// ===========================================================================
// Live secret references
// ===========================================================================

func TestValidateGatewayCRs_MissingSecret(t *testing.T) {
	// The 2026-07-27 failure: the switchover CR named a Secret that did not
	// exist, the operator refused the spec and the gateway stayed fenced.
	cs := clientsetWithSecrets("vault-config", "ccloud-tls")

	_, err := validateGatewayCRs(context.Background(), cs, testNamespace, testGateway,
		[]byte(initialCR), []byte(switchoverCR), defaultFenceRoutes)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "reference 1 secret(s) that do not exist in namespace kcp: plain-jaas")
}

func TestValidateGatewayCRs_AllMissingSecretsReportedTogether(t *testing.T) {
	// One run must list every missing secret: fixing them one re-run at a time
	// is exactly the loop this check exists to avoid.
	_, err := validateGatewayCRs(context.Background(), clientsetWithSecrets("vault-config"),
		testNamespace, testGateway, []byte(initialCR), []byte(switchoverCR), defaultFenceRoutes)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "reference 2 secret(s) that do not exist in namespace kcp: ccloud-tls, plain-jaas")
}

func TestValidateGatewayCRs_SecretsInOtherNamespaceDoNotCount(t *testing.T) {
	objects := make([]runtime.Object, 0, len(allTestSecrets))
	for _, name := range allTestSecrets {
		objects = append(objects, newSecret("somewhere-else", name))
	}

	_, err := validateGatewayCRs(context.Background(), newFakeClientset(objects...),
		testNamespace, testGateway, []byte(initialCR), []byte(switchoverCR), defaultFenceRoutes)

	require.ErrorContains(t, err, "reference 3 secret(s) that do not exist in namespace kcp")
}

func TestValidateGatewayCRs_InitialCRSecretsNotRequired(t *testing.T) {
	// The live initial CR is already running, so its references resolve by
	// construction. Requiring them again would fail migrations for no reason.
	initial := replaceFirst(t, initialCR, "secretRef: msk-tls", "secretRef: long-deleted-secret")

	result, err := validateGatewayCRs(context.Background(), clientsetWithSecrets(allTestSecrets...),
		testNamespace, testGateway, []byte(initial), []byte(switchoverCR), defaultFenceRoutes)

	require.NoError(t, err)
	assert.Equal(t, 3, result.SecretRefsChecked, "long-deleted-secret is never looked up")
}

func TestValidateGatewayCRs_SecretReadForbiddenSkipsCheck(t *testing.T) {
	// Reading secrets is a privilege many migration operators lack. A denial must
	// not block a migration that would otherwise succeed.
	cs := clientsetWithSecrets(allTestSecrets...)
	cs.(*kubernetesfake.Clientset).PrependReactor("get", "secrets", func(ktesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(schema.GroupResource{Resource: "secrets"}, "", fmt.Errorf("no access"))
	})

	result, err := validateGatewayCRs(context.Background(), cs, testNamespace, testGateway,
		[]byte(initialCR), []byte(switchoverCR), defaultFenceRoutes)

	require.NoError(t, err)
	assert.Equal(t, "no permission to read secrets in namespace kcp", result.SecretCheckSkipped)
	assert.Zero(t, result.SecretRefsChecked, "a partial count would read as a pass")
}

func TestValidateGatewayCRs_UnexpectedSecretAPIErrorFails(t *testing.T) {
	// Neither NotFound nor a permission denial: the cluster is not answering, so
	// don't pretend the check ran.
	cs := clientsetWithSecrets(allTestSecrets...)
	cs.(*kubernetesfake.Clientset).PrependReactor("get", "secrets", func(ktesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewInternalError(fmt.Errorf("etcd is having a moment"))
	})

	_, err := validateGatewayCRs(context.Background(), cs, testNamespace, testGateway,
		[]byte(initialCR), []byte(switchoverCR), defaultFenceRoutes)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to check whether secret")
	assert.Contains(t, err.Error(), "etcd is having a moment")
}

func TestValidateGatewayCRs_NoSecretRefs(t *testing.T) {
	switchover := `
apiVersion: platform.confluent.io/v1beta1
kind: Gateway
metadata:
  name: migration-gateway
spec:
  routes:
    - name: migration-route
      endpoint: gateway:9595
`

	result, err := validateGatewayCRs(context.Background(), newFakeClientset(),
		testNamespace, testGateway, []byte(initialCR), []byte(switchover), defaultFenceRoutes)

	require.NoError(t, err)
	assert.Zero(t, result.SecretRefsChecked)
	assert.Empty(t, result.SecretCheckSkipped)
}

// ===========================================================================
// Aggregation
// ===========================================================================

func TestValidateGatewayCRs_MissingSecretsFoundAlongsideStaticProblems(t *testing.T) {
	// Regression: the secret sweep used to stop as soon as any problem existed,
	// so a static failure earlier in the run masked every missing secret but the
	// first — the operator fixed one, re-ran, and found another.
	switchover := replaceFirst(t, switchoverCR, "name: migration-gateway", "name: migration-gatway")

	_, err := validateGatewayCRs(context.Background(), clientsetWithSecrets("vault-config"),
		testNamespace, testGateway, []byte(initialCR), []byte(switchover), defaultFenceRoutes)

	require.Error(t, err)
	assert.Contains(t, err.Error(), `is named "migration-gatway"`)
	assert.Contains(t, err.Error(), "reference 2 secret(s) that do not exist in namespace kcp: ccloud-tls, plain-jaas")
}

func TestValidateGatewayCRs_ReportsEveryProblemInOneRun(t *testing.T) {
	// A broken switchover CR: wrong gateway name, no spec.routes fence lifted,
	// dangling streaming domain, and a secret that does not exist.
	switchover := `
apiVersion: platform.confluent.io/v1beta1
kind: Gateway
metadata:
  name: wrong-gateway
spec:
  streamingDomains:
    - name: confluent-cloud
      type: kafka
  routes:
    - name: migration-route
      fence:
        scope: ALL
      streamingDomain:
        name: nope
      security:
        cluster:
          authentication:
            jaasConfigPassThrough:
              secretRef: missing-secret
`

	_, err := validate(t, initialCR, switchover)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "4 problem(s) found:")
	assert.Contains(t, err.Error(), `is named "wrong-gateway"`)
	assert.Contains(t, err.Error(), "still fences the route(s)")
	assert.Contains(t, err.Error(), `streaming domain "nope"`)
	assert.Contains(t, err.Error(), "missing-secret")
}

// ===========================================================================
// Helper unit tests
// ===========================================================================

func TestCollectSecretRefs_WalksEveryDepth(t *testing.T) {
	cr, err := parseGatewayCR(roleSwitchover, []byte(switchoverCR))
	require.NoError(t, err)

	refs, unrecognised := map[string]struct{}{}, map[string]struct{}{}
	collectSecretRefs(cr.obj, refs, unrecognised)

	assert.Equal(t, map[string]struct{}{
		"vault-config": {}, // secretStores[].provider.configSecretRef
		"ccloud-tls":   {}, // streamingDomains[].kafkaCluster.bootstrapServers[].tls.secretRef
		"plain-jaas":   {}, // routes[].security.cluster.authentication.jaasConfigPassThrough.secretRef
	}, refs)
	assert.Empty(t, unrecognised)
}

func TestCollectSecretRefs_ClientCredentialsRef(t *testing.T) {
	// Regression: clientCredentialsRef names a Secret in four of the eight shipped
	// switchover examples and was not collected, so the operator got a green tick
	// over exactly the missing-secret failure this validation exists to catch.
	cr, err := parseGatewayCR(roleSwitchover, []byte(`
kind: Gateway
spec:
  secretStores:
    - name: file-store
      provider:
        type: File
        configSecretRef: file-store-config
        clientCredentialsRef: file-store-ccloud-credentials
`))
	require.NoError(t, err)

	refs, unrecognised := map[string]struct{}{}, map[string]struct{}{}
	collectSecretRefs(cr.obj, refs, unrecognised)

	assert.Equal(t, map[string]struct{}{
		"file-store-config":             {},
		"file-store-ccloud-credentials": {},
	}, refs)
	assert.Empty(t, unrecognised)
}

func TestCollectSecretRefs_FlagsUnrecognisedRefFields(t *testing.T) {
	// The tripwire for CRD growth: a Ref-suffixed field kcp does not know is a
	// hole in the check, and must not sit behind a tick claiming a full pass.
	cr, err := parseGatewayCR(roleSwitchover, []byte(`
kind: Gateway
spec:
  routes:
    - name: migration-route
      security:
        someFutureCredentialRef: a-secret-kcp-cannot-see
`))
	require.NoError(t, err)

	refs, unrecognised := map[string]struct{}{}, map[string]struct{}{}
	collectSecretRefs(cr.obj, refs, unrecognised)

	assert.Empty(t, refs)
	assert.Equal(t, map[string]struct{}{"someFutureCredentialRef": {}}, unrecognised)
}

func TestValidateGatewayCRs_UnrecognisedRefFieldWarns(t *testing.T) {
	switchover := replaceFirst(t, switchoverCR, "      secretRef: plain-jaas", "      someFutureCredentialRef: unseen-secret")

	result, err := validateGatewayCRs(context.Background(), clientsetWithSecrets(allTestSecrets...),
		testNamespace, testGateway, []byte(initialCR), []byte(switchover), defaultFenceRoutes)

	require.NoError(t, err)
	require.NotEmpty(t, result.Warnings)
	assert.Contains(t, strings.Join(result.Warnings, "\n"), "someFutureCredentialRef")
}

func TestCollectSecretRefs_DescendsIntoNonStringSecretRef(t *testing.T) {
	// A future object-valued secretRef must still be walked, not swallowed.
	cr, err := parseGatewayCR(roleFenced, []byte(`
kind: Gateway
spec:
  routes:
    - secretRef:
        secretRef: nested-secret
`))
	require.NoError(t, err)

	refs, unrecognised := map[string]struct{}{}, map[string]struct{}{}
	collectSecretRefs(cr.obj, refs, unrecognised)

	assert.Equal(t, map[string]struct{}{"nested-secret": {}}, refs)
	assert.Empty(t, unrecognised)
}

// aliasBombCR is a YAML anchor bomb: goccy/go-yaml resolves an alias by sharing
// the anchored node rather than copying it, so this sub-kilobyte document parses
// in microseconds while describing a logical tree of ~4^19 (2.7e11) nodes.
//
// The level count is deliberate. Measured on this machine an un-memoised walk
// covers ~600M nodes/second, so a 4^12 bomb finishes in 50ms and would make the
// test below pass with or without the walk guard. At 4^19 the unguarded walk
// needs several minutes and the test is a real regression detector, while the
// guarded walk stays at ~10µs because it only ever visits the parsed nodes.
const aliasBombCR = `
apiVersion: platform.confluent.io/v1beta1
kind: Gateway
metadata:
  name: migration-gateway
spec:
  routes:
    - name: migration-route
      fence: {scope: ALL}
  bomb:
    a0: &a0 ["x","x","x","x"]
    a1: &a1 [*a0,*a0,*a0,*a0]
    a2: &a2 [*a1,*a1,*a1,*a1]
    a3: &a3 [*a2,*a2,*a2,*a2]
    a4: &a4 [*a3,*a3,*a3,*a3]
    a5: &a5 [*a4,*a4,*a4,*a4]
    a6: &a6 [*a5,*a5,*a5,*a5]
    a7: &a7 [*a6,*a6,*a6,*a6]
    a8: &a8 [*a7,*a7,*a7,*a7]
    a9: &a9 [*a8,*a8,*a8,*a8]
    a10: &a10 [*a9,*a9,*a9,*a9]
    a11: &a11 [*a10,*a10,*a10,*a10]
    a12: &a12 [*a11,*a11,*a11,*a11]
    a13: &a13 [*a12,*a12,*a12,*a12]
    a14: &a14 [*a13,*a13,*a13,*a13]
    a15: &a15 [*a14,*a14,*a14,*a14]
    a16: &a16 [*a15,*a15,*a15,*a15]
    a17: &a17 [*a16,*a16,*a16,*a16]
    a18: &a18 [*a17,*a17,*a17,*a17]
    a19: [*a18,*a18,*a18,*a18]
`

func TestValidateGatewayCRs_AliasBombTerminates(t *testing.T) {
	// Without the walk guard this pins one core for hours on a flat heap, with no
	// ctx check to interrupt it — a sub-kilobyte CR file hanging `migration init`.
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = validateGatewayCRs(context.Background(), clientsetWithSecrets(allTestSecrets...),
			testNamespace, testGateway, []byte(initialCR), []byte(aliasBombCR), defaultFenceRoutes)
	}()

	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("validation did not terminate on an alias bomb: the walk guard is not working")
	}
}

func TestCollectSecretRefs_AliasedNodeWalkedOnce(t *testing.T) {
	// The guard must dedupe by node identity without losing content: the secret
	// behind an alias still has to be found.
	cr, err := parseGatewayCR(roleFenced, []byte(`
kind: Gateway
spec:
  shared: &shared
    tls:
      secretRef: shared-secret
  a: *shared
  b: *shared
`))
	require.NoError(t, err)

	refs, unrecognised := map[string]struct{}{}, map[string]struct{}{}
	collectSecretRefs(cr.obj, refs, unrecognised)

	assert.Equal(t, map[string]struct{}{"shared-secret": {}}, refs)
	assert.Empty(t, unrecognised)
}

func TestTreeContainsNonNilKey_AliasBombTerminates(t *testing.T) {
	cr, err := parseGatewayCR(roleFenced, []byte(aliasBombCR))
	require.NoError(t, err)

	done := make(chan struct{})
	go func() {
		defer close(done)
		// A key that is absent forces the full walk.
		assert.False(t, treeContainsNonNilKey(cr.obj, "no-such-key"))
	}()

	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("treeContainsNonNilKey did not terminate on an alias bomb")
	}
}

func TestFencedRoutes(t *testing.T) {
	cr, err := parseGatewayCR(roleFenced, []byte(`
kind: Gateway
spec:
  routes:
    - name: b-route
      fence: {scope: ALL}
    - name: a-route
      fence: {scope: ALL}
    - name: unfenced-route
    - endpoint: gateway:9599
      fence: {scope: ALL}
    - fence: {scope: ALL}
`))
	require.NoError(t, err)

	fenced, present := fencedRoutes(cr)

	assert.True(t, present)
	assert.Equal(t, []routeFence{
		{identity: "name=a-route", label: "a-route", matchable: true},
		{identity: "name=b-route", label: "b-route", matchable: true},
		{identity: "endpoint=gateway:9599", label: "routes[3] (endpoint gateway:9599)", matchable: true},
		{identity: "", label: "routes[4]", matchable: false},
	}, fenced, "name preferred, endpoint as fallback identity, neither means unmatchable")
}

func TestFencedRoutes_NoRoutesKey(t *testing.T) {
	cr, err := parseGatewayCR(roleFenced, []byte("kind: Gateway\nspec:\n  replicas: 1\n"))
	require.NoError(t, err)

	fenced, present := fencedRoutes(cr)

	assert.False(t, present, "no spec.routes is a different failure from routes-but-none-fenced")
	assert.Empty(t, fenced)
}

func TestSplitAPIVersion(t *testing.T) {
	group, version := splitAPIVersion("platform.confluent.io/v1beta1")
	assert.Equal(t, "platform.confluent.io", group)
	assert.Equal(t, "v1beta1", version)

	group, version = splitAPIVersion("v1")
	assert.Empty(t, group, "a bare version belongs to the core group")
	assert.Equal(t, "v1", version)
}

// replaceFirst substitutes the first occurrence of old in s, failing the test if
// it is absent — a silently unmatched fixture edit would leave a test asserting
// against the unmodified CR.
func replaceFirst(t *testing.T, s, old, new string) string {
	t.Helper()
	i := strings.Index(s, old)
	require.GreaterOrEqual(t, i, 0, "fixture does not contain %q", old)
	return s[:i] + new + s[i+len(old):]
}
