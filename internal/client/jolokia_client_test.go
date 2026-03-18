package client

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestJolokiaClient_ReadMBean_Success(t *testing.T) {
	// Create a test server that returns a valid Jolokia response
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/jolokia/read/kafka.server:type=BrokerTopicMetrics,name=BytesInPerSec", r.URL.Path)
		assert.Equal(t, "GET", r.Method)

		response := map[string]any{
			"status": 200,
			"value": map[string]any{
				"OneMinuteRate":  1234.5,
				"FiveMinuteRate": 1100.2,
				"Count":          float64(99999),
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	client := NewJolokiaClient(server.URL)
	values, err := client.ReadMBean("kafka.server:type=BrokerTopicMetrics,name=BytesInPerSec")

	require.NoError(t, err)
	assert.Equal(t, 1234.5, values["OneMinuteRate"])
	assert.Equal(t, 1100.2, values["FiveMinuteRate"])
	assert.Equal(t, float64(99999), values["Count"])
}

func TestJolokiaClient_ReadMBean_WithJolokiaBasicAuth(t *testing.T) {
	expectedUsername := "admin"
	expectedPassword := "secret"

	// Create a test server that requires basic auth
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		username, password, ok := r.BasicAuth()
		if !ok || username != expectedUsername || password != expectedPassword {
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]any{
				"status": 401,
				"error":  "Authentication required",
			})
			return
		}

		response := map[string]any{
			"status": 200,
			"value": map[string]any{
				"test": "authenticated",
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	// Test without auth - should fail
	clientNoAuth := NewJolokiaClient(server.URL)
	_, err := clientNoAuth.ReadMBean("test:type=Test")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "status 401")

	// Test with auth - should succeed
	clientWithAuth := NewJolokiaClient(server.URL, WithJolokiaBasicAuth(expectedUsername, expectedPassword))
	values, err := clientWithAuth.ReadMBean("test:type=Test")
	require.NoError(t, err)
	assert.Equal(t, "authenticated", values["test"])
}

func TestJolokiaClient_ReadMBean_ServerError(t *testing.T) {
	// Create a test server that returns a Jolokia error
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := map[string]any{
			"status": 404,
			"error":  "javax.management.InstanceNotFoundException: kafka.server:type=Invalid",
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	client := NewJolokiaClient(server.URL)
	_, err := client.ReadMBean("kafka.server:type=Invalid")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "Jolokia error")
	assert.Contains(t, err.Error(), "404")
	assert.Contains(t, err.Error(), "javax.management.InstanceNotFoundException")
}

func TestJolokiaClient_ReadMBean_ConnectionRefused(t *testing.T) {
	// Connect to a port that's not listening
	client := NewJolokiaClient("http://localhost:1")
	_, err := client.ReadMBean("test:type=Test")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to make request")
}

func TestJolokiaClient_ReadMBean_URLEncoding(t *testing.T) {
	// Verify that MBean paths are NOT URL-encoded (Jolokia expects raw ObjectName format)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The path should contain literal colons and commas, not %3A and %2C
		expectedPath := "/jolokia/read/kafka.server:type=BrokerTopicMetrics,name=BytesInPerSec"
		assert.Equal(t, expectedPath, r.URL.Path)
		assert.NotContains(t, r.URL.Path, "%3A") // colon should not be encoded
		assert.NotContains(t, r.URL.Path, "%2C") // comma should not be encoded

		response := map[string]any{
			"status": 200,
			"value":  map[string]any{"test": "ok"},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	client := NewJolokiaClient(server.URL)
	_, err := client.ReadMBean("kafka.server:type=BrokerTopicMetrics,name=BytesInPerSec")
	require.NoError(t, err)
}
