package migration

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/confluentinc/kcp/internal/services/clusterlink"
	"github.com/confluentinc/kcp/internal/services/gateway"
	"github.com/goccy/go-yaml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	k8stypes "k8s.io/apimachinery/pkg/types"
)

// testInitialCR is a minimal live-read gateway CR with a route named
// migration-route, so FenceGateway's cleanInitialCR + gateway.FenceRoutes can
// derive a fenced CR from it in unit tests.
const testInitialCR = `apiVersion: platform.confluent.io/v1beta1
kind: Gateway
metadata:
  name: gw-1
spec:
  routes:
    - name: migration-route
      endpoint: gateway:9595
`

// testSwitchoverTargets pairs with testInitialCR's one fence route, so
// SwitchGateway's cleanInitialCR + gateway.SwitchRoutesObj can derive a
// switched CR from it in unit tests.
var testSwitchoverTargets = []gateway.RouteSwitchoverTarget{
	{RouteName: "migration-route", StreamingDomainName: "confluent-cloud", BootstrapServerId: "SASL_PLAIN"},
}

// ===========================================================================
// Initialize tests
// ===========================================================================

func TestWorkflow_Initialize_Success(t *testing.T) {
	gw := &mockGatewayService{
		getGatewayYAMLFn: func(_ context.Context, _, _ string) ([]byte, error) {
			return []byte(testInitialCR), nil
		},
		// validateGatewayCRsFn left unset: the mock's default passes validation.
	}

	cl := &mockClusterLinkService{
		listMirrorTopicsFn: func(_ context.Context, _ clusterlink.Config) ([]clusterlink.MirrorTopic, error) {
			return []clusterlink.MirrorTopic{
				{MirrorTopicName: "topic-a", MirrorStatus: "ACTIVE"},
				{MirrorTopicName: "topic-b", MirrorStatus: "ACTIVE"},
				{MirrorTopicName: "topic-c", MirrorStatus: "ACTIVE"},
			}, nil
		},
		listConfigsFn: func(_ context.Context, _ clusterlink.Config) (map[string]string, error) {
			return map[string]string{"bootstrap.servers": "broker:9092"}, nil
		},
	}

	wf := NewMigrationActions(gw, cl)
	config := &MigrationConfig{
		MigrationId:         "test-1",
		K8sNamespace:        "ns",
		InitialCrName:       "my-gw",
		ClusterRestEndpoint: "https://cluster",
		ClusterId:           "lkc-123",
		ClusterLinkName:     "link-1",
		FenceRoutes:         []string{"migration-route"},
		SwitchoverTargets:   testSwitchoverTargets,
	}

	err := wf.Initialize(context.Background(), config, clusterlink.BasicAuth{Username: "key", Password: "secret"})
	require.NoError(t, err)

	assert.Equal(t, testInitialCR, string(config.InitialCrYAML))
	assert.Len(t, config.ClusterLinkTopics, 3)
	assert.Equal(t, "broker:9092", config.ClusterLinkConfigs["bootstrap.servers"])
}

// ===========================================================================
// Gateway CR validation reporting
//
// Initialize used to print "✔ Gateway CRs validated" unconditionally, over a
// validator that did nothing but log "not yet implemented". These tests pin the
// reporter line to what was actually verified: a tick may only claim a check
// that ran.
// ===========================================================================

// initializeWithValidation runs Initialize against a healthy cluster link with
// the given validation outcome, returning what the reporter printed.
func initializeWithValidation(t *testing.T, result gateway.CRValidationResult, validationErr error) (string, error) {
	t.Helper()

	gw := &mockGatewayService{
		getGatewayYAMLFn: func(_ context.Context, _, _ string) ([]byte, error) {
			return []byte(testInitialCR), nil
		},
		checkRedundantAuthStagedFn: func(_ context.Context, _ string, _ []byte, _ []gateway.RouteSwitchoverTarget) (gateway.CRValidationResult, error) {
			return result, validationErr
		},
	}
	cl := &mockClusterLinkService{
		listMirrorTopicsFn: func(_ context.Context, _ clusterlink.Config) ([]clusterlink.MirrorTopic, error) {
			return []clusterlink.MirrorTopic{{MirrorTopicName: "topic-a", MirrorStatus: "ACTIVE"}}, nil
		},
		listConfigsFn: func(_ context.Context, _ clusterlink.Config) (map[string]string, error) {
			return map[string]string{}, nil
		},
	}

	wf := NewMigrationActions(gw, cl)
	var out, errOut bytes.Buffer
	wf.reporter = &reporter{out: &out, err: &errOut}

	err := wf.Initialize(context.Background(), &MigrationConfig{
		K8sNamespace:        "ns",
		InitialCrName:       "my-gw",
		ClusterRestEndpoint: "https://cluster",
		ClusterId:           "lkc-123",
		ClusterLinkName:     "link-1",
		FenceRoutes:         []string{"migration-route"},
		SwitchoverTargets:   testSwitchoverTargets,
	}, clusterlink.BasicAuth{Username: "key", Password: "secret"})

	return out.String() + errOut.String(), err
}

func TestWorkflow_Initialize_ReportsSecretsChecked(t *testing.T) {
	output, err := initializeWithValidation(t, gateway.CRValidationResult{SecretRefsChecked: 4}, nil)

	require.NoError(t, err)
	assert.Contains(t, output, "Gateway CRs validated (4 secret reference(s) present in ns)")
}

func TestWorkflow_Initialize_ReportsSkippedSecretCheck(t *testing.T) {
	// The honesty case: the static checks ran, the live one could not. A check
	// that did not run gets ⚠️ and a Warn in kcp.log — NOT a green tick that an
	// operator scanning output minutes before cutover will read as "verified".
	output, err := initializeWithValidation(t, gateway.CRValidationResult{
		SecretCheckSkipped: "no permission to read secrets in namespace ns",
	}, nil)

	require.NoError(t, err)
	assert.Contains(t, output, "secret references were NOT checked: no permission to read secrets in namespace ns")
	assert.NotContains(t, output, "✔ Gateway CRs validated", "a skipped check must not be reported under a success tick")
}

func TestWorkflow_Initialize_ReportsNoSecretReferences(t *testing.T) {
	output, err := initializeWithValidation(t, gateway.CRValidationResult{}, nil)

	require.NoError(t, err)
	assert.Contains(t, output, "Gateway CRs validated (no secret references)")
}

func TestWorkflow_Initialize_ValidationFailureIsNotReportedAsSuccess(t *testing.T) {
	output, err := initializeWithValidation(t, gateway.CRValidationResult{},
		fmt.Errorf("1 problem(s) found:\n  - the fenced gateway CR contains no fence block"))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "gateway CR validation failed")
	assert.Contains(t, err.Error(), "contains no fence block")
	assert.NotContains(t, output, "Gateway CRs validated")
}

func TestWorkflow_Initialize_SurfacesValidationWarnings(t *testing.T) {
	output, err := initializeWithValidation(t, gateway.CRValidationResult{
		SecretRefsChecked: 1,
		Warnings:          []string{"the switchover gateway CR fences route(s) the migration does not fence (legacy-route)"},
	}, nil)

	require.NoError(t, err)
	assert.Contains(t, output, "fences route(s) the migration does not fence (legacy-route)")
	assert.Contains(t, output, "Gateway CRs validated")
}

func TestWorkflow_Initialize_WarningsSurfaceEvenWhenValidationFails(t *testing.T) {
	// Warnings are context for diagnosing the failure, so they must not be
	// swallowed by the early return.
	output, err := initializeWithValidation(t, gateway.CRValidationResult{
		Warnings: []string{"the fenced gateway CR has a fence block that is not on a route"},
	}, fmt.Errorf("1 problem(s) found:\n  - something else"))

	require.Error(t, err)
	assert.Contains(t, output, "fence block that is not on a route")
}

func TestWorkflow_Initialize_PassesNamespaceAndTargetsToValidator(t *testing.T) {
	// The live secret lookup is namespace-scoped, so the namespace comes from
	// the migration config; the initial CR and switchover targets are what the
	// validator proves the redundant auth against.
	var gotNamespace string
	var gotInitial []byte
	var gotTargets []gateway.RouteSwitchoverTarget

	gw := &mockGatewayService{
		getGatewayYAMLFn: func(_ context.Context, _, _ string) ([]byte, error) {
			return []byte(testInitialCR), nil
		},
		checkRedundantAuthStagedFn: func(_ context.Context, namespace string, initial []byte, targets []gateway.RouteSwitchoverTarget) (gateway.CRValidationResult, error) {
			gotNamespace = namespace
			gotInitial = initial
			gotTargets = targets
			return gateway.CRValidationResult{}, nil
		},
	}
	cl := &mockClusterLinkService{
		listMirrorTopicsFn: func(_ context.Context, _ clusterlink.Config) ([]clusterlink.MirrorTopic, error) {
			return []clusterlink.MirrorTopic{{MirrorTopicName: "topic-a", MirrorStatus: "ACTIVE"}}, nil
		},
		listConfigsFn: func(_ context.Context, _ clusterlink.Config) (map[string]string, error) {
			return map[string]string{}, nil
		},
	}

	wf := NewMigrationActions(gw, cl)
	err := wf.Initialize(context.Background(), &MigrationConfig{
		K8sNamespace:        "kcp",
		InitialCrName:       "migration-gateway",
		ClusterRestEndpoint: "https://cluster",
		ClusterId:           "lkc-123",
		ClusterLinkName:     "link-1",
		FenceRoutes:         []string{"migration-route"},
		SwitchoverTargets:   testSwitchoverTargets,
	}, clusterlink.BasicAuth{Username: "key", Password: "secret"})

	require.NoError(t, err)
	assert.Equal(t, "kcp", gotNamespace)
	assert.Equal(t, testInitialCR, string(gotInitial), "the live CR just fetched, not the stale config field")
	assert.Equal(t, testSwitchoverTargets, gotTargets)
}

func TestWorkflow_Initialize_GatewayFetchError(t *testing.T) {
	gw := &mockGatewayService{
		getGatewayYAMLFn: func(_ context.Context, _, _ string) ([]byte, error) {
			return nil, fmt.Errorf("k8s unreachable")
		},
	}
	cl := &mockClusterLinkService{}

	wf := NewMigrationActions(gw, cl)
	config := &MigrationConfig{
		K8sNamespace:  "ns",
		InitialCrName: "my-gw",
	}

	err := wf.Initialize(context.Background(), config, clusterlink.BasicAuth{Username: "key", Password: "secret"})
	require.Error(t, err)
	assert.Equal(t, "failed to get initial CR YAML: k8s unreachable", err.Error())
}

func TestWorkflow_Initialize_InactiveMirrorTopics(t *testing.T) {
	gw := &mockGatewayService{
		getGatewayYAMLFn: func(_ context.Context, _, _ string) ([]byte, error) {
			return []byte(testInitialCR), nil
		},
	}

	cl := &mockClusterLinkService{
		listMirrorTopicsFn: func(_ context.Context, _ clusterlink.Config) ([]clusterlink.MirrorTopic, error) {
			return []clusterlink.MirrorTopic{
				{MirrorTopicName: "topic-a", MirrorStatus: "ACTIVE"},
				{MirrorTopicName: "topic-b", MirrorStatus: "PAUSED"},
			}, nil
		},
	}

	wf := NewMigrationActions(gw, cl)
	config := &MigrationConfig{
		K8sNamespace:        "ns",
		InitialCrName:       "my-gw",
		ClusterRestEndpoint: "https://cluster",
		ClusterId:           "lkc-123",
		ClusterLinkName:     "link-1",
		FenceRoutes:         []string{"migration-route"},
		SwitchoverTargets:   testSwitchoverTargets,
	}

	err := wf.Initialize(context.Background(), config, clusterlink.BasicAuth{Username: "key", Password: "secret"})
	require.Error(t, err)
	assert.Equal(t, "1 mirror topics are not active: topic-b (status: PAUSED)", err.Error())
}

func TestWorkflow_Initialize_TopicValidationError(t *testing.T) {
	gw := &mockGatewayService{
		getGatewayYAMLFn: func(_ context.Context, _, _ string) ([]byte, error) {
			return []byte(testInitialCR), nil
		},
	}

	cl := &mockClusterLinkService{
		listMirrorTopicsFn: func(_ context.Context, _ clusterlink.Config) ([]clusterlink.MirrorTopic, error) {
			return []clusterlink.MirrorTopic{
				{MirrorTopicName: "topic-a", MirrorStatus: "ACTIVE"},
			}, nil
		},
		validateTopicsFn: func(topics []string, _ []string) error {
			return fmt.Errorf("topic topic-x not found in cluster link")
		},
	}

	wf := NewMigrationActions(gw, cl)
	config := &MigrationConfig{
		K8sNamespace:        "ns",
		InitialCrName:       "my-gw",
		ClusterRestEndpoint: "https://cluster",
		ClusterId:           "lkc-123",
		ClusterLinkName:     "link-1",
		Topics:              []string{"topic-x"},
		FenceRoutes:         []string{"migration-route"},
		SwitchoverTargets:   testSwitchoverTargets,
	}

	err := wf.Initialize(context.Background(), config, clusterlink.BasicAuth{Username: "key", Password: "secret"})
	require.Error(t, err)
	assert.Equal(t, "failed to validate topics in cluster link: topic topic-x not found in cluster link", err.Error())
}

func TestWorkflow_Initialize_NoTopicsDiscoverAll(t *testing.T) {
	gw := &mockGatewayService{
		getGatewayYAMLFn: func(_ context.Context, _, _ string) ([]byte, error) {
			return []byte(testInitialCR), nil
		},
	}

	cl := &mockClusterLinkService{
		listMirrorTopicsFn: func(_ context.Context, _ clusterlink.Config) ([]clusterlink.MirrorTopic, error) {
			return []clusterlink.MirrorTopic{
				{MirrorTopicName: "orders", MirrorStatus: "ACTIVE"},
				{MirrorTopicName: "payments", MirrorStatus: "ACTIVE"},
				{MirrorTopicName: "users", MirrorStatus: "ACTIVE"},
			}, nil
		},
		listConfigsFn: func(_ context.Context, _ clusterlink.Config) (map[string]string, error) {
			return map[string]string{}, nil
		},
	}

	wf := NewMigrationActions(gw, cl)
	config := &MigrationConfig{
		K8sNamespace:        "ns",
		InitialCrName:       "my-gw",
		ClusterRestEndpoint: "https://cluster",
		ClusterId:           "lkc-123",
		ClusterLinkName:     "link-1",
		Topics:              nil, // empty — should discover all
		FenceRoutes:         []string{"migration-route"},
		SwitchoverTargets:   testSwitchoverTargets,
	}

	err := wf.Initialize(context.Background(), config, clusterlink.BasicAuth{Username: "key", Password: "secret"})
	require.NoError(t, err)

	require.Len(t, config.Topics, 3)

	expected := map[string]bool{"orders": true, "payments": true, "users": true}
	for _, topic := range config.Topics {
		assert.True(t, expected[topic], "unexpected topic %q in discovered topics", topic)
	}
}

// ===========================================================================
// PauseConsumerOffsetSync precondition tests (U2)
// ===========================================================================

// makeOffsetSyncWorkflow builds a workflow with mocks that satisfy Initialize
// up to the cluster-link config check. listConfigsFn is the seam under test.
func makeOffsetSyncWorkflow(t *testing.T, listConfigsFn func(_ context.Context, _ clusterlink.Config) (map[string]string, error)) *MigrationActions {
	t.Helper()
	gw := &mockGatewayService{
		getGatewayYAMLFn: func(_ context.Context, _, _ string) ([]byte, error) {
			return []byte(testInitialCR), nil
		},
	}
	cl := &mockClusterLinkService{
		listMirrorTopicsFn: func(_ context.Context, _ clusterlink.Config) ([]clusterlink.MirrorTopic, error) {
			return []clusterlink.MirrorTopic{{MirrorTopicName: "topic-a", MirrorStatus: "ACTIVE"}}, nil
		},
		listConfigsFn: listConfigsFn,
	}
	return NewMigrationActions(gw, cl)
}

func TestWorkflow_Initialize_PauseOffsetSync_Pass(t *testing.T) {
	wf := makeOffsetSyncWorkflow(t, func(_ context.Context, _ clusterlink.Config) (map[string]string, error) {
		return map[string]string{"consumer.offset.sync.enable": "true"}, nil
	})
	config := &MigrationConfig{
		ClusterLinkName:         "link-pause",
		PauseConsumerOffsetSync: true,
		FenceRoutes:             []string{"migration-route"},
		SwitchoverTargets:       testSwitchoverTargets,
	}

	err := wf.Initialize(context.Background(), config, clusterlink.BasicAuth{Username: "key", Password: "secret"})
	require.NoError(t, err)
	assert.True(t, config.PauseConsumerOffsetSync, "intent should be retained on config")
	assert.False(t, config.PauseConsumerOffsetSyncFlipped, "flipped marker must remain false at init time")
}

func TestWorkflow_Initialize_PauseOffsetSync_RefusesOnFalse(t *testing.T) {
	wf := makeOffsetSyncWorkflow(t, func(_ context.Context, _ clusterlink.Config) (map[string]string, error) {
		return map[string]string{"consumer.offset.sync.enable": "false"}, nil
	})
	config := &MigrationConfig{
		ClusterLinkName:         "link-falsey",
		PauseConsumerOffsetSync: true,
		FenceRoutes:             []string{"migration-route"},
		SwitchoverTargets:       testSwitchoverTargets,
	}

	err := wf.Initialize(context.Background(), config, clusterlink.BasicAuth{Username: "key", Password: "secret"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "link-falsey")
	assert.Contains(t, err.Error(), "consumer.offset.sync.enable")
	assert.Contains(t, err.Error(), `"false"`)
}

func TestWorkflow_Initialize_PauseOffsetSync_RefusesOnAbsentKey(t *testing.T) {
	wf := makeOffsetSyncWorkflow(t, func(_ context.Context, _ clusterlink.Config) (map[string]string, error) {
		return map[string]string{"other.key": "value"}, nil
	})
	config := &MigrationConfig{
		ClusterLinkName:         "link-absent",
		PauseConsumerOffsetSync: true,
		FenceRoutes:             []string{"migration-route"},
		SwitchoverTargets:       testSwitchoverTargets,
	}

	err := wf.Initialize(context.Background(), config, clusterlink.BasicAuth{Username: "key", Password: "secret"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "link-absent")
	assert.Contains(t, err.Error(), "no consumer.offset.sync.enable config key", "error must distinguish absent key from false value")
}

func TestWorkflow_Initialize_PauseOffsetSync_FlagOff_IgnoresConfigValue(t *testing.T) {
	// Cluster link reports enable=false. Without the flag, init must succeed
	// regardless — the precondition only applies when the operator opted in.
	wf := makeOffsetSyncWorkflow(t, func(_ context.Context, _ clusterlink.Config) (map[string]string, error) {
		return map[string]string{"consumer.offset.sync.enable": "false"}, nil
	})
	config := &MigrationConfig{
		ClusterLinkName:         "link-offset-disabled",
		PauseConsumerOffsetSync: false,
		FenceRoutes:             []string{"migration-route"},
		SwitchoverTargets:       testSwitchoverTargets,
	}

	err := wf.Initialize(context.Background(), config, clusterlink.BasicAuth{Username: "key", Password: "secret"})
	require.NoError(t, err, "flag off must not assert offset-sync state")
}

// TestWorkflow_Initialize_PauseOffsetSync_AlreadyFlipped_SkipsPrecondition
// covers the --skip-validate + --pause-consumer-offset-sync flow where:
//  1. init runs with --skip-validate so the precondition was NOT checked at init time
//  2. first execute calls DisableOffsetSync which sets enable=false and marker=true
//  3. FSM transitions out of StateUninitialized, calling Initialize
//
// At step 3 the live config is "false" (kcp just set it) and the marker is
// true, meaning kcp is the reason the value drifted. Initialize must NOT
// refuse — that would wedge the migration mid-flight.
func TestWorkflow_Initialize_PauseOffsetSync_AlreadyFlipped_SkipsPrecondition(t *testing.T) {
	wf := makeOffsetSyncWorkflow(t, func(_ context.Context, _ clusterlink.Config) (map[string]string, error) {
		return map[string]string{"consumer.offset.sync.enable": "false"}, nil
	})
	config := &MigrationConfig{
		ClusterLinkName:                "link-mid-flight",
		PauseConsumerOffsetSync:        true,
		PauseConsumerOffsetSyncFlipped: true,
		FenceRoutes:                    []string{"migration-route"},
		SwitchoverTargets:              testSwitchoverTargets,
	}

	err := wf.Initialize(context.Background(), config, clusterlink.BasicAuth{Username: "key", Password: "secret"})
	require.NoError(t, err, "Initialize must not refuse when kcp already flipped the config (Flipped=true)")
}

// TestWorkflow_Initialize_PauseOffsetSync_AlreadyFlipped_PreservesSnapshot
// pins the defensive guard against the snapshot-clobbering bug. If Initialize
// is ever called after DisableOffsetSync has run (i.e. Flipped=true), the live
// configs reflect the post-disable state. Writing them to ClusterLinkConfigs
// would clobber the pre-disable snapshot that RestoreOffsetSync needs to diff
// against. The guard must keep the existing snapshot in that case.
//
// Today the CLI blocks the ordering hazard via mutual exclusion of
// --skip-validate and --pause-consumer-offset-sync, but this test pins the
// in-code defense in case a future caller reintroduces the ordering.
func TestWorkflow_Initialize_PauseOffsetSync_AlreadyFlipped_PreservesSnapshot(t *testing.T) {
	wf := makeOffsetSyncWorkflow(t, func(_ context.Context, _ clusterlink.Config) (map[string]string, error) {
		// Post-disable live state — toggle false, filters cleared.
		return map[string]string{"consumer.offset.sync.enable": "false"}, nil
	})
	preDisableSnapshot := map[string]string{
		"consumer.offset.sync.enable":   "true",
		"consumer.offset.group.filters": `{"groups":["app-*"]}`,
	}
	config := &MigrationConfig{
		ClusterLinkName:                "link-mid-flight",
		PauseConsumerOffsetSync:        true,
		PauseConsumerOffsetSyncFlipped: true,
		ClusterLinkConfigs:             preDisableSnapshot,
		FenceRoutes:                    []string{"migration-route"},
		SwitchoverTargets:              testSwitchoverTargets,
	}

	err := wf.Initialize(context.Background(), config, clusterlink.BasicAuth{Username: "key", Password: "secret"})
	require.NoError(t, err)

	assert.Equal(t, "true", config.ClusterLinkConfigs["consumer.offset.sync.enable"],
		"pre-disable toggle value must survive Initialize when Flipped=true")
	assert.Equal(t, `{"groups":["app-*"]}`, config.ClusterLinkConfigs["consumer.offset.group.filters"],
		"pre-disable filters value must survive Initialize when Flipped=true")
}

// ===========================================================================
// CheckLags tests
// ===========================================================================

func TestWorkflow_CheckLags_ImmediatelyBelowThreshold(t *testing.T) {
	gw := &mockGatewayService{}
	cl := &mockClusterLinkService{}

	sourceOffset := &mockOffsetProvider{
		getFn: func(topic string) (map[int32]int64, error) {
			return map[int32]int64{0: 1000}, nil
		},
	}
	destOffset := &mockOffsetProvider{
		getFn: func(topic string) (map[int32]int64, error) {
			return map[int32]int64{0: 999}, nil
		},
	}

	wf := NewMigrationActionsWithOffsets(gw, cl, sourceOffset, destOffset)
	config := &MigrationConfig{
		Topics: []string{"topic-1", "topic-2"},
	}

	err := wf.CheckLags(context.Background(), config, 10, clusterlink.BasicAuth{Username: "key", Password: "secret"})
	require.NoError(t, err)
}

func TestWorkflow_CheckLags_NoTopics(t *testing.T) {
	gw := &mockGatewayService{}
	cl := &mockClusterLinkService{}

	sourceOffset := &mockOffsetProvider{
		getFn: func(topic string) (map[int32]int64, error) {
			return map[int32]int64{}, nil
		},
	}
	destOffset := &mockOffsetProvider{
		getFn: func(topic string) (map[int32]int64, error) {
			return map[int32]int64{}, nil
		},
	}

	wf := NewMigrationActionsWithOffsets(gw, cl, sourceOffset, destOffset)
	config := &MigrationConfig{
		Topics: []string{},
	}

	err := wf.CheckLags(context.Background(), config, 10, clusterlink.BasicAuth{Username: "key", Password: "secret"})
	require.NoError(t, err)
}

func TestWorkflow_CheckLags_NilOffsetServices(t *testing.T) {
	gw := &mockGatewayService{}
	cl := &mockClusterLinkService{}

	wf := NewMigrationActions(gw, cl)
	config := &MigrationConfig{
		Topics: []string{"topic-1"},
	}

	err := wf.CheckLags(context.Background(), config, 10, clusterlink.BasicAuth{Username: "key", Password: "secret"})
	require.Error(t, err)
	assert.Equal(t, "source and destination offset services are required", err.Error())
}

func TestWorkflow_CheckLags_ContextCancelled(t *testing.T) {
	gw := &mockGatewayService{}
	cl := &mockClusterLinkService{}

	// Return high lag so the loop does not exit early on threshold
	sourceOffset := &mockOffsetProvider{
		getFn: func(topic string) (map[int32]int64, error) {
			return map[int32]int64{0: 10000}, nil
		},
	}
	destOffset := &mockOffsetProvider{
		getFn: func(topic string) (map[int32]int64, error) {
			return map[int32]int64{0: 0}, nil
		},
	}

	wf := NewMigrationActionsWithOffsets(gw, cl, sourceOffset, destOffset)
	config := &MigrationConfig{
		Topics: []string{"topic-1"},
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel

	err := wf.CheckLags(ctx, config, 10, clusterlink.BasicAuth{Username: "key", Password: "secret"})
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
}

func TestWorkflow_CheckLags_DestinationAhead(t *testing.T) {
	gw := &mockGatewayService{}
	cl := &mockClusterLinkService{}

	sourceOffset := &mockOffsetProvider{
		getFn: func(topic string) (map[int32]int64, error) {
			return map[int32]int64{0: 100}, nil
		},
	}
	destOffset := &mockOffsetProvider{
		getFn: func(topic string) (map[int32]int64, error) {
			return map[int32]int64{0: 200}, nil // ahead of source
		},
	}

	wf := NewMigrationActionsWithOffsets(gw, cl, sourceOffset, destOffset)
	config := &MigrationConfig{
		Topics: []string{"topic-1"},
	}

	err := wf.CheckLags(context.Background(), config, 10, clusterlink.BasicAuth{Username: "key", Password: "secret"})
	require.NoError(t, err, "negative lag (destination ahead) should be treated as 0 and pass threshold")
}

// ===========================================================================
// PromoteTopics tests
// ===========================================================================

func TestWorkflow_PromoteTopics_AllAtZeroLag(t *testing.T) {
	gw := &mockGatewayService{}

	promoted := make(map[string]bool)
	cl := &mockClusterLinkService{
		promoteMirrorTopicsFn: func(_ context.Context, _ clusterlink.Config, topicNames []string) (*clusterlink.PromoteMirrorTopicsResponse, error) {
			resp := &clusterlink.PromoteMirrorTopicsResponse{}
			for _, name := range topicNames {
				promoted[name] = true
				resp.Data = append(resp.Data, struct {
					MirrorTopicName string `json:"mirror_topic_name"`
					ErrorMessage    string `json:"error_message,omitempty"`
					ErrorCode       int    `json:"error_code,omitempty"`
				}{
					MirrorTopicName: name,
					ErrorCode:       0,
				})
			}
			return resp, nil
		},
		// After promotion is accepted, the backend reports STOPPED.
		listMirrorTopicsFn: func(_ context.Context, _ clusterlink.Config) ([]clusterlink.MirrorTopic, error) {
			return []clusterlink.MirrorTopic{
				{MirrorTopicName: "topic-1", MirrorStatus: clusterlink.MirrorStatusStopped},
				{MirrorTopicName: "topic-2", MirrorStatus: clusterlink.MirrorStatusStopped},
			}, nil
		},
	}

	// Both source and dest return identical offsets (zero lag)
	offsetProvider := &mockOffsetProvider{
		getFn: func(topic string) (map[int32]int64, error) {
			return map[int32]int64{0: 500, 1: 600}, nil
		},
	}

	wf := NewMigrationActionsWithOffsets(gw, cl, offsetProvider, offsetProvider)
	wf.promotePollInterval = time.Millisecond
	config := &MigrationConfig{
		Topics:              []string{"topic-1", "topic-2"},
		ClusterRestEndpoint: "https://cluster",
		ClusterId:           "lkc-123",
		ClusterLinkName:     "link-1",
	}

	err := wf.PromoteTopics(context.Background(), config, clusterlink.BasicAuth{Username: "key", Password: "secret"})
	require.NoError(t, err)
	assert.True(t, promoted["topic-1"], "topic-1 should have been promoted")
	assert.True(t, promoted["topic-2"], "topic-2 should have been promoted")
}

func TestWorkflow_PromoteTopics_PartialPromotionError(t *testing.T) {
	gw := &mockGatewayService{}

	var callCount int64

	cl := &mockClusterLinkService{
		promoteMirrorTopicsFn: func(_ context.Context, _ clusterlink.Config, topicNames []string) (*clusterlink.PromoteMirrorTopicsResponse, error) {
			n := atomic.AddInt64(&callCount, 1)
			resp := &clusterlink.PromoteMirrorTopicsResponse{}
			for _, name := range topicNames {
				entry := struct {
					MirrorTopicName string `json:"mirror_topic_name"`
					ErrorMessage    string `json:"error_message,omitempty"`
					ErrorCode       int    `json:"error_code,omitempty"`
				}{
					MirrorTopicName: name,
				}
				// First call: topic-2 fails
				if n == 1 && name == "topic-2" {
					entry.ErrorCode = 1
					entry.ErrorMessage = "temporary error"
				}
				resp.Data = append(resp.Data, entry)
			}
			return resp, nil
		},
		// Once accepted, both topics report STOPPED.
		listMirrorTopicsFn: func(_ context.Context, _ clusterlink.Config) ([]clusterlink.MirrorTopic, error) {
			return []clusterlink.MirrorTopic{
				{MirrorTopicName: "topic-1", MirrorStatus: clusterlink.MirrorStatusStopped},
				{MirrorTopicName: "topic-2", MirrorStatus: clusterlink.MirrorStatusStopped},
			}, nil
		},
	}

	offsetProvider := &mockOffsetProvider{
		getFn: func(topic string) (map[int32]int64, error) {
			return map[int32]int64{0: 100}, nil
		},
	}

	wf := NewMigrationActionsWithOffsets(gw, cl, offsetProvider, offsetProvider)
	wf.promotePollInterval = time.Millisecond
	config := &MigrationConfig{
		Topics:              []string{"topic-1", "topic-2"},
		ClusterRestEndpoint: "https://cluster",
		ClusterId:           "lkc-123",
		ClusterLinkName:     "link-1",
	}

	err := wf.PromoteTopics(context.Background(), config, clusterlink.BasicAuth{Username: "key", Password: "secret"})
	require.NoError(t, err, "retry should succeed")

	finalCallCount := atomic.LoadInt64(&callCount)
	assert.GreaterOrEqual(t, finalCallCount, int64(2), "expected at least 2 promote calls (initial + retry)")
}

// TestWorkflow_PromoteTopics_StuckPendingStoppedDoesNotSucceed reproduces the
// customer-reported false-positive: promote returns error_code 0 (accepted),
// but the mirror topic never leaves PENDING_STOPPED. PromoteTopics must NOT
// report success on the enqueue acknowledgement alone — it must wait for the
// terminal STOPPED status, so a topic stuck in PENDING_STOPPED keeps it polling
// until the caller cancels.
func TestWorkflow_PromoteTopics_StuckPendingStoppedDoesNotSucceed(t *testing.T) {
	gw := &mockGatewayService{}

	var promoteCalls int64
	cl := &mockClusterLinkService{
		promoteMirrorTopicsFn: func(_ context.Context, _ clusterlink.Config, topicNames []string) (*clusterlink.PromoteMirrorTopicsResponse, error) {
			atomic.AddInt64(&promoteCalls, 1)
			resp := &clusterlink.PromoteMirrorTopicsResponse{}
			for _, name := range topicNames {
				resp.Data = append(resp.Data, struct {
					MirrorTopicName string `json:"mirror_topic_name"`
					ErrorMessage    string `json:"error_message,omitempty"`
					ErrorCode       int    `json:"error_code,omitempty"`
				}{MirrorTopicName: name, ErrorCode: 0})
			}
			return resp, nil
		},
		// Backend never finishes the async promotion: always PENDING_STOPPED.
		listMirrorTopicsFn: func(_ context.Context, _ clusterlink.Config) ([]clusterlink.MirrorTopic, error) {
			return []clusterlink.MirrorTopic{
				{MirrorTopicName: "topic-1", MirrorStatus: "PENDING_STOPPED"},
			}, nil
		},
	}

	offsetProvider := &mockOffsetProvider{
		getFn: func(topic string) (map[int32]int64, error) {
			return map[int32]int64{0: 100}, nil
		},
	}

	wf := NewMigrationActionsWithOffsets(gw, cl, offsetProvider, offsetProvider)
	wf.promotePollInterval = time.Millisecond
	config := &MigrationConfig{
		Topics:              []string{"topic-1"},
		ClusterRestEndpoint: "https://cluster",
		ClusterId:           "lkc-123",
		ClusterLinkName:     "link-1",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := wf.PromoteTopics(ctx, config, clusterlink.BasicAuth{Username: "key", Password: "secret"})
	require.Error(t, err, "must not report success while topic is stuck in PENDING_STOPPED")
	assert.ErrorIs(t, err, context.DeadlineExceeded)
}

// TestWorkflow_PromoteTopics_WaitsForStoppedStatus verifies the happy path of
// the async promotion: the topic is PENDING_STOPPED immediately after promote
// and only later transitions to STOPPED. PromoteTopics must poll
// ListMirrorTopics and only return once the terminal STOPPED status is observed.
func TestWorkflow_PromoteTopics_WaitsForStoppedStatus(t *testing.T) {
	gw := &mockGatewayService{}

	var listCalls int64
	cl := &mockClusterLinkService{
		promoteMirrorTopicsFn: func(_ context.Context, _ clusterlink.Config, topicNames []string) (*clusterlink.PromoteMirrorTopicsResponse, error) {
			resp := &clusterlink.PromoteMirrorTopicsResponse{}
			for _, name := range topicNames {
				resp.Data = append(resp.Data, struct {
					MirrorTopicName string `json:"mirror_topic_name"`
					ErrorMessage    string `json:"error_message,omitempty"`
					ErrorCode       int    `json:"error_code,omitempty"`
				}{MirrorTopicName: name, ErrorCode: 0})
			}
			return resp, nil
		},
		// PENDING_STOPPED for the first two polls, then STOPPED.
		listMirrorTopicsFn: func(_ context.Context, _ clusterlink.Config) ([]clusterlink.MirrorTopic, error) {
			n := atomic.AddInt64(&listCalls, 1)
			status := "PENDING_STOPPED"
			if n >= 3 {
				status = "STOPPED"
			}
			return []clusterlink.MirrorTopic{
				{MirrorTopicName: "topic-1", MirrorStatus: status},
			}, nil
		},
	}

	offsetProvider := &mockOffsetProvider{
		getFn: func(topic string) (map[int32]int64, error) {
			return map[int32]int64{0: 100}, nil
		},
	}

	wf := NewMigrationActionsWithOffsets(gw, cl, offsetProvider, offsetProvider)
	wf.promotePollInterval = time.Millisecond
	config := &MigrationConfig{
		Topics:              []string{"topic-1"},
		ClusterRestEndpoint: "https://cluster",
		ClusterId:           "lkc-123",
		ClusterLinkName:     "link-1",
	}

	err := wf.PromoteTopics(context.Background(), config, clusterlink.BasicAuth{Username: "key", Password: "secret"})
	require.NoError(t, err)
	assert.GreaterOrEqual(t, atomic.LoadInt64(&listCalls), int64(3),
		"expected PromoteTopics to poll mirror status until STOPPED was observed")
}

// TestWorkflow_PromoteTopics_BatchSizeProcessesSequentially verifies that when
// promoteBatchSize is set, PromoteTopics (1) never submits more than the cap in
// a single promote call, and (2) does not start the next batch until every
// topic in the current batch has reached STOPPED — i.e. synchronous batches.
func TestWorkflow_PromoteTopics_BatchSizeProcessesSequentially(t *testing.T) {
	gw := &mockGatewayService{}

	const batchSize = 10
	topics := make([]string, 25)
	for i := range topics {
		topics[i] = fmt.Sprintf("topic-%02d", i)
	}

	var mu sync.Mutex
	var promoteCallSizes []int
	inFlight := make(map[string]bool)  // promoted but not yet confirmed STOPPED
	pollsSince := make(map[string]int) // polls observed since a topic was promoted

	cl := &mockClusterLinkService{
		promoteMirrorTopicsFn: func(_ context.Context, _ clusterlink.Config, topicNames []string) (*clusterlink.PromoteMirrorTopicsResponse, error) {
			mu.Lock()
			// A new batch must not begin while the previous batch is still
			// draining to STOPPED.
			if len(inFlight) != 0 {
				t.Errorf("promoted a new batch of %d while %d topics still in flight", len(topicNames), len(inFlight))
			}
			promoteCallSizes = append(promoteCallSizes, len(topicNames))
			resp := &clusterlink.PromoteMirrorTopicsResponse{}
			for _, name := range topicNames {
				inFlight[name] = true
				pollsSince[name] = 0
				resp.Data = append(resp.Data, struct {
					MirrorTopicName string `json:"mirror_topic_name"`
					ErrorMessage    string `json:"error_message,omitempty"`
					ErrorCode       int    `json:"error_code,omitempty"`
				}{MirrorTopicName: name, ErrorCode: 0})
			}
			mu.Unlock()
			return resp, nil
		},
		// Each promoted topic reports PENDING_STOPPED on its first poll and
		// STOPPED thereafter, so the workflow must poll at least twice per batch.
		listMirrorTopicsFn: func(_ context.Context, _ clusterlink.Config) ([]clusterlink.MirrorTopic, error) {
			mu.Lock()
			defer mu.Unlock()
			out := make([]clusterlink.MirrorTopic, len(topics))
			for i, name := range topics {
				status := clusterlink.MirrorStatusActive
				if _, promoted := pollsSince[name]; promoted {
					pollsSince[name]++
					if pollsSince[name] >= 2 {
						status = clusterlink.MirrorStatusStopped
						delete(inFlight, name)
					} else {
						// Transient wire value the backend reports before STOPPED;
						// the workflow treats any non-STOPPED status as "not done".
						status = "PENDING_STOPPED"
					}
				}
				out[i] = clusterlink.MirrorTopic{MirrorTopicName: name, MirrorStatus: status}
			}
			return out, nil
		},
	}

	offsetProvider := &mockOffsetProvider{
		getFn: func(topic string) (map[int32]int64, error) {
			return map[int32]int64{0: 100}, nil
		},
	}

	wf := NewMigrationActionsWithOffsets(gw, cl, offsetProvider, offsetProvider)
	wf.promotePollInterval = time.Millisecond
	wf.promoteBatchSize = batchSize
	config := &MigrationConfig{
		Topics:              topics,
		ClusterRestEndpoint: "https://cluster",
		ClusterId:           "lkc-123",
		ClusterLinkName:     "link-1",
	}

	err := wf.PromoteTopics(context.Background(), config, clusterlink.BasicAuth{Username: "key", Password: "secret"})
	require.NoError(t, err)

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, []int{10, 10, 5}, promoteCallSizes,
		"expected 25 topics promoted in sequential batches of at most 10")
}

func TestWorkflow_PromoteTopics_MaxRetriesExceeded(t *testing.T) {
	gw := &mockGatewayService{}

	cl := &mockClusterLinkService{
		promoteMirrorTopicsFn: func(_ context.Context, _ clusterlink.Config, topicNames []string) (*clusterlink.PromoteMirrorTopicsResponse, error) {
			resp := &clusterlink.PromoteMirrorTopicsResponse{}
			for _, name := range topicNames {
				resp.Data = append(resp.Data, struct {
					MirrorTopicName string `json:"mirror_topic_name"`
					ErrorMessage    string `json:"error_message,omitempty"`
					ErrorCode       int    `json:"error_code,omitempty"`
				}{
					MirrorTopicName: name,
					ErrorCode:       1,
					ErrorMessage:    "persistent error",
				})
			}
			return resp, nil
		},
	}

	offsetProvider := &mockOffsetProvider{
		getFn: func(topic string) (map[int32]int64, error) {
			return map[int32]int64{0: 200}, nil
		},
	}

	wf := NewMigrationActionsWithOffsets(gw, cl, offsetProvider, offsetProvider)
	wf.promotePollInterval = time.Millisecond
	config := &MigrationConfig{
		Topics:              []string{"topic-1"},
		ClusterRestEndpoint: "https://cluster",
		ClusterId:           "lkc-123",
		ClusterLinkName:     "link-1",
	}

	err := wf.PromoteTopics(context.Background(), config, clusterlink.BasicAuth{Username: "key", Password: "secret"})
	require.Error(t, err)
	assert.Equal(t, "topic topic-1 failed promotion after 3 attempts: persistent error", err.Error())
}

func TestWorkflow_PromoteTopics_NilOffsetServices(t *testing.T) {
	gw := &mockGatewayService{}
	cl := &mockClusterLinkService{}

	wf := NewMigrationActions(gw, cl)
	config := &MigrationConfig{
		Topics: []string{"topic-1"},
	}

	err := wf.PromoteTopics(context.Background(), config, clusterlink.BasicAuth{Username: "key", Password: "secret"})
	require.Error(t, err)
	assert.Equal(t, "source and destination offset services are required", err.Error())
}

// ===========================================================================
// FenceGateway / SwitchGateway tests
// ===========================================================================

// TestWorkflow_FenceGateway_AppliesFenceInjectedIntoInitialCR is the behavioural
// heart of the inline-fence change: FenceGateway no longer applies a
// snapshotted fenced CR file — it derives the fenced CR at cutover from the
// metadata-stripped initial CR by injecting a fence block onto the routes named
// in config.FenceRoutes. The applied bytes must carry that fence.
func TestWorkflow_FenceGateway_AppliesFenceInjectedIntoInitialCR(t *testing.T) {
	var applied []byte
	gw := &mockGatewayService{
		applyGatewayYAMLFn: func(_ context.Context, _, _ string, yaml []byte, configID string) (string, error) {
			applied = yaml
			return configID, nil
		},
		waitForGatewayReadyFn: func(_ context.Context, _, _ string, _ int64, _, _ time.Duration, _ func(gateway.GatewayReadinessProgress)) error {
			return nil
		},
	}
	wf := NewMigrationActions(gw, &mockClusterLinkService{})
	config := &MigrationConfig{K8sNamespace: "ns", InitialCrName: "gw-1", InitialCrYAML: []byte(testInitialCR), FenceRoutes: []string{"migration-route"}}

	require.NoError(t, wf.FenceGateway(context.Background(), config))
	require.NotNil(t, applied, "FenceGateway must apply a CR")

	// Re-parse the applied bytes and confirm the fence landed on the named route.
	var obj map[string]any
	require.NoError(t, yaml.Unmarshal(applied, &obj))
	routes := obj["spec"].(map[string]any)["routes"].([]any)
	route := routes[0].(map[string]any)
	assert.Equal(t, "migration-route", route["name"])
	fence, ok := route["fence"].(map[string]any)
	require.True(t, ok, "the named route must carry a fence block")
	assert.Equal(t, "ALL", fence["scope"])
	assert.Equal(t, "BROKER_NOT_AVAILABLE", fence["errorCode"])
}

func TestWorkflow_FenceGateway_HappyPath(t *testing.T) {
	var callOrder []string
	gw := &mockGatewayService{
		applyGatewayYAMLFn: func(_ context.Context, _, _ string, _ []byte, _ string) (string, error) {
			callOrder = append(callOrder, "apply")
			return "", nil
		},
		waitForGatewayReadyFn: func(_ context.Context, _, _ string, _ int64, _ time.Duration, _ time.Duration, onProgress func(gateway.GatewayReadinessProgress)) error {
			callOrder = append(callOrder, "wait")
			if onProgress != nil {
				onProgress(gateway.GatewayReadinessProgress{InitialPodCount: 3, PodsReady: 3, Elapsed: 2 * time.Second, RolloutDetected: true, Ready: true})
			}
			return nil
		},
	}
	cl := &mockClusterLinkService{}
	wf := NewMigrationActions(gw, cl)
	config := &MigrationConfig{K8sNamespace: "ns", InitialCrName: "gw-1", InitialCrYAML: []byte(testInitialCR), FenceRoutes: []string{"migration-route"}}

	err := wf.FenceGateway(context.Background(), config)
	require.NoError(t, err)
	assert.Equal(t, []string{"apply", "wait"}, callOrder, "apply must precede wait")
}

func TestWorkflow_FenceGateway_DetectionDisabled_UsesReadyWaitNotUIDDiffing(t *testing.T) {
	var unwantedCall string
	waitReadyCalled := false
	acceptedCalled := false
	gw := &mockGatewayService{
		getGatewayPodUIDsFn: func(_ context.Context, _, _ string) (map[k8stypes.UID]struct{}, error) {
			unwantedCall = "GetGatewayPodUIDs"
			return nil, nil
		},
		waitForGatewayPodsFn: func(_ context.Context, _, _ string, _ map[k8stypes.UID]struct{}, _ int64, _, _ time.Duration, _ func(gateway.PodRolloutProgress)) error {
			unwantedCall = "WaitForGatewayPods"
			return nil
		},
		waitForGatewayAcceptedFn: func(_ context.Context, _, _ string, _, _ time.Duration) error {
			acceptedCalled = true
			return nil
		},
		applyGatewayYAMLFn: func(_ context.Context, _, _ string, _ []byte, _ string) (string, error) {
			return "", nil
		},
		waitForGatewayReadyFn: func(_ context.Context, _, _ string, _ int64, _, _ time.Duration, _ func(gateway.GatewayReadinessProgress)) error {
			waitReadyCalled = true
			return nil
		},
	}
	cl := &mockClusterLinkService{}
	wf := NewMigrationActions(gw, cl)
	// DetectUnroutedProducersDuration unset (0) → detection disabled: the fence
	// keeps the lightweight readiness-only wait and never touches pod UIDs. The
	// operator-acceptance wait, by contrast, now runs on every path — the
	// Deployment-only wait cannot tell a no-op apply from a rejected one.
	config := &MigrationConfig{K8sNamespace: "ns", InitialCrName: "gw-1", InitialCrYAML: []byte(testInitialCR), FenceRoutes: []string{"migration-route"}}

	err := wf.FenceGateway(context.Background(), config)
	require.NoError(t, err)
	assert.Empty(t, unwantedCall, "with detection disabled, FenceGateway must not use UID-diffing methods, but called: %s", unwantedCall)
	assert.True(t, waitReadyCalled, "with detection disabled, FenceGateway must wait via WaitForGatewayReady")
	assert.True(t, acceptedCalled, "the acceptance wait must run even with detection disabled")
}

// TestWorkflow_FenceGateway_OperatorRejection_DoesNotProceed asserts a rejected
// fence CR aborts before the readiness wait. Previously the fence-without-
// detection path relied solely on the Deployment wait, which would report "No
// pod restart required" and let the migration continue to promote topics with
// the gateway never actually fenced.
func TestWorkflow_FenceGateway_OperatorRejection_DoesNotProceed(t *testing.T) {
	waitReadyCalled := false
	gw := &mockGatewayService{
		applyGatewayYAMLFn: func(_ context.Context, _, _ string, _ []byte, _ string) (string, error) { return "", nil },
		waitForGatewayAcceptedFn: func(_ context.Context, _, _ string, _, _ time.Duration) error {
			return rejectionError("gw-1")
		},
		waitForGatewayReadyFn: func(_ context.Context, _, _ string, _ int64, _, _ time.Duration, _ func(gateway.GatewayReadinessProgress)) error {
			waitReadyCalled = true
			return nil
		},
	}
	wf := NewMigrationActions(gw, &mockClusterLinkService{})
	config := &MigrationConfig{K8sNamespace: "ns", InitialCrName: "gw-1", InitialCrYAML: []byte(testInitialCR), FenceRoutes: []string{"migration-route"}}

	err := wf.FenceGateway(context.Background(), config)
	require.Error(t, err)
	assert.False(t, waitReadyCalled, "a rejected fence must not fall through to the Deployment readiness wait")
	assert.Contains(t, err.Error(), "secretRef kcp-perf-plain-jaas not found")
}

func TestWorkflow_FenceGateway_DetectionEnabled_WaitsForOldPodsGone(t *testing.T) {
	var callOrder []string
	waitReadyCalled := false
	oldUIDs := map[k8stypes.UID]struct{}{"old-pod": {}}
	var passedUIDs map[k8stypes.UID]struct{}
	gw := &mockGatewayService{
		getGatewayPodUIDsFn: func(_ context.Context, _, _ string) (map[k8stypes.UID]struct{}, error) {
			callOrder = append(callOrder, "getUIDs")
			return oldUIDs, nil
		},
		applyGatewayYAMLFn: func(_ context.Context, _, _ string, _ []byte, _ string) (string, error) {
			callOrder = append(callOrder, "apply")
			return "", nil
		},
		waitForGatewayAcceptedFn: func(_ context.Context, _, _ string, _, _ time.Duration) error {
			callOrder = append(callOrder, "reconcile")
			return nil
		},
		waitForGatewayPodsFn: func(_ context.Context, _, _ string, initialPodUIDs map[k8stypes.UID]struct{}, _ int64, _, _ time.Duration, onProgress func(gateway.PodRolloutProgress)) error {
			callOrder = append(callOrder, "waitPods")
			passedUIDs = initialPodUIDs
			if onProgress != nil {
				onProgress(gateway.PodRolloutProgress{InitialPodCount: 1, NewPodsReady: 1, OldPodsRemaining: 0, RolloutDetected: true})
			}
			return nil
		},
		waitForGatewayReadyFn: func(_ context.Context, _, _ string, _ int64, _, _ time.Duration, _ func(gateway.GatewayReadinessProgress)) error {
			waitReadyCalled = true
			return nil
		},
	}
	cl := &mockClusterLinkService{}
	wf := NewMigrationActions(gw, cl)
	// Detection enabled: capture the pre-fence pod set, wait for the operator to
	// observe the fenced CR (so "no rollout detected" downstream is trustworthy),
	// then wait for those old pods to actually terminate so no unfenced pod is
	// still serving traffic when detection's first offset snapshot is taken.
	config := &MigrationConfig{K8sNamespace: "ns", InitialCrName: "gw-1", InitialCrYAML: []byte(testInitialCR), FenceRoutes: []string{"migration-route"}, DetectUnroutedProducersDuration: 10 * time.Second}

	err := wf.FenceGateway(context.Background(), config)
	require.NoError(t, err)
	assert.Equal(t, []string{"getUIDs", "apply", "reconcile", "waitPods"}, callOrder,
		"with detection enabled, FenceGateway must capture pod UIDs before apply, wait for operator reconcile, then wait for pod rollout")
	assert.False(t, waitReadyCalled, "with detection enabled, FenceGateway must not use the readiness-only wait")
	assert.Equal(t, oldUIDs, passedUIDs, "the pre-apply pod UIDs must be passed to WaitForGatewayPods")
}

func TestWorkflow_FenceGateway_ApplyFailsReturnsWrappedError(t *testing.T) {
	gw := &mockGatewayService{
		applyGatewayYAMLFn: func(_ context.Context, _, _ string, _ []byte, _ string) (string, error) {
			return "", fmt.Errorf("k8s 403")
		},
	}
	cl := &mockClusterLinkService{}
	wf := NewMigrationActions(gw, cl)
	config := &MigrationConfig{K8sNamespace: "ns", InitialCrName: "gw-1", InitialCrYAML: []byte(testInitialCR), FenceRoutes: []string{"migration-route"}}

	err := wf.FenceGateway(context.Background(), config)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to apply fenced gateway CR")
	assert.Contains(t, err.Error(), "k8s 403")
}

func TestWorkflow_FenceGateway_WaitTimeoutPropagatesDeadlineExceeded(t *testing.T) {
	gw := &mockGatewayService{
		applyGatewayYAMLFn: func(_ context.Context, _, _ string, _ []byte, _ string) (string, error) {
			return "", nil
		},
		waitForGatewayReadyFn: func(_ context.Context, _, _ string, _ int64, _, _ time.Duration, _ func(gateway.GatewayReadinessProgress)) error {
			return fmt.Errorf("rollout-timeout exceeded: %w", context.DeadlineExceeded)
		},
	}
	cl := &mockClusterLinkService{}
	wf := NewMigrationActions(gw, cl)
	wf.SetRolloutTimeout(100 * time.Millisecond)
	config := &MigrationConfig{K8sNamespace: "ns", InitialCrName: "gw-1", InitialCrYAML: []byte(testInitialCR), FenceRoutes: []string{"migration-route"}}

	err := wf.FenceGateway(context.Background(), config)
	require.Error(t, err)
	assert.True(t, errors.Is(err, context.DeadlineExceeded), "DeadlineExceeded must propagate: %v", err)
}

func TestWorkflow_FenceGateway_WaitContextCancelledPropagates(t *testing.T) {
	gw := &mockGatewayService{
		applyGatewayYAMLFn: func(_ context.Context, _, _ string, _ []byte, _ string) (string, error) {
			return "", nil
		},
		waitForGatewayReadyFn: func(ctx context.Context, _, _ string, _ int64, _, _ time.Duration, _ func(gateway.GatewayReadinessProgress)) error {
			<-ctx.Done()
			return ctx.Err()
		},
	}
	cl := &mockClusterLinkService{}
	wf := NewMigrationActions(gw, cl)
	config := &MigrationConfig{K8sNamespace: "ns", InitialCrName: "gw-1", InitialCrYAML: []byte(testInitialCR), FenceRoutes: []string{"migration-route"}}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	err := wf.FenceGateway(ctx, config)
	require.Error(t, err)
	assert.True(t, errors.Is(err, context.Canceled))
}

func TestWorkflow_FenceGateway_PassesRolloutTimeoutToService(t *testing.T) {
	var observedTimeout time.Duration
	gw := &mockGatewayService{
		applyGatewayYAMLFn: func(_ context.Context, _, _ string, _ []byte, _ string) (string, error) { return "", nil },
		waitForGatewayReadyFn: func(_ context.Context, _, _ string, _ int64, _, timeout time.Duration, _ func(gateway.GatewayReadinessProgress)) error {
			observedTimeout = timeout
			return nil
		},
	}
	cl := &mockClusterLinkService{}
	wf := NewMigrationActions(gw, cl)
	wf.SetRolloutTimeout(15 * time.Minute)
	config := &MigrationConfig{K8sNamespace: "ns", InitialCrName: "gw-1", InitialCrYAML: []byte(testInitialCR), FenceRoutes: []string{"migration-route"}}

	err := wf.FenceGateway(context.Background(), config)
	require.NoError(t, err)
	assert.Equal(t, 15*time.Minute, observedTimeout)
}

func TestWorkflow_FenceGateway_DefaultRolloutTimeoutIsZero(t *testing.T) {
	var observedTimeout time.Duration
	gw := &mockGatewayService{
		applyGatewayYAMLFn: func(_ context.Context, _, _ string, _ []byte, _ string) (string, error) { return "", nil },
		waitForGatewayReadyFn: func(_ context.Context, _, _ string, _ int64, _, timeout time.Duration, _ func(gateway.GatewayReadinessProgress)) error {
			observedTimeout = timeout
			return nil
		},
	}
	cl := &mockClusterLinkService{}
	wf := NewMigrationActions(gw, cl)
	config := &MigrationConfig{K8sNamespace: "ns", InitialCrName: "gw-1", InitialCrYAML: []byte(testInitialCR), FenceRoutes: []string{"migration-route"}}

	err := wf.FenceGateway(context.Background(), config)
	require.NoError(t, err)
	assert.Equal(t, time.Duration(0), observedTimeout, "default rolloutTimeout should be 0 (no deadline)")
}

func TestWorkflow_SwitchGateway_HappyPath(t *testing.T) {
	var callOrder []string
	var appliedYAML []byte
	gw := &mockGatewayService{
		applyGatewayYAMLFn: func(_ context.Context, _, _ string, yaml []byte, _ string) (string, error) {
			appliedYAML = yaml
			callOrder = append(callOrder, "apply")
			return "", nil
		},
		waitForGatewayReadyFn: func(_ context.Context, _, _ string, _ int64, _, _ time.Duration, _ func(gateway.GatewayReadinessProgress)) error {
			callOrder = append(callOrder, "wait")
			return nil
		},
	}
	cl := &mockClusterLinkService{}
	wf := NewMigrationActions(gw, cl)
	config := &MigrationConfig{K8sNamespace: "ns", InitialCrName: "gw-1", InitialCrYAML: []byte(testInitialCR), SwitchoverTargets: testSwitchoverTargets}

	err := wf.SwitchGateway(context.Background(), config)
	require.NoError(t, err)
	assert.Equal(t, []string{"apply", "wait"}, callOrder, "apply (derived switched CR) must precede wait")
	assert.Contains(t, string(appliedYAML), "confluent-cloud", "the applied CR must carry the target streaming domain")
}

func TestWorkflow_SwitchGateway_WaitErrorIsWrapped(t *testing.T) {
	gw := &mockGatewayService{
		applyGatewayYAMLFn: func(_ context.Context, _, _ string, _ []byte, _ string) (string, error) { return "", nil },
		waitForGatewayReadyFn: func(_ context.Context, _, _ string, _ int64, _, _ time.Duration, _ func(gateway.GatewayReadinessProgress)) error {
			return fmt.Errorf("kube unreachable")
		},
	}
	cl := &mockClusterLinkService{}
	wf := NewMigrationActions(gw, cl)
	config := &MigrationConfig{K8sNamespace: "ns", InitialCrName: "gw-1", InitialCrYAML: []byte(testInitialCR), SwitchoverTargets: testSwitchoverTargets}

	err := wf.SwitchGateway(context.Background(), config)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed waiting for gateway readiness")
	assert.Contains(t, err.Error(), "kube unreachable")
}

// ===========================================================================
// Operator-acceptance guard — the switchover-verification gap
// ===========================================================================

// rejectionError builds the error the gateway service returns when the CFK
// operator refuses a spec, matching the rejection observed on 2026-07-27 while
// setting up the live-cluster e2e test infrastructure.
func rejectionError(gatewayName string) *gateway.GatewayRejectedError {
	return &gateway.GatewayRejectedError{
		Gateway:            gatewayName,
		ConditionType:      "platform.confluent.io/cluster-ready",
		Reason:             "ApplyFailed",
		Message:            "secretRef kcp-perf-plain-jaas not found",
		Generation:         4,
		ObservedGeneration: 3,
	}
}

// TestWorkflow_SwitchGateway_OperatorRejection_FailsWithOperatorMessage is the
// regression test for the reported bug: kcp printed "No pod restart required"
// then "✅ Migration complete!" while the gateway was still fenced, because the
// Deployment-based readiness wait cannot see that the operator refused the
// switchover CR. SwitchGateway must now fail, and fail with the operator's own
// diagnosis.
func TestWorkflow_SwitchGateway_OperatorRejection_FailsWithOperatorMessage(t *testing.T) {
	waitReadyCalled := false
	gw := &mockGatewayService{
		applyGatewayYAMLFn: func(_ context.Context, _, _ string, _ []byte, _ string) (string, error) { return "", nil },
		waitForGatewayAcceptedFn: func(_ context.Context, _, _ string, _, _ time.Duration) error {
			return rejectionError("gw-1")
		},
		waitForGatewayReadyFn: func(_ context.Context, _, _ string, _ int64, _, _ time.Duration, _ func(gateway.GatewayReadinessProgress)) error {
			waitReadyCalled = true
			return nil
		},
	}
	wf := NewMigrationActions(gw, &mockClusterLinkService{})
	config := &MigrationConfig{K8sNamespace: "ns", InitialCrName: "gw-1", InitialCrYAML: []byte(testInitialCR), SwitchoverTargets: testSwitchoverTargets}

	err := wf.SwitchGateway(context.Background(), config)
	require.Error(t, err, "a switchover the operator rejected must not be reported as a success")
	assert.False(t, waitReadyCalled, "must abort before the Deployment wait that would report 'No pod restart required'")

	var rejected *gateway.GatewayRejectedError
	require.ErrorAs(t, err, &rejected, "the typed rejection must survive to the caller")
	assert.Contains(t, err.Error(), "secretRef kcp-perf-plain-jaas not found")
	assert.Contains(t, err.Error(), "ApplyFailed")
}

// TestWorkflow_SwitchGateway_WaitsForAcceptanceBeforeReadiness pins the
// ordering: acceptance must be confirmed between the apply and the readiness
// wait, otherwise the readiness wait observes the pre-switchover Deployment.
func TestWorkflow_SwitchGateway_WaitsForAcceptanceBeforeReadiness(t *testing.T) {
	var callOrder []string
	gw := &mockGatewayService{
		applyGatewayYAMLFn: func(_ context.Context, _, _ string, _ []byte, _ string) (string, error) {
			callOrder = append(callOrder, "apply")
			return "", nil
		},
		waitForGatewayAcceptedFn: func(_ context.Context, _, _ string, _, _ time.Duration) error {
			callOrder = append(callOrder, "accepted")
			return nil
		},
		waitForGatewayReadyFn: func(_ context.Context, _, _ string, _ int64, _, _ time.Duration, _ func(gateway.GatewayReadinessProgress)) error {
			callOrder = append(callOrder, "ready")
			return nil
		},
	}
	wf := NewMigrationActions(gw, &mockClusterLinkService{})
	config := &MigrationConfig{K8sNamespace: "ns", InitialCrName: "gw-1", InitialCrYAML: []byte(testInitialCR), SwitchoverTargets: testSwitchoverTargets}

	require.NoError(t, wf.SwitchGateway(context.Background(), config))
	assert.Equal(t, []string{"apply", "accepted", "ready"}, callOrder)
}

// TestWorkflow_SwitchGateway_NonRejectionWaitError_IsWrapped keeps transport
// failures distinguishable from operator rejections.
func TestWorkflow_SwitchGateway_NonRejectionWaitError_IsWrapped(t *testing.T) {
	gw := &mockGatewayService{
		applyGatewayYAMLFn: func(_ context.Context, _, _ string, _ []byte, _ string) (string, error) { return "", nil },
		waitForGatewayAcceptedFn: func(_ context.Context, _, _ string, _, _ time.Duration) error {
			return fmt.Errorf("kube unreachable")
		},
	}
	wf := NewMigrationActions(gw, &mockClusterLinkService{})
	config := &MigrationConfig{K8sNamespace: "ns", InitialCrName: "gw-1", InitialCrYAML: []byte(testInitialCR), SwitchoverTargets: testSwitchoverTargets}

	err := wf.SwitchGateway(context.Background(), config)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed waiting for gateway reconcile during switchover")
	assert.Contains(t, err.Error(), "kube unreachable")
	var rejected *gateway.GatewayRejectedError
	assert.NotErrorAs(t, err, &rejected, "a transport failure is not an operator rejection")
}

// TestWorkflow_SwitchGateway_PassesRolloutTimeoutToAcceptanceWait ensures
// --rollout-timeout bounds the acceptance wait too, not just the readiness wait.
func TestWorkflow_SwitchGateway_PassesRolloutTimeoutToAcceptanceWait(t *testing.T) {
	var observedTimeout time.Duration
	gw := &mockGatewayService{
		applyGatewayYAMLFn: func(_ context.Context, _, _ string, _ []byte, _ string) (string, error) { return "", nil },
		waitForGatewayAcceptedFn: func(_ context.Context, _, _ string, _ time.Duration, timeout time.Duration) error {
			observedTimeout = timeout
			return nil
		},
	}
	wf := NewMigrationActions(gw, &mockClusterLinkService{})
	wf.SetRolloutTimeout(15 * time.Minute)
	config := &MigrationConfig{K8sNamespace: "ns", InitialCrName: "gw-1", InitialCrYAML: []byte(testInitialCR), SwitchoverTargets: testSwitchoverTargets}

	require.NoError(t, wf.SwitchGateway(context.Background(), config))
	assert.Equal(t, 15*time.Minute, observedTimeout)
}

// TestWorkflow_UnfenceGateway_OperatorRejection_Fails covers the rollback path:
// reporting restored traffic while the gateway is still fenced is the worst
// place to be blind to a rejected apply.
func TestWorkflow_UnfenceGateway_OperatorRejection_Fails(t *testing.T) {
	waitReadyCalled := false
	gw := &mockGatewayService{
		applyGatewayYAMLFn: func(_ context.Context, _, _ string, _ []byte, _ string) (string, error) { return "", nil },
		waitForGatewayAcceptedFn: func(_ context.Context, _, _ string, _, _ time.Duration) error {
			return rejectionError("gw-1")
		},
		waitForGatewayReadyFn: func(_ context.Context, _, _ string, _ int64, _, _ time.Duration, _ func(gateway.GatewayReadinessProgress)) error {
			waitReadyCalled = true
			return nil
		},
	}
	wf := NewMigrationActions(gw, &mockClusterLinkService{})
	config := &MigrationConfig{K8sNamespace: "ns", InitialCrName: "gw-1", InitialCrYAML: []byte("apiVersion: v1\nkind: Gateway\n")}

	err := wf.unfenceGateway(context.Background(), config)
	require.Error(t, err)
	assert.False(t, waitReadyCalled, "a rejected unfence must not report traffic restored")
	assert.Contains(t, err.Error(), "secretRef kcp-perf-plain-jaas not found")
}

// TestWorkflow_UnfenceGateway_WaitsForAcceptanceBeforeReadiness pins the
// ordering on the rollback path.
func TestWorkflow_UnfenceGateway_WaitsForAcceptanceBeforeReadiness(t *testing.T) {
	var callOrder []string
	gw := &mockGatewayService{
		applyGatewayYAMLFn: func(_ context.Context, _, _ string, _ []byte, _ string) (string, error) {
			callOrder = append(callOrder, "apply")
			return "", nil
		},
		waitForGatewayAcceptedFn: func(_ context.Context, _, _ string, _, _ time.Duration) error {
			callOrder = append(callOrder, "accepted")
			return nil
		},
		waitForGatewayReadyFn: func(_ context.Context, _, _ string, _ int64, _, _ time.Duration, _ func(gateway.GatewayReadinessProgress)) error {
			callOrder = append(callOrder, "ready")
			return nil
		},
	}
	wf := NewMigrationActions(gw, &mockClusterLinkService{})
	config := &MigrationConfig{K8sNamespace: "ns", InitialCrName: "gw-1", InitialCrYAML: []byte("apiVersion: v1\nkind: Gateway\n")}

	require.NoError(t, wf.unfenceGateway(context.Background(), config))
	assert.Equal(t, []string{"apply", "accepted", "ready"}, callOrder)
}

// ===========================================================================
// VerifyFence / DetectUnroutedProducers tests
// ===========================================================================

func TestWorkflow_VerifyFence_StableOffsets(t *testing.T) {
	gw := &mockGatewayService{}
	cl := &mockClusterLinkService{}

	// Source offsets are stable (same value on every call)
	offsetProvider := &mockOffsetProvider{
		getFn: func(topic string) (map[int32]int64, error) {
			return map[int32]int64{0: 500, 1: 600}, nil
		},
	}

	wf := NewMigrationActionsWithOffsets(gw, cl, offsetProvider, offsetProvider)
	config := &MigrationConfig{
		Topics:                          []string{"topic-1", "topic-2"},
		DetectUnroutedProducersDuration: time.Millisecond,
	}

	err := wf.VerifyFence(context.Background(), config)
	require.NoError(t, err)
}

func TestWorkflow_VerifyFence_IncreasingOffsets_ReturnsError(t *testing.T) {
	// VerifyFence only detects — it must NOT unfence the gateway itself.
	// Restoring traffic is the orchestrator's job on the abort_fence rollback
	// (see TestOrchestrator_Execute_UnroutedProducers_AbortsFenceAndRollsBack).
	var applyCalled bool
	gw := &mockGatewayService{
		applyGatewayYAMLFn: func(_ context.Context, _, _ string, _ []byte, _ string) (string, error) {
			applyCalled = true
			return "", nil
		},
	}
	cl := &mockClusterLinkService{}

	// Simulates an unrouted producer: offsets keep increasing on every call.
	var callCount int64
	sourceOffset := &mockOffsetProvider{
		getFn: func(topic string) (map[int32]int64, error) {
			n := atomic.AddInt64(&callCount, 1)
			if topic == "topic-1" {
				return map[int32]int64{0: 100 + n*10, 1: 200}, nil
			}
			return map[int32]int64{0: 300}, nil
		},
	}

	destOffset := &mockOffsetProvider{
		getFn: func(topic string) (map[int32]int64, error) {
			return map[int32]int64{0: 100, 1: 200}, nil
		},
	}

	wf := NewMigrationActionsWithOffsets(gw, cl, sourceOffset, destOffset)
	config := &MigrationConfig{
		Topics:                          []string{"topic-1"},
		DetectUnroutedProducersDuration: time.Millisecond,
		InitialCrYAML:                   []byte("apiVersion: platform.confluent.io/v1beta1\nkind: Gateway\nmetadata:\n  name: my-gw\n  namespace: ns\n  managedFields: []\n  resourceVersion: \"123\"\n"),
		InitialCrName:                   "my-gw",
		K8sNamespace:                    "ns",
	}

	err := wf.VerifyFence(context.Background(), config)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrUnroutedProducers)
	assert.Contains(t, err.Error(), "topic-1 partition 0")
	assert.False(t, applyCalled, "VerifyFence must not unfence the gateway; that is the orchestrator's responsibility")
}

func TestWorkflow_VerifyFence_NewPartitionBetweenSnapshots_IsViolation(t *testing.T) {
	// A partition that appears only in the second snapshot (created during
	// the monitoring window, or missing from the first fetch's metadata)
	// starts at offset 0 — any data on it was written after fencing and must
	// be flagged, not silently skipped.
	gw := &mockGatewayService{}
	cl := &mockClusterLinkService{}

	var call int64
	sourceOffset := &mockOffsetProvider{
		getFn: func(topic string) (map[int32]int64, error) {
			if atomic.AddInt64(&call, 1) == 1 {
				return map[int32]int64{0: 100}, nil // partition 1 not yet visible
			}
			return map[int32]int64{0: 100, 1: 50}, nil // partition 0 stable, 1 has data
		},
	}

	wf := NewMigrationActionsWithOffsets(gw, cl, sourceOffset, sourceOffset)
	config := &MigrationConfig{
		Topics:                          []string{"topic-1"},
		DetectUnroutedProducersDuration: time.Millisecond,
	}

	err := wf.VerifyFence(context.Background(), config)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrUnroutedProducers)
	assert.Contains(t, err.Error(), "topic-1 partition 1: offset 0 → 50")
}

func TestWorkflow_VerifyFence_Disabled_SkipsOffsetChecks(t *testing.T) {
	gw := &mockGatewayService{}
	cl := &mockClusterLinkService{}

	// Detection is disabled, so the offset providers must never be consulted.
	var getCalls int64
	offsetProvider := &mockOffsetProvider{
		getFn: func(topic string) (map[int32]int64, error) {
			atomic.AddInt64(&getCalls, 1)
			return map[int32]int64{0: 100}, nil
		},
	}

	wf := NewMigrationActionsWithOffsets(gw, cl, offsetProvider, offsetProvider)
	config := &MigrationConfig{
		Topics:                          []string{"topic-1"},
		DetectUnroutedProducersDuration: 0, // check disabled
	}

	err := wf.VerifyFence(context.Background(), config)
	require.NoError(t, err)
	assert.Equal(t, int64(0), atomic.LoadInt64(&getCalls),
		"offset providers should not be consulted when detection is disabled")
}

func TestWorkflow_VerifyFence_NilOffsetServices(t *testing.T) {
	gw := &mockGatewayService{}
	cl := &mockClusterLinkService{}

	wf := NewMigrationActions(gw, cl) // no offset providers
	config := &MigrationConfig{
		Topics:                          []string{"topic-1"},
		DetectUnroutedProducersDuration: time.Millisecond,
	}

	err := wf.VerifyFence(context.Background(), config)
	require.Error(t, err)
	assert.Equal(t, "source offset service is required for unrouted producer detection", err.Error())
}

func TestWorkflow_PromoteTopics_IgnoresDetectionConfig(t *testing.T) {
	// Unrouted-producer detection belongs to the verify_fence step; PromoteTopics
	// must not re-run it. If it did, this test would block on the 30s detection
	// window and trip the 2s context deadline.
	gw := &mockGatewayService{}

	promoted := make(map[string]bool)
	cl := &mockClusterLinkService{
		promoteMirrorTopicsFn: func(_ context.Context, _ clusterlink.Config, topicNames []string) (*clusterlink.PromoteMirrorTopicsResponse, error) {
			resp := &clusterlink.PromoteMirrorTopicsResponse{}
			for _, name := range topicNames {
				promoted[name] = true
				resp.Data = append(resp.Data, struct {
					MirrorTopicName string `json:"mirror_topic_name"`
					ErrorMessage    string `json:"error_message,omitempty"`
					ErrorCode       int    `json:"error_code,omitempty"`
				}{
					MirrorTopicName: name,
					ErrorCode:       0,
				})
			}
			return resp, nil
		},
		// Accepted promotions are confirmed STOPPED via ListMirrorTopics
		// before PromoteTopics returns.
		listMirrorTopicsFn: func(_ context.Context, _ clusterlink.Config) ([]clusterlink.MirrorTopic, error) {
			return []clusterlink.MirrorTopic{
				{MirrorTopicName: "topic-1", MirrorStatus: clusterlink.MirrorStatusStopped},
			}, nil
		},
	}

	// Zero lag so promotion completes immediately
	offsetProvider := &mockOffsetProvider{
		getFn: func(topic string) (map[int32]int64, error) {
			return map[int32]int64{0: 500}, nil
		},
	}

	wf := NewMigrationActionsWithOffsets(gw, cl, offsetProvider, offsetProvider)
	wf.promotePollInterval = time.Millisecond
	config := &MigrationConfig{
		Topics:                          []string{"topic-1"},
		ClusterRestEndpoint:             "https://cluster",
		ClusterId:                       "lkc-123",
		ClusterLinkName:                 "link-1",
		DetectUnroutedProducersDuration: 30 * time.Second,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err := wf.PromoteTopics(ctx, config, clusterlink.BasicAuth{Username: "key", Password: "secret"})
	require.NoError(t, err, "PromoteTopics should not run unrouted-producer detection")
	assert.True(t, promoted["topic-1"])
}

func TestWorkflow_UnfenceGateway_StripsServerMetadata(t *testing.T) {
	var appliedYAML []byte
	gw := &mockGatewayService{
		applyGatewayYAMLFn: func(_ context.Context, _, _ string, yaml []byte, _ string) (string, error) {
			appliedYAML = yaml
			return "", nil
		},
	}
	cl := &mockClusterLinkService{}

	wf := NewMigrationActions(gw, cl)
	config := &MigrationConfig{
		InitialCrName: "my-gw",
		K8sNamespace:  "confluent",
		InitialCrYAML: []byte(`apiVersion: platform.confluent.io/v1beta1
kind: Gateway
metadata:
  name: my-gw
  namespace: confluent
  managedFields:
  - manager: confluent-operator
    operation: Apply
  resourceVersion: "12345"
  uid: abc-def-123
  creationTimestamp: "2026-01-01T00:00:00Z"
  generation: 3
spec:
  streamingDomains:
  - name: source-kafka-cluster
status:
  phase: RUNNING
`),
	}

	err := wf.unfenceGateway(context.Background(), config)
	require.NoError(t, err)
	require.NotNil(t, appliedYAML, "ApplyGatewayYAML should have been called")

	yamlStr := string(appliedYAML)
	assert.NotContains(t, yamlStr, "managedFields", "managedFields should be stripped")
	assert.NotContains(t, yamlStr, "resourceVersion", "resourceVersion should be stripped")
	assert.NotContains(t, yamlStr, "uid", "uid should be stripped")
	assert.NotContains(t, yamlStr, "creationTimestamp", "creationTimestamp should be stripped")
	assert.NotContains(t, yamlStr, "generation", "generation should be stripped")
	assert.NotContains(t, yamlStr, "status", "status should be stripped")
	assert.Contains(t, yamlStr, "streamingDomains", "spec should be preserved")
	assert.Contains(t, yamlStr, "source-kafka-cluster", "spec values should be preserved")
}

func TestWorkflow_UnfenceGateway_WaitsForGatewayReadiness(t *testing.T) {
	var applyCalled, waitCalled bool
	gw := &mockGatewayService{
		applyGatewayYAMLFn: func(_ context.Context, _, _ string, _ []byte, _ string) (string, error) {
			applyCalled = true
			return "", nil
		},
		waitForGatewayReadyFn: func(_ context.Context, namespace, name string, _ int64, _, _ time.Duration, _ func(gateway.GatewayReadinessProgress)) error {
			waitCalled = true
			assert.True(t, applyCalled, "readiness wait must happen after the CR is applied")
			assert.Equal(t, "confluent", namespace)
			assert.Equal(t, "my-gw", name)
			return nil
		},
	}
	cl := &mockClusterLinkService{}

	wf := NewMigrationActions(gw, cl)
	config := &MigrationConfig{
		InitialCrName: "my-gw",
		K8sNamespace:  "confluent",
		InitialCrYAML: []byte("apiVersion: platform.confluent.io/v1beta1\nkind: Gateway\nmetadata:\n  name: my-gw\n  namespace: confluent\n"),
	}

	err := wf.unfenceGateway(context.Background(), config)
	require.NoError(t, err)
	assert.True(t, waitCalled, "unfenceGateway should wait for gateway readiness after applying the initial CR")
}

func TestWorkflow_UnfenceGateway_ReadinessFailure_ReturnsError(t *testing.T) {
	gw := &mockGatewayService{
		applyGatewayYAMLFn: func(_ context.Context, _, _ string, _ []byte, _ string) (string, error) {
			return "", nil
		},
		waitForGatewayReadyFn: func(_ context.Context, _, _ string, _ int64, _, _ time.Duration, _ func(gateway.GatewayReadinessProgress)) error {
			return fmt.Errorf("gateway pods did not converge")
		},
	}
	cl := &mockClusterLinkService{}

	wf := NewMigrationActions(gw, cl)
	config := &MigrationConfig{
		InitialCrName: "my-gw",
		K8sNamespace:  "confluent",
		InitialCrYAML: []byte("apiVersion: platform.confluent.io/v1beta1\nkind: Gateway\nmetadata:\n  name: my-gw\n  namespace: confluent\n"),
	}

	err := wf.unfenceGateway(context.Background(), config)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed waiting for gateway readiness during unfence")
	assert.Contains(t, err.Error(), "gateway pods did not converge")
}

func TestWorkflow_DetectUnroutedProducers_ContextCancelled(t *testing.T) {
	gw := &mockGatewayService{}
	cl := &mockClusterLinkService{}

	sourceOffset := &mockOffsetProvider{
		getFn: func(topic string) (map[int32]int64, error) {
			return map[int32]int64{0: 100}, nil
		},
	}
	destOffset := &mockOffsetProvider{
		getFn: func(topic string) (map[int32]int64, error) {
			return map[int32]int64{0: 100}, nil
		},
	}

	wf := NewMigrationActionsWithOffsets(gw, cl, sourceOffset, destOffset)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel

	err := wf.detectUnroutedProducers(ctx, []string{"topic-1"}, 5*time.Second)
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
}

// ===========================================================================
// Helper tests
// ===========================================================================

func TestFormatLag64(t *testing.T) {
	tests := []struct {
		input    int64
		expected string
	}{
		{0, "0"},
		{999, "999"},
		{1000, "1,000"},
		{21655, "21,655"},
		{1000000, "1,000,000"},
	}

	for _, tc := range tests {
		t.Run(fmt.Sprintf("%d", tc.input), func(t *testing.T) {
			got := formatLag64(tc.input)
			assert.Equal(t, tc.expected, got, "formatLag64(%d)", tc.input)
		})
	}
}

// ===========================================================================
// Sweep-failure tolerance tests (maxConsecutiveSweepFailures)
// ===========================================================================

// zeroLagBatch builds a GetMany-shaped result with the same offset for every
// topic, so source and destination compare at zero lag.
func zeroLagBatch(topics []string, off int64) map[string]map[int32]int64 {
	out := make(map[string]map[int32]int64, len(topics))
	for _, topic := range topics {
		out[topic] = map[int32]int64{0: off}
	}
	return out
}

func TestWorkflow_CheckLags_ToleratesTransientSweepFailures(t *testing.T) {
	gw := &mockGatewayService{}
	cl := &mockClusterLinkService{}

	// The source sweep fails twice (fewer than maxConsecutiveSweepFailures),
	// then succeeds at zero lag.
	var calls atomic.Int32
	sourceOffset := &mockOffsetProvider{
		getManyFn: func(topics []string) (map[string]map[int32]int64, error) {
			if calls.Add(1) <= 2 {
				return nil, fmt.Errorf("leader election in progress")
			}
			return zeroLagBatch(topics, 1000), nil
		},
	}
	destOffset := &mockOffsetProvider{
		getManyFn: func(topics []string) (map[string]map[int32]int64, error) {
			return zeroLagBatch(topics, 1000), nil
		},
	}

	wf := NewMigrationActionsWithOffsets(gw, cl, sourceOffset, destOffset)
	wf.lagPollInterval = time.Millisecond
	config := &MigrationConfig{Topics: []string{"topic-1"}}

	err := wf.CheckLags(context.Background(), config, 10, clusterlink.BasicAuth{Username: "key", Password: "secret"})
	require.NoError(t, err, "two transient sweep failures must be ridden out")
	assert.GreaterOrEqual(t, calls.Load(), int32(3), "expected the sweep to be retried on later ticks")
}

func TestWorkflow_CheckLags_AbortsAfterMaxConsecutiveSweepFailures(t *testing.T) {
	gw := &mockGatewayService{}
	cl := &mockClusterLinkService{}

	var calls atomic.Int32
	sourceOffset := &mockOffsetProvider{
		getManyFn: func(topics []string) (map[string]map[int32]int64, error) {
			calls.Add(1)
			return nil, fmt.Errorf("broker unreachable")
		},
	}
	destOffset := &mockOffsetProvider{
		getManyFn: func(topics []string) (map[string]map[int32]int64, error) {
			return zeroLagBatch(topics, 1000), nil
		},
	}

	wf := NewMigrationActionsWithOffsets(gw, cl, sourceOffset, destOffset)
	wf.lagPollInterval = time.Millisecond
	config := &MigrationConfig{Topics: []string{"topic-1"}}

	err := wf.CheckLags(context.Background(), config, 10, clusterlink.BasicAuth{Username: "key", Password: "secret"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), fmt.Sprintf("%d consecutive", maxConsecutiveSweepFailures))
	assert.Contains(t, err.Error(), "broker unreachable", "the underlying cause must be preserved")
	assert.Equal(t, int32(maxConsecutiveSweepFailures), calls.Load(),
		"the sweep must not be attempted again after the abort threshold")
}

func TestWorkflow_CheckLags_SweepFailureCounterResetsOnSuccess(t *testing.T) {
	gw := &mockGatewayService{}
	cl := &mockClusterLinkService{}

	// Scripted sequence: fail, fail, succeed-above-threshold (loop continues),
	// fail, fail, succeed-at-zero-lag. Four total failures but never three in
	// a row — only a counter that resets on success lets this pass.
	var calls atomic.Int32
	sourceOffset := &mockOffsetProvider{
		getManyFn: func(topics []string) (map[string]map[int32]int64, error) {
			switch calls.Add(1) {
			case 1, 2, 4, 5:
				return nil, fmt.Errorf("transient sweep failure")
			case 3:
				return zeroLagBatch(topics, 5000), nil // lag 4000 → above threshold
			default:
				return zeroLagBatch(topics, 1000), nil // lag 0 → done
			}
		},
	}
	destOffset := &mockOffsetProvider{
		getManyFn: func(topics []string) (map[string]map[int32]int64, error) {
			return zeroLagBatch(topics, 1000), nil
		},
	}

	wf := NewMigrationActionsWithOffsets(gw, cl, sourceOffset, destOffset)
	wf.lagPollInterval = time.Millisecond
	config := &MigrationConfig{Topics: []string{"topic-1"}}

	err := wf.CheckLags(context.Background(), config, 10, clusterlink.BasicAuth{Username: "key", Password: "secret"})
	require.NoError(t, err, "four non-consecutive failures must not abort")
	assert.Equal(t, int32(6), calls.Load())
}

func TestWorkflow_PromoteTopics_ToleratesTransientSweepFailures(t *testing.T) {
	gw := &mockGatewayService{}

	promoted := make(map[string]bool)
	cl := &mockClusterLinkService{
		promoteMirrorTopicsFn: func(_ context.Context, _ clusterlink.Config, topicNames []string) (*clusterlink.PromoteMirrorTopicsResponse, error) {
			resp := &clusterlink.PromoteMirrorTopicsResponse{}
			for _, name := range topicNames {
				promoted[name] = true
				resp.Data = append(resp.Data, struct {
					MirrorTopicName string `json:"mirror_topic_name"`
					ErrorMessage    string `json:"error_message,omitempty"`
					ErrorCode       int    `json:"error_code,omitempty"`
				}{
					MirrorTopicName: name,
					ErrorCode:       0,
				})
			}
			return resp, nil
		},
		listMirrorTopicsFn: func(_ context.Context, _ clusterlink.Config) ([]clusterlink.MirrorTopic, error) {
			return []clusterlink.MirrorTopic{
				{MirrorTopicName: "topic-1", MirrorStatus: clusterlink.MirrorStatusStopped},
			}, nil
		},
	}

	// The source sweep fails twice, then reports zero lag so promotion runs.
	var calls atomic.Int32
	sourceOffset := &mockOffsetProvider{
		getManyFn: func(topics []string) (map[string]map[int32]int64, error) {
			if calls.Add(1) <= 2 {
				return nil, fmt.Errorf("leader election in progress")
			}
			return zeroLagBatch(topics, 1000), nil
		},
	}
	destOffset := &mockOffsetProvider{
		getManyFn: func(topics []string) (map[string]map[int32]int64, error) {
			return zeroLagBatch(topics, 1000), nil
		},
	}

	wf := NewMigrationActionsWithOffsets(gw, cl, sourceOffset, destOffset)
	wf.promotePollInterval = time.Millisecond
	config := &MigrationConfig{Topics: []string{"topic-1"}}

	err := wf.PromoteTopics(context.Background(), config, clusterlink.BasicAuth{Username: "key", Password: "secret"})
	require.NoError(t, err, "two transient sweep failures must be ridden out")
	assert.True(t, promoted["topic-1"], "topic must still be promoted after tolerated failures")
}

func TestWorkflow_PromoteTopics_AbortsAfterMaxConsecutiveSweepFailures(t *testing.T) {
	gw := &mockGatewayService{}
	cl := &mockClusterLinkService{}

	var calls atomic.Int32
	sourceOffset := &mockOffsetProvider{
		getManyFn: func(topics []string) (map[string]map[int32]int64, error) {
			calls.Add(1)
			return nil, fmt.Errorf("broker unreachable")
		},
	}
	destOffset := &mockOffsetProvider{
		getManyFn: func(topics []string) (map[string]map[int32]int64, error) {
			return zeroLagBatch(topics, 1000), nil
		},
	}

	wf := NewMigrationActionsWithOffsets(gw, cl, sourceOffset, destOffset)
	wf.promotePollInterval = time.Millisecond
	config := &MigrationConfig{Topics: []string{"topic-1"}}

	err := wf.PromoteTopics(context.Background(), config, clusterlink.BasicAuth{Username: "key", Password: "secret"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), fmt.Sprintf("%d consecutive", maxConsecutiveSweepFailures))
	assert.Contains(t, err.Error(), "broker unreachable", "the underlying cause must be preserved")
	assert.Equal(t, int32(maxConsecutiveSweepFailures), calls.Load(),
		"the sweep must not be attempted again after the abort threshold")
}
