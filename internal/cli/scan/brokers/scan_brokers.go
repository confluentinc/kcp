package brokers

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	kafkatypes "github.com/aws/aws-sdk-go-v2/service/kafka/types"
	"github.com/goccy/go-yaml"

	"github.com/confluentinc/kcp/internal/client"
	"github.com/confluentinc/kcp/internal/generators/scan/brokers"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/confluentinc/kcp/internal/utils"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

var (
	discoverDir     string
	credentialsYaml string
)

func NewScanBrokersCmd() *cobra.Command {
	brokersCmd := &cobra.Command{
		Use:           "brokers",
		Short:         "Scan brokers using the Kafka Admin API",
		Long:          "Scan brokers for information using the Kafka Admin API such as topics, ACLs and cluster ID",
		SilenceErrors: true,
		PreRunE:       preRunScanBrokers,
		RunE:          runScanBrokers,
	}

	groups := map[*pflag.FlagSet]string{}

	requiredFlags := pflag.NewFlagSet("required", pflag.ExitOnError)
	requiredFlags.SortFlags = false
	requiredFlags.StringVar(&discoverDir, "discover-dir", "", "The path to the directory where the MSK cluster discovery reports have been written to.")
	requiredFlags.StringVar(&credentialsYaml, "credentials-yaml", "", "The credentials YAML file used for authenticating to the MSK cluster(s).")
	brokersCmd.Flags().AddFlagSet(requiredFlags)
	groups[requiredFlags] = "Required Flags"

	brokersCmd.SetUsageFunc(func(c *cobra.Command) error {
		fmt.Printf("%s\n\n", c.Short)

		flagOrder := []*pflag.FlagSet{requiredFlags}
		groupNames := []string{"Required Flags"}

		for i, fs := range flagOrder {
			usage := fs.FlagUsages()
			if usage != "" {
				fmt.Printf("%s:\n%s\n", groupNames[i], usage)
			}
		}

		fmt.Println("All flags can be provided via environment variables (uppercase, with underscores).")

		return nil
	})

	return brokersCmd
}

func preRunScanBrokers(cmd *cobra.Command, args []string) error {
	if err := utils.BindEnvToFlags(cmd); err != nil {
		return err
	}

	return nil
}

func runScanBrokers(cmd *cobra.Command, args []string) error {
	data, err := os.ReadFile(credentialsYaml)
	if err != nil {
		return fmt.Errorf("failed to read creds.yaml file: %w", err)
	}

	var credsFile types.CredsYaml
	if err := yaml.Unmarshal(data, &credsFile); err != nil {
		return fmt.Errorf("failed to unmarshal YAML: %w", err)
	}

	for region, clusters := range credsFile.Regions {
		for arn, cluster := range clusters.Clusters {

			opts, err := parseScanBrokersOpts(region, arn, cluster)
			if err != nil {
				slog.Error("failed to parse opts for cluster", "cluster", arn, "error", err)
				continue
			}

			kafkaAdminFactory := func(brokerAddresses []string, clientBrokerEncryptionInTransit kafkatypes.ClientBroker, kafkaVersion string) (client.KafkaAdmin, error) {
				switch opts.AuthType {
				case types.AuthTypeIAM:
					return client.NewKafkaAdmin(brokerAddresses, clientBrokerEncryptionInTransit, region, kafkaVersion, client.WithIAMAuth())
				case types.AuthTypeSASLSCRAM:
					return client.NewKafkaAdmin(brokerAddresses, clientBrokerEncryptionInTransit, region, kafkaVersion, client.WithSASLSCRAMAuth(opts.SASLScramUsername, opts.SASLScramPassword))
				case types.AuthTypeUnauthenticated:
					return client.NewKafkaAdmin(brokerAddresses, clientBrokerEncryptionInTransit, region, kafkaVersion, client.WithUnauthenticatedAuth())
				case types.AuthTypeTLS:
					return client.NewKafkaAdmin(brokerAddresses, clientBrokerEncryptionInTransit, region, kafkaVersion, client.WithTLSAuth(opts.TLSCACert, opts.TLSClientCert, opts.TLSClientKey))
				default:
					return nil, fmt.Errorf("❌ Auth type: %v not yet supported", opts.AuthType)
				}
			}

			brokerScanner := brokers.NewBrokerScanner(kafkaAdminFactory, opts)
			if err := brokerScanner.Run(); err != nil {
				slog.Error(fmt.Sprintf("❌ failed to scan cluster %s error: %v", opts.ClusterName, err))
				continue
			}
		}
	}

	return nil
}

func parseScanBrokersOpts(region, arn string, clusterEntry types.ClusterEntry) (*brokers.BrokerScannerOpts, error) {
	opts := brokers.BrokerScannerOpts{}

	clusterName, err := getClusterName(arn)
	if err != nil {
		return nil, fmt.Errorf("❌ failed to get cluster name: %v", err)
	}
	opts.ClusterName = clusterName

	if discoverDir != "" {
		clusterFile := filepath.Join(discoverDir, region, clusterName, fmt.Sprintf("%s.json", clusterName))
		file, err := os.ReadFile(clusterFile)
		if err != nil {
			return nil, fmt.Errorf("❌ failed to read cluster file: %v", err)
		}

		var clusterInfo types.ClusterInformation
		if err := json.Unmarshal(file, &clusterInfo); err != nil {
			return nil, fmt.Errorf("❌ failed to unmarshal cluster info: %v", err)
		}

		enabledMethods := getAuthMethods(clusterEntry)
		if len(enabledMethods) > 1 {
			slog.Warn(fmt.Sprintf("⚠️ multiple authentication methods enabled for cluster %s in %s, skipping the cluster", clusterName, region))
		} else {
			switch enabledMethods[0] {
			case "unauthenticated":
				opts.AuthType = types.AuthTypeUnauthenticated
				opts.BootstrapServer = *clusterInfo.BootstrapBrokers.BootstrapBrokerString
			case "iam":
				opts.AuthType = types.AuthTypeIAM

				if clusterInfo.BootstrapBrokers.BootstrapBrokerStringPublicSaslIam != nil {
					opts.BootstrapServer = *clusterInfo.BootstrapBrokers.BootstrapBrokerStringPublicSaslIam
				} else {
					opts.BootstrapServer = *clusterInfo.BootstrapBrokers.BootstrapBrokerStringSaslIam
				}
			case "sasl_scram":
				opts.AuthType = types.AuthTypeSASLSCRAM
				opts.SASLScramUsername = clusterEntry.AuthMethod.SASLScram.Username
				opts.SASLScramPassword = clusterEntry.AuthMethod.SASLScram.Password

				if clusterInfo.BootstrapBrokers.BootstrapBrokerStringPublicSaslScram != nil {
					opts.BootstrapServer = *clusterInfo.BootstrapBrokers.BootstrapBrokerStringPublicSaslScram
				} else {
					opts.BootstrapServer = *clusterInfo.BootstrapBrokers.BootstrapBrokerStringSaslScram
				}
			case "tls":
				opts.AuthType = types.AuthTypeTLS
				opts.TLSCACert = clusterEntry.AuthMethod.TLS.CACert
				opts.TLSClientCert = clusterEntry.AuthMethod.TLS.ClientCert
				opts.TLSClientKey = clusterEntry.AuthMethod.TLS.ClientKey

				if clusterInfo.BootstrapBrokers.BootstrapBrokerStringPublicTls != nil {
					opts.BootstrapServer = *clusterInfo.BootstrapBrokers.BootstrapBrokerStringPublicTls
				} else {
					opts.BootstrapServer = *clusterInfo.BootstrapBrokers.BootstrapBrokerStringTls
				}
			}
		}
	}

	return &opts, nil
}

func getClusterName(arn string) (string, error) {
	arnParts := strings.Split(arn, "/")
	if len(arnParts) < 2 {
		return "", fmt.Errorf("invalid cluster ARN format: %s", arn)
	}

	clusterName := arnParts[1]
	if clusterName == "" {
		return "", fmt.Errorf("cluster name not found in cluster ARN: %s", arn)
	}

	return clusterName, nil
}

func getAuthMethods(clusterEntry types.ClusterEntry) []string {
	enabledMethods := []string{}

	if clusterEntry.AuthMethod.Unauthenticated != nil && clusterEntry.AuthMethod.Unauthenticated.Use {
		enabledMethods = append(enabledMethods, "unauthenticated")
	}
	if clusterEntry.AuthMethod.IAM != nil && clusterEntry.AuthMethod.IAM.Use {
		enabledMethods = append(enabledMethods, "iam")
	}
	if clusterEntry.AuthMethod.SASLScram != nil && clusterEntry.AuthMethod.SASLScram.Use {
		enabledMethods = append(enabledMethods, "sasl_scram")
	}
	if clusterEntry.AuthMethod.TLS != nil && clusterEntry.AuthMethod.TLS.Use {
		enabledMethods = append(enabledMethods, "tls")
	}

	return enabledMethods
}
