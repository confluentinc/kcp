package bastion_host

import (
	"fmt"

	"github.com/confluentinc/kcp-internal/internal/generators/create_asset/bastion_host"
	"github.com/confluentinc/kcp-internal/internal/utils"
	"github.com/spf13/cobra"
)

func NewBastionHostCmd() *cobra.Command {
	bastionHostCmd := &cobra.Command{
		Use:   "bastion-host",
		Short: "Create assets for the bastion host",
		Long: `Create assets for the bastion host

All flags can be provided via environment variables (uppercase, with underscores):

FLAG                     | ENV_VAR
-------------------------|---------------------------
--region                 | REGION=us-east-1
--vpc-id                 | VPC_ID=vpc-1234567890
--bastion-host-cidr      | BASTION_HOST_CIDR=10.0.0.0/16
`,
		SilenceErrors: true,
		PreRunE:       preRunCreateBastionHost,
		RunE:          runCreateBastionHost,
	}

	bastionHostCmd.Flags().String("region", "", "The AWS region to target")
	bastionHostCmd.Flags().String("vpc-id", "", "The VPC ID to target")
	bastionHostCmd.Flags().String("bastion-host-cidr", "", "The bastion host CIDR to target")

	bastionHostCmd.MarkFlagRequired("region")
	bastionHostCmd.MarkFlagRequired("vpc-id")
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
	opts, err := parseBastionHostOpts(cmd)
	if err != nil {
		return fmt.Errorf("failed to parse bastion host opts: %v", err)
	}

	bastionHostAssetGenerator := bastion_host.NewBastionHostAssetGenerator(*opts)
	if err := bastionHostAssetGenerator.Run(); err != nil {
		return fmt.Errorf("failed to create bastion host assets: %v", err)
	}

	return nil
}

func parseBastionHostOpts(cmd *cobra.Command) (*bastion_host.BastionHostOpts, error) {
	region, err := cmd.Flags().GetString("region")
	if err != nil {
		return nil, fmt.Errorf("failed to get region: %v", err)
	}

	vpcId, err := cmd.Flags().GetString("vpc-id")
	if err != nil {
		return nil, fmt.Errorf("failed to get vpc id: %v", err)
	}

	bastionHostCidr, err := cmd.Flags().GetString("bastion-host-cidr")
	if err != nil {
		return nil, fmt.Errorf("failed to get bastion host cidr: %v", err)
	}

	opts := bastion_host.BastionHostOpts{
		Region:           region,
		VPCId:            vpcId,
		PublicSubnetCidr: bastionHostCidr,
	}

	return &opts, nil
}
