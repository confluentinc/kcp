package metrics

import (
	"fmt"
	"strings"
	"time"

	"github.com/confluentinc/kcp/internal/client"
	rrm "github.com/confluentinc/kcp/internal/generators/report/cluster/metrics"
	"github.com/confluentinc/kcp/internal/services/metrics"
	"github.com/confluentinc/kcp/internal/services/msk"
	"github.com/confluentinc/kcp/internal/types"
	kafkatypes "github.com/aws/aws-sdk-go-v2/service/kafka/types"
	"github.com/confluentinc/kcp/internal/utils"

	"github.com/spf13/cobra"
)

var (
	clusterArn         string
	start              string
	end                string
	saslScramUsername  string
	saslScramPassword  string
	tlsCaCert          string
	tlsClientCert      string
	tlsClientKey       string
	skipKafka          bool
	useSaslIam         bool
	useSaslScram       bool
	useUnauthenticated bool
	useTls             bool
)

func NewReportClusterMetricsCmd() *cobra.Command {
	clusterCmd := &cobra.Command{
		Hidden: true,
		Use:    "metrics",
		Short:  "Generate metrics report on an msk cluster",
		Long: `Generate a metrics report on an msk cluster.

All flags can be provided via environment variables (uppercase, with underscores):

FLAG                        | ENV_VAR
----------------------------|-----------------------------------------------------
Required flags:   
--cluster-arn               | CLUSTER_ARN=arn:aws:kafka:us-east-1:1234567890:cluster/my-cluster/1234567890
--start                     | START=2024-01-01
--end                       | END=2024-01-02

Auth flags [choose one of the following]
--skip-kafka                | SKIP_KAFKA=true
--use-sasl-iam              | USE_SASL_IAM=true
--use-sasl-scram            | USE_SASL_SCRAM=true
--use-unauthenticated       | USE_UNAUTHENTICATED=true
--use-tls                   | USE_TLS=true

Provide with --use-sasl-scram
--sasl-scram-username       | SASL_SCRAM_USERNAME=msk-username
--sasl-scram-password       | SASL_SCRAM_PASSWORD=msk-password

Provide with --use-tls
--tls-ca-cert               | TLS_CA_CERT=path/to/ca-cert.pem
--tls-client-cert           | TLS_CLIENT_CERT=path/to/client-cert.pem
--tls-client-key            | TLS_CLIENT_KEY=path/to/client-key.pem
`,
		SilenceErrors: true,
		PreRunE:       preRunReportClusterMetrics,
		RunE:          runReportClusterMetrics,
	}

	clusterCmd.Flags().StringVar(&clusterArn, "cluster-arn", "", "cluster arn")

	clusterCmd.Flags().StringVar(&start, "start", "", "inclusive start date for metrics report (YYYY-MM-DD format)")
	clusterCmd.Flags().StringVar(&end, "end", "", "exclusive end date for metrics report (YYYY-MM-DD format)")

	clusterCmd.Flags().StringVar(&saslScramUsername, "sasl-scram-username", "", "The SASL SCRAM username")
	clusterCmd.Flags().StringVar(&saslScramPassword, "sasl-scram-password", "", "The SASL SCRAM password")
	clusterCmd.Flags().StringVar(&tlsCaCert, "tls-ca-cert", "", "The TLS CA certificate")
	clusterCmd.Flags().StringVar(&tlsClientCert, "tls-client-cert", "", "The TLS client certificate")
	clusterCmd.Flags().StringVar(&tlsClientKey, "tls-client-key", "", "The TLS client key")

	clusterCmd.Flags().BoolVar(&skipKafka, "skip-kafka", false, "skip kafka level cluster scan, use when brokers are not reachable")
	clusterCmd.Flags().BoolVar(&useSaslIam, "use-sasl-iam", false, "use sasl iam authentication")
	clusterCmd.Flags().BoolVar(&useSaslScram, "use-sasl-scram", false, "use sasl scram authentication")
	clusterCmd.Flags().BoolVar(&useUnauthenticated, "use-unauthenticated", false, "use unauthenticated authentication")
	clusterCmd.Flags().BoolVar(&useTls, "use-tls", false, "use TLS authentication")

	clusterCmd.MarkFlagRequired("cluster-arn")
	clusterCmd.MarkFlagRequired("start")
	clusterCmd.MarkFlagRequired("end")
	clusterCmd.MarkFlagsMutuallyExclusive("skip-kafka", "use-sasl-iam", "use-sasl-scram", "use-unauthenticated", "use-tls")
	clusterCmd.MarkFlagsOneRequired("skip-kafka", "use-sasl-iam", "use-sasl-scram", "use-unauthenticated", "use-tls")

	return clusterCmd
}

// sets flag values from corresponding environment variables if flags weren't explicitly provided
func preRunReportClusterMetrics(cmd *cobra.Command, args []string) error {
	if err := utils.BindEnvToFlags(cmd); err != nil {
		return err
	}

	return nil
}

func runReportClusterMetrics(cmd *cobra.Command, args []string) error {
	opts, err := parseReportRegionMetricsOpts()
	if err != nil {
		return fmt.Errorf("failed to parse region report opts: %v", err)
	}

	mskClient, err := client.NewMSKClient(opts.Region)
	if err != nil {
		return fmt.Errorf("failed to create msk client: %v", err)
	}

	kafkaAdminFactory := func(brokerAddresses []string, clientBrokerEncryptionInTransit kafkatypes.ClientBroker) (client.KafkaAdmin, error) {
		switch opts.AuthType {
		case types.AuthTypeIAM:
			return client.NewKafkaAdmin(brokerAddresses, clientBrokerEncryptionInTransit, opts.Region, client.WithIAMAuth())
		case types.AuthTypeSASLSCRAM:
			return client.NewKafkaAdmin(brokerAddresses, clientBrokerEncryptionInTransit, opts.Region, client.WithSASLSCRAMAuth(opts.SASLScramUsername, opts.SASLScramPassword))
		case types.AuthTypeUnauthenticated:
			return client.NewKafkaAdmin(brokerAddresses, clientBrokerEncryptionInTransit, opts.Region, client.WithUnauthenticatedAuth())
		case types.AuthTypeTLS:
			return client.NewKafkaAdmin(brokerAddresses, clientBrokerEncryptionInTransit, opts.Region, client.WithTLSAuth(opts.TLSCACert, opts.TLSClientCert, opts.TLSClientKey))
		default:
			return nil, fmt.Errorf("❌ Auth type: %v not yet supported", opts.AuthType)
		}
	}

	mskService := msk.NewMSKService(mskClient)

	cloudWatchClient, err := client.NewCloudWatchClient(opts.Region)
	if err != nil {
		return fmt.Errorf("failed to create cloudwatch client: %v", err)
	}

	metricService := metrics.NewMetricService(cloudWatchClient, opts.StartDate, opts.EndDate)

	regionMetrics := rrm.NewClusterMetrics(mskService, metricService, kafkaAdminFactory, *opts)
	if err := regionMetrics.Run(); err != nil {
		return fmt.Errorf("failed to report region metrics: %v", err)
	}

	return nil
}

func parseReportRegionMetricsOpts() (*rrm.ClusterMetricsOpts, error) {

	arnParts := strings.Split(clusterArn, ":")
	if len(arnParts) < 4 {
		return nil, fmt.Errorf("invalid cluster ARN format: %s", clusterArn)
	}
	region := arnParts[3]
	if region == "" {
		return nil, fmt.Errorf("region not found in cluster ARN: %s", clusterArn)
	}

	var authType types.AuthType
	switch {
	case useSaslIam:
		authType = types.AuthTypeIAM
	case useSaslScram:
		authType = types.AuthTypeSASLSCRAM
	case useUnauthenticated:
		authType = types.AuthTypeUnauthenticated
	case useTls:
		authType = types.AuthTypeTLS
	}

	const dateFormat = "2006-01-02"

	startDate, err := time.Parse(dateFormat, start)
	if err != nil {
		return nil, fmt.Errorf("invalid start date format '%s': expected YYYY-MM-DD", start)
	}

	endDate, err := time.Parse(dateFormat, end)
	if err != nil {
		return nil, fmt.Errorf("invalid end date format '%s': expected YYYY-MM-DD", end)
	}

	if startDate.After(endDate) {
		return nil, fmt.Errorf("start date '%s' cannot be after end date '%s'", start, end)
	}

	opts := rrm.ClusterMetricsOpts{
		Region:            region,
		StartDate:         startDate,
		EndDate:           endDate,
		ClusterArn:        clusterArn,
		SkipKafka:         skipKafka,
		AuthType:          authType,
		SASLScramUsername: saslScramUsername,
		SASLScramPassword: saslScramPassword,
		TLSCACert:         tlsCaCert,
		TLSClientKey:      tlsClientKey,
		TLSClientCert:     tlsClientCert,
	}

	return &opts, nil
}
