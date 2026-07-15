package acls

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/confluentinc/kcp/internal/services/clusterlink"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/stretchr/testify/require"
)

// testAuth is the BasicAuth used across this file's tests, so every handler
// can assert the exact Authorization header value the client is expected to
// send (regression guard: NewACLClient must apply auth per request, since
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

func TestACLClient_Create_PostsCCWireShape(t *testing.T) {
	var gotPath string
	var gotBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requireAuthHeader(t, r)
		gotPath = r.URL.Path
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &gotBody)
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	client := NewACLClient(srv.URL, "lkc-123", srv.Client(), testAuth)

	err := client.Create(context.Background(), types.Acls{
		ResourceType:        "Topic",
		ResourceName:        "orders",
		ResourcePatternType: "Literal",
		Principal:           "User:sa-abc123",
		Host:                "*",
		Operation:           "Read",
		PermissionType:      "Allow",
	})
	require.NoError(t, err)

	require.Equal(t, "/kafka/v3/clusters/lkc-123/acls", gotPath)
	require.Equal(t, "TOPIC", gotBody["resource_type"])
	require.Equal(t, "orders", gotBody["resource_name"])
	require.Equal(t, "LITERAL", gotBody["pattern_type"])
	require.Equal(t, "User:sa-abc123", gotBody["principal"])
	require.Equal(t, "*", gotBody["host"])
	require.Equal(t, "READ", gotBody["operation"])
	require.Equal(t, "ALLOW", gotBody["permission"])
}

// TestACLClient_Create_UnknownOperation_ReturnsError confirms the highest-risk
// mapping path (canonical -> CC wire enum) fails loud on an out-of-map value
// instead of silently posting a zero-value operation.
func TestACLClient_Create_UnknownOperation_ReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("Create must not call the CC API when enum conversion fails")
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	client := NewACLClient(srv.URL, "lkc-123", srv.Client(), testAuth)

	err := client.Create(context.Background(), types.Acls{
		ResourceType:        "Topic",
		ResourceName:        "orders",
		ResourcePatternType: "Literal",
		Principal:           "User:sa-abc123",
		Host:                "*",
		Operation:           "BogusOp",
		PermissionType:      "Allow",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "BogusOp")
}

func TestACLClient_List_ParsesCCResponseToCanonical(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requireAuthHeader(t, r)
		require.Equal(t, "/kafka/v3/clusters/lkc-123/acls", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[
			{"resource_type":"TOPIC","resource_name":"orders","pattern_type":"LITERAL","principal":"User:sa-abc123","host":"*","operation":"READ","permission":"ALLOW"},
			{"resource_type":"TRANSACTIONAL_ID","resource_name":"tx-1","pattern_type":"LITERAL","principal":"User:sa-xyz","host":"*","operation":"IDEMPOTENT_WRITE","permission":"ALLOW"}
		]}`))
	}))
	defer srv.Close()

	client := NewACLClient(srv.URL, "lkc-123", srv.Client(), testAuth)

	got, err := client.List(context.Background())
	require.NoError(t, err)
	require.Len(t, got, 2)

	require.Equal(t, types.Acls{
		ResourceType:        "Topic",
		ResourceName:        "orders",
		ResourcePatternType: "Literal",
		Principal:           "User:sa-abc123",
		Host:                "*",
		Operation:           "Read",
		PermissionType:      "Allow",
	}, got[0])

	require.Equal(t, types.Acls{
		ResourceType:        "TransactionalID",
		ResourceName:        "tx-1",
		ResourcePatternType: "Literal",
		Principal:           "User:sa-xyz",
		Host:                "*",
		Operation:           "IdempotentWrite",
		PermissionType:      "Allow",
	}, got[1])
}

// TestEnumMapping_RoundTrip verifies every documented canonical<->CC wire
// enum value maps correctly in both directions, for all four enum
// dimensions (ResourceType, Operation, ResourcePatternType, PermissionType).
func TestEnumMapping_RoundTrip(t *testing.T) {
	resourceTypes := map[string]string{
		"Topic":           "TOPIC",
		"Group":           "GROUP",
		"Cluster":         "CLUSTER",
		"TransactionalID": "TRANSACTIONAL_ID",
	}
	for canonical, wire := range resourceTypes {
		got, ok := resourceTypeToWire[canonical]
		require.True(t, ok, "missing resourceTypeToWire entry for %q", canonical)
		require.Equal(t, wire, got)

		gotBack, ok := resourceTypeFromWire[wire]
		require.True(t, ok, "missing resourceTypeFromWire entry for %q", wire)
		require.Equal(t, canonical, gotBack)
	}

	operations := map[string]string{
		"Read":            "READ",
		"Write":           "WRITE",
		"Create":          "CREATE",
		"Delete":          "DELETE",
		"Alter":           "ALTER",
		"Describe":        "DESCRIBE",
		"ClusterAction":   "CLUSTER_ACTION",
		"DescribeConfigs": "DESCRIBE_CONFIGS",
		"AlterConfigs":    "ALTER_CONFIGS",
		"IdempotentWrite": "IDEMPOTENT_WRITE",
	}
	for canonical, wire := range operations {
		got, ok := operationToWire[canonical]
		require.True(t, ok, "missing operationToWire entry for %q", canonical)
		require.Equal(t, wire, got)

		gotBack, ok := operationFromWire[wire]
		require.True(t, ok, "missing operationFromWire entry for %q", wire)
		require.Equal(t, canonical, gotBack)
	}

	patternTypes := map[string]string{
		"Literal":  "LITERAL",
		"Prefixed": "PREFIXED",
	}
	for canonical, wire := range patternTypes {
		got, ok := patternTypeToWire[canonical]
		require.True(t, ok, "missing patternTypeToWire entry for %q", canonical)
		require.Equal(t, wire, got)

		gotBack, ok := patternTypeFromWire[wire]
		require.True(t, ok, "missing patternTypeFromWire entry for %q", wire)
		require.Equal(t, canonical, gotBack)
	}

	permissions := map[string]string{
		"Allow": "ALLOW",
		"Deny":  "DENY",
	}
	for canonical, wire := range permissions {
		got, ok := permissionToWire[canonical]
		require.True(t, ok, "missing permissionToWire entry for %q", canonical)
		require.Equal(t, wire, got)

		gotBack, ok := permissionFromWire[wire]
		require.True(t, ok, "missing permissionFromWire entry for %q", wire)
		require.Equal(t, canonical, gotBack)
	}
}
