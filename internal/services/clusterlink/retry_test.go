package clusterlink

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/confluentinc/kcp/internal/retry/retrytest"
	"github.com/confluentinc/kcp/internal/tracing/tracingtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
)

// resetConnection drops the request's connection with a TCP reset, the way a cluster does in a connection storm.
func resetConnection(t *testing.T, w http.ResponseWriter) {
	t.Helper()
	conn, _, err := w.(http.Hijacker).Hijack()
	if err != nil {
		t.Errorf("hijacking the connection: %v", err)
		return
	}
	if tc, ok := conn.(*net.TCPConn); ok {
		_ = tc.SetLinger(0)
	}
	_ = conn.Close()
}

// writeMirrors answers GET .../mirrors with the given status per mirror topic, in name order.
func writeMirrors(t *testing.T, w http.ResponseWriter, status map[string]string) {
	t.Helper()
	names := make([]string, 0, len(status))
	for name := range status {
		names = append(names, name)
	}
	sort.Strings(names)
	var resp struct {
		Data []MirrorTopic `json:"data"`
	}
	for _, name := range names {
		resp.Data = append(resp.Data, MirrorTopic{MirrorTopicName: name, MirrorStatus: status[name]})
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		t.Errorf("writing mirrors: %v", err)
	}
}

func testConfig(url string) Config {
	return Config{RestEndpoint: url, ClusterID: "lkc-1", LinkName: "link-1", APIKey: "key", APISecret: "secret"}
}

func TestListMirrorTopics_RetriesAResetConnection(t *testing.T) {
	retrytest.Fast(t)
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) == 1 {
			resetConnection(t, w)
			return
		}
		writeMirrors(t, w, map[string]string{"orders": MirrorStatusActive})
	}))
	defer server.Close()

	mirrors, err := NewConfluentCloudService(server.Client()).ListMirrorTopics(context.Background(), testConfig(server.URL))

	require.NoError(t, err)
	require.Len(t, mirrors, 1)
	assert.Equal(t, int32(2), hits.Load())
}

func TestListMirrorTopics_Retries503(t *testing.T) {
	retrytest.Fast(t)
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		writeMirrors(t, w, map[string]string{"orders": MirrorStatusActive})
	}))
	defer server.Close()

	_, err := NewConfluentCloudService(server.Client()).ListMirrorTopics(context.Background(), testConfig(server.URL))

	require.NoError(t, err)
	assert.Equal(t, int32(2), hits.Load())
}

func TestGetClusterLink_DoesNotRetry404(t *testing.T) {
	retrytest.Fast(t)
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	_, err := NewConfluentCloudService(server.Client()).GetClusterLink(context.Background(), testConfig(server.URL))

	require.ErrorIs(t, err, ErrLinkNotFound)
	assert.Equal(t, int32(1), hits.Load())
}

func TestListMirrorTopics_RetriesARequestThatTimesOut(t *testing.T) {
	retrytest.Fast(t)
	var hits atomic.Int32
	release := make(chan struct{})
	defer close(release)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) == 1 {
			select {
			case <-release:
			case <-r.Context().Done():
			}
			return
		}
		writeMirrors(t, w, map[string]string{"orders": MirrorStatusActive})
	}))
	defer server.Close()
	client := server.Client()
	client.Timeout = 50 * time.Millisecond

	_, err := NewConfluentCloudService(client).ListMirrorTopics(context.Background(), testConfig(server.URL))

	require.NoError(t, err)
	assert.Equal(t, int32(2), hits.Load())
}

func TestAlterConfigs_RetriesAResetConnection(t *testing.T) {
	retrytest.Fast(t)
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) == 1 {
			resetConnection(t, w)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	err := NewConfluentCloudService(server.Client()).AlterConfigs(context.Background(), testConfig(server.URL),
		[]ConfigAlteration{{Name: "consumer.offset.sync.enable", Value: "false", Operation: OperationSet}})

	require.NoError(t, err)
	assert.Equal(t, int32(2), hits.Load())
}

func TestEndpointOf_ReplacesNames(t *testing.T) {
	for path, want := range map[string]string{
		"/kafka/v3/clusters/lkc-1/links/link-1":                                     "/kafka/v3/clusters/{cluster}/links/{link}",
		"/kafka/v3/clusters/lkc-1/links/link-1/mirrors":                             "/kafka/v3/clusters/{cluster}/links/{link}/mirrors",
		"/kafka/v3/clusters/lkc-1/links/link-1/mirrors/orders":                      "/kafka/v3/clusters/{cluster}/links/{link}/mirrors/{mirror}",
		"/kafka/v3/clusters/lkc-1/links/link-1/mirrors:promote":                     "/kafka/v3/clusters/{cluster}/links/{link}/mirrors:promote",
		"/kafka/v3/clusters/lkc-1/links/link-1/configs/consumer.offset.sync.enable": "/kafka/v3/clusters/{cluster}/links/{link}/configs/consumer.offset.sync.enable",
		"/kafka/v3/clusters/lkc-1/topics/orders/partitions":                         "/kafka/v3/clusters/{cluster}/topics/{topic}/partitions",
	} {
		assert.Equal(t, want, endpointOf(path), path)
	}
}

func TestListMirrorTopics_RecordsTheRetryWithTheEndpoint(t *testing.T) {
	rec := tracingtest.Install(t)
	retrytest.Fast(t)
	var mu sync.Mutex
	first := true
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if first {
			first = false
			resetConnection(t, w)
			return
		}
		writeMirrors(t, w, map[string]string{"orders": MirrorStatusActive})
	}))
	defer server.Close()

	_, err := NewConfluentCloudService(server.Client()).ListMirrorTopics(context.Background(), testConfig(server.URL))
	require.NoError(t, err)

	span := tracingtest.Find(t, tracingtest.Spans(rec), "clusterlink.list_mirror_topics")
	var found bool
	for _, e := range span.Events() {
		if e.Name == "retry.attempt" {
			found = true
			assert.Contains(t, e.Attributes, attribute.String("kcp.retry.operation", "rest.get"))
			assert.Contains(t, e.Attributes, attribute.String("kcp.retry.endpoint", "/kafka/v3/clusters/{cluster}/links/{link}/mirrors"))
		}
	}
	assert.True(t, found, "the retry is recorded on the list span")
}
