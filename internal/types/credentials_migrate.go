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
//
// Exactly one auth method block must be present. Auth is selected by PRESENCE (no use: flag,
// no auth_method: wrapper). Example:
//
//	sasl_scram: { username: admin, password: secret, mechanism: SHA256 }
//
// or for plaintext:
//
//	unauthenticated_plaintext: {}
type MigrateClusterCredentials struct {
	SASLScram                *MigrateSASLScram                `yaml:"sasl_scram,omitempty"`
	SASLPlain                *MigrateSASLPlain                `yaml:"sasl_plain,omitempty"`
	TLS                      *MigrateTLS                      `yaml:"tls,omitempty"`
	UnauthenticatedTLS       *MigrateUnauthenticatedTLS       `yaml:"unauthenticated_tls,omitempty"`
	UnauthenticatedPlaintext *MigrateUnauthenticatedPlaintext `yaml:"unauthenticated_plaintext,omitempty"`
	InsecureSkipTLSVerify    bool                             `yaml:"insecure_skip_tls_verify,omitempty"`
}

// MigrateSASLScram is the SASL/SCRAM auth block for migrate credentials (no use: flag).
type MigrateSASLScram struct {
	Username  string `yaml:"username"`
	Password  string `yaml:"password"`
	Mechanism string `yaml:"mechanism,omitempty"` // "SHA256" or "SHA512"
	CACert    string `yaml:"ca_cert,omitempty"`
}

// MigrateSASLPlain is the SASL/PLAIN auth block for migrate credentials (no use: flag).
type MigrateSASLPlain struct {
	Username string `yaml:"username"`
	Password string `yaml:"password"`
	CACert   string `yaml:"ca_cert,omitempty"`
}

// MigrateTLS is the mTLS auth block for migrate credentials (no use: flag).
type MigrateTLS struct {
	CACert     string `yaml:"ca_cert,omitempty"`
	ClientCert string `yaml:"client_cert"`
	ClientKey  string `yaml:"client_key"`
}

// MigrateUnauthenticatedTLS is the unauthenticated TLS auth block for migrate credentials (no use: flag).
type MigrateUnauthenticatedTLS struct {
	CACert string `yaml:"ca_cert,omitempty"`
}

// MigrateUnauthenticatedPlaintext is an empty marker block — its PRESENCE (unauthenticated_plaintext: {})
// selects plaintext. goccy/go-yaml unmarshals `{}` into a non-nil pointer for an empty struct,
// while a missing key or null yields nil.
type MigrateUnauthenticatedPlaintext struct{}

// methodCount returns how many auth method blocks are set (must be exactly 1).
func (c MigrateClusterCredentials) methodCount() int {
	n := 0
	if c.SASLScram != nil {
		n++
	}
	if c.SASLPlain != nil {
		n++
	}
	if c.TLS != nil {
		n++
	}
	if c.UnauthenticatedTLS != nil {
		n++
	}
	if c.UnauthenticatedPlaintext != nil {
		n++
	}
	return n
}

// authMethodConfig maps the present migrate auth block onto the shared AuthMethodConfig,
// setting Use: true on the one selected sub-config. If no block is set the returned
// AuthMethodConfig is zero (all nil pointers).
func (c MigrateClusterCredentials) authMethodConfig() AuthMethodConfig {
	amc := AuthMethodConfig{}
	switch {
	case c.SASLScram != nil:
		amc.SASLScram = &SASLScramConfig{
			Use:       true,
			Username:  c.SASLScram.Username,
			Password:  c.SASLScram.Password,
			Mechanism: c.SASLScram.Mechanism,
			CACert:    c.SASLScram.CACert,
		}
	case c.SASLPlain != nil:
		amc.SASLPlain = &SASLPlainConfig{
			Use:      true,
			Username: c.SASLPlain.Username,
			Password: c.SASLPlain.Password,
			CACert:   c.SASLPlain.CACert,
		}
	case c.TLS != nil:
		amc.TLS = &TLSConfig{
			Use:        true,
			CACert:     c.TLS.CACert,
			ClientCert: c.TLS.ClientCert,
			ClientKey:  c.TLS.ClientKey,
		}
	case c.UnauthenticatedTLS != nil:
		amc.UnauthenticatedTLS = &UnauthenticatedTLSConfig{
			Use:    true,
			CACert: c.UnauthenticatedTLS.CACert,
		}
	case c.UnauthenticatedPlaintext != nil:
		amc.UnauthenticatedPlaintext = &UnauthenticatedPlaintextConfig{Use: true}
	}
	return amc
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
		msg := err.Error()
		// Old format: auth_method: wrapper — hint that auth is now top-level.
		if strings.Contains(msg, "auth_method") {
			return MigrateClusterCredentials{}, []error{fmt.Errorf(
				"auth methods are now specified at the top-level (no auth_method: wrapper) — e.g. 'sasl_scram: { username: ..., password: ... }': %w", err)}
		}
		// Common mistake: passing the OSK scan format (clusters: list).
		if strings.Contains(msg, "clusters") {
			return MigrateClusterCredentials{}, []error{fmt.Errorf(
				"migrate credentials use a single-cluster format (top-level auth method only), not the scan 'clusters:' list: %w", err)}
		}
		// Common mistake: including bootstrap_servers in the creds file.
		if strings.Contains(msg, "bootstrap_servers") || strings.Contains(msg, "bootstrapServers") {
			return MigrateClusterCredentials{}, []error{fmt.Errorf(
				"bootstrap servers belong in the manifest (spec.source.bootstrapServers or spec.clusterLink.source/destination.bootstrapServers), not the credentials file: %w", err)}
		}
		return MigrateClusterCredentials{}, []error{fmt.Errorf("failed to parse migrate credentials: %w", err)}
	}

	var errs []error
	switch mc.methodCount() {
	case 0:
		errs = append(errs, fmt.Errorf(
			"no authentication method specified (set exactly one of sasl_scram, sasl_plain, tls, unauthenticated_tls, unauthenticated_plaintext)"))
	default:
		if mc.methodCount() > 1 {
			errs = append(errs, fmt.Errorf("multiple authentication methods specified (only one allowed)"))
		} else {
			// Reuse shared per-method validation via AuthMethodConfig.
			amc := mc.authMethodConfig()
			enabled := OSKClusterAuth{AuthMethod: amc}.GetAuthMethods()
			if err := validateAuthMethodConfig(amc, enabled); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return mc, errs
}

// MigrateConn combines a manifest bootstrap address with auth-only creds into the
// OSKClusterAuth the source reader / link-auth mapper already consume.
func MigrateConn(bootstrapServers []string, creds MigrateClusterCredentials) OSKClusterAuth {
	return OSKClusterAuth{
		BootstrapServers:      bootstrapServers,
		AuthMethod:            creds.authMethodConfig(),
		InsecureSkipTLSVerify: creds.InsecureSkipTLSVerify,
	}
}
