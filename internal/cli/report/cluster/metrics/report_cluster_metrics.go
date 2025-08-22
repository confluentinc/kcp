package metrics

import (
	"fmt"
	"strings"
	"time"

	kafkatypes "github.com/aws/aws-sdk-go-v2/service/kafka/types"
	"github.com/confluentinc/kcp/internal/client"
	rrm "github.com/confluentinc/kcp/internal/generators/report/cluster/metrics"
	"github.com/confluentinc/kcp/internal/services/metrics"
	"github.com/confluentinc/kcp/internal/services/msk"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/confluentinc/kcp/internal/utils"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

var (
	clusterArn         string
	start              string
	end                string
	lastDay            bool
	lastWeek           bool
	lastThirtyDays     bool
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
		Use:           "metrics",
		Short:         "Generate metrics report on an msk cluster",
		Long:          "Generate a metrics report on an msk cluster.",
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		PreRunE:       preRunReportClusterMetrics,
		RunE:          runReportClusterMetrics,
	}

	groups := map[*pflag.FlagSet]string{}
	// Required flags.
	requiredFlags := pflag.NewFlagSet("required", pflag.ExitOnError)
	requiredFlags.SortFlags = false
	requiredFlags.StringVar(&clusterArn, "cluster-arn", "", "cluster arn")
	// requiredFlags.StringVar(&start, "start", "", "inclusive start date for metrics report (YYYY-MM-DD format)")
	// requiredFlags.StringVar(&end, "end", "", "exclusive end date for metrics report (YYYY-MM-DD format)")
	clusterCmd.Flags().AddFlagSet(requiredFlags)
	groups[requiredFlags] = "Required Flags"

	// Time range flags.
	timeRangeFlags := pflag.NewFlagSet("time-range", pflag.ExitOnError)
	timeRangeFlags.SortFlags = false
	timeRangeFlags.StringVar(&start, "start", "", "inclusive start date for cost report (YYYY-MM-DD)")
	timeRangeFlags.StringVar(&end, "end", "", "exclusive end date for cost report (YYYY-MM-DD)")
	timeRangeFlags.BoolVar(&lastDay, "last-day", false, "generate cost report for the previous day")
	timeRangeFlags.BoolVar(&lastWeek, "last-week", false, "generate cost report for the previous 7 days (not including today)")
	timeRangeFlags.BoolVar(&lastThirtyDays, "last-thirty-days", false, "generate cost report for the previous 30 days (not including today)")
	clusterCmd.Flags().AddFlagSet(timeRangeFlags)
	groups[timeRangeFlags] = "Time Range Flags"

	// Authentication flags.
	authFlags := pflag.NewFlagSet("auth", pflag.ExitOnError)
	authFlags.SortFlags = false
	authFlags.BoolVar(&useSaslIam, "use-sasl-iam", false, "Use IAM authentication")
	authFlags.BoolVar(&useSaslScram, "use-sasl-scram", false, "Use SASL/SCRAM authentication")
	authFlags.BoolVar(&useTls, "use-tls", false, "Use TLS authentication")
	authFlags.BoolVar(&useUnauthenticated, "use-unauthenticated", false, "Use unauthenticated authentication")
	authFlags.BoolVar(&skipKafka, "skip-kafka", false, "Skip kafka level cluster scan, use when brokers are not reachable")
	clusterCmd.Flags().AddFlagSet(authFlags)
	groups[authFlags] = "Authentication Flags"

	// SASL/SCRAM flags.
	saslScramFlags := pflag.NewFlagSet("sasl-scram", pflag.ExitOnError)
	saslScramFlags.SortFlags = false
	saslScramFlags.StringVar(&saslScramUsername, "sasl-scram-username", "", "The SASL SCRAM username")
	saslScramFlags.StringVar(&saslScramPassword, "sasl-scram-password", "", "The SASL SCRAM password")
	clusterCmd.Flags().AddFlagSet(saslScramFlags)
	groups[saslScramFlags] = "SASL/SCRAM Flags"

	// TLS flags.
	tlsFlags := pflag.NewFlagSet("tls", pflag.ExitOnError)
	tlsFlags.SortFlags = false
	tlsFlags.StringVar(&tlsCaCert, "tls-ca-cert", "", "The TLS CA certificate")
	tlsFlags.StringVar(&tlsClientCert, "tls-client-cert", "", "The TLS client certificate")
	tlsFlags.StringVar(&tlsClientKey, "tls-client-key", "", "The TLS client key")
	clusterCmd.Flags().AddFlagSet(tlsFlags)
	groups[tlsFlags] = "TLS Flags"

	clusterCmd.SetUsageFunc(func(c *cobra.Command) error {
		fmt.Printf("%s\n\n", c.Short)
		flagOrder := []*pflag.FlagSet{requiredFlags, timeRangeFlags, authFlags, saslScramFlags, tlsFlags}
		groupNames := []string{"Required Flags", "Time Range Flags", "Authentication Flags", "SASL/SCRAM Flags", "TLS Flags"}
		for i, fs := range flagOrder {
			usage := fs.FlagUsages()
			if usage != "" {
				fmt.Printf("%s:\n%s\n", groupNames[i], usage)
			}
		}
		fmt.Println("All flags can be provided via environment variables (uppercase, with underscores).")
		return nil
	})
	clusterCmd.MarkFlagRequired("cluster-arn")

	clusterCmd.MarkFlagsMutuallyExclusive("start", "last-day", "last-week", "last-thirty-days")
	clusterCmd.MarkFlagsOneRequired("start", "last-day", "last-week", "last-thirty-days")
	clusterCmd.MarkFlagsRequiredTogether("start", "end")

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
		return fmt.Errorf("failed to parse cluster report opts: %v", err)
	}

	mskClient, err := client.NewMSKClient(opts.Region)
	if err != nil {
		return fmt.Errorf("failed to create msk client: %v", err)
	}

	mskService := msk.NewMSKService(mskClient)


	kafkaAdminFactory := func(brokerAddresses []string, clientBrokerEncryptionInTransit kafkatypes.ClientBroker, kafkaVersion string) (client.KafkaAdmin, error) {
		switch opts.AuthType {
		case types.AuthTypeIAM:
			return client.NewKafkaAdmin(brokerAddresses, clientBrokerEncryptionInTransit, opts.Region, kafkaVersion, client.WithIAMAuth())
		case types.AuthTypeSASLSCRAM:
			return client.NewKafkaAdmin(brokerAddresses, clientBrokerEncryptionInTransit, opts.Region, kafkaVersion, client.WithSASLSCRAMAuth(opts.SASLScramUsername, opts.SASLScramPassword))
		case types.AuthTypeUnauthenticated:
			return client.NewKafkaAdmin(brokerAddresses, clientBrokerEncryptionInTransit, opts.Region, kafkaVersion, client.WithUnauthenticatedAuth())
		case types.AuthTypeTLS:
			return client.NewKafkaAdmin(brokerAddresses, clientBrokerEncryptionInTransit, opts.Region, kafkaVersion, client.WithTLSAuth(opts.TLSCACert, opts.TLSClientCert, opts.TLSClientKey))
		default:
			return nil, fmt.Errorf("❌ Auth type: %v not yet supported", opts.AuthType)
		}
	}

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
	var startDate, endDate time.Time
	var err error

	switch {
	case start != "" && end != "":
		startDate, err = time.Parse(dateFormat, start)
		if err != nil {
			return nil, fmt.Errorf("invalid start date format '%s': expected YYYY-MM-DD", start)
		}

		endDate, err = time.Parse(dateFormat, end)
		if err != nil {
			return nil, fmt.Errorf("invalid end date format '%s': expected YYYY-MM-DD", end)
		}

		if startDate.After(endDate) {
			return nil, fmt.Errorf("start date '%s' cannot be after end date '%s'", start, end)
		}

	case lastDay:
		now := time.Now()
		startDate = now.AddDate(0, 0, -1).UTC().Truncate(24 * time.Hour)
		endDate = now.UTC().Truncate(24 * time.Hour)

	case lastWeek:
		now := time.Now()
		startDate = now.AddDate(0, 0, -8).UTC().Truncate(24 * time.Hour)
		endDate = now.UTC().Truncate(24 * time.Hour)

	case lastThirtyDays:
		now := time.Now()
		startDate = now.AddDate(0, 0, -31).UTC().Truncate(24 * time.Hour)
		endDate = now.UTC().Truncate(24 * time.Hour)
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
