package gateway

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGenerationChangedError(t *testing.T) {
	err := &GenerationChangedError{Namespace: "confluent", Gateway: "gw", Expected: 5, Observed: 7}

	assert.Equal(t, "gateway CR confluent/gw changed since kcp last knew it: metadata.generation is 7, kcp expected 5", err.Error())
	assert.Equal(t,
		"The gateway CR changed since kcp last knew it: metadata.generation is 7, kcp expected 5. Stopping with no gateway write — fence applied, not promoted.",
		err.Summary("fence applied, not promoted"))
	assert.True(t, errors.Is(err, ErrGatewayChanged))
	assert.False(t, errors.Is(err, ErrApplyUnverified))
}

func TestInspectCommands(t *testing.T) {
	assert.Equal(t,
		"kubectl -n confluent get gateway gw -o yaml; kubectl -n confluent get pods -l app=gw",
		InspectCommands("confluent", "gw"))
}
