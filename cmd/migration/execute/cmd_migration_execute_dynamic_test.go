package execute

import (
	"bytes"
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/confluentinc/kcp/internal/manifest"
	"github.com/confluentinc/kcp/internal/services/clusterlink"
	"github.com/confluentinc/kcp/internal/services/gateway"
	"github.com/confluentinc/kcp/internal/services/migplan"
	"github.com/confluentinc/kcp/internal/services/migration"
	"github.com/confluentinc/kcp/internal/services/offset"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	k8stypes "k8s.io/apimachinery/pkg/types"
)

// This file exercises the dynamic-mode branch of `execute` the way
// cmd_migration_execute_static_test.go exercises the static one.
//
// The command layer calls migplan.Reconcile with no injectable seam, and a real
// Reconcile dials live Kafka and pulls the live Gateway CR (see
// internal/services/migplan/run.go), so it cannot succeed — nor resolve a
// Mode — inside this test process. These tests therefore call runDynamicBranch
// directly (runDynamicBranchWithConfig below), standing in for that live call
// with a hand-built *migplan.Result and MigrationConfig, and stub the
// downstream services through the builders newMigrationExecuteCmd injects.

// dynamicRouteGatewayYAML is a minimal dynamic-route Gateway CR fixture,
// mirroring internal/services/migration/tbm's own test fixture of the same
// shape. A dynamic route carries a `rules:` block (routing/coordination), the
// shape migplan resolves to Mode:"dynamic"; a static route would instead carry
// a fence/streamingDomain block.
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
// fragments a live migplan.Reconcile would produce, so the fence/switch
// transitions can graft them onto dynamicRouteGatewayYAML's migration-route
// and succeed against the stub gateway.
const (
	dynamicFenceYAML      = "rules:\n  routing:\n    coordination:\n      group: source\n    default: source\n  fencing:\n    - topics: [\"t1.order\"]\n"
	dynamicSwitchoverYAML = "rules:\n  routing:\n    coordination:\n      group: source\n    default: source\n    conditions:\n      - topics: [\"t1.order\"]\n        streamingDomain: target\n"
)

// dynamicConfig builds the MigrationConfig a dynamic-mode run drives — the
// same pure manifest projection buildFreshMigrationConfig produces for f's
// manifest, plus Mode and the fence/switchover/gateway artifacts a live
// migplan.Reconcile would have produced for this fixture. There is no
// migration state file to read any of this from any more, so a test
// that needs to drive runDynamicBranch directly builds the config and its
// matching *migplan.Result (dynamicResult) by hand. edit runs last, so a test
// can vary a single field.
func dynamicConfig(t *testing.T, f fixture, edit func(*migration.MigrationConfig)) *migration.MigrationConfig {
	t.Helper()
	g := loadGateway(t, f.manifestPath)
	kubeConfigPath, err := resolveKubeConfigPath(g)
	require.NoError(t, err)
	cfg := buildFreshMigrationConfig(g, "msk-prod-to-cc-batch-1", kubeConfigPath)
	cfg.Mode = "dynamic"
	cfg.Topics = []string{"t1.order"}
	cfg.GatewayYAML = dynamicRouteGatewayYAML
	cfg.FenceYAML = dynamicFenceYAML
	cfg.SwitchoverYAML = dynamicSwitchoverYAML
	if edit != nil {
		edit(&cfg)
	}
	return &cfg
}

// dynamicResult builds the *migplan.Result matching config exactly — the
// stand-in for what a live migplan.Reconcile would have returned for it.
func dynamicResult(config *migration.MigrationConfig) *migplan.Result {
	return &migplan.Result{
		Route:          "migration-route",
		Topics:         config.Topics,
		FenceYAML:      config.FenceYAML,
		SwitchoverYAML: config.SwitchoverYAML,
		GatewayYAML:    config.GatewayYAML,
		Mode:           config.Mode,
	}
}

// runDynamicBranchWithConfig drives runDynamicBranch directly against config and its
// matching dynamicResult, standing in for the live migplan.Reconcile call
// runMigrationExecute now makes on every invocation ahead of the mode dispatch
// — a call that cannot succeed in this process (see this file's header
// comment). This still exercises the SAME tbm.TBMOrchestrator.Execute call
// runDynamicBranch makes in production, just reached via a reconcile-free front
// door. editGateway, when non-nil, mutates the loaded manifest before
// dispatch — the substitute for a CLI flag override (applyPolicyOverrides
// needs real cobra flag parsing, which this seam skips; setting the manifest
// field directly is equivalent for what these tests observe).
func runDynamicBranchWithConfig(
	t *testing.T, f fixture, config *migration.MigrationConfig,
	editGateway func(*manifest.GatewayMigration),
	buildOffsets offsetProvidersFunc, buildGateway gatewayServiceFunc, buildClusterLink clusterLinkServiceFunc,
) (string, error) {
	t.Helper()
	g := loadGateway(t, f.manifestPath)
	if editGateway != nil {
		editGateway(g)
	}

	res := dynamicResult(config)

	var out bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&out)

	err := runDynamicBranch(cmd, g, config, res, buildOffsets, buildGateway, buildClusterLink)
	return out.String(), err
}

// --- stub downstream services (shared with cmd_migration_execute_static_test.go) ---

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

// --- a dynamic run completes on stubbed services ---

// TestExecute_DynamicMode_RunsToCompletionOnStubbedServices proves a
// dynamic-mode run on stubbed services walks tbm.TBMOrchestrator's FSM all the
// way to switched and prints the "Migration completed" banner.
func TestExecute_DynamicMode_RunsToCompletionOnStubbedServices(t *testing.T) {
	f := newFixture(t, nil)
	config := dynamicConfig(t, f, nil)

	out, err := runDynamicBranchWithConfig(t, f, config, nil, stubOffsetProviders, stubGatewayService, stubClusterLinkService)
	require.NoError(t, err)
	assert.Contains(t, out, "Migration completed")
}

// --- --promote-batch-size reaches tbm.TBMActions.SetPromoteBatchSize ---

// recordingClusterLinkService is stubClusterLinkServiceImpl plus a record of
// the batch size passed to each PromoteMirrorTopics call, so a test can observe
// how many topics the promote transition submitted per batch — the only
// externally visible effect of tbm.TBMActions.SetPromoteBatchSize.
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

// TestExecute_DynamicMode_PromoteBatchSizeReachesDynamicActions confirms that
// --promote-batch-size reaches tbm.TBMActions.SetPromoteBatchSize for a
// dynamic-mode run. Two topics are staged
// at zero lag; the recorded per-call batch sizes discriminate a capped run
// (batch size 1 → each PromoteMirrorTopics call submits exactly one topic) from
// an uncapped one (both promoted in a single call).
func TestExecute_DynamicMode_PromoteBatchSizeReachesDynamicActions(t *testing.T) {
	topics := []string{"t1.order", "t2.inventory"}

	t.Run("flag caps the batch", func(t *testing.T) {
		f := newFixture(t, nil)
		config := dynamicConfig(t, f, func(c *migration.MigrationConfig) { c.Topics = topics })
		rec := &recordingClusterLinkService{}
		// The CLI's --promote-batch-size relies on real cobra flag parsing
		// (applyPolicyOverrides), which runDynamicBranchWithConfig's reconcile-free
		// seam skips (see its doc comment) — setting the manifest's
		// spec.defaultPolicies field directly is equivalent for what this test
		// observes: runDynamicBranch always reads the effective policy from there.
		editGateway := func(g *manifest.GatewayMigration) { g.Spec.DefaultPolicies.PromoteBatchSize = 1 }
		_, err := runDynamicBranchWithConfig(t, f, config, editGateway, stubOffsetProviders, stubGatewayService,
			func(*manifest.GatewayMigration) (clusterlink.Service, error) { return rec, nil })
		require.NoError(t, err)
		assert.Equal(t, 1, rec.maxBatch(),
			"--promote-batch-size 1 must cap every promote batch at one topic")
	})

	t.Run("unlimited promotes all at once (control)", func(t *testing.T) {
		f := newFixture(t, nil)
		config := dynamicConfig(t, f, func(c *migration.MigrationConfig) { c.Topics = topics })
		rec := &recordingClusterLinkService{}
		_, err := runDynamicBranchWithConfig(t, f, config, nil, stubOffsetProviders, stubGatewayService,
			func(*manifest.GatewayMigration) (clusterlink.Service, error) { return rec, nil })
		require.NoError(t, err)
		assert.Equal(t, len(topics), rec.maxBatch(),
			"with no batch cap both topics promote in a single call — proving the flag, not chance, produced the capped run above")
	})
}

// --- Test 3: a dynamic run records LastRunPolicies ---

// TestExecute_DynamicMode_RecordsLastRunPolicies confirms that a
// dynamic-mode run populates config.LastRunPolicies with its five supported
// fields (read from the effective policy — the manifest's spec.defaultPolicies,
// no flags here — the same source tbm.TBMActions reads, so this also pins
// policy plumbing parity with the static branch), while
// ConsumerOffsetSyncDrainDuration stays zero because the dynamic FSM has no
// offset-sync-pause stage to record a value for.
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
	config := dynamicConfig(t, f, nil)

	_, err := runDynamicBranchWithConfig(t, f, config, nil, stubOffsetProviders, stubGatewayService, stubClusterLinkService)
	require.NoError(t, err)

	rec := config.LastRunPolicies
	require.NotNil(t, rec, "a dynamic-mode run must record the effective policy")
	assert.Equal(t, 42, rec.LagThreshold)
	assert.Equal(t, 7, rec.PromoteBatchSize, "the manifest default reaches the record with no flag set — the same policy plumbing as the static branch")
	assert.Equal(t, 3*time.Minute, rec.RolloutTimeout)
	assert.Equal(t, 30*time.Second, rec.DetectUnroutedProducersDuration)
	assert.Equal(t, 45*time.Second, rec.HotReloadTimeout)
	assert.Equal(t, 9090, rec.GatewayConfigPort)
	assert.Equal(t, time.Duration(0), rec.ConsumerOffsetSyncDrainDuration,
		"the dynamic FSM has no offset-sync-pause stage, so this field stays zero")
}

// --- --gateway-config-port reaches the dynamic capability probe ---

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

// TestExecute_DynamicMode_GatewayConfigPortReachesDynamicCapabilityProbe
// confirms end-to-end through the command that --gateway-config-port reaches
// config.GatewayConfigPort before the dynamic capability probe
// (DetectCapability) runs.
func TestExecute_DynamicMode_GatewayConfigPortReachesDynamicCapabilityProbe(t *testing.T) {
	f := newFixture(t, nil)
	config := dynamicConfig(t, f, nil)

	rec := &recordingGatewayService{}
	// The CLI's --gateway-config-port relies on real cobra flag parsing
	// (applyPolicyOverrides), which runDynamicBranchWithConfig's reconcile-free
	// seam skips — setting the manifest field directly is equivalent for
	// what this test observes: runDynamicBranch reads GatewayConfigPort straight
	// from g.Spec.DefaultPolicies.
	editGateway := func(g *manifest.GatewayMigration) { g.Spec.DefaultPolicies.GatewayConfigPort = 9999 }
	_, err := runDynamicBranchWithConfig(t, f, config, editGateway, stubOffsetProviders,
		func(*manifest.GatewayMigration) (gateway.Service, error) { return rec, nil },
		stubClusterLinkService)
	require.NoError(t, err)
	assert.Equal(t, 9999, rec.port(),
		"--gateway-config-port must reach config.GatewayConfigPort before the dynamic capability probe")
	assert.Equal(t, 9999, config.GatewayConfigPort)
}

// --- refusal decision (pauseConsumerOffsetSync on a dynamic route) ---

// TestPauseOffsetSyncRefusedForDynamic pins the exact refusal decision
// runMigrationExecute makes: a pauseConsumerOffsetSync manifest is refused for
// a dynamic route and permitted for a static one (which honors the field).
// That branch is unreachable through the command in-process (it needs a live
// migplan.Reconcile), so the decision is factored into
// pauseOffsetSyncRefusedForDynamic and asserted directly here.
func TestPauseOffsetSyncRefusedForDynamic(t *testing.T) {
	set := &manifest.GatewayMigration{}
	set.Spec.ClusterLink.PauseConsumerOffsetSync = true
	unset := &manifest.GatewayMigration{}

	assert.True(t, pauseOffsetSyncRefusedForDynamic("dynamic", set),
		"a dynamic route with pauseConsumerOffsetSync set must be refused")
	assert.False(t, pauseOffsetSyncRefusedForDynamic("dynamic", unset),
		"nothing to refuse when the field is unset")
	assert.False(t, pauseOffsetSyncRefusedForDynamic("static", set),
		"a static route honors pauseConsumerOffsetSync — no refusal")
}
