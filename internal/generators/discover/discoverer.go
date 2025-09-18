package discover

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/aws/aws-sdk-go-v2/aws"
	kafkatypes "github.com/aws/aws-sdk-go-v2/service/kafka/types"
	"github.com/confluentinc/kcp/internal/client"
	"github.com/confluentinc/kcp/internal/services/cost"
	"github.com/confluentinc/kcp/internal/services/ec2"
	"github.com/confluentinc/kcp/internal/services/metrics"
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
	slog.Info("🚀 starting discover")

	if err := d.discoverRegions(); err != nil {
		slog.Error("failed to discover regions", "error", err)
	}

	return nil
}

func (d *Discoverer) discoverRegions() error {
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

		costExplorerClient, err := client.NewCostExplorerClient(region)
		if err != nil {
			slog.Error("failed to create cost explorer client", "region", region, "error", err)
			continue
		}
		costService := cost.NewCostService(costExplorerClient)

		cloudWatchClient, err := client.NewCloudWatchClient(region)
		if err != nil {
			slog.Error("failed to create cloudwatch client", "region", region, "error", err)
			continue
		}
		metricService := metrics.NewMetricService(cloudWatchClient)

		ec2Service, err := ec2.NewEC2Service(region)
		if err != nil {
			slog.Error("failed to create ec2 service", "region", region, "error", err)
			continue
		}

		// Discover region-level resources (costs, configurations, cluster ARNs)
		regionDiscoverer := NewRegionDiscoverer(mskService, costService)
		discoveredRegion, err := regionDiscoverer.Discover(context.Background(), region)
		if err != nil {
			slog.Error("failed to discover region", "region", region, "error", err)
			continue
		}

		// Discover detailed cluster information for each cluster in the region
		clusterDiscoverer := NewClusterDiscoverer(mskService, ec2Service, metricService)
		discoveredClusters := []types.DiscoveredCluster{}

		for _, clusterArn := range discoveredRegion.ClusterArns {
			discoveredCluster, err := clusterDiscoverer.Discover(context.Background(), clusterArn)
			if err != nil {
				slog.Error("failed to discover cluster", "cluster", clusterArn, "error", err)
				continue
			}
			discoveredClusters = append(discoveredClusters, *discoveredCluster)
		}
		discoveredRegion.Clusters = discoveredClusters

		discoveredRegions = append(discoveredRegions, *discoveredRegion)

		// Generate credential configurations for connecting to clusters
		regionEntry, err := d.captureCredentialOptions(mskService, region)
		if err != nil {
			slog.Error("failed to get region entry", "region", region, "error", err)
			continue
		}

		// Track regions with/without clusters for reporting
		if len(regionEntry.Clusters) == 0 {
			regionsWithoutClusters = append(regionsWithoutClusters, region)
		} else {
			regionEntries = append(regionEntries, *regionEntry)
		}
	}

	// Write discovery results to JSON file
	discovery := types.NewDiscovery(discoveredRegions)
	if err := discovery.WriteToJsonFile("kcp-state.json"); err != nil {
		return fmt.Errorf("failed to write discovery to file: %w", err)
	}

	// Write credential configurations to YAML file
	credentials := types.Credentials{
		Regions: regionEntries,
	}
	if err := credentials.WriteToFile("cluster-credentials.yaml"); err != nil {
		return fmt.Errorf("failed to write creds.yaml file: %w", err)
	}

	// Report regions without clusters
	if len(regionsWithoutClusters) > 0 {
		for _, region := range regionsWithoutClusters {
			slog.Info("no clusters found in region", "region", region)
		}
	}

	return nil
}

func (d *Discoverer) captureCredentialOptions(mskService *msk.MSKService, region string) (*types.RegionEntry, error) {
	// Get basic cluster info for credential generation
	clusters, err := mskService.ListClusters(context.Background(), 100)
	if err != nil {
		return nil, fmt.Errorf("failed to list clusters: %v", err)
	}

	clusterEntries := []types.ClusterEntry{}

	// Parse authentication options for each cluster
	for _, cluster := range clusters {
		clusterEntry, err := d.getAvailableClusterAuthOptions(cluster)
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

func (d *Discoverer) getAvailableClusterAuthOptions(cluster kafkatypes.Cluster) (types.ClusterEntry, error) {
	clusterEntry := types.ClusterEntry{
		Name: aws.ToString(cluster.ClusterName),
		Arn:  aws.ToString(cluster.ClusterArn),
	}

	// Check which authentication methods are enabled on the cluster
	var isSaslIamEnabled, isSaslScramEnabled, isTlsEnabled, isUnauthenticatedEnabled bool

	switch cluster.ClusterType {
	case kafkatypes.ClusterTypeProvisioned:
		// Parse authentication settings from provisioned cluster config
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
		// Serverless clusters only support IAM authentication
		isSaslIamEnabled = true
	}

	// Configure auth methods with priority: unauthenticated > iam > sasl_scram > tls
	// Only one method is set as default to avoid conflicts
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
