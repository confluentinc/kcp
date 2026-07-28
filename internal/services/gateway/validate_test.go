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
// A realistic trio modelled on docs/assets/gateway-switchover: the initial CR
// routes to the source cluster, the fenced CR adds a fence to that route, and
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

const fencedCR = `
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
      fence:
        scope: ALL
        errorCode: BROKER_NOT_AVAILABLE
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

// validate runs the validator against the standard namespace/gateway with all
// fixture secrets present, so a test only has to vary the CRs it cares about.
func validate(t *testing.T, initial, fenced, switchover string) (CRValidationResult, error) {
	t.Helper()
	return validateGatewayCRs(context.Background(), clientsetWithSecrets(allTestSecrets...),
		testNamespace, testGateway, []byte(initial), []byte(fenced), []byte(switchover))
}

// ===========================================================================
// Happy path
// ===========================================================================

func TestValidateGatewayCRs_ValidTrio(t *testing.T) {
	result, err := validate(t, initialCR, fencedCR, switchoverCR)

	require.NoError(t, err)
	assert.Empty(t, result.Warnings)
	assert.Empty(t, result.SecretCheckSkipped)
	assert.Equal(t, 4, result.SecretRefsChecked, "vault-config is referenced twice but counted once")
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
			initial, fenced, switchover := read("gateway_init.yaml"), read("gateway_fenced.yaml"), read("gateway_switchover.yaml")

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
				testNamespace, testGateway, initial, fenced, switchover)

			require.NoError(t, err, "a shipped example must pass validation")
			assert.Empty(t, result.Warnings)
			assert.Equal(t, len(expected), result.SecretRefsChecked,
				"every secret the example references must be found and checked")

			// And the walker must find precisely that set — no more, no less.
			refs, unrecognised := map[string]struct{}{}, map[string]struct{}{}
			for _, data := range [][]byte{fenced, switchover} {
				cr, err := parseGatewayCR(roleFenced, data)
				require.NoError(t, err)
				collectSecretRefs(cr.obj, refs, unrecognised)
			}
			assert.ElementsMatch(t, expected, slices.Sorted(maps.Keys(refs)))
			assert.Empty(t, unrecognised, "an unrecognised *Ref field means a secret is going unchecked")
		})
	}
}

// shippedExampleSecrets is every Secret the fenced and switchover CRs of each
// worked example reference, taken from the CRs and cross-checked against the
// `kubectl create secret` commands in each example's README.
var shippedExampleSecrets = map[string][]string{
	"switchover-mtls-to-mtls": {
		"ccloud-client-tls", "gateway-tls", "msk-client-tls",
	},
	"switchover-mtls-to-oauth": {
		"file-store-config", "file-store-idp-credentials", "gateway-tls", "msk-client-tls", "oauth-jaas", "tls",
	},
	"switchover-mtls-to-sasl-plain": {
		"file-store-ccloud-credentials", "file-store-config", "gateway-tls", "msk-client-tls", "plain-jaas", "tls",
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
		"msk-tls", "oauth-jaas", "scram-admin-credentials", "tls", "vault-config",
	},
	"switchover-sasl-scram-to-sasl-plain": {
		"msk-tls", "plain-jaas", "scram-admin-credentials", "tls", "vault-config",
	},
}

// ===========================================================================
// Parsing — terminal, since every later check reads the parsed tree
// ===========================================================================

func TestValidateGatewayCRs_EmptyCR(t *testing.T) {
	_, err := validate(t, initialCR, "   \n", switchoverCR)
	require.ErrorContains(t, err, "the fenced gateway CR is empty")
}

func TestValidateGatewayCRs_MalformedYAML(t *testing.T) {
	_, err := validate(t, initialCR, fencedCR, "kind: Gateway\n  bad: [indent")
	require.ErrorContains(t, err, "failed to parse the switchover gateway CR")
}

func TestValidateGatewayCRs_YAMLIsNotAMapping(t *testing.T) {
	_, err := validate(t, initialCR, "- just\n- a\n- list\n", switchoverCR)
	require.ErrorContains(t, err, "fenced gateway CR")
}

// ===========================================================================
// Shape: kind, apiVersion, spec
// ===========================================================================

func TestValidateGatewayCRs_WrongKind(t *testing.T) {
	// The mistake this guards: ApplyGatewayYAML pushes whatever it is handed
	// through the Gateway GVR, so a Deployment would be applied as the gateway.
	_, err := validate(t, initialCR, fencedCR, `
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
	_, err := validate(t, initialCR, fencedCR, `
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

	_, err := validate(t, initialCR, fencedCR, switchover)

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

	_, err := validate(t, initialCR, fencedCR, switchover)

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

	_, err := validate(t, initial, fencedCR, switchoverCR)

	require.NoError(t, err)
}

func TestValidateGatewayCRs_MissingSpec(t *testing.T) {
	_, err := validate(t, initialCR, fencedCR, `
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
	fenced := replaceFirst(t, fencedCR, "name: migration-gateway", "name: some-other-gateway")

	_, err := validate(t, initialCR, fenced, switchoverCR)

	require.Error(t, err)
	assert.Contains(t, err.Error(), `the fenced gateway CR is named "some-other-gateway" but the migration targets gateway "migration-gateway"`)
}

func TestValidateGatewayCRs_AbsentNameIsAllowed(t *testing.T) {
	// ApplyGatewayYAML fills the name in, so a CR file that omits it works today
	// and must keep working — only a name that is present and wrong is a refusal.
	fenced := replaceFirst(t, fencedCR, "metadata:\n  name: migration-gateway\n", "metadata:\n  labels:\n    app: gw\n")

	_, err := validate(t, initialCR, fenced, switchoverCR)

	require.NoError(t, err)
}

func TestValidateGatewayCRs_NamespaceMismatch(t *testing.T) {
	fenced := replaceFirst(t, fencedCR, "  name: migration-gateway", "  name: migration-gateway\n  namespace: other-ns")

	_, err := validate(t, initialCR, fenced, switchoverCR)

	require.ErrorContains(t, err, `declares namespace "other-ns" but the migration targets namespace "kcp"`)
}

func TestValidateGatewayCRs_InitialCRIdentityNotChecked(t *testing.T) {
	// The initial CR is fetched from the cluster by name, so re-checking its
	// identity would only add noise.
	initial := replaceFirst(t, initialCR, "name: migration-gateway", "name: whatever-the-cluster-said")

	_, err := validate(t, initial, fencedCR, switchoverCR)

	require.NoError(t, err)
}

// ===========================================================================
// Fence blocks
// ===========================================================================

func TestValidateGatewayCRs_FencedCRHasNoFence(t *testing.T) {
	// The silent failure this prevents: kcp reports "Gateway fenced and ready"
	// and then promotes topics while producers are still writing to the source.
	_, err := validate(t, initialCR, initialCR, switchoverCR)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "the fenced gateway CR contains no fence block")
}

func TestValidateGatewayCRs_FencedCRHasNoRoutes(t *testing.T) {
	_, err := validate(t, initialCR, `
apiVersion: platform.confluent.io/v1beta1
kind: Gateway
metadata:
  name: migration-gateway
spec:
  replicas: 3
`, switchoverCR)

	require.ErrorContains(t, err, "declares no spec.routes")
}

func TestValidateGatewayCRs_FenceNotOnARouteWarnsOnly(t *testing.T) {
	// A fence somewhere kcp cannot attribute to a route: don't refuse (the CRD
	// may grow other placements), just say the fence could not be confirmed.
	fenced := replaceFirst(t, initialCR, "spec:\n  replicas: 3", "spec:\n  replicas: 3\n  fence:\n    scope: ALL")

	result, err := validate(t, initialCR, fenced, switchoverCR)

	require.NoError(t, err)
	require.Len(t, result.Warnings, 1)
	assert.Contains(t, result.Warnings[0], "fence block that is not on a route")
}

func TestValidateGatewayCRs_SwitchoverStillFencesMigrationRoute(t *testing.T) {
	// This is the mirror of the 2026-07-27 outcome: the migration completes and
	// reports success while every client stays blocked.
	_, err := validate(t, initialCR, fencedCR, replaceFirst(t, switchoverCR,
		"    - name: migration-route\n      endpoint: gateway:9595",
		"    - name: migration-route\n      endpoint: gateway:9595\n      fence:\n        scope: ALL"))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "the switchover gateway CR still fences the route(s) the fenced CR blocks (migration-route)")
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

	result, err := validate(t, initialCR, fencedCR, switchover)

	require.NoError(t, err)
	require.Len(t, result.Warnings, 1)
	assert.Contains(t, result.Warnings[0], "fences route(s) the migration does not fence (legacy-route)")
}

func TestValidateGatewayCRs_UnnamedRoutesMatchedByEndpoint(t *testing.T) {
	// Regression: routes used to be matched across the two files by position, so
	// this — the same route (port 9595) still fenced after switchover, with the
	// fenced CR holding an extra route ahead of it — passed with only a warning
	// naming the wrong slot. Name is the identity when present; the endpoint is
	// what distinguishes unnamed routes.
	fenced := `
apiVersion: platform.confluent.io/v1beta1
kind: Gateway
metadata:
  name: migration-gateway
spec:
  routes:
    - endpoint: gateway:9599
    - endpoint: gateway:9595
      fence:
        scope: ALL
`
	switchover := `
apiVersion: platform.confluent.io/v1beta1
kind: Gateway
metadata:
  name: migration-gateway
spec:
  replicas: 1
  routes:
    - endpoint: gateway:9595
      fence:
        scope: ALL
`

	_, err := validateGatewayCRs(context.Background(), newFakeClientset(),
		testNamespace, testGateway, []byte(initialCR), []byte(fenced), []byte(switchover))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "still fences the route(s) the fenced CR blocks (routes[0] (endpoint gateway:9595))")
}

func TestValidateGatewayCRs_ExplicitlyNulledFenceIsNotAFence(t *testing.T) {
	// `fence: null` is how a scripted edit removes a fence — server-side apply
	// deletes a nulled field — so this switchover CR works and must not be
	// refused as "still fences".
	switchover := replaceFirst(t, switchoverCR,
		"    - name: migration-route\n      endpoint: gateway:9595",
		"    - name: migration-route\n      endpoint: gateway:9595\n      fence: null")

	_, err := validate(t, initialCR, fencedCR, switchover)

	require.NoError(t, err)
}

func TestValidateGatewayCRs_NulledFenceInFencedCRIsRefused(t *testing.T) {
	// The mirror: a fenced CR whose only fence is nulled does not fence anything.
	fenced := replaceFirst(t, fencedCR, "      fence:\n        scope: ALL\n        errorCode: BROKER_NOT_AVAILABLE", "      fence: null")

	_, err := validate(t, initialCR, fenced, switchoverCR)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "contains no fence block")
}

func TestValidateGatewayCRs_UnmatchableFencedRouteWarns(t *testing.T) {
	// With neither a name nor an endpoint there is nothing to match on, so say
	// that rather than guess either way.
	fenced := replaceFirst(t, fencedCR, "    - name: migration-route\n      endpoint: gateway:9595\n", "    - \n")
	switchover := replaceFirst(t, switchoverCR,
		"    - name: migration-route\n      endpoint: gateway:9595\n",
		"    - fence:\n        scope: ALL\n")

	result, err := validateGatewayCRs(context.Background(), clientsetWithSecrets(allTestSecrets...),
		testNamespace, testGateway, []byte(initialCR), []byte(fenced), []byte(switchover))

	require.NoError(t, err)
	require.NotEmpty(t, result.Warnings)
	assert.Contains(t, strings.Join(result.Warnings, "\n"), "neither a name nor an endpoint")
}

// ===========================================================================
// Wrong-file mistakes
// ===========================================================================

func TestValidateGatewayCRs_FencedAndSwitchoverIdentical(t *testing.T) {
	_, err := validate(t, initialCR, fencedCR, fencedCR)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "identical specs")
}

func TestValidateGatewayCRs_SwitchoverIdenticalToLiveGateway(t *testing.T) {
	// Passing gateway_init.yaml as --switchover-cr-yaml: the apply is a no-op, so
	// it does not even bump metadata.generation for the acceptance check to spot.
	_, err := validate(t, initialCR, fencedCR, initialCR)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "identical to the live gateway's")
}

// ===========================================================================
// Streaming domain wiring
// ===========================================================================

func TestValidateGatewayCRs_DanglingStreamingDomain(t *testing.T) {
	switchover := replaceFirst(t, switchoverCR, "        name: confluent-cloud\n        bootstrapServerId", "        name: typo-cloud\n        bootstrapServerId")

	_, err := validate(t, initialCR, fencedCR, switchover)

	require.Error(t, err)
	assert.Contains(t, err.Error(), `route migration-route points at streaming domain "typo-cloud", which the CR does not declare (declared: confluent-cloud)`)
}

func TestValidateGatewayCRs_UnknownBootstrapServerID(t *testing.T) {
	switchover := replaceFirst(t, switchoverCR, "bootstrapServerId: SASL_PLAIN", "bootstrapServerId: NOPE")

	_, err := validate(t, initialCR, fencedCR, switchover)

	require.Error(t, err)
	assert.Contains(t, err.Error(), `points at bootstrapServerId "NOPE", which streaming domain "confluent-cloud" does not define (defined: SASL_PLAIN)`)
}

func TestValidateGatewayCRs_RouteWithoutStreamingDomainIsAllowed(t *testing.T) {
	// Only what the file states is checked; an absent field is not assumed
	// mandatory.
	switchover := replaceFirst(t, switchoverCR,
		"      streamingDomain:\n        name: confluent-cloud\n        bootstrapServerId: SASL_PLAIN\n", "")

	_, err := validate(t, initialCR, fencedCR, switchover)

	require.NoError(t, err)
}

// ===========================================================================
// Live secret references
// ===========================================================================

func TestValidateGatewayCRs_MissingSecret(t *testing.T) {
	// The 2026-07-27 failure: the switchover CR named a Secret that did not
	// exist, the operator refused the spec and the gateway stayed fenced.
	cs := clientsetWithSecrets("vault-config", "msk-tls", "ccloud-tls")

	_, err := validateGatewayCRs(context.Background(), cs, testNamespace, testGateway,
		[]byte(initialCR), []byte(fencedCR), []byte(switchoverCR))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "reference 1 secret(s) that do not exist in namespace kcp: plain-jaas")
}

func TestValidateGatewayCRs_AllMissingSecretsReportedTogether(t *testing.T) {
	// One run must list every missing secret: fixing them one re-run at a time
	// is exactly the loop this check exists to avoid.
	_, err := validateGatewayCRs(context.Background(), clientsetWithSecrets("vault-config"),
		testNamespace, testGateway, []byte(initialCR), []byte(fencedCR), []byte(switchoverCR))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "reference 3 secret(s) that do not exist in namespace kcp: ccloud-tls, msk-tls, plain-jaas")
}

func TestValidateGatewayCRs_SecretsInOtherNamespaceDoNotCount(t *testing.T) {
	objects := make([]runtime.Object, 0, len(allTestSecrets))
	for _, name := range allTestSecrets {
		objects = append(objects, newSecret("somewhere-else", name))
	}

	_, err := validateGatewayCRs(context.Background(), newFakeClientset(objects...),
		testNamespace, testGateway, []byte(initialCR), []byte(fencedCR), []byte(switchoverCR))

	require.ErrorContains(t, err, "reference 4 secret(s) that do not exist in namespace kcp")
}

func TestValidateGatewayCRs_InitialCRSecretsNotRequired(t *testing.T) {
	// The live initial CR is already running, so its references resolve by
	// construction. Requiring them again would fail migrations for no reason.
	initial := replaceFirst(t, initialCR, "secretRef: msk-tls", "secretRef: long-deleted-secret")

	result, err := validateGatewayCRs(context.Background(), clientsetWithSecrets(allTestSecrets...),
		testNamespace, testGateway, []byte(initial), []byte(fencedCR), []byte(switchoverCR))

	require.NoError(t, err)
	assert.Equal(t, 4, result.SecretRefsChecked, "long-deleted-secret is never looked up")
}

func TestValidateGatewayCRs_SecretReadForbiddenSkipsCheck(t *testing.T) {
	// Reading secrets is a privilege many migration operators lack. A denial must
	// not block a migration that would otherwise succeed.
	cs := clientsetWithSecrets(allTestSecrets...)
	cs.(*kubernetesfake.Clientset).PrependReactor("get", "secrets", func(ktesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(schema.GroupResource{Resource: "secrets"}, "", fmt.Errorf("no access"))
	})

	result, err := validateGatewayCRs(context.Background(), cs, testNamespace, testGateway,
		[]byte(initialCR), []byte(fencedCR), []byte(switchoverCR))

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
		[]byte(initialCR), []byte(fencedCR), []byte(switchoverCR))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to check whether secret")
	assert.Contains(t, err.Error(), "etcd is having a moment")
}

func TestValidateGatewayCRs_NoSecretRefs(t *testing.T) {
	fenced := `
apiVersion: platform.confluent.io/v1beta1
kind: Gateway
metadata:
  name: migration-gateway
spec:
  routes:
    - name: migration-route
      fence:
        scope: ALL
`
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
		testNamespace, testGateway, []byte(initialCR), []byte(fenced), []byte(switchover))

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
	fenced := replaceFirst(t, fencedCR, "name: migration-gateway", "name: migration-gatway")

	_, err := validateGatewayCRs(context.Background(), clientsetWithSecrets("vault-config"),
		testNamespace, testGateway, []byte(initialCR), []byte(fenced), []byte(switchoverCR))

	require.Error(t, err)
	assert.Contains(t, err.Error(), `is named "migration-gatway"`)
	assert.Contains(t, err.Error(), "reference 3 secret(s) that do not exist in namespace kcp: ccloud-tls, msk-tls, plain-jaas")
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

	_, err := validate(t, initialCR, fencedCR, switchover)

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
		testNamespace, testGateway, []byte(initialCR), []byte(fencedCR), []byte(switchover))

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
			testNamespace, testGateway, []byte(initialCR), []byte(aliasBombCR), []byte(switchoverCR))
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
