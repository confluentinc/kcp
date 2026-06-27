package targets

import (
	"github.com/confluentinc/kcp/internal/services/clusterlink"
)

// NewConfluentCloudTarget constructs a cluster-link REST client for a Confluent
// Cloud cluster. Unlike CP, CC has no GET /kafka/v3/clusters list endpoint (it
// 404s), so the lkc cluster id is supplied from the manifest (spec.target.clusterId)
// and pre-seeded here — ClusterID() then returns it without any discovery call.
// httpClient may be nil to use http.DefaultClient.
func NewConfluentCloudTarget(restEndpoint, clusterID string, creds *Credentials, httpClient clusterlink.HTTPClient) *LinkEndpoint {
	e := NewLinkEndpoint(restEndpoint, creds, httpClient)
	e.clusterID = clusterID
	return e
}
