package self_managed_connectors

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/confluentinc/kcp/internal/types"
	"github.com/confluentinc/kcp/internal/utils"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

var (
	stateFile      string
	connectRestURL string
	clusterID      string
	sourceType     string

	useSaslScram       bool
	useTls             bool
	useUnauthenticated bool

	saslScramUsername string
	saslScramPassword string

	tlsCaCert     string
	tlsClientCert string
	tlsClientKey  string

	metricsSource   string
	metricsDuration string
	metricsInterval string
	metricsRange    string
	credentialsFile string
)

func NewScanSelfManagedConnectorsCmd() *cobra.Command {
	selfManagedConnectorsCmd := &cobra.Command{
		Use:   "self-managed-connectors",
		Short: "Scan self-managed Kafka Connect cluster for connector information",
		Long:  "Scan a self-managed Kafka Connect cluster using its REST API to discover connector configurations and status. Sensitive config values are redacted before being written to the state file.",
		Example: `  # Scan connectors for an MSK cluster (auto-detected from ARN format)
  kcp scan self-managed-connectors \
    --state-file kcp-state.json \
    --connect-rest-url http://connect:8083 \
    --cluster-id arn:aws:kafka:us-east-1:123456789012:cluster/my-cluster/abc-123 \
    --use-unauthenticated

  # Scan connectors for an OSK cluster (auto-detected from non-ARN format)
  kcp scan self-managed-connectors \
    --state-file kcp-state.json \
    --connect-rest-url https://connect.example.com:8083 \
    --cluster-id production-kafka \
    --use-sasl-scram \
    --sasl-scram-username admin \
    --sasl-scram-password secret

  # Explicitly specify source type (overrides auto-detection)
  kcp scan self-managed-connectors \
    --state-file kcp-state.json \
    --connect-rest-url http://connect:8083 \
    --cluster-id my-cluster \
    --source-type osk \
    --use-unauthenticated

  # Scan with Jolokia metrics collection
  kcp scan self-managed-connectors \
    --state-file kcp-state.json \
    --connect-rest-url http://connect:8083 \
    --cluster-id my-cluster \
    --use-unauthenticated \
    --metrics jolokia --metrics-duration 5m --metrics-interval 10s \
    --credentials-file osk-credentials.yaml`,
		SilenceErrors: true,
		PreRunE:       preRunScanSelfManagedConnectors,
		RunE:          runScanSelfManagedConnectors,
		Hidden:        false,
	}

	requiredFlags := pflag.NewFlagSet("required", pflag.ExitOnError)
	requiredFlags.SortFlags = false
	requiredFlags.StringVar(&stateFile, "state-file", "", "The path to the kcp state file to update with connector information.")
	requiredFlags.StringVar(&connectRestURL, "connect-rest-url", "", "The Kafka Connect REST API URL (e.g., http://localhost:8083).")
	requiredFlags.StringVar(&clusterID, "cluster-id", "", "The cluster identifier in the state file. Accepts both MSK ARNs (arn:aws:kafka:...) and OSK cluster IDs.")
	selfManagedConnectorsCmd.Flags().AddFlagSet(requiredFlags)

	optionalFlags := pflag.NewFlagSet("optional", pflag.ExitOnError)
	optionalFlags.SortFlags = false
	optionalFlags.StringVar(&sourceType, "source-type", "", "Source type: 'msk' or 'osk'. If not specified, auto-detects from cluster-id format (ARN = MSK, non-ARN = OSK).")
	selfManagedConnectorsCmd.Flags().AddFlagSet(optionalFlags)

	authMethodFlags := pflag.NewFlagSet("auth-method", pflag.ExitOnError)
	authMethodFlags.SortFlags = false
	authMethodFlags.BoolVar(&useSaslScram, "use-sasl-scram", false, "Use SASL/SCRAM authentication (requires --sasl-scram-username and --sasl-scram-password).")
	authMethodFlags.BoolVar(&useTls, "use-tls", false, "Use TLS certificate authentication (requires --tls-ca-cert, --tls-client-cert, and --tls-client-key).")
	authMethodFlags.BoolVar(&useUnauthenticated, "use-unauthenticated", false, "Use no authentication.")
	selfManagedConnectorsCmd.Flags().AddFlagSet(authMethodFlags)

	saslScramFlags := pflag.NewFlagSet("sasl-scram", pflag.ExitOnError)
	saslScramFlags.SortFlags = false
	saslScramFlags.StringVar(&saslScramUsername, "sasl-scram-username", "", "SASL/SCRAM username (required when using --use-sasl-scram).")
	saslScramFlags.StringVar(&saslScramPassword, "sasl-scram-password", "", "SASL/SCRAM password (required when using --use-sasl-scram).")
	selfManagedConnectorsCmd.Flags().AddFlagSet(saslScramFlags)

	tlsFlags := pflag.NewFlagSet("tls", pflag.ExitOnError)
	tlsFlags.SortFlags = false
	tlsFlags.StringVar(&tlsCaCert, "tls-ca-cert", "", "Path to CA certificate file (required when using --use-tls).")
	tlsFlags.StringVar(&tlsClientCert, "tls-client-cert", "", "Path to client certificate file (required when using --use-tls).")
	tlsFlags.StringVar(&tlsClientKey, "tls-client-key", "", "Path to client key file (required when using --use-tls).")
	selfManagedConnectorsCmd.Flags().AddFlagSet(tlsFlags)

	metricsFlags := pflag.NewFlagSet("metrics", pflag.ExitOnError)
	metricsFlags.SortFlags = false
	metricsFlags.StringVar(&metricsSource, "metrics", "", "Collect Connect worker metrics: 'jolokia' or 'prometheus'. Requires --credentials-file. Endpoints/filters must target the Connect workers, not the Kafka brokers.")
	metricsFlags.StringVar(&metricsDuration, "metrics-duration", "", "Duration to poll Jolokia metrics (e.g. 5m, 1h). Required with --metrics jolokia.")
	metricsFlags.StringVar(&metricsInterval, "metrics-interval", "10s", "Polling interval for Jolokia metrics (e.g. 10s, 30s). Default: 10s.")
	metricsFlags.StringVar(&metricsRange, "metrics-range", "", "Day range to query from Prometheus (e.g. 7d, 30d). Required with --metrics prometheus.")
	metricsFlags.StringVar(&credentialsFile, "credentials-file", "", "Path to the apache-kafka-credentials.yaml file providing Jolokia/Prometheus configuration. Required with --metrics.")
	selfManagedConnectorsCmd.Flags().AddFlagSet(metricsFlags)

	selfManagedConnectorsCmd.SetUsageFunc(func(c *cobra.Command) error {
		fmt.Printf("%s\n\n", c.Short)

		if c.Example != "" {
			fmt.Printf("Examples:\n%s\n\n", c.Example)
		}

		flagOrder := []*pflag.FlagSet{requiredFlags, optionalFlags, authMethodFlags, saslScramFlags, tlsFlags, metricsFlags}
		groupNames := []string{"Required Flags", "Optional Flags", "Authentication Method (choose one)", "SASL/SCRAM Credentials", "TLS Credentials", "Metrics Collection"}

		for i, fs := range flagOrder {
			usage := fs.FlagUsages()
			if usage != "" {
				fmt.Printf("%s:\n%s\n", groupNames[i], usage)
			}
		}

		fmt.Println("All flags can be provided via environment variables (uppercase, with underscores).")

		return nil
	})

	_ = selfManagedConnectorsCmd.MarkFlagRequired("state-file")
	_ = selfManagedConnectorsCmd.MarkFlagRequired("connect-rest-url")
	_ = selfManagedConnectorsCmd.MarkFlagRequired("cluster-id")

	selfManagedConnectorsCmd.MarkFlagsMutuallyExclusive("use-sasl-scram", "use-tls", "use-unauthenticated")
	selfManagedConnectorsCmd.MarkFlagsOneRequired("use-sasl-scram", "use-tls", "use-unauthenticated")
	selfManagedConnectorsCmd.MarkFlagsMutuallyExclusive("metrics-duration", "metrics-range")

	return selfManagedConnectorsCmd
}

func preRunScanSelfManagedConnectors(cmd *cobra.Command, args []string) error {
	if err := utils.BindEnvToFlags(cmd); err != nil {
		return err
	}

	if useSaslScram {
		_ = cmd.MarkFlagRequired("sasl-scram-username")
		_ = cmd.MarkFlagRequired("sasl-scram-password")
	}

	if useTls {
		_ = cmd.MarkFlagRequired("tls-ca-cert")
		_ = cmd.MarkFlagRequired("tls-client-cert")
		_ = cmd.MarkFlagRequired("tls-client-key")
	}

	// Validate metrics flags. Mirrors the cluster-scan validation style
	// (cmd/scan/clusters): mutual exclusion via Flags().Changed, fail fast
	// before any collection. Error values carry no credential material.
	if metricsSource != "" {
		switch metricsSource {
		case "jolokia":
			if metricsDuration == "" {
				return fmt.Errorf("--metrics-duration is required when --metrics jolokia is set")
			}
			if _, err := time.ParseDuration(metricsDuration); err != nil {
				return fmt.Errorf("invalid --metrics-duration '%s': %w", metricsDuration, err)
			}
			if _, err := time.ParseDuration(metricsInterval); err != nil {
				return fmt.Errorf("invalid --metrics-interval '%s': %w", metricsInterval, err)
			}
			duration, _ := time.ParseDuration(metricsDuration)
			interval, _ := time.ParseDuration(metricsInterval)
			if duration <= interval {
				return fmt.Errorf("--metrics-duration (%s) must be greater than --metrics-interval (%s) to collect at least one data point", metricsDuration, metricsInterval)
			}
			if cmd.Flags().Changed("metrics-range") {
				return fmt.Errorf("--metrics-range cannot be used with --metrics jolokia")
			}
		case "prometheus":
			if metricsRange == "" {
				return fmt.Errorf("--metrics-range is required when --metrics prometheus is set")
			}
			if _, err := utils.ParseDurationDays(metricsRange); err != nil {
				return fmt.Errorf("invalid --metrics-range '%s': must be like 1d, 7d, 30d", metricsRange)
			}
			if cmd.Flags().Changed("metrics-duration") {
				return fmt.Errorf("--metrics-duration cannot be used with --metrics prometheus")
			}
			if cmd.Flags().Changed("metrics-interval") {
				return fmt.Errorf("--metrics-interval cannot be used with --metrics prometheus")
			}
		default:
			return fmt.Errorf("invalid --metrics '%s': must be 'jolokia' or 'prometheus'", metricsSource)
		}
		if credentialsFile == "" {
			return fmt.Errorf("--credentials-file is required when --metrics is set")
		}
	}

	return nil
}

func runScanSelfManagedConnectors(cmd *cobra.Command, args []string) error {
	opts, err := parseScanSelfManagedConnectorsOpts()
	if err != nil {
		return fmt.Errorf("failed to parse scan self-managed connectors opts: %v", err)
	}

	scanner, err := NewSelfManagedConnectorsScanner(*opts)
	if err != nil {
		return fmt.Errorf("failed to create self-managed connectors scanner: %v", err)
	}
	if err := scanner.Run(); err != nil {
		return fmt.Errorf("failed to scan self-managed connectors: %v", err)
	}

	return nil
}

func parseScanSelfManagedConnectorsOpts() (*SelfManagedConnectorsScannerOpts, error) {
	if _, err := os.Stat(stateFile); os.IsNotExist(err) {
		return nil, fmt.Errorf("state file does not exist: %s", stateFile)
	}

	state, err := types.NewStateFromFile(stateFile)
	if err != nil {
		return nil, fmt.Errorf("failed to load existing state file: %v", err)
	}

	normalizedURL := normaliseConnectURL(connectRestURL)

	var authMethod types.ConnectAuthMethod
	switch {
	case useSaslScram:
		authMethod = types.ConnectAuthMethodSaslScram
	case useTls:
		authMethod = types.ConnectAuthMethodTls
	default:
		authMethod = types.ConnectAuthMethodUnauthenticated
	}

	// Determine source type: use explicit flag if provided, otherwise auto-detect from cluster ID format
	var detectedSourceType types.SourceType
	var clusterArn string
	var oskClusterID string

	if sourceType != "" {
		// Validate explicit source type
		if sourceType != "msk" && sourceType != "osk" {
			return nil, fmt.Errorf("invalid source-type: %s (must be 'msk' or 'osk')", sourceType)
		}
		detectedSourceType = types.SourceType(sourceType)
	} else {
		// Auto-detect from cluster ID format
		if strings.HasPrefix(clusterID, "arn:") {
			detectedSourceType = types.SourceTypeMSK
		} else {
			detectedSourceType = types.SourceTypeOSK
		}
	}

	// Set cluster identifiers based on source type
	if detectedSourceType == types.SourceTypeMSK {
		clusterArn = clusterID
	} else {
		oskClusterID = clusterID
	}

	// Validate cluster exists in state based on detected source type
	switch detectedSourceType {
	case types.SourceTypeMSK:
		_, err = state.GetClusterByArn(clusterArn)
		if err != nil {
			return nil, fmt.Errorf("cluster not found in state file: %v", err)
		}
	case types.SourceTypeOSK:
		_, err = state.GetOSKClusterByID(oskClusterID)
		if err != nil {
			return nil, fmt.Errorf("cluster not found in state file: %v", err)
		}
	}

	// Resolve metrics cluster credentials only when metrics collection is
	// requested. Credentials are read from the file here and never persisted to
	// state or logged. The lookup is routed by source type (MSK ARN or OSK id),
	// and the error path carries the cluster identifier and file path only — no
	// credential material (R11).
	var metricsClusterCreds *types.OSKClusterAuth
	if metricsSource != "" {
		creds, errs := types.NewOSKCredentialsFromFile(credentialsFile)
		if len(errs) > 0 {
			return nil, fmt.Errorf("failed to load credentials file %s: %v", credentialsFile, errs)
		}
		lookupID := oskClusterID
		if detectedSourceType == types.SourceTypeMSK {
			lookupID = clusterArn
		}
		for i := range creds.Clusters {
			if creds.Clusters[i].ID == lookupID {
				metricsClusterCreds = &creds.Clusters[i]
				break
			}
		}
		if metricsClusterCreds == nil {
			return nil, fmt.Errorf("no matching cluster entry for %q in credentials file %s; metrics collection requires a matching cluster entry", lookupID, credentialsFile)
		}
	}

	opts := SelfManagedConnectorsScannerOpts{
		StateFile:      stateFile,
		State:          state,
		ConnectRestURL: normalizedURL,
		SourceType:     detectedSourceType,
		ClusterArn:     clusterArn,
		ClusterID:      oskClusterID,
		AuthMethod:     authMethod,
		SaslScramAuth: types.ConnectSaslScramAuth{
			Username: saslScramUsername,
			Password: saslScramPassword,
		},
		TlsAuth: types.ConnectTlsAuth{
			CACert:     tlsCaCert,
			ClientCert: tlsClientCert,
			ClientKey:  tlsClientKey,
		},
		MetricsSource:       metricsSource,
		MetricsClusterCreds: metricsClusterCreds,
		MetricsDuration:     metricsDuration,
		MetricsInterval:     metricsInterval,
		MetricsRange:        metricsRange,
	}

	return &opts, nil
}

func normaliseConnectURL(url string) string {
	// If the URL already has http:// or https://, return as-is
	if strings.HasPrefix(url, "http://") || strings.HasPrefix(url, "https://") {
		return url
	}

	normalisedUrl := "http://" + url
	fmt.Printf("ℹ️  Adding protocol scheme 'http://' to provided Connect URL: %s\n", normalisedUrl)

	return normalisedUrl
}
