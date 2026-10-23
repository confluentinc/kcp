package clusterlink

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/confluentinc/kcp/internal/retry/retrytest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// promoteAPI is a cluster-link REST API with mirror topics in a status map. onPromote decides each promote
// request's fate; it is called with the request number (from 1) and the topics asked for. Every promote
// request's topics are recorded.
type promoteAPI struct {
	t         *testing.T
	mu        sync.Mutex
	status    map[string]string
	posts     [][]string
	gets      int
	onPromote func(api *promoteAPI, n int, topics []string, w http.ResponseWriter)
}

func (a *promoteAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	switch {
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/mirrors:promote"):
		body, err := io.ReadAll(r.Body)
		require.NoError(a.t, err)
		var req struct {
			MirrorTopicNames []string `json:"mirror_topic_names"`
		}
		require.NoError(a.t, json.Unmarshal(body, &req))
		a.posts = append(a.posts, req.MirrorTopicNames)
		a.onPromote(a, len(a.posts), req.MirrorTopicNames, w)
	case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/mirrors"):
		a.gets++
		writeMirrors(a.t, w, a.status)
	default:
		a.t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
	}
}

// accept promotes topics and answers like Confluent Cloud.
func (a *promoteAPI) accept(topics []string, w http.ResponseWriter) {
	resp := PromoteMirrorTopicsResponse{}
	for _, topic := range topics {
		a.status[topic] = MirrorStatusPendingStopped
		resp.Data = append(resp.Data, promoteEntry{MirrorTopicName: topic})
	}
	w.Header().Set("Content-Type", "application/json")
	require.NoError(a.t, json.NewEncoder(w).Encode(resp))
}

func promoted(resp *PromoteMirrorTopicsResponse) []string {
	var out []string
	for _, d := range resp.Data {
		if d.ErrorCode == 0 {
			out = append(out, d.MirrorTopicName)
		}
	}
	return out
}

func runPromote(t *testing.T, api *promoteAPI, topics []string) (*PromoteMirrorTopicsResponse, error) {
	t.Helper()
	server := httptest.NewServer(api)
	t.Cleanup(server.Close)
	return NewConfluentCloudService(server.Client()).PromoteMirrorTopics(context.Background(), testConfig(server.URL), topics)
}

func TestPromoteMirrorTopics_ResendsOnlyTheTopicsAResetLeftUnpromoted(t *testing.T) {
	retrytest.Fast(t)
	api := &promoteAPI{t: t, status: map[string]string{"orders": MirrorStatusActive, "users": MirrorStatusActive}}
	api.onPromote = func(a *promoteAPI, n int, topics []string, w http.ResponseWriter) {
		if n == 1 {
			// The cluster accepted "orders", then the connection dropped before the answer.
			a.status["orders"] = MirrorStatusPendingStopped
			resetConnection(t, w)
			return
		}
		a.accept(topics, w)
	}

	resp, err := runPromote(t, api, []string{"orders", "users"})

	require.NoError(t, err)
	assert.Equal(t, [][]string{{"orders", "users"}, {"users"}}, api.posts, "orders was promoted by the first request and is not sent again")
	assert.ElementsMatch(t, []string{"orders", "users"}, promoted(resp))
}

func TestPromoteMirrorTopics_ResendsEverythingAfterA503ThatPromotedNothing(t *testing.T) {
	retrytest.Fast(t)
	api := &promoteAPI{t: t, status: map[string]string{"orders": MirrorStatusActive, "users": MirrorStatusActive}}
	api.onPromote = func(a *promoteAPI, n int, topics []string, w http.ResponseWriter) {
		if n == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		a.accept(topics, w)
	}

	resp, err := runPromote(t, api, []string{"orders", "users"})

	require.NoError(t, err)
	assert.Equal(t, [][]string{{"orders", "users"}, {"orders", "users"}}, api.posts)
	assert.Equal(t, 1, api.gets, "the mirror status is read once, after the unanswered request")
	assert.ElementsMatch(t, []string{"orders", "users"}, promoted(resp))
}

func TestPromoteMirrorTopics_SendsNothingMoreWhenTheLostRequestPromotedEverything(t *testing.T) {
	retrytest.Fast(t)
	api := &promoteAPI{t: t, status: map[string]string{"orders": MirrorStatusActive, "users": MirrorStatusActive}}
	api.onPromote = func(a *promoteAPI, n int, topics []string, w http.ResponseWriter) {
		for _, topic := range topics {
			a.status[topic] = MirrorStatusPendingStopped
		}
		resetConnection(t, w)
	}

	resp, err := runPromote(t, api, []string{"orders", "users"})

	require.NoError(t, err)
	assert.Len(t, api.posts, 1)
	assert.ElementsMatch(t, []string{"orders", "users"}, promoted(resp))
}

func TestPromoteMirrorTopics_DoesNotRetryA400(t *testing.T) {
	retrytest.Fast(t)
	api := &promoteAPI{t: t, status: map[string]string{"orders": MirrorStatusActive}}
	api.onPromote = func(a *promoteAPI, n int, topics []string, w http.ResponseWriter) {
		w.WriteHeader(http.StatusBadRequest)
	}

	_, err := runPromote(t, api, []string{"orders"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "400")
	assert.Len(t, api.posts, 1)
	assert.Zero(t, api.gets, "a rejected request is not followed by a status read")
}
