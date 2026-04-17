package clusterlink

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Pure helper tests
// ---------------------------------------------------------------------------

func TestClassifyMirrorTopics_AllActive(t *testing.T) {
	mirrors := []MirrorTopic{
		{MirrorTopicName: "topic-a", MirrorStatus: "ACTIVE"},
		{MirrorTopicName: "topic-b", MirrorStatus: "ACTIVE"},
		{MirrorTopicName: "topic-c", MirrorStatus: "ACTIVE"},
	}

	topicNames, inactiveTopics := ClassifyMirrorTopics(mirrors)

	require.Len(t, topicNames, 3)
	for i, name := range []string{"topic-a", "topic-b", "topic-c"} {
		assert.Equal(t, name, topicNames[i], "topicNames[%d]", i)
	}
	assert.Empty(t, inactiveTopics)
}

func TestClassifyMirrorTopics_MixedStatus(t *testing.T) {
	mirrors := []MirrorTopic{
		{MirrorTopicName: "active-1", MirrorStatus: "ACTIVE"},
		{MirrorTopicName: "paused-1", MirrorStatus: "PAUSED"},
		{MirrorTopicName: "failed-1", MirrorStatus: "FAILED"},
		{MirrorTopicName: "active-2", MirrorStatus: "ACTIVE"},
	}

	topicNames, inactiveTopics := ClassifyMirrorTopics(mirrors)

	require.Len(t, topicNames, 4)
	require.Len(t, inactiveTopics, 2)
	// Verify inactive entries contain the topic name and status
	assert.Contains(t, inactiveTopics[0], "paused-1")
	assert.Contains(t, inactiveTopics[0], "PAUSED")
	assert.Contains(t, inactiveTopics[1], "failed-1")
	assert.Contains(t, inactiveTopics[1], "FAILED")
}

func TestClassifyMirrorTopics_Empty(t *testing.T) {
	topicNames, inactiveTopics := ClassifyMirrorTopics(nil)

	assert.Nil(t, topicNames)
	assert.Nil(t, inactiveTopics)
}

func TestGetActiveTopicsWithZeroLag(t *testing.T) {
	mirrors := []MirrorTopic{
		{
			MirrorTopicName: "zero-lag-active",
			MirrorStatus:    "ACTIVE",
			MirrorLags:      []MirrorLag{{Partition: 0, Lag: 0}, {Partition: 1, Lag: 0}},
		},
		{
			MirrorTopicName: "nonzero-lag-active",
			MirrorStatus:    "ACTIVE",
			MirrorLags:      []MirrorLag{{Partition: 0, Lag: 0}, {Partition: 1, Lag: 5}},
		},
		{
			MirrorTopicName: "zero-lag-paused",
			MirrorStatus:    "PAUSED",
			MirrorLags:      []MirrorLag{{Partition: 0, Lag: 0}},
		},
	}

	result := GetActiveTopicsWithZeroLag(mirrors)

	require.Len(t, result, 1)
	assert.Equal(t, "zero-lag-active", result[0])
}

func TestGetActiveTopicsWithZeroLag_NonZeroLag(t *testing.T) {
	mirrors := []MirrorTopic{
		{
			MirrorTopicName: "topic-a",
			MirrorStatus:    "ACTIVE",
			MirrorLags:      []MirrorLag{{Partition: 0, Lag: 10}},
		},
		{
			MirrorTopicName: "topic-b",
			MirrorStatus:    "ACTIVE",
			MirrorLags:      []MirrorLag{{Partition: 0, Lag: 3}, {Partition: 1, Lag: 7}},
		},
	}

	result := GetActiveTopicsWithZeroLag(mirrors)

	assert.Empty(t, result)
}

func TestHasActiveTopicsWithNonZeroLag_True(t *testing.T) {
	mirrors := []MirrorTopic{
		{
			MirrorTopicName: "topic-a",
			MirrorStatus:    "ACTIVE",
			MirrorLags:      []MirrorLag{{Partition: 0, Lag: 42}},
		},
	}

	assert.True(t, HasActiveTopicsWithNonZeroLag(mirrors))
}

func TestHasActiveTopicsWithNonZeroLag_AllZero(t *testing.T) {
	mirrors := []MirrorTopic{
		{
			MirrorTopicName: "topic-a",
			MirrorStatus:    "ACTIVE",
			MirrorLags:      []MirrorLag{{Partition: 0, Lag: 0}, {Partition: 1, Lag: 0}},
		},
		{
			MirrorTopicName: "topic-b",
			MirrorStatus:    "ACTIVE",
			MirrorLags:      []MirrorLag{{Partition: 0, Lag: 0}},
		},
	}

	assert.False(t, HasActiveTopicsWithNonZeroLag(mirrors))
}

func TestCountActiveMirrorTopics(t *testing.T) {
	mirrors := []MirrorTopic{
		{MirrorTopicName: "a", MirrorStatus: "ACTIVE"},
		{MirrorTopicName: "b", MirrorStatus: "PAUSED"},
		{MirrorTopicName: "c", MirrorStatus: "ACTIVE"},
		{MirrorTopicName: "d", MirrorStatus: "FAILED"},
		{MirrorTopicName: "e", MirrorStatus: "ACTIVE"},
	}

	count := CountActiveMirrorTopics(mirrors)
	assert.Equal(t, 3, count)
}

func TestValidateTopics_AllExist(t *testing.T) {
	topics := []string{"orders", "users"}
	clusterLinkTopics := []string{"orders", "users", "events"}

	svc := NewConfluentCloudService(nil)
	err := svc.ValidateTopics(topics, clusterLinkTopics)
	assert.NoError(t, err)
}

func TestValidateTopics_Missing(t *testing.T) {
	topics := []string{"orders", "missing-topic"}
	clusterLinkTopics := []string{"orders", "users"}

	svc := NewConfluentCloudService(nil)
	err := svc.ValidateTopics(topics, clusterLinkTopics)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing-topic")
}

// ---------------------------------------------------------------------------
// HTTP mock tests
// ---------------------------------------------------------------------------

func TestListMirrorTopics_Success(t *testing.T) {
	clusterID := "lkc-abc123"
	linkName := "my-cluster-link"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		expectedPath := "/kafka/v3/clusters/" + clusterID + "/links/" + linkName + "/mirrors"
		assert.Equal(t, expectedPath, r.URL.Path, "request path")
		assert.Equal(t, http.MethodGet, r.Method, "HTTP method")

		resp := map[string]interface{}{
			"data": []map[string]interface{}{
				{"mirror_topic_name": "topic-1", "mirror_status": "ACTIVE", "mirror_lags": []map[string]interface{}{{"partition": 0, "lag": 0}}},
				{"mirror_topic_name": "topic-2", "mirror_status": "PAUSED", "mirror_lags": []map[string]interface{}{}},
				{"mirror_topic_name": "topic-3", "mirror_status": "ACTIVE", "mirror_lags": []map[string]interface{}{{"partition": 0, "lag": 5}}},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	svc := NewConfluentCloudService(server.Client())
	cfg := Config{
		RestEndpoint: server.URL,
		ClusterID:    clusterID,
		LinkName:     linkName,
		APIKey:       "key",
		APISecret:    "secret",
	}

	topics, err := svc.ListMirrorTopics(context.Background(), cfg)
	require.NoError(t, err)
	require.Len(t, topics, 3)
	assert.Equal(t, "topic-1", topics[0].MirrorTopicName)
	assert.Equal(t, "PAUSED", topics[1].MirrorStatus)
	assert.Equal(t, 5, topics[2].MirrorLags[0].Lag)
}

func TestListMirrorTopics_FiltersByTopics(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"data": []map[string]interface{}{
				{"mirror_topic_name": "topic-1", "mirror_status": "ACTIVE", "mirror_lags": []map[string]interface{}{}},
				{"mirror_topic_name": "topic-2", "mirror_status": "ACTIVE", "mirror_lags": []map[string]interface{}{}},
				{"mirror_topic_name": "topic-3", "mirror_status": "ACTIVE", "mirror_lags": []map[string]interface{}{}},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	svc := NewConfluentCloudService(server.Client())
	cfg := Config{
		RestEndpoint: server.URL,
		ClusterID:    "lkc-xyz",
		LinkName:     "link-1",
		APIKey:       "key",
		APISecret:    "secret",
		Topics:       []string{"topic-1", "topic-3"},
	}

	topics, err := svc.ListMirrorTopics(context.Background(), cfg)
	require.NoError(t, err)
	require.Len(t, topics, 2)
	assert.Equal(t, "topic-1", topics[0].MirrorTopicName)
	assert.Equal(t, "topic-3", topics[1].MirrorTopicName)
}

func TestListMirrorTopics_HTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("internal server error"))
	}))
	defer server.Close()

	svc := NewConfluentCloudService(server.Client())
	cfg := Config{
		RestEndpoint: server.URL,
		ClusterID:    "lkc-err",
		LinkName:     "link-err",
		APIKey:       "key",
		APISecret:    "secret",
	}

	_, err := svc.ListMirrorTopics(context.Background(), cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "500")
}

func TestPromoteMirrorTopics_Success(t *testing.T) {
	clusterID := "lkc-promo"
	linkName := "promo-link"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		expectedPath := "/kafka/v3/clusters/" + clusterID + "/links/" + linkName + "/mirrors:promote"
		assert.Equal(t, expectedPath, r.URL.Path, "request path")
		assert.Equal(t, http.MethodPost, r.Method, "HTTP method")

		body, err := io.ReadAll(r.Body)
		require.NoError(t, err, "reading request body")

		var reqBody struct {
			MirrorTopicNames []string `json:"mirror_topic_names"`
		}
		require.NoError(t, json.Unmarshal(body, &reqBody), "unmarshalling request body")
		require.Len(t, reqBody.MirrorTopicNames, 2)
		assert.Equal(t, []string{"orders", "users"}, reqBody.MirrorTopicNames)

		resp := PromoteMirrorTopicsResponse{
			Data: []struct {
				MirrorTopicName string `json:"mirror_topic_name"`
				ErrorMessage    string `json:"error_message,omitempty"`
				ErrorCode       int    `json:"error_code,omitempty"`
			}{
				{MirrorTopicName: "orders"},
				{MirrorTopicName: "users"},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	svc := NewConfluentCloudService(server.Client())
	cfg := Config{
		RestEndpoint: server.URL,
		ClusterID:    clusterID,
		LinkName:     linkName,
		APIKey:       "key",
		APISecret:    "secret",
	}

	resp, err := svc.PromoteMirrorTopics(context.Background(), cfg, []string{"orders", "users"})
	require.NoError(t, err)
	require.Len(t, resp.Data, 2)
	assert.Equal(t, "orders", resp.Data[0].MirrorTopicName)
}

func TestPromoteMirrorTopics_Empty(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("server should not receive any request for empty topic list")
	}))
	defer server.Close()

	svc := NewConfluentCloudService(server.Client())
	cfg := Config{
		RestEndpoint: server.URL,
		ClusterID:    "lkc-empty",
		LinkName:     "link-empty",
		APIKey:       "key",
		APISecret:    "secret",
	}

	resp, err := svc.PromoteMirrorTopics(context.Background(), cfg, []string{})
	require.NoError(t, err)
	require.NotNil(t, resp)
}

func TestPromoteMirrorTopics_HTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("promote failed"))
	}))
	defer server.Close()

	svc := NewConfluentCloudService(server.Client())
	cfg := Config{
		RestEndpoint: server.URL,
		ClusterID:    "lkc-err",
		LinkName:     "link-err",
		APIKey:       "key",
		APISecret:    "secret",
	}

	_, err := svc.PromoteMirrorTopics(context.Background(), cfg, []string{"topic-1"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "500")
}

func TestGetClusterLink_Success(t *testing.T) {
	clusterID := "lkc-abc123"
	linkName := "my-cluster-link"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		expectedPath := "/kafka/v3/clusters/" + clusterID + "/links/" + linkName
		assert.Equal(t, expectedPath, r.URL.Path, "request path")
		assert.Equal(t, http.MethodGet, r.Method, "HTTP method")

		resp := map[string]interface{}{
			"link_name":         linkName,
			"link_id":           "link-id-42",
			"cluster_id":        clusterID,
			"source_cluster_id": "lkc-source",
			"link_state":        "ACTIVE",
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	svc := NewConfluentCloudService(server.Client())
	cfg := Config{
		RestEndpoint: server.URL,
		ClusterID:    clusterID,
		LinkName:     linkName,
		APIKey:       "key",
		APISecret:    "secret",
	}

	link, err := svc.GetClusterLink(context.Background(), cfg)
	require.NoError(t, err)
	require.NotNil(t, link)
	assert.Equal(t, linkName, link.LinkName)
	assert.Equal(t, "link-id-42", link.LinkID)
	assert.Equal(t, clusterID, link.ClusterID)
	assert.Equal(t, "lkc-source", link.SourceClusterID)
	assert.Equal(t, "ACTIVE", link.LinkState)
}

func TestGetClusterLink_NotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error_code":404,"message":"link not found"}`))
	}))
	defer server.Close()

	svc := NewConfluentCloudService(server.Client())
	cfg := Config{
		RestEndpoint: server.URL,
		ClusterID:    "lkc-missing",
		LinkName:     "missing-link",
		APIKey:       "key",
		APISecret:    "secret",
	}

	link, err := svc.GetClusterLink(context.Background(), cfg)
	require.Error(t, err)
	assert.Nil(t, link)
	assert.Contains(t, err.Error(), "missing-link")
	assert.Contains(t, err.Error(), "lkc-missing")
	assert.Contains(t, err.Error(), "not found")
}

func TestGetClusterLink_HTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("unauthorized"))
	}))
	defer server.Close()

	svc := NewConfluentCloudService(server.Client())
	cfg := Config{
		RestEndpoint: server.URL,
		ClusterID:    "lkc-err",
		LinkName:     "link-err",
		APIKey:       "key",
		APISecret:    "secret",
	}

	link, err := svc.GetClusterLink(context.Background(), cfg)
	require.Error(t, err)
	assert.Nil(t, link)
	assert.Contains(t, err.Error(), "401")
}

func TestListConfigs_Success(t *testing.T) {
	clusterID := "lkc-cfg"
	linkName := "config-link"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		expectedPath := "/kafka/v3/clusters/" + clusterID + "/links/" + linkName + "/configs"
		assert.Equal(t, expectedPath, r.URL.Path, "request path")
		assert.Equal(t, http.MethodGet, r.Method, "HTTP method")

		resp := map[string]interface{}{
			"data": []map[string]interface{}{
				{"name": "consumer.offset.sync.enable", "value": "true"},
				{"name": "acl.sync.enable", "value": "false"},
				{"name": "topic.config.sync", "value": "true"},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	svc := NewConfluentCloudService(server.Client())
	cfg := Config{
		RestEndpoint: server.URL,
		ClusterID:    clusterID,
		LinkName:     linkName,
		APIKey:       "key",
		APISecret:    "secret",
	}

	configs, err := svc.ListConfigs(context.Background(), cfg)
	require.NoError(t, err)
	require.Len(t, configs, 3)
	assert.Equal(t, "true", configs["consumer.offset.sync.enable"])
	assert.Equal(t, "false", configs["acl.sync.enable"])
	assert.Equal(t, "true", configs["topic.config.sync"])
}
