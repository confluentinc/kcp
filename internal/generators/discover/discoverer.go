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
	"github.com/confluentinc/kcp/internal/types"
)

type DiscovererOpts struct {
	Regions []string
}

type Discoverer struct {
	regions []string
}

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

	// create the creds yaml file for all clusters in each provided region
	if err := d.generateCredsFile(outputDir); err != nil {
		slog.Error("failed to discover region", "error", err)
	}

	return nil
}

func (d *Discoverer) generateCredsFile(outputDir string) error {
	credsYaml := make(types.CredsYaml)

	for _, region := range d.regions {
		clusterEntries, err := d.getClusterEntries(region)
		if err != nil {
			slog.Error("failed to discover region", "region", region, "error", err)
		}
		credsYaml[region] = *clusterEntries
	}

	yamlFile := filepath.Join(outputDir, "creds.yaml")

	yamlData, err := yaml.Marshal(credsYaml)
	if err != nil {
		return fmt.Errorf("failed to marshal YAML: %w", err)
	}

	if err := os.WriteFile(yamlFile, yamlData, 0644); err != nil {
		return fmt.Errorf("failed to write YAML file: %w", err)
	}

	slog.Info("generated creds.yaml", "file", yamlFile)

	return nil

}

func (d *Discoverer) getClusterEntries(region string) (*types.RegionEntries, error) {
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

	regionEntries := make(types.RegionEntries)

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

		clusterEntry := types.ClusterEntry{}

		// we can always skip kafka
		clusterEntry.SkipKafka = aws.Bool(false)

		// only include auth entries for enabled auth types
		if isSaslIamEnabled {
			clusterEntry.UseSASLIAM = aws.Bool(false)
		}
		if isSaslScramEnabled {
			clusterEntry.UseSASLScram = aws.Bool(false)
			clusterEntry.SASLScram = &types.SASLScramConfig{
				Username: "",
				Password: "",
			}
		}
		if isTlsEnabled {
			clusterEntry.UseTLS = aws.Bool(false)
			clusterEntry.TLS = &types.TLSConfig{
				CACert:     "",
				ClientCert: "",
				ClientKey:  "",
			}
		}
		if isUnauthenticatedEnabled {
			clusterEntry.UseUnauthenticated = aws.Bool(false)
		}

		regionEntries[*cluster.ClusterArn] = clusterEntry
	}

	return &regionEntries, nil
}
