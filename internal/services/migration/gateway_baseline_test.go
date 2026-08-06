package migration

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/confluentinc/kcp/internal/services/gateway"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	k8stypes "k8s.io/apimachinery/pkg/types"
)

// rolloutVerifiedGateway builds a mock for a cluster that cannot report a config
// revision, so every transition is verified by the Deployment rollout — the path
// the generation baseline feeds.
func rolloutVerifiedGateway() *mockGatewayService {
	return &mockGatewayService{
		detectCapabilityFn: func(context.Context, string, string, int) (gateway.Capability, error) {
			return gateway.Capability{Mode: gateway.VerifyRollout}, nil
		},
		applyGatewayYAMLFn: func(_ context.Context, _, _ string, _ []byte, configID string) (string, error) {
			return configID, nil
		},
	}
}

func TestApplyGatewayCR_DeploymentGenerationBaseline(t *testing.T) {
	t.Run("captures the generation before applying", func(t *testing.T) {
		// The ordering is the whole point: a baseline read after the apply could
		// already include the bump the apply caused, making the roll invisible.
		gw := rolloutVerifiedGateway()

		var calls []string
		gw.getDeploymentGenFn = func(context.Context, string, string) (int64, error) {
			calls = append(calls, "read-generation")
			return 11, nil
		}
		gw.applyGatewayYAMLFn = func(_ context.Context, _, _ string, _ []byte, configID string) (string, error) {
			calls = append(calls, "apply")
			return configID, nil
		}

		var gotBaseline int64 = -1
		gw.waitForGatewayReadyFn = func(_ context.Context, _, _ string, baseline int64, _, _ time.Duration, _ func(gateway.GatewayReadinessProgress)) error {
			gotBaseline = baseline
			return nil
		}

		actions := NewMigrationActions(gw, &mockClusterLinkService{})
		config := hotReloadConfig()
		require.NoError(t, actions.ResolveGatewayCapability(context.Background(), config))
		require.NoError(t, actions.FenceGateway(context.Background(), config))

		assert.Equal(t, []string{"read-generation", "apply"}, calls,
			"the baseline must be read before the apply, not after")
		assert.Equal(t, int64(11), gotBaseline, "the readiness wait must receive the pre-apply generation")
	})

	t.Run("an unreadable generation does not fail the transition", func(t *testing.T) {
		// The baseline only feeds the rollout path, and 0 makes that path
		// conservative rather than wrong. Failing here would break a migration
		// over a signal the configId path never even consults.
		gw := rolloutVerifiedGateway()
		gw.getDeploymentGenFn = func(context.Context, string, string) (int64, error) {
			return 0, fmt.Errorf("deployments.apps \"gw-1\" is forbidden")
		}

		var gotBaseline int64 = -1
		gw.waitForGatewayReadyFn = func(_ context.Context, _, _ string, baseline int64, _, _ time.Duration, _ func(gateway.GatewayReadinessProgress)) error {
			gotBaseline = baseline
			return nil
		}

		actions := NewMigrationActions(gw, &mockClusterLinkService{})
		config := hotReloadConfig()
		require.NoError(t, actions.ResolveGatewayCapability(context.Background(), config))
		require.NoError(t, actions.FenceGateway(context.Background(), config))

		assert.Equal(t, int64(0), gotBaseline, "an unreadable baseline becomes 0, which treats any generation as a bump")
	})

	t.Run("switchover carries its own baseline", func(t *testing.T) {
		// Each transition reads its own baseline: after the fence rolled the pods,
		// the fence's baseline is stale and would make the switchover's roll look
		// like it had already happened.
		gw := rolloutVerifiedGateway()

		generations := []int64{20, 21}
		var reads int
		gw.getDeploymentGenFn = func(context.Context, string, string) (int64, error) {
			gen := generations[reads]
			reads++
			return gen, nil
		}

		var baselines []int64
		gw.waitForGatewayReadyFn = func(_ context.Context, _, _ string, baseline int64, _, _ time.Duration, _ func(gateway.GatewayReadinessProgress)) error {
			baselines = append(baselines, baseline)
			return nil
		}

		actions := NewMigrationActions(gw, &mockClusterLinkService{})
		config := hotReloadConfig()
		require.NoError(t, actions.ResolveGatewayCapability(context.Background(), config))
		require.NoError(t, actions.FenceGateway(context.Background(), config))
		require.NoError(t, actions.SwitchGateway(context.Background(), config))

		assert.Equal(t, []int64{20, 21}, baselines, "each transition must use the generation current at its own apply")
	})

	t.Run("the pod-drain wait receives the baseline too", func(t *testing.T) {
		// With unrouted-producer detection on and no configId available, the fence
		// takes the pod-replacement path — which needs the same baseline to know a
		// roll is coming at all.
		gw := rolloutVerifiedGateway()
		gw.getDeploymentGenFn = func(context.Context, string, string) (int64, error) {
			return 30, nil
		}
		gw.getGatewayPodUIDsFn = func(context.Context, string, string) (map[k8stypes.UID]struct{}, error) {
			return map[k8stypes.UID]struct{}{"pod-a": {}}, nil
		}

		var gotBaseline int64 = -1
		gw.waitForGatewayPodsFn = func(_ context.Context, _, _ string, _ map[k8stypes.UID]struct{}, baseline int64, _, _ time.Duration, _ func(gateway.PodRolloutProgress)) error {
			gotBaseline = baseline
			return nil
		}

		actions := NewMigrationActions(gw, &mockClusterLinkService{})
		config := hotReloadConfig()
		config.DetectUnroutedProducersDuration = 5 * time.Second
		require.NoError(t, actions.ResolveGatewayCapability(context.Background(), config))
		require.NoError(t, actions.FenceGateway(context.Background(), config))

		assert.Equal(t, int64(30), gotBaseline)
	})

	t.Run("the configId path does not need the baseline", func(t *testing.T) {
		// A capable cluster verifies per pod, so a Deployment it cannot read must
		// not stop the migration.
		var applied []string
		gw := hotReloadCapableGateway(&applied)
		gw.getDeploymentGenFn = func(context.Context, string, string) (int64, error) {
			return 0, fmt.Errorf("no permission to read deployments")
		}
		gw.waitForGatewayReadyFn = func(_ context.Context, _, _ string, _ int64, _, _ time.Duration, _ func(gateway.GatewayReadinessProgress)) error {
			t.Fatal("must not fall back to the rollout wait when a configId was applied")
			return nil
		}

		actions := NewMigrationActions(gw, &mockClusterLinkService{})
		config := hotReloadConfig()
		require.NoError(t, actions.ResolveGatewayCapability(context.Background(), config))
		require.NoError(t, actions.FenceGateway(context.Background(), config))

		require.Len(t, applied, 1)
		assert.NotEmpty(t, applied[0])
	})

	t.Run("the configId wait receives both budgets and the baseline", func(t *testing.T) {
		// The wait picks its deadline from whichever mechanism it observes, so it
		// needs the roll budget as well as the hot-reload one. Passing only the
		// latter is what would cut a legitimate rollout short after fencing.
		var applied []string
		gw := hotReloadCapableGateway(&applied)
		gw.getDeploymentGenFn = func(context.Context, string, string) (int64, error) {
			return 17, nil
		}

		var got gateway.ConfigWaitOptions
		gw.waitForConfigIDFn = func(_ context.Context, _, _ string, opts gateway.ConfigWaitOptions) error {
			got = opts
			return nil
		}

		actions := NewMigrationActions(gw, &mockClusterLinkService{})
		actions.SetRolloutTimeout(25 * time.Minute)
		actions.SetHotReloadTimeout(45 * time.Second)

		config := hotReloadConfig()
		require.NoError(t, actions.ResolveGatewayCapability(context.Background(), config))
		require.NoError(t, actions.FenceGateway(context.Background(), config))

		assert.Equal(t, 45*time.Second, got.HotReloadTimeout)
		assert.Equal(t, 25*time.Minute, got.RollTimeout, "the user's --rollout-timeout must govern an observed roll")
		assert.Equal(t, int64(17), got.BaselineDeploymentGeneration)
		assert.Equal(t, gateway.DefaultGatewayConfigPort, got.Port)
		assert.NotEmpty(t, got.ConfigID)
	})

	t.Run("rollback carries a baseline", func(t *testing.T) {
		// Rollback is the worst place to be blind to a roll: without a baseline the
		// unfence would report traffic restored off a Deployment that was already
		// healthy at the fenced generation.
		gw := rolloutVerifiedGateway()
		gw.getDeploymentGenFn = func(context.Context, string, string) (int64, error) {
			return 42, nil
		}

		var baselines []int64
		gw.waitForGatewayReadyFn = func(_ context.Context, _, _ string, baseline int64, _, _ time.Duration, _ func(gateway.GatewayReadinessProgress)) error {
			baselines = append(baselines, baseline)
			return nil
		}

		actions := NewMigrationActions(gw, &mockClusterLinkService{})
		config := hotReloadConfig()
		require.NoError(t, actions.ResolveGatewayCapability(context.Background(), config))
		require.NoError(t, actions.unfenceGateway(context.Background(), config))

		require.Len(t, baselines, 1)
		assert.Equal(t, int64(42), baselines[0])
	})
}
