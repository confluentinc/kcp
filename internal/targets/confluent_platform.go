package targets

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/confluentinc/kcp/internal/services/clusterlink"
)

// ConfluentPlatformTarget talks to a Confluent Server embedded Admin REST API.
type ConfluentPlatformTarget struct {
	restEndpoint string
	creds        *Credentials
	svc          *clusterlink.ConfluentCloudService
	clusterID    string // cached after first discovery
}

// NewConfluentPlatformTarget constructs a target backed by a Confluent Server
// Admin REST endpoint. httpClient may be nil to use http.DefaultClient.
func NewConfluentPlatformTarget(restEndpoint string, creds *Credentials, httpClient clusterlink.HTTPClient) *ConfluentPlatformTarget {
	return &ConfluentPlatformTarget{
		restEndpoint: strings.TrimRight(restEndpoint, "/"),
		creds:        creds,
		svc:          clusterlink.NewConfluentCloudService(httpClient),
	}
}

func (t *ConfluentPlatformTarget) config(linkName string) clusterlink.Config {
	user, pass := t.creds.basicPair()
	return clusterlink.Config{
		RestEndpoint: t.restEndpoint,
		ClusterID:    t.clusterID,
		LinkName:     linkName,
		APIKey:       user,
		APISecret:    pass,
	}
}

// ClusterID discovers the target Kafka cluster id via GET /kafka/v3/clusters.
func (t *ConfluentPlatformTarget) ClusterID(ctx context.Context) (string, error) {
	if t.clusterID != "" {
		return t.clusterID, nil
	}
	id, err := t.svc.GetKafkaClusterID(ctx, t.config(""))
	if err != nil {
		return "", fmt.Errorf("discovering target cluster id: %w", err)
	}
	t.clusterID = id
	return id, nil
}

// GetClusterLink fetches the named cluster link from the Confluent Server target.
// Returns (nil, nil) when the link does not exist so the reconciler treats
// absence as "to create".
func (t *ConfluentPlatformTarget) GetClusterLink(ctx context.Context, name string) (*clusterlink.ClusterLink, error) {
	link, err := t.svc.GetClusterLink(ctx, t.config(name))
	if err != nil {
		if errors.Is(err, clusterlink.ErrLinkNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return link, nil
}

// CreateClusterLink creates the named cluster link on the Confluent Server target.
// A pre-existing link (ErrLinkExists) is silently treated as success.
func (t *ConfluentPlatformTarget) CreateClusterLink(ctx context.Context, name string, req clusterlink.CreateClusterLinkRequest) error {
	err := t.svc.CreateClusterLink(ctx, t.config(name), req)
	if errors.Is(err, clusterlink.ErrLinkExists) {
		return nil // belt-and-braces: read-first already filtered, treat as success (§8.6)
	}
	return err
}
