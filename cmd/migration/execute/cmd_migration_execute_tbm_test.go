package execute

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/confluentinc/kcp/internal/manifest"
	"github.com/confluentinc/kcp/internal/services/clusterlink"
	"github.com/confluentinc/kcp/internal/services/gateway"
	"github.com/confluentinc/kcp/internal/services/migplan"
	"github.com/confluentinc/kcp/internal/services/migration"
	"github.com/confluentinc/kcp/internal/services/migration/tbm"
	"github.com/confluentinc/kcp/internal/services/offset"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	k8stypes "k8s.io/apimachinery/pkg/types"
)

// This file exercises the dynamic-mode (TBM) branch of the unified `execute`
// dispatcher. Its structural problem, and the reason these tests are shaped the
// way they are:
//
// The command layer calls migplan.Reconcile with NO injectable seam (Global
// Constraints keep the AAO branch's zero-DI posture, and the dynamic branch
// inherits it for reconcile). A real migplan.Reconcile builds LIVE source/target
// Kafka listers and a LIVE Gateway CR pull unconditionally (see
// internal/services/migplan/run.go), so it cannot succeed — nor resolve a
// Mode — inside this test process, exactly as
// TestExecute_ResumeFromUninitialized_CallsReconcile already demonstrates for
// the static path (it asserts Reconcile is *called* and *fails*).
//
// Reconcile is therefore only consulted for a migration still at
// StateUninitialized. Every RESUME (CurrentState past StateUninitialized) skips
// it and dispatches on the persisted config.Mode — which is precisely the
// production path a second `execute` run takes after a prior run's Initialize
// resolved and persisted the mode. These tests drive that real resume dispatch:
// a migration persisted at a resumable state with Mode:"dynamic" and the
// artifacts a prior Initialize would have captured, run with ONLY the TBM
// branch's downstream services (offsets/gateway/cluster-link) stubbed. Reaching
// tbm.StateSwitched proves the dispatch routed to tbm.TBMOrchestrator: the AAO
// executor dials the real source/destination Kafka clusters BEFORE building its
// orchestrator (MigrationExecutor.createSourceOffset/createDestinationOffset),
// so against the fixture's placeholder endpoints it could never reach switched
// — only the stubbed TBM branch can.

// dynamicRouteGatewayYAML is a minimal dynamic-route Gateway CR fixture,
// mirroring internal/services/migration/tbm's own test fixture of the same
// shape (and the deleted execute-tbm command test's copy) — kept as a separate
// copy since this package cannot import an internal package's test-only file.
// A dynamic route carries a `rules:` block (routing/coordination), the shape
// migplan resolves to Mode:"dynamic"; a static route would instead carry a
// fence/streamingDomain block.
const dynamicRouteGatewayYAML = `apiVersion: platform.confluent.io/v1beta1
kind: Gateway
metadata:
  name: gateway-initial
  namespace: confluent
spec:
  streamingDomains:
    - name: source
      kafkaCluster:
        name: source-cluster
    - name: target
      kafkaCluster:
        name: target-cluster
  routes:
    - name: migration-route
      endpoint: kafka-gw.example.com:9092
      streamingDomains:
        - name: source
          bootstrapServerId: sasl-scram
        - name: target
          bootstrapServerId: sasl-plain
      rules:
        routing:
          coordination:
            group: source
          default: source
`

// dynamicFenceYAML / dynamicSwitchoverYAML are the mutually-consistent rules:
// fragments a prior Initialize would have captured onto the config from
// migplan.Reconcile's Result, so the now-real fence/switch transitions can graft
// them onto dynamicRouteGatewayYAML's migration-route and succeed against the
// stub gateway. Copied from the deleted execute-tbm test's realisticReconcileResult.
const (
	dynamicFenceYAML      = "rules:\n  routing:\n    coordination:\n      group: source\n    default: source\n  fencing:\n    - topics: [\"t1.order\"]\n"
	dynamicSwitchoverYAML = "rules:\n  routing:\n    coordination:\n      group: source\n    default: source\n    conditions:\n      - topics: [\"t1.order\"]\n        streamingDomain: target\n"
)

// writeDynamicState persists a config for a migration a prior run already
// registered and initialized as dynamic-mode: CurrentState is a resumable state
// past StateUninitialized (so the dispatcher skips reconcile and routes on
// config.Mode), Mode is "dynamic", and the fence/switch artifacts are populated
// exactly as tbm.TBMActions.Initialize would have left them. edit runs last, so
// a test can vary a single field. The base config still matches the unmutated
// manifest (writeState builds it from there), so detectDrift stays clean.
func (f fixture) writeDynamicState(t *testing.T, currentState string, edit func(*migration.MigrationConfig)) {
	t.Helper()
	f.writeState(t, func(c *migration.MigrationConfig) {
		c.CurrentState = currentState
		c.Mode = "dynamic"
		c.Topics = []string{"t1.order"}
		c.GatewayYAML = dynamicRouteGatewayYAML
		c.FenceYAML = dynamicFenceYAML
		c.SwitchoverYAML = dynamicSwitchoverYAML
		if edit != nil {
			edit(c)
		}
	})
}

// runExecuteWithTBMDeps builds the command with the three TBM service builders
// injected (mirroring this file's runExecute, which uses the production
// builders) and runs it. Only the TBM branch's downstream services are stubbed;
// migplan.Reconcile keeps its zero-DI posture, so a dynamic dispatch here must
// come from a persisted config.Mode, never a stubbed reconcile.
func runExecuteWithTBMDeps(t *testing.T, buildOffsets offsetProvidersFunc, buildGateway gatewayServiceFunc, buildClusterLink clusterLinkServiceFunc, args ...string) (string, error) {
	t.Helper()
	cmd := newMigrationExecuteCmd(buildOffsets, buildGateway, buildClusterLink)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

// --- stub downstream services (ported from the deleted execute-tbm test) ---

// zeroLagOffsetProvider implements offset.Provider, reporting the same fixed
// offset for every topic requested — used for both source and destination, so
// every topic sees zero lag (wait_for_lags passes any threshold; promote sees
// exact zero lag) without dialing anything.
type zeroLagOffsetProvider struct{}

func (zeroLagOffsetProvider) GetMany(_ context.Context, topics []string) (map[string]map[int32]int64, error) {
	out := make(map[string]map[int32]int64, len(topics))
	for _, topic := range topics {
		out[topic] = map[int32]int64{0: 1000}
	}
	return out, nil
}

func stubOffsetProviders(*manifest.GatewayMigration) (offset.Provider, offset.Provider, func() error, error) {
	return zeroLagOffsetProvider{}, zeroLagOffsetProvider{}, func() error { return nil }, nil
}

// stubGatewayServiceImpl implements gateway.Service with always-succeeding
// no-op behavior (VerifyRollout, so no configId is injected), so the fence and
// switch transitions walk without reaching a real Kubernetes cluster. The
// engine's own gateway behavior is exercised in internal/services/gateway and
// internal/services/migration/tbm, not duplicated here.
type stubGatewayServiceImpl struct{}

func (stubGatewayServiceImpl) GetGatewayYAML(context.Context, string, string) ([]byte, error) {
	return nil, fmt.Errorf("stubGatewayServiceImpl.GetGatewayYAML not implemented")
}

func (stubGatewayServiceImpl) DetectCapability(context.Context, string, string, int, []byte, []byte) (gateway.Capability, error) {
	return gateway.Capability{Mode: gateway.VerifyRollout}, nil
}

func (stubGatewayServiceImpl) WaitForGatewayConfigID(context.Context, string, string, gateway.ConfigWaitOptions) error {
	return nil
}

func (stubGatewayServiceImpl) CheckPermissions(context.Context, string, string, string, string) (bool, error) {
	return true, nil
}

func (stubGatewayServiceImpl) PatchGatewayRoute(context.Context, string, string, gateway.RoutePatch, string) (string, error) {
	return "", nil
}

func (stubGatewayServiceImpl) PatchGatewayConfigID(context.Context, string, string, string) (string, error) {
	return "", nil
}

func (stubGatewayServiceImpl) WaitForGatewayAccepted(context.Context, string, string, time.Duration, time.Duration) error {
	return nil
}

func (stubGatewayServiceImpl) GetGatewayPodUIDs(context.Context, string, string) (map[k8stypes.UID]struct{}, error) {
	return nil, fmt.Errorf("stubGatewayServiceImpl.GetGatewayPodUIDs not implemented")
}

func (stubGatewayServiceImpl) GetGatewayDeploymentGeneration(context.Context, string, string) (int64, error) {
	return 0, nil
}

func (stubGatewayServiceImpl) WaitForGatewayPods(context.Context, string, string, map[k8stypes.UID]struct{}, int64, time.Duration, time.Duration, func(gateway.PodRolloutProgress)) error {
	return fmt.Errorf("stubGatewayServiceImpl.WaitForGatewayPods not implemented")
}

func (stubGatewayServiceImpl) WaitForGatewayReady(context.Context, string, string, int64, time.Duration, time.Duration, func(gateway.GatewayReadinessProgress)) error {
	return nil
}

func stubGatewayService(*manifest.GatewayMigration) (gateway.Service, error) {
	return stubGatewayServiceImpl{}, nil
}

// stubClusterLinkServiceImpl implements clusterlink.Service, reporting every
// requested topic as already STOPPED and accepting every promote, so the
// promote transition walks without reaching a real cluster-link REST endpoint.
type stubClusterLinkServiceImpl struct{}

func (stubClusterLinkServiceImpl) GetClusterLink(context.Context, clusterlink.Config) (*clusterlink.ClusterLink, error) {
	return nil, fmt.Errorf("stubClusterLinkServiceImpl.GetClusterLink not implemented")
}

func (stubClusterLinkServiceImpl) ListMirrorTopics(_ context.Context, config clusterlink.Config) ([]clusterlink.MirrorTopic, error) {
	out := make([]clusterlink.MirrorTopic, len(config.Topics))
	for i, t := range config.Topics {
		out[i] = clusterlink.MirrorTopic{MirrorTopicName: t, MirrorStatus: clusterlink.MirrorStatusStopped}
	}
	return out, nil
}

func (stubClusterLinkServiceImpl) ListConfigs(context.Context, clusterlink.Config) (map[string]string, error) {
	return nil, fmt.Errorf("stubClusterLinkServiceImpl.ListConfigs not implemented")
}

func (stubClusterLinkServiceImpl) ValidateTopics([]string, []string) error {
	return fmt.Errorf("stubClusterLinkServiceImpl.ValidateTopics not implemented")
}

func (stubClusterLinkServiceImpl) PromoteMirrorTopics(_ context.Context, _ clusterlink.Config, topicNames []string) (*clusterlink.PromoteMirrorTopicsResponse, error) {
	return promoteAll(topicNames), nil
}

func (stubClusterLinkServiceImpl) CreateMirrorTopic(context.Context, clusterlink.Config, string, string) error {
	return fmt.Errorf("stubClusterLinkServiceImpl.CreateMirrorTopic not implemented")
}

func (stubClusterLinkServiceImpl) ListTopics(context.Context, clusterlink.Config) ([]string, error) {
	return nil, fmt.Errorf("stubClusterLinkServiceImpl.ListTopics not implemented")
}

func (stubClusterLinkServiceImpl) CreateTopic(context.Context, clusterlink.Config, clusterlink.CreateTopicRequest) error {
	return fmt.Errorf("stubClusterLinkServiceImpl.CreateTopic not implemented")
}

func (stubClusterLinkServiceImpl) AlterConfigs(context.Context, clusterlink.Config, []clusterlink.ConfigAlteration) error {
	return fmt.Errorf("stubClusterLinkServiceImpl.AlterConfigs not implemented")
}

func stubClusterLinkService(*manifest.GatewayMigration) (clusterlink.Service, error) {
	return stubClusterLinkServiceImpl{}, nil
}

// promoteAll builds a response accepting every named topic (error_code 0).
func promoteAll(topicNames []string) *clusterlink.PromoteMirrorTopicsResponse {
	resp := &clusterlink.PromoteMirrorTopicsResponse{}
	for _, name := range topicNames {
		resp.Data = append(resp.Data, struct {
			MirrorTopicName string `json:"mirror_topic_name"`
			ErrorMessage    string `json:"error_message,omitempty"`
			ErrorCode       int    `json:"error_code,omitempty"`
		}{MirrorTopicName: name})
	}
	return resp
}

// --- Test 1: dynamic dispatch drives the TBM orchestrator to switched ---

// TestExecute_DynamicMode_DispatchesToTBMOrchestrator proves a dynamic-mode
// resume routes through tbm.TBMOrchestrator and walks its FSM to
// tbm.StateSwitched. Reaching switched is the discriminator: the AAO branch
// dials the real source/destination Kafka clusters before it ever builds an
// orchestrator, so against the fixture's placeholder endpoints it could not
// reach switched — only the stubbed dynamic branch can. Starting from
// StateInitialized exercises the full remaining walk (wait_for_lags → fence →
// verify_fence → promote → switch).
func TestExecute_DynamicMode_DispatchesToTBMOrchestrator(t *testing.T) {
	f := newFixture(t, nil)
	f.writeDynamicState(t, migration.StateInitialized, nil)

	out, err := runExecuteWithTBMDeps(t, stubOffsetProviders, stubGatewayService, stubClusterLinkService,
		"--migration-yaml", f.manifestPath, "--migration-state-file", f.stateFile)
	require.NoError(t, err)
	assert.Contains(t, out, "Migration completed")

	cfg := persistedConfig(t, f)
	assert.Equal(t, migration.StateSwitched, cfg.CurrentState,
		"a dynamic-mode resume must walk the TBM FSM all the way to switched")
}

// TestExecute_StaticModeSetup_DoesNotReachSwitchedWithTBMStubs is the negative
// control for Test 1: the SAME fixture and SAME injected TBM stubs, but with
// Mode:"static", must NOT reach switched — the dispatcher sends it down the AAO
// branch, which ignores these stubs and dials the fixture's unreachable
// clusters. This proves Test 1's success is caused by the mode dispatch, not by
// the stubs being wired in regardless of mode.
func TestExecute_StaticModeSetup_DoesNotReachSwitchedWithTBMStubs(t *testing.T) {
	f := newFixture(t, nil)
	f.writeDynamicState(t, migration.StateInitialized, func(c *migration.MigrationConfig) {
		c.Mode = "static"
	})

	_, err := runExecuteWithTBMDeps(t, stubOffsetProviders, stubGatewayService, stubClusterLinkService,
		"--migration-yaml", f.manifestPath, "--migration-state-file", f.stateFile)
	require.Error(t, err, "the AAO branch dials the fixture's unreachable clusters and fails")

	cfg := persistedConfig(t, f)
	assert.NotEqual(t, migration.StateSwitched, cfg.CurrentState,
		"the static branch must not use the TBM stubs, so it cannot reach switched here")
}

// TestExecute_DynamicMode_InitializePersistsMode_ResumeDispatchesToTBM is the
// two-invocation regression guard for the plan defect where TBMActions.Initialize
// never wrote config.Mode: a dynamic migration interrupted after Initialize
// persisted Mode:"" and the NEXT run's dispatch (mode := config.Mode) fell
// through to the static/AAO branch, silently driving the wrong executor. Unlike
// this file's other tests it does NOT pre-set Mode on a fixture — run 1 registers
// a fresh migration and completes a REAL TBMActions.Initialize, which must
// persist Mode itself; run 2 reads only what run 1 wrote.
//
// Run 1 is driven through the orchestrator directly (with the TBM services
// stubbed) rather than the command, because the command's live migplan.Reconcile
// cannot run in this process — it dials real Kafka and the live Gateway CR. That
// call is exactly what runTBMBranch makes in production once reconcile has
// produced the Result; here we supply that Result directly, standing in for the
// live reconcile. Nothing pre-sets config.Mode; TBMActions.Initialize must.
func TestExecute_DynamicMode_InitializePersistsMode_ResumeDispatchesToTBM(t *testing.T) {
	f := newFixture(t, nil)

	// Run 1: a fresh, unregistered dynamic migration, built exactly as the
	// command registers a new one (buildFreshMigrationConfig → StateUninitialized,
	// Mode unset). Drive its real Initialize to completion via the orchestrator.
	const id = "msk-prod-to-cc-batch-1"
	g := loadGateway(t, f.manifestPath)
	kubePath, err := resolveKubeConfigPath(g)
	require.NoError(t, err)
	fresh := buildFreshMigrationConfig(g, id, kubePath)
	require.Empty(t, fresh.Mode, "sanity: a freshly-registered migration carries no Mode")
	require.Equal(t, migration.StateUninitialized, fresh.CurrentState)

	state := migration.NewMigrationState()
	state.UpsertMigration(fresh)
	require.NoError(t, state.WriteToFile(f.stateFile))

	restCreds, err := g.RestCredentials()
	require.NoError(t, err)
	actions := tbm.NewTBMActions(zeroLagOffsetProvider{}, zeroLagOffsetProvider{}, stubGatewayServiceImpl{}, stubClusterLinkServiceImpl{})
	orchestrator := tbm.NewTBMOrchestrator(&fresh, actions, state, f.stateFile)
	res := &migplan.Result{
		Route:          "migration-route",
		Topics:         []string{"t1.order"},
		FenceYAML:      dynamicFenceYAML,
		SwitchoverYAML: dynamicSwitchoverYAML,
		GatewayYAML:    dynamicRouteGatewayYAML,
		Mode:           "dynamic",
	}
	require.NoError(t, orchestrator.Execute(context.Background(), res, 0, 0, restCreds.Authenticator()))

	// Direct regression proof: Initialize itself persisted Mode. Without the fix
	// this reads "" and the assertion fails here.
	cfgAfterRun1 := persistedConfig(t, f)
	require.Equal(t, "dynamic", cfgAfterRun1.Mode,
		"TBMActions.Initialize must persist config.Mode to the state file")
	require.Equal(t, migration.StateSwitched, cfgAfterRun1.CurrentState)

	// Run 2: a fresh command invocation resumes from the file run 1 wrote.
	// CurrentState != StateUninitialized, so the dispatcher never re-runs
	// reconcile — it reads mode := config.Mode and must route to runTBMBranch.
	// With the fix (Mode "dynamic") the TBM branch sees no pending work and
	// returns cleanly WITHOUT dialing anything. Without the fix (Mode ""), the
	// dispatch falls to the AAO branch, whose executor dials the fixture's
	// unreachable source cluster and errors — it can never reach this clean
	// short-circuit. NoError is therefore proof the run routed to the TBM branch.
	out, err := runExecuteWithTBMDeps(t, stubOffsetProviders, stubGatewayService, stubClusterLinkService,
		"--migration-yaml", f.manifestPath, "--migration-state-file", f.stateFile)
	require.NoError(t, err, "run 2 must route to the TBM branch on the persisted Mode, not the AAO branch")
	assert.Contains(t, out, "already complete")
}

// --- Test 2: --promote-batch-size reaches TBMActions.SetPromoteBatchSize ---

// recordingClusterLinkService is stubClusterLinkServiceImpl plus a record of
// the batch size passed to each PromoteMirrorTopics call, so a test can observe
// how many topics the promote transition submitted per batch — the only
// externally visible effect of TBMActions.SetPromoteBatchSize.
type recordingClusterLinkService struct {
	stubClusterLinkServiceImpl
	mu      sync.Mutex
	batches []int
}

func (r *recordingClusterLinkService) PromoteMirrorTopics(_ context.Context, _ clusterlink.Config, topicNames []string) (*clusterlink.PromoteMirrorTopicsResponse, error) {
	r.mu.Lock()
	r.batches = append(r.batches, len(topicNames))
	r.mu.Unlock()
	return promoteAll(topicNames), nil
}

func (r *recordingClusterLinkService) maxBatch() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	max := 0
	for _, n := range r.batches {
		if n > max {
			max = n
		}
	}
	return max
}

// TestExecute_DynamicMode_PromoteBatchSizeReachesTBMActions confirms Decision 5:
// --promote-batch-size, previously AAO-only, now reaches
// TBMActions.SetPromoteBatchSize for a dynamic-mode run. Two topics are staged
// at zero lag; the recorded per-call batch sizes discriminate a capped run
// (batch size 1 → each PromoteMirrorTopics call submits exactly one topic) from
// an uncapped one (both promoted in a single call). Resuming at StateLagsOk
// walks fence → verify_fence → promote → switch, exercising the real promote
// batching.
func TestExecute_DynamicMode_PromoteBatchSizeReachesTBMActions(t *testing.T) {
	topics := []string{"t1.order", "t2.inventory"}

	t.Run("flag caps the batch", func(t *testing.T) {
		f := newFixture(t, nil)
		f.writeDynamicState(t, migration.StateLagsOk, func(c *migration.MigrationConfig) {
			c.Topics = topics
		})
		rec := &recordingClusterLinkService{}
		_, err := runExecuteWithTBMDeps(t, stubOffsetProviders, stubGatewayService,
			func(*manifest.GatewayMigration) (clusterlink.Service, error) { return rec, nil },
			"--migration-yaml", f.manifestPath, "--migration-state-file", f.stateFile,
			"--promote-batch-size", "1")
		require.NoError(t, err)
		assert.Equal(t, migration.StateSwitched, persistedConfig(t, f).CurrentState)
		assert.Equal(t, 1, rec.maxBatch(),
			"--promote-batch-size 1 must cap every promote batch at one topic")
	})

	t.Run("unlimited promotes all at once (control)", func(t *testing.T) {
		f := newFixture(t, nil)
		f.writeDynamicState(t, migration.StateLagsOk, func(c *migration.MigrationConfig) {
			c.Topics = topics
		})
		rec := &recordingClusterLinkService{}
		_, err := runExecuteWithTBMDeps(t, stubOffsetProviders, stubGatewayService,
			func(*manifest.GatewayMigration) (clusterlink.Service, error) { return rec, nil },
			"--migration-yaml", f.manifestPath, "--migration-state-file", f.stateFile)
		require.NoError(t, err)
		assert.Equal(t, migration.StateSwitched, persistedConfig(t, f).CurrentState)
		assert.Equal(t, len(topics), rec.maxBatch(),
			"with no batch cap both topics promote in a single call — proving the flag, not chance, produced the capped run above")
	})
}

// --- Test 3: a dynamic run records LastRunPolicies ---

// TestExecute_DynamicMode_RecordsLastRunPolicies confirms Decision 4: a
// dynamic-mode run populates config.LastRunPolicies with its five supported
// fields (read from the effective policy — the manifest's spec.defaultPolicies,
// no flags here — the same source TBMActions reads, so this also pins the
// AAO-parity policy plumbing), while ConsumerOffsetSyncDrainDuration stays zero
// because TBM has no offset-sync-pause stage to record a value for. Resuming at
// StatePromoted leaves only the switch step, so the record lands without paying
// promote's poll waits, and without executing the (recorded) unrouted-producer
// window.
func TestExecute_DynamicMode_RecordsLastRunPolicies(t *testing.T) {
	f := newFixture(t, func(doc string) string {
		return doc + "  defaultPolicies:\n" +
			"    lagThreshold: 42\n" +
			"    promoteBatchSize: 7\n" +
			"    rolloutTimeout: 3m\n" +
			"    detectUnroutedProducersDuration: 30s\n" +
			"    hotReloadTimeout: 45s\n" +
			"    gatewayConfigPort: 9090\n"
	})
	f.writeDynamicState(t, migration.StatePromoted, nil)

	_, err := runExecuteWithTBMDeps(t, stubOffsetProviders, stubGatewayService, stubClusterLinkService,
		"--migration-yaml", f.manifestPath, "--migration-state-file", f.stateFile)
	require.NoError(t, err)

	cfg := persistedConfig(t, f)
	require.Equal(t, migration.StateSwitched, cfg.CurrentState)
	rec := cfg.LastRunPolicies
	require.NotNil(t, rec, "a dynamic-mode run must record the effective policy")
	assert.Equal(t, 42, rec.LagThreshold)
	assert.Equal(t, 7, rec.PromoteBatchSize, "the manifest default reaches the record with no flag set — AAO-parity policy plumbing")
	assert.Equal(t, 3*time.Minute, rec.RolloutTimeout)
	assert.Equal(t, 30*time.Second, rec.DetectUnroutedProducersDuration)
	assert.Equal(t, 45*time.Second, rec.HotReloadTimeout)
	assert.Equal(t, 9090, rec.GatewayConfigPort)
	assert.Equal(t, time.Duration(0), rec.ConsumerOffsetSyncDrainDuration,
		"TBM has no offset-sync-pause stage, so this field stays zero")
}

// --- Test 4: pauseConsumerOffsetSync on a dynamic route warns, never refuses ---

// TestExecute_DynamicMode_PauseOffsetSyncSet_ProceedsWithoutRefusing confirms
// the "proceeds" half of Decision 6: a dynamic-mode migration whose manifest
// carries spec.clusterLink.pauseConsumerOffsetSync: true is NOT refused — it
// completes the run. (The warning that accompanies it fires only on the
// StateUninitialized reconcile path, which needs live infrastructure this
// process does not have; the warning decision itself is unit-tested below in
// TestPauseOffsetSyncIgnoredForDynamic.) The config must carry the same flag as
// the manifest, or detectDrift would refuse first for an unrelated reason.
func TestExecute_DynamicMode_PauseOffsetSyncSet_ProceedsWithoutRefusing(t *testing.T) {
	f := newFixture(t, func(doc string) string {
		return strings.Replace(doc, "    name: msk-to-cc\n", "    name: msk-to-cc\n    pauseConsumerOffsetSync: true\n", 1)
	})
	f.writeDynamicState(t, migration.StatePromoted, func(c *migration.MigrationConfig) {
		c.PauseConsumerOffsetSync = true
	})

	out, err := runExecuteWithTBMDeps(t, stubOffsetProviders, stubGatewayService, stubClusterLinkService,
		"--migration-yaml", f.manifestPath, "--migration-state-file", f.stateFile)
	require.NoError(t, err, "a dynamic route with pauseConsumerOffsetSync set must proceed, not refuse")
	assert.Contains(t, out, "Migration completed")
	assert.Equal(t, migration.StateSwitched, persistedConfig(t, f).CurrentState)
}

// --- Test 5: --gateway-config-port reaches the TBM capability probe ---

// recordingGatewayService is stubGatewayServiceImpl plus a record of the port
// passed to DetectCapability — the first place a gateway transition reads
// config.GatewayConfigPort (see tbm.resolveGatewayCapability). Observing it here
// proves the override reached config.GatewayConfigPort BEFORE the capability
// probe ran.
type recordingGatewayService struct {
	stubGatewayServiceImpl
	mu       sync.Mutex
	seenPort int
}

func (r *recordingGatewayService) DetectCapability(_ context.Context, _ string, _ string, port int, _ []byte, _ []byte) (gateway.Capability, error) {
	r.mu.Lock()
	r.seenPort = port
	r.mu.Unlock()
	return gateway.Capability{Mode: gateway.VerifyRollout}, nil
}

func (r *recordingGatewayService) port() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.seenPort
}

// TestExecute_DynamicMode_GatewayConfigPortOverrideReachesTBM confirms Decision
// 7 end-to-end through the command: --gateway-config-port reaches
// config.GatewayConfigPort before tbm's capability probe (DetectCapability)
// runs. Resuming at StatePromoted leaves only the switch step, which calls
// ensureGatewayCapability → DetectCapability with config.GatewayConfigPort.
func TestExecute_DynamicMode_GatewayConfigPortOverrideReachesTBM(t *testing.T) {
	f := newFixture(t, nil)
	f.writeDynamicState(t, migration.StatePromoted, nil)

	rec := &recordingGatewayService{}
	_, err := runExecuteWithTBMDeps(t, stubOffsetProviders,
		func(*manifest.GatewayMigration) (gateway.Service, error) { return rec, nil },
		stubClusterLinkService,
		"--migration-yaml", f.manifestPath, "--migration-state-file", f.stateFile,
		"--gateway-config-port", "9999")
	require.NoError(t, err)
	assert.Equal(t, 9999, rec.port(),
		"--gateway-config-port must reach config.GatewayConfigPort before the TBM capability probe")
	assert.Equal(t, 9999, persistedConfig(t, f).GatewayConfigPort)
}

// --- Test 6: a dynamic-mode entry drift-checks via the shared mechanism ---

// TestExecute_DynamicModeEntry_DriftRefusesUnconditionally confirms pre-flight
// finding #1: a dynamic-mode entry drift-checks via the SAME
// detectDrift/checkSpecDrift the static path uses (both run before the mode
// dispatch), with no separate mechanism. A manifest whose cluster-link name no
// longer matches the registered dynamic migration is refused with the same
// message TestExecute_DriftRefusesUnconditionallyAtEveryState asserts for static
// entries — and the injected TBM stubs are never reached, since the refusal
// precedes dispatch.
func TestExecute_DynamicModeEntry_DriftRefusesUnconditionally(t *testing.T) {
	f := newFixture(t, nil)
	f.writeDynamicState(t, migration.StateInitialized, func(c *migration.MigrationConfig) {
		c.ClusterLinkName = "changed-link"
	})

	unreachable := func(*manifest.GatewayMigration) (offset.Provider, offset.Provider, func() error, error) {
		t.Fatal("drift must refuse before any TBM service is built")
		return nil, nil, nil, nil
	}
	_, err := runExecuteWithTBMDeps(t, unreachable, stubGatewayService, stubClusterLinkService,
		"--migration-yaml", f.manifestPath, "--migration-state-file", f.stateFile)
	require.Error(t, err, "a dynamic-mode entry must refuse on drift, unconditionally")
	assert.Contains(t, err.Error(), "changed since")
	assert.Contains(t, err.Error(), "new metadata.name")

	assert.Equal(t, migration.StateInitialized, persistedConfig(t, f).CurrentState,
		"a refused run must not advance the migration")
}

// --- warning decision (the half of Decision 6 the command path cannot reach) ---

// TestPauseOffsetSyncIgnoredForDynamic pins the exact warn-not-refuse decision
// runMigrationExecute makes on the StateUninitialized reconcile path: a
// pauseConsumerOffsetSync manifest is a no-op warning for a dynamic route and
// silent for a static one (which honors the field). That branch is unreachable
// through the command in-process (it needs a live migplan.Reconcile), so the
// decision is factored into pauseOffsetSyncIgnoredForDynamic and asserted
// directly here.
func TestPauseOffsetSyncIgnoredForDynamic(t *testing.T) {
	set := &manifest.GatewayMigration{}
	set.Spec.ClusterLink.PauseConsumerOffsetSync = true
	unset := &manifest.GatewayMigration{}

	assert.True(t, pauseOffsetSyncIgnoredForDynamic("dynamic", set),
		"a dynamic route with pauseConsumerOffsetSync set must warn (the field is ignored)")
	assert.False(t, pauseOffsetSyncIgnoredForDynamic("dynamic", unset),
		"nothing to warn about when the field is unset")
	assert.False(t, pauseOffsetSyncIgnoredForDynamic("static", set),
		"a static route honors pauseConsumerOffsetSync — no warning")
}
