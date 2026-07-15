package serviceaccounts

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/confluentinc/kcp/internal/services/clusterlink"
	"github.com/stretchr/testify/require"
)

// testAuth is the BasicAuth used across this file's tests, so every handler
// can assert the exact Authorization header value the client is expected to
// send (regression guard: NewCCClient must apply auth per request, since
// srv.Client() carries no auth of its own).
var testAuth = clusterlink.BasicAuth{Username: "k", Password: "s"}

// wantAuthHeader is the literal Authorization header value testAuth.Apply
// produces, computed independently of clusterlink's own base64 encoding so
// the assertion doesn't just restate the implementation.
const wantAuthHeader = "Basic azpz" // base64("k:s")

func requireAuthHeader(t *testing.T, r *http.Request) {
	t.Helper()
	got := r.Header.Get("Authorization")
	require.NotEmpty(t, got, "request must carry an Authorization header")
	require.Equal(t, wantAuthHeader, got)
}

func TestCCClient_FindAndCreate(t *testing.T) {
	var created bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requireAuthHeader(t, r)
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
	c := NewCCClient(srv.URL, srv.Client(), testAuth)
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
		requireAuthHeader(t, r)
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

	c := NewCCClient(srv.URL, srv.Client(), testAuth)
	sa, err := c.Create(context.Background(), "app-consumer", "kcp:source-principal=User:app-consumer")
	require.NoError(t, err)
	require.NotNil(t, sa)
	require.Equal(t, "sa-existing", sa.ID)
}

// TestCCClient_NumericToResourceID covers the legacy /service_accounts read:
// it must hit the ROOT /service_accounts path (not /iam/v2/...), apply the
// per-request auth, return only service_account:true entries (with a non-empty
// resource_id) keyed by their numeric id as a string, and follow the
// page_info.page_token cursor across pages (NOT page_info.next_page_token,
// which this endpoint always returns empty — this is a regression test for
// that exact bug: reading the wrong field silently truncates the result to
// page 1, which is why this test asserts BOTH pages' entries end up in the
// returned map).
func TestCCClient_NumericToResourceID(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requireAuthHeader(t, r)
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, "/service_accounts", r.URL.Path, "must read the legacy root path, not /iam/v2/service-accounts")
		calls++
		switch r.URL.Query().Get("page_token") {
		case "":
			// First page: one service account, one human user (dropped), and a
			// next-page cursor advertised via page_info.page_token. Also sets
			// next_page_token (always empty on the real endpoint) to prove the
			// client ignores it rather than terminating early.
			_, _ = io.WriteString(w, `{"users":[
				{"id":1267635,"resource_id":"sa-21d32o","service_account":true},
				{"id":42,"resource_id":"","service_account":false}
			],"page_info":{"next_page_token":"","page_token":"tok2"}}`)
		case "tok2":
			// Second (final) page: another service account, empty cursor.
			_, _ = io.WriteString(w, `{"users":[
				{"id":9988776,"resource_id":"sa-other1","service_account":true}
			],"page_info":{"next_page_token":"","page_token":""}}`)
		default:
			t.Fatalf("unexpected page_token: %q", r.URL.Query().Get("page_token"))
		}
	}))
	defer srv.Close()

	c := NewCCClient(srv.URL, srv.Client(), testAuth)
	got, err := c.NumericToResourceID(context.Background())
	require.NoError(t, err)
	require.Equal(t, 2, calls, "must follow the page_token cursor across both pages")
	require.Equal(t, map[string]string{
		"1267635": "sa-21d32o",
		"9988776": "sa-other1",
	}, got, "only service_account:true entries, keyed by numeric id as a string, from BOTH pages")
}

// TestCCClient_NumericToResourceID_Error surfaces a non-2xx from the legacy
// endpoint as an error rather than a partial/empty map read as success.
func TestCCClient_NumericToResourceID_Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requireAuthHeader(t, r)
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"errors":[{"detail":"make sure you're using a Cloud or Global API Key"}]}`)
	}))
	defer srv.Close()

	c := NewCCClient(srv.URL, srv.Client(), testAuth)
	got, err := c.NumericToResourceID(context.Background())
	require.Error(t, err)
	require.Nil(t, got)
}

// TestCCClient_Create_409ThenNotFound covers the fallback path where the
// POST returns a 409 conflict but the follow-up GET finds nothing (e.g.
// eventual consistency or a concurrent delete). Create must return an
// explicit error rather than (nil, nil), which would read as success.
func TestCCClient_Create_409ThenNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requireAuthHeader(t, r)
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

	c := NewCCClient(srv.URL, srv.Client(), testAuth)
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
		requireAuthHeader(t, r)
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

	c := NewCCClient(srv.URL, srv.Client(), testAuth)
	sa, err := c.Create(context.Background(), "app-consumer", "kcp:source-principal=User:app-consumer")
	require.Error(t, err)
	require.Nil(t, sa)
	require.True(t, strings.Contains(err.Error(), "500") || strings.Contains(err.Error(), "internal error"))
}
