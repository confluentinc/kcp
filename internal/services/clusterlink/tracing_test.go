package clusterlink

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/confluentinc/kcp/internal/tracing/tracingtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTracing_PromoteMirrorTopics_IsASpanWithTheBatchSize(t *testing.T) {
	rec := tracingtest.Install(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"mirror_topic_name":"orders"},{"mirror_topic_name":"payments"}]}`))
	}))
	defer srv.Close()
	svc := NewConfluentCloudService(srv.Client())
	cfg := Config{RestEndpoint: srv.URL, ClusterID: "lkc-1", LinkName: "l", APIKey: "k", APISecret: "s"}

	_, err := svc.PromoteMirrorTopics(context.Background(), cfg, []string{"orders", "payments"})
	require.NoError(t, err)
	mirrors, err := svc.ListMirrorTopics(context.Background(), cfg)
	require.NoError(t, err)
	require.Len(t, mirrors, 2)

	spans := tracingtest.Spans(rec)
	size, _ := tracingtest.Attr(tracingtest.Find(t, spans, "clusterlink.promote_mirror_topics"), "kcp.batch.size")
	assert.Equal(t, int64(2), size.AsInt64())
	n, _ := tracingtest.Attr(tracingtest.Find(t, spans, "clusterlink.list_mirror_topics"), "kcp.mirrors")
	assert.Equal(t, int64(2), n.AsInt64())
	tracingtest.AssertNoValues(t, spans, "orders", "payments")
}
