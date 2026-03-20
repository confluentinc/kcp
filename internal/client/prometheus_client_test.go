package client

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPrometheusClient_QueryRange_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v1/query_range", r.URL.Path)
		assert.Equal(t, "test_metric", r.URL.Query().Get("query"))

		resp := prometheusAPIResponse{
			Status: "success",
			Data: prometheusResponseData{
				ResultType: "matrix",
				Result: []prometheusMatrixResult{
					{
						Metric: map[string]string{"__name__": "test_metric"},
						Values: [][]interface{}{
							{float64(1710000000), "1234.5"},
							{float64(1710003600), "1300.2"},
						},
					},
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewPrometheusClient(server.URL)
	start := time.Unix(1710000000, 0)
	end := time.Unix(1710007200, 0)

	results, err := client.QueryRange("test_metric", start, end, time.Hour)
	require.NoError(t, err)
	require.Len(t, results, 1)

	assert.Equal(t, "test_metric", results[0].MetricName)
	assert.Len(t, results[0].Values, 2)
	assert.Equal(t, time.Unix(1710000000, 0), results[0].Values[0].Timestamp)
	assert.InDelta(t, 1234.5, results[0].Values[0].Value, 0.01)
	assert.Equal(t, time.Unix(1710003600, 0), results[0].Values[1].Timestamp)
	assert.InDelta(t, 1300.2, results[0].Values[1].Value, 0.01)
}

func TestPrometheusClient_QueryRange_WithBasicAuth(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != "promuser" || pass != "prompass" {
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]string{"status": "error", "error": "unauthorized"})
			return
		}

		resp := prometheusAPIResponse{
			Status: "success",
			Data: prometheusResponseData{
				ResultType: "matrix",
				Result: []prometheusMatrixResult{
					{
						Metric: map[string]string{"__name__": "test_metric"},
						Values: [][]interface{}{
							{float64(1710000000), "100.0"},
						},
					},
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	// Without auth — should fail
	noAuthClient := NewPrometheusClient(server.URL)
	_, err := noAuthClient.QueryRange("test_metric", time.Now().Add(-time.Hour), time.Now(), time.Minute)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "401")

	// With auth — should succeed
	authClient := NewPrometheusClient(server.URL, WithPrometheusBasicAuth("promuser", "prompass"))
	results, err := authClient.QueryRange("test_metric", time.Now().Add(-time.Hour), time.Now(), time.Minute)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.InDelta(t, 100.0, results[0].Values[0].Value, 0.01)
}

func TestPrometheusClient_QueryRange_WithTLS(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := prometheusAPIResponse{
			Status: "success",
			Data: prometheusResponseData{
				ResultType: "matrix",
				Result:     []prometheusMatrixResult{},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewPrometheusClient(server.URL, WithPrometheusTLS("", true))
	results, err := client.QueryRange("test_metric", time.Now().Add(-time.Hour), time.Now(), time.Minute)
	require.NoError(t, err)
	assert.Empty(t, results)
}

func TestPrometheusClient_QueryRange_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("internal server error"))
	}))
	defer server.Close()

	client := NewPrometheusClient(server.URL)
	_, err := client.QueryRange("test_metric", time.Now().Add(-time.Hour), time.Now(), time.Minute)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "500")
}

func TestPrometheusClient_QueryRange_EmptyResult(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := prometheusAPIResponse{
			Status: "success",
			Data: prometheusResponseData{
				ResultType: "matrix",
				Result:     []prometheusMatrixResult{},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewPrometheusClient(server.URL)
	results, err := client.QueryRange("test_metric", time.Now().Add(-time.Hour), time.Now(), time.Minute)
	require.NoError(t, err)
	assert.Empty(t, results)
}

func TestPrometheusClient_QueryRange_ConnectionRefused(t *testing.T) {
	client := NewPrometheusClient("http://localhost:19999")
	_, err := client.QueryRange("test_metric", time.Now().Add(-time.Hour), time.Now(), time.Minute)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to query Prometheus")
}

func TestPrometheusClient_QueryRange_ErrorStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := prometheusAPIResponse{
			Status: "error",
			Error:  "bad_data: invalid query",
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewPrometheusClient(server.URL)
	_, err := client.QueryRange("invalid{", time.Now().Add(-time.Hour), time.Now(), time.Minute)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "Prometheus query failed")
}
