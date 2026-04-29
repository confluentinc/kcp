package bastion_host

import (
	"fmt"
	"net"

	"github.com/confluentinc/kcp/internal/services/iampolicy"
	"github.com/confluentinc/kcp/internal/utils"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

var (
	region                  string
	vpcId                   string
	bastionHostCidr         net.IPNet
	existingInternetGateway bool
	securityGroupIds        []string
	outputDir               string
)

func NewBastionHostCmd() *cobra.Command {
	bastionHostCmd := &cobra.Command{
		Use:   "bastion-host",
		Short: "Create assets for the bastion host",
		Long:  "Create Terraform assets for deploying a bastion host in AWS within an existing VPC. Use this when your source Kafka cluster (MSK or OSK) is not reachable from the machine running kcp and you do not already have a jump server.",
		Example: `  # Provision a new bastion (and a new internet gateway) in an existing VPC
  kcp create-asset bastion-host \
      --region us-east-1 \
      --vpc-id vpc-xxxxxxxx \
      --bastion-host-cidr 10.0.255.0/24 \
      --security-group-ids sg-xxxxxxxxxx \
      --output-dir bastion_host

  # Same, but reuse the existing internet gateway already attached to the VPC
  kcp create-asset bastion-host \
      --region us-east-1 \
      --vpc-id vpc-xxxxxxxx \
      --bastion-host-cidr 10.0.255.0/24 \
      --existing-internet-gateway`,
		Annotations: map[string]string{
			iampolicy.AnnotationKey: bastionHostIAMAnnotation(),
		},
		SilenceErrors: true,
		PreRunE:       preRunCreateBastionHost,
		RunE:          runCreateBastionHost,
	}

	groups := map[*pflag.FlagSet]string{}

	// Required flags.
	requiredFlags := pflag.NewFlagSet("required", pflag.ExitOnError)
	requiredFlags.SortFlags = false
	requiredFlags.IPNetVar(&bastionHostCidr, "bastion-host-cidr", net.IPNet{}, "The bastion host CIDR (e.g. 10.0.255.0/24)")
	requiredFlags.StringVar(&region, "region", "", "AWS region the bastion host is provisioned in")
	requiredFlags.StringVar(&vpcId, "vpc-id", "", "VPC ID where the bastion host will be provisioned (typically the source Kafka cluster's VPC)")
	bastionHostCmd.Flags().AddFlagSet(requiredFlags)
	groups[requiredFlags] = "Required Flags"

	// Optional flags.
	optionalFlags := pflag.NewFlagSet("optional", pflag.ExitOnError)
	optionalFlags.SortFlags = false
	optionalFlags.BoolVar(&existingInternetGateway, "existing-internet-gateway", false, "Whether to reuse the internet gateway already attached to the VPC. (default: false — a new internet gateway is created)")
	optionalFlags.StringSliceVar(&securityGroupIds, "security-group-ids", []string{}, "Existing list of comma separated AWS security group ids")
	optionalFlags.StringVar(&outputDir, "output-dir", "bastion_host", "Directory to output the generated Terraform files to")
	bastionHostCmd.Flags().AddFlagSet(optionalFlags)
	groups[optionalFlags] = "Optional Flags"

	bastionHostCmd.SetUsageFunc(func(c *cobra.Command) error {
		fmt.Printf("%s\n\n", c.Short)

		flagOrder := []*pflag.FlagSet{requiredFlags, optionalFlags}
		groupNames := []string{"Required Flags", "Optional Flags"}

		for i, fs := range flagOrder {
			usage := fs.FlagUsages()
			if usage != "" {
				fmt.Printf("%s:\n", groupNames[i])
				fmt.Printf("%s\n", usage)
			}
		}

		fmt.Println("All flags can be provided via environment variables (uppercase, with underscores).")

		return nil
	})

	bastionHostCmd.MarkFlagsRequiredTogether("region", "vpc-id")

	_ = bastionHostCmd.MarkFlagRequired("bastion-host-cidr")

	return bastionHostCmd
}

// sets flag values from corresponding environment variables if flags weren't explicitly provided
func preRunCreateBastionHost(cmd *cobra.Command, args []string) error {
	if err := utils.BindEnvToFlags(cmd); err != nil {
		return err
	}

	return nil
}

func runCreateBastionHost(cmd *cobra.Command, args []string) error {
	opts, err := parseBastionHostOpts()
	if err != nil {
		return fmt.Errorf("failed to parse bastion host opts: %w", err)
	}

	bastionHostAssetGenerator := NewBastionHostAssetGenerator(*opts)
	if err := bastionHostAssetGenerator.Run(); err != nil {
		return fmt.Errorf("failed to create bastion host assets: %w", err)
	}

	return nil
}

func parseBastionHostOpts() (*BastionHostOpts, error) {
	opts := BastionHostOpts{
		Region:                     region,
		VPCId:                      vpcId,
		PublicSubnetCidr:           bastionHostCidr.String(),
		HasExistingInternetGateway: existingInternetGateway,
		SecurityGroupIds:           securityGroupIds,
		OutputDir:                  outputDir,
	}

	return &opts, nil
}
