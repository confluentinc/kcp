package targetinfra

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/confluentinc/kcp/internal/services/hcl"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/confluentinc/kcp/internal/utils"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

var (
	stateFile       string
	sourceClusterId string

	needsEnvironment bool
	environmentName  string
	environmentId    string

	needsCluster        bool
	clusterName         string
	clusterId           string
	clusterType         string
	clusterAvailability string
	clusterCku          int

	awsRegion string

	needsPrivateLink       bool
	useExistingRoute53Zone bool
	vpcId                  string
	subnetCidrs            []string

	preventDestroy bool

	outputDir string
)

type TargetInfraOpts struct {
	NeedsEnvironment       bool
	EnvironmentName        string
	EnvironmentId          string
	NeedsCluster           bool
	ClusterName            string
	ClusterId              string
	ClusterType            string
	ClusterAvailability    string
	ClusterCku             int
	AwsRegion              string
	NeedsPrivateLink       bool
	UseExistingRoute53Zone bool
	PreventDestroy         bool
	VpcId                  string
	SubnetCidrs            []string
}

func NewTargetInfraCmd() *cobra.Command {
	targetInfraCmd := &cobra.Command{
		Use:   "target-infra",
		Short: "Create a target infrastructure asset",
		Long:  "Create Terraform assets for Confluent Cloud target infrastructure including environment, cluster, and private link setup. Infrastructure provisioning is controlled by --needs-environment, --needs-cluster and --needs-private-link.",
		Example: `  # Full provision from a kcp-state file (creates environment, cluster and private link)
  kcp create-asset target-infra \
      --state-file kcp-state.json \
      --source-cluster-id arn:aws:kafka:us-east-1:XXX:cluster/my-cluster/abc-5 \
      --needs-environment --env-name example-env \
      --needs-cluster --cluster-name example-cluster --cluster-type enterprise \
      --needs-private-link --subnet-cidrs 10.0.0.0/16,10.0.1.0/16,10.0.2.0/16 \
      --output-dir confluent-cloud-infrastructure

  # Reuse an existing environment + cluster, only wire up private link
  kcp create-asset target-infra \
      --aws-region us-east-1 --vpc-id vpc-xxxxxxxx \
      --env-id env-abc123 --cluster-id lkc-xyz789 --cluster-type dedicated \
      --needs-private-link --subnet-cidrs 10.0.0.0/16,10.0.1.0/16,10.0.2.0/16`,
		Annotations: map[string]string{
			"aws_iam_permissions": iamAnnotation(),
		},
		SilenceErrors: true,
		PreRunE:       preRunCreateTargetInfra,
		RunE:          runCreateTargetInfra,
	}

	groups := map[*pflag.FlagSet]string{}

	stateFileFlags := pflag.NewFlagSet("statefile", pflag.ExitOnError)
	stateFileFlags.SortFlags = false
	stateFileFlags.StringVar(&stateFile, "state-file", "", "Path to kcp state file (if provided, vpc-id and aws-region are extracted from state)")
	stateFileFlags.StringVar(&sourceClusterId, "source-cluster-id", "", "The ARN of the MSK cluster (required when --state-file is provided).")
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
	envFlags.BoolVar(&needsEnvironment, "needs-environment", false, "Create a new environment (requires --env-name)")
	envFlags.StringVar(&environmentName, "env-name", "", "Name for new environment (required with --needs-environment)")
	envFlags.StringVar(&environmentId, "env-id", "", "ID of existing environment (required without --needs-environment)")
	targetInfraCmd.Flags().AddFlagSet(envFlags)
	groups[envFlags] = "Target Environment"

	clusterFlags := pflag.NewFlagSet("cluster", pflag.ExitOnError)
	clusterFlags.SortFlags = false
	clusterFlags.BoolVar(&needsCluster, "needs-cluster", false, "Create a new cluster (requires --cluster-name and --cluster-type)")
	clusterFlags.StringVar(&clusterName, "cluster-name", "", "Name for new cluster (required with --needs-cluster)")
	clusterFlags.StringVar(&clusterId, "cluster-id", "", "ID of existing cluster (required without --needs-cluster)")
	clusterFlags.StringVar(&clusterType, "cluster-type", "", "Cluster type: 'dedicated' or 'enterprise' (required with --needs-cluster)")
	clusterFlags.StringVar(&clusterAvailability, "cluster-availability", "SINGLE_ZONE", "Cluster availability: 'SINGLE_ZONE' or 'MULTI_ZONE'")
	clusterFlags.IntVar(&clusterCku, "cluster-cku", 1, "Number of CKUs for dedicated clusters (MULTI_ZONE requires >= 2)")
	clusterFlags.BoolVar(&preventDestroy, "prevent-destroy", true, "Set lifecycle { prevent_destroy = true } on resources (use --prevent-destroy=false to disable)")
	targetInfraCmd.Flags().AddFlagSet(clusterFlags)
	groups[clusterFlags] = "Target Cluster"

	privateLinkFlags := pflag.NewFlagSet("privatelink", pflag.ExitOnError)
	privateLinkFlags.SortFlags = false
	privateLinkFlags.BoolVar(&needsPrivateLink, "needs-private-link", false, "Setup private link (requires --subnet-cidrs). Required for Enterprise clusters.")
	privateLinkFlags.StringSliceVar(&subnetCidrs, "subnet-cidrs", []string{}, "Subnet CIDRs for private link (required with --needs-private-link)")
	privateLinkFlags.BoolVar(&useExistingRoute53Zone, "use-existing-route53-zone", false, "Use an existing Route53 hosted zone instead of creating a new one")
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

	// Validate state file or manual configuration
	if stateFile != "" {
		// When using state file, source-cluster-id is required
		if sourceClusterId == "" {
			return fmt.Errorf("required flag `--source-cluster-id` not set when `--state-file` is provided")
		}
	} else {
		if awsRegion == "" {
			return fmt.Errorf("--aws-region is required when --state-file is not provided")
		}
		if vpcId == "" {
			return fmt.Errorf("--vpc-id is required when --state-file is not provided")
		}
	}

	if needsEnvironment {
		if environmentName == "" {
			return fmt.Errorf("--env-name is required with --needs-environment")
		}
	} else {
		if environmentId == "" {
			return fmt.Errorf("--env-id is required without --needs-environment")
		}
	}

	if needsCluster {
		if clusterName == "" {
			return fmt.Errorf("--cluster-name is required with --needs-cluster")
		}
		if clusterType == "" {
			return fmt.Errorf("--cluster-type is required with --needs-cluster")
		}
		if clusterAvailability != "SINGLE_ZONE" && clusterAvailability != "MULTI_ZONE" {
			return fmt.Errorf("invalid --cluster-availability: must be 'SINGLE_ZONE' or 'MULTI_ZONE', got '%s'", clusterAvailability)
		}
		if clusterCku < 1 {
			return fmt.Errorf("invalid --cluster-cku: must be >= 1, got %d", clusterCku)
		}
		if clusterAvailability == "MULTI_ZONE" && clusterCku < 2 {
			return fmt.Errorf("invalid --cluster-cku: MULTI_ZONE requires >= 2 CKUs, got %d", clusterCku)
		}
	} else if clusterId == "" {
		return fmt.Errorf("required flag `--cluster-id` not set when `--needs-cluster=false`")
	}

	if needsPrivateLink {
		if len(subnetCidrs) == 0 {
			return fmt.Errorf("--subnet-cidrs is required with --needs-private-link")
		}
	}

	return nil
}

func runCreateTargetInfra(cmd *cobra.Command, args []string) error {
	fmt.Printf("🚀 Generating target infrastructure\n")

	// If state file is provided, extract vpc-id and region from it
	if stateFile != "" {
		slog.Debug("reading state file", "file", stateFile)

		file, err := os.ReadFile(stateFile)
		if err != nil {
			return fmt.Errorf("failed to read statefile %s: %w", stateFile, err)
		}

		var state types.State
		if err := json.Unmarshal(file, &state); err != nil {
			return fmt.Errorf("failed to parse statefile JSON: %w", err)
		}

		cluster, err := state.GetClusterByArn(sourceClusterId)
		if err != nil {
			return fmt.Errorf("failed to get cluster: %w", err)
		}

		// Extract values from cluster
		awsRegion = aws.ToString(&cluster.Region)
		vpcId = aws.ToString(&cluster.AWSClientInformation.ClusterNetworking.VpcId)

		slog.Debug("extracted from state file",
			"region", awsRegion,
			"vpc_id", vpcId)
	}

	opts := parseTargetInfraOpts()

	request := types.TargetClusterWizardRequest{
		AwsRegion:              opts.AwsRegion,
		NeedsEnvironment:       opts.NeedsEnvironment,
		EnvironmentName:        opts.EnvironmentName,
		EnvironmentId:          opts.EnvironmentId,
		NeedsCluster:           opts.NeedsCluster,
		ClusterName:            opts.ClusterName,
		ClusterType:            opts.ClusterType,
		ClusterAvailability:    opts.ClusterAvailability,
		ClusterCku:             opts.ClusterCku,
		NeedsPrivateLink:       opts.NeedsPrivateLink,
		UseExistingRoute53Zone: opts.UseExistingRoute53Zone,
		PreventDestroy:         opts.PreventDestroy,
		VpcId:                  opts.VpcId,
		SubnetCidrRanges:       opts.SubnetCidrs,
	}

	slog.Debug("generating Terraform configuration")
	hclService := hcl.NewTargetInfraHCLService()
	project := hclService.GenerateTerraformFiles(request)

	if err := utils.ValidateOutputDir(outputDir); err != nil {
		return err
	}
	slog.Debug("creating output directory", "directory", outputDir)
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	if err := hcl.WriteTerraformProject(outputDir, project); err != nil {
		return fmt.Errorf("failed to write Terraform project: %w", err)
	}

	fmt.Printf("✅ Target infrastructure generated: %s\n", outputDir)
	return nil
}

func parseTargetInfraOpts() *TargetInfraOpts {
	return &TargetInfraOpts{
		NeedsEnvironment:       needsEnvironment,
		EnvironmentName:        environmentName,
		EnvironmentId:          environmentId,
		NeedsCluster:           needsCluster,
		ClusterName:            clusterName,
		ClusterId:              clusterId,
		ClusterType:            clusterType,
		ClusterAvailability:    clusterAvailability,
		ClusterCku:             clusterCku,
		AwsRegion:              awsRegion,
		NeedsPrivateLink:       needsPrivateLink,
		UseExistingRoute53Zone: useExistingRoute53Zone,
		PreventDestroy:         preventDestroy,
		VpcId:                  vpcId,
		SubnetCidrs:            subnetCidrs,
	}
}
