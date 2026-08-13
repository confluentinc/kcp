// Package targets is the Target abstraction (feasibility §7.4): Confluent Cloud
// and Confluent Platform implementations behind a common set of operations.
// Cluster-link operations support four target-REST auth methods: HTTP basic
// (CP) / api_key+secret (CC), bearer token (RBAC/MDS), and mutual TLS.
package targets

import (
	"fmt"
	"net/http"
	"os"

	"github.com/confluentinc/kcp/internal/interpolate"
	"github.com/confluentinc/kcp/internal/services/clusterlink"
	"github.com/confluentinc/kcp/internal/utils"
	"github.com/goccy/go-yaml"
)

// BasicAuth is HTTP basic auth (CP) — also how CC api_key/api_secret are sent.
type BasicAuth struct {
	Username           string `yaml:"username"`
	Password           string `yaml:"password"`
	CACert             string `yaml:"ca_cert,omitempty"`
	InsecureSkipVerify bool   `yaml:"insecure_skip_verify,omitempty"`
}

// BearerCreds carries an RBAC/OAuth bearer token (e.g. an MDS-issued JWT) sent
// as `Authorization: Bearer <token>`.
type BearerCreds struct {
	Token              string `yaml:"token"`
	CACert             string `yaml:"ca_cert,omitempty"`
	InsecureSkipVerify bool   `yaml:"insecure_skip_verify,omitempty"`
}

// MTLSCreds carries client-certificate material for mutual TLS to the target
// REST API. Auth is the TLS client cert, so no Authorization header is sent.
type MTLSCreds struct {
	CACert             string `yaml:"ca_cert,omitempty"`
	ClientCert         string `yaml:"client_cert"`
	ClientKey          string `yaml:"client_key"`
	InsecureSkipVerify bool   `yaml:"insecure_skip_verify,omitempty"`
}

// Credentials is the parsed target-creds.yaml. Exactly one auth block is allowed
// out of: basic, api_key/api_secret (CC), bearer, mtls.
//
// ca_cert and insecure_skip_verify are top-level because the api_key form is
// itself flat — basic, bearer and mtls carry their own copies inside the block.
// They apply ONLY to the api_key form and are rejected alongside the others,
// rather than being silently ignored.
type Credentials struct {
	Basic     *BasicAuth   `yaml:"basic,omitempty"`
	APIKey    string       `yaml:"api_key,omitempty"`
	APISecret string       `yaml:"api_secret,omitempty"`
	Bearer    *BearerCreds `yaml:"bearer,omitempty"`
	MTLS      *MTLSCreds   `yaml:"mtls,omitempty"`

	// CACert / InsecureSkipVerify are the api_key form's TLS-trust siblings.
	CACert             string `yaml:"ca_cert,omitempty"`
	InsecureSkipVerify bool   `yaml:"insecure_skip_verify,omitempty"`

	// Interpolate opts this file in to ${ENV_VAR} resolution. It is file-level
	// rather than a CLI flag so each file governs itself: a manifest that opts
	// in never changes how a credentials file it references is read.
	Interpolate bool `yaml:"interpolate,omitempty"`
}

// LoadCredentials reads, parses and validates a target-creds.yaml file.
func LoadCredentials(path string) (*Credentials, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading target credentials: %w", err)
	}
	return ParseCredentials(data)
}

// ParseCredentials parses and validates target credentials from bytes. It is
// the shared entry point so an inline block and a referenced file run exactly
// the same validation — a rule can never apply to one spelling and not the
// other.
//
// ${ENV_VAR} resolution runs immediately after the unmarshal and before every
// validation, because validation stats ca_cert paths: resolving afterwards
// would report `ca_cert file "${CA_PATH}": no such file`.
func ParseCredentials(data []byte) (*Credentials, error) {
	var c Credentials
	if err := yaml.UnmarshalWithOptions(data, &c, yaml.Strict()); err != nil {
		return nil, fmt.Errorf("parsing target credentials: %w", err)
	}
	if c.Interpolate {
		if err := interpolate.Struct(&c); err != nil {
			return nil, fmt.Errorf("resolving target credentials: %w", err)
		}
	}
	if err := ValidateCredentials(&c); err != nil {
		return nil, err
	}
	return &c, nil
}

// ValidateCredentials applies every target-credentials rule to an already-built
// struct, so a caller that assembled the block itself (an inline manifest
// block) is held to the same rules as a file.
func ValidateCredentials(c *Credentials) error {
	if (c.APIKey != "") != (c.APISecret != "") {
		return fmt.Errorf("api_key and api_secret must both be set or both omitted")
	}
	if n := c.authBlockCount(); n != 1 {
		return fmt.Errorf("target credentials must specify exactly one auth block, found %d", n)
	}
	if c.Bearer != nil && c.Bearer.Token == "" {
		return fmt.Errorf("bearer.token must not be empty")
	}
	if c.MTLS != nil {
		if c.MTLS.ClientCert == "" || c.MTLS.ClientKey == "" {
			return fmt.Errorf("mtls requires both client_cert and client_key")
		}
		for _, f := range []string{c.MTLS.ClientCert, c.MTLS.ClientKey, c.MTLS.CACert} {
			if f == "" {
				continue // CACert is optional
			}
			if _, err := os.Stat(f); err != nil {
				return fmt.Errorf("mtls certificate file %q: %w", f, err)
			}
		}
	}
	if c.Basic != nil && c.Basic.CACert != "" {
		if _, err := os.Stat(c.Basic.CACert); err != nil {
			return fmt.Errorf("basic ca_cert file %q: %w", c.Basic.CACert, err)
		}
	}
	if c.Bearer != nil && c.Bearer.CACert != "" {
		if _, err := os.Stat(c.Bearer.CACert); err != nil {
			return fmt.Errorf("bearer ca_cert file %q: %w", c.Bearer.CACert, err)
		}
	}
	if c.APIKey == "" && (c.CACert != "" || c.InsecureSkipVerify) {
		return fmt.Errorf("top-level ca_cert/insecure_skip_verify apply to the api_key form only; basic, bearer and mtls carry their own inside the block")
	}
	if c.APIKey != "" && c.CACert != "" {
		if _, err := os.Stat(c.CACert); err != nil {
			return fmt.Errorf("api_key ca_cert file %q: %w", c.CACert, err)
		}
	}
	return nil
}

func (c Credentials) authBlockCount() int {
	n := 0
	if c.Basic != nil {
		n++
	}
	if c.APIKey != "" || c.APISecret != "" {
		n++
	}
	if c.Bearer != nil {
		n++
	}
	if c.MTLS != nil {
		n++
	}
	return n
}

// authenticator returns the request Authenticator for the configured block.
// mtls authenticates at the TLS layer, so it carries no header (NoHeaderAuth).
func (c Credentials) authenticator() clusterlink.Authenticator {
	switch {
	case c.Bearer != nil:
		return clusterlink.BearerAuth{Token: c.Bearer.Token}
	case c.MTLS != nil:
		return clusterlink.NoHeaderAuth{}
	case c.Basic != nil:
		return clusterlink.BasicAuth{Username: c.Basic.Username, Password: c.Basic.Password}
	default:
		return clusterlink.BasicAuth{Username: c.APIKey, Password: c.APISecret}
	}
}

// Authenticator returns the request Authenticator for the configured auth
// block. It is the exported form of authenticator, for callers outside this
// package (e.g. migrate clients) that need to apply auth themselves on top of
// an HTTPClient built from these same credentials — HTTPClient carries only
// the TLS transport, never an Authorization header.
func (c Credentials) Authenticator() clusterlink.Authenticator {
	return c.authenticator()
}

// HTTPClient builds the HTTP client for these credentials. Always returns a
// fresh client cloned from the default transport (never http.DefaultClient) with
// TLS trust sourced from the active auth block. basic and bearer support
// optional ca_cert / insecure_skip_verify to reach CP/MDS targets behind a
// private CA. api_key (CC) uses system roots. mtls additionally presents a
// client certificate.
func (c Credentials) HTTPClient() (clusterlink.HTTPClient, error) {
	caCertFile, skipVerify := c.tlsTrust()
	pool, err := utils.OptionalCACertPool(caCertFile)
	if err != nil {
		return nil, err
	}
	tlsCfg := utils.TLSClientConfig(pool, skipVerify)
	if c.MTLS != nil {
		if err := utils.AppendClientCert(tlsCfg, c.MTLS.ClientCert, c.MTLS.ClientKey); err != nil {
			return nil, err
		}
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = tlsCfg
	return &http.Client{Transport: transport}, nil
}

// tlsTrust returns the ca_cert path and insecure-skip flag from the active block.
func (c Credentials) tlsTrust() (string, bool) {
	switch {
	case c.MTLS != nil:
		return c.MTLS.CACert, c.MTLS.InsecureSkipVerify
	case c.Bearer != nil:
		return c.Bearer.CACert, c.Bearer.InsecureSkipVerify
	case c.Basic != nil:
		return c.Basic.CACert, c.Basic.InsecureSkipVerify
	default: // api_key/api_secret — public CA unless the siblings say otherwise
		return c.CACert, c.InsecureSkipVerify
	}
}
