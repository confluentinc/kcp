package gateway

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/types"
)

func TestStrategyForCapability(t *testing.T) {
	tests := []struct {
		name       string
		capability Capability
		want       string
	}{
		{name: "per-pod configId", capability: Capability{Mode: VerifyPerPodConfigID}, want: "config_id"},
		{name: "rollout", capability: Capability{Mode: VerifyRollout}, want: "rollout"},
		{name: "unresolved zero value", capability: Capability{}, want: "rollout"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, strategyForCapability(tt.capability).name())
		})
	}
}

func TestStrategyForApplied(t *testing.T) {
	assert.Equal(t, "config_id", strategyForApplied(ApplyResult{ConfigID: "id-1"}).name())
	assert.Equal(t, "rollout", strategyForApplied(ApplyResult{}).name())
}

func TestVerifyTransition_DispatchesOnTheAppliedConfigIDNotTheCapability(t *testing.T) {
	ctx := context.Background()
	svc := &mockService{}
	v := &TransitionVerifier{Client: svc, Reporter: noopReporter{},
		Capability: Capability{Mode: VerifyPerPodConfigID}}
	applied := ApplyResult{PreApplyPodUIDs: map[types.UID]struct{}{"uid-a": {}}} // no ConfigID
	require.NoError(t, v.VerifyTransition(ctx, "ns", "gw", 9180, applied, "fence"))
	assert.Equal(t, 1, svc.waitPodsCalls, "no configId applied: the replaced-pod wait runs even on a hot-reload capability")
}
