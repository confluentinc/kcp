package self_managed_connectors

import (
	"fmt"
	"os"
	"strings"

	"github.com/confluentinc/kcp/internal/types"
	"github.com/confluentinc/kcp/internal/utils"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

var (
	stateFile      string
	connectRestURL string
	clusterID      string

	useSaslScram       bool
	useTls             bool
	useUnauthenticated bool

	saslScramUsername string
	saslScramPassword string

	tlsCaCert     string
	tlsClientCert string
	tlsClientKey  string
)

func NewScanSelfManagedConnectorsCmd() *cobra.Command {
	selfManagedConnectorsCmd := &cobra.Command{
		Use:   "self-managed-connectors",
		Short: "Scan self-managed Kafka Connect cluster for connector information",
		Long:  "Scan a self-managed Kafka Connect cluster using its REST API to discover connector configurations and status",
		Example: `  # Scan connectors for an MSK cluster (cluster-id is an ARN)
  kcp scan self-managed-connectors \
    --state-file kcp-state.json \
    --connect-rest-url http://connect:8083 \
    --cluster-id arn:aws:kafka:us-east-1:123456789012:cluster/my-cluster/abc-123 \
    --use-unauthenticated

  # Scan connectors for an OSK cluster with SASL/SCRAM auth (cluster-id is a simple identifier)
  kcp scan self-managed-connectors \
    --state-file kcp-state.json \
    --connect-rest-url https://connect.example.com:8083 \
    --cluster-id production-kafka \
    --use-sasl-scram \
    --sasl-scram-username admin \
    --sasl-scram-password secret`,
		SilenceErrors: true,
		PreRunE:       preRunScanSelfManagedConnectors,
		RunE:          runScanSelfManagedConnectors,
		Hidden:        true,
	}

	groups := map[*pflag.FlagSet]string{}

	requiredFlags := pflag.NewFlagSet("required", pflag.ExitOnError)
	requiredFlags.SortFlags = false
	requiredFlags.StringVar(&stateFile, "state-file", "", "The path to the kcp state file to update with connector information.")
	requiredFlags.StringVar(&connectRestURL, "connect-rest-url", "", "The Kafka Connect REST API URL (e.g., http://localhost:8083).")
	requiredFlags.StringVar(&clusterID, "cluster-id", "", "The cluster identifier in the state file. Accepts both MSK ARNs (arn:aws:kafka:...) and OSK cluster IDs.")
	selfManagedConnectorsCmd.Flags().AddFlagSet(requiredFlags)
	groups[requiredFlags] = "Required Flags"

	authMethodFlags := pflag.NewFlagSet("auth-method", pflag.ExitOnError)
	authMethodFlags.SortFlags = false
	authMethodFlags.BoolVar(&useSaslScram, "use-sasl-scram", false, "Use SASL/SCRAM authentication (requires --sasl-scram-username and --sasl-scram-password).")
	authMethodFlags.BoolVar(&useTls, "use-tls", false, "Use TLS certificate authentication (requires --tls-ca-cert, --tls-client-cert, and --tls-client-key).")
	authMethodFlags.BoolVar(&useUnauthenticated, "use-unauthenticated", false, "Use no authentication.")
	selfManagedConnectorsCmd.Flags().AddFlagSet(authMethodFlags)
	groups[authMethodFlags] = "Authentication Method (choose one)"

	saslScramFlags := pflag.NewFlagSet("sasl-scram", pflag.ExitOnError)
	saslScramFlags.SortFlags = false
	saslScramFlags.StringVar(&saslScramUsername, "sasl-scram-username", "", "SASL/SCRAM username (required when using --use-sasl-scram).")
	saslScramFlags.StringVar(&saslScramPassword, "sasl-scram-password", "", "SASL/SCRAM password (required when using --use-sasl-scram).")
	selfManagedConnectorsCmd.Flags().AddFlagSet(saslScramFlags)
	groups[saslScramFlags] = "SASL/SCRAM Credentials"

	tlsFlags := pflag.NewFlagSet("tls", pflag.ExitOnError)
	tlsFlags.SortFlags = false
	tlsFlags.StringVar(&tlsCaCert, "tls-ca-cert", "", "Path to CA certificate file (required when using --use-tls).")
	tlsFlags.StringVar(&tlsClientCert, "tls-client-cert", "", "Path to client certificate file (required when using --use-tls).")
	tlsFlags.StringVar(&tlsClientKey, "tls-client-key", "", "Path to client key file (required when using --use-tls).")
	selfManagedConnectorsCmd.Flags().AddFlagSet(tlsFlags)
	groups[tlsFlags] = "TLS Credentials"

	selfManagedConnectorsCmd.SetUsageFunc(func(c *cobra.Command) error {
		fmt.Printf("%s\n\n", c.Short)

		if c.Example != "" {
			fmt.Printf("Examples:\n%s\n\n", c.Example)
		}

		flagOrder := []*pflag.FlagSet{requiredFlags, authMethodFlags, saslScramFlags, tlsFlags}
		groupNames := []string{"Required Flags", "Authentication Method (choose one)", "SASL/SCRAM Credentials", "TLS Credentials"}

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

	return nil
}

func runScanSelfManagedConnectors(cmd *cobra.Command, args []string) error {
	opts, err := parseScanSelfManagedConnectorsOpts()
	if err != nil {
		return fmt.Errorf("failed to parse scan self-managed connectors opts: %v", err)
	}

	scanner := NewSelfManagedConnectorsScanner(*opts)
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

	// Auto-detect source type from cluster ID format
	var sourceType string
	var clusterArn string
	var oskClusterID string

	if strings.HasPrefix(clusterID, "arn:") {
		sourceType = "msk"
		clusterArn = clusterID
	} else {
		sourceType = "osk"
		oskClusterID = clusterID
	}

	// Validate cluster exists in state based on detected source type
	switch sourceType {
	case "msk":
		_, err = state.GetClusterByArn(clusterArn)
		if err != nil {
			return nil, fmt.Errorf("cluster not found in state file: %v", err)
		}
	case "osk":
		_, err = state.GetOSKClusterByID(oskClusterID)
		if err != nil {
			return nil, fmt.Errorf("cluster not found in state file: %v", err)
		}
	}

	opts := SelfManagedConnectorsScannerOpts{
		StateFile:      stateFile,
		State:          state,
		ConnectRestURL: normalizedURL,
		SourceType:     sourceType,
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
