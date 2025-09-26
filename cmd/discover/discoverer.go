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
	"github.com/confluentinc/kcp/internal/services/msk_connect"
	"github.com/confluentinc/kcp/internal/types"
)

type DiscovererOpts struct {
	Regions     []string
	State       *types.State
	Credentials *types.Credentials
}

type Discoverer struct {
	regions     []string
	state       *types.State
	credentials *types.Credentials
}

func NewDiscoverer(opts DiscovererOpts) *Discoverer {
	return &Discoverer{
		regions:     opts.Regions,
		state:       opts.State,
		credentials: opts.Credentials,
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
	regionsWithoutClusters := []string{}
	// initialize state/credentials from existing state/credentials if passed in
	state := types.NewStateFrom(d.state)
	credentials := types.NewCredentialsFrom(d.credentials)

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

		mskConnectClient, err := client.NewMSKConnectClient(region)
		if err != nil {
			slog.Error("failed to create msk connect client", "region", region, "error", err)
			continue
		}
		mskConnectService := msk_connect.NewMSKConnectService(mskConnectClient)

		// discover region-level resources (costs, configurations, cluster ARNs)
		regionDiscoverer := NewRegionDiscoverer(mskService, costService)
		discoveredRegion, err := regionDiscoverer.Discover(context.Background(), region)
		if err != nil {
			slog.Error("failed to discover region", "region", region, "error", err)
			continue
		}

		// discover detailed cluster information for each cluster in the region
		clusterDiscoverer := NewClusterDiscoverer(mskService, ec2Service, metricService, mskConnectService)
		discoveredClusters := []types.DiscoveredCluster{}

		for _, clusterArn := range discoveredRegion.ClusterArns {
			discoveredCluster, err := clusterDiscoverer.Discover(context.Background(), clusterArn, region)
			if err != nil {
				slog.Error("failed to discover cluster", "cluster", clusterArn, "error", err)
				continue
			}
			discoveredClusters = append(discoveredClusters, *discoveredCluster)
		}

		discoveredRegion.Clusters = discoveredClusters
		// upsert region into state (preserves untouched regions)
		state.UpsertRegion(*discoveredRegion)

		// generate credential configurations for connecting to clusters
		regionAuth, err := d.captureCredentialOptions(discoveredRegion.Clusters, region)
		if err != nil {
			slog.Error("failed to get region entry", "region", region, "error", err)
			continue
		}

		// upsert region credentials (preserves existing region auths)
		credentials.UpsertRegion(*regionAuth)

		// track regions with/without clusters for reporting
		if len(regionAuth.Clusters) == 0 {
			regionsWithoutClusters = append(regionsWithoutClusters, region)
		}
	}

	if err := state.WriteToJsonFile(stateFileName); err != nil {
		return fmt.Errorf("failed to write state to file: %w", err)
	}

	if err := credentials.WriteToFile(credentialsFileName); err != nil {
		return fmt.Errorf("failed to write creds.yaml file: %w", err)
	}

	// report regions without clusters
	if len(regionsWithoutClusters) > 0 {
		for _, region := range regionsWithoutClusters {
			slog.Info("no clusters found in region", "region", region)
		}
	}

	return nil
}

func (d *Discoverer) captureCredentialOptions(clusters []types.DiscoveredCluster, region string) (*types.RegionAuth, error) {
	clusterAuths := []types.ClusterAuth{}

	// Parse authentication options for each cluster
	for _, cluster := range clusters {
		clusterAuth, err := d.getAvailableClusterAuthOptions(cluster.AWSClientInformation.MskClusterConfig)
		if err != nil {
			slog.Error("failed to get cluster entry", "cluster", cluster.Name, "error", err)
			continue
		}
		clusterAuths = append(clusterAuths, clusterAuth)
	}

	return &types.RegionAuth{
		Name:     region,
		Clusters: clusterAuths,
	}, nil

}

func (d *Discoverer) getAvailableClusterAuthOptions(cluster kafkatypes.Cluster) (types.ClusterAuth, error) {
	clusterAuth := types.ClusterAuth{
		Name: aws.ToString(cluster.ClusterName),
		Arn:  aws.ToString(cluster.ClusterArn),
	}

	// Check which authentication methods are enabled on the cluster
	var isSaslIamEnabled, isSaslScramEnabled, isTlsEnabled, isUnauthenticatedTLSEnabled, isUnauthenticatedPlaintextEnabled bool

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

			if cluster.Provisioned.ClientAuthentication.Unauthenticated != nil &&
				cluster.Provisioned.EncryptionInfo != nil &&
				*cluster.Provisioned.ClientAuthentication.Unauthenticated.Enabled {

				encryptionInTransit := cluster.Provisioned.EncryptionInfo.EncryptionInTransit.ClientBroker
				if encryptionInTransit == kafkatypes.ClientBrokerTls || encryptionInTransit == kafkatypes.ClientBrokerTlsPlaintext {
					isUnauthenticatedTLSEnabled = true
				}
				if encryptionInTransit == kafkatypes.ClientBrokerPlaintext || encryptionInTransit == kafkatypes.ClientBrokerTlsPlaintext {
					isUnauthenticatedPlaintextEnabled = true
				}

			}
		}

	case kafkatypes.ClusterTypeServerless:
		// Serverless clusters only support IAM authentication
		isSaslIamEnabled = true
	}

	// Configure auth methods with priority: unauthenticated_tls > unauthenticated_plaintext > iam > sasl_scram > tls
	// Only one method is set as default to avoid conflicts
	defaultAuthSelected := false
	if isUnauthenticatedTLSEnabled {
		clusterAuth.AuthMethod.UnauthenticatedTLS = &types.UnauthenticatedTLSConfig{
			Use: !defaultAuthSelected,
		}
		defaultAuthSelected = true
	}
	if isUnauthenticatedPlaintextEnabled {
		clusterAuth.AuthMethod.UnauthenticatedPlaintext = &types.UnauthenticatedPlaintextConfig{
			Use: !defaultAuthSelected,
		}
		defaultAuthSelected = true
	}
	if isSaslIamEnabled {
		clusterAuth.AuthMethod.IAM = &types.IAMConfig{
			Use: !defaultAuthSelected,
		}
		defaultAuthSelected = true
	}
	if isSaslScramEnabled {
		clusterAuth.AuthMethod.SASLScram = &types.SASLScramConfig{
			Use:      !defaultAuthSelected,
			Username: "",
			Password: "",
		}
		defaultAuthSelected = true
	}
	if isTlsEnabled {
		clusterAuth.AuthMethod.TLS = &types.TLSConfig{
			Use:        !defaultAuthSelected,
			CACert:     "",
			ClientCert: "",
			ClientKey:  "",
		}
		defaultAuthSelected = true
	}

	return clusterAuth, nil
}
