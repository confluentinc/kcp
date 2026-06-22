package clusterlink

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strings"
)

// httpStatusError is returned by doRequest/doPostRequest when the server
// responds with a non-success status code. Callers can use errors.As to
// branch on StatusCode (e.g. to distinguish 404 from 401).
type httpStatusError struct {
	StatusCode int
	Body       string
}

func (e *httpStatusError) Error() string {
	return fmt.Sprintf("unexpected status code %d: %s", e.StatusCode, e.Body)
}

// linkPath returns the escaped base path for the cluster link resource.
func linkPath(config Config) string {
	return fmt.Sprintf("/kafka/v3/clusters/%s/links/%s",
		url.PathEscape(config.ClusterID),
		url.PathEscape(config.LinkName))
}

// ClusterLink describes a Confluent Cloud cluster link as returned by
// GET /kafka/v3/clusters/{cluster_id}/links/{link_name}.
type ClusterLink struct {
	LinkName        string `json:"link_name"`
	LinkID          string `json:"link_id"`
	ClusterID       string `json:"cluster_id"`
	SourceClusterID string `json:"source_cluster_id"`
	LinkState       string `json:"link_state"`
	LinkError       string `json:"link_error,omitempty"`
}

type MirrorLag struct {
	Partition             int `json:"partition"`
	Lag                   int `json:"lag"`
	LastSourceFetchOffset int `json:"last_source_fetch_offset"`
}

type MirrorTopic struct {
	MirrorTopicName string      `json:"mirror_topic_name"`
	MirrorStatus    string      `json:"mirror_status"`
	MirrorLags      []MirrorLag `json:"mirror_lags"`
}

const (
	// Mirror topic status constants
	MirrorStatusActive = "ACTIVE"
)

// Config holds cluster link configuration
type Config struct {
	RestEndpoint string
	ClusterID    string
	LinkName     string
	APIKey       string
	APISecret    string
	Topics       []string
	// Auth authenticates each REST request. When nil, requests fall back to
	// HTTP basic using APIKey/APISecret (so the existing migration-execute and
	// Confluent Cloud api-key callers keep working unchanged).
	Auth Authenticator
}

// authenticator returns the configured Authenticator, or a basic-auth fallback
// built from APIKey/APISecret when none is set.
func (c Config) authenticator() Authenticator {
	if c.Auth != nil {
		return c.Auth
	}
	return BasicAuth{Username: c.APIKey, Password: c.APISecret}
}

// Operation values accepted by AlterConfigs. They mirror the Confluent REST v3
// batch-alter conventions: SET writes the value, DELETE clears it back to the
// server-side default.
const (
	OperationSet    = "SET"
	OperationDelete = "DELETE"
)

// ConfigAlteration is one entry in a batch :alter request body. Operation must
// be OperationSet or OperationDelete; Value is ignored for DELETE.
type ConfigAlteration struct {
	Name      string `json:"name"`
	Value     string `json:"value"`
	Operation string `json:"operation"`
}

// PromoteMirrorTopicsResponse represents the response from promoting mirror topics
type PromoteMirrorTopicsResponse struct {
	Data []struct {
		MirrorTopicName string `json:"mirror_topic_name"`
		ErrorMessage    string `json:"error_message,omitempty"`
		ErrorCode       int    `json:"error_code,omitempty"`
	} `json:"data"`
}

// Service defines cluster link operations.
//
// Lifecycle operations (CreateClusterLink) are intentionally NOT part of this
// interface — they live on *ConfluentCloudService directly, because they are
// setup operations rather than ongoing monitoring.
type Service interface {
	GetClusterLink(ctx context.Context, config Config) (*ClusterLink, error)
	ListMirrorTopics(ctx context.Context, config Config) ([]MirrorTopic, error)
	ListConfigs(ctx context.Context, config Config) (map[string]string, error)
	ValidateTopics(topics []string, clusterLinkTopics []string) error
	PromoteMirrorTopics(ctx context.Context, config Config, topicNames []string) (*PromoteMirrorTopicsResponse, error)

	// AlterConfigs applies the given alterations to the cluster link's configs.
	//
	// IMPORTANT: this method is NOT atomic across multiple alterations. The
	// implementation iterates the slice and issues one network call per entry,
	// short-circuiting on the first error. On mid-batch failure, alterations
	// before the failing index are already persisted server-side; alterations
	// at and after the failing index are NOT applied. The returned error does
	// not identify which entries succeeded — callers that need batch
	// atomicity must implement compensation themselves, or call this method
	// with a single alteration at a time.
	AlterConfigs(ctx context.Context, config Config, alterations []ConfigAlteration) error
}

// HTTPClient interface for HTTP operations
type HTTPClient interface {
	Do(req *http.Request) (*http.Response, error)
}

// ConfluentCloudService implements cluster link operations using Confluent Cloud REST API
type ConfluentCloudService struct {
	httpClient HTTPClient
}

// NewConfluentCloudService creates a new cluster link service
func NewConfluentCloudService(httpClient HTTPClient) *ConfluentCloudService {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &ConfluentCloudService{
		httpClient: httpClient,
	}
}

// GetClusterLink fetches the cluster link resource. Translates common
// failure statuses (404, 401/403) into user-facing messages so callers can
// surface actionable errors to the CLI.
func (s *ConfluentCloudService) GetClusterLink(ctx context.Context, config Config) (*ClusterLink, error) {
	var link ClusterLink
	if err := s.doRequest(ctx, config, linkPath(config), &link); err != nil {
		var statusErr *httpStatusError
		if errors.As(err, &statusErr) {
			switch statusErr.StatusCode {
			case http.StatusNotFound:
				return nil, fmt.Errorf("cluster link %q not found on cluster %s: %w", config.LinkName, config.ClusterID, ErrLinkNotFound)
			case http.StatusUnauthorized, http.StatusForbidden:
				return nil, fmt.Errorf("authentication failed (status %d) — verify --cluster-api-key and --cluster-api-secret", statusErr.StatusCode)
			}
		}
		return nil, fmt.Errorf("failed to get cluster link: %w", err)
	}
	return &link, nil
}

func (s *ConfluentCloudService) ListMirrorTopics(ctx context.Context, config Config) ([]MirrorTopic, error) {
	path := linkPath(config) + "/mirrors"

	var response struct {
		Data []MirrorTopic `json:"data"`
	}

	if err := s.doRequest(ctx, config, path, &response); err != nil {
		return nil, fmt.Errorf("failed to list cluster link mirror topics: %w", err)
	}

	// If no topics specified, return all
	if len(config.Topics) == 0 {
		return response.Data, nil
	}

	// Filter for specified topics only
	migrationTopics := make([]MirrorTopic, 0, len(config.Topics))
	for _, topic := range response.Data {
		if slices.Contains(config.Topics, topic.MirrorTopicName) {
			migrationTopics = append(migrationTopics, topic)
		}
	}

	return migrationTopics, nil
}

// ListConfigs retrieves cluster link configurations
func (s *ConfluentCloudService) ListConfigs(ctx context.Context, config Config) (map[string]string, error) {
	path := linkPath(config) + "/configs"

	var response struct {
		Data []struct {
			Name  string `json:"name"`
			Value string `json:"value"`
		} `json:"data"`
	}

	if err := s.doRequest(ctx, config, path, &response); err != nil {
		return nil, fmt.Errorf("failed to list cluster link configs: %w", err)
	}

	configs := make(map[string]string, len(response.Data))
	for _, cfg := range response.Data {
		configs[cfg.Name] = cfg.Value
	}

	return configs, nil
}

// ValidateTopics validates that all specified topics exist in cluster link
func (s *ConfluentCloudService) ValidateTopics(topics []string, clusterLinkTopics []string) error {
	for _, topic := range topics {
		if !slices.Contains(clusterLinkTopics, topic) {
			return fmt.Errorf("topic %s not found in cluster link", topic)
		}
	}
	return nil
}

// AlterConfigs applies a batch of cluster link config alterations.
//
// The Confluent Platform Kafka REST v3 API exposes per-config endpoints
// rather than a batch :alter resource (verified against CP 8.x where POST
// /configs:alter returns 405). SET maps to PUT /configs/{name} with
// {"value": ...}, DELETE maps to DELETE /configs/{name}. We iterate the
// batch client-side to provide a stable interface above the
// version-dependent shape — first error short-circuits and is returned.
//
// NOT atomic: alterations before the failing index are already persisted
// server-side. The returned error does not identify the partition between
// applied and skipped entries. See the Service interface doc.
//
// Empty alterations short-circuit without a network call. 404 / 401 / 403
// translate to actionable messages matching GetClusterLink's style.
func (s *ConfluentCloudService) AlterConfigs(ctx context.Context, config Config, alterations []ConfigAlteration) error {
	if len(alterations) == 0 {
		return nil
	}

	for _, a := range alterations {
		path := linkPath(config) + "/configs/" + url.PathEscape(a.Name)
		var err error
		switch a.Operation {
		case OperationSet:
			body := struct {
				Value string `json:"value"`
			}{Value: a.Value}
			err = s.doPutRequest(ctx, config, path, body)
		case OperationDelete:
			err = s.doDeleteRequest(ctx, config, path)
		default:
			return fmt.Errorf("unsupported AlterConfigs operation %q for %s", a.Operation, a.Name)
		}
		if err != nil {
			return translateAlterError(config, a, err)
		}
	}
	return nil
}

func translateAlterError(config Config, alteration ConfigAlteration, err error) error {
	var statusErr *httpStatusError
	if errors.As(err, &statusErr) {
		switch statusErr.StatusCode {
		case http.StatusNotFound:
			return fmt.Errorf("cluster link %q not found on cluster %s (while %s %q)", config.LinkName, config.ClusterID, alteration.Operation, alteration.Name)
		case http.StatusUnauthorized, http.StatusForbidden:
			return fmt.Errorf("authentication failed (status %d) while %s %q on cluster link %q — verify --cluster-api-key and --cluster-api-secret", statusErr.StatusCode, alteration.Operation, alteration.Name, config.LinkName)
		}
	}
	return fmt.Errorf("failed to %s cluster link config %q on link %q: %w", alteration.Operation, alteration.Name, config.LinkName, err)
}

// PromoteMirrorTopics promotes the specified mirror topics
func (s *ConfluentCloudService) PromoteMirrorTopics(ctx context.Context, config Config, topicNames []string) (*PromoteMirrorTopicsResponse, error) {
	if len(topicNames) == 0 {
		return &PromoteMirrorTopicsResponse{}, nil
	}

	path := linkPath(config) + "/mirrors:promote"

	requestBody := struct {
		MirrorTopicNames []string `json:"mirror_topic_names"`
	}{
		MirrorTopicNames: topicNames,
	}

	var response PromoteMirrorTopicsResponse
	if err := s.doPostRequest(ctx, config, path, requestBody, &response); err != nil {
		return nil, fmt.Errorf("failed to promote mirror topics: %w", err)
	}

	return &response, nil
}

// doRequest performs an authenticated HTTP GET request to Confluent Cloud API.
// Non-2xx responses are returned as *httpStatusError so callers can branch on
// the status code.
func (s *ConfluentCloudService) doRequest(ctx context.Context, config Config, path string, result interface{}) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, config.RestEndpoint+path, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	config.authenticator().Apply(req)

	res, err := s.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to execute request: %w", err)
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(res.Body)
		return &httpStatusError{StatusCode: res.StatusCode, Body: string(body)}
	}

	body, err := io.ReadAll(res.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	if err := json.Unmarshal(body, result); err != nil {
		return fmt.Errorf("failed to unmarshal response: %w", err)
	}

	return nil
}

// doPostRequest performs an authenticated HTTP POST request to Confluent Cloud API
func (s *ConfluentCloudService) doPostRequest(ctx context.Context, config Config, path string, requestBody interface{}, result interface{}) error {
	jsonBody, err := json.Marshal(requestBody)
	if err != nil {
		return fmt.Errorf("failed to marshal request body: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, config.RestEndpoint+path, bytes.NewBuffer(jsonBody))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	config.authenticator().Apply(req)
	req.Header.Set("Content-Type", "application/json")

	res, err := s.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to execute request: %w", err)
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK && res.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(res.Body)
		return &httpStatusError{StatusCode: res.StatusCode, Body: string(body)}
	}

	body, err := io.ReadAll(res.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	if len(body) > 0 && result != nil {
		if err := json.Unmarshal(body, result); err != nil {
			return fmt.Errorf("failed to unmarshal response: %w", err)
		}
	}

	return nil
}

// doPutRequest performs an authenticated HTTP PUT. Used by AlterConfigs to
// update a single config key on the CP Kafka REST v3 API. Treats 200 and 204
// as success.
func (s *ConfluentCloudService) doPutRequest(ctx context.Context, config Config, path string, requestBody interface{}) error {
	jsonBody, err := json.Marshal(requestBody)
	if err != nil {
		return fmt.Errorf("failed to marshal request body: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, config.RestEndpoint+path, bytes.NewBuffer(jsonBody))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	config.authenticator().Apply(req)
	req.Header.Set("Content-Type", "application/json")

	res, err := s.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to execute request: %w", err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, res.Body)
		_ = res.Body.Close()
	}()

	if res.StatusCode != http.StatusOK && res.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(res.Body)
		return &httpStatusError{StatusCode: res.StatusCode, Body: string(body)}
	}
	return nil
}

// doDeleteRequest performs an authenticated HTTP DELETE. Used by AlterConfigs
// to reset a single config key to its server-side default.
func (s *ConfluentCloudService) doDeleteRequest(ctx context.Context, config Config, path string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, config.RestEndpoint+path, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	config.authenticator().Apply(req)

	res, err := s.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to execute request: %w", err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, res.Body)
		_ = res.Body.Close()
	}()

	if res.StatusCode != http.StatusOK && res.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(res.Body)
		return &httpStatusError{StatusCode: res.StatusCode, Body: string(body)}
	}
	return nil
}

func ClassifyMirrorTopics(mirrors []MirrorTopic) (topicNames []string, inactiveTopics []string) {
	for _, mirror := range mirrors {
		topicNames = append(topicNames, mirror.MirrorTopicName)
		if mirror.MirrorStatus != MirrorStatusActive {
			inactiveTopics = append(inactiveTopics, fmt.Sprintf("%s (status: %s)",
				mirror.MirrorTopicName, mirror.MirrorStatus))
		}
	}
	return topicNames, inactiveTopics
}

// GetActiveTopicsWithZeroLag returns topic names that are active and have zero lag across all partitions
func GetActiveTopicsWithZeroLag(mirrors []MirrorTopic) []string {
	var result []string
	for _, mirror := range mirrors {
		if mirror.MirrorStatus != MirrorStatusActive {
			continue
		}

		// Check if all partitions have zero lag
		allZeroLag := true
		for _, lag := range mirror.MirrorLags {
			if lag.Lag != 0 {
				allZeroLag = false
				break
			}
		}

		if allZeroLag {
			result = append(result, mirror.MirrorTopicName)
		}
	}
	return result
}

// HasActiveTopicsWithNonZeroLag checks if there are any active topics with non-zero lag
func HasActiveTopicsWithNonZeroLag(mirrors []MirrorTopic) bool {
	for _, mirror := range mirrors {
		if mirror.MirrorStatus != MirrorStatusActive {
			continue
		}
		for _, lag := range mirror.MirrorLags {
			slog.Debug("mirror topic", "topic", mirror.MirrorTopicName, "lag", lag.Lag, "partition", lag.Partition)
			if lag.Lag != 0 {
				return true
			}

		}
	}
	return false
}

// CountActiveMirrorTopics returns the count of active mirror topics
func CountActiveMirrorTopics(mirrors []MirrorTopic) int {
	count := 0
	for _, mirror := range mirrors {
		if mirror.MirrorStatus == MirrorStatusActive {
			count++
		}
	}
	return count
}

// ErrLinkExists is returned by CreateClusterLink when the target reports the
// link already exists. The reconcile model reads first so this is normally
// avoided; it is a belt-and-braces signal for the read-write race (§8.6).
var ErrLinkExists = errors.New("cluster link already exists")

// ErrLinkNotFound is returned (wrapped) by GetClusterLink when the target has
// no link of that name (HTTP 404), so callers can branch with errors.Is.
var ErrLinkNotFound = errors.New("cluster link not found")

// SourceTLSMaterial is inline PEM for the link's TLS connection to the source:
// the CA to trust the source (SSL/SASL_SSL) and, for mTLS, the client cert+key.
type SourceTLSMaterial struct {
	CACertPEM     string
	ClientCertPEM string // "" unless mTLS
	ClientKeyPEM  string // "" unless mTLS
}

// CreateClusterLinkRequest describes a destination-initiated cluster link:
// security.protocol plus optional SASL (mechanism + JAAS) and TLS material
// (SourceTLS), all derived from the source credentials.
type CreateClusterLinkRequest struct {
	SourceClusterID        string
	SourceBootstrapServers []string
	SecurityProtocol       string             // PLAINTEXT | SSL | SASL_SSL | SASL_PLAINTEXT
	SaslMechanism          string             // optional (SASL_*)
	SaslJaasConfig         string             // required when SaslMechanism is set (SASL_*)
	SourceTLS              *SourceTLSMaterial // optional: truststore (CA) and, for mTLS, keystore (client cert+key), as inline PEM
	Configs                map[string]string  // optional overrides from manifest spec.clusterLink.configs
}

// linkConfigEntry is one {name,value} pair in a create-link request body.
type linkConfigEntry struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// CreateClusterLink issues POST /kafka/v3/clusters/{ClusterID}/links/?link_name={LinkName}.
// Body shape matches the proven Terraform path (hcl/confluent/cluster_link.go).
func (s *ConfluentCloudService) CreateClusterLink(ctx context.Context, config Config, req CreateClusterLinkRequest) error {
	if req.SaslMechanism != "" && req.SaslJaasConfig == "" {
		return fmt.Errorf("SaslJaasConfig is required when SaslMechanism is set (link %q)", config.LinkName)
	}
	if req.SourceTLS != nil && (req.SourceTLS.ClientCertPEM == "") != (req.SourceTLS.ClientKeyPEM == "") {
		return fmt.Errorf("SourceTLS: ClientCertPEM and ClientKeyPEM must both be set or both empty (link %q)", config.LinkName)
	}
	// Assemble link configs. Derived defaults are added first; explicit
	// req.Configs then override any of them by name (so a single config key
	// never appears twice in the request body). This lets the manifest point
	// the link at the source's network-reachable address — which can differ
	// from the address KCP itself used to read the source — by overriding
	// bootstrap.servers.
	ordered := []string{}
	values := map[string]string{}
	put := func(name, value string) {
		if _, seen := values[name]; !seen {
			ordered = append(ordered, name)
		}
		values[name] = value
	}
	put("bootstrap.servers", strings.Join(req.SourceBootstrapServers, ","))
	put("link.mode", "DESTINATION")
	if req.SecurityProtocol != "" {
		put("security.protocol", req.SecurityProtocol)
	}
	if req.SaslMechanism != "" {
		put("sasl.mechanism", req.SaslMechanism)
		put("sasl.jaas.config", req.SaslJaasConfig)
	}
	if req.SourceTLS != nil {
		if req.SourceTLS.CACertPEM != "" {
			put("ssl.truststore.type", "PEM")
			put("ssl.truststore.certificates", req.SourceTLS.CACertPEM)
		}
		if req.SourceTLS.ClientCertPEM != "" {
			put("ssl.keystore.type", "PEM")
			put("ssl.keystore.certificate.chain", req.SourceTLS.ClientCertPEM)
			put("ssl.keystore.key", req.SourceTLS.ClientKeyPEM)
		}
	}
	// req.Configs is an operator escape hatch for advanced link settings and
	// for overriding bootstrap.servers (see above). Callers should not use it to
	// override the protocol-defining keys (link.mode, security.protocol,
	// sasl.mechanism, sasl.jaas.config), which are set from the typed request
	// fields; doing so is unsupported and may conflict with validation.
	overrideKeys := make([]string, 0, len(req.Configs))
	for k := range req.Configs {
		overrideKeys = append(overrideKeys, k)
	}
	slices.Sort(overrideKeys) // deterministic order for newly-added keys
	for _, k := range overrideKeys {
		put(k, req.Configs[k])
	}
	configs := make([]linkConfigEntry, len(ordered))
	for i, name := range ordered {
		configs[i] = linkConfigEntry{Name: name, Value: values[name]}
	}

	body := struct {
		SourceClusterID string            `json:"source_cluster_id"`
		Configs         []linkConfigEntry `json:"configs"`
	}{SourceClusterID: req.SourceClusterID, Configs: configs}

	// Create uses .../links/?link_name=NAME (linkPath embeds the name in the
	// path, which is what GET/DELETE want — so build the create path explicitly).
	createPath := fmt.Sprintf("/kafka/v3/clusters/%s/links/?link_name=%s",
		url.PathEscape(config.ClusterID), url.QueryEscape(config.LinkName))

	if err := s.doPostRequestExpectStatus(ctx, config, createPath, body); err != nil {
		var statusErr *httpStatusError
		if errors.As(err, &statusErr) && statusErr.StatusCode == http.StatusConflict {
			return ErrLinkExists
		}
		return fmt.Errorf("failed to create cluster link %q on cluster %s: %w", config.LinkName, config.ClusterID, err)
	}
	return nil
}

// GetKafkaClusterID returns the (single) Kafka cluster id from
// GET /kafka/v3/clusters. Confluent Server exposes exactly one entry.
func (s *ConfluentCloudService) GetKafkaClusterID(ctx context.Context, config Config) (string, error) {
	var resp struct {
		Data []struct {
			ClusterID string `json:"cluster_id"`
		} `json:"data"`
	}
	if err := s.doRequest(ctx, config, "/kafka/v3/clusters", &resp); err != nil {
		return "", fmt.Errorf("failed to list kafka clusters: %w", err)
	}
	if len(resp.Data) == 0 {
		return "", fmt.Errorf("no kafka cluster returned by %s/kafka/v3/clusters", config.RestEndpoint)
	}
	return resp.Data[0].ClusterID, nil
}

// doPostRequestExpectStatus posts a body and treats 200/201/204 as success,
// returning *httpStatusError otherwise. (doPostRequest unmarshals a result and
// only accepts 200/204; create returns 201 with no body we need.)
func (s *ConfluentCloudService) doPostRequestExpectStatus(ctx context.Context, config Config, path string, requestBody any) error {
	jsonBody, err := json.Marshal(requestBody)
	if err != nil {
		return fmt.Errorf("failed to marshal request body: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, config.RestEndpoint+path, bytes.NewBuffer(jsonBody))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	config.authenticator().Apply(req)
	req.Header.Set("Content-Type", "application/json")

	res, err := s.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to execute request: %w", err)
	}
	defer func() { _, _ = io.Copy(io.Discard, res.Body); _ = res.Body.Close() }()

	switch res.StatusCode {
	case http.StatusOK, http.StatusCreated, http.StatusNoContent:
		return nil
	default:
		b, _ := io.ReadAll(res.Body)
		return &httpStatusError{StatusCode: res.StatusCode, Body: string(b)}
	}
}
