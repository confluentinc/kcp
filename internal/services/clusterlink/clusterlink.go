package clusterlink

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
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

// basicAuthHeader returns the full "Basic <base64>" header value built from
// the config's API key and secret.
func basicAuthHeader(config Config) string {
	return "Basic " + base64.StdEncoding.EncodeToString(fmt.Appendf(nil, "%s:%s", config.APIKey, config.APISecret))
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

// Service defines cluster link operations
type Service interface {
	GetClusterLink(ctx context.Context, config Config) (*ClusterLink, error)
	ListMirrorTopics(ctx context.Context, config Config) ([]MirrorTopic, error)
	ListConfigs(ctx context.Context, config Config) (map[string]string, error)
	ValidateTopics(topics []string, clusterLinkTopics []string) error
	PromoteMirrorTopics(ctx context.Context, config Config, topicNames []string) (*PromoteMirrorTopicsResponse, error)
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
				return nil, fmt.Errorf("cluster link %q not found on cluster %s", config.LinkName, config.ClusterID)
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
			return translateAlterError(config, err)
		}
	}
	return nil
}

func translateAlterError(config Config, err error) error {
	var statusErr *httpStatusError
	if errors.As(err, &statusErr) {
		switch statusErr.StatusCode {
		case http.StatusNotFound:
			return fmt.Errorf("cluster link %q not found on cluster %s", config.LinkName, config.ClusterID)
		case http.StatusUnauthorized, http.StatusForbidden:
			return fmt.Errorf("authentication failed (status %d) — verify --cluster-api-key and --cluster-api-secret", statusErr.StatusCode)
		}
	}
	return fmt.Errorf("failed to alter cluster link configs: %w", err)
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
	req.Header.Set("Authorization", basicAuthHeader(config))

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
	req.Header.Set("Authorization", basicAuthHeader(config))
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
	req.Header.Set("Authorization", basicAuthHeader(config))
	req.Header.Set("Content-Type", "application/json")

	res, err := s.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to execute request: %w", err)
	}
	defer func() { _ = res.Body.Close() }()

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
	req.Header.Set("Authorization", basicAuthHeader(config))

	res, err := s.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to execute request: %w", err)
	}
	defer func() { _ = res.Body.Close() }()

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
