package reverse_proxy

import (
	"encoding/json"
	"fmt"
	"net"
	"os"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/confluentinc/kcp/internal/generators/create_asset/reverse_proxy"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/confluentinc/kcp/internal/utils"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

var (
	clusterFile          string
	region               string
	vpcId                string
	reverseProxyCidr     net.IPNet
	migrationInfraFolder string
	securityGroupIds     string
)

func NewReverseProxyCmd() *cobra.Command {
	reverseProxyCmd := &cobra.Command{
		Use:           "reverse-proxy",
		Short:         "Create assets for the reverse proxy",
		Long:          "Create Terraform assets for deploying a reverse proxy to access the privately networked Confluent Cloud cluster",
		SilenceErrors: true,
		PreRunE:       preRunCreateReverseProxy,
		RunE:          runCreateReverseProxy,
	}

	groups := map[*pflag.FlagSet]string{}

	// Required flags.
	requiredFlags := pflag.NewFlagSet("required", pflag.ExitOnError)
	requiredFlags.SortFlags = false
	requiredFlags.IPNetVar(&reverseProxyCidr, "reverse-proxy-cidr", net.IPNet{}, "Revese proxy subnet CIDR (e.g. 10.0.255.0/24)")
	requiredFlags.StringVar(&migrationInfraFolder, "migration-infra-folder", "", "The migration-infra folder produced from 'kcp create-asset migration-infra' command after applying the Terraform")
	reverseProxyCmd.Flags().AddFlagSet(requiredFlags)
	groups[requiredFlags] = "Required Flags"

	conditionalFlags := pflag.NewFlagSet("conditional", pflag.ExitOnError)
	conditionalFlags.SortFlags = false
	conditionalFlags.StringVar(&clusterFile, "cluster-file", "", "Cluster scan JSON file produced from 'kcp scan cluster' command")
	conditionalFlags.StringVar(&region, "region", "", "AWS region the reverse proxy is provisioned in")
	conditionalFlags.StringVar(&vpcId, "vpc-id", "", "VPC ID of the existing MSK cluster")
	reverseProxyCmd.Flags().AddFlagSet(conditionalFlags)
	groups[conditionalFlags] = "Conditional Flags"

	// Optional flags.
	optionalFlags := pflag.NewFlagSet("optional", pflag.ExitOnError)
	optionalFlags.SortFlags = false
	optionalFlags.StringVar(&securityGroupIds, "security-group-ids", "", "Existing list of comma separated AWS security group ids")
	reverseProxyCmd.Flags().AddFlagSet(optionalFlags)
	groups[optionalFlags] = "Optional Flags"

	reverseProxyCmd.SetUsageFunc(func(c *cobra.Command) error {
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

	reverseProxyCmd.MarkFlagsMutuallyExclusive("cluster-file", "region")
	reverseProxyCmd.MarkFlagsMutuallyExclusive("cluster-file", "vpc-id")
	reverseProxyCmd.MarkFlagsRequiredTogether("region", "vpc-id")
	reverseProxyCmd.MarkFlagRequired("reverse-proxy-cidr")
	reverseProxyCmd.MarkFlagRequired("migration-infra-folder")

	return reverseProxyCmd
}

func preRunCreateReverseProxy(cmd *cobra.Command, args []string) error {
	if err := utils.BindEnvToFlags(cmd); err != nil {
		return err
	}

	return nil
}

func runCreateReverseProxy(cmd *cobra.Command, args []string) error {
	opts, err := parseReverseProxyOpts()
	if err != nil {
		return fmt.Errorf("failed to parse reverse proxy opts: %v", err)
	}

	reverseProxyAssetGenerator := reverse_proxy.NewReverseProxyAssetGenerator(*opts)
	if err := reverseProxyAssetGenerator.Run(); err != nil {
		return fmt.Errorf("failed to create reverse proxy assets: %v", err)
	}

	return nil
}

func parseReverseProxyOpts() (*reverse_proxy.ReverseProxyOpts, error) {
	requiredTFStateFields := []string{"confluent_cloud_cluster_bootstrap_endpoint"}
	terraformState, err := utils.ParseTerraformState(migrationInfraFolder, requiredTFStateFields)
	if err != nil {
		return nil, fmt.Errorf("error: %v\n please run terraform apply in the migration infra folder", err)
	}

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

	opts := reverse_proxy.ReverseProxyOpts{
		Region:           region,
		VPCId:            vpcId,
		PublicSubnetCidr: reverseProxyCidr.String(),
		TerraformOutput:  terraformState.Outputs,
		SecurityGroupIds: securityGroupIds,
	}

	return &opts, nil
}
