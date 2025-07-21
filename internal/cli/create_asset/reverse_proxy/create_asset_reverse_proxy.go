package reverse_proxy

import (
	"fmt"

	"github.com/confluentinc/kcp-internal/internal/generators/create_asset/reverse_proxy"
	"github.com/confluentinc/kcp-internal/internal/utils"
	"github.com/spf13/cobra"
)

var (
	region               string
	vpcId                string
	reverseProxyCidr     string
	migrationInfraFolder string
)

func NewReverseProxyCmd() *cobra.Command {
	reverseProxyCmd := &cobra.Command{
		Use:   "reverse-proxy",
		Short: "Create assets for the reverse proxy",
		Long: `Create assets for the reverse proxy
		
All flags can be provided via environment variables (uppercase, with underscores):

FLAG                     | ENV_VAR
-------------------------|------------------------------------------
--region                 | REGION=us-east-1
--vpc-id                 | VPC_ID=vpc-1234567890
--reverse-proxy-cidr     | REVERSE_PROXY_CIDR=10.0.0.0/16
--migration-infra-folder | MIGRATION_INFRA_FOLDER=path/to/migration-infra-folder
`,
		SilenceErrors: true,
		PreRunE:       preRunCreateReverseProxy,
		RunE:          runCreateReverseProxy,
	}

	reverseProxyCmd.Flags().StringVar(&region, "region", "", "The AWS region")
	reverseProxyCmd.Flags().StringVar(&vpcId, "vpc-id", "", "The VPC ID")
	reverseProxyCmd.Flags().StringVar(&reverseProxyCidr, "reverse-proxy-cidr", "", "The public subnet CIDR")
	reverseProxyCmd.Flags().StringVar(&migrationInfraFolder, "migration-infra-folder", "", "The migration infra folder produced from 'create-asset migration-infra' command after applying the terraform")

	reverseProxyCmd.MarkFlagRequired("region")
	reverseProxyCmd.MarkFlagRequired("vpc-id")
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

	opts := reverse_proxy.ReverseProxyOpts{
		Region:           region,
		VPCId:            vpcId,
		PublicSubnetCidr: reverseProxyCidr,
		TerraformOutput:  terraformState.Outputs,
	}

	return &opts, nil
}
