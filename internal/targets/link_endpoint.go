package targets

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/confluentinc/kcp/internal/services/clusterlink"
)

// LinkEndpoint is a generic cluster-link REST client. It talks to a single
// Kafka REST endpoint (the v3 Admin REST API) and is agnostic about whether
// that endpoint is the destination or the source of a cluster link — both
// expose the same /kafka/v3/clusters and /links surface. Source-initiated
// links create/read link objects on BOTH a destination and a source endpoint;
// each is just a separate LinkEndpoint built with that endpoint's creds.
type LinkEndpoint struct {
	restEndpoint string
	creds        *Credentials
	svc          *clusterlink.ConfluentCloudService
	clusterID    string // cached after first discovery
}

// NewLinkEndpoint constructs a cluster-link REST client for an arbitrary
// endpoint (destination OR source). httpClient may be nil to use
// http.DefaultClient.
func NewLinkEndpoint(restEndpoint string, creds *Credentials, httpClient clusterlink.HTTPClient) *LinkEndpoint {
	return &LinkEndpoint{
		restEndpoint: strings.TrimRight(restEndpoint, "/"),
		creds:        creds,
		svc:          clusterlink.NewConfluentCloudService(httpClient),
	}
}

func (e *LinkEndpoint) config(linkName string) clusterlink.Config {
	return clusterlink.Config{
		RestEndpoint: e.restEndpoint,
		ClusterID:    e.clusterID,
		LinkName:     linkName,
		Auth:         e.creds.authenticator(),
	}
}

// ClusterID discovers the endpoint's Kafka cluster id via GET /kafka/v3/clusters.
// The result is cached after the first call.
func (e *LinkEndpoint) ClusterID(ctx context.Context) (string, error) {
	if e.clusterID != "" {
		return e.clusterID, nil
	}
	id, err := e.svc.GetKafkaClusterID(ctx, e.config(""))
	if err != nil {
		return "", fmt.Errorf("discovering cluster id: %w", err)
	}
	e.clusterID = id
	return id, nil
}

// GetClusterLink fetches the named cluster link from the endpoint. Returns
// (nil, nil) when the link does not exist so the reconciler treats absence as
// "to create".
func (e *LinkEndpoint) GetClusterLink(ctx context.Context, name string) (*clusterlink.ClusterLink, error) {
	link, err := e.svc.GetClusterLink(ctx, e.config(name))
	if err != nil {
		if errors.Is(err, clusterlink.ErrLinkNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return link, nil
}

// GetClusterLinkConfigs returns the named link's live config key/value map
// (GET .../links/{name}/configs), used by the reconciler for drift detection.
func (e *LinkEndpoint) GetClusterLinkConfigs(ctx context.Context, name string) (map[string]string, error) {
	return e.svc.ListConfigs(ctx, e.config(name))
}

// CreateClusterLink creates the named cluster link on the endpoint. A
// pre-existing link (ErrLinkExists) is silently treated as success.
func (e *LinkEndpoint) CreateClusterLink(ctx context.Context, name string, req clusterlink.CreateClusterLinkRequest) error {
	err := e.svc.CreateClusterLink(ctx, e.config(name), req)
	if errors.Is(err, clusterlink.ErrLinkExists) {
		return nil // belt-and-braces: read-first already filtered, treat as success (§8.6)
	}
	return err
}

// ListTopics returns the names of all topics on the endpoint's cluster. It
// discovers (and caches) the cluster id first.
func (e *LinkEndpoint) ListTopics(ctx context.Context) ([]string, error) {
	if _, err := e.ClusterID(ctx); err != nil {
		return nil, err
	}
	return e.svc.ListTopics(ctx, e.config(""))
}

// ListClusterLinks returns the names of all cluster links on the endpoint's
// cluster. It discovers (and caches) the cluster id first.
func (e *LinkEndpoint) ListClusterLinks(ctx context.Context) ([]string, error) {
	if _, err := e.ClusterID(ctx); err != nil {
		return nil, err
	}
	return e.svc.ListClusterLinks(ctx, e.config(""))
}

// CreateTopic creates a plain (non-mirror) topic on the endpoint's cluster. It
// discovers (and caches) the cluster id first.
func (e *LinkEndpoint) CreateTopic(ctx context.Context, req clusterlink.CreateTopicRequest) error {
	if _, err := e.ClusterID(ctx); err != nil {
		return err
	}
	return e.svc.CreateTopic(ctx, e.config(""), req)
}

// PartitionCount returns the live partition count of a topic on the endpoint's
// cluster. It satisfies the newtopics.partitionCounter optional interface, so a
// LinkEndpoint target reports partition-count drift for existing topics. It
// discovers (and caches) the cluster id first.
func (e *LinkEndpoint) PartitionCount(ctx context.Context, topic string) (int, error) {
	if _, err := e.ClusterID(ctx); err != nil {
		return 0, err
	}
	return e.svc.GetTopicPartitionCount(ctx, e.config(""), topic)
}

// ListMirrorTopics returns all mirror topics on the named link. Config.Topics
// is left empty by e.config(name), so the service applies no filtering and the
// caller gets the full existing-mirror list.
func (e *LinkEndpoint) ListMirrorTopics(ctx context.Context, name string) ([]clusterlink.MirrorTopic, error) {
	if _, err := e.ClusterID(ctx); err != nil {
		return nil, err
	}
	return e.svc.ListMirrorTopics(ctx, e.config(name))
}

// CreateMirrorTopic creates a mirror topic on the named link. It discovers (and
// caches) the cluster id first.
func (e *LinkEndpoint) CreateMirrorTopic(ctx context.Context, name, sourceTopic, mirrorTopic string) error {
	if _, err := e.ClusterID(ctx); err != nil {
		return err
	}
	return e.svc.CreateMirrorTopic(ctx, e.config(name), sourceTopic, mirrorTopic)
}
