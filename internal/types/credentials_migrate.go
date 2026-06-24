package types

import (
	"fmt"
	"os"
	"strings"

	"github.com/goccy/go-yaml"
)

// MigrateClusterCredentials is the flat, single-cluster, auth-only Kafka credentials
// format used by `kcp migrate` (spec.source.credentials, spec.clusterLink.source.credentials,
// spec.clusterLink.destination.credentials). Unlike the OSK scan format it has no
// clusters: list, no id, no bootstrap_servers, and no scan-only jolokia/prometheus/metadata
// blocks — bootstrap addresses come from the manifest, and this file holds auth only.
// The auth_method block is identical to the OSK format (reuses AuthMethodConfig).
type MigrateClusterCredentials struct {
	AuthMethod            AuthMethodConfig `yaml:"auth_method"`
	InsecureSkipTLSVerify bool             `yaml:"insecure_skip_tls_verify,omitempty"`
}

// LoadMigrateClusterCredentials reads and validates the auth-only flat migrate
// credentials file, returning the auth-only MigrateClusterCredentials. The
// bootstrap address comes from the manifest (see MigrateConn). Returns all problems.
func LoadMigrateClusterCredentials(path string) (MigrateClusterCredentials, []error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return MigrateClusterCredentials{}, []error{fmt.Errorf("failed to read migrate credentials file: %w", err)}
	}

	var mc MigrateClusterCredentials
	if err := yaml.UnmarshalWithOptions(data, &mc, yaml.Strict()); err != nil {
		// A common mistake is passing the OSK scan format (clusters: list).
		if strings.Contains(err.Error(), "clusters") {
			return MigrateClusterCredentials{}, []error{fmt.Errorf("migrate credentials use a single-cluster format (top-level auth_method only), not the scan 'clusters:' list: %w", err)}
		}
		// A common mistake is including bootstrap_servers or bootstrapServers in the creds file.
		if strings.Contains(err.Error(), "bootstrap_servers") || strings.Contains(err.Error(), "bootstrapServers") {
			return MigrateClusterCredentials{}, []error{fmt.Errorf("bootstrap servers belong in the manifest (spec.source.bootstrapServers or spec.clusterLink.source/destination.bootstrapServers), not the credentials file: %w", err)}
		}
		return MigrateClusterCredentials{}, []error{fmt.Errorf("failed to parse migrate credentials: %w", err)}
	}

	// Build a temporary OSKClusterAuth to reuse GetAuthMethods and validateAuthMethodConfig.
	tmp := OSKClusterAuth{AuthMethod: mc.AuthMethod}
	enabled := tmp.GetAuthMethods()

	var errs []error
	switch {
	case len(enabled) == 0:
		errs = append(errs, fmt.Errorf("no authentication method enabled (set exactly one auth_method, e.g. unauthenticated_plaintext: { use: true })"))
	case len(enabled) > 1:
		errs = append(errs, fmt.Errorf("multiple authentication methods enabled (only one allowed)"))
	default:
		if err := validateAuthMethodConfig(mc.AuthMethod, enabled); err != nil {
			errs = append(errs, err)
		}
	}
	return mc, errs
}

// MigrateConn combines a manifest bootstrap address with auth-only creds into the
// OSKClusterAuth the source reader / link-auth mapper already consume.
func MigrateConn(bootstrapServers []string, creds MigrateClusterCredentials) OSKClusterAuth {
	return OSKClusterAuth{
		BootstrapServers:      bootstrapServers,
		AuthMethod:            creds.AuthMethod,
		InsecureSkipTLSVerify: creds.InsecureSkipTLSVerify,
	}
}
