package types

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/goccy/go-yaml"
)

// OSKCredentials represents the osk-credentials.yaml file
type OSKCredentials struct {
	Clusters []OSKClusterAuth `yaml:"clusters"`
}

// OSKClusterAuth contains authentication details for a single OSK cluster
type OSKClusterAuth struct {
	ID               string                `yaml:"id"`
	BootstrapServers []string              `yaml:"bootstrap_servers"`
	AuthMethod       AuthMethodConfig      `yaml:"auth_method"`
	Metadata         OSKCredentialMetadata `yaml:"metadata,omitempty"`
}

// OSKCredentialMetadata allows users to add optional organizational metadata
type OSKCredentialMetadata struct {
	Environment string            `yaml:"environment,omitempty"`
	Location    string            `yaml:"location,omitempty"`
	Labels      map[string]string `yaml:"labels,omitempty"`
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

// WriteToFile writes the credentials to a YAML file
func (c *OSKCredentials) WriteToFile(filePath string) error {
	yamlData, err := yaml.Marshal(c)
	if err != nil {
		return fmt.Errorf("failed to marshal YAML: %w", err)
	}
	if err := os.WriteFile(filePath, yamlData, 0644); err != nil {
		return fmt.Errorf("failed to write YAML file: %w", err)
	}
	return nil
}

// isValidBootstrapServer checks if a bootstrap server string is valid (host:port format)
func isValidBootstrapServer(server string) bool {
	parts := strings.Split(server, ":")
	if len(parts) != 2 {
		return false
	}

	host := parts[0]
	port := parts[1]

	if host == "" || port == "" {
		return false
	}

	// Validate port is numeric
	if _, err := strconv.Atoi(port); err != nil {
		return false
	}

	return true
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
