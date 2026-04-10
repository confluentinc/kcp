package types

import (
	"fmt"
	"net"
	"os"
	"strconv"

	"github.com/goccy/go-yaml"
)

// OSKCredentials represents the osk-credentials.yaml file
type OSKCredentials struct {
	Clusters []OSKClusterAuth `yaml:"clusters"`
}

// OSKClusterAuth contains authentication details for a single OSK cluster
type OSKClusterAuth struct {
	ID                    string                `yaml:"id"`
	BootstrapServers      []string              `yaml:"bootstrap_servers"`
	AuthMethod            AuthMethodConfig      `yaml:"auth_method"`
	InsecureSkipTLSVerify bool                  `yaml:"insecure_skip_tls_verify,omitempty"` // Only set true for test environments with self-signed certs
	Metadata              OSKCredentialMetadata `yaml:"metadata,omitempty"`
	Jolokia               *JolokiaConfig        `yaml:"jolokia,omitempty"`
	Prometheus            *PrometheusConfig     `yaml:"prometheus,omitempty"`
}

// OSKCredentialMetadata allows users to add optional organizational metadata
type OSKCredentialMetadata struct {
	Environment string            `yaml:"environment,omitempty"`
	Location    string            `yaml:"location,omitempty"`
	Labels      map[string]string `yaml:"labels,omitempty"`
}

// JolokiaConfig contains Jolokia monitoring configuration for a cluster
type JolokiaConfig struct {
	Endpoints []string           `yaml:"endpoints"`
	Auth      *JolokiaAuthConfig `yaml:"auth,omitempty"`
	TLS       *JolokiaTLSConfig  `yaml:"tls,omitempty"`
}

// JolokiaAuthConfig contains authentication credentials for Jolokia
type JolokiaAuthConfig struct {
	Username string `yaml:"username"`
	Password string `yaml:"password"`
}

// JolokiaTLSConfig contains TLS configuration for Jolokia connections
type JolokiaTLSConfig struct {
	CACert             string `yaml:"ca_cert,omitempty"`
	InsecureSkipVerify bool   `yaml:"insecure_skip_verify,omitempty"`
}

// PrometheusConfig holds Prometheus connection details for metrics queries
type PrometheusConfig struct {
	URL  string                `yaml:"url"`
	Auth *PrometheusAuthConfig `yaml:"auth,omitempty"`
	TLS  *PrometheusTLSConfig  `yaml:"tls,omitempty"`
}

// PrometheusAuthConfig holds HTTP basic auth credentials for Prometheus
type PrometheusAuthConfig struct {
	Username string `yaml:"username"`
	Password string `yaml:"password"`
}

// PrometheusTLSConfig holds TLS settings for Prometheus HTTPS connections
type PrometheusTLSConfig struct {
	CACert             string `yaml:"ca_cert,omitempty"`
	InsecureSkipVerify bool   `yaml:"insecure_skip_verify,omitempty"`
}

// NewOSKCredentialsFromFile loads OSK credentials from a YAML file
func NewOSKCredentialsFromFile(credentialsYamlPath string) (*OSKCredentials, []error) {
	data, err := os.ReadFile(credentialsYamlPath)
	if err != nil {
		return nil, []error{fmt.Errorf("failed to read osk-credentials.yaml file: %w", err)}
	}

	var credsFile OSKCredentials
	if err := yaml.Unmarshal(data, &credsFile); err != nil {
		return nil, []error{fmt.Errorf("failed to unmarshal YAML: %w", err)}
	}

	if valid, errs := credsFile.Validate(); !valid {
		return nil, errs
	}

	return &credsFile, nil
}

// Validate checks that the credentials file is valid
func (c OSKCredentials) Validate() (bool, []error) {
	errs := []error{}

	if len(c.Clusters) == 0 {
		errs = append(errs, fmt.Errorf("no clusters defined in osk-credentials.yaml"))
	}

	// Track duplicate IDs
	ids := make(map[string]bool)

	for i, cluster := range c.Clusters {
		clusterRef := fmt.Sprintf("cluster[%d]", i)

		// Validate required fields
		if cluster.ID == "" {
			errs = append(errs, fmt.Errorf("%s: 'id' is required", clusterRef))
		}
		if len(cluster.BootstrapServers) == 0 {
			errs = append(errs, fmt.Errorf("%s (id=%s): no bootstrap servers specified", clusterRef, cluster.ID))
		}

		// Check for duplicate IDs
		if cluster.ID != "" {
			if ids[cluster.ID] {
				errs = append(errs, fmt.Errorf("%s: duplicate cluster ID '%s'", clusterRef, cluster.ID))
			}
			ids[cluster.ID] = true
		}

		// Validate bootstrap servers format
		for j, server := range cluster.BootstrapServers {
			if !isValidBootstrapServer(server) {
				errs = append(errs, fmt.Errorf("%s (id=%s): invalid bootstrap server format '%s' at index %d (expected host:port)",
					clusterRef, cluster.ID, server, j))
			}
		}

		// Validate auth method
		enabledMethods := cluster.GetAuthMethods()
		if len(enabledMethods) > 1 {
			errs = append(errs, fmt.Errorf("%s (id=%s): multiple authentication methods enabled (only one allowed)",
				clusterRef, cluster.ID))
		}

		// Validate auth method-specific fields
		if err := validateAuthMethodConfig(cluster.AuthMethod, enabledMethods); err != nil {
			errs = append(errs, fmt.Errorf("%s (id=%s): %w", clusterRef, cluster.ID, err))
		}

		// Validate Jolokia config if present
		if cluster.Jolokia != nil {
			if err := validateJolokiaConfig(cluster.Jolokia); err != nil {
				errs = append(errs, fmt.Errorf("%s (id=%s): jmx: %w", clusterRef, cluster.ID, err))
			}
		}

		// Validate Prometheus config if present
		if cluster.Prometheus != nil {
			if err := validatePrometheusConfig(cluster.Prometheus); err != nil {
				errs = append(errs, fmt.Errorf("%s (id=%s): prometheus: %w", clusterRef, cluster.ID, err))
			}
		}
	}

	return len(errs) == 0, errs
}

// GetAuthMethods returns the enabled authentication methods for this cluster
func (c OSKClusterAuth) GetAuthMethods() []AuthType {
	enabledMethods := []AuthType{}

	if c.AuthMethod.UnauthenticatedPlaintext != nil && c.AuthMethod.UnauthenticatedPlaintext.Use {
		enabledMethods = append(enabledMethods, AuthTypeUnauthenticatedPlaintext)
	}
	if c.AuthMethod.UnauthenticatedTLS != nil && c.AuthMethod.UnauthenticatedTLS.Use {
		enabledMethods = append(enabledMethods, AuthTypeUnauthenticatedTLS)
	}
	if c.AuthMethod.SASLScram != nil && c.AuthMethod.SASLScram.Use {
		enabledMethods = append(enabledMethods, AuthTypeSASLSCRAM)
	}
	if c.AuthMethod.TLS != nil && c.AuthMethod.TLS.Use {
		enabledMethods = append(enabledMethods, AuthTypeTLS)
	}
	// Note: IAM not supported for OSK

	return enabledMethods
}

// GetSelectedAuthType returns the selected auth type for the cluster
func (c OSKClusterAuth) GetSelectedAuthType() (AuthType, error) {
	enabledMethods := c.GetAuthMethods()
	if len(enabledMethods) == 0 {
		return "", fmt.Errorf("no authentication method enabled for cluster")
	}
	return enabledMethods[0], nil
}

// HasJolokiaConfig returns true if the cluster has Jolokia configuration
func (c OSKClusterAuth) HasJolokiaConfig() bool {
	return c.Jolokia != nil
}

// HasPrometheusConfig returns true if the cluster has Prometheus configuration
func (c OSKClusterAuth) HasPrometheusConfig() bool {
	return c.Prometheus != nil
}

// WriteToFile writes the credentials to a YAML file
func (c *OSKCredentials) WriteToFile(filePath string) error {
	yamlData, err := yaml.Marshal(c)
	if err != nil {
		return fmt.Errorf("failed to marshal YAML: %w", err)
	}
	if err := os.WriteFile(filePath, yamlData, 0600); err != nil {
		return fmt.Errorf("failed to write YAML file: %w", err)
	}
	return nil
}

// isValidBootstrapServer checks if a bootstrap server string is valid (host:port format).
// Supports hostnames, IPv4, and bracketed IPv6 addresses (e.g. [::1]:9092).
func isValidBootstrapServer(server string) bool {
	host, port, err := net.SplitHostPort(server)
	if err != nil {
		return false
	}
	if host == "" || port == "" {
		return false
	}
	portNum, err := strconv.Atoi(port)
	if err != nil {
		return false
	}
	return portNum > 0 && portNum <= 65535
}

// validateAuthMethodConfig validates auth method specific configuration
func validateAuthMethodConfig(authMethod AuthMethodConfig, enabledMethods []AuthType) error {
	if len(enabledMethods) == 0 {
		return nil
	}

	authType := enabledMethods[0]

	switch authType {
	case AuthTypeSASLSCRAM:
		if authMethod.SASLScram == nil {
			return fmt.Errorf("sasl_scram config is nil")
		}
		if authMethod.SASLScram.Username == "" {
			return fmt.Errorf("sasl_scram username is required")
		}
		if authMethod.SASLScram.Password == "" {
			return fmt.Errorf("sasl_scram password is required")
		}
		switch authMethod.SASLScram.Mechanism {
		case "", "SHA256", "SHA512", "SCRAM-SHA-256", "SCRAM-SHA-512":
			// valid
		default:
			return fmt.Errorf("unsupported sasl_scram mechanism %q: must be SHA256, SHA512, SCRAM-SHA-256, or SCRAM-SHA-512", authMethod.SASLScram.Mechanism)
		}

	case AuthTypeTLS:
		if authMethod.TLS == nil {
			return fmt.Errorf("tls config is nil")
		}
		// Validate cert files exist
		if authMethod.TLS.CACert != "" {
			if _, err := os.Stat(authMethod.TLS.CACert); err != nil {
				return fmt.Errorf("ca_cert file not found: %s", authMethod.TLS.CACert)
			}
		}
		if authMethod.TLS.ClientCert == "" {
			return fmt.Errorf("tls client_cert is required")
		}
		if _, err := os.Stat(authMethod.TLS.ClientCert); err != nil {
			return fmt.Errorf("client_cert file not found: %s", authMethod.TLS.ClientCert)
		}
		if authMethod.TLS.ClientKey == "" {
			return fmt.Errorf("tls client_key is required")
		}
		if _, err := os.Stat(authMethod.TLS.ClientKey); err != nil {
			return fmt.Errorf("client_key file not found: %s", authMethod.TLS.ClientKey)
		}
	}

	return nil
}

// validatePrometheusConfig validates Prometheus configuration
func validatePrometheusConfig(prom *PrometheusConfig) error {
	if prom.URL == "" {
		return fmt.Errorf("url is required")
	}
	if prom.Auth != nil {
		if prom.Auth.Username == "" {
			return fmt.Errorf("auth username is required when auth is configured")
		}
		if prom.Auth.Password == "" {
			return fmt.Errorf("auth password is required when auth is configured")
		}
	}
	return nil
}

// validateJolokiaConfig validates Jolokia configuration
func validateJolokiaConfig(jolokia *JolokiaConfig) error {
	if len(jolokia.Endpoints) == 0 {
		return fmt.Errorf("at least one endpoint is required")
	}
	if jolokia.Auth != nil {
		if jolokia.Auth.Username == "" {
			return fmt.Errorf("jolokia auth username is required when auth is configured")
		}
		if jolokia.Auth.Password == "" {
			return fmt.Errorf("jolokia auth password is required when auth is configured")
		}
	}
	return nil
}
