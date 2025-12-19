package discover

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"

	"github.com/aws/aws-sdk-go-v2/aws"
	kafkatypes "github.com/aws/aws-sdk-go-v2/service/kafka/types"
	"github.com/confluentinc/kcp/internal/client"
	"github.com/confluentinc/kcp/internal/services/cost"
	"github.com/confluentinc/kcp/internal/services/ec2"
	"github.com/confluentinc/kcp/internal/services/markdown"
	"github.com/confluentinc/kcp/internal/services/metrics"
	"github.com/confluentinc/kcp/internal/services/msk"
	"github.com/confluentinc/kcp/internal/services/msk_connect"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/confluentinc/kcp/internal/utils"
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
		// Use conservative rate limits to avoid AWS 429 Too Many Requests errors
		// 10 requests per second with burst of 1
		mskClient, err := client.NewMSKClient(region, 10, 1)
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

	if err := state.WriteToFile(stateFileName); err != nil {
		return fmt.Errorf("failed to write state to file: %w", err)
	}

	if err := credentials.WriteToFile(credentialsFileName); err != nil {
		return fmt.Errorf("failed to write creds.yaml file: %w", err)
	}

	// TODO: in future uncomment if users want to generate report commands or else delete this and the WriteReportCommands code
	// if err := state.WriteReportCommands(reportCommandsFileName, stateFileName); err != nil {
	// 	return fmt.Errorf("failed to write report commands to file: %w", err)
	// }

	// report regions without clusters
	if len(regionsWithoutClusters) > 0 {
		for _, region := range regionsWithoutClusters {
			slog.Info("no clusters found in region", "region", region)
		}
	}

	if err := d.outputClusterSummaryTable(state); err != nil {
		slog.Warn("failed to output cluster summary table", "error", err)
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

// TODO: If/when we add the topic discovery to `kcp discover`, we should add topics/partitions count to this table to.
func (d *Discoverer) outputClusterSummaryTable(state *types.State) error {
	allClusters := []types.DiscoveredCluster{}
	for _, region := range state.Regions {
		allClusters = append(allClusters, region.Clusters...)
	}

	if len(allClusters) == 0 {
		return nil
	}

	headers := []string{"Cluster Name", "Region", "# of Brokers", "Public Access", "Kafka Version", "MSK Connectors"}
	data := [][]string{}
	arnData := [][]string{}

	for _, cluster := range allClusters {
		clusterName := cluster.Name
		clusterArn := cluster.Arn
		region := cluster.Region
		numBrokers := strconv.Itoa(cluster.ClusterMetrics.MetricMetadata.NumberOfBrokerNodes)
		publicAccess := getPublicAccess(cluster)
		kafkaVersion := utils.GetKafkaVersion(cluster.AWSClientInformation)
		connectorCount := len(cluster.AWSClientInformation.Connectors)

		data = append(data, []string{
			clusterName,
			region,
			numBrokers,
			publicAccess,
			kafkaVersion,
			strconv.Itoa(connectorCount),
		})
		arnData = append(arnData, []string{clusterName, clusterArn})
	}

	md := markdown.New()
	md.AddHeading("Discovered Clusters Summary", 1)
	md.AddParagraph("This report shows a quick overview of the discovered clusters. More detailed information can be found in the `kcp ui`.")

	md.AddTable(headers, data)

	// Separate ARN table to reduce the truncation of the column names due to the length of the ARNs.
	md.AddHeading("Cluster ARNs", 2)
	arnHeaders := []string{"Cluster Name", "Cluster ARN"}
	md.AddTable(arnHeaders, arnData)

	md.AddHeading("Discovered Topics", 2)
	topicHeaders := []string{"Cluster", "Topics", "Internal Topics", "Total Partitions", "Total Internal Partitions", "Compact Topics", "Compact Partitions"}

	topicData := [][]string{}
	for _, cluster := range allClusters {
		// Skip clusters without topic information
		if cluster.KafkaAdminClientInformation.Topics == nil {
			continue
		}

		summary := cluster.KafkaAdminClientInformation.Topics.Summary
		topicData = append(topicData, []string{
			cluster.Name,
			strconv.Itoa(summary.Topics),
			strconv.Itoa(summary.InternalTopics),
			strconv.Itoa(summary.TotalPartitions),
			strconv.Itoa(summary.TotalInternalPartitions),
			strconv.Itoa(summary.CompactTopics),
			strconv.Itoa(summary.CompactPartitions),
		})
	}

	if len(topicData) > 0 {
		md.AddTable(topicHeaders, topicData)
	}

	return md.Print(markdown.PrintOptions{ToTerminal: true, ToFile: ""})
}

func getPublicAccess(cluster types.DiscoveredCluster) string {
	clusterType := cluster.AWSClientInformation.MskClusterConfig.ClusterType

	if clusterType == kafkatypes.ClusterTypeProvisioned {
		if cluster.AWSClientInformation.MskClusterConfig.Provisioned != nil &&
			cluster.AWSClientInformation.MskClusterConfig.Provisioned.BrokerNodeGroupInfo != nil &&
			cluster.AWSClientInformation.MskClusterConfig.Provisioned.BrokerNodeGroupInfo.ConnectivityInfo != nil &&
			cluster.AWSClientInformation.MskClusterConfig.Provisioned.BrokerNodeGroupInfo.ConnectivityInfo.PublicAccess != nil {
			publicAccessType := cluster.AWSClientInformation.MskClusterConfig.Provisioned.BrokerNodeGroupInfo.ConnectivityInfo.PublicAccess.Type
			if publicAccessType != nil && aws.ToString(publicAccessType) == "SERVICE_PROVIDED_EIPS" {
				return "true"
			}
		}
	}

	return "false"
}
