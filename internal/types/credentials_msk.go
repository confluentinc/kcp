package types

import (
	"fmt"

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
	return loadCredentialsFile[Credentials](credentialsYamlPath)
}

// UpsertTargetedClusters creates or replaces only the clusters present in newRegion.Clusters,
// preserving the auth config of every other cluster in the region. If the region does not
// exist it is added as-is. Mirrors State.UpsertTargetedClusters for the credentials file.
func (c *Credentials) UpsertTargetedClusters(newRegion RegionAuth) {
	for i := range c.Regions {
		if c.Regions[i].Name == newRegion.Name {
			for _, targeted := range newRegion.Clusters {
				c.Regions[i].UpsertCluster(targeted)
			}
			return
		}
	}
	c.Regions = append(c.Regions, newRegion)
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
	return writeYAMLFile(filePath, c)
}

func (c *Credentials) ToYaml() ([]byte, error) {
	yamlData, err := yaml.Marshal(c)
	if err != nil {
		return []byte{}, fmt.Errorf("failed to marshal YAML: %w", err)
	}
	return yamlData, nil
}

// FindClusterByArn searches all regions for a cluster matching the given ARN.
func (c *Credentials) FindClusterByArn(arn string) (*ClusterAuth, error) {
	for _, region := range c.Regions {
		for i, cluster := range region.Clusters {
			if cluster.Arn == arn {
				return &region.Clusters[i], nil
			}
		}
	}
	return nil, fmt.Errorf("cluster with ARN %q not found in credentials file", arn)
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

// UpsertCluster creates or replaces a single cluster auth by ARN, preserving the auth
// configs of every other cluster and merging auth-method config onto the replaced entry.
func (ra *RegionAuth) UpsertCluster(newCluster ClusterAuth) {
	for i := range ra.Clusters {
		if ra.Clusters[i].Arn == newCluster.Arn {
			newCluster.AuthMethod.MergeWith(ra.Clusters[i].AuthMethod)
			ra.Clusters[i] = newCluster
			return
		}
	}
	ra.Clusters = append(ra.Clusters, newCluster)
}

type ClusterAuth struct {
	Name       string           `yaml:"name"`
	Arn        string           `yaml:"arn"`
	AuthMethod AuthMethodConfig `yaml:"auth_method"`
}

func (ce ClusterAuth) GetSelectedAuthType() (AuthType, error) {
	return ce.AuthMethod.SelectedAuthType(true)
}

// Gets a list of the authentication method(s) selected in the `creds.yaml` file generated during discovery.
func (ce ClusterAuth) GetAuthMethods() []AuthType {
	return ce.AuthMethod.EnabledAuthMethods(true)
}

type AuthMethodConfig struct {
	IAM                      *IAMConfig                      `yaml:"iam,omitempty"`
	SASLScram                *SASLScramConfig                `yaml:"sasl_scram,omitempty"`
	SASLPlain                *SASLPlainConfig                `yaml:"sasl_plain,omitempty"`
	TLS                      *TLSConfig                      `yaml:"tls,omitempty"`
	UnauthenticatedTLS       *UnauthenticatedTLSConfig       `yaml:"unauthenticated_tls,omitempty"`
	UnauthenticatedPlaintext *UnauthenticatedPlaintextConfig `yaml:"unauthenticated_plaintext,omitempty"`
}

// EnabledAuthMethods returns the enabled methods in canonical order.
// includeIAM is false for sources that don't support IAM (Apache Kafka / OSK).
func (amc AuthMethodConfig) EnabledAuthMethods(includeIAM bool) []AuthType {
	methods := []AuthType{}
	if amc.UnauthenticatedPlaintext != nil && amc.UnauthenticatedPlaintext.Use {
		methods = append(methods, AuthTypeUnauthenticatedPlaintext)
	}
	if amc.UnauthenticatedTLS != nil && amc.UnauthenticatedTLS.Use {
		methods = append(methods, AuthTypeUnauthenticatedTLS)
	}
	if includeIAM && amc.IAM != nil && amc.IAM.Use {
		methods = append(methods, AuthTypeIAM)
	}
	if amc.SASLScram != nil && amc.SASLScram.Use {
		methods = append(methods, AuthTypeSASLSCRAM)
	}
	if amc.SASLPlain != nil && amc.SASLPlain.Use {
		methods = append(methods, AuthTypeSASLPlain)
	}
	if amc.TLS != nil && amc.TLS.Use {
		methods = append(methods, AuthTypeTLS)
	}
	return methods
}

// SelectedAuthType returns the single enabled method, or an error if none.
func (amc AuthMethodConfig) SelectedAuthType(includeIAM bool) (AuthType, error) {
	enabled := amc.EnabledAuthMethods(includeIAM)
	if len(enabled) == 0 {
		return "", fmt.Errorf("no authentication method enabled for cluster")
	}
	return enabled[0], nil
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
		amc.SASLScram.Mechanism = existing.SASLScram.Mechanism
	}

	if amc.SASLPlain != nil && existing.SASLPlain != nil {
		amc.SASLPlain.Use = existing.SASLPlain.Use
		amc.SASLPlain.Username = existing.SASLPlain.Username
		amc.SASLPlain.Password = existing.SASLPlain.Password
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
	Use       bool   `yaml:"use"`
	Username  string `yaml:"username"`
	Password  string `yaml:"password"`
	Mechanism string `yaml:"mechanism,omitempty"` // "SHA256" or "SHA512". MSK requires "SHA512", Apache Kafka commonly uses "SHA256"
}

// NormalizeSaslMechanism converts shorthand mechanism values (e.g. "SHA256")
// to the Kafka-standard format (e.g. "SCRAM-SHA-256").
// Returns empty string for empty input.
func NormalizeSaslMechanism(mechanism string) string {
	switch mechanism {
	case "SHA256", "SCRAM-SHA-256":
		return "SCRAM-SHA-256"
	case "SHA512", "SCRAM-SHA-512":
		return "SCRAM-SHA-512"
	default:
		return mechanism
	}
}

type SASLPlainConfig struct {
	Use      bool   `yaml:"use"`
	Username string `yaml:"username"`
	Password string `yaml:"password"`
}
