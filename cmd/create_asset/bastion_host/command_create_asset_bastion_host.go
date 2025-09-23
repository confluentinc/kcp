package bastion_host

import (
	"fmt"
	"net"

	"github.com/confluentinc/kcp/internal/utils"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

var (
	region           string
	vpcId            string
	bastionHostCidr  net.IPNet
	createIGW        bool
	securityGroupIds []string
)

func NewBastionHostCmd() *cobra.Command {
	bastionHostCmd := &cobra.Command{
		Use:           "bastion-host",
		Short:         "Create assets for the bastion host",
		Long:          "Create Terraform assets for the deploying a bastion host in AWS within you an existing VPC.",
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
	requiredFlags.StringVar(&vpcId, "vpc-id", "", "VPC ID of the existing MSK cluster")
	bastionHostCmd.Flags().AddFlagSet(requiredFlags)
	groups[requiredFlags] = "Required Flags"

	// Optional flags.
	optionalFlags := pflag.NewFlagSet("optional", pflag.ExitOnError)
	optionalFlags.SortFlags = false
	optionalFlags.BoolVar(&createIGW, "create-igw", false, "When set, Terraform will create a new internet gateway in the VPC.")
	optionalFlags.StringSliceVar(&securityGroupIds, "security-group-ids", []string{}, "Existing list of comma separated AWS security group ids")
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

	bastionHostCmd.MarkFlagRequired("bastion-host-cidr")

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
		return fmt.Errorf("failed to parse bastion host opts: %v", err)
	}

	bastionHostAssetGenerator := NewBastionHostAssetGenerator(*opts)
	if err := bastionHostAssetGenerator.Run(); err != nil {
		return fmt.Errorf("failed to create bastion host assets: %v", err)
	}

	return nil
}

func parseBastionHostOpts() (*BastionHostOpts, error) {
	opts := BastionHostOpts{
		Region:           region,
		VPCId:            vpcId,
		PublicSubnetCidr: bastionHostCidr.String(),
		CreateIGW:        createIGW,
		SecurityGroupIds: securityGroupIds,
	}

	return &opts, nil
}
