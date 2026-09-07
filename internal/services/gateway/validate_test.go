package gateway

import (
	"context"
	"fmt"
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
// ===========================================================================

const (
	testNamespace = "kcp"
	testGateway   = "migration-gateway"
)

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

// redundantAuthCR is a live-read initial gateway CR modelled on the
// redundant-auth runbook (~/gateway-testing/gateway-static-redundant-auth.yaml):
// a route currently bound to cp-a, with security.cluster pre-staging swap auth
// for BOTH cp-a and cp-b even though only cp-a is bound today. The default
// fixture switches migration-route to cp-b.
const redundantAuthCR = `
apiVersion: platform.confluent.io/v1beta1
kind: Gateway
metadata:
  name: migration-gateway
spec:
  secretStores:
    - name: file-store-cp-a
      provider:
        type: File
        configSecretRef: file-store-config
        clientCredentialsRef: file-store-cp-a-credentials
    - name: file-store-cp-b
      provider:
        type: File
        configSecretRef: file-store-config
        clientCredentialsRef: file-store-cp-b-credentials
  streamingDomains:
    - name: cp-a
      type: kafka
      kafkaCluster:
        bootstrapServers:
          - id: SASL_PLAIN
            endpoint: SASL_PLAINTEXT://kafka.cp-a:9092
        nodeIdRanges:
          - name: pool-1
            start: 0
            end: 5
    - name: cp-b
      type: kafka
      kafkaCluster:
        bootstrapServers:
          - id: SASL_PLAIN
            endpoint: SASL_PLAINTEXT://kafka.cp-b:9092
  routes:
    - name: migration-route
      endpoint: gateway:9595
      streamingDomain:
        name: cp-a
        bootstrapServerId: SASL_PLAIN
      security:
        cluster:
          cp-a:
            auth: swap
            secretStore: file-store-cp-a
            authentication:
              type: plain
              jaasConfigPassThrough:
                secretRef: plain-jaas-cp-a
          cp-b:
            auth: swap
            secretStore: file-store-cp-b
            authentication:
              type: plain
              jaasConfigPassThrough:
                secretRef: plain-jaas-cp-b
`

// redundantAuthSecrets are every secret redundantAuthCR references.
var redundantAuthSecrets = []string{
	"file-store-config", "file-store-cp-a-credentials", "file-store-cp-b-credentials",
	"plain-jaas-cp-a", "plain-jaas-cp-b",
}

// switchToCPB is the standard target: migration-route flips from its current
// cp-a binding to cp-b, which redundantAuthCR already stages.
var switchToCPB = []RouteSwitchoverTarget{
	{RouteName: "migration-route", StreamingDomainName: "cp-b", BootstrapServerId: "SASL_PLAIN"},
}

// check runs checkRedundantAuthStaged against the standard namespace with all
// fixture secrets present, so a test only has to vary what it cares about.
func check(t *testing.T, initial string, targets []RouteSwitchoverTarget) (CRValidationResult, error) {
	t.Helper()
	return checkRedundantAuthStaged(context.Background(), clientsetWithSecrets(redundantAuthSecrets...),
		testNamespace, []byte(initial), targets)
}

// ===========================================================================
// Happy path
// ===========================================================================

func TestCheckRedundantAuthStaged_ValidTarget(t *testing.T) {
	result, err := check(t, redundantAuthCR, switchToCPB)

	require.NoError(t, err)
	assert.Empty(t, result.Warnings)
	assert.Empty(t, result.SecretCheckSkipped)
	assert.Equal(t, len(redundantAuthSecrets), result.SecretRefsChecked,
		"every secret in the initial CR is checked, including the not-yet-exercised target block")
}

// ===========================================================================
// Parsing — terminal, since every later check reads the parsed tree
// ===========================================================================

func TestCheckRedundantAuthStaged_EmptyCR(t *testing.T) {
	_, err := check(t, "   \n", switchToCPB)
	require.ErrorContains(t, err, "the initial gateway CR is empty")
}

func TestCheckRedundantAuthStaged_MalformedYAML(t *testing.T) {
	_, err := check(t, "kind: Gateway\n  bad: [indent", switchToCPB)
	require.ErrorContains(t, err, "failed to parse the initial gateway CR")
}

// ===========================================================================
// Route lookup
// ===========================================================================

func TestCheckRedundantAuthStaged_RouteNotFoundInInitialCR(t *testing.T) {
	_, err := check(t, redundantAuthCR, []RouteSwitchoverTarget{
		{RouteName: "no-such-route", StreamingDomainName: "cp-b", BootstrapServerId: "SASL_PLAIN"},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `route "no-such-route" not found`)
}

// ===========================================================================
// Streaming domain wiring — the target must actually be declared
// ===========================================================================

func TestCheckRedundantAuthStaged_TargetDomainNotDeclared(t *testing.T) {
	_, err := check(t, redundantAuthCR, []RouteSwitchoverTarget{
		{RouteName: "migration-route", StreamingDomainName: "cp-c", BootstrapServerId: "SASL_PLAIN"},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `streaming domain "cp-c", which the initial gateway CR does not declare (declared: cp-a, cp-b)`)
}

func TestCheckRedundantAuthStaged_TargetBootstrapServerIdNotDefined(t *testing.T) {
	_, err := check(t, redundantAuthCR, []RouteSwitchoverTarget{
		{RouteName: "migration-route", StreamingDomainName: "cp-b", BootstrapServerId: "NOPE"},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `bootstrapServerId "NOPE", which streaming domain "cp-b" does not define (defined: SASL_PLAIN)`)
}

// ===========================================================================
// Pre-staged auth — the whole premise of a safe inline switch
// ===========================================================================

func TestCheckRedundantAuthStaged_RouteMissingStagedAuthForTarget(t *testing.T) {
	// cp-c is declared as a domain but the route's security.cluster carries no
	// pre-staged block for it — the redundant-auth premise does not hold.
	withCPC := replaceFirst(t, redundantAuthCR,
		"    - name: cp-b\n      type: kafka\n",
		"    - name: cp-b\n      type: kafka\n"+
			"      kafkaCluster:\n        bootstrapServers:\n          - id: SASL_PLAIN\n            endpoint: x\n"+
			"    - name: cp-c\n      type: kafka\n")
	withCPC = replaceFirst(t, withCPC,
		"      kafkaCluster:\n        bootstrapServers:\n          - id: SASL_PLAIN\n            endpoint: SASL_PLAINTEXT://kafka.cp-b:9092\n",
		"      kafkaCluster:\n        bootstrapServers:\n          - id: SASL_PLAIN\n            endpoint: SASL_PLAINTEXT://kafka.cp-b:9092\n    - name: cp-c\n      type: kafka\n      kafkaCluster:\n        bootstrapServers:\n          - id: SASL_PLAIN\n            endpoint: x\n")

	_, err := check(t, withCPC, []RouteSwitchoverTarget{
		{RouteName: "migration-route", StreamingDomainName: "cp-c", BootstrapServerId: "SASL_PLAIN"},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no pre-staged security.cluster.cp-c block")
}

func TestCheckRedundantAuthStaged_StagedAuthMissingSecretStore(t *testing.T) {
	broken := replaceFirst(t, redundantAuthCR,
		"          cp-b:\n            auth: swap\n            secretStore: file-store-cp-b\n",
		"          cp-b:\n            auth: swap\n")
	_, err := check(t, broken, switchToCPB)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "security.cluster.cp-b has no secretStore")
}

func TestCheckRedundantAuthStaged_StagedAuthMissingAuthentication(t *testing.T) {
	broken := replaceFirst(t, redundantAuthCR,
		"            authentication:\n              type: plain\n              jaasConfigPassThrough:\n                secretRef: plain-jaas-cp-b\n",
		"")
	_, err := check(t, broken, switchToCPB)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "security.cluster.cp-b has no authentication block")
}

// ===========================================================================
// No-op guard — the flip must be a real change
// ===========================================================================

func TestCheckRedundantAuthStaged_RouteAlreadyBoundToTarget(t *testing.T) {
	_, err := check(t, redundantAuthCR, []RouteSwitchoverTarget{
		{RouteName: "migration-route", StreamingDomainName: "cp-a", BootstrapServerId: "SASL_PLAIN"},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `already bound to streaming domain "cp-a"`)
}

// ===========================================================================
// Live secret references — the whole initial CR is in scope
// ===========================================================================

func TestCheckRedundantAuthStaged_TargetSecretRefMissing(t *testing.T) {
	cs := clientsetWithSecrets("file-store-config", "file-store-cp-a-credentials", "file-store-cp-b-credentials", "plain-jaas-cp-a")
	_, err := checkRedundantAuthStaged(context.Background(), cs, testNamespace, []byte(redundantAuthCR), switchToCPB)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "reference 1 secret(s) that do not exist in namespace kcp: plain-jaas-cp-b")
}

func TestCheckRedundantAuthStaged_SecretReadForbiddenSkipsCheck(t *testing.T) {
	cs := clientsetWithSecrets(redundantAuthSecrets...)
	cs.(*kubernetesfake.Clientset).PrependReactor("get", "secrets", func(ktesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(schema.GroupResource{Resource: "secrets"}, "", fmt.Errorf("no access"))
	})

	result, err := checkRedundantAuthStaged(context.Background(), cs, testNamespace, []byte(redundantAuthCR), switchToCPB)

	require.NoError(t, err)
	assert.Equal(t, "no permission to read secrets in namespace kcp", result.SecretCheckSkipped)
	assert.Zero(t, result.SecretRefsChecked, "a partial count would read as a pass")
}

func TestCheckRedundantAuthStaged_UnrecognisedRefFieldWarns(t *testing.T) {
	withUnknownRef := replaceFirst(t, redundantAuthCR,
		"                secretRef: plain-jaas-cp-b\n",
		"                someFutureCredentialRef: unseen-secret\n")

	result, err := check(t, withUnknownRef, switchToCPB)

	require.NoError(t, err)
	require.NotEmpty(t, result.Warnings)
	assert.Contains(t, result.Warnings[0], "someFutureCredentialRef")
}

// ===========================================================================
// Aggregation
// ===========================================================================

func TestCheckRedundantAuthStaged_MultipleTargetsReportAllProblems(t *testing.T) {
	_, err := check(t, redundantAuthCR, []RouteSwitchoverTarget{
		{RouteName: "migration-route", StreamingDomainName: "cp-a", BootstrapServerId: "SASL_PLAIN"}, // already bound
		{RouteName: "no-such-route", StreamingDomainName: "cp-b", BootstrapServerId: "SASL_PLAIN"},   // route missing
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "2 problem(s) found:")
	assert.Contains(t, err.Error(), "already bound")
	assert.Contains(t, err.Error(), `route "no-such-route" not found`)
}

// ===========================================================================
// Helper unit tests
// ===========================================================================

func TestCollectSecretRefs_WalksEveryDepth(t *testing.T) {
	cr, err := parseGatewayCR(roleInitial, []byte(redundantAuthCR))
	require.NoError(t, err)

	refs, unrecognised := map[string]struct{}{}, map[string]struct{}{}
	collectSecretRefs(cr.obj, refs, unrecognised)

	assert.Equal(t, map[string]struct{}{
		"file-store-config":           {}, // secretStores[].provider.configSecretRef
		"file-store-cp-a-credentials": {}, // secretStores[].provider.clientCredentialsRef
		"file-store-cp-b-credentials": {},
		"plain-jaas-cp-a":             {}, // routes[].security.cluster.<domain>.authentication.jaasConfigPassThrough.secretRef
		"plain-jaas-cp-b":             {},
	}, refs)
	assert.Empty(t, unrecognised)
}

func TestCollectSecretRefs_ClientCredentialsRef(t *testing.T) {
	// Regression: clientCredentialsRef names a Secret in four of the eight shipped
	// switchover examples and was not collected, so the operator got a green tick
	// over exactly the missing-secret failure this validation exists to catch.
	cr, err := parseGatewayCR(roleInitial, []byte(`
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
	cr, err := parseGatewayCR(roleInitial, []byte(`
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

func TestCollectSecretRefs_DescendsIntoNonStringSecretRef(t *testing.T) {
	// A future object-valued secretRef must still be walked, not swallowed.
	cr, err := parseGatewayCR(roleInitial, []byte(`
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

func TestCheckRedundantAuthStaged_AliasBombTerminates(t *testing.T) {
	// Without the walk guard this pins one core for hours on a flat heap, with no
	// ctx check to interrupt it — a sub-kilobyte CR file hanging `migration init`.
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = checkRedundantAuthStaged(context.Background(), clientsetWithSecrets(redundantAuthSecrets...),
			testNamespace, []byte(aliasBombCR), switchToCPB)
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
	cr, err := parseGatewayCR(roleInitial, []byte(`
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
	cr, err := parseGatewayCR(roleInitial, []byte(aliasBombCR))
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
