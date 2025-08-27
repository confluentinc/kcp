package discover

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/goccy/go-yaml"

	"github.com/aws/aws-sdk-go-v2/aws"
	kafkatypes "github.com/aws/aws-sdk-go-v2/service/kafka/types"
	"github.com/confluentinc/kcp/internal/client"
	"github.com/confluentinc/kcp/internal/services/msk"
)

type DiscovererOpts struct {
	Regions []string
}

type Discoverer struct {
	regions []string
}

// YAML structures for cluster configuration
type SASLScramConfig struct {
	Username string `yaml:"username"`
	Password string `yaml:"password"`
}

type TLSConfig struct {
	CACert     string `yaml:"ca_cert"`
	ClientCert string `yaml:"client_cert"`
	ClientKey  string `yaml:"client_key"`
}

type ClusterAuthEntry struct {
	UseSASLIAM         *bool            `yaml:"use_sasl_iam,omitempty"`
	UseSASLScram       *bool            `yaml:"use_sasl_scram,omitempty"`
	UseTLS             *bool            `yaml:"use_tls,omitempty"`
	UseUnauthenticated *bool            `yaml:"use_unauthenticated,omitempty"`
	SkipKafka          *bool            `yaml:"skip_kafka,omitempty"`
	SASLScram          *SASLScramConfig `yaml:"sasl_scram,omitempty"`
	TLS                *TLSConfig       `yaml:"tls,omitempty"`
}

type RegionClusters map[string]ClusterAuthEntry

type DiscoveryConfig map[string]RegionClusters

func NewDiscoverer(opts DiscovererOpts) *Discoverer {
	return &Discoverer{
		regions: opts.Regions,
	}
}

func (d *Discoverer) Run() error {
	// todo: name for output folder
	outputDir := "kcp_discovery"

	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("failed to create kcp discover output folder: %w", err)
	}

	if err := d.generateCredsFile(outputDir); err != nil {
		slog.Error("failed to discover region", "error", err)
	}

	return nil
}

func (d *Discoverer) generateCredsFile(outputDir string) error {
	discoveryConfig := make(DiscoveryConfig)

	for _, r := range d.regions {
		clusterAuthEntries, err := d.getClusterAuthEntries(r)
		if err != nil {
			slog.Error("failed to discover region", "region", r, "error", err)
		}
		discoveryConfig[r] = *clusterAuthEntries
	}

	yamlFile := filepath.Join(outputDir, "creds.yaml")

	yamlData, err := yaml.Marshal(discoveryConfig)
	if err != nil {
		return fmt.Errorf("failed to marshal YAML: %w", err)
	}

	if err := os.WriteFile(yamlFile, yamlData, 0644); err != nil {
		return fmt.Errorf("failed to write YAML file: %w", err)
	}

	slog.Info("Discovery completed", "yaml_file", yamlFile)

	return nil

}

func (d *Discoverer) getClusterAuthEntries(region string) (*RegionClusters, error) {
	slog.Info("discovering region", "region", region)
	mskClient, err := client.NewMSKClient(region)
	if err != nil {
		return nil, fmt.Errorf("failed to create msk client: %v", err)
	}
	mskService := msk.NewMSKService(mskClient)

	clusters, err := mskService.ListClusters(context.Background(), 100)
	if err != nil {
		return nil, fmt.Errorf("failed to list clusters: %v", err)
	}

	regionClusters := make(RegionClusters)

	for _, cluster := range clusters {
		var isSaslIamEnabled, isSaslScramEnabled, isTlsEnabled, isUnauthenticatedEnabled bool

		switch cluster.ClusterType {
		case kafkatypes.ClusterTypeProvisioned:
			if cluster.Provisioned != nil && cluster.Provisioned.ClientAuthentication != nil {
				if cluster.Provisioned.ClientAuthentication.Sasl != nil &&
					cluster.Provisioned.ClientAuthentication.Sasl.Iam != nil {
					isSaslIamEnabled = aws.ToBool(cluster.Provisioned.ClientAuthentication.Sasl.Iam.Enabled)
				}

				if cluster.Provisioned.ClientAuthentication.Sasl != nil &&
					cluster.Provisioned.ClientAuthentication.Sasl.Scram != nil {
					isSaslScramEnabled = aws.ToBool(cluster.Provisioned.ClientAuthentication.Sasl.Scram.Enabled)
				}

				if cluster.Provisioned.ClientAuthentication.Tls != nil {
					isTlsEnabled = aws.ToBool(cluster.Provisioned.ClientAuthentication.Tls.Enabled)
				}

				if cluster.Provisioned.ClientAuthentication.Unauthenticated != nil {
					isUnauthenticatedEnabled = aws.ToBool(cluster.Provisioned.ClientAuthentication.Unauthenticated.Enabled)
				}
			}

		case kafkatypes.ClusterTypeServerless:
			// For serverless clusters, typically only IAM is supported
			isSaslIamEnabled = true
		}

		clusterAuthEntry := ClusterAuthEntry{}

		if isSaslIamEnabled {
			clusterAuthEntry.UseSASLIAM = aws.Bool(false)
		}
		if isSaslScramEnabled {
			clusterAuthEntry.UseSASLScram = aws.Bool(false)
			clusterAuthEntry.SASLScram = &SASLScramConfig{
				Username: "",
				Password: "",
			}
		}
		if isTlsEnabled {
			clusterAuthEntry.UseTLS = aws.Bool(false)
			clusterAuthEntry.TLS = &TLSConfig{
				CACert:     "",
				ClientCert: "",
				ClientKey:  "",
			}
		}
		if isUnauthenticatedEnabled {
			clusterAuthEntry.UseUnauthenticated = aws.Bool(false)
		}

		// we can always skip kafka
		clusterAuthEntry.SkipKafka = aws.Bool(false)

		regionClusters[*cluster.ClusterArn] = clusterAuthEntry
	}

	return &regionClusters, nil
}
