package types

import (
	"fmt"
	"os"

	"github.com/goccy/go-yaml"
)

type Credentials struct {
	Regions []RegionAuth `yaml:"regions"`
}

func NewCredentialsFrom(fromCredentials *Credentials) *Credentials {
	// Always create with fresh structure for the current discovery run
	workingCredentials := &Credentials{}

	if fromCredentials == nil {
		workingCredentials.Regions = []RegionAuth{}
	} else {
		// Copy existing regions to preserve untouched regions
		workingCredentials.Regions = make([]RegionAuth, len(fromCredentials.Regions))
		copy(workingCredentials.Regions, fromCredentials.Regions)
	}

	return workingCredentials
}

func NewCredentialsFromFile(credentialsYamlPath string) (*Credentials, []error) {
	data, err := os.ReadFile(credentialsYamlPath)
	if err != nil {
		return nil, []error{fmt.Errorf("failed to read creds.yaml file: %w", err)}
	}

	var credsFile Credentials
	if err := yaml.Unmarshal(data, &credsFile); err != nil {
		return nil, []error{fmt.Errorf("failed to unmarshal YAML: %w", err)}
	}

	if valid, errs := credsFile.Validate(); !valid {
		return nil, errs
	}

	return &credsFile, nil
}

// UpsertRegion inserts a new region or updates an existing one by name
// Automatically preserves existing cluster auth configurations
func (c *Credentials) UpsertRegion(newRegion RegionAuth) {
	for i, existingRegion := range c.Regions {
		if existingRegion.Name == newRegion.Name {
			// Region exists - merge cluster configs and update in place
			newRegion.MergeClusterConfigs(existingRegion)
			c.Regions[i] = newRegion
			return
		}
	}
	// New region - add it
	c.Regions = append(c.Regions, newRegion)
}

func (c *Credentials) WriteToFile(filePath string) error {
	yamlData, err := c.ToYaml()
	if err != nil {
		return fmt.Errorf("failed to marshal YAML: %w", err)
	}
	if err := os.WriteFile(filePath, yamlData, 0644); err != nil {
		return fmt.Errorf("failed to write YAML file: %w", err)
	}
	return nil
}

func (c *Credentials) ToYaml() ([]byte, error) {
	yamlData, err := yaml.Marshal(c)
	if err != nil {
		return []byte{}, fmt.Errorf("failed to marshal YAML: %w", err)
	}
	return yamlData, nil
}

func (c Credentials) Validate() (bool, []error) {
	errs := []error{}

	for _, clusters := range c.Regions {
		for _, cluster := range clusters.Clusters {
			enabledMethods := cluster.GetAuthMethods()
			if len(enabledMethods) > 1 {
				errs = append(errs, fmt.Errorf("more than one authentication method enabled for %s", cluster.Arn))
				continue
			}
		}
	}
	return len(errs) == 0, errs
}

type RegionAuth struct {
	Name     string        `yaml:"name"`
	Clusters []ClusterAuth `yaml:"clusters"`
}

// MergeClusterConfigs preserves existing cluster auth configurations from the existing region
func (ra *RegionAuth) MergeClusterConfigs(existingRegion RegionAuth) {
	// Build map of cluster ARN -> existing ClusterAuth for preservation
	existingClustersByArn := make(map[string]ClusterAuth)
	for _, existingCluster := range existingRegion.Clusters {
		existingClustersByArn[existingCluster.Arn] = existingCluster
	}

	// Restore existing auth configurations for clusters that still exist
	for i := range ra.Clusters {
		if existingCluster, exists := existingClustersByArn[ra.Clusters[i].Arn]; exists {
			// Merge auth method configurations - only preserve configs for auth methods that still exist
			ra.Clusters[i].AuthMethod.MergeWith(existingCluster.AuthMethod)
		}
	}
}

type ClusterAuth struct {
	Name       string           `yaml:"name"`
	Arn        string           `yaml:"arn"`
	AuthMethod AuthMethodConfig `yaml:"auth_method"`
}

func (ce ClusterAuth) GetSelectedAuthType() (AuthType, error) {
	enabledMethods := ce.GetAuthMethods()
	if len(enabledMethods) == 0 {
		return "", fmt.Errorf("no authentication method enabled for cluster")
	}
	return enabledMethods[0], nil

}

// Gets a list of the authentication method(s) selected in the `creds.yaml` file generated during discovery.
func (ce ClusterAuth) GetAuthMethods() []AuthType {
	enabledMethods := []AuthType{}

	if ce.AuthMethod.UnauthenticatedPlaintext != nil && ce.AuthMethod.UnauthenticatedPlaintext.Use {
		enabledMethods = append(enabledMethods, AuthTypeUnauthenticatedPlaintext)
	}
	if ce.AuthMethod.UnauthenticatedTLS != nil && ce.AuthMethod.UnauthenticatedTLS.Use {
		enabledMethods = append(enabledMethods, AuthTypeUnauthenticatedTLS)
	}
	if ce.AuthMethod.IAM != nil && ce.AuthMethod.IAM.Use {
		enabledMethods = append(enabledMethods, AuthTypeIAM)
	}
	if ce.AuthMethod.SASLScram != nil && ce.AuthMethod.SASLScram.Use {
		enabledMethods = append(enabledMethods, AuthTypeSASLSCRAM)
	}
	if ce.AuthMethod.TLS != nil && ce.AuthMethod.TLS.Use {
		enabledMethods = append(enabledMethods, AuthTypeTLS)
	}

	return enabledMethods
}

type AuthMethodConfig struct {
	UnauthenticatedTLS       *UnauthenticatedTLSConfig       `yaml:"unauthenticated_tls,omitempty"`
	UnauthenticatedPlaintext *UnauthenticatedPlaintextConfig `yaml:"unauthenticated_plaintext,omitempty"`
	IAM                      *IAMConfig                      `yaml:"iam,omitempty"`
	TLS                      *TLSConfig                      `yaml:"tls,omitempty"`
	SASLScram                *SASLScramConfig                `yaml:"sasl_scram,omitempty"`
}

// MergeWith preserves existing auth configurations only for auth methods that still exist in the new config
func (amc *AuthMethodConfig) MergeWith(existing AuthMethodConfig) {
	// Only preserve existing configs if the auth method still exists in the new discovery
	if amc.UnauthenticatedTLS != nil && existing.UnauthenticatedTLS != nil {
		amc.UnauthenticatedTLS.Use = existing.UnauthenticatedTLS.Use
	}

	if amc.UnauthenticatedPlaintext != nil && existing.UnauthenticatedPlaintext != nil {
		amc.UnauthenticatedPlaintext.Use = existing.UnauthenticatedPlaintext.Use
	}

	if amc.IAM != nil && existing.IAM != nil {
		amc.IAM.Use = existing.IAM.Use
	}

	if amc.TLS != nil && existing.TLS != nil {
		amc.TLS.Use = existing.TLS.Use
		amc.TLS.CACert = existing.TLS.CACert
		amc.TLS.ClientCert = existing.TLS.ClientCert
		amc.TLS.ClientKey = existing.TLS.ClientKey
	}

	if amc.SASLScram != nil && existing.SASLScram != nil {
		amc.SASLScram.Use = existing.SASLScram.Use
		amc.SASLScram.Username = existing.SASLScram.Username
		amc.SASLScram.Password = existing.SASLScram.Password
	}
}

type UnauthenticatedPlaintextConfig struct {
	Use bool `yaml:"use"`
}

type UnauthenticatedTLSConfig struct {
	Use bool `yaml:"use"`
}

type IAMConfig struct {
	Use bool `yaml:"use"`
}

type TLSConfig struct {
	Use        bool   `yaml:"use"`
	CACert     string `yaml:"ca_cert"`
	ClientCert string `yaml:"client_cert"`
	ClientKey  string `yaml:"client_key"`
}

type SASLScramConfig struct {
	Use      bool   `yaml:"use"`
	Username string `yaml:"username"`
	Password string `yaml:"password"`
}
