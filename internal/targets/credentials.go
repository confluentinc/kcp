// Package targets is the Target abstraction (feasibility §7.4): Confluent Cloud
// and Confluent Platform implementations behind a common set of operations.
// Cluster-link operations support four target-REST auth methods: HTTP basic
// (CP) / api_key+secret (CC), bearer token (RBAC/MDS), and mutual TLS.
package targets

import (
	"crypto/tls"
	"fmt"
	"net/http"
	"os"

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
type Credentials struct {
	Basic     *BasicAuth   `yaml:"basic,omitempty"`
	APIKey    string       `yaml:"api_key,omitempty"`
	APISecret string       `yaml:"api_secret,omitempty"`
	Bearer    *BearerCreds `yaml:"bearer,omitempty"`
	MTLS      *MTLSCreds   `yaml:"mtls,omitempty"`
}

// LoadCredentials reads and validates a target-creds.yaml file.
func LoadCredentials(path string) (*Credentials, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading target credentials: %w", err)
	}
	var c Credentials
	if err := yaml.UnmarshalWithOptions(data, &c, yaml.Strict()); err != nil {
		return nil, fmt.Errorf("parsing target credentials: %w", err)
	}
	if (c.APIKey != "") != (c.APISecret != "") {
		return nil, fmt.Errorf("api_key and api_secret must both be set or both omitted")
	}
	if n := c.authBlockCount(); n != 1 {
		return nil, fmt.Errorf("target credentials must specify exactly one auth block, found %d", n)
	}
	if c.Bearer != nil && c.Bearer.Token == "" {
		return nil, fmt.Errorf("bearer.token must not be empty")
	}
	if c.MTLS != nil {
		if c.MTLS.ClientCert == "" || c.MTLS.ClientKey == "" {
			return nil, fmt.Errorf("mtls requires both client_cert and client_key")
		}
		for _, f := range []string{c.MTLS.ClientCert, c.MTLS.ClientKey, c.MTLS.CACert} {
			if f == "" {
				continue // CACert is optional
			}
			if _, err := os.Stat(f); err != nil {
				return nil, fmt.Errorf("mtls certificate file %q: %w", f, err)
			}
		}
	}
	if c.Basic != nil && c.Basic.CACert != "" {
		if _, err := os.Stat(c.Basic.CACert); err != nil {
			return nil, fmt.Errorf("basic ca_cert file %q: %w", c.Basic.CACert, err)
		}
	}
	if c.Bearer != nil && c.Bearer.CACert != "" {
		if _, err := os.Stat(c.Bearer.CACert); err != nil {
			return nil, fmt.Errorf("bearer ca_cert file %q: %w", c.Bearer.CACert, err)
		}
	}
	return &c, nil
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

// HTTPClient builds the HTTP client for these credentials. Always returns a
// fresh client cloned from the default transport (never http.DefaultClient) with
// TLS trust sourced from the active auth block. basic and bearer support
// optional ca_cert / insecure_skip_verify to reach CP/MDS targets behind a
// private CA. api_key (CC) uses system roots. mtls additionally presents a
// client certificate.
func (c Credentials) HTTPClient() (clusterlink.HTTPClient, error) {
	caCertFile, skipVerify := c.tlsTrust()
	tlsCfg := &tls.Config{InsecureSkipVerify: skipVerify} //nolint:gosec // user-controlled flag
	if caCertFile != "" {
		pool, err := utils.CACertPool(caCertFile)
		if err != nil {
			return nil, err
		}
		tlsCfg.RootCAs = pool
	}
	if c.MTLS != nil {
		cert, err := tls.LoadX509KeyPair(c.MTLS.ClientCert, c.MTLS.ClientKey)
		if err != nil {
			return nil, fmt.Errorf("loading mtls client cert/key: %w", err)
		}
		tlsCfg.Certificates = []tls.Certificate{cert}
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
	default: // api_key/api_secret (CC) — public CA
		return "", false
	}
}
