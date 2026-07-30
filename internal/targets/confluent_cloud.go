package targets

import (
	"fmt"

	"github.com/confluentinc/kcp/internal/services/clusterlink"
)

// NewConfluentCloudTarget constructs a cluster-link REST client for a Confluent
// Cloud cluster. Unlike CP, CC has no GET /kafka/v3/clusters list endpoint (it
// 404s), so the lkc cluster id is supplied from the manifest (spec.target.clusterId)
// and pre-seeded here — ClusterID() then returns it without any discovery call.
// httpClient may be nil to use http.DefaultClient.
//
// clusterID must be non-empty: a CC target cannot discover its own id, so an empty
// id would otherwise fall through to the CC-404 list endpoint. Manifest validation
// already requires spec.target.clusterId for CC; this enforces the invariant at the
// owner so a future caller can't build a CC target that silently fails with a 404.
func NewConfluentCloudTarget(restEndpoint, clusterID string, creds *Credentials, httpClient clusterlink.HTTPClient) (*LinkEndpoint, error) {
	if clusterID == "" {
		return nil, fmt.Errorf("clusterId is required for a Confluent Cloud target (it has no cluster-discovery endpoint)")
	}
	e := NewLinkEndpoint(restEndpoint, creds, httpClient)
	e.clusterID = clusterID
	return e, nil
}
