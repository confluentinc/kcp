package main

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func apiHost(srv *httptest.Server) string { return strings.TrimPrefix(srv.URL, "http://") }

func TestRuntimeLoopNextFailure(t *testing.T) {
	tests := []struct {
		name   string
		status int
		id     string
	}{
		{"non-2xx", http.StatusInternalServerError, "req-1"},
		{"missing request id", http.StatusOK, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var posts int
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPost {
					posts++
				}
				if tt.id != "" {
					w.Header().Set("Lambda-Runtime-Aws-Request-Id", tt.id)
				}
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(`{}`))
			}))
			defer srv.Close()
			h := &handler{tmpRoot: t.TempDir(), logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
			err := runtimeLoop(apiHost(srv), h)
			require.ErrorContains(t, err, "unexpected response from runtime API /next")
			assert.Zero(t, posts)
		})
	}
}

func TestRuntimeLoopRoutesResponseAndError(t *testing.T) {
	h, _ := newTestHandler(t, "")
	require.NoError(t, os.WriteFile(h.kcpPath, []byte("#!/bin/sh\necho '{}' > kcp-state.json\n"), 0o755))

	events := []string{
		`not json`, // fatal -> /error
		`{"cluster_arn":"` + testArn + `","region":"us-east-1","aws_access_key_id":"A","aws_secret_access_key":"S","steps":["discover"]}`, // -> /response
	}
	var mu sync.Mutex
	var posted []string
	next := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.Method == http.MethodPost {
			posted = append(posted, r.URL.Path)
			w.WriteHeader(http.StatusAccepted)
			return
		}
		if next >= len(events) { // end the loop with a /next failure
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Lambda-Runtime-Aws-Request-Id", "id"+string(rune('0'+next)))
		_, _ = w.Write([]byte(events[next]))
		next++
	}))
	defer srv.Close()

	require.Error(t, runtimeLoop(apiHost(srv), h))
	assert.Equal(t, []string{
		"/2018-06-01/runtime/invocation/id0/error",
		"/2018-06-01/runtime/invocation/id1/response",
	}, posted)
}
