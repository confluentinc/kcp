package gateway

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func gatewayCR(generation, observed int, conditions string) []byte {
	status := ""
	if observed >= 0 {
		status = fmt.Sprintf("status:\n  observedGeneration: %d\n%s", observed, conditions)
	}
	return []byte(fmt.Sprintf("apiVersion: platform.confluent.io/v1beta1\nkind: Gateway\nmetadata:\n  name: gw\n  generation: %d\n%s",
		generation, status))
}

const applyFailed = `  conditions:
  - type: platform.confluent.io/cluster-ready
    status: "False"
    reason: ApplyFailed
    message: secretRef no-such-secret not found
    lastTransitionTime: "2020-01-01T00:00:00Z"
`

func TestCheckAccepted(t *testing.T) {
	t.Run("observed generation caught up is accepted", func(t *testing.T) {
		assert.NoError(t, CheckAccepted(gatewayCR(3, 3, ""), "gw"))
	})

	t.Run("an old failure condition does not make a caught-up generation fail", func(t *testing.T) {
		assert.NoError(t, CheckAccepted(gatewayCR(3, 3, applyFailed), "gw"))
	})

	t.Run("a lagging generation with a failure is a rejection however old the condition is", func(t *testing.T) {
		err := CheckAccepted(gatewayCR(4, 3, applyFailed), "gw")

		var rejected *GatewayRejectedError
		require.True(t, errors.As(err, &rejected), "got %v", err)
		assert.Equal(t, "ApplyFailed", rejected.Reason)
		assert.Contains(t, err.Error(), "secretRef no-such-secret not found")
	})

	t.Run("a failure left by an earlier generation is not this spec's rejection", func(t *testing.T) {
		stale := applyFailed + "    observedGeneration: 2\n"

		err := CheckAccepted(gatewayCR(4, 3, stale), "gw")

		var rejected *GatewayRejectedError
		assert.False(t, errors.As(err, &rejected))
		assert.ErrorContains(t, err, "has not observed generation 4")
	})

	t.Run("a lagging generation with no failure is not accepted", func(t *testing.T) {
		assert.ErrorContains(t, CheckAccepted(gatewayCR(2, 1, ""), "gw"), "has not observed generation 2")
	})

	t.Run("no status yet is not accepted", func(t *testing.T) {
		assert.ErrorContains(t, CheckAccepted(gatewayCR(1, -1, ""), "gw"), "has not observed generation 1")
	})

	t.Run("unparseable input is an error", func(t *testing.T) {
		assert.Error(t, CheckAccepted([]byte("{not: [yaml"), "gw"))
	})
}
