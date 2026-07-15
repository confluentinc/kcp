package serviceaccounts

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/confluentinc/kcp/internal/services/clusterlink"
)

// DefaultBaseURL is the default Confluent Cloud API base URL used when no
// override is supplied.
const DefaultBaseURL = "https://api.confluent.cloud"

const serviceAccountsPath = "/iam/v2/service-accounts"

// ServiceAccount represents a Confluent Cloud IAM v2 service account.
type ServiceAccount struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	Description string `json:"description"`
}

// CCClient finds and creates Confluent Cloud IAM v2 service accounts.
type CCClient interface {
	// FindByDisplayName looks up a service account by its exact display
	// name. It returns nil (with no error) if no service account with that
	// name exists.
	FindByDisplayName(ctx context.Context, name string) (*ServiceAccount, error)

	// Create creates a new service account with the given display name and
	// description. If a service account with that display name already
	// exists (API returns 409), Create falls back to FindByDisplayName and
	// returns the existing account instead of erroring.
	Create(ctx context.Context, name, description string) (*ServiceAccount, error)
}

// ccClient implements CCClient against the Confluent Cloud IAM v2 REST API.
type ccClient struct {
	base string
	hc   clusterlink.HTTPClient
	auth clusterlink.Authenticator
}

// NewCCClient creates a CCClient. base is the Confluent Cloud API base URL
// (e.g. https://api.confluent.cloud); hc is the HTTP client (transport only —
// it carries no auth of its own); auth is applied to every outgoing request,
// mirroring how internal/services/clusterlink applies config.authenticator()
// per request rather than baking auth into the transport.
func NewCCClient(base string, hc clusterlink.HTTPClient, auth clusterlink.Authenticator) *ccClient {
	return &ccClient{base: base, hc: hc, auth: auth}
}

// FindByDisplayName implements CCClient.
func (c *ccClient) FindByDisplayName(ctx context.Context, name string) (*ServiceAccount, error) {
	path := serviceAccountsPath + "?display_name=" + url.QueryEscape(name)

	var response struct {
		Data []ServiceAccount `json:"data"`
	}
	if err := c.doRequest(ctx, http.MethodGet, path, nil, &response); err != nil {
		return nil, fmt.Errorf("failed to find service account %q: %w", name, err)
	}

	if len(response.Data) == 0 {
		return nil, nil
	}
	return &response.Data[0], nil
}

// Create implements CCClient.
func (c *ccClient) Create(ctx context.Context, name, description string) (*ServiceAccount, error) {
	reqBody := struct {
		DisplayName string `json:"display_name"`
		Description string `json:"description"`
	}{
		DisplayName: name,
		Description: description,
	}

	var sa ServiceAccount
	err := c.doRequest(ctx, http.MethodPost, serviceAccountsPath, reqBody, &sa)
	if err == nil {
		return &sa, nil
	}

	var statusErr *httpStatusError
	if errors.As(err, &statusErr) && statusErr.StatusCode == http.StatusConflict {
		existing, findErr := c.FindByDisplayName(ctx, name)
		if findErr != nil {
			return nil, fmt.Errorf("looking up existing service account %q after 409 conflict: %w", name, findErr)
		}
		if existing == nil {
			return nil, fmt.Errorf("service account %q reported as already in use (409) but was not found on lookup", name)
		}
		return existing, nil
	}

	return nil, fmt.Errorf("failed to create service account %q: %w", name, err)
}

// httpStatusError is returned by doRequest when the server responds with a
// non-success status code. Mirrors the equivalent type in
// internal/services/clusterlink/clusterlink.go.
type httpStatusError struct {
	StatusCode int
	Body       string
}

func (e *httpStatusError) Error() string {
	return fmt.Sprintf("unexpected status code %d: %s", e.StatusCode, e.Body)
}

// doRequest performs an authenticated HTTP request against the Confluent
// Cloud IAM v2 API. body, when non-nil, is marshaled as the JSON request
// body. result, when non-nil, receives the unmarshaled JSON response body.
// Non-2xx responses are wrapped in an *httpStatusError including the status
// code and response body.
func (c *ccClient) doRequest(ctx context.Context, method, path string, body, result any) error {
	var bodyReader io.Reader
	if body != nil {
		jsonBody, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("failed to marshal request body: %w", err)
		}
		bodyReader = bytes.NewBuffer(jsonBody)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.base+path, bodyReader)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	c.auth.Apply(req)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	res, err := c.hc.Do(req)
	if err != nil {
		return fmt.Errorf("failed to execute request: %w", err)
	}
	defer func() { _ = res.Body.Close() }()

	respBody, err := io.ReadAll(res.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return &httpStatusError{StatusCode: res.StatusCode, Body: string(respBody)}
	}

	if len(respBody) > 0 && result != nil {
		if err := json.Unmarshal(respBody, result); err != nil {
			return fmt.Errorf("failed to unmarshal response: %w", err)
		}
	}

	return nil
}
