package discover_v2

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"

	"github.com/aws/aws-sdk-go-v2/aws"
	kafkatypes "github.com/aws/aws-sdk-go-v2/service/kafka/types"
	"github.com/confluentinc/kcp/internal/client"
	"github.com/confluentinc/kcp/internal/services/ec2"
	"github.com/confluentinc/kcp/internal/services/msk"
	"github.com/confluentinc/kcp/internal/types"
)

type DiscovererV2Opts struct {
	Regions []string
}

type DiscovererV2 struct {
	regions []string
}

func NewDiscovererV2(opts DiscovererV2Opts) *DiscovererV2 {
	return &DiscovererV2{
		regions: opts.Regions,
	}
}

func (d *DiscovererV2) Run() error {
	fmt.Println("Running Discover V2")

	if err := d.discoverRegions(); err != nil {
		slog.Error("failed to discover regions", "error", err)
	}

	return nil
}

// cost == region
// metrics == cluster

func (d *DiscovererV2) discoverRegions() error {
	regionEntries := []types.RegionEntry{}
	regionsWithoutClusters := []string{}

	discoveredRegions := []types.DiscoveredRegion{}

	for _, region := range d.regions {
		mskClient, err := client.NewMSKClient(region)
		if err != nil {
			slog.Error("failed to create msk client", "region", region, "error", err)
			continue
		}
		mskService := msk.NewMSKService(mskClient)

		ec2Service, err := ec2.NewEC2Service(region)
		if err != nil {
			slog.Error("failed to create ec2 service", "region", region, "error", err)
			continue
		}

		clusterDiscoverer := NewClusterDiscoverer(mskService, ec2Service)
		regionDiscoverer := NewRegionDiscoverer(mskService, clusterDiscoverer)

		discoveredRegion, err := regionDiscoverer.Discover(context.Background(), region)
		if err != nil {
			slog.Error("failed to discover region", "region", region, "error", err)
			continue
		}
		// add region to the discovered regions
		discoveredRegions = append(discoveredRegions, *discoveredRegion)

		regionEntry, err := d.getRegionEntry(mskService, region)
		if err != nil {
			slog.Error("failed to get region entry", "region", region, "error", err)
			continue
		}

		if len(regionEntry.Clusters) == 0 {
			regionsWithoutClusters = append(regionsWithoutClusters, region)
		} else {
			regionEntries = append(regionEntries, *regionEntry)
		}
	}

	discovery := types.NewDiscovery(discoveredRegions)
	if err := discovery.WriteToJsonFile("kcp-state.json"); err != nil {
		slog.Error("failed to write discovery to file", "error", err)
	}

	credentials := types.Credentials{
		Regions: regionEntries,
	}
	if err := credentials.WriteToFile("cluster-credentials.yaml"); err != nil {
		return fmt.Errorf("failed to write creds.yaml file: %w", err)
	}

	if len(regionsWithoutClusters) > 0 {
		for _, region := range regionsWithoutClusters {
			slog.Info("no clusters found in region", "region", region)
		}
	}

	return nil
}

func (d *DiscovererV2) writeToJsonFile(discovery types.Discovery, filePath string) error {
	data, err := json.MarshalIndent(discovery, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal discovery: %v", err)
	}
	if err := os.WriteFile(filePath, data, 0644); err != nil {
		return fmt.Errorf("failed to write discovery to file: %v", err)
	}
	return nil
}

func (d *DiscovererV2) getRegionEntry(mskService *msk.MSKService, region string) (*types.RegionEntry, error) {
	clusters, err := mskService.ListClusters(context.Background(), 100)
	if err != nil {
		return nil, fmt.Errorf("failed to list clusters: %v", err)
	}

	clusterEntries := []types.ClusterEntry{}

	for _, cluster := range clusters {
		clusterEntry, err := d.getClusterEntry(cluster)
		if err != nil {
			slog.Error("failed to get cluster entry", "cluster", cluster.ClusterName, "error", err)
			continue
		}
		clusterEntries = append(clusterEntries, clusterEntry)
	}

	return &types.RegionEntry{
		Name:     region,
		Clusters: clusterEntries,
	}, nil

}

func (d *DiscovererV2) getClusterEntry(cluster kafkatypes.Cluster) (types.ClusterEntry, error) {
	clusterEntry := types.ClusterEntry{
		Name: aws.ToString(cluster.ClusterName),
		Arn:  aws.ToString(cluster.ClusterArn),
	}

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

	// we want a SINGLE auth mech to be enabled by default
	// priority is unauthenticated > iam > sasl_scram > tls
	defaultAuthSelected := false
	if isUnauthenticatedEnabled {
		clusterEntry.AuthMethod.Unauthenticated = &types.UnauthenticatedConfig{
			Use: !defaultAuthSelected,
		}
		defaultAuthSelected = true
	}
	if isSaslIamEnabled {
		clusterEntry.AuthMethod.IAM = &types.IAMConfig{
			Use: !defaultAuthSelected,
		}
		defaultAuthSelected = true
	}
	if isSaslScramEnabled {
		clusterEntry.AuthMethod.SASLScram = &types.SASLScramConfig{
			Use:      !defaultAuthSelected,
			Username: "",
			Password: "",
		}
		defaultAuthSelected = true
	}
	if isTlsEnabled {
		clusterEntry.AuthMethod.TLS = &types.TLSConfig{
			Use:        !defaultAuthSelected,
			CACert:     "",
			ClientCert: "",
			ClientKey:  "",
		}
		defaultAuthSelected = true
	}

	return clusterEntry, nil
}
