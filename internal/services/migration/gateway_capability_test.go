package migration

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/confluentinc/kcp/internal/services/gateway"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveGatewayCapability(t *testing.T) {
	t.Run("adopts per-pod configId verification and records it", func(t *testing.T) {
		gw := &mockGatewayService{
			detectCapabilityFn: func(context.Context, string, string, int, []byte, []byte) (gateway.Capability, error) {
				return gateway.Capability{
					Mode:                gateway.VerifyPerPodConfigID,
					CRDSupportsConfigID: true,
					HotReloadEnabled:    true,
				}, nil
			},
		}
		actions := NewMigrationActions(gw, &mockClusterLinkService{})
		config := &MigrationConfig{K8sNamespace: "confluent", InitialCrName: "gw-1"}

		require.NoError(t, actions.ResolveGatewayCapability(context.Background(), config))

		assert.Equal(t, string(gateway.VerifyPerPodConfigID), config.GatewayVerificationMode)
		assert.True(t, config.GatewayHotReloadEnabled)
		assert.True(t, actions.gatewayCapability.InjectsConfigID())
	})

	// Detection is only as good as its inputs. The fenced and switchover CRs are
	// the ones that put spec.hotReload into force, so a detector handed only the
	// live gateway can still pick rollout verification for a migration that will
	// hot-reload — the exact silent false success this path exists to prevent.
	t.Run("hands the detector the CRs the migration will apply", func(t *testing.T) {
		var gotFenced, gotSwitchover []byte
		gw := &mockGatewayService{
			detectCapabilityFn: func(_ context.Context, _, _ string, _ int, fenced, switchover []byte) (gateway.Capability, error) {
				gotFenced, gotSwitchover = fenced, switchover
				return gateway.Capability{Mode: gateway.VerifyRollout}, nil
			},
		}
		actions := NewMigrationActions(gw, &mockClusterLinkService{})
		config := &MigrationConfig{
			K8sNamespace:     "confluent",
			InitialCrName:    "gw-1",
			FencedCrYAML:     []byte("kind: Gateway # fenced"),
			SwitchoverCrYAML: []byte("kind: Gateway # switchover"),
		}

		require.NoError(t, actions.ResolveGatewayCapability(context.Background(), config))

		assert.Equal(t, config.FencedCrYAML, gotFenced)
		assert.Equal(t, config.SwitchoverCrYAML, gotSwitchover)
	})

	t.Run("adopts rollout verification on a pre-hot-reload cluster", func(t *testing.T) {
		gw := &mockGatewayService{
			detectCapabilityFn: func(context.Context, string, string, int, []byte, []byte) (gateway.Capability, error) {
				return gateway.Capability{
					Mode:     gateway.VerifyRollout,
					Advisory: "the installed CFK operator's Gateway CRD does not declare spec.configId",
				}, nil
			},
		}
		actions := NewMigrationActions(gw, &mockClusterLinkService{})
		config := &MigrationConfig{K8sNamespace: "confluent", InitialCrName: "gw-1"}

		require.NoError(t, actions.ResolveGatewayCapability(context.Background(), config))

		assert.Equal(t, string(gateway.VerifyRollout), config.GatewayVerificationMode)
		assert.False(t, actions.gatewayCapability.InjectsConfigID(),
			"a rollout-mode cluster must never be sent a configId")
	})

	t.Run("defaults the config port and preserves an explicit one", func(t *testing.T) {
		gw := &mockGatewayService{}
		actions := NewMigrationActions(gw, &mockClusterLinkService{})

		unset := &MigrationConfig{}
		require.NoError(t, actions.ResolveGatewayCapability(context.Background(), unset))
		assert.Equal(t, gateway.DefaultGatewayConfigPort, unset.GatewayConfigPort)

		explicit := &MigrationConfig{GatewayConfigPort: 19180}
		require.NoError(t, actions.ResolveGatewayCapability(context.Background(), explicit))
		assert.Equal(t, 19180, explicit.GatewayConfigPort)
	})

	t.Run("a live downgrade overrides the mode recorded at init", func(t *testing.T) {
		// The correctness-critical direction: the CRD no longer declares
		// spec.configId, so continuing to inject it would make every apply fail.
		gw := &mockGatewayService{
			detectCapabilityFn: func(context.Context, string, string, int, []byte, []byte) (gateway.Capability, error) {
				return gateway.Capability{Mode: gateway.VerifyRollout, Advisory: "no configId"}, nil
			},
		}
		actions := NewMigrationActions(gw, &mockClusterLinkService{})
		config := &MigrationConfig{GatewayVerificationMode: string(gateway.VerifyPerPodConfigID)}

		require.NoError(t, actions.ResolveGatewayCapability(context.Background(), config))

		assert.Equal(t, string(gateway.VerifyRollout), config.GatewayVerificationMode)
		assert.False(t, actions.gatewayCapability.InjectsConfigID())
	})

	t.Run("a live upgrade overrides the mode recorded at init", func(t *testing.T) {
		// The operator upgraded CFK between init and execute; the live cluster wins.
		gw := &mockGatewayService{
			detectCapabilityFn: func(context.Context, string, string, int, []byte, []byte) (gateway.Capability, error) {
				return gateway.Capability{
					Mode:                gateway.VerifyPerPodConfigID,
					CRDSupportsConfigID: true,
					HotReloadEnabled:    true,
				}, nil
			},
		}
		actions := NewMigrationActions(gw, &mockClusterLinkService{})
		config := &MigrationConfig{GatewayVerificationMode: string(gateway.VerifyRollout)}

		require.NoError(t, actions.ResolveGatewayCapability(context.Background(), config))

		assert.Equal(t, string(gateway.VerifyPerPodConfigID), config.GatewayVerificationMode)
		assert.True(t, actions.gatewayCapability.InjectsConfigID())
	})

	t.Run("a detection failure aborts", func(t *testing.T) {
		gw := &mockGatewayService{
			detectCapabilityFn: func(context.Context, string, string, int, []byte, []byte) (gateway.Capability, error) {
				return gateway.Capability{}, fmt.Errorf("k8s 403 on customresourcedefinitions")
			},
		}
		actions := NewMigrationActions(gw, &mockClusterLinkService{})

		err := actions.ResolveGatewayCapability(context.Background(), &MigrationConfig{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "403")
	})

	t.Run("an unresolved capability defaults to the pre-hot-reload behaviour", func(t *testing.T) {
		// The zero value must be the safe one: if nothing ever resolves a
		// capability, kcp must behave exactly as it did before hot-reload support.
		actions := NewMigrationActions(&mockGatewayService{}, &mockClusterLinkService{})
		assert.False(t, actions.gatewayCapability.InjectsConfigID())
	})
}

func TestGatewayHotReloadTimeout(t *testing.T) {
	t.Run("defaults rather than running unbounded", func(t *testing.T) {
		// Deliberately unlike --rollout-timeout: a hot-reload moves no Kubernetes
		// signal, so an unbounded wait on a gateway whose config watcher never
		// started would hang with nothing to show.
		actions := NewMigrationActions(&mockGatewayService{}, &mockClusterLinkService{})
		assert.Equal(t, gateway.DefaultHotReloadTimeout, actions.gatewayHotReloadTimeout())
	})

	t.Run("honours an explicit value", func(t *testing.T) {
		actions := NewMigrationActions(&mockGatewayService{}, &mockClusterLinkService{})
		actions.SetHotReloadTimeout(30 * time.Second)
		assert.Equal(t, 30*time.Second, actions.gatewayHotReloadTimeout())
	})

	t.Run("treats a negative value as unset", func(t *testing.T) {
		actions := NewMigrationActions(&mockGatewayService{}, &mockClusterLinkService{})
		actions.SetHotReloadTimeout(-1 * time.Second)
		assert.Equal(t, gateway.DefaultHotReloadTimeout, actions.gatewayHotReloadTimeout())
	})
}

func TestGatewayConfigPort(t *testing.T) {
	tests := []struct {
		name string
		port int
		want int
	}{
		{name: "unset falls back to the default", port: 0, want: gateway.DefaultGatewayConfigPort},
		{name: "negative falls back to the default", port: -1, want: gateway.DefaultGatewayConfigPort},
		{name: "explicit value is honoured", port: 19180, want: 19180},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, gatewayConfigPort(&MigrationConfig{GatewayConfigPort: tt.port}))
		})
	}
}
