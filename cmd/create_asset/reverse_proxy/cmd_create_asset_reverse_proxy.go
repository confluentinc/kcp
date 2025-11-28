package reverse_proxy

import (
	"fmt"
	"net"

	"github.com/confluentinc/kcp/internal/utils"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

var (
	region            string
	vpcId             string
	reverseProxyCidr  net.IPNet
	bootstrapEndpoint string
)

func NewReverseProxyCmd() *cobra.Command {
	reverseProxyCmd := &cobra.Command{
		Use:           "reverse-proxy",
		Short:         "Create assets for the reverse proxy",
		Long:          "Create Terraform assets for deploying a reverse proxy to access the privately networked Confluent Cloud cluster",
		SilenceErrors: true,
		PreRunE:       preRunCreateReverseProxy,
		RunE:          runCreateReverseProxy,
		Hidden:        true,
	}

	groups := map[*pflag.FlagSet]string{}

	// Required flags.
	requiredFlags := pflag.NewFlagSet("required", pflag.ExitOnError)
	requiredFlags.SortFlags = false
	requiredFlags.IPNetVar(&reverseProxyCidr, "reverse-proxy-cidr", net.IPNet{}, "Reverse proxy subnet CIDR (e.g. 10.0.255.0/24)")
	requiredFlags.StringVar(&region, "region", "", "AWS region the reverse proxy is provisioned in")
	requiredFlags.StringVar(&vpcId, "vpc-id", "", "VPC ID of the existing MSK cluster")
	requiredFlags.StringVar(&bootstrapEndpoint, "bootstrap-endpoint", "", "The Confluent Cloud cluster bootstrap endpoint (e.g. pkc-xxxxx.us-east-1.aws.confluent.cloud:9092)")
	reverseProxyCmd.Flags().AddFlagSet(requiredFlags)
	groups[requiredFlags] = "Required Flags"

	reverseProxyCmd.SetUsageFunc(func(c *cobra.Command) error {
		fmt.Printf("%s\n\n", c.Short)

		flagOrder := []*pflag.FlagSet{requiredFlags}
		groupNames := []string{"Required Flags"}

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

	reverseProxyCmd.MarkFlagRequired("reverse-proxy-cidr")
	reverseProxyCmd.MarkFlagRequired("region")
	reverseProxyCmd.MarkFlagRequired("vpc-id")
	reverseProxyCmd.MarkFlagRequired("bootstrap-endpoint")

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

	reverseProxyAssetGenerator := NewReverseProxyAssetGenerator(*opts)
	if err := reverseProxyAssetGenerator.Run(); err != nil {
		return fmt.Errorf("failed to create reverse proxy assets: %v", err)
	}

	return nil
}

func parseReverseProxyOpts() (*ReverseProxyOpts, error) {
	opts := ReverseProxyOpts{
		Region:                                 region,
		VPCId:                                  vpcId,
		PublicSubnetCidr:                       reverseProxyCidr.String(),
		ConfluentCloudClusterBootstrapEndpoint: bootstrapEndpoint,
	}

	return &opts, nil
}
