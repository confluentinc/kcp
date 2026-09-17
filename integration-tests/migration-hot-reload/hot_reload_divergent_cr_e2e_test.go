//go:build e2e

// Hot-reload discrepancies between the live Gateway CR and the CRs a migration is
// about to apply.
//
// kcp applies the operator's fenced and switchover files, so those files can change
// how the running gateway behaves: one that enables hot-reload converts every later
// transition to an in-place apply, one that disables it starts rolling pods that
// were not rolling before. kcp makes neither change on the operator's behalf — it
// refuses and names the file.
//
// Both tests here exercise that refusal purely through DetectCapability's read-only
// check — neither applies a CR or mutates the live gateway, so both survive
// the SSA-apply removal untouched. Two siblings that DID exercise the refusal via a
// real apply, and a pair that proved the server-side-apply field-ownership mechanism
// the refusal exists to avoid (a narrowing apply from kcp's own field manager
// pruning a field an earlier apply from that manager declared, and an omission of an
// already-solely-owned field being rejected by the CRD outright), were removed when
// kcp moved off full-CR SSA apply onto targeted JSON patches — a targeted RoutePatch
// has no whole-object merge or field-manager ownership semantics, so that mechanism
// no longer exists in production to test.
package hotreload

import (
	"context"
	"testing"

	"github.com/confluentinc/kcp/internal/services/gateway"
	"github.com/goccy/go-yaml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// withHotReload returns crYAML with spec.hotReload.enabled set to enabled.
func withHotReload(t *testing.T, crYAML []byte, enabled bool) []byte {
	t.Helper()

	var obj map[string]any
	require.NoError(t, yaml.Unmarshal(crYAML, &obj))

	spec, ok := obj["spec"].(map[string]any)
	require.True(t, ok, "the CR must have a spec to edit")
	spec["hotReload"] = map[string]any{"enabled": enabled}

	out, err := yaml.Marshal(obj)
	require.NoError(t, err)
	return out
}

// withoutHotReload returns crYAML with the spec.hotReload block removed, which is
// the shape of every example under docs/assets/gateway-switchover.
func withoutHotReload(t *testing.T, crYAML []byte) []byte {
	t.Helper()

	var obj map[string]any
	require.NoError(t, yaml.Unmarshal(crYAML, &obj))

	spec, ok := obj["spec"].(map[string]any)
	require.True(t, ok, "the CR must have a spec to edit")
	delete(spec, "hotReload")

	out, err := yaml.Marshal(obj)
	require.NoError(t, err)
	return out
}

// declaresHotReload reports what a CR file says, so a test can state its premise
// rather than assume the rendered fixtures still hold it.
func declaresHotReload(t *testing.T, crYAML []byte) bool {
	t.Helper()

	var obj map[string]any
	require.NoError(t, yaml.Unmarshal(crYAML, &obj))

	spec, ok := obj["spec"].(map[string]any)
	if !ok {
		return false
	}
	hr, ok := spec["hotReload"].(map[string]any)
	if !ok {
		return false
	}
	enabled, _ := hr["enabled"].(bool)
	return enabled
}

// liveHotReload reads spec.hotReload.enabled off the running gateway. A plain read
// for inspection only — unlike the old readCR, it does not strip server-managed
// metadata, since nothing here re-applies the result.
func (e *env) liveHotReload(t *testing.T, ctx context.Context) bool {
	t.Helper()

	raw, err := e.svc.GetGatewayYAML(ctx, e.namespace, e.gateway)
	require.NoError(t, err)
	return declaresHotReload(t, raw)
}

// TestPlannedCRThatWouldDisableHotReloadIsRefused covers the direction that matters
// just as much as turning hot-reload on: the operator's gateway would start rolling
// pods on every transition because of a file kcp applied. Detection must refuse
// rather than adopt it.
func TestPlannedCRThatWouldDisableHotReloadIsRefused(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)

	require.True(t, e.liveHotReload(t, ctx), "the rig's gateway must be running hot-reload")

	fenced := withHotReload(t, mustReadFile(t, "KCP_HR_FENCED_CR"), false)
	switchover := withHotReload(t, mustReadFile(t, "KCP_HR_SWITCHOVER_CR"), false)

	_, err := e.svc.DetectCapability(ctx, e.namespace, e.gateway,
		gateway.DefaultGatewayConfigPort, fenced, switchover)
	require.Error(t, err, "kcp must not turn hot-reload off for a gateway that is running it")

	assert.Contains(t, err.Error(), "fenced")
	assert.Contains(t, err.Error(), "spec.hotReload.enabled")
	t.Logf("refused with: %v", err)
}

// TestPlannedCRsMustAgreeOnMentioningHotReload covers the fenced and switchover
// files disagreeing on whether they mention spec.hotReload at all: the fenced CR
// declares it, the switchover CR silently omits it. kcp must refuse the pair rather
// than let a later apply of the switchover CR interact with whatever the fence apply
// left declared.
func TestPlannedCRsMustAgreeOnMentioningHotReload(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)

	require.True(t, e.liveHotReload(t, ctx), "the rig's gateway must be running hot-reload")

	fenced := mustReadFile(t, "KCP_HR_FENCED_CR")
	switchoverSilent := withoutHotReload(t, mustReadFile(t, "KCP_HR_SWITCHOVER_CR"))
	require.True(t, declaresHotReload(t, fenced))
	require.False(t, declaresHotReload(t, switchoverSilent))

	t.Run("kcp refuses the mismatched pair", func(t *testing.T) {
		_, err := e.svc.DetectCapability(ctx, e.namespace, e.gateway,
			gateway.DefaultGatewayConfigPort, fenced, switchoverSilent)
		require.Error(t, err, "the switchover apply would delete the field the fence apply declared")

		assert.Contains(t, err.Error(), "switchover")
		t.Logf("refused with: %v", err)
	})
}
