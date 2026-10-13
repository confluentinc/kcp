package migplan

import (
	"context"
	"testing"

	"github.com/confluentinc/kcp/internal/services/migplan/reconcile"
	"github.com/confluentinc/kcp/internal/tracing/tracingtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTracing_EngineRun_SpansEachRead(t *testing.T) {
	rec := tracingtest.Install(t)
	engine := NewReconciliationEngine(
		&fakeGateway{gw: dynGatewayConfig()},
		&fakeLister{topics: []string{"orders"}},
		&fakeLister{topics: []string{"orders"}},
		&fakeLink{ls: &LinkStatus{Mirrors: map[string]reconcile.MirrorState{"orders": reconcile.MirrorActive}}},
		&fakeSecretChecker{},
	)
	in := reconcile.ReconcileInput{Topics: []string{"orders"}, Route: "migration-route", TargetDomain: "cc"}
	_, err := engine.Run(context.Background(), in)
	require.NoError(t, err)

	spans := tracingtest.Spans(rec)
	names := tracingtest.Names(spans)
	for _, want := range []string{"migplan.load_gateway", "kafka.list_topics", "clusterlink.link_status", "kafka.cluster_id"} {
		assert.Contains(t, names, want)
	}
	clusters := map[string]bool{}
	for _, s := range tracingtest.FindAll(spans, "kafka.list_topics") {
		v, _ := tracingtest.Attr(s, "kcp.cluster")
		clusters[v.AsString()] = true
	}
	assert.Equal(t, map[string]bool{"source": true, "destination": true}, clusters)
	tracingtest.AssertNoValues(t, spans, "orders")
}
