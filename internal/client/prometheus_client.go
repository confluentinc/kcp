package client

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"time"
)

// PrometheusClient is an HTTP client for querying the Prometheus API
type PrometheusClient struct {
	baseURL    string
	httpClient *http.Client
	username   string
	password   string
}

// PrometheusOption is a functional option for configuring the PrometheusClient
type PrometheusOption func(*PrometheusClient)

// WithPrometheusBasicAuth configures the client to use HTTP basic authentication
func WithPrometheusBasicAuth(username, password string) PrometheusOption {
	return func(c *PrometheusClient) {
		c.username = username
		c.password = password
	}
}

// WithPrometheusTLS configures the client to use TLS with an optional custom CA certificate
func WithPrometheusTLS(caCertFile string, insecureSkipVerify bool) PrometheusOption {
	return func(c *PrometheusClient) {
		tlsConfig := &tls.Config{
			InsecureSkipVerify: insecureSkipVerify, //nolint:gosec // Only true when explicitly set for test environments
		}

		if caCertFile != "" {
			caCert, err := os.ReadFile(caCertFile)
			if err != nil {
				slog.Warn("failed to read Prometheus CA certificate file, proceeding without custom CA", "path", caCertFile, "error", err)
			} else {
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

// NewPrometheusClient creates a new Prometheus HTTP client
func NewPrometheusClient(baseURL string, opts ...PrometheusOption) *PrometheusClient {
	client := &PrometheusClient{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
	for _, opt := range opts {
		opt(client)
	}
	return client
}

// PrometheusMetricResult holds the parsed result for a single metric from a range query
type PrometheusMetricResult struct {
	MetricName string
	Values     []PrometheusDataPoint
}

// PrometheusDataPoint is a single timestamp+value pair from a Prometheus query
type PrometheusDataPoint struct {
	Timestamp time.Time
	Value     float64
}

// prometheusAPIResponse is the top-level JSON envelope from the Prometheus HTTP API
type prometheusAPIResponse struct {
	Status string                 `json:"status"`
	Data   prometheusResponseData `json:"data"`
	Error  string                 `json:"error,omitempty"`
}

// prometheusResponseData holds the data portion of a Prometheus API response
type prometheusResponseData struct {
	ResultType string                    `json:"resultType"`
	Result     []prometheusMatrixResult  `json:"result"`
}

// prometheusMatrixResult is a single time series in a matrix response
type prometheusMatrixResult struct {
	Metric map[string]string `json:"metric"`
	Values [][]interface{}   `json:"values"`
}

// QueryRange executes a Prometheus range query and returns parsed results
func (c *PrometheusClient) QueryRange(ctx context.Context, query string, start, end time.Time, step time.Duration) ([]PrometheusMetricResult, error) {
	params := url.Values{}
	params.Set("query", query)
	params.Set("start", fmt.Sprintf("%d", start.Unix()))
	params.Set("end", fmt.Sprintf("%d", end.Unix()))
	params.Set("step", fmt.Sprintf("%d", int(step.Seconds())))

	reqURL := fmt.Sprintf("%s/api/v1/query_range?%s", c.baseURL, params.Encode())
	req, err := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	if c.username != "" {
		req.SetBasicAuth(c.username, c.password)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to query Prometheus: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Prometheus returned status %d: %s", resp.StatusCode, string(body))
	}

	var apiResp prometheusAPIResponse
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return nil, fmt.Errorf("failed to parse Prometheus response: %w", err)
	}

	if apiResp.Status != "success" {
		return nil, fmt.Errorf("Prometheus query failed: %s", apiResp.Error)
	}

	return parseMatrixResults(apiResp.Data.Result)
}

// parseMatrixResults converts raw Prometheus matrix results into typed results
func parseMatrixResults(raw []prometheusMatrixResult) ([]PrometheusMetricResult, error) {
	results := make([]PrometheusMetricResult, 0, len(raw))

	for _, r := range raw {
		metricName := r.Metric["__name__"]
		points := make([]PrometheusDataPoint, 0, len(r.Values))

		for _, v := range r.Values {
			if len(v) != 2 {
				continue
			}

			ts, ok := v[0].(float64)
			if !ok {
				continue
			}

			valStr, ok := v[1].(string)
			if !ok {
				continue
			}

			val, err := strconv.ParseFloat(valStr, 64)
			if err != nil {
				continue
			}

			if math.IsNaN(val) || math.IsInf(val, 0) {
				continue
			}

			points = append(points, PrometheusDataPoint{
				Timestamp: time.Unix(int64(ts), 0),
				Value:     val,
			})
		}

		results = append(results, PrometheusMetricResult{
			MetricName: metricName,
			Values:     points,
		})
	}

	return results, nil
}
