package cutover

import (
	"net/http"

	"github.com/confluentinc/kcp/internal/utils"
)

// NewRESTHTTPClient builds the HTTP client used for the destination cluster's REST /
// cluster-link API during cutover. caCertPath trusts a private/internal CA fronting
// the endpoint (e.g. an enterprise TLS-terminating proxy); insecureSkip disables
// verification for test environments. Empty caCertPath + no skip → http.DefaultClient
// (system trust roots — the normal Confluent Cloud public-CA case). CA + skip are built
// via the shared utils.TLSClientConfig helper so REST TLS behaves like every other client.
func NewRESTHTTPClient(caCertPath string, insecureSkip bool) (*http.Client, error) {
	if caCertPath == "" && !insecureSkip {
		return http.DefaultClient, nil
	}
	pool, err := utils.OptionalCACertPool(caCertPath)
	if err != nil {
		return nil, err
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = utils.TLSClientConfig(pool, insecureSkip)
	return &http.Client{Transport: transport}, nil
}
