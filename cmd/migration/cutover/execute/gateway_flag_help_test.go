package execute

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGatewayFlagHelpText pins the exact help text of the flags whose text
// quotes gateway defaults, so building it from the gateway constants cannot
// change what an operator reads.
func TestGatewayFlagHelpText(t *testing.T) {
	want := map[string]string{
		"rollout-timeout":     "Override policies.rolloutTimeout (all migration types): max wait for the operator to report the gateway Ready, and for the replaced gateway pods to be gone, during fence and switchover (e.g. 10m). 0 means no deadline, but a replaced pod stuck more than 60s past its termination deadline still fails the wait.",
		"hot-reload-timeout":  "Override policies.hotReloadTimeout (all migration types): max wait for every gateway pod to report the new config revision when the gateway supports hot-reload. Unlike --rollout-timeout this is never unbounded: a hot-reload moves no Kubernetes signal, so 0 uses the built-in 90s budget rather than waiting forever.",
		"gateway-config-port": "Override policies.gatewayConfigPort (all migration types): port serving the gateway's /config endpoint, polled per pod to confirm a config revision was applied. Unset, uses the manifest's policies.gatewayConfigPort; 0 means the gateway default (9180).",
	}
	cmd := NewMigrationExecuteCmd()
	for name, usage := range want {
		f := cmd.Flags().Lookup(name)
		require.NotNil(t, f, name)
		assert.Equal(t, usage, f.Usage, name)
	}
}
