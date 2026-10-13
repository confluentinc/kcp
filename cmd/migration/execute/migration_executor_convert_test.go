package execute

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/confluentinc/kcp/internal/manifest"
	"github.com/confluentinc/kcp/internal/services/gateway"
	"github.com/confluentinc/kcp/internal/services/groupoffsets"
	"github.com/confluentinc/kcp/internal/services/migplan"
	"github.com/confluentinc/kcp/internal/services/migplan/reconcile"
	"github.com/confluentinc/kcp/internal/services/migration/d2s"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This file exercises the conversion branch of `execute` the way migration_executor_dynamic_test.go
// exercises the dynamic one: runConvertBranch is driven directly with a hand-built *migplan.Result (a real
// migplan.Reconcile cannot succeed in this process), the gateway is stubbed through executorDependencies,
// and the conversion's two clusters are a fake world behind executorDependencies.convert.

// convertFixture is the canonical manifest as a route conversion: spec.route.convertTo: static in place of
// the topicGroup.
func convertFixture(t *testing.T) fixture {
	return newFixture(t, func(doc string) string {
		return strings.Replace(doc, "    topicGroup:\n      - topicPatterns:\n          - '.*'\n", "    convertTo: static\n", 1)
	})
}

// convertPlanResult is what a live migplan.Reconcile returns for a convertible route over
// dynamicRouteGatewayYAML.
func convertPlanResult() *migplan.Result {
	return &migplan.Result{
		Route:             "migration-route",
		Mode:              "convert",
		GatewayYAML:       dynamicRouteGatewayYAML,
		FenceYAML:         dynamicRollbackFenceYAML + "  fencing:\n    - topicPatterns: [\".*\"]\n      blocked: true\n",
		SwitchoverYAML:    "route:\n  name: migration-route\n  endpoint: kafka-gw.example.com:9092\n  streamingDomain:\n    name: confluent-cloud\n    bootstrapServerId: sasl-plain\n",
		RollbackFenceYAML: dynamicRollbackFenceYAML,
		RollbackAllowed:   true,
	}
}

// convertWorld is a conversion's two clusters, faked: one promoted link topic (orders, routed to the
// destination), the given source groups each committed at orders[0]=5 in both snapshots, and an empty
// destination with room for it. It records what reaches it.
type convertWorld struct {
	mu            sync.Mutex
	facts         *migplan.ConvertFacts
	groups        []string
	waits         []time.Duration
	fetcherBuilds int
	commits       map[string]groupoffsets.Offsets
}

func newConvertWorld(groups ...string) *convertWorld {
	rules := map[string]any{
		"routing": map[string]any{
			"coordination": map[string]any{"group": "source"},
			"default":      "source",
			"conditions":   []any{map[string]any{"topics": []any{"orders"}, "streamingDomain": "confluent-cloud"}},
		},
		"fencing": []any{map[string]any{"topicPatterns": []any{".*"}, "blocked": true}},
	}
	return &convertWorld{
		groups: groups,
		facts: &migplan.ConvertFacts{
			Gateway: &reconcile.GatewayConfig{Route: &reconcile.RouteConfig{
				Name: "migration-route", Mode: "dynamic", BoundDomains: []string{"source", "confluent-cloud"}, Rules: rules,
			}},
			SourceTopics: []string{"orders"},
			TargetTopics: []string{"orders"},
			Link: &migplan.LinkStatus{LinkMirrors: []reconcile.LinkMirror{
				{SourceTopic: "orders", MirrorTopic: "orders", State: reconcile.MirrorStopped, Status: "STOPPED"},
			}},
			SourceCanDescribeGroups: true,
			TargetCanDescribeGroups: true,
			SourceCanDescribeTopics: true,
			TargetCanDescribeTopics: true,
			TargetCanCommitOffsets:  true,
			Partitions:              reconcile.PartitionCounts{Source: map[string]int{"orders": 1}, Target: map[string]int{"orders": 1}},
		},
	}
}

// CommittedOffsets is the source fetcher (groupoffsets.GroupOffsetFetcher).
func (w *convertWorld) CommittedOffsets(string) (groupoffsets.Offsets, error) {
	return groupoffsets.Offsets{"orders": {0: {Offset: 5}}}, nil
}

// CommitGroupOffsets is the destination committer (groupoffsets.GroupCommitter).
func (w *convertWorld) CommitGroupOffsets(group string, offsets groupoffsets.Offsets) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.commits == nil {
		w.commits = map[string]groupoffsets.Offsets{}
	}
	w.commits[group] = offsets
	return nil
}

// GetMany is the destination high-water-mark sweep (offset.Provider).
func (w *convertWorld) GetMany(context.Context, []string) (map[string]map[int32]int64, error) {
	return map[string]map[int32]int64{"orders": {0: 10}}, nil
}

// services is executorDependencies.convert over this world.
func (w *convertWorld) services(g *manifest.GatewayMigration) (convertServices, error) {
	in, err := migplan.BuildReconcileInput(g)
	if err != nil {
		return convertServices{}, err
	}
	return convertServices{
		deps: d2s.Dependencies{
			Input:  in,
			Gather: func(context.Context) (*migplan.ConvertFacts, error) { return w.facts, nil },
			SourceGroups: func(context.Context) ([]string, error) {
				return append([]string(nil), w.groups...), nil
			},
			DestinationGroups: func(context.Context) ([]types.ConsumerGroupListing, error) { return nil, nil },
			Fetchers: func() (groupoffsets.GroupOffsetFetcher, func(), error) {
				w.mu.Lock()
				w.fetcherBuilds++
				w.mu.Unlock()
				return w, nil, nil
			},
			Wait: func(_ context.Context, d time.Duration) error {
				w.mu.Lock()
				w.waits = append(w.waits, d)
				w.mu.Unlock()
				return nil
			},
			DestinationTopics: func(context.Context) ([]string, error) { return []string{"orders"}, nil },
			HighWaterMarks:    w,
			Committers:        func() (groupoffsets.GroupCommitter, func(), error) { return w, nil, nil },
		},
		close: func() error { return nil },
	}, nil
}

// patchRecordingGateway is stubGatewayServiceImpl recording each route patch's Field.
type patchRecordingGateway struct {
	stubGatewayServiceImpl
	mu     sync.Mutex
	fields []string
}

func (r *patchRecordingGateway) PatchGatewayRoute(_ context.Context, _, _ string, rp gateway.RoutePatch, _ string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.fields = append(r.fields, rp.Field)
	return "", nil
}

func (r *patchRecordingGateway) patched() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.fields...)
}

func convertDeps(gw gateway.Service, w *convertWorld) executorDependencies {
	d := stubDeps(gw, nil)
	d.convert = w.services
	return d
}

// runConvertBranchFor drives runConvertBranch for f's manifest with convertPlanResult standing in for the live
// reconcile. editGateway, when set, edits the loaded manifest first (the stand-in for a flag override).
func runConvertBranchFor(t *testing.T, f fixture, editGateway func(*manifest.GatewayMigration), deps executorDependencies) (string, error) {
	t.Helper()
	color.NoColor = true
	g := loadGateway(t, f.manifestPath)
	if editGateway != nil {
		editGateway(g)
	}
	config := buildFreshMigrationConfig(g, "msk-prod-to-cc-batch-1")
	var out bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&out)
	err := runConvertBranch(cmd, g, &config, convertPlanResult(), deps)
	return out.String(), err
}

func TestConvertFixture_IsAConversionManifest(t *testing.T) {
	g := loadGateway(t, convertFixture(t).manifestPath)
	assert.Equal(t, manifest.RouteConvertToStatic, g.Spec.Route.ConvertTo)
	assert.Nil(t, g.Spec.Route.TopicGroup)
}

func TestExecute_ConvertMode_RunsToCompletionOnStubbedServices(t *testing.T) {
	w := newConvertWorld("orders-app")
	gw := &patchRecordingGateway{}

	out, err := runConvertBranchFor(t, convertFixture(t), nil, convertDeps(gw, w))

	require.NoError(t, err)
	assert.Contains(t, out, "✅ Route conversion completed: msk-prod-to-cc-batch-1")
	assert.Equal(t, map[string]groupoffsets.Offsets{"orders-app": {"orders": {0: {Offset: 5}}}}, w.commits)
	assert.Equal(t, []string{"rules", ""}, gw.patched(), "the fence (a rules patch), then the switch (a whole-route replace)")
}

func TestExecute_ConvertMode_PoliciesReachTheStateMachine(t *testing.T) {
	groups := []string{"g1", "g2", "g3", "g4"}
	t.Run("manifest values", func(t *testing.T) {
		w := newConvertWorld(groups...)
		edit := func(g *manifest.GatewayMigration) {
			g.Spec.DefaultPolicies.DetectUnroutedCommitsDuration = 45 * time.Second
			g.Spec.DefaultPolicies.OffsetSyncConcurrency = 3
		}

		_, err := runConvertBranchFor(t, convertFixture(t), edit, convertDeps(&patchRecordingGateway{}, w))

		require.NoError(t, err)
		assert.Equal(t, []time.Duration{45 * time.Second}, w.waits, "detectUnroutedCommitsDuration is the wait between the snapshots")
		assert.Equal(t, 6, w.fetcherBuilds, "three workers for each of the two snapshots")
	})
	t.Run("built-in defaults", func(t *testing.T) {
		w := newConvertWorld(groups...)

		_, err := runConvertBranchFor(t, convertFixture(t), nil, convertDeps(&patchRecordingGateway{}, w))

		require.NoError(t, err)
		assert.Equal(t, []time.Duration{manifest.DefaultDetectUnroutedCommitsDuration}, w.waits)
		assert.Equal(t, 8, w.fetcherBuilds, "the default 8 workers, capped at the 4 groups, for each snapshot")
	})
}

func TestExecute_ConvertMode_AVerifyRefusalRemovesTheFenceAndFails(t *testing.T) {
	w := newConvertWorld("orders-app")
	w.facts.TargetCanCommitOffsets = false
	gw := &patchRecordingGateway{}

	out, err := runConvertBranchFor(t, convertFixture(t), nil, convertDeps(gw, w))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to execute route conversion")
	assert.Contains(t, err.Error(), "destination credential can write consumer-group offsets")
	assert.Contains(t, out, "✗ destination credential can write consumer-group offsets", "the refusal is rendered to the command's output")
	assert.NotContains(t, out, "Route conversion completed")
	assert.Equal(t, []string{"rules", "rules"}, gw.patched(), "the fence, then the rollback")
	assert.Empty(t, w.commits)
}

func TestBuildConvertServices_RefusesATopicMigrationManifest(t *testing.T) {
	g := loadGateway(t, newFixture(t, nil).manifestPath)

	_, err := buildConvertServices(g)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "convertTo")
}

// TestExecute_ConvertMode_AServicesBuildFailureNamesTheConversionServices — building the conversion's
// services is more than a connection (the reconcile input, the gather, the fetchers), so its failure is
// worded as that, not as a cluster connection failure.
func TestExecute_ConvertMode_AServicesBuildFailureNamesTheConversionServices(t *testing.T) {
	deps := stubDeps(&patchRecordingGateway{}, nil)
	deps.convert = func(*manifest.GatewayMigration) (convertServices, error) {
		return convertServices{}, errors.New("boom")
	}

	_, err := runConvertBranchFor(t, convertFixture(t), nil, deps)

	require.Error(t, err)
	assert.Equal(t, "failed to build route-conversion services: boom", err.Error())
}
