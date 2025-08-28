package bastion_host

import (
	"encoding/json"
	"fmt"
	"net"
	"os"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/confluentinc/kcp/internal/generators/create_asset/bastion_host"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/confluentinc/kcp/internal/utils"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

var (
	clusterFile      string
	region           string
	vpcId            string
	bastionHostCidr  net.IPNet
	createIGW        bool
	securityGroupIds string
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
	bastionHostCmd.Flags().AddFlagSet(requiredFlags)
	groups[requiredFlags] = "Required Flags"

	conditionalFlags := pflag.NewFlagSet("conditional", pflag.ExitOnError)
	conditionalFlags.SortFlags = false
	conditionalFlags.StringVar(&clusterFile, "cluster-file", "", "Cluster scan JSON file produced from 'kcp scan cluster' command")
	conditionalFlags.StringVar(&region, "region", "", "AWS region the bastion host is provisioned in")
	conditionalFlags.StringVar(&vpcId, "vpc-id", "", "VPC ID of the existing MSK cluster")
	bastionHostCmd.Flags().AddFlagSet(conditionalFlags)
	groups[conditionalFlags] = "Conditional Flags"

	// Optional flags.
	optionalFlags := pflag.NewFlagSet("optional", pflag.ExitOnError)
	optionalFlags.SortFlags = false
	optionalFlags.BoolVar(&createIGW, "create-igw", false, "When set, Terraform will create a new internet gateway in the VPC.")
	optionalFlags.StringVar(&securityGroupIds, "security-group-ids", "", "Existing list of comma separated AWS security group ids")
	bastionHostCmd.Flags().AddFlagSet(optionalFlags)
	groups[optionalFlags] = "Optional Flags"

	bastionHostCmd.SetUsageFunc(func(c *cobra.Command) error {
		fmt.Printf("%s\n\n", c.Short)

		flagOrder := []*pflag.FlagSet{requiredFlags, conditionalFlags, optionalFlags}
		groupNames := []string{"Required Flags", "Conditional Flags", "Optional Flags"}

		for i, fs := range flagOrder {
			usage := fs.FlagUsages()
			if usage != "" {
				fmt.Printf("%s:\n", groupNames[i])
				if groupNames[i] == "Conditional Flags" {
					fmt.Printf("  (Provide either --cluster-file OR both --region and --vpc-id)\n")
				}
				fmt.Printf("%s\n", usage)
			}
		}

		fmt.Println("All flags can be provided via environment variables (uppercase, with underscores).")

		return nil
	})

	bastionHostCmd.MarkFlagsMutuallyExclusive("cluster-file", "region")
	bastionHostCmd.MarkFlagsMutuallyExclusive("cluster-file", "vpc-id")
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

	bastionHostAssetGenerator := bastion_host.NewBastionHostAssetGenerator(*opts)
	if err := bastionHostAssetGenerator.Run(); err != nil {
		return fmt.Errorf("failed to create bastion host assets: %v", err)
	}

	return nil
}

func parseBastionHostOpts() (*bastion_host.BastionHostOpts, error) {
	if clusterFile != "" {
		// Parse cluster information from JSON file
		file, err := os.ReadFile(clusterFile)
		if err != nil {
			return nil, fmt.Errorf("failed to read cluster file: %v", err)
		}

		var clusterInfo types.ClusterInformation
		if err := json.Unmarshal(file, &clusterInfo); err != nil {
			return nil, fmt.Errorf("failed to unmarshal cluster info: %v", err)
		}

		if region == "" {
			region = aws.ToString(&clusterInfo.Region)
		}

		if vpcId == "" {
			vpcId = aws.ToString(&clusterInfo.ClusterNetworking.VpcId)
		}
	}

	opts := bastion_host.BastionHostOpts{
		Region:           region,
		VPCId:            vpcId,
		PublicSubnetCidr: bastionHostCidr.String(),
		CreateIGW:        createIGW,
		SecurityGroupIds: securityGroupIds,
	}

	return &opts, nil
}
