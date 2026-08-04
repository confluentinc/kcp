package gateway

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/goccy/go-yaml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// --- generateConfigID -------------------------------------------------------

func TestGenerateConfigID_SatisfiesCRDConstraints(t *testing.T) {
	now := time.Date(2026, 8, 3, 10, 53, 11, 0, time.UTC)

	for i := 0; i < 200; i++ {
		id := generateConfigID(now)

		assert.LessOrEqual(t, len(id), maxConfigIDLength,
			"configId must fit the CRD maxLength")
		assert.NotEmpty(t, id, "configId must be non-empty (CRD pattern requires 1+ chars)")
		assert.Regexp(t, configIDPattern, id,
			"configId must match the CRD pattern ^[a-zA-Z0-9._:-]+$")
	}
}

// The CRD rejects base64 padding characters, so a base64-derived id would break
// the apply. Guard the charset explicitly.
func TestGenerateConfigID_RejectsBase64PaddingChars(t *testing.T) {
	id := generateConfigID(time.Now())

	for _, bad := range []string{"+", "/", "="} {
		assert.NotContains(t, id, bad,
			"configId must not contain base64 padding char %q — the CRD pattern rejects it", bad)
	}
}

func TestGenerateConfigID_IsUniqueAcrossCallsAtTheSameInstant(t *testing.T) {
	// Two applies inside the same second must still get distinct ids, otherwise
	// a re-apply looks like a no-op and /config polling can never distinguish
	// "already converged" from "never applied".
	now := time.Date(2026, 8, 3, 10, 53, 11, 0, time.UTC)

	seen := make(map[string]struct{}, 500)
	for i := 0; i < 500; i++ {
		id := generateConfigID(now)
		_, dup := seen[id]
		require.False(t, dup, "generateConfigID produced a duplicate: %q", id)
		seen[id] = struct{}{}
	}
}

func TestGenerateConfigID_IsRecognisablyKCPOwned(t *testing.T) {
	id := generateConfigID(time.Now())
	assert.True(t, strings.HasPrefix(id, configIDPrefix),
		"configId should be attributable to kcp, got %q", id)
}

// --- injectConfigID ---------------------------------------------------------

const fencedCRYAML = `apiVersion: platform.confluent.io/v1beta1
kind: Gateway
metadata:
  name: migration-gateway
  namespace: confluent
spec:
  replicas: 3
  hotReload:
    enabled: true
  routes:
    - name: migration-route
      fence:
        scope: ALL
        errorCode: BROKER_NOT_AVAILABLE
      streamingDomain:
        name: source-kafka-cluster
`

func specOf(t *testing.T, out []byte) map[string]any {
	t.Helper()
	var obj map[string]any
	require.NoError(t, yaml.Unmarshal(out, &obj))
	spec, ok := obj["spec"].(map[string]any)
	require.True(t, ok, "spec missing or not a map in %s", out)
	return spec
}

func TestInjectConfigID_SetsFieldWhenAbsent(t *testing.T) {
	out, err := injectConfigID([]byte(fencedCRYAML), "kcp-1-abc")
	require.NoError(t, err)

	assert.Equal(t, "kcp-1-abc", specOf(t, out)["configId"])
}

func TestInjectConfigID_OverwritesExistingValue(t *testing.T) {
	withExisting := strings.Replace(fencedCRYAML,
		"spec:\n  replicas: 3", "spec:\n  configId: stale-000\n  replicas: 3", 1)
	require.Contains(t, withExisting, "stale-000", "fixture setup")

	out, err := injectConfigID([]byte(withExisting), "kcp-2-def")
	require.NoError(t, err)

	assert.Equal(t, "kcp-2-def", specOf(t, out)["configId"])
}

// The fenced CR is user-supplied and applied byte-for-byte via SSA. Injection
// must not disturb anything else — especially the fence block, which is the
// entire point of the apply.
func TestInjectConfigID_PreservesEverythingElse(t *testing.T) {
	out, err := injectConfigID([]byte(fencedCRYAML), "kcp-3-ghi")
	require.NoError(t, err)

	var got, want map[string]any
	require.NoError(t, yaml.Unmarshal(out, &got))
	require.NoError(t, yaml.Unmarshal([]byte(fencedCRYAML), &want))

	// Remove the injected key; the rest must be byte-identical in structure.
	delete(got["spec"].(map[string]any), "configId")
	assert.Equal(t, want, got, "injection changed something other than spec.configId")
}

func TestInjectConfigID_PreservesFenceBlockSpecifically(t *testing.T) {
	out, err := injectConfigID([]byte(fencedCRYAML), "kcp-4-jkl")
	require.NoError(t, err)

	routes, ok := specOf(t, out)["routes"].([]any)
	require.True(t, ok, "routes missing")
	require.Len(t, routes, 1)

	route := routes[0].(map[string]any)
	fence, ok := route["fence"].(map[string]any)
	require.True(t, ok, "fence block lost during injection")
	assert.Equal(t, "ALL", fence["scope"])
	assert.Equal(t, "BROKER_NOT_AVAILABLE", fence["errorCode"])
}

func TestInjectConfigID_Errors(t *testing.T) {
	tests := []struct {
		name    string
		crYAML  string
		id      string
		wantErr string
	}{
		{
			name:    "unparseable yaml",
			crYAML:  "spec: {this: is: not: valid",
			id:      "kcp-1-a",
			wantErr: "parse",
		},
		{
			name:    "missing spec",
			crYAML:  "apiVersion: platform.confluent.io/v1beta1\nkind: Gateway\n",
			id:      "kcp-1-a",
			wantErr: "spec",
		},
		{
			name:    "spec is not a map",
			crYAML:  "kind: Gateway\nspec: just-a-string\n",
			id:      "kcp-1-a",
			wantErr: "spec",
		},
		{
			name:    "empty id",
			crYAML:  fencedCRYAML,
			id:      "",
			wantErr: "empty",
		},
		{
			name:    "empty yaml",
			crYAML:  "",
			id:      "kcp-1-a",
			wantErr: "spec",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := injectConfigID([]byte(tt.crYAML), tt.id)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

// The injected YAML must survive the same decode path ApplyGatewayYAML uses,
// which unmarshals straight into unstructured.Unstructured's map.
func TestInjectConfigID_OutputDecodesForServerSideApply(t *testing.T) {
	out, err := injectConfigID([]byte(fencedCRYAML), "kcp-5-mno")
	require.NoError(t, err)

	var obj map[string]any
	require.NoError(t, yaml.Unmarshal(out, &obj),
		"injected YAML must decode the way ApplyGatewayYAML decodes it")
	assert.Equal(t, "Gateway", obj["kind"])
}

// --- eligiblePods -----------------------------------------------------------

func pod(name string, phase corev1.PodPhase, ready bool, terminating bool) corev1.Pod {
	p := corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Status: corev1.PodStatus{
			Phase: phase,
			Conditions: []corev1.PodCondition{{
				Type:   corev1.PodReady,
				Status: corev1.ConditionFalse,
			}},
		},
	}
	if ready {
		p.Status.Conditions[0].Status = corev1.ConditionTrue
	}
	if terminating {
		now := metav1.Now()
		p.DeletionTimestamp = &now
	}
	return p
}

func TestEligiblePods_KeepsOnlyRunningReadyNonTerminating(t *testing.T) {
	pods := []corev1.Pod{
		pod("live-b", corev1.PodRunning, true, false),
		pod("evicted", corev1.PodFailed, false, false),
		pod("pending", corev1.PodPending, false, false),
		pod("running-not-ready", corev1.PodRunning, false, false),
		pod("terminating", corev1.PodRunning, true, true),
		pod("live-a", corev1.PodRunning, true, false),
		pod("succeeded", corev1.PodSucceeded, false, false),
	}

	got := eligiblePods(pods)

	// Sorted, so callers and log output are deterministic.
	assert.Equal(t, []string{"live-a", "live-b"}, got)
}

// The measured hazard: a 3-replica gateway whose label selector returned 6 pods,
// 3 of them evicted with stale pod IPs. An unfiltered set never converges.
func TestEligiblePods_HandlesTheSixPodsForThreeReplicasCase(t *testing.T) {
	pods := []corev1.Pod{
		pod("gw-2ghff", corev1.PodRunning, true, false),
		pod("gw-4fcxl", corev1.PodRunning, true, false),
		pod("gw-c2tmb", corev1.PodRunning, true, false),
		pod("gw-6t2ws", corev1.PodFailed, false, false),
		pod("gw-fljgf", corev1.PodFailed, false, false),
		pod("gw-qxvq9", corev1.PodFailed, false, false),
	}

	assert.Len(t, eligiblePods(pods), 3)
}

func TestEligiblePods_EmptyInput(t *testing.T) {
	assert.Empty(t, eligiblePods(nil))
	assert.Empty(t, eligiblePods([]corev1.Pod{}))
}

// --- parseConfigResponse ----------------------------------------------------

func TestParseConfigResponse_ValidWithNanosecondPrecision(t *testing.T) {
	// Real observed shape: 9 fractional digits, not the 3 the contract doc shows.
	raw := []byte(`{"configId":"fence-101","appliedAt":"2026-08-03T10:53:18.507420686Z"}`)

	id, appliedAt, err := parseConfigResponse(raw)
	require.NoError(t, err)
	require.NotNil(t, id)

	assert.Equal(t, "fence-101", *id)
	assert.Equal(t, 507420686, appliedAt.Nanosecond(),
		"nanosecond precision must survive parsing")
	assert.True(t, appliedAt.Equal(
		time.Date(2026, 8, 3, 10, 53, 18, 507420686, time.UTC)))
}

func TestParseConfigResponse_MillisecondPrecisionAlsoParses(t *testing.T) {
	// The shape documented in the contract.
	raw := []byte(`{"configId":"x","appliedAt":"2026-07-06T10:15:30.123Z"}`)

	_, appliedAt, err := parseConfigResponse(raw)
	require.NoError(t, err)
	assert.Equal(t, 123000000, appliedAt.Nanosecond())
}

func TestParseConfigResponse_NullConfigIDMeansNeverSet(t *testing.T) {
	raw := []byte(`{"configId":null,"appliedAt":null}`)

	id, appliedAt, err := parseConfigResponse(raw)
	require.NoError(t, err, "null configId is a valid documented state, not an error")

	assert.Nil(t, id, "null configId must be nil, not empty string")
	assert.True(t, appliedAt.IsZero())
}

// An evicted pod returned HTTP 200 with an empty body through the proxy. That
// must not read as "no configId set" — it is a probe failure.
func TestParseConfigResponse_EmptyBodyIsAnError(t *testing.T) {
	for _, raw := range []string{"", "   ", "\n\t "} {
		_, _, err := parseConfigResponse([]byte(raw))
		require.Error(t, err, "empty body %q must error", raw)
		assert.Contains(t, err.Error(), "empty")
	}
}

func TestParseConfigResponse_MalformedJSONIsAnError(t *testing.T) {
	_, _, err := parseConfigResponse([]byte(`<html>404 not found</html>`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "decode")
}

// appliedAt is corroborating-only (it is pod-boot time, not hot-reload time),
// so a malformed value must not fail a probe whose configId is good.
func TestParseConfigResponse_MalformedAppliedAtIsLenient(t *testing.T) {
	raw := []byte(`{"configId":"fence-101","appliedAt":"not-a-timestamp"}`)

	id, appliedAt, err := parseConfigResponse(raw)
	require.NoError(t, err, "appliedAt is corroborating-only and must not gate the probe")
	require.NotNil(t, id)

	assert.Equal(t, "fence-101", *id)
	assert.True(t, appliedAt.IsZero(), "unparseable appliedAt should yield the zero time")
}

func TestParseConfigResponse_IgnoresUnknownFields(t *testing.T) {
	raw := []byte(`{"configId":"a","appliedAt":"2026-08-03T10:00:00Z","futureField":42}`)

	id, _, err := parseConfigResponse(raw)
	require.NoError(t, err)
	require.NotNil(t, id)
	assert.Equal(t, "a", *id)
}

// --- converged --------------------------------------------------------------

func result(name, id string) podConfig {
	return podConfig{Pod: name, ConfigID: &id}
}

func TestConverged_AllPodsReportTargetAndCountMatches(t *testing.T) {
	results := []podConfig{result("a", "t1"), result("b", "t1"), result("c", "t1")}

	ok, reason := converged(results, "t1", 3)
	assert.True(t, ok, "reason: %s", reason)
}

// A gateway scaled to zero has no pods, so "every pod reports the new configId"
// is vacuously true. That must never count as converged.
func TestConverged_ZeroPodsIsNeverConverged(t *testing.T) {
	ok, reason := converged(nil, "t1", 0)
	assert.False(t, ok)
	assert.Contains(t, reason, "no")

	ok, reason = converged([]podConfig{}, "t1", 3)
	assert.False(t, ok)
	assert.NotEmpty(t, reason)
}

// Mid-roll the label selector returns old + new + failed pods, so the eligible
// count drifts from the Deployment's ready count. Converging on a subset would
// report success while some pods still serve the old config.
func TestConverged_PodCountMustMatchDeploymentReadyReplicas(t *testing.T) {
	results := []podConfig{result("a", "t1"), result("b", "t1")}

	ok, reason := converged(results, "t1", 3)
	assert.False(t, ok, "2 pods agreeing must not satisfy a 3-replica gateway")
	assert.Contains(t, reason, "3")
}

func TestConverged_UnknownReplicaCountSkipsTheCountCheck(t *testing.T) {
	// wantReplicas == 0 means the Deployment was unreadable; fall back to
	// "all eligible pods agree" rather than blocking forever.
	results := []podConfig{result("a", "t1"), result("b", "t1")}

	ok, reason := converged(results, "t1", 0)
	assert.True(t, ok, "reason: %s", reason)
}

func TestConverged_StalePodBlocksConvergence(t *testing.T) {
	results := []podConfig{result("a", "t1"), result("b", "old-000"), result("c", "t1")}

	ok, reason := converged(results, "t1", 3)
	assert.False(t, ok)
	assert.Contains(t, reason, "2", "reason should report how many of 3 applied")
}

func TestConverged_ErroringPodBlocksConvergence(t *testing.T) {
	results := []podConfig{
		result("a", "t1"),
		{Pod: "b", Err: fmt.Errorf("no route to host")},
		result("c", "t1"),
	}

	ok, reason := converged(results, "t1", 3)
	assert.False(t, ok, "an unreachable pod must not be treated as converged")
	assert.NotEmpty(t, reason)
}

func TestConverged_NilConfigIDBlocksConvergence(t *testing.T) {
	results := []podConfig{result("a", "t1"), {Pod: "b", ConfigID: nil}}

	ok, _ := converged(results, "t1", 2)
	assert.False(t, ok, "a pod that never had a configId set is not converged")
}

// Errors and progress reach the terminal, so they must carry counts rather than
// pod-name lists.
func TestConverged_ReasonReportsCountsNotPodNames(t *testing.T) {
	results := []podConfig{
		result("gw-abcdef-11111", "t1"),
		result("gw-abcdef-22222", "old"),
		result("gw-abcdef-33333", "old"),
	}

	_, reason := converged(results, "t1", 3)

	for _, r := range results {
		assert.NotContains(t, reason, r.Pod,
			"reason must not embed pod names; counts only")
	}
}

// --- cross-check against the real endpoint payload --------------------------

// Guards the two-stage flow end to end on the exact bytes observed from a live
// gateway: parse the response, then decide convergence.
func TestParseThenConverge_OnRealPayloads(t *testing.T) {
	payloads := map[string]string{
		"gw-2ghff": `{"configId":"fence-101","appliedAt":"2026-08-03T10:53:18.507420686Z"}`,
		"gw-4fcxl": `{"configId":"fence-101","appliedAt":"2026-08-03T10:53:18.680927533Z"}`,
		"gw-c2tmb": `{"configId":"fence-101","appliedAt":"2026-08-03T10:53:18.589685790Z"}`,
	}

	var results []podConfig
	for name, body := range payloads {
		id, _, err := parseConfigResponse([]byte(body))
		require.NoError(t, err)
		results = append(results, podConfig{Pod: name, ConfigID: id})
	}

	ok, reason := converged(results, "fence-101", 3)
	assert.True(t, ok, "reason: %s", reason)

	// The rejected-fence run: pods held the previous id for the whole poll.
	ok, _ = converged(results, "badfence-102", 3)
	assert.False(t, ok, "pods still on the old configId must not converge")
}

// Sanity: the response shape we decode matches what the contract documents.
func TestConfigResponseShapeMatchesContract(t *testing.T) {
	documented := `{"configId":"3f1a9c8e-1b2c-4d5e-8f90-a1b2c3d4e5f6","appliedAt":"2026-07-06T10:15:30.123Z"}`

	var probe map[string]any
	require.NoError(t, json.Unmarshal([]byte(documented), &probe))
	require.Contains(t, probe, "configId")
	require.Contains(t, probe, "appliedAt")

	id, appliedAt, err := parseConfigResponse([]byte(documented))
	require.NoError(t, err)
	require.NotNil(t, id)
	assert.Equal(t, "3f1a9c8e-1b2c-4d5e-8f90-a1b2c3d4e5f6", *id)
	assert.False(t, appliedAt.IsZero())
}
