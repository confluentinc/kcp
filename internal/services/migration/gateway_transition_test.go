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

// hotReloadCapableGateway builds a mock whose cluster supports per-pod configId
// verification, recording every applied configId.
func hotReloadCapableGateway(applied *[]string) *mockGatewayService {
	return &mockGatewayService{
		detectCapabilityFn: func(context.Context, string, string) (gateway.Capability, error) {
			return gateway.Capability{
				Mode:                gateway.VerifyPerPodConfigID,
				CRDSupportsConfigID: true,
				HotReloadEnabled:    true,
			}, nil
		},
		applyGatewayYAMLFn: func(_ context.Context, _, _ string, _ []byte, configID string) (string, error) {
			*applied = append(*applied, configID)
			return configID, nil
		},
	}
}

func hotReloadConfig() *MigrationConfig {
	return &MigrationConfig{
		K8sNamespace:     "confluent",
		InitialCrName:    "gw-1",
		InitialCrYAML:    []byte("apiVersion: platform.confluent.io/v1beta1\nkind: Gateway\nmetadata:\n  name: gw-1\n  resourceVersion: \"123\"\nspec:\n  replicas: 1\nstatus:\n  observedGeneration: 4\n"),
		FencedCrYAML:     []byte("apiVersion: platform.confluent.io/v1beta1\nkind: Gateway\nspec:\n  replicas: 1\n"),
		SwitchoverCrYAML: []byte("apiVersion: platform.confluent.io/v1beta1\nkind: Gateway\nspec:\n  replicas: 1\n"),
	}
}

func TestFenceGateway_ConfigIDVerification(t *testing.T) {
	t.Run("injects a configId and verifies it per pod", func(t *testing.T) {
		var applied []string
		gw := hotReloadCapableGateway(&applied)

		var verified string
		gw.waitForConfigIDFn = func(_ context.Context, _, _, configID string, _ int, _, _ time.Duration, _ func(gateway.ConfigWaitProgress)) error {
			verified = configID
			return nil
		}
		gw.waitForGatewayReadyFn = func(_ context.Context, _, _ string, _, _ time.Duration, _ func(gateway.GatewayReadinessProgress)) error {
			t.Fatal("must not fall back to the rollout wait when a configId was applied")
			return nil
		}

		actions := NewMigrationActions(gw, &mockClusterLinkService{})
		config := hotReloadConfig()
		require.NoError(t, actions.ResolveGatewayCapability(context.Background(), config))
		require.NoError(t, actions.FenceGateway(context.Background(), config))

		require.Len(t, applied, 1)
		assert.NotEmpty(t, applied[0], "the fence apply must carry a configId")
		assert.Equal(t, applied[0], verified, "verification must wait for the id that was applied")
	})

	t.Run("uses the rollout wait on a pre-hot-reload cluster", func(t *testing.T) {
		var applied []string
		gw := hotReloadCapableGateway(&applied)
		gw.detectCapabilityFn = func(context.Context, string, string) (gateway.Capability, error) {
			return gateway.Capability{Mode: gateway.VerifyRollout}, nil
		}
		readyCalled := false
		gw.waitForGatewayReadyFn = func(_ context.Context, _, _ string, _, _ time.Duration, _ func(gateway.GatewayReadinessProgress)) error {
			readyCalled = true
			return nil
		}
		gw.waitForConfigIDFn = func(_ context.Context, _, _, _ string, _ int, _, _ time.Duration, _ func(gateway.ConfigWaitProgress)) error {
			t.Fatal("must not verify by configId when the cluster cannot report one")
			return nil
		}

		actions := NewMigrationActions(gw, &mockClusterLinkService{})
		config := hotReloadConfig()
		require.NoError(t, actions.ResolveGatewayCapability(context.Background(), config))
		require.NoError(t, actions.FenceGateway(context.Background(), config))

		require.Len(t, applied, 1)
		assert.Empty(t, applied[0], "a rollout-mode cluster must never be sent a configId")
		assert.True(t, readyCalled)
	})

	t.Run("skips the pod-replacement wait when verifying by configId", func(t *testing.T) {
		// Under a hot-reload the running pods apply the fence in place and are
		// never replaced, so waiting for the pre-fence pods to disappear could
		// never be satisfied. Per-pod verification is the stronger guarantee.
		var applied []string
		gw := hotReloadCapableGateway(&applied)
		gw.waitForGatewayPodsFn = func(_ context.Context, _, _ string, _ map[k8stypes.UID]struct{}, _, _ time.Duration, _ func(gateway.PodRolloutProgress)) error {
			t.Fatal("the pod-replacement wait can never be satisfied by a hot-reload")
			return nil
		}
		gw.getGatewayPodUIDsFn = func(context.Context, string, string) (map[k8stypes.UID]struct{}, error) {
			t.Fatal("no need to snapshot pods when verifying by configId")
			return nil, nil
		}

		actions := NewMigrationActions(gw, &mockClusterLinkService{})
		config := hotReloadConfig()
		config.DetectUnroutedProducersDuration = 30 * time.Second
		require.NoError(t, actions.ResolveGatewayCapability(context.Background(), config))
		require.NoError(t, actions.FenceGateway(context.Background(), config))
	})

	t.Run("still uses the pod-replacement wait when detecting without configId", func(t *testing.T) {
		var applied []string
		gw := hotReloadCapableGateway(&applied)
		gw.detectCapabilityFn = func(context.Context, string, string) (gateway.Capability, error) {
			return gateway.Capability{Mode: gateway.VerifyRollout}, nil
		}
		podsCalled := false
		gw.getGatewayPodUIDsFn = func(context.Context, string, string) (map[k8stypes.UID]struct{}, error) {
			return map[k8stypes.UID]struct{}{"uid-1": {}}, nil
		}
		gw.waitForGatewayPodsFn = func(_ context.Context, _, _ string, _ map[k8stypes.UID]struct{}, _, _ time.Duration, _ func(gateway.PodRolloutProgress)) error {
			podsCalled = true
			return nil
		}

		actions := NewMigrationActions(gw, &mockClusterLinkService{})
		config := hotReloadConfig()
		config.DetectUnroutedProducersDuration = 30 * time.Second
		require.NoError(t, actions.ResolveGatewayCapability(context.Background(), config))
		require.NoError(t, actions.FenceGateway(context.Background(), config))

		assert.True(t, podsCalled, "the pre-existing detection path must be unchanged")
	})

	t.Run("a configId verification failure fails the fence", func(t *testing.T) {
		var applied []string
		gw := hotReloadCapableGateway(&applied)
		gw.waitForConfigIDFn = func(_ context.Context, _, _, _ string, _ int, _, _ time.Duration, _ func(gateway.ConfigWaitProgress)) error {
			return fmt.Errorf("timed out after 90s")
		}

		actions := NewMigrationActions(gw, &mockClusterLinkService{})
		config := hotReloadConfig()
		require.NoError(t, actions.ResolveGatewayCapability(context.Background(), config))

		err := actions.FenceGateway(context.Background(), config)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "timed out")
	})
}

func TestSwitchGateway_ConfigIDVerification(t *testing.T) {
	t.Run("injects and verifies a fresh configId", func(t *testing.T) {
		var applied []string
		gw := hotReloadCapableGateway(&applied)
		var verified string
		gw.waitForConfigIDFn = func(_ context.Context, _, _, configID string, _ int, _, _ time.Duration, _ func(gateway.ConfigWaitProgress)) error {
			verified = configID
			return nil
		}

		actions := NewMigrationActions(gw, &mockClusterLinkService{})
		config := hotReloadConfig()
		require.NoError(t, actions.ResolveGatewayCapability(context.Background(), config))
		require.NoError(t, actions.SwitchGateway(context.Background(), config))

		require.Len(t, applied, 1)
		assert.Equal(t, applied[0], verified)
	})

	t.Run("uses a different configId from the fence", func(t *testing.T) {
		// The contract requires each configId to differ from the last sent;
		// a repeat would make the switchover verify against the fence's revision.
		var applied []string
		gw := hotReloadCapableGateway(&applied)

		actions := NewMigrationActions(gw, &mockClusterLinkService{})
		config := hotReloadConfig()
		require.NoError(t, actions.ResolveGatewayCapability(context.Background(), config))
		require.NoError(t, actions.FenceGateway(context.Background(), config))
		require.NoError(t, actions.SwitchGateway(context.Background(), config))

		require.Len(t, applied, 2)
		assert.NotEqual(t, applied[0], applied[1])
	})
}

func TestVerifyHotReloadCapability(t *testing.T) {
	t.Run("re-applies the live spec with only a new configId", func(t *testing.T) {
		// Safe at any point in a migration, including a resume: the spec applied
		// is the spec already in force, so it cannot fence or unfence anything.
		var applied []string
		var appliedYAML [][]byte
		gw := hotReloadCapableGateway(&applied)
		gw.getGatewayYAMLFn = func(context.Context, string, string) ([]byte, error) {
			return []byte("apiVersion: platform.confluent.io/v1beta1\nkind: Gateway\nmetadata:\n  name: gw-1\n  resourceVersion: \"9\"\n  uid: abc\nspec:\n  fenced: true\nstatus:\n  observedGeneration: 3\n"), nil
		}
		gw.applyGatewayYAMLFn = func(_ context.Context, _, _ string, yamlData []byte, configID string) (string, error) {
			applied = append(applied, configID)
			appliedYAML = append(appliedYAML, yamlData)
			return configID, nil
		}

		actions := NewMigrationActions(gw, &mockClusterLinkService{})
		config := hotReloadConfig()
		require.NoError(t, actions.ResolveGatewayCapability(context.Background(), config))
		require.NoError(t, actions.VerifyHotReloadCapability(context.Background(), config))

		require.Len(t, appliedYAML, 1)
		body := string(appliedYAML[0])
		assert.Contains(t, body, "fenced", "the live spec must be re-applied as-is")
		assert.NotContains(t, body, "resourceVersion", "server metadata must be stripped")
		assert.NotContains(t, body, "uid:")
		assert.NotContains(t, body, "status")
		assert.NotEmpty(t, applied[0])
	})

	t.Run("is a no-op on a cluster that cannot report a configId", func(t *testing.T) {
		gw := &mockGatewayService{
			detectCapabilityFn: func(context.Context, string, string) (gateway.Capability, error) {
				return gateway.Capability{Mode: gateway.VerifyRollout}, nil
			},
			getGatewayYAMLFn: func(context.Context, string, string) ([]byte, error) {
				t.Fatal("must not touch the cluster when hot-reload is not in use")
				return nil, nil
			},
		}

		actions := NewMigrationActions(gw, &mockClusterLinkService{})
		config := hotReloadConfig()
		require.NoError(t, actions.ResolveGatewayCapability(context.Background(), config))
		require.NoError(t, actions.VerifyHotReloadCapability(context.Background(), config))
	})

	t.Run("fails when the revision never reaches the pods", func(t *testing.T) {
		// The licence gate: CFK reports success while the gateway serves stale
		// config. This check is the only place that failure is visible.
		var applied []string
		gw := hotReloadCapableGateway(&applied)
		gw.getGatewayYAMLFn = func(context.Context, string, string) ([]byte, error) {
			return []byte("apiVersion: platform.confluent.io/v1beta1\nkind: Gateway\nspec:\n  replicas: 1\n"), nil
		}
		gw.waitForConfigIDFn = func(_ context.Context, _, _, _ string, _ int, _, _ time.Duration, _ func(gateway.ConfigWaitProgress)) error {
			return fmt.Errorf("timed out after 90s waiting for the gateway to apply the configId")
		}

		actions := NewMigrationActions(gw, &mockClusterLinkService{})
		config := hotReloadConfig()
		require.NoError(t, actions.ResolveGatewayCapability(context.Background(), config))

		err := actions.VerifyHotReloadCapability(context.Background(), config)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "timed out")
	})
}

func TestStripServerMetadata(t *testing.T) {
	t.Run("removes every server-managed field", func(t *testing.T) {
		in := []byte(`
apiVersion: platform.confluent.io/v1beta1
kind: Gateway
metadata:
  name: gw-1
  managedFields:
    - manager: kcp-migration
  resourceVersion: "123"
  uid: 1234-5678
  creationTimestamp: "2026-01-01T00:00:00Z"
  generation: 7
spec:
  replicas: 3
status:
  observedGeneration: 7
`)
		out, err := stripServerMetadata(in)
		require.NoError(t, err)

		body := string(out)
		for _, field := range []string{"managedFields", "resourceVersion", "uid", "creationTimestamp", "generation", "status"} {
			assert.NotContains(t, body, field)
		}
		assert.Contains(t, body, "gw-1", "the identity must survive")
		assert.Contains(t, body, "replicas", "the spec must survive")
	})

	t.Run("tolerates a CR with no metadata block", func(t *testing.T) {
		out, err := stripServerMetadata([]byte("apiVersion: v1\nkind: Gateway\nspec: {}\n"))
		require.NoError(t, err)
		assert.Contains(t, string(out), "Gateway")
	})

	t.Run("rejects unparseable YAML", func(t *testing.T) {
		_, err := stripServerMetadata([]byte("\tnot: [valid"))
		require.Error(t, err)
	})
}
