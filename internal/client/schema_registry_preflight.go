package client

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"

	"github.com/confluentinc/kcp/internal/types"
)

const (
	// maxSnippetRunes bounds how much of an attacker-influenceable response body
	// is embedded in an error message.
	maxSnippetRunes = 200
	// maxProbeBodyBytes caps how many bytes of the probe response are read into
	// memory, so a hostile or huge response cannot exhaust it.
	maxProbeBodyBytes = 4096
	// probeTimeout bounds the whole pre-flight request.
	probeTimeout = 30 * time.Second
)

// ValidateConfluentSchemaRegistryURL performs a pre-flight GET against the
// Schema Registry's /config endpoint (the same endpoint the scan's first call
// hits) and returns a clear, class-distinct error when the endpoint is not a
// usable Schema Registry. It honors the same auth options as the scan and
// returns nil only when the endpoint responds 2xx with a JSON body.
//
// It exists because confluent-kafka-go decodes any 2xx body as JSON without
// checking Content-Type; pointing --url at an HTML endpoint otherwise surfaces
// the opaque "invalid character '<' looking for beginning of value".
func ValidateConfluentSchemaRegistryURL(rawURL string, opts ...SchemaRegistryOption) error {
	cfg := resolveSchemaRegistryConfig(opts...)

	probeURL, err := buildConfigProbeURL(rawURL)
	if err != nil {
		return fmt.Errorf("invalid schema registry url %q: %w", rawURL, err)
	}

	req, err := http.NewRequest(http.MethodGet, probeURL, nil)
	if err != nil {
		return fmt.Errorf("invalid schema registry url %q: %w", rawURL, err)
	}
	if cfg.authType == types.SchemaRegistryAuthTypeBasicAuth {
		req.SetBasicAuth(cfg.username, cfg.password)
	}

	httpClient := &http.Client{
		Timeout: probeTimeout,
		// Refuse redirects: a real Schema Registry /config responds directly, so a
		// 3xx is surfaced as a class-distinct error rather than followed. This
		// blocks SSRF to internal/metadata addresses and stops the basic-auth
		// header from following a cross-scheme/host redirect.
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("could not reach schema registry at %s: %w", displayURL(rawURL), err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxProbeBodyBytes))

	switch {
	case resp.StatusCode >= 300 && resp.StatusCode < 400:
		return fmt.Errorf("schema registry at %s returned an unexpected redirect (HTTP %d to %s); a Schema Registry REST API responds directly, so this URL is being refused — verify --url",
			displayURL(rawURL), resp.StatusCode, sanitizeBodySnippet([]byte(resp.Header.Get("Location"))))

	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return fmt.Errorf("schema registry at %s rejected the request with an authentication error (HTTP %d); check --use-basic-auth / --username / --password",
			displayURL(rawURL), resp.StatusCode)

	case resp.StatusCode < 200 || resp.StatusCode >= 300:
		// A real Schema Registry reports errors as a JSON body
		// ({"error_code":..,"message":".."}) — that message is the useful part, so
		// surface it. A non-JSON body (an HTML error page from a proxy/web server)
		// is noise, so report only the status and point at the URL.
		if isJSONBody(body) {
			return fmt.Errorf("schema registry at %s returned HTTP %d: %s",
				displayURL(rawURL), resp.StatusCode, sanitizeBodySnippet(body))
		}
		return notSchemaRegistryError(rawURL, fmt.Sprintf("it returned HTTP %d with a non-JSON (%s) body",
			resp.StatusCode, contentTypeOrUnknown(resp.Header.Get("Content-Type"))))

	default:
		if !isJSONBody(body) {
			return notSchemaRegistryError(rawURL, fmt.Sprintf("it returned a non-JSON response (Content-Type: %s)",
				contentTypeOrUnknown(resp.Header.Get("Content-Type"))))
		}
		return nil
	}
}

// isJSONBody reports whether body parses as JSON. confluent-kafka-go decodes any
// body as JSON without this check, which is the root cause this pre-flight guards.
func isJSONBody(body []byte) bool {
	var raw json.RawMessage
	return json.Unmarshal(body, &raw) == nil
}

// notSchemaRegistryError builds the message shown when an endpoint responds but
// is not a Schema Registry REST API. It deliberately omits the response body —
// an HTML error/login page is noise — and gives the operator a next step.
func notSchemaRegistryError(rawURL, detail string) error {
	return fmt.Errorf("%s does not look like a Schema Registry REST API: %s; verify --url points to the Schema Registry endpoint",
		displayURL(rawURL), detail)
}

// buildConfigProbeURL appends the Schema Registry global config path to the
// user-supplied base URL, tolerating a trailing slash.
func buildConfigProbeURL(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/config"
	return u.String(), nil
}

// displayURL renders a scheme://host form of the URL for error messages, which
// avoids echoing any userinfo, path, or query the caller may have supplied.
func displayURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return raw
	}
	return u.Scheme + "://" + u.Host
}

func contentTypeOrUnknown(ct string) string {
	if ct == "" {
		return "unknown"
	}
	return ct
}

// sanitizeBodySnippet makes a response-body fragment safe to embed in an error
// message and log line: control characters (including newlines and tabs) are
// replaced with spaces, runs of whitespace are collapsed, the result is trimmed,
// and it is truncated to a bounded length with an ellipsis. This guards against
// log injection and oversized error output from a hostile or wrong endpoint.
func sanitizeBodySnippet(body []byte) string {
	var b strings.Builder
	for _, r := range string(body) {
		if unicode.IsControl(r) {
			b.WriteRune(' ')
		} else {
			b.WriteRune(r)
		}
	}

	collapsed := strings.Join(strings.Fields(b.String()), " ")

	runes := []rune(collapsed)
	if len(runes) > maxSnippetRunes {
		return string(runes[:maxSnippetRunes]) + "…"
	}
	return collapsed
}
