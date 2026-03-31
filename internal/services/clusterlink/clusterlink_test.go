package clusterlink

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
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

	if len(topicNames) != 3 {
		t.Fatalf("expected 3 topic names, got %d", len(topicNames))
	}
	for i, name := range []string{"topic-a", "topic-b", "topic-c"} {
		if topicNames[i] != name {
			t.Errorf("topicNames[%d] = %q, want %q", i, topicNames[i], name)
		}
	}
	if len(inactiveTopics) != 0 {
		t.Errorf("expected 0 inactive topics, got %d: %v", len(inactiveTopics), inactiveTopics)
	}
}

func TestClassifyMirrorTopics_MixedStatus(t *testing.T) {
	mirrors := []MirrorTopic{
		{MirrorTopicName: "active-1", MirrorStatus: "ACTIVE"},
		{MirrorTopicName: "paused-1", MirrorStatus: "PAUSED"},
		{MirrorTopicName: "failed-1", MirrorStatus: "FAILED"},
		{MirrorTopicName: "active-2", MirrorStatus: "ACTIVE"},
	}

	topicNames, inactiveTopics := ClassifyMirrorTopics(mirrors)

	if len(topicNames) != 4 {
		t.Fatalf("expected 4 topic names, got %d", len(topicNames))
	}
	if len(inactiveTopics) != 2 {
		t.Fatalf("expected 2 inactive topics, got %d", len(inactiveTopics))
	}
	// Verify inactive entries contain the topic name and status
	if !strings.Contains(inactiveTopics[0], "paused-1") || !strings.Contains(inactiveTopics[0], "PAUSED") {
		t.Errorf("inactive[0] = %q, expected to contain paused-1 and PAUSED", inactiveTopics[0])
	}
	if !strings.Contains(inactiveTopics[1], "failed-1") || !strings.Contains(inactiveTopics[1], "FAILED") {
		t.Errorf("inactive[1] = %q, expected to contain failed-1 and FAILED", inactiveTopics[1])
	}
}

func TestClassifyMirrorTopics_Empty(t *testing.T) {
	topicNames, inactiveTopics := ClassifyMirrorTopics(nil)

	if topicNames != nil {
		t.Errorf("expected nil topicNames, got %v", topicNames)
	}
	if inactiveTopics != nil {
		t.Errorf("expected nil inactiveTopics, got %v", inactiveTopics)
	}
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

	if len(result) != 1 {
		t.Fatalf("expected 1 topic, got %d: %v", len(result), result)
	}
	if result[0] != "zero-lag-active" {
		t.Errorf("expected zero-lag-active, got %q", result[0])
	}
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

	if len(result) != 0 {
		t.Errorf("expected empty result, got %v", result)
	}
}

func TestHasActiveTopicsWithNonZeroLag_True(t *testing.T) {
	mirrors := []MirrorTopic{
		{
			MirrorTopicName: "topic-a",
			MirrorStatus:    "ACTIVE",
			MirrorLags:      []MirrorLag{{Partition: 0, Lag: 42}},
		},
	}

	if !HasActiveTopicsWithNonZeroLag(mirrors) {
		t.Error("expected true, got false")
	}
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

	if HasActiveTopicsWithNonZeroLag(mirrors) {
		t.Error("expected false, got true")
	}
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
	if count != 3 {
		t.Errorf("expected 3, got %d", count)
	}
}

func TestValidateTopics_AllExist(t *testing.T) {
	topics := []string{"orders", "users"}
	clusterLinkTopics := []string{"orders", "users", "events"}

	svc := NewConfluentCloudService(nil)
	err := svc.ValidateTopics(topics, clusterLinkTopics)
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
}

func TestValidateTopics_Missing(t *testing.T) {
	topics := []string{"orders", "missing-topic"}
	clusterLinkTopics := []string{"orders", "users"}

	svc := NewConfluentCloudService(nil)
	err := svc.ValidateTopics(topics, clusterLinkTopics)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "missing-topic") {
		t.Errorf("error %q should contain 'missing-topic'", err.Error())
	}
}

// ---------------------------------------------------------------------------
// HTTP mock tests
// ---------------------------------------------------------------------------

func TestListMirrorTopics_Success(t *testing.T) {
	clusterID := "lkc-abc123"
	linkName := "my-cluster-link"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		expectedPath := "/kafka/v3/clusters/" + clusterID + "/links/" + linkName + "/mirrors"
		if r.URL.Path != expectedPath {
			t.Errorf("request path = %q, want %q", r.URL.Path, expectedPath)
		}
		if r.Method != http.MethodGet {
			t.Errorf("method = %q, want GET", r.Method)
		}

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
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(topics) != 3 {
		t.Fatalf("expected 3 topics, got %d", len(topics))
	}
	if topics[0].MirrorTopicName != "topic-1" {
		t.Errorf("topics[0].MirrorTopicName = %q, want topic-1", topics[0].MirrorTopicName)
	}
	if topics[1].MirrorStatus != "PAUSED" {
		t.Errorf("topics[1].MirrorStatus = %q, want PAUSED", topics[1].MirrorStatus)
	}
	if topics[2].MirrorLags[0].Lag != 5 {
		t.Errorf("topics[2].MirrorLags[0].Lag = %d, want 5", topics[2].MirrorLags[0].Lag)
	}
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
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(topics) != 2 {
		t.Fatalf("expected 2 topics, got %d", len(topics))
	}
	if topics[0].MirrorTopicName != "topic-1" {
		t.Errorf("topics[0] = %q, want topic-1", topics[0].MirrorTopicName)
	}
	if topics[1].MirrorTopicName != "topic-3" {
		t.Errorf("topics[1] = %q, want topic-3", topics[1].MirrorTopicName)
	}
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
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("error %q should contain status code 500", err.Error())
	}
}

func TestPromoteMirrorTopics_Success(t *testing.T) {
	clusterID := "lkc-promo"
	linkName := "promo-link"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		expectedPath := "/kafka/v3/clusters/" + clusterID + "/links/" + linkName + "/mirrors:promote"
		if r.URL.Path != expectedPath {
			t.Errorf("request path = %q, want %q", r.URL.Path, expectedPath)
		}
		if r.Method != http.MethodPost {
			t.Errorf("method = %q, want POST", r.Method)
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("failed to read request body: %v", err)
		}

		var reqBody struct {
			MirrorTopicNames []string `json:"mirror_topic_names"`
		}
		if err := json.Unmarshal(body, &reqBody); err != nil {
			t.Fatalf("failed to unmarshal request body: %v", err)
		}
		if len(reqBody.MirrorTopicNames) != 2 {
			t.Errorf("expected 2 topic names in body, got %d", len(reqBody.MirrorTopicNames))
		}
		if reqBody.MirrorTopicNames[0] != "orders" || reqBody.MirrorTopicNames[1] != "users" {
			t.Errorf("unexpected topic names: %v", reqBody.MirrorTopicNames)
		}

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
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Data) != 2 {
		t.Fatalf("expected 2 items in response, got %d", len(resp.Data))
	}
	if resp.Data[0].MirrorTopicName != "orders" {
		t.Errorf("resp.Data[0].MirrorTopicName = %q, want orders", resp.Data[0].MirrorTopicName)
	}
}

func TestPromoteMirrorTopics_Empty(t *testing.T) {
	var requestCount int64

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&requestCount, 1)
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
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp == nil {
		t.Fatal("expected non-nil response")
	}
	if atomic.LoadInt64(&requestCount) != 0 {
		t.Errorf("expected 0 requests, got %d", atomic.LoadInt64(&requestCount))
	}
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
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("error %q should contain status code 500", err.Error())
	}
}

func TestListConfigs_Success(t *testing.T) {
	clusterID := "lkc-cfg"
	linkName := "config-link"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		expectedPath := "/kafka/v3/clusters/" + clusterID + "/links/" + linkName + "/configs"
		if r.URL.Path != expectedPath {
			t.Errorf("request path = %q, want %q", r.URL.Path, expectedPath)
		}
		if r.Method != http.MethodGet {
			t.Errorf("method = %q, want GET", r.Method)
		}

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
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(configs) != 3 {
		t.Fatalf("expected 3 configs, got %d", len(configs))
	}
	if configs["consumer.offset.sync.enable"] != "true" {
		t.Errorf("consumer.offset.sync.enable = %q, want true", configs["consumer.offset.sync.enable"])
	}
	if configs["acl.sync.enable"] != "false" {
		t.Errorf("acl.sync.enable = %q, want false", configs["acl.sync.enable"])
	}
	if configs["topic.config.sync"] != "true" {
		t.Errorf("topic.config.sync = %q, want true", configs["topic.config.sync"])
	}
}
