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
	IAM                      *MigrateIAM                      `yaml:"iam,omitempty"`
	SASLScram                *MigrateSASLScram                `yaml:"sasl_scram,omitempty"`
	SASLPlain                *MigrateSASLPlain                `yaml:"sasl_plain,omitempty"`
	MTLS                     *MigrateMTLS                     `yaml:"mtls,omitempty"`
	UnauthenticatedTLS       *MigrateUnauthenticatedTLS       `yaml:"unauthenticated_tls,omitempty"`
	UnauthenticatedPlaintext *MigrateUnauthenticatedPlaintext `yaml:"unauthenticated_plaintext,omitempty"`
	InsecureSkipTLSVerify    bool                             `yaml:"insecure_skip_tls_verify,omitempty"`
}

// MigrateIAM is the MSK IAM auth block. region is required (SigV4 token signing);
// there is no auto-derive. Valid only for an MSK source's read credentials.
type MigrateIAM struct {
	Region string `yaml:"region"`
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

// MigrateMTLS is the mutual-TLS auth block for migrate credentials (client cert +
// key; no use: flag). The client authenticates with a certificate — distinct from
// unauthenticated_tls (one-way TLS: server cert only, client not authenticated).
type MigrateMTLS struct {
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
	if c.IAM != nil {
		n++
	}
	if c.SASLScram != nil {
		n++
	}
	if c.SASLPlain != nil {
		n++
	}
	if c.MTLS != nil {
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
	case c.IAM != nil:
		amc.IAM = &IAMConfig{Use: true, Region: c.IAM.Region}
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
	case c.MTLS != nil:
		amc.TLS = &TLSConfig{
			Use:        true,
			CACert:     c.MTLS.CACert,
			ClientCert: c.MTLS.ClientCert,
			ClientKey:  c.MTLS.ClientKey,
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
			"no authentication method specified (set exactly one of iam, sasl_scram, sasl_plain, mtls, unauthenticated_tls, unauthenticated_plaintext)"))
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
	if mc.IAM != nil && strings.TrimSpace(mc.IAM.Region) == "" {
		errs = append(errs, fmt.Errorf("iam.region is required (the AWS region for SigV4 token signing)"))
	}
	// Migrate creds are hand-written (unlike scan creds, which kcp discover fills
	// in), so require an explicit SCRAM mechanism rather than silently defaulting
	// to SHA256 — that default is wrong for MSK (SHA-512-only) and surfaces only as
	// an opaque auth failure. (The scan format keeps its SHA256 default; this check
	// is migrate-only, hence here and not in the shared validateAuthMethodConfig.)
	if mc.SASLScram != nil && !isValidScramMechanism(mc.SASLScram.Mechanism) {
		errs = append(errs, fmt.Errorf("sasl_scram.mechanism is required and must be SHA256 or SHA512 (MSK requires SHA512)"))
	}
	return mc, errs
}

// isValidScramMechanism reports whether m is an explicitly-specified, supported
// SCRAM mechanism (empty is NOT valid for migrate creds).
func isValidScramMechanism(m string) bool {
	switch m {
	case "SHA256", "SCRAM-SHA-256", "SHA512", "SCRAM-SHA-512":
		return true
	default:
		return false
	}
}

// KafkaSourceConn is the neutral, migrate-facing Kafka connection: a bootstrap
// address plus the single selected auth method (and IAM region, carried on
// AuthMethod.IAM.Region). It replaces the migrate path's prior reuse of the scan
// type OSKClusterAuth, which is correctly named only on the scan side.
type KafkaSourceConn struct {
	BootstrapServers      []string
	AuthMethod            AuthMethodConfig
	InsecureSkipTLSVerify bool
}

// IAM is included (includeIAM=true): a migrate source may be MSK, which carries
// IAM on AuthMethod.IAM (region on AuthMethod.IAM.Region).
func (c KafkaSourceConn) GetAuthMethods() []AuthType { return c.AuthMethod.EnabledAuthMethods(true) }
func (c KafkaSourceConn) GetSelectedAuthType() (AuthType, error) {
	return c.AuthMethod.SelectedAuthType(true)
}

// MigrateConn combines a manifest bootstrap address with auth-only creds into the
// neutral KafkaSourceConn consumed by the migrate source reader and link-auth mapper.
func MigrateConn(bootstrapServers []string, creds MigrateClusterCredentials) KafkaSourceConn {
	return KafkaSourceConn{
		BootstrapServers:      bootstrapServers,
		AuthMethod:            creds.authMethodConfig(),
		InsecureSkipTLSVerify: creds.InsecureSkipTLSVerify,
	}
}
