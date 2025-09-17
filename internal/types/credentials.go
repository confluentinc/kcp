package types

import (
	"fmt"
	"os"

	"github.com/goccy/go-yaml"
)

type Credentials struct {
	Regions []RegionEntry `yaml:"regions"`
}

func NewCredentials(credentialsYamlPath string) (*Credentials, []error) {
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
				errs = append(errs, fmt.Errorf("More than one authentication method enabled for %s", cluster.Arn))
				continue
			}
		}
	}
	return len(errs) == 0, errs
}

type RegionEntry struct {
	Name     string         `yaml:"name"`
	Clusters []ClusterEntry `yaml:"clusters"`
}

type ClusterEntry struct {
	Name       string           `yaml:"name"`
	Arn        string           `yaml:"arn"`
	AuthMethod AuthMethodConfig `yaml:"auth_method"`
}

func (ce ClusterEntry) GetSelectedAuthType() (AuthType, error) {
	enabledMethods := ce.GetAuthMethods()
	if len(enabledMethods) == 0 {
		return "", fmt.Errorf("no authentication method enabled for cluster")
	}

	return enabledMethods[0], nil
}

// Gets a list of the authentication method(s) selected in the `creds.yaml` file generated during discovery.
func (ce ClusterEntry) GetAuthMethods() []AuthType {
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
