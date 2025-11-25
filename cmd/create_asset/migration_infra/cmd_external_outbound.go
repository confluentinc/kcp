package migration_infra

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/confluentinc/kcp/internal/services/hcl"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/confluentinc/kcp/internal/utils"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

var (
	extOutboundStateFile  string
	extOutboundClusterArn string
	targetEnvironmentId   string
	targetClusterId       string
	targetRestEndpoint    string
	subnetId              string
	securityGroupId       string
	clusterLinkName       string
	outputDir             string
)

func NewExternalOutboundCmd() *cobra.Command {
	externalOutboundCmd := &cobra.Command{
		Use:           "external-outbound",
		Short:         "Create assets for external outbound cluster linking infrastructure",
		Long:          "Create Terraform assets for external outbound cluster linking that connects MSK to Confluent Cloud via private networking.",
		SilenceErrors: true,
		PreRunE:       preRunExternalOutbound,
		RunE:          runExternalOutbound,
	}

	requiredFlags := pflag.NewFlagSet("required", pflag.ExitOnError)
	requiredFlags.SortFlags = false
	requiredFlags.StringVar(&extOutboundStateFile, "state-file", "", "The path to the kcp state file where the MSK cluster discovery reports have been written to.")
	requiredFlags.StringVar(&extOutboundClusterArn, "cluster-arn", "", "The ARN of the MSK cluster to create external outbound infrastructure for.")
	requiredFlags.StringVar(&targetEnvironmentId, "target-environment-id", "", "The Confluent Cloud environment ID.")
	requiredFlags.StringVar(&targetClusterId, "target-cluster-id", "", "The Confluent Cloud cluster ID.")
	requiredFlags.StringVar(&targetRestEndpoint, "target-rest-endpoint", "", "The Confluent Cloud cluster REST endpoint.")
	externalOutboundCmd.Flags().AddFlagSet(requiredFlags)

	optionalFlags := pflag.NewFlagSet("optional", pflag.ExitOnError)
	optionalFlags.SortFlags = false
	optionalFlags.StringVar(&subnetId, "subnet-id", "", "Override subnet ID for the EC2 instance (defaults to first broker subnet).")
	optionalFlags.StringVar(&securityGroupId, "security-group-id", "", "Override security group ID for the EC2 instance (defaults to first cluster security group).")
	optionalFlags.StringVar(&clusterLinkName, "cluster-link-name", "", "Name for the cluster link (defaults to 'kcp-msk-to-cc-link').")
	optionalFlags.StringVar(&outputDir, "output-dir", "migration_infra", "Output directory for generated Terraform files.")
	externalOutboundCmd.Flags().AddFlagSet(optionalFlags)

	externalOutboundCmd.SetUsageFunc(func(c *cobra.Command) error {
		fmt.Printf("%s\n\n", c.Long)

		flagOrder := []*pflag.FlagSet{requiredFlags, optionalFlags}
		groupNames := []string{"Required Flags", "Optional Flags"}

		for i, fs := range flagOrder {
			usage := fs.FlagUsages()
			if usage != "" {
				fmt.Printf("%s:\n%s\n", groupNames[i], usage)
			}
		}

		fmt.Println("All flags can be provided via environment variables (uppercase, with underscores).")

		return nil
	})

	externalOutboundCmd.MarkFlagRequired("state-file")
	externalOutboundCmd.MarkFlagRequired("cluster-arn")
	externalOutboundCmd.MarkFlagRequired("target-environment-id")
	externalOutboundCmd.MarkFlagRequired("target-cluster-id")
	externalOutboundCmd.MarkFlagRequired("target-rest-endpoint")

	return externalOutboundCmd
}

func preRunExternalOutbound(cmd *cobra.Command, args []string) error {
	if err := utils.BindEnvToFlags(cmd); err != nil {
		return err
	}
	return nil
}

func runExternalOutbound(cmd *cobra.Command, args []string) error {
	slog.Info("🏁 generating external outbound cluster linking infrastructure")

	file, err := os.ReadFile(extOutboundStateFile)
	if err != nil {
		return fmt.Errorf("failed to read statefile %s: %w", extOutboundStateFile, err)
	}

	var state types.State
	if err := json.Unmarshal(file, &state); err != nil {
		return fmt.Errorf("failed to parse statefile JSON: %w", err)
	}

	cluster, err := utils.GetClusterByArn(&state, extOutboundClusterArn)
	if err != nil {
		return fmt.Errorf("failed to get cluster: %w", err)
	}

	region := aws.ToString(&cluster.Region)
	vpcId := aws.ToString(&cluster.AWSClientInformation.ClusterNetworking.VpcId)

	// We use the subnet attached to the MSK cluster broker 1 but we allow user override at their own risk.
	if subnetId == "" {
		if len(cluster.AWSClientInformation.ClusterNetworking.SubnetIds) > 0 {
			subnetId = cluster.AWSClientInformation.ClusterNetworking.SubnetIds[0]
		} else {
			return fmt.Errorf("no subnet IDs found in cluster networking information")
		}
	}

	// We use the security group attached to the MSK cluster but we allow user override at their own risk.
	if securityGroupId == "" {
		if len(cluster.AWSClientInformation.ClusterNetworking.SecurityGroups) > 0 {
			securityGroupId = cluster.AWSClientInformation.ClusterNetworking.SecurityGroups[0]
		} else {
			return fmt.Errorf("no security groups found in cluster networking information")
		}
	}

	if clusterLinkName == "" {
		clusterLinkName = "kcp-msk-to-cc-link"
	}

	extOutboundBrokers := extractBrokerInformation(cluster)

	bootstrapServers := aws.ToString(cluster.AWSClientInformation.BootstrapBrokers.BootstrapBrokerStringSaslScram)
	if bootstrapServers == "" {
		return fmt.Errorf("SASL/SCRAM bootstrap brokers not available for this cluster")
	}

	mskClusterId := aws.ToString(&cluster.KafkaAdminClientInformation.ClusterID)

	request := types.MigrationWizardRequest{
		HasPublicCcEndpoints:         false,
		UseJumpClusters:              false,
		VpcId:                        vpcId,
		ExtOutboundSubnetId:          subnetId,
		ExtOutboundSecurityGroupId:   securityGroupId,
		ExtOutboundBrokers:           extOutboundBrokers,
		MskRegion:                    region,
		MskClusterId:                 mskClusterId,
		MskSaslScramBootstrapServers: bootstrapServers,
		TargetEnvironmentId:          targetEnvironmentId,
		TargetClusterId:              targetClusterId,
		TargetRestEndpoint:           targetRestEndpoint,
		ClusterLinkName:              clusterLinkName,
	}

	slog.Info("📋 generating Terraform configuration")
	hclService := hcl.NewMigrationInfraHCLService()
	project := hclService.GenerateTerraformModules(request)

	slog.Info("📁 creating output directory", "directory", outputDir)
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	if err := buildTerraformProject(outputDir, project); err != nil {
		return fmt.Errorf("failed to write Terraform project: %w", err)
	}

	slog.Info("✅ external outbound cluster linking infrastructure generated", "directory", outputDir)
	return nil
}

func extractBrokerInformation(cluster *types.DiscoveredCluster) []types.ExtOutboundClusterKafkaBroker {
	var brokers []types.ExtOutboundClusterKafkaBroker
	bootstrapBrokers := strings.Split(aws.ToString(cluster.AWSClientInformation.BootstrapBrokers.BootstrapBrokerStringSaslScram), ",")

	var formattedBootstrapBrokers []string
	for _, broker := range bootstrapBrokers {
		formattedBootstrapBrokers = append(formattedBootstrapBrokers, strings.TrimSuffix(broker, ":9096"))
	}
	slices.Sort(formattedBootstrapBrokers)

	for _, subnet := range cluster.AWSClientInformation.ClusterNetworking.Subnets {
		broker := types.ExtOutboundClusterKafkaBroker{
			ID:       fmt.Sprintf("%d", subnet.SubnetMskBrokerId),
			SubnetID: subnet.SubnetId,
			Endpoints: []types.ExtOutboundClusterKafkaEndpoint{
				{
					Host: formattedBootstrapBrokers[subnet.SubnetMskBrokerId-1],
					Port: 9096, // Default port for SASL/SCRAM.
					IP:   subnet.PrivateIpAddress,
				},
			},
		}

		brokers = append(brokers, broker)
	}

	return brokers
}

func buildTerraformProject(outputDir string, project types.MigrationInfraTerraformProject) error {
	if project.MainTf != "" {
		if err := os.WriteFile(filepath.Join(outputDir, "main.tf"), []byte(project.MainTf), 0644); err != nil {
			return fmt.Errorf("failed to write main.tf: %w", err)
		}
		slog.Info("✅ wrote root main.tf")
	}

	if project.ProvidersTf != "" {
		if err := os.WriteFile(filepath.Join(outputDir, "providers.tf"), []byte(project.ProvidersTf), 0644); err != nil {
			return fmt.Errorf("failed to write providers.tf: %w", err)
		}
		slog.Info("✅ wrote root providers.tf")
	}

	if project.VariablesTf != "" {
		if err := os.WriteFile(filepath.Join(outputDir, "variables.tf"), []byte(project.VariablesTf), 0644); err != nil {
			return fmt.Errorf("failed to write variables.tf: %w", err)
		}
		slog.Info("✅ wrote root variables.tf")
	}

	if project.InputsAutoTfvars != "" {
		if err := os.WriteFile(filepath.Join(outputDir, "inputs.auto.tfvars"), []byte(project.InputsAutoTfvars), 0644); err != nil {
			return fmt.Errorf("failed to write inputs.auto.tfvars: %w", err)
		}
		slog.Info("✅ wrote root inputs.auto.tfvars")
	}

	for _, module := range project.Modules {
		moduleDir := filepath.Join(outputDir, module.Name)
		if err := os.MkdirAll(moduleDir, 0755); err != nil {
			return fmt.Errorf("failed to create module directory %s: %w", module.Name, err)
		}

		if module.MainTf != "" {
			if err := os.WriteFile(filepath.Join(moduleDir, "main.tf"), []byte(module.MainTf), 0644); err != nil {
				return fmt.Errorf("failed to write module %s main.tf: %w", module.Name, err)
			}
		}

		if module.VariablesTf != "" {
			if err := os.WriteFile(filepath.Join(moduleDir, "variables.tf"), []byte(module.VariablesTf), 0644); err != nil {
				return fmt.Errorf("failed to write module %s variables.tf: %w", module.Name, err)
			}
		}

		if module.OutputsTf != "" {
			if err := os.WriteFile(filepath.Join(moduleDir, "outputs.tf"), []byte(module.OutputsTf), 0644); err != nil {
				return fmt.Errorf("failed to write module %s outputs.tf: %w", module.Name, err)
			}
		}

		if module.VersionsTf != "" {
			if err := os.WriteFile(filepath.Join(moduleDir, "versions.tf"), []byte(module.VersionsTf), 0644); err != nil {
				return fmt.Errorf("failed to write module %s versions.tf: %w", module.Name, err)
			}
		}

		for filename, content := range module.AdditionalFiles {
			if err := os.WriteFile(filepath.Join(moduleDir, filename), []byte(content), 0644); err != nil {
				return fmt.Errorf("failed to write module %s file %s: %w", module.Name, filename, err)
			}
		}

		slog.Info("✅ wrote module", "module", module.Name)
	}

	return nil
}
