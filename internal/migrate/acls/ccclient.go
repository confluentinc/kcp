package acls

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/confluentinc/kcp/internal/services/clusterlink"
	"github.com/confluentinc/kcp/internal/types"
)

// ACLClient reads and writes ACLs on a Confluent Kafka REST v3 target
// (Confluent Cloud or Confluent Platform) via /kafka/v3/clusters/{id}/acls.
//
// It is the SINGLE boundary that converts between the canonical titlecase
// types.Acls shape used everywhere else in KCP (ReadNativeACLs,
// NormalizeForCC) and the SCREAMING_SNAKE_CASE wire enums the REST v3 ACL
// API expects. Callers never see or produce CC's wire enums directly.
type ACLClient interface {
	// List returns every ACL on the cluster (GET .../acls), translated from
	// CC's wire enums to canonical titlecase form.
	List(ctx context.Context) ([]types.Acls, error)
	// Create adds one ACL (POST .../acls). a is in canonical form; Create
	// translates it to CC's wire enums before sending.
	Create(ctx context.Context, a types.Acls) error
}

// aclClient implements ACLClient against the Kafka REST v3 ACL API.
type aclClient struct {
	restEndpoint string
	clusterID    string
	hc           clusterlink.HTTPClient
	auth         clusterlink.Authenticator
}

// NewACLClient builds an ACLClient. hc supplies the transport only (e.g. an
// *http.Client with TLS trust configured, or srv.Client() in tests) — it
// carries no auth of its own; auth is applied to every outgoing request,
// mirroring how internal/services/clusterlink applies config.authenticator()
// per request rather than baking auth into the transport.
func NewACLClient(restEndpoint, clusterID string, hc clusterlink.HTTPClient, auth clusterlink.Authenticator) *aclClient {
	return &aclClient{restEndpoint: restEndpoint, clusterID: clusterID, hc: hc, auth: auth}
}

func (c *aclClient) path() string {
	return fmt.Sprintf("/kafka/v3/clusters/%s/acls", url.PathEscape(c.clusterID))
}

// wireACL is the SCREAMING_SNAKE_CASE JSON shape of a single ACL entry, used
// both inside the GET .../acls response ("data" array) and as the POST
// .../acls request body.
type wireACL struct {
	ResourceType string `json:"resource_type"`
	ResourceName string `json:"resource_name"`
	PatternType  string `json:"pattern_type"`
	Principal    string `json:"principal"`
	Host         string `json:"host"`
	Operation    string `json:"operation"`
	Permission   string `json:"permission"`
}

// Canonical (titlecase, sarama .String() form) <-> CC wire (SCREAMING_SNAKE)
// enum mappings, one map per ACL enum dimension. Each *ToWire map is the
// source of truth; the corresponding *FromWire map is its exact inverse
// (built by invert below), so the two directions can never drift apart.

var resourceTypeToWire = map[string]string{
	"Topic":           "TOPIC",
	"Group":           "GROUP",
	"Cluster":         "CLUSTER",
	"TransactionalID": "TRANSACTIONAL_ID",
}

var operationToWire = map[string]string{
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

var patternTypeToWire = map[string]string{
	"Literal":  "LITERAL",
	"Prefixed": "PREFIXED",
}

var permissionToWire = map[string]string{
	"Allow": "ALLOW",
	"Deny":  "DENY",
}

var (
	resourceTypeFromWire = invert(resourceTypeToWire)
	operationFromWire    = invert(operationToWire)
	patternTypeFromWire  = invert(patternTypeToWire)
	permissionFromWire   = invert(permissionToWire)
)

// invert returns the reverse of a 1:1 string map.
func invert(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[v] = k
	}
	return out
}

// fromCanonical translates a canonical types.Acls into CC's wire shape.
// Fields that pass through unchanged (Principal, Host, ResourceName) are
// copied verbatim; the four enum fields go through the *ToWire maps.
func fromCanonical(a types.Acls) (wireACL, error) {
	resourceType, ok := resourceTypeToWire[a.ResourceType]
	if !ok {
		return wireACL{}, fmt.Errorf("unknown ACL resource type %q", a.ResourceType)
	}
	patternType, ok := patternTypeToWire[a.ResourcePatternType]
	if !ok {
		return wireACL{}, fmt.Errorf("unknown ACL resource pattern type %q", a.ResourcePatternType)
	}
	operation, ok := operationToWire[a.Operation]
	if !ok {
		return wireACL{}, fmt.Errorf("unknown ACL operation %q", a.Operation)
	}
	permission, ok := permissionToWire[a.PermissionType]
	if !ok {
		return wireACL{}, fmt.Errorf("unknown ACL permission type %q", a.PermissionType)
	}

	return wireACL{
		ResourceType: resourceType,
		ResourceName: a.ResourceName,
		PatternType:  patternType,
		Principal:    a.Principal,
		Host:         a.Host,
		Operation:    operation,
		Permission:   permission,
	}, nil
}

// toCanonical translates a CC wire ACL back into canonical types.Acls.
func (w wireACL) toCanonical() (types.Acls, error) {
	resourceType, ok := resourceTypeFromWire[w.ResourceType]
	if !ok {
		return types.Acls{}, fmt.Errorf("unknown CC ACL resource_type %q", w.ResourceType)
	}
	patternType, ok := patternTypeFromWire[w.PatternType]
	if !ok {
		return types.Acls{}, fmt.Errorf("unknown CC ACL pattern_type %q", w.PatternType)
	}
	operation, ok := operationFromWire[w.Operation]
	if !ok {
		return types.Acls{}, fmt.Errorf("unknown CC ACL operation %q", w.Operation)
	}
	permission, ok := permissionFromWire[w.Permission]
	if !ok {
		return types.Acls{}, fmt.Errorf("unknown CC ACL permission %q", w.Permission)
	}

	return types.Acls{
		ResourceType:        resourceType,
		ResourceName:        w.ResourceName,
		ResourcePatternType: patternType,
		Principal:           w.Principal,
		Host:                w.Host,
		Operation:           operation,
		PermissionType:      permission,
	}, nil
}

// List fetches every ACL on the cluster (GET .../acls) and translates each
// entry to canonical form.
func (c *aclClient) List(ctx context.Context) ([]types.Acls, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.restEndpoint+c.path(), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	c.auth.Apply(req)

	res, err := c.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute request: %w", err)
	}
	defer func() { _ = res.Body.Close() }()

	body, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code %d listing acls: %s", res.StatusCode, string(body))
	}

	var response struct {
		Data []wireACL `json:"data"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	out := make([]types.Acls, 0, len(response.Data))
	for _, w := range response.Data {
		acl, err := w.toCanonical()
		if err != nil {
			return nil, err
		}
		out = append(out, acl)
	}
	return out, nil
}

// Create adds one ACL (POST .../acls), translating from canonical to CC's
// wire enums before sending.
func (c *aclClient) Create(ctx context.Context, a types.Acls) error {
	w, err := fromCanonical(a)
	if err != nil {
		return err
	}

	jsonBody, err := json.Marshal(w)
	if err != nil {
		return fmt.Errorf("failed to marshal request body: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.restEndpoint+c.path(), bytes.NewBuffer(jsonBody))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	c.auth.Apply(req)
	req.Header.Set("Content-Type", "application/json")

	res, err := c.hc.Do(req)
	if err != nil {
		return fmt.Errorf("failed to execute request: %w", err)
	}
	defer func() { _, _ = io.Copy(io.Discard, res.Body); _ = res.Body.Close() }()

	switch res.StatusCode {
	case http.StatusOK, http.StatusCreated, http.StatusNoContent:
		return nil
	default:
		body, _ := io.ReadAll(res.Body)
		return fmt.Errorf("unexpected status code %d creating acl: %s", res.StatusCode, string(body))
	}
}
