package gateway

import (
	"context"
	"log/slog"
	"testing"

	"github.com/confluentinc/kcp/internal/testsupport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func attrValue(r slog.Record, key string) (slog.Value, bool) {
	var v slog.Value
	found := false
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == key {
			v = a.Value
			found = true
			return false
		}
		return true
	})
	return v, found
}

func TestGetGatewayYAML(t *testing.T) {
	const ns, gw = "confluent", "test-gateway"

	seededGateway := func() *unstructured.Unstructured {
		return &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": GatewayGroup + "/" + GatewayVersion,
			"kind":       GatewayKind,
			"metadata":   map[string]any{"name": gw, "namespace": ns},
			"spec":       map[string]any{"replicas": int64(3)},
		}}
	}

	t.Run("returns the served object as YAML and logs the fetch with an ms timing", func(t *testing.T) {
		h := testsupport.WithRecordingSlog(t)
		cs := newFakeDynamicClient(seededGateway())

		got, err := getGatewayYAML(context.Background(), cs, ns, gw)
		require.NoError(t, err)
		assert.Contains(t, string(got), gw, "returned YAML must marshal the served gateway object")

		rec, found := h.Find("fetched gateway CR")
		require.True(t, found, "a 'fetched gateway CR' Debug record must be emitted")
		assert.Equal(t, slog.LevelDebug, rec.Level)

		ms, hasMS := attrValue(rec, "ms")
		require.True(t, hasMS, "the fetch log must carry an ms attribute")
		assert.GreaterOrEqual(t, ms.Int64(), int64(0), "ms must be a non-negative integer")
	})

	t.Run("wraps a Get error and emits no success log", func(t *testing.T) {
		h := testsupport.WithRecordingSlog(t)
		cs := newFakeDynamicClient() // empty: Get returns NotFound

		_, err := getGatewayYAML(context.Background(), cs, ns, "missing-gateway")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get Gateway")

		_, found := h.Find("fetched gateway CR")
		assert.False(t, found, "no success log on a Get error")
	})
}
