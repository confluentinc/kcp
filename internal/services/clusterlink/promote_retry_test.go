package clusterlink

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/confluentinc/kcp/internal/retry"
	"github.com/confluentinc/kcp/internal/retry/retrytest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// promoteAPI is a cluster-link REST API with mirror topics in a status map. onPromote decides each promote
// request's fate; it is called with the request number (from 1) and the topics asked for. Every promote
// request's topics are recorded. onGet, if set, decides each mirror status read's fate in place of answering
// from the status map.
type promoteAPI struct {
	t         *testing.T
	mu        sync.Mutex
	status    map[string]string
	posts     [][]string
	gets      int
	onPromote func(api *promoteAPI, n int, topics []string, w http.ResponseWriter)
	onGet     func(api *promoteAPI, w http.ResponseWriter)
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
		if a.onGet != nil {
			a.onGet(a, w)
			return
		}
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

func TestPromoteMirrorTopics_ReadsTheStatusOfTopicsOutsideConfigTopics(t *testing.T) {
	retrytest.Fast(t)
	api := &promoteAPI{t: t, status: map[string]string{"orders": MirrorStatusActive, "users": MirrorStatusActive}}
	api.onPromote = func(a *promoteAPI, n int, topics []string, w http.ResponseWriter) {
		if n == 1 {
			a.status["orders"] = MirrorStatusPendingStopped
			resetConnection(t, w)
			return
		}
		a.accept(topics, w)
	}
	server := httptest.NewServer(api)
	t.Cleanup(server.Close)
	config := testConfig(server.URL)
	config.Topics = []string{"users"}

	resp, err := NewConfluentCloudService(server.Client()).PromoteMirrorTopics(context.Background(), config, []string{"orders", "users"})

	require.NoError(t, err)
	assert.Equal(t, [][]string{{"orders", "users"}, {"users"}}, api.posts, "orders is promoted and is not sent again, although Config.Topics does not list it")
	assert.ElementsMatch(t, []string{"orders", "users"}, promoted(resp))
}

func TestPromoteMirrorTopics_IsNotCutOffAtTheRequestTimeout(t *testing.T) {
	retrytest.Fast(t)
	setTimeoutsForTest(t, 50*time.Millisecond, 5*time.Second)
	api := &promoteAPI{t: t, status: map[string]string{"orders": MirrorStatusActive}}
	api.onPromote = func(a *promoteAPI, n int, topics []string, w http.ResponseWriter) {
		time.Sleep(150 * time.Millisecond) // the cluster takes a while to accept the batch
		a.accept(topics, w)
	}

	resp, err := runPromote(t, api, []string{"orders"})

	require.NoError(t, err)
	assert.Len(t, api.posts, 1, "the promote is sent once")
	assert.Zero(t, api.gets, "the promote's own answer arrives, so the mirror status is not read")
	assert.Equal(t, []string{"orders"}, promoted(resp))
}

func TestPromoteMirrorTopics_ReadsTheStatusAgainAfterASecondUnansweredRequest(t *testing.T) {
	retrytest.Fast(t)
	api := &promoteAPI{t: t, status: map[string]string{"orders": MirrorStatusActive, "users": MirrorStatusActive}}
	api.onPromote = func(a *promoteAPI, n int, topics []string, w http.ResponseWriter) {
		switch n {
		case 1:
			// The cluster accepted "orders", then the connection dropped before the answer.
			a.status["orders"] = MirrorStatusPendingStopped
		case 2:
			// The resend for "users" was accepted, and its answer was lost too.
			a.status["users"] = MirrorStatusPendingStopped
		}
		resetConnection(t, w)
	}

	resp, err := runPromote(t, api, []string{"orders", "users"})

	require.NoError(t, err)
	assert.Equal(t, [][]string{{"orders", "users"}, {"users"}}, api.posts)
	assert.Equal(t, 2, api.gets, "the mirror status is read after each unanswered request")
	assert.ElementsMatch(t, []string{"orders", "users"}, promoted(resp))
}

func TestPromoteMirrorTopics_SendsNothingMoreWhenTheStatusReadFails(t *testing.T) {
	retrytest.Fast(t)
	api := &promoteAPI{t: t, status: map[string]string{"orders": MirrorStatusActive}}
	api.onPromote = func(a *promoteAPI, n int, topics []string, w http.ResponseWriter) {
		resetConnection(t, w) // unanswered: kcp cannot know whether orders was promoted
	}
	api.onGet = func(a *promoteAPI, w http.ResponseWriter) {
		resetConnection(t, w) // and the status read that would tell it fails as well
	}

	resp, err := runPromote(t, api, []string{"orders"})

	require.Error(t, err)
	assert.Nil(t, resp)
	assert.Contains(t, err.Error(), "reading mirror status after a promote request went unanswered")
	assert.Len(t, api.posts, 1, "a promote whose status cannot be read is not sent again: it may already have been accepted")
	assert.Positive(t, api.gets)
}

func TestPromoteMirrorTopics_ReadsTheStatusBeforeEveryResendUntilItRunsOutOfBudget(t *testing.T) {
	retrytest.Fast(t)
	api := &promoteAPI{t: t, status: map[string]string{"orders": MirrorStatusActive}}
	api.onPromote = func(a *promoteAPI, n int, topics []string, w http.ResponseWriter) {
		resetConnection(t, w) // never answered, never accepted: orders stays active
	}

	resp, err := runPromote(t, api, []string{"orders"})

	var gave *retry.GaveUpError
	require.True(t, errors.As(err, &gave), "err = %v, want it to give up once the budget runs out", err)
	assert.Nil(t, resp)
	assert.Greater(t, len(api.posts), 1, "the promote was resent while the budget lasted")
	assert.Equal(t, len(api.posts)-1, api.gets, "the mirror status is read before each resend, never after the first request alone")
}
