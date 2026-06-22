// Package targets is the Target abstraction (feasibility §7.4): Confluent Cloud
// and Confluent Platform implementations behind a common set of operations.
// Phase 1 covers the cluster-link operations with basic auth only.
package targets

import (
	"fmt"
	"os"

	"github.com/goccy/go-yaml"
)

// BasicAuth is HTTP basic auth (CP) — also how CC api_key/api_secret are sent.
type BasicAuth struct {
	Username string `yaml:"username"`
	Password string `yaml:"password"`
}

// Credentials is the parsed target-creds.yaml. Exactly one auth block is allowed.
// Phase 1: basic (CP) or api_key/api_secret (CC). bearer/mtls land in Phase 3.
type Credentials struct {
	Basic     *BasicAuth `yaml:"basic,omitempty"`
	APIKey    string     `yaml:"api_key,omitempty"`
	APISecret string     `yaml:"api_secret,omitempty"`
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
	return n
}

// basicPair returns the (username, password) to send as HTTP basic auth,
// whether configured as `basic:` (CP) or api_key/api_secret (CC).
func (c Credentials) basicPair() (string, string) {
	if c.Basic != nil {
		return c.Basic.Username, c.Basic.Password
	}
	return c.APIKey, c.APISecret
}
