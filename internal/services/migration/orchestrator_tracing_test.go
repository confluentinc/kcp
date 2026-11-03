package migration

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/confluentinc/kcp/internal/services/clusterlink"
	"github.com/confluentinc/kcp/internal/services/migration/killpoint"
	"github.com/confluentinc/kcp/internal/tracing/tracingtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/codes"
)

var testAuth = clusterlink.BasicAuth{Username: "api-key", Password: "api-secret"}

func TestTracing_Execute_StepSpansUnderTheRunSpan(t *testing.T) {
	rec := tracingtest.Install(t)
	orch, _ := newHappyPathOrchestrator(t, nil)
	require.NoError(t, orch.Execute(context.Background(), 0, testAuth, uninitializedReconcileResult(nil)))

	spans := tracingtest.Spans(rec)
	run := tracingtest.Find(t, spans, "migration.run")
	strategy, _ := tracingtest.Attr(run, "kcp.strategy")
	assert.Equal(t, "aao", strategy.AsString())
	assert.Equal(t, codes.Unset, run.Status().Code)

	assert.Equal(t, []string{
		"step.initialize", "step.wait_for_lags", "step.fence", "step.pause_offset_sync",
		"step.verify_fence", "step.promote", "step.switch", "step.restore_offset_sync",
	}, tracingtest.StepNames(tracingtest.Children(spans, run)))

	from, _ := tracingtest.Attr(tracingtest.Find(t, spans, "step.fence"), "kcp.step.from")
	assert.Equal(t, StateLagsOk, from.AsString())
	_, hasTopics := tracingtest.Attr(run, "kcp.topics.migrate")
	assert.True(t, hasTopics, "plan attributes are set once initialize has run")

	tracingtest.AssertNoValues(t, spans, "topic-a", "topic-b")
}

func TestTracing_Execute_RunSpanRecordsTheDetectionWindow(t *testing.T) {
	rec := tracingtest.Install(t)
	orch, config := newHappyPathOrchestrator(t, nil)
	config.DetectUnroutedProducersDuration = time.Millisecond
	require.NoError(t, orch.Execute(context.Background(), 0, testAuth, uninitializedReconcileResult(nil)))

	window, ok := tracingtest.Attr(tracingtest.Find(t, tracingtest.Spans(rec), "migration.run"), "kcp.detect_unrouted_producers_ms")
	require.True(t, ok, "the static run span records the detection window, as the dynamic one does")
	assert.Equal(t, int64(1), window.AsInt64())
}

func TestTracing_Execute_FailedStepIsAnError(t *testing.T) {
	rec := tracingtest.Install(t)
	t.Setenv(killpoint.FailEnvVar, EventWaitForLags)
	orch, _ := newHappyPathOrchestrator(t, nil)
	require.Error(t, orch.Execute(context.Background(), 0, testAuth, uninitializedReconcileResult(nil)))

	spans := tracingtest.Spans(rec)
	assert.Equal(t, codes.Error, tracingtest.Find(t, spans, "step.wait_for_lags").Status().Code)
	assert.Equal(t, codes.Error, tracingtest.Find(t, spans, "migration.run").Status().Code)
	assert.Empty(t, tracingtest.FindAll(spans, "step.fence"), "no step after the failure")
	assert.Empty(t, tracingtest.FindAll(spans, "migration.rollback"), "no fence was up, so nothing rolls back")
}

func TestTracing_Execute_RollbackIsASiblingOfTheFailedStep(t *testing.T) {
	rec := tracingtest.Install(t)
	orch, config := newHappyPathOrchestrator(t, nil)
	config.DetectUnroutedProducersDuration = time.Millisecond
	config.GatewayYAML = "apiVersion: platform.confluent.io/v1beta1\nkind: Gateway\nmetadata:\n  name: my-gateway\n  namespace: confluent\nspec:\n  routes:\n    - name: migration-route\n      endpoint: gateway:9595\n"
	var calls int64
	orch.actions.sourceOffset = &mockOffsetProvider{getFn: func(string) (map[int32]int64, error) {
		n := atomic.AddInt64(&calls, 1)
		return map[int32]int64{0: 100 + n*10}, nil
	}}
	orch.actions.destinationOffset = caughtUpDestination()
	var out strings.Builder
	orch.reporter = &reporter{out: &out, err: io.Discard}

	require.ErrorIs(t, orch.Execute(context.Background(), 0, testAuth, resultFromConfig(config)), ErrUnroutedProducers)

	spans := tracingtest.Spans(rec)
	run := tracingtest.Find(t, spans, "migration.run")
	rollback := tracingtest.Find(t, spans, "migration.rollback")
	assert.Equal(t, run.SpanContext().SpanID(), rollback.Parent().SpanID())
	path, _ := tracingtest.Attr(rollback, "kcp.rollback.path")
	assert.Equal(t, RollbackPathAbortFence, path.AsString())
	assert.Equal(t, codes.Unset, rollback.Status().Code, "the rollback itself succeeded")
	abort := tracingtest.Find(t, spans, "step.abort_fence")
	assert.Equal(t, rollback.SpanContext().SpanID(), abort.Parent().SpanID())
	assert.Equal(t, codes.Error, tracingtest.Find(t, spans, "step.verify_fence").Status().Code)
}

func TestTracing_Execute_RollbackIsAnErrorWhenTheOffsetSyncRestoreFails(t *testing.T) {
	rec := tracingtest.Install(t)
	orch, config := newHappyPathOrchestrator(t, nil)
	config.PauseConsumerOffsetSync = true
	config.DetectUnroutedProducersDuration = time.Millisecond
	config.GatewayYAML = "apiVersion: platform.confluent.io/v1beta1\nkind: Gateway\nmetadata:\n  name: my-gateway\n  namespace: confluent\nspec:\n  routes:\n    - name: migration-route\n      endpoint: gateway:9595\n"
	var listCalls int64
	orch.actions.clusterLinkService = &mockClusterLinkService{
		listConfigsFn: func(context.Context, clusterlink.Config) (map[string]string, error) {
			if atomic.AddInt64(&listCalls, 1) == 1 {
				return map[string]string{"consumer.offset.sync.enable": "true"}, nil
			}
			return map[string]string{"consumer.offset.sync.enable": "false"}, nil
		},
		alterConfigsFn: func(_ context.Context, _ clusterlink.Config, alts []clusterlink.ConfigAlteration) error {
			for _, a := range alts {
				if a.Value == "true" {
					return errors.New("restore refused")
				}
			}
			return nil
		},
	}
	var calls int64
	orch.actions.sourceOffset = &mockOffsetProvider{getFn: func(string) (map[int32]int64, error) {
		n := atomic.AddInt64(&calls, 1)
		return map[int32]int64{0: 100 + n*10}, nil
	}}
	orch.actions.destinationOffset = caughtUpDestination()
	orch.reporter = &reporter{out: io.Discard, err: io.Discard}

	require.ErrorIs(t, orch.Execute(context.Background(), 0, testAuth, resultFromConfig(config)), ErrUnroutedProducers)

	rollback := tracingtest.Find(t, tracingtest.Spans(rec), "migration.rollback")
	assert.Equal(t, codes.Error, rollback.Status().Code, "the unfence landed but the offset-sync restore did not")
}
