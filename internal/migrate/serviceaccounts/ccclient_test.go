package serviceaccounts

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCCClient_FindAndCreate(t *testing.T) {
	var created bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Query().Get("display_name") == "app-consumer" && !created:
			_, _ = io.WriteString(w, `{"data":[]}`)
		case r.Method == "POST":
			created = true
			w.WriteHeader(201)
			_, _ = io.WriteString(w, `{"id":"sa-abc123","display_name":"app-consumer","description":"kcp:source-principal=User:app-consumer"}`)
		default:
			_, _ = io.WriteString(w, `{"data":[{"id":"sa-abc123","display_name":"app-consumer"}]}`)
		}
	}))
	defer srv.Close()
	c := NewCCClient(srv.URL, srv.Client())
	got, err := c.FindByDisplayName(context.Background(), "app-consumer")
	require.NoError(t, err)
	require.Nil(t, got)
	sa, err := c.Create(context.Background(), "app-consumer", "kcp:source-principal=User:app-consumer")
	require.NoError(t, err)
	require.Equal(t, "sa-abc123", sa.ID)
}

// TestCCClient_Create_409ThenFound covers the fallback path where the POST
// returns a 409 conflict and the follow-up GET finds the existing service
// account. Create should return that account with no error.
func TestCCClient_Create_409ThenFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "POST":
			w.WriteHeader(http.StatusConflict)
			_, _ = io.WriteString(w, `{"errors":[{"detail":"display_name already in use"}]}`)
		case r.Method == "GET" && r.URL.Query().Get("display_name") == "app-consumer":
			_, _ = io.WriteString(w, `{"data":[{"id":"sa-existing","display_name":"app-consumer"}]}`)
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
	}))
	defer srv.Close()

	c := NewCCClient(srv.URL, srv.Client())
	sa, err := c.Create(context.Background(), "app-consumer", "kcp:source-principal=User:app-consumer")
	require.NoError(t, err)
	require.NotNil(t, sa)
	require.Equal(t, "sa-existing", sa.ID)
}

// TestCCClient_Create_409ThenNotFound covers the fallback path where the
// POST returns a 409 conflict but the follow-up GET finds nothing (e.g.
// eventual consistency or a concurrent delete). Create must return an
// explicit error rather than (nil, nil), which would read as success.
func TestCCClient_Create_409ThenNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "POST":
			w.WriteHeader(http.StatusConflict)
			_, _ = io.WriteString(w, `{"errors":[{"detail":"display_name already in use"}]}`)
		case r.Method == "GET" && r.URL.Query().Get("display_name") == "app-consumer":
			_, _ = io.WriteString(w, `{"data":[]}`)
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
	}))
	defer srv.Close()

	c := NewCCClient(srv.URL, srv.Client())
	sa, err := c.Create(context.Background(), "app-consumer", "kcp:source-principal=User:app-consumer")
	require.Error(t, err)
	require.Nil(t, sa)
	require.Contains(t, err.Error(), "app-consumer")
}

// TestCCClient_Create_409ThenFindErrors covers the fallback path where the
// POST returns a 409 conflict and the follow-up GET itself fails. Create
// must surface the lookup error, not the stale original 409 error.
func TestCCClient_Create_409ThenFindErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "POST":
			w.WriteHeader(http.StatusConflict)
			_, _ = io.WriteString(w, `{"errors":[{"detail":"display_name already in use"}]}`)
		case r.Method == "GET" && r.URL.Query().Get("display_name") == "app-consumer":
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = io.WriteString(w, `{"errors":[{"detail":"internal error"}]}`)
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
	}))
	defer srv.Close()

	c := NewCCClient(srv.URL, srv.Client())
	sa, err := c.Create(context.Background(), "app-consumer", "kcp:source-principal=User:app-consumer")
	require.Error(t, err)
	require.Nil(t, sa)
	require.True(t, strings.Contains(err.Error(), "500") || strings.Contains(err.Error(), "internal error"))
}
