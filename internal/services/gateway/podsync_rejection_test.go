package gateway

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCurrentFatalCondition(t *testing.T) {
	t0 := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	const msg = "secretRef not found"

	tests := []struct {
		name         string
		opts         []gatewayCROption
		wantRejected bool
	}{
		{
			name:         "condition observedGeneration equals generation",
			opts:         []gatewayCROption{withGatewayConditionAt("ready", "False", "ApplyFailed", msg, 3, t0)},
			wantRejected: true,
		},
		{
			name: "condition observedGeneration older",
			opts: []gatewayCROption{withGatewayConditionAt("ready", "False", "ApplyFailed", msg, 2, t0)},
		},
		{
			// CFK's case: no condition-level generation, and the condition is
			// left behind after a later spec applies, however recent it is.
			name: "transition time only",
			opts: []gatewayCROption{withGatewayConditionAtTime("ready", "False", "ApplyFailed", msg, time.Now())},
		},
		{
			name: "no evidence at all",
			opts: []gatewayCROption{withGatewayCondition("ready", "False", "ApplyFailed", msg)},
		},
		{
			name: "non-fatal reason ignored",
			opts: []gatewayCROption{withGatewayConditionAt("ready", "False", "Reconciling", msg, 3, t0)},
		},
		{
			name: "True condition ignored",
			opts: []gatewayCROption{withGatewayConditionAt("ready", "True", "ApplyFailed", msg, 3, t0)},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cr := newGatewayCR("gw", "ns", 3, 3, true, tt.opts...)
			rej, err := currentFatalCondition(cr, "gw")
			require.NoError(t, err)
			assert.Equal(t, tt.wantRejected, rej != nil)
			if rej != nil {
				assert.Equal(t, "gw", rej.Gateway)
				assert.Equal(t, "ApplyFailed", rej.Reason)
				assert.Equal(t, msg, rej.Message)
				assert.Equal(t, int64(3), rej.Generation)
				assert.Equal(t, int64(3), rej.ObservedGeneration)
			}
		})
	}
}
