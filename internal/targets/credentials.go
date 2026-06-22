// Package targets is the Target abstraction (feasibility §7.4): Confluent Cloud
// and Confluent Platform implementations behind a common set of operations.
// Cluster-link operations support four target-REST auth methods: HTTP basic
// (CP) / api_key+secret (CC), bearer token (RBAC/MDS), and mutual TLS.
package targets

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"os"

	"github.com/confluentinc/kcp/internal/services/clusterlink"
	"github.com/goccy/go-yaml"
)

// BasicAuth is HTTP basic auth (CP) — also how CC api_key/api_secret are sent.
type BasicAuth struct {
	Username string `yaml:"username"`
	Password string `yaml:"password"`
}

// BearerCreds carries an RBAC/OAuth bearer token (e.g. an MDS-issued JWT) sent
// as `Authorization: Bearer <token>`.
type BearerCreds struct {
	Token string `yaml:"token"`
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

// HTTPClient builds the HTTP client for these credentials. basic/bearer use the
// default client; mtls returns a client whose transport presents the client
// certificate and trusts the configured CA.
func (c Credentials) HTTPClient() (clusterlink.HTTPClient, error) {
	if c.MTLS == nil {
		return http.DefaultClient, nil
	}
	cert, err := tls.LoadX509KeyPair(c.MTLS.ClientCert, c.MTLS.ClientKey)
	if err != nil {
		return nil, fmt.Errorf("loading mtls client cert/key: %w", err)
	}
	tlsCfg := &tls.Config{
		Certificates:       []tls.Certificate{cert},
		InsecureSkipVerify: c.MTLS.InsecureSkipVerify,
	}
	if c.MTLS.CACert != "" {
		pem, err := os.ReadFile(c.MTLS.CACert)
		if err != nil {
			return nil, fmt.Errorf("reading mtls ca_cert: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("no certificates found in mtls ca_cert %q", c.MTLS.CACert)
		}
		tlsCfg.RootCAs = pool
	}
	return &http.Client{Transport: &http.Transport{TLSClientConfig: tlsCfg}}, nil
}
