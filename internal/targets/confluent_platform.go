package targets

import (
	"github.com/confluentinc/kcp/internal/services/clusterlink"
)

// ConfluentPlatformTarget is a cluster-link REST endpoint that happens to be a
// migration target (a Confluent Server embedded Admin REST API). It is an alias
// for the generic LinkEndpoint: the same client type backs both the destination
// and the source of a source-initiated cluster link, since both expose the same
// /kafka/v3 REST surface. New code should prefer LinkEndpoint/NewLinkEndpoint;
// this name is retained for existing callers.
type ConfluentPlatformTarget = LinkEndpoint

// NewConfluentPlatformTarget constructs a cluster-link REST client backed by a
// Confluent Server Admin REST endpoint. It is a thin wrapper over
// NewLinkEndpoint kept for existing callers. httpClient may be nil to use
// http.DefaultClient.
func NewConfluentPlatformTarget(restEndpoint string, creds *Credentials, httpClient clusterlink.HTTPClient) *ConfluentPlatformTarget {
	return NewLinkEndpoint(restEndpoint, creds, httpClient)
}
