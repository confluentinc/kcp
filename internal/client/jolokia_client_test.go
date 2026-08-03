package client

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestJolokiaClient_ReadMBean_Success(t *testing.T) {
	// Create a test server that returns a valid Jolokia response
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/read/kafka.server:type=BrokerTopicMetrics,name=BytesInPerSec", r.URL.Path)
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
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	client := NewJolokiaClient(server.URL)
	values, err := client.ReadMBean(context.Background(), "kafka.server:type=BrokerTopicMetrics,name=BytesInPerSec")

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
			_ = json.NewEncoder(w).Encode(map[string]any{
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
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	// Test without auth - should fail
	clientNoAuth := NewJolokiaClient(server.URL)
	_, err := clientNoAuth.ReadMBean(context.Background(), "test:type=Test")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "status 401")

	// Test with auth - should succeed
	clientWithAuth := NewJolokiaClient(server.URL, WithJolokiaBasicAuth(expectedUsername, expectedPassword))
	values, err := clientWithAuth.ReadMBean(context.Background(), "test:type=Test")
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
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	client := NewJolokiaClient(server.URL)
	_, err := client.ReadMBean(context.Background(), "kafka.server:type=Invalid")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "jolokia error")
	assert.Contains(t, err.Error(), "404")
	assert.Contains(t, err.Error(), "javax.management.InstanceNotFoundException")
	assert.True(t, errors.Is(err, ErrJolokiaMBeanNotFound), "404/InstanceNotFoundException should wrap ErrJolokiaMBeanNotFound")
}

// TestJolokiaClient_ReadMBean_NotFoundWrapsSentinel is a regression test for the
// noisy-warning fix: a 404/InstanceNotFoundException response (e.g. reading a
// sink-task MBean pattern on a source-only Connect cluster) must be
// distinguishable via errors.Is(err, ErrJolokiaMBeanNotFound) from other
// failures like timeouts, so callers can log it at Debug instead of Warn.
func TestJolokiaClient_ReadMBean_NotFoundWrapsSentinel(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := map[string]any{
			"status": 404,
			"error":  "javax.management.InstanceNotFoundException: kafka.connect:type=sink-task-metrics,connector=*,task=*",
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	client := NewJolokiaClient(server.URL)
	_, err := client.ReadMBean(context.Background(), "kafka.connect:type=sink-task-metrics,connector=*,task=*")

	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrJolokiaMBeanNotFound))
}

func TestJolokiaClient_ReadMBean_ConnectionRefused(t *testing.T) {
	// Connect to a port that's not listening
	client := NewJolokiaClient("http://localhost:1")
	_, err := client.ReadMBean(context.Background(), "test:type=Test")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to make request")
}

func TestJolokiaClient_ReadMBean_URLEncoding(t *testing.T) {
	// Verify that MBean paths are NOT URL-encoded (Jolokia expects raw ObjectName format)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The path should contain literal colons and commas, not %3A and %2C
		expectedPath := "/read/kafka.server:type=BrokerTopicMetrics,name=BytesInPerSec"
		assert.Equal(t, expectedPath, r.URL.Path)
		assert.NotContains(t, r.URL.Path, "%3A") // colon should not be encoded
		assert.NotContains(t, r.URL.Path, "%2C") // comma should not be encoded

		response := map[string]any{
			"status": 200,
			"value":  map[string]any{"test": "ok"},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	client := NewJolokiaClient(server.URL)
	_, err := client.ReadMBean(context.Background(), "kafka.server:type=BrokerTopicMetrics,name=BytesInPerSec")
	require.NoError(t, err)
}

func TestJolokiaClient_ReadMBean_WithTLS(t *testing.T) {
	// Create a TLS test server
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": 200,
			"value":  map[string]any{"Count": 12345.0},
		})
	}))
	defer server.Close()

	// Without TLS config — should fail (self-signed cert)
	clientNoTLS := NewJolokiaClient(server.URL)
	_, err := clientNoTLS.ReadMBean(context.Background(), "test:type=Test")
	require.Error(t, err)

	// With insecure skip verify — should succeed
	clientInsecure := NewJolokiaClient(server.URL, WithJolokiaTLS(nil, true))
	values, err := clientInsecure.ReadMBean(context.Background(), "test:type=Test")
	require.NoError(t, err)
	assert.Equal(t, 12345.0, values["Count"])
}

func TestJolokiaClient_ReadMBean_WithTLSAndAuth(t *testing.T) {
	// TLS server that also requires basic auth
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != "monitor" || pass != "secret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": 200,
			"value":  map[string]any{"Count": 99.0},
		})
	}))
	defer server.Close()

	// TLS only, no auth — should fail 401
	clientTLSOnly := NewJolokiaClient(server.URL, WithJolokiaTLS(nil, true))
	_, err := clientTLSOnly.ReadMBean(context.Background(), "test:type=Test")
	require.Error(t, err)

	// TLS + auth — should succeed
	clientBoth := NewJolokiaClient(server.URL,
		WithJolokiaTLS(nil, true),
		WithJolokiaBasicAuth("monitor", "secret"),
	)
	values, err := clientBoth.ReadMBean(context.Background(), "test:type=Test")
	require.NoError(t, err)
	assert.Equal(t, 99.0, values["Count"])
}

func TestJolokiaClient_ReadMBean_WithTLSCustomCA(t *testing.T) {
	// Create TLS server
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": 200,
			"value":  map[string]any{"Value": 42.0},
		})
	}))
	defer server.Close()

	// Extract the test server's CA cert and create a client that trusts it
	certPool := x509.NewCertPool()
	certPool.AddCert(server.Certificate())

	client := NewJolokiaClient(server.URL)
	client.httpClient.Transport = &http.Transport{
		TLSClientConfig: &tls.Config{
			RootCAs: certPool,
		},
	}

	values, err := client.ReadMBean(context.Background(), "test:type=Test")
	require.NoError(t, err)
	assert.Equal(t, 42.0, values["Value"])
}

func TestJolokiaClient_ReadMBeanAggregate_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Wildcard response: map of MBean names to attribute maps
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": 200,
			"value": map[string]any{
				"kafka.log:name=Size,partition=0,topic=test,type=Log": map[string]any{
					"Value": 1000.0,
				},
				"kafka.log:name=Size,partition=1,topic=test,type=Log": map[string]any{
					"Value": 2000.0,
				},
				"kafka.log:name=Size,partition=2,topic=test,type=Log": map[string]any{
					"Value": 3000.0,
				},
			},
		})
	}))
	defer server.Close()

	client := NewJolokiaClient(server.URL)
	total, err := client.ReadMBeanAggregate(context.Background(), "kafka.log:type=Log,name=Size,*", "Value")

	require.NoError(t, err)
	assert.Equal(t, 6000.0, total) // 1000 + 2000 + 3000
}

func TestJolokiaClient_ReadMBeanAggregate_DirectValues(t *testing.T) {
	// Some wildcard responses return values directly (not nested in attribute map)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": 200,
			"value": map[string]any{
				"kafka.server:listener=PLAIN,networkProcessor=0": 5.0,
				"kafka.server:listener=PLAIN,networkProcessor=1": 3.0,
			},
		})
	}))
	defer server.Close()

	client := NewJolokiaClient(server.URL)
	total, err := client.ReadMBeanAggregate(context.Background(), "kafka.server:type=socket-server-metrics,*", "connection-count")

	require.NoError(t, err)
	assert.Equal(t, 8.0, total) // 5 + 3
}

func TestJolokiaClient_ReadMBeanAggregate_WithAuth(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != "admin" || pass != "pass" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": 200,
			"value": map[string]any{
				"mbean1": map[string]any{"Value": 100.0},
			},
		})
	}))
	defer server.Close()

	// Without auth — should fail
	clientNoAuth := NewJolokiaClient(server.URL)
	_, err := clientNoAuth.ReadMBeanAggregate(context.Background(), "test:*", "Value")
	require.Error(t, err)

	// With auth — should succeed
	clientAuth := NewJolokiaClient(server.URL, WithJolokiaBasicAuth("admin", "pass"))
	total, err := clientAuth.ReadMBeanAggregate(context.Background(), "test:*", "Value")
	require.NoError(t, err)
	assert.Equal(t, 100.0, total)
}

func TestJolokiaClient_ReadMBeanAggregate_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": 404,
			"error":  "javax.management.InstanceNotFoundException",
		})
	}))
	defer server.Close()

	client := NewJolokiaClient(server.URL)
	_, err := client.ReadMBeanAggregate(context.Background(), "nonexistent:*", "Value")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "InstanceNotFoundException")
	assert.True(t, errors.Is(err, ErrJolokiaMBeanNotFound), "404/InstanceNotFoundException should wrap ErrJolokiaMBeanNotFound")
}

// TestJolokiaClient_ReadMBeanAggregate_ConnectionRefusedNotSentinel is a
// regression guard: real failures (connection refused, timeout, auth) must
// NOT be classified as ErrJolokiaMBeanNotFound, since collectRawSample logs
// those at Warn (not Debug) precisely because they indicate a real problem
// rather than an expected-absent metric.
func TestJolokiaClient_ReadMBeanAggregate_ConnectionRefusedNotSentinel(t *testing.T) {
	client := NewJolokiaClient("http://localhost:1")
	_, err := client.ReadMBeanAggregate(context.Background(), "test:*", "Value")

	require.Error(t, err)
	assert.False(t, errors.Is(err, ErrJolokiaMBeanNotFound), "connection errors must not be classified as MBean-not-found")
}

func TestJolokiaClient_ReadMBeanAggregateByLabel_NotFoundWrapsSentinel(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": 404,
			"error":  "javax.management.InstanceNotFoundException: kafka.connect:type=sink-task-metrics,connector=*,task=*",
		})
	}))
	defer server.Close()

	client := NewJolokiaClient(server.URL)
	_, err := client.ReadMBeanAggregateByLabel(context.Background(),
		"kafka.connect:type=sink-task-metrics,connector=*,task=*", "sink-record-read-rate", "connector")

	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrJolokiaMBeanNotFound), "404/InstanceNotFoundException should wrap ErrJolokiaMBeanNotFound")
}

func TestReadMBeanAggregateByLabel_GroupsByConnector(t *testing.T) {
	body := `{"status":200,"value":{
		"kafka.connect:type=source-task-metrics,connector=c1,task=0":{"source-record-write-rate":1.0},
		"kafka.connect:type=source-task-metrics,connector=c1,task=1":{"source-record-write-rate":2.0},
		"kafka.connect:type=source-task-metrics,connector=c2,task=0":{"source-record-write-rate":5.0}
	}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()
	c := NewJolokiaClient(srv.URL)
	got, err := c.ReadMBeanAggregateByLabel(context.Background(),
		"kafka.connect:type=source-task-metrics,connector=*,task=*", "source-record-write-rate", "connector")
	if err != nil {
		t.Fatal(err)
	}
	if got["c1"] != 3.0 || got["c2"] != 5.0 || len(got) != 2 {
		t.Fatalf("group-by-connector wrong: %+v", got)
	}
}

func TestReadMBeanAggregateByLabel_DirectValues(t *testing.T) {
	// Jolokia wildcard response where values are bare numbers (not nested attribute maps)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": 200,
			"value": map[string]any{
				"kafka.connect:type=source-task-metrics,connector=c1,task=0": 4.0,
				"kafka.connect:type=source-task-metrics,connector=c1,task=1": 6.0,
				"kafka.connect:type=source-task-metrics,connector=c2,task=0": 2.0,
			},
		})
	}))
	defer server.Close()

	client := NewJolokiaClient(server.URL)
	got, err := client.ReadMBeanAggregateByLabel(context.Background(),
		"kafka.connect:type=source-task-metrics,connector=*,task=*", "source-record-write-rate", "connector")

	require.NoError(t, err)
	assert.Equal(t, 10.0, got["c1"])
	assert.Equal(t, 2.0, got["c2"])
	assert.Equal(t, 2, len(got))
}

func TestReadMBeanAggregateByLabel_SkipsMissingLabel(t *testing.T) {
	// Response with mixed MBeans: some with connector property, some without
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": 200,
			"value": map[string]any{
				"kafka.connect:type=source-task-metrics,connector=c1,task=0": map[string]any{
					"source-record-write-rate": 5.0,
				},
				"kafka.connect:type=connect-worker-metrics": map[string]any{
					"source-record-write-rate": 9.0,
				},
				"kafka.connect:type=source-task-metrics,connector=c2,task=0": map[string]any{
					"source-record-write-rate": 3.0,
				},
			},
		})
	}))
	defer server.Close()

	client := NewJolokiaClient(server.URL)
	got, err := client.ReadMBeanAggregateByLabel(context.Background(),
		"kafka.connect:type=*", "source-record-write-rate", "connector")

	require.NoError(t, err)
	assert.Equal(t, 5.0, got["c1"])
	assert.Equal(t, 3.0, got["c2"])
	assert.Equal(t, 2, len(got))
	// Verify no empty key (MBean without connector property should be skipped)
	assert.NotContains(t, got, "")
}
