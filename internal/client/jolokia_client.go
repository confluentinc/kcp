package client

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

// JolokiaClient is an HTTP client for querying Jolokia REST endpoints
type JolokiaClient struct {
	baseURL    string
	httpClient *http.Client
	username   string
	password   string
}

// JolokiaOption is a functional option for configuring the JolokiaClient
type JolokiaOption func(*JolokiaClient)

// WithJolokiaBasicAuth configures the client to use HTTP basic authentication
func WithJolokiaBasicAuth(username, password string) JolokiaOption {
	return func(c *JolokiaClient) {
		c.username = username
		c.password = password
	}
}

// WithJolokiaTLS configures the client to use TLS with an optional custom CA certificate
// and optional insecure skip verify for self-signed certificates
func WithJolokiaTLS(caCertFile string, insecureSkipVerify bool) JolokiaOption {
	return func(c *JolokiaClient) {
		tlsConfig := &tls.Config{
			InsecureSkipVerify: insecureSkipVerify, //nolint:gosec // Only true when explicitly set for test environments
		}

		// Load custom CA certificate if provided
		if caCertFile != "" {
			caCert, err := os.ReadFile(caCertFile)
			if err == nil {
				caCertPool := x509.NewCertPool()
				if caCertPool.AppendCertsFromPEM(caCert) {
					tlsConfig.RootCAs = caCertPool
				}
			}
		}

		c.httpClient.Transport = &http.Transport{
			TLSClientConfig: tlsConfig,
		}
	}
}

// NewJolokiaClient creates a new Jolokia HTTP client
func NewJolokiaClient(baseURL string, opts ...JolokiaOption) *JolokiaClient {
	client := &JolokiaClient{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}

	// Apply all options
	for _, opt := range opts {
		opt(client)
	}

	return client
}

// jolokiaResponse represents the JSON response from Jolokia
type jolokiaResponse struct {
	Status int            `json:"status"`
	Value  map[string]any `json:"value,omitempty"`
	Error  string         `json:"error,omitempty"`
}

// ReadMBean queries a Jolokia endpoint for JMX MBean data
// mbeanPath is the MBean ObjectName (e.g., "kafka.server:type=BrokerTopicMetrics,name=BytesInPerSec")
// Returns the "value" map from the Jolokia response
func (c *JolokiaClient) ReadMBean(mbeanPath string) (map[string]any, error) {
	// Construct URL - do NOT URL-encode the mbean path
	// Jolokia expects the raw ObjectName format in the URL path
	url := fmt.Sprintf("%s/read/%s", c.baseURL, mbeanPath)

	// Create HTTP request
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Add basic auth if configured
	if c.username != "" || c.password != "" {
		req.SetBasicAuth(c.username, c.password)
	}

	// Make the request
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to make request: %w", err)
	}
	defer resp.Body.Close()

	// Read response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	// Handle HTTP-level errors (401, 404, 500, etc.)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP error: status %d: %s", resp.StatusCode, string(body))
	}

	// Parse JSON response
	var jolokiaResp jolokiaResponse
	if err := json.Unmarshal(body, &jolokiaResp); err != nil {
		return nil, fmt.Errorf("failed to parse JSON response: %w", err)
	}

	// Check Jolokia status (200 = success, other = error)
	if jolokiaResp.Status != 200 {
		return nil, fmt.Errorf("Jolokia error: status %d: %s", jolokiaResp.Status, jolokiaResp.Error)
	}

	return jolokiaResp.Value, nil
}

// ReadMBeanAggregate queries a wildcard MBean pattern and sums a numeric attribute
// across all matching MBeans. Used for metrics like log size (per-partition) and
// connection count (per-listener) that need to be aggregated.
func (c *JolokiaClient) ReadMBeanAggregate(mbeanPattern string, attribute string) (float64, error) {
	url := fmt.Sprintf("%s/read/%s/%s", c.baseURL, mbeanPattern, attribute)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return 0, fmt.Errorf("failed to create request: %w", err)
	}

	if c.username != "" || c.password != "" {
		req.SetBasicAuth(c.username, c.password)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("failed to make request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("HTTP error: status %d: %s", resp.StatusCode, string(body))
	}

	// Wildcard responses have value as map[mbeanName] -> attributeValue or map[mbeanName] -> map[attr] -> value
	var raw struct {
		Status int            `json:"status"`
		Value  map[string]any `json:"value"`
		Error  string         `json:"error,omitempty"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return 0, fmt.Errorf("failed to parse JSON response: %w", err)
	}

	if raw.Status != 200 {
		return 0, fmt.Errorf("Jolokia error: status %d: %s", raw.Status, raw.Error)
	}

	var total float64
	for _, val := range raw.Value {
		switch v := val.(type) {
		case float64:
			total += v
		case map[string]any:
			if attrVal, ok := v[attribute]; ok {
				if f, ok := attrVal.(float64); ok {
					total += f
				}
			}
		}
	}

	return total, nil
}
