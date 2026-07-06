package client

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSanitizeBodySnippet(t *testing.T) {
	t.Run("strips control characters and collapses whitespace to single line", func(t *testing.T) {
		got := sanitizeBodySnippet([]byte("line1\nline2\ttab\r\n\x00end"))

		assert.Equal(t, "line1 line2 tab end", got)
		assert.NotContains(t, got, "\n")
		assert.NotContains(t, got, "\t")
		assert.NotContains(t, got, "\r")
		assert.NotContains(t, got, "\x00")
	})

	t.Run("truncates an over-long body with an ellipsis and stays bounded", func(t *testing.T) {
		got := sanitizeBodySnippet([]byte(strings.Repeat("a", 500)))

		assert.True(t, strings.HasSuffix(got, "…"), "expected ellipsis suffix, got %q", got)
		assert.LessOrEqual(t, utf8.RuneCountInString(got), 201, "snippet should be bounded")
	})

	t.Run("leaves a short clean body unchanged", func(t *testing.T) {
		assert.Equal(t, "hello world", sanitizeBodySnippet([]byte("hello world")))
	})

	t.Run("returns empty string for empty body", func(t *testing.T) {
		assert.Equal(t, "", sanitizeBodySnippet([]byte{}))
	})
}

const validConfigJSON = `{"compatibilityLevel":"BACKWARD"}`

func TestValidateConfluentSchemaRegistryURL(t *testing.T) {
	t.Run("passes for a real schema registry (unauthenticated, JSON /config)", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/config", r.URL.Path)
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, validConfigJSON)
		}))
		defer server.Close()

		err := ValidateConfluentSchemaRegistryURL(server.URL, WithUnauthenticated())
		assert.NoError(t, err)
	})

	t.Run("passes and sends basic auth when configured", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			u, p, ok := r.BasicAuth()
			assert.True(t, ok, "expected basic auth header")
			assert.Equal(t, "admin", u)
			assert.Equal(t, "s3cr3t", p)
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, validConfigJSON)
		}))
		defer server.Close()

		err := ValidateConfluentSchemaRegistryURL(server.URL, WithBasicAuth("admin", "s3cr3t"))
		assert.NoError(t, err)
	})

	t.Run("rejects HTML on a 2xx without leaking the json decode error", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			_, _ = fmt.Fprint(w, "<!DOCTYPE html><html><body>not a registry</body></html>")
		}))
		defer server.Close()

		err := ValidateConfluentSchemaRegistryURL(server.URL, WithUnauthenticated())
		require.Error(t, err)
		assert.NotContains(t, err.Error(), "invalid character", "must not surface the raw json decode error")
		assert.NotContains(t, err.Error(), "<", "must not dump the HTML body")
		assert.Contains(t, err.Error(), "text/html", "should name the observed content-type")
		assert.Contains(t, err.Error(), "does not look like a Schema Registry")
	})

	t.Run("rejects HTML body even when Content-Type lies that it is JSON", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, "<!DOCTYPE html><html></html>")
		}))
		defer server.Close()

		err := ValidateConfluentSchemaRegistryURL(server.URL, WithUnauthenticated())
		require.Error(t, err)
		assert.NotContains(t, err.Error(), "invalid character")
	})

	t.Run("reports authentication failure on 401 without leaking credentials", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
		}))
		defer server.Close()

		err := ValidateConfluentSchemaRegistryURL(server.URL, WithBasicAuth("secret-user", "secret-pass"))
		require.Error(t, err)
		assert.Contains(t, strings.ToLower(err.Error()), "auth")
		assert.NotContains(t, err.Error(), "secret-user")
		assert.NotContains(t, err.Error(), "secret-pass")
	})

	t.Run("reports authentication failure on 403", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusForbidden)
		}))
		defer server.Close()

		err := ValidateConfluentSchemaRegistryURL(server.URL, WithUnauthenticated())
		require.Error(t, err)
		assert.Contains(t, strings.ToLower(err.Error()), "auth")
	})

	t.Run("surfaces the schema registry's JSON error body on a non-2xx", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = fmt.Fprint(w, `{"error_code":50001,"message":"backend exploded"}`)
		}))
		defer server.Close()

		err := ValidateConfluentSchemaRegistryURL(server.URL, WithUnauthenticated())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "500")
		assert.Contains(t, err.Error(), "backend exploded", "a JSON error body is the meaningful part — surface it")
	})

	t.Run("does not dump an HTML body on a non-2xx, gives url guidance instead", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(http.StatusNotFound)
			_, _ = fmt.Fprint(w, "<!DOCTYPE html><html><body>Example Domain</body></html>")
		}))
		defer server.Close()

		err := ValidateConfluentSchemaRegistryURL(server.URL, WithUnauthenticated())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "404", "the status code is the useful signal")
		assert.Contains(t, err.Error(), "does not look like a Schema Registry")
		assert.NotContains(t, err.Error(), "<", "must not dump the HTML body")
		assert.NotContains(t, err.Error(), "Example Domain")
	})

	t.Run("reports an unreachable host naming the url", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
		url := server.URL
		server.Close() // free the port so the connection is refused

		err := ValidateConfluentSchemaRegistryURL(url, WithUnauthenticated())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "127.0.0.1", "should name the url host")
	})

	t.Run("does not leak credentials on a transport error", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
		url := server.URL
		server.Close()

		err := ValidateConfluentSchemaRegistryURL(url, WithBasicAuth("secret-user", "secret-pass"))
		require.Error(t, err)
		assert.NotContains(t, err.Error(), "secret-user")
		assert.NotContains(t, err.Error(), "secret-pass")
	})

	t.Run("bounds the snippet for an oversized body", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = fmt.Fprint(w, strings.Repeat("x", 5_000_000))
		}))
		defer server.Close()

		err := ValidateConfluentSchemaRegistryURL(server.URL, WithUnauthenticated())
		require.Error(t, err)
		assert.Less(t, utf8.RuneCountInString(err.Error()), 1000, "error must stay bounded regardless of body size")
	})

	t.Run("rejects an untrusted TLS certificate rather than passing", func(t *testing.T) {
		server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, validConfigJSON)
		}))
		defer server.Close()

		// Default client (the validator's) does not trust the self-signed cert.
		err := ValidateConfluentSchemaRegistryURL(server.URL, WithUnauthenticated())
		require.Error(t, err, "an untrusted TLS cert must not be silently accepted")
	})

	t.Run("refuses to follow a redirect (no SSRF / credential-follow)", func(t *testing.T) {
		redirectTarget := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t.Errorf("redirect target must not be requested: %s", r.URL.Path)
		}))
		defer redirectTarget.Close()

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, redirectTarget.URL+"/config", http.StatusFound)
		}))
		defer server.Close()

		err := ValidateConfluentSchemaRegistryURL(server.URL, WithUnauthenticated())
		require.Error(t, err, "a redirect must surface as an error, not be followed")
		assert.Contains(t, err.Error(), "redirect", "should classify the failure as a redirect, not a generic non-2xx")
	})

	t.Run("does not send an Authorization header when unauthenticated", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _, ok := r.BasicAuth()
			assert.False(t, ok, "no basic auth header expected when unauthenticated")
			assert.Empty(t, r.Header.Get("Authorization"))
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, validConfigJSON)
		}))
		defer server.Close()

		err := ValidateConfluentSchemaRegistryURL(server.URL, WithUnauthenticated())
		assert.NoError(t, err)
	})

	t.Run("redacts credentials embedded in the url from the error", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			_, _ = fmt.Fprint(w, "<!DOCTYPE html><html></html>")
		}))
		defer server.Close()

		u, err := url.Parse(server.URL)
		require.NoError(t, err)
		u.User = url.UserPassword("url-user", "url-secret")

		err = ValidateConfluentSchemaRegistryURL(u.String(), WithUnauthenticated())
		require.Error(t, err)
		assert.NotContains(t, err.Error(), "url-secret")
		assert.NotContains(t, err.Error(), "url-user")
	})
}
