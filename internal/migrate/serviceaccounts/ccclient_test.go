package serviceaccounts

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
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
