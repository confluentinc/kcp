package clusterlink

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
)

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
}

// Service defines cluster link operations
type Service interface {
	ListMirrorTopics(ctx context.Context, config Config) ([]MirrorTopic, error)
	ListConfigs(ctx context.Context, config Config) (map[string]string, error)
	ValidateTopics(topics []string, clusterLinkTopics []string) error
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

// ListMirrorTopics retrieves all mirror topics from a cluster link
func (s *ConfluentCloudService) ListMirrorTopics(ctx context.Context, config Config) ([]MirrorTopic, error) {
	path := fmt.Sprintf("/kafka/v3/clusters/%s/links/%s/mirrors", config.ClusterID, config.LinkName)

	var response struct {
		Data []MirrorTopic `json:"data"`
	}

	if err := s.doRequest(ctx, config, path, &response); err != nil {
		return nil, fmt.Errorf("failed to list cluster link mirror topics: %w", err)
	}

	if len(response.Data) == 0 {
		return nil, fmt.Errorf("no mirror topics found in cluster link")
	}

	return response.Data, nil
}

// ListConfigs retrieves cluster link configurations
func (s *ConfluentCloudService) ListConfigs(ctx context.Context, config Config) (map[string]string, error) {
	path := fmt.Sprintf("/kafka/v3/clusters/%s/links/%s/configs", config.ClusterID, config.LinkName)

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

// doRequest performs an authenticated HTTP request to Confluent Cloud API
func (s *ConfluentCloudService) doRequest(ctx context.Context, config Config, path string, result interface{}) error {
	url := config.RestEndpoint + path
	auth := base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf("%s:%s", config.APIKey, config.APISecret)))

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Add("Authorization", "Basic "+auth)

	res, err := s.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to execute request: %w", err)
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(res.Body)
		return fmt.Errorf("unexpected status code %d: %s", res.StatusCode, string(body))
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
