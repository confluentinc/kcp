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

	if err := d.processRegionsForCredsYaml(outputDir); err != nil {
		slog.Error("failed to discover region", "error", err)
	}

	return nil
}

func (d *Discoverer) processRegionsForCredsYaml(outputDir string) error {
	credsYaml := types.CredsYaml{
		Regions: make(map[string]types.RegionEntries),
	}

	regionsWithoutClusters := []string{}
	for _, region := range d.regions {
		clusterEntries, err := d.getClusterEntries(region)
		if err != nil {
			slog.Error("failed to discover region", "region", region, "error", err)
			continue
		}

		if len(clusterEntries.Clusters) == 0 {
			regionsWithoutClusters = append(regionsWithoutClusters, region)
		} else {
			credsYaml.Regions[region] = *clusterEntries
		}
	}

	yamlData, err := yaml.Marshal(credsYaml)
	if err != nil {
		return fmt.Errorf("failed to marshal YAML: %w", err)
	}

	yamlFile := filepath.Join(outputDir, "creds.yaml")

	if err := os.WriteFile(yamlFile, yamlData, 0644); err != nil {
		return fmt.Errorf("failed to write YAML file: %w", err)
	}

	slog.Info("generated creds.yaml", "file", yamlFile)

	if len(regionsWithoutClusters) > 0 {
		for _, region := range regionsWithoutClusters {
			slog.Info("no clusters found in region", "region", region)
		}
	}

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

	regionEntries := types.RegionEntries{
		Clusters: make(map[string]types.ClusterEntry),
	}

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

		// we want a SINGLE auth mech to enabled by default
		// priotity preference is unauthenticated > iam > sasl_scram > tls
		authEnabled := false
		if isUnauthenticatedEnabled {
			clusterEntry.AuthMethod.Unauthenticated = &types.UnauthenticatedConfig{
				Enabled: !authEnabled,
			}
			authEnabled = true
		}
		if isSaslIamEnabled {
			clusterEntry.AuthMethod.IAM = &types.IAMConfig{
				Enabled: !authEnabled,
			}
			authEnabled = true
		}
		if isSaslScramEnabled {
			clusterEntry.AuthMethod.SASLScram = &types.SASLScramConfig{
				Enabled:  !authEnabled,
				Username: "",
				Password: "",
			}
			authEnabled = true
		}
		if isTlsEnabled {
			clusterEntry.AuthMethod.TLS = &types.TLSConfig{
				Enabled:    !authEnabled,
				CACert:     "",
				ClientCert: "",
				ClientKey:  "",
			}
			authEnabled = true
		}

		regionEntries.Clusters[*cluster.ClusterArn] = clusterEntry
	}

	return &regionEntries, nil
}
