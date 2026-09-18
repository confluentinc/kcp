//go:build e2e

// Hot-reload discrepancy between the live Gateway CR and a planned migration CR.
//
// kcp patches only route fields and never writes spec.hotReload, so it cannot
// change the gateway's hot-reload behaviour. A planned fenced/switchover CR whose
// spec.hotReload declaration disagrees with the live gateway is therefore intent
// kcp would silently drop — so DetectCapability refuses it and names the file
// rather than run a migration that ignores what the operator wrote.
//
// The test here exercises that refusal purely through DetectCapability's read-only
// check — it neither applies a CR nor mutates the live gateway. The presence-
// agreement sibling (one file mentions spec.hotReload, the other omits it) was
// removed with the check itself: under targeted JSON patches an omitted field is a
// no-op, so a mismatch in mere presence changes nothing and is no longer refused.
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
// just as much as turning hot-reload on: a planned CR declares hot-reload off while
// the live gateway runs it. kcp cannot honour that — it never writes spec.hotReload
// — so detection must refuse and name the file rather than silently ignore it.
func TestPlannedCRThatWouldDisableHotReloadIsRefused(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)

	require.True(t, e.liveHotReload(t, ctx), "the rig's gateway must be running hot-reload")

	fenced := withHotReload(t, mustReadFile(t, "KCP_HR_FENCED_CR"), false)
	switchover := withHotReload(t, mustReadFile(t, "KCP_HR_SWITCHOVER_CR"), false)

	_, err := e.svc.DetectCapability(ctx, e.namespace, e.gateway,
		gateway.DefaultGatewayConfigPort, fenced, switchover)
	require.Error(t, err, "a planned CR declaring hot-reload off while the gateway runs it must be refused, not ignored")

	assert.Contains(t, err.Error(), "fenced")
	assert.Contains(t, err.Error(), "spec.hotReload.enabled")
	t.Logf("refused with: %v", err)
}
