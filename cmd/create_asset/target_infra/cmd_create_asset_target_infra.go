package targetinfra

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strconv"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/confluentinc/kcp/internal/services/hcl"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/confluentinc/kcp/internal/utils"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

var (
	stateFile  string
	clusterArn string

	needsEnvironmentStr string
	environmentName     string
	environmentId       string

	needsClusterStr string
	clusterName     string
	clusterId       string
	clusterType     string

	awsRegion string

	needsPrivateLinkStr string
	vpcId               string
	subnetCidrs         []string

	outputDir string
)

type TargetInfraOpts struct {
	NeedsEnvironment bool
	EnvironmentName  string
	EnvironmentId    string
	NeedsCluster     bool
	ClusterName      string
	ClusterId        string
	ClusterType      string
	AwsRegion        string
	NeedsPrivateLink bool
	VpcId            string
	SubnetCidrs      []string
}

func NewTargetInfraCmd() *cobra.Command {
	targetInfraCmd := &cobra.Command{
		Use:           "target-infra",
		Short:         "Create a target infrastructure asset",
		Long:          "Create Terraform assets for Confluent Cloud target infrastructure including environment, cluster, and private link setup.",
		SilenceErrors: true,
		PreRunE:       preRunCreateTargetInfra,
		RunE:          runCreateTargetInfra,
	}

	groups := map[*pflag.FlagSet]string{}

	stateFileFlags := pflag.NewFlagSet("statefile", pflag.ExitOnError)
	stateFileFlags.SortFlags = false
	stateFileFlags.StringVar(&stateFile, "state-file", "", "Path to kcp state file (if provided, vpc-id and aws-region are extracted from state)")
	stateFileFlags.StringVar(&clusterArn, "cluster-arn", "", "MSK cluster ARN (required when --state-file is provided)")
	targetInfraCmd.Flags().AddFlagSet(stateFileFlags)
	groups[stateFileFlags] = "State File (Optional)"

	manualConfigFlags := pflag.NewFlagSet("manualconfig", pflag.ExitOnError)
	manualConfigFlags.SortFlags = false
	manualConfigFlags.StringVar(&awsRegion, "aws-region", "", "AWS region for the infrastructure (required when --state-file is not provided)")
	manualConfigFlags.StringVar(&vpcId, "vpc-id", "", "VPC ID (required when --state-file is not provided)")
	targetInfraCmd.Flags().AddFlagSet(manualConfigFlags)
	groups[manualConfigFlags] = "Manual Configuration (when not using state file)"

	envFlags := pflag.NewFlagSet("environment", pflag.ExitOnError)
	envFlags.SortFlags = false
	envFlags.StringVar(&needsEnvironmentStr, "needs-environment", "false", "Whether to create a new environment (true) or use existing (false)")
	envFlags.StringVar(&environmentName, "env-name", "", "Name for new environment (required when --needs-environment=true)")
	envFlags.StringVar(&environmentId, "env-id", "", "ID of existing environment (required when --needs-environment=false)")
	targetInfraCmd.Flags().AddFlagSet(envFlags)
	groups[envFlags] = "Target Environment"

	clusterFlags := pflag.NewFlagSet("cluster", pflag.ExitOnError)
	clusterFlags.SortFlags = false
	clusterFlags.StringVar(&needsClusterStr, "needs-cluster", "false", "Whether to create a new cluster (true) or use existing (false)")
	clusterFlags.StringVar(&clusterName, "cluster-name", "", "Name for new cluster (required when --needs-cluster=true)")
	clusterFlags.StringVar(&clusterId, "cluster-id", "", "ID of existing cluster (required when --needs-cluster=false)")
	clusterFlags.StringVar(&clusterType, "cluster-type", "", "Cluster type (e.g. 'dedicated' or 'enterprise')")
	targetInfraCmd.Flags().AddFlagSet(clusterFlags)
	groups[clusterFlags] = "Target Cluster"

	privateLinkFlags := pflag.NewFlagSet("privatelink", pflag.ExitOnError)
	privateLinkFlags.SortFlags = false
	privateLinkFlags.StringVar(&needsPrivateLinkStr, "needs-private-link", "false", "Whether the infrastructure needs private link setup. If using Enterprise clusters, Private Link is required.")
	privateLinkFlags.StringSliceVar(&subnetCidrs, "subnet-cidrs", []string{}, "Subnet CIDRs for private link (required when --needs-private-link=true)")
	targetInfraCmd.Flags().AddFlagSet(privateLinkFlags)
	groups[privateLinkFlags] = "Private Link"

	outputFlags := pflag.NewFlagSet("output", pflag.ExitOnError)
	outputFlags.SortFlags = false
	outputFlags.StringVar(&outputDir, "output-dir", "target_infra", "Output directory for generated Terraform files")
	targetInfraCmd.Flags().AddFlagSet(outputFlags)
	groups[outputFlags] = "Output"

	targetInfraCmd.MarkFlagsMutuallyExclusive("env-name", "env-id")
	targetInfraCmd.MarkFlagsMutuallyExclusive("cluster-id", "cluster-name")
	targetInfraCmd.MarkFlagsMutuallyExclusive("cluster-id", "cluster-type")

	targetInfraCmd.SetUsageFunc(func(c *cobra.Command) error {
		fmt.Printf("%s\n\n", c.Long)

		flagOrder := []*pflag.FlagSet{stateFileFlags, manualConfigFlags, envFlags, clusterFlags, privateLinkFlags, outputFlags}
		groupNames := []string{"State File (Optional)", "Manual Configuration (when not using state file)", "Target Environment", "Target Cluster", "Private Link", "Output"}

		for i, fs := range flagOrder {
			usage := fs.FlagUsages()
			if usage != "" {
				fmt.Printf("%s:\n%s\n", groupNames[i], usage)
			}
		}

		fmt.Println("All flags can be provided via environment variables (uppercase, with underscores).")

		return nil
	})

	return targetInfraCmd
}

func preRunCreateTargetInfra(cmd *cobra.Command, args []string) error {
	if err := utils.BindEnvToFlags(cmd); err != nil {
		return err
	}

	// Parsing flags from string to boolean. Opted for this approach to make the inputs appear as more natural syntax.
	// --needs-environment=false vs --needs-environment false
	needsEnvironment, err := strconv.ParseBool(needsEnvironmentStr)
	if err != nil {
		return fmt.Errorf("invalid value for --needs-environment: must be 'true' or 'false', got '%s'", needsEnvironmentStr)
	}

	needsCluster, err := strconv.ParseBool(needsClusterStr)
	if err != nil {
		return fmt.Errorf("invalid value for --needs-cluster: must be 'true' or 'false', got '%s'", needsClusterStr)
	}

	needsPrivateLink, err := strconv.ParseBool(needsPrivateLinkStr)
	if err != nil {
		return fmt.Errorf("invalid value for --needs-private-link: must be 'true' or 'false', got '%s'", needsPrivateLinkStr)
	}

	// Validate state file or manual configuration
	if stateFile != "" {
		// When using state file, cluster-arn is required
		if clusterArn == "" {
			return fmt.Errorf("required flag `--cluster-arn` not set when `--state-file` is provided")
		}
		// vpc-id and aws-region will be extracted from state file
	} else {
		// When not using state file, require manual configuration
		if awsRegion == "" {
			return fmt.Errorf("required flag `--aws-region` not set when `--state-file` is not provided")
		}
		if vpcId == "" {
			return fmt.Errorf("required flag `--vpc-id` not set when `--state-file` is not provided")
		}
	}

	if needsEnvironment {
		if environmentName == "" {
			return fmt.Errorf("required flag `--env-name` not set when `--needs-environment=true`")
		}
	} else {
		if environmentId == "" {
			return fmt.Errorf("required flag `--env-id` not set when `--needs-environment=false`")
		}
	}

	if needsCluster {
		if clusterName == "" {
			return fmt.Errorf("required flag `--cluster-name` not set when `--needs-cluster=true`")
		}
		if clusterType == "" {
			return fmt.Errorf("required flag `--cluster-type` not set when `--needs-cluster=true`")
		}
	} else {
		if clusterId == "" {
			return fmt.Errorf("required flag `--cluster-id` not set when `--needs-cluster=false`")
		}
	}

	if needsPrivateLink {
		if len(subnetCidrs) == 0 {
			return fmt.Errorf("required flag `--subnet-cidrs` not set when `--needs-private-link=true`")
		}
	}

	return nil
}

func runCreateTargetInfra(cmd *cobra.Command, args []string) error {
	slog.Info("🏁 generating target infrastructure")

	// If state file is provided, extract vpc-id and region from it
	if stateFile != "" {
		slog.Info("📖 reading state file", "file", stateFile)

		file, err := os.ReadFile(stateFile)
		if err != nil {
			return fmt.Errorf("failed to read statefile %s: %w", stateFile, err)
		}

		var state types.State
		if err := json.Unmarshal(file, &state); err != nil {
			return fmt.Errorf("failed to parse statefile JSON: %w", err)
		}

		cluster, err := utils.GetClusterByArn(&state, clusterArn)
		if err != nil {
			return fmt.Errorf("failed to get cluster: %w", err)
		}

		// Extract values from cluster
		awsRegion = aws.ToString(&cluster.Region)
		vpcId = aws.ToString(&cluster.AWSClientInformation.ClusterNetworking.VpcId)

		slog.Info("✅ extracted from state file",
			"region", awsRegion,
			"vpc_id", vpcId)
	}

	opts, err := parseTargetInfraOpts()
	if err != nil {
		return fmt.Errorf("failed to parse options: %w", err)
	}

	request := types.TargetClusterWizardRequest{
		AwsRegion:        opts.AwsRegion,
		NeedsEnvironment: opts.NeedsEnvironment,
		EnvironmentName:  opts.EnvironmentName,
		EnvironmentId:    opts.EnvironmentId,
		NeedsCluster:     opts.NeedsCluster,
		ClusterName:      opts.ClusterName,
		ClusterType:      opts.ClusterType,
		NeedsPrivateLink: opts.NeedsPrivateLink,
		VpcId:            opts.VpcId,
		SubnetCidrRanges: opts.SubnetCidrs,
	}

	slog.Info("📋 generating Terraform configuration")
	hclService := hcl.NewTargetInfraHCLService()
	project := hclService.GenerateTerraformFiles(request)

	slog.Info("📁 creating output directory", "directory", outputDir)
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	generator := NewTargetInfraGenerator(outputDir)
	if err := generator.BuildTerraformProject(project); err != nil {
		return fmt.Errorf("failed to write Terraform project: %w", err)
	}

	slog.Info("✅ target infrastructure generated", "directory", outputDir)
	return nil
}

func parseTargetInfraOpts() (*TargetInfraOpts, error) {
	needsEnvironment, err := strconv.ParseBool(needsEnvironmentStr)
	if err != nil {
		return nil, fmt.Errorf("invalid value for --needs-environment: %w", err)
	}

	needsCluster, err := strconv.ParseBool(needsClusterStr)
	if err != nil {
		return nil, fmt.Errorf("invalid value for --needs-cluster: %w", err)
	}

	needsPrivateLink, err := strconv.ParseBool(needsPrivateLinkStr)
	if err != nil {
		return nil, fmt.Errorf("invalid value for --needs-private-link: %w", err)
	}

	return &TargetInfraOpts{
		NeedsEnvironment: needsEnvironment,
		EnvironmentName:  environmentName,
		EnvironmentId:    environmentId,
		NeedsCluster:     needsCluster,
		ClusterName:      clusterName,
		ClusterId:        clusterId,
		ClusterType:      clusterType,
		AwsRegion:        awsRegion,
		NeedsPrivateLink: needsPrivateLink,
		VpcId:            vpcId,
		SubnetCidrs:      subnetCidrs,
	}, nil
}
