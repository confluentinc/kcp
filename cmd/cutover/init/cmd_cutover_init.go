package init

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/confluentinc/kcp/internal/services/cutover"
	"github.com/confluentinc/kcp/internal/utils"
	"github.com/google/uuid"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

var (
	cutoverStateFile        string
	skipValidate            bool
	pauseConsumerOffsetSync bool

	k8sNamespace   string
	initialCrName  string
	kubeConfigPath string

	sourceBootstrap     string
	clusterBootstrap    string
	clusterId           string
	clusterRestEndpoint string
	clusterLinkName     string
	clusterApiKey       string
	clusterApiSecret    string
	topics              []string

	fencedCrYamlPath      string
	switchoverCrYamlPath  string
	insecureSkipTLSVerify bool
	clusterRestCaCert     string

	useSaslIam                  bool
	useSaslScram                bool
	useSaslPlain                bool
	useTls                      bool
	useUnauthenticatedTLS       bool
	useUnauthenticatedPlaintext bool

	saslScramUsername string
	saslScramPassword string

	saslPlainUsername string
	saslPlainPassword string

	tlsCaCert     string
	tlsClientCert string
	tlsClientKey  string
)

func NewCutoverInitCmd() *cobra.Command {
	cutoverInitCmd := &cobra.Command{
		Use:   "init",
		Short: "Initialize a new cutover",
		Long: `Initialize a new cutover by validating infrastructure and persisting cutover state.

This command validates the cluster link and mirror topics on the destination cluster,
fetches the current gateway CR from Kubernetes, validates consistency across the initial,
fenced, and switchover gateway CRs, and writes the cutover configuration to the state file.

The state file can then be used by 'kcp cutover execute' to run the cutover.

All flags can be provided via environment variables using uppercase names with underscores
(e.g. ` + "`--cluster-api-key`" + ` → ` + "`CLUSTER_API_KEY`" + `, ` + "`--source-bootstrap`" + ` → ` + "`SOURCE_BOOTSTRAP`" + `).`,
		Example: `  # MSK source with IAM auth
  kcp cutover init \
      --k8s-namespace my-namespace \
      --initial-cr-name my-gateway \
      --source-bootstrap b1.my-cluster.kafka.us-east-1.amazonaws.com:9098 \
      --cluster-bootstrap pkc-abc123.us-east-1.aws.confluent.cloud:9092 \
      --cluster-id lkc-abc123 \
      --cluster-rest-endpoint https://lkc-abc123.us-east-1.aws.confluent.cloud:443 \
      --cluster-link-name my-cluster-link \
      --cluster-api-key ABCDEFGHIJKLMNOP \
      --cluster-api-secret xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx \
      --fenced-cr-yaml gateway-fenced.yaml \
      --switchover-cr-yaml gateway-switchover.yaml \
      --use-sasl-iam

  # SASL/SCRAM source
  kcp cutover init \
      --k8s-namespace my-namespace --initial-cr-name my-gateway \
      --source-bootstrap broker1:9096 --cluster-bootstrap pkc-abc123.us-east-1.aws.confluent.cloud:9092 \
      --cluster-id lkc-abc123 --cluster-rest-endpoint https://lkc-abc123.us-east-1.aws.confluent.cloud:443 \
      --cluster-link-name my-cluster-link \
      --cluster-api-key ABCDEFGHIJKLMNOP --cluster-api-secret xxxx \
      --fenced-cr-yaml gateway-fenced.yaml --switchover-cr-yaml gateway-switchover.yaml \
      --use-sasl-scram --sasl-scram-username kafkauser --sasl-scram-password kafkapass`,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		PreRunE:       preRunCutoverInit,
		RunE:          runCutoverInit,
	}

	groups := map[*pflag.FlagSet]string{}

	requiredFlags := pflag.NewFlagSet("required", pflag.ExitOnError)
	requiredFlags.SortFlags = false
	requiredFlags.StringVar(&k8sNamespace, "k8s-namespace", "", "Kubernetes namespace where the gateway is deployed.")
	requiredFlags.StringVar(&initialCrName, "initial-cr-name", "", "Name of the initial gateway custom resource in Kubernetes.")
	requiredFlags.StringVar(&sourceBootstrap, "source-bootstrap", "", "Bootstrap server(s) of the source Kafka cluster (e.g. broker1:9092,broker2:9092).")
	requiredFlags.StringVar(&clusterBootstrap, "cluster-bootstrap", "", "Confluent Cloud Kafka bootstrap endpoint (e.g. pkc-abc123.us-east-1.aws.confluent.cloud:9092).")
	requiredFlags.StringVar(&clusterId, "cluster-id", "", "Confluent Cloud destination cluster ID (e.g. lkc-abc123).")
	requiredFlags.StringVar(&clusterRestEndpoint, "cluster-rest-endpoint", "", "REST endpoint of the destination Confluent Cloud cluster.")
	requiredFlags.StringVar(&clusterLinkName, "cluster-link-name", "", "Name of the cluster link on the destination cluster.")
	requiredFlags.StringVar(&clusterApiKey, "cluster-api-key", "", "API key for authenticating with the destination cluster.")
	requiredFlags.StringVar(&clusterApiSecret, "cluster-api-secret", "", "API secret for authenticating with the destination cluster.")
	requiredFlags.StringVar(&fencedCrYamlPath, "fenced-cr-yaml", "", "Path to the gateway CR YAML that blocks traffic during cutover.")
	requiredFlags.StringVar(&switchoverCrYamlPath, "switchover-cr-yaml", "", "Path to the gateway CR YAML that routes traffic to Confluent Cloud.")

	cutoverInitCmd.Flags().AddFlagSet(requiredFlags)
	groups[requiredFlags] = "Required Flags"

	optionalFlags := pflag.NewFlagSet("optional", pflag.ExitOnError)
	optionalFlags.SortFlags = false
	optionalFlags.StringVar(&cutoverStateFile, "cutover-state-file", "cutover-state.json", "The path to the cutover state file. If it doesn't exist, it will be created. If it exists, the new cutover will be appended.")
	optionalFlags.BoolVar(&skipValidate, "skip-validate", false, "Skip infrastructure validation. Creates cutover metadata without validating gateway/Kubernetes resources. Useful for testing.")
	optionalFlags.BoolVar(&pauseConsumerOffsetSync, "pause-consumer-offset-sync", false, "Disable the cluster link's consumer.offset.sync.enable during execute and restore it after switchover. Requires the cluster link to currently have consumer.offset.sync.enable=true.")
	optionalFlags.StringVar(&kubeConfigPath, "kube-path", "", "The path to the Kubernetes config file to use for the cutover.")
	optionalFlags.StringSliceVar(&topics, "topics", []string{}, "The topics to cut over (comma separated list or repeated flag).")
	optionalFlags.BoolVar(&insecureSkipTLSVerify, "insecure-skip-tls-verify", false, "Skip TLS certificate verification for REST endpoint and Kafka connections.")
	optionalFlags.StringVar(&clusterRestCaCert, "cluster-rest-ca-cert", "", "Path to a CA certificate that verifies the destination cluster REST endpoint's TLS certificate. Use when the REST endpoint is HTTPS behind a private/internal CA; omit for Confluent Cloud (public CA).")
	cutoverInitCmd.Flags().AddFlagSet(optionalFlags)
	groups[optionalFlags] = "Optional Flags"

	// Authentication flags. These are validated at init time so the user declares their source auth
	// strategy up front (fail-fast), but credentials are not passed to the initializer — source cluster
	// connections only happen during 'cutover execute'.
	authFlags := pflag.NewFlagSet("auth", pflag.ExitOnError)
	authFlags.SortFlags = false
	authFlags.BoolVar(&useSaslIam, "use-sasl-iam", false, "Use IAM authentication for the source MSK cluster.")
	authFlags.BoolVar(&useSaslScram, "use-sasl-scram", false, "Use SASL/SCRAM authentication for the source MSK cluster.")
	authFlags.BoolVar(&useSaslPlain, "use-sasl-plain", false, "Use SASL/PLAIN authentication for the source cluster.")
	authFlags.BoolVar(&useTls, "use-tls", false, "Use TLS authentication for the source MSK cluster.")
	authFlags.BoolVar(&useUnauthenticatedTLS, "use-unauthenticated-tls", false, "Use unauthenticated (TLS encryption) for the source MSK cluster.")
	authFlags.BoolVar(&useUnauthenticatedPlaintext, "use-unauthenticated-plaintext", false, "Use unauthenticated (plaintext) for the source MSK cluster.")
	cutoverInitCmd.Flags().AddFlagSet(authFlags)
	groups[authFlags] = "Source Cluster Authentication Flags"

	// SASL/SCRAM credential flags.
	saslScramFlags := pflag.NewFlagSet("sasl-scram", pflag.ExitOnError)
	saslScramFlags.SortFlags = false
	saslScramFlags.StringVar(&saslScramUsername, "sasl-scram-username", "", "SASL/SCRAM username for the source MSK cluster.")
	saslScramFlags.StringVar(&saslScramPassword, "sasl-scram-password", "", "SASL/SCRAM password for the source MSK cluster.")
	cutoverInitCmd.Flags().AddFlagSet(saslScramFlags)
	groups[saslScramFlags] = "SASL/SCRAM Flags"

	// SASL/PLAIN credential flags.
	saslPlainFlags := pflag.NewFlagSet("sasl-plain", pflag.ExitOnError)
	saslPlainFlags.SortFlags = false
	saslPlainFlags.StringVar(&saslPlainUsername, "sasl-plain-username", "", "SASL/PLAIN username for the source cluster.")
	saslPlainFlags.StringVar(&saslPlainPassword, "sasl-plain-password", "", "SASL/PLAIN password for the source cluster.")
	cutoverInitCmd.Flags().AddFlagSet(saslPlainFlags)
	groups[saslPlainFlags] = "SASL/PLAIN Flags"

	// TLS credential flags.
	tlsFlags := pflag.NewFlagSet("tls", pflag.ExitOnError)
	tlsFlags.SortFlags = false
	tlsFlags.StringVar(&tlsCaCert, "tls-ca-cert", "", "Path to a CA certificate that verifies the source broker's TLS server certificate. Applies to any TLS-fronted source auth method (SASL/SCRAM, SASL/PLAIN over TLS, TLS/mTLS, unauthenticated-TLS); supply it only for a private/internal CA.")
	tlsFlags.StringVar(&tlsClientCert, "tls-client-cert", "", "Path to the TLS client certificate for the source MSK cluster.")
	tlsFlags.StringVar(&tlsClientKey, "tls-client-key", "", "Path to the TLS client key for the source MSK cluster.")
	cutoverInitCmd.Flags().AddFlagSet(tlsFlags)
	groups[tlsFlags] = "TLS Flags"

	cutoverInitCmd.SetUsageFunc(func(c *cobra.Command) error {
		fmt.Printf("%s\n\n", c.Short)

		flagOrder := []*pflag.FlagSet{requiredFlags, optionalFlags, authFlags, saslScramFlags, saslPlainFlags, tlsFlags}
		groupNames := []string{"Required Flags", "Optional Flags", "Source Cluster Authentication Flags", "SASL/SCRAM Flags", "SASL/PLAIN Flags", "TLS Flags"}

		for i, fs := range flagOrder {
			usage := fs.FlagUsages()
			if usage != "" {
				fmt.Printf("%s:\n%s\n", groupNames[i], usage)
			}
		}

		fmt.Println("All flags can be provided via environment variables (uppercase, with underscores).")

		return nil
	})

	_ = cutoverInitCmd.MarkFlagRequired("source-bootstrap")
	_ = cutoverInitCmd.MarkFlagRequired("cluster-bootstrap")
	_ = cutoverInitCmd.MarkFlagRequired("k8s-namespace")
	_ = cutoverInitCmd.MarkFlagRequired("initial-cr-name")
	_ = cutoverInitCmd.MarkFlagRequired("cluster-id")
	_ = cutoverInitCmd.MarkFlagRequired("cluster-rest-endpoint")
	_ = cutoverInitCmd.MarkFlagRequired("cluster-link-name")
	_ = cutoverInitCmd.MarkFlagRequired("cluster-api-key")
	_ = cutoverInitCmd.MarkFlagRequired("cluster-api-secret")
	_ = cutoverInitCmd.MarkFlagRequired("fenced-cr-yaml")
	_ = cutoverInitCmd.MarkFlagRequired("switchover-cr-yaml")

	cutoverInitCmd.MarkFlagsMutuallyExclusive("use-sasl-iam", "use-sasl-scram", "use-sasl-plain", "use-tls", "use-unauthenticated-tls", "use-unauthenticated-plaintext")
	cutoverInitCmd.MarkFlagsOneRequired("use-sasl-iam", "use-sasl-scram", "use-sasl-plain", "use-tls", "use-unauthenticated-tls", "use-unauthenticated-plaintext")

	// --pause-consumer-offset-sync requires the init-time snapshot captured by
	// the validation path, so it cannot be combined with --skip-validate.
	// Without the snapshot, the restore bookend has nothing to diff against
	// and would silently leave the cluster link disabled after switchover.
	cutoverInitCmd.MarkFlagsMutuallyExclusive("skip-validate", "pause-consumer-offset-sync")

	// If any credential in a pair is set, the whole pair must be set.
	cutoverInitCmd.MarkFlagsRequiredTogether("sasl-scram-username", "sasl-scram-password")
	cutoverInitCmd.MarkFlagsRequiredTogether("sasl-plain-username", "sasl-plain-password")
	// The mTLS client identity is a pair; --tls-ca-cert is deliberately NOT
	// grouped in, so it can be supplied on its own to trust a private CA on the
	// SASL/SCRAM, SASL/PLAIN-over-TLS, and unauthenticated-TLS source paths.
	cutoverInitCmd.MarkFlagsRequiredTogether("tls-client-cert", "tls-client-key")

	return cutoverInitCmd
}

func preRunCutoverInit(cmd *cobra.Command, args []string) error {
	if err := utils.BindEnvToFlags(cmd); err != nil {
		return err
	}

	if useSaslScram {
		_ = cmd.MarkFlagRequired("sasl-scram-username")
		_ = cmd.MarkFlagRequired("sasl-scram-password")
	}

	if useSaslPlain {
		_ = cmd.MarkFlagRequired("sasl-plain-username")
		_ = cmd.MarkFlagRequired("sasl-plain-password")
	}

	if useTls {
		// --tls-ca-cert is NOT required: mTLS against a public/system-trusted CA
		// works with system roots. It stays optional, for a private/internal CA.
		_ = cmd.MarkFlagRequired("tls-client-cert")
		_ = cmd.MarkFlagRequired("tls-client-key")
	}

	return nil
}

func runCutoverInit(cmd *cobra.Command, args []string) error {
	// ===== PHASE 1: Load or create state =====
	var cutoverState *cutover.CutoverState
	if _, err := os.Stat(cutoverStateFile); err == nil {
		// File exists, load it
		cutoverState, err = cutover.NewCutoverStateFromFile(cutoverStateFile)
		if err != nil {
			return fmt.Errorf("failed to load cutover state: %w", err)
		}
	} else {
		// File doesn't exist, create new state
		cutoverState = cutover.NewCutoverState()
	}

	// ===== PHASE 2: Read YAML files =====
	fencedCrYAML, err := os.ReadFile(fencedCrYamlPath)
	if err != nil {
		return fmt.Errorf("failed to read fenced CR YAML file: %w", err)
	}

	switchoverCrYAML, err := os.ReadFile(switchoverCrYamlPath)
	if err != nil {
		return fmt.Errorf("failed to read switchover CR YAML file: %w", err)
	}

	// Parse kube config path with default
	kubeConfigPathResolved := kubeConfigPath
	if kubeConfigPathResolved == "" {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("failed to get user home directory: %v", err)
		}
		kubeConfigPathResolved = filepath.Join(homeDir, ".kube", "config")
	}
	slog.Debug("using kube config path", "path", kubeConfigPathResolved)

	config := &cutover.CutoverConfig{
		CutoverId:               fmt.Sprintf("cutover-%s", uuid.New().String()),
		SourceBootstrap:         sourceBootstrap,
		ClusterBootstrap:        clusterBootstrap,
		K8sNamespace:            k8sNamespace,
		InitialCrName:           initialCrName,
		KubeConfigPath:          kubeConfigPathResolved,
		ClusterId:               clusterId,
		ClusterRestEndpoint:     clusterRestEndpoint,
		ClusterLinkName:         clusterLinkName,
		Topics:                  topics,
		FencedCrYAML:            fencedCrYAML,
		SwitchoverCrYAML:        switchoverCrYAML,
		CurrentState:            cutover.StateUninitialized,
		PauseConsumerOffsetSync: pauseConsumerOffsetSync,
	}

	// ===== PHASE 3: Early write - upsert cutover and write to file =====
	// CRITICAL: File MUST exist before orchestrator runs to prevent panic
	cutoverState.UpsertCutover(*config)
	if err := cutoverState.WriteToFile(cutoverStateFile); err != nil {
		return fmt.Errorf("failed to write cutover state file: %w", err)
	}

	// ===== PHASE 4: Handle skip-validate flag (exit early if set) =====
	if skipValidate {
		fmt.Printf("✅ Cutover created (validation skipped): %s\n", config.CutoverId)
		return nil
	}

	// ===== PHASE 5: Pass to initializer for validation orchestration only =====
	opts := parseCutoverInitializerOpts(*cutoverState, *config)
	cutoverInitializer := NewCutoverInitializer(opts)
	if err := cutoverInitializer.Run(); err != nil {
		return err
	}

	fmt.Printf("✅ Cutover initialized: %s\n", config.CutoverId)
	return nil
}

func parseCutoverInitializerOpts(state cutover.CutoverState, config cutover.CutoverConfig) CutoverInitializerOpts {
	return CutoverInitializerOpts{
		CutoverStateFile:      cutoverStateFile,
		CutoverState:          state,
		CutoverConfig:         config,
		ClusterApiKey:         clusterApiKey,
		ClusterApiSecret:      clusterApiSecret,
		ClusterRestCACert:     clusterRestCaCert,
		InsecureSkipTLSVerify: insecureSkipTLSVerify,
	}
}
