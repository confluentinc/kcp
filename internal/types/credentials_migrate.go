package types

import (
	"fmt"
	"os"
	"strings"

	"github.com/goccy/go-yaml"
)

// MigrateClusterCredentials is the flat, single-cluster Kafka credentials format
// used by `kcp migrate` (spec.source.credentials, spec.clusterLink.sourceCredentials,
// spec.clusterLink.destinationCredentials). Unlike the OSK scan format it has no
// clusters: list, no id, and no scan-only jolokia/prometheus/metadata blocks — a
// migration only ever connects to one cluster. The auth_method block is identical
// to the OSK format (reuses AuthMethodConfig).
type MigrateClusterCredentials struct {
	BootstrapServers      []string         `yaml:"bootstrap_servers"`
	AuthMethod            AuthMethodConfig `yaml:"auth_method"`
	InsecureSkipTLSVerify bool             `yaml:"insecure_skip_tls_verify,omitempty"`
}

// LoadMigrateClusterCredentials reads and validates the flat migrate credentials
// file, returning it as an OSKClusterAuth (id/metadata empty) so the existing
// source-reader and link-auth plumbing consume it unchanged. Returns all problems.
func LoadMigrateClusterCredentials(path string) (OSKClusterAuth, []error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return OSKClusterAuth{}, []error{fmt.Errorf("failed to read migrate credentials file: %w", err)}
	}

	var mc MigrateClusterCredentials
	if err := yaml.UnmarshalWithOptions(data, &mc, yaml.Strict()); err != nil {
		// A common mistake is passing the OSK scan format (clusters: list).
		if strings.Contains(err.Error(), "clusters") {
			return OSKClusterAuth{}, []error{fmt.Errorf("migrate credentials use a single-cluster format (top-level bootstrap_servers + auth_method), not the scan 'clusters:' list: %w", err)}
		}
		return OSKClusterAuth{}, []error{fmt.Errorf("failed to parse migrate credentials: %w", err)}
	}

	cluster := OSKClusterAuth{
		BootstrapServers:      mc.BootstrapServers,
		AuthMethod:            mc.AuthMethod,
		InsecureSkipTLSVerify: mc.InsecureSkipTLSVerify,
	}

	var errs []error
	if len(cluster.BootstrapServers) == 0 {
		errs = append(errs, fmt.Errorf("no bootstrap_servers specified"))
	}
	for i, server := range cluster.BootstrapServers {
		if !isValidBootstrapServer(server) {
			errs = append(errs, fmt.Errorf("invalid bootstrap server format %q at index %d (expected host:port)", server, i))
		}
	}
	enabled := cluster.GetAuthMethods()
	switch {
	case len(enabled) == 0:
		errs = append(errs, fmt.Errorf("no authentication method enabled (set exactly one auth_method, e.g. unauthenticated_plaintext: { use: true })"))
	case len(enabled) > 1:
		errs = append(errs, fmt.Errorf("multiple authentication methods enabled (only one allowed)"))
	default:
		if err := validateAuthMethodConfig(cluster.AuthMethod, enabled); err != nil {
			errs = append(errs, err)
		}
	}
	return cluster, errs
}
