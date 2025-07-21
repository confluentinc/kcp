package migration_infra

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/confluentinc/kcp/internal/generators/create_asset/migration_infra"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/confluentinc/kcp/internal/utils"
	"github.com/spf13/cobra"
)

var (
	region                        string
	vpcId                         string
	ccEnvName                     string
	ccClusterName                 string
	ccClusterType                 string
	clusterFile                   string
	migrationInfraType            string
	jumpClusterBrokerSubnetConfig string
	ansibleControlNodeSubnetCIDR  string
	jumpClusterBrokerIAMRoleName  string
)

func NewMigrationInfraCmd() *cobra.Command {
	migrationInfraCmd := &cobra.Command{
		Use:   "migration-infra",
		Short: "Create assets for the migration infrastructure",
		Long: `Create assets for the migration infrastructure

All flags can be provided via environment variables (uppercase, with underscores):

FLAG                                            | ENV_VAR
------------------------------------------------|--------------------------------------------------
Required flags:
--region                                        | REGION=us-east-1
--vpc-id                                        | VPC_ID=vpc-1234567890
--cc-env-name                                   | CC_ENV_NAME=prod-env
--cc-cluster-name                               | CC_CLUSTER_NAME=my-cluster
--cluster-file                                  | CLUSTER_FILE=path/to/cluster.json
--type                                          | TYPE=1

Optional flags:
Provide with --type 1
--ansible-control-node-subnet-cidr              | ANSIBLE_CONTROL_NODE_SUBNET_CIDR=10.0.24.0/24
--jump-cluster-broker-subnet-config            	| JUMP_CLUSTER_BROKER_SUBNET_CONFIG="us-east-1a:10.0.34.0/24,us-east-1b:10.0.35.0/24,us-east-1c:10.0.36.0/24"
--jump-cluster-broker-iam-role-name             | JUMP_CLUSTER_BROKER_IAM_ROLE_NAME=msk-broker-role
--cc-cluster-type                               | CC_CLUSTER_TYPE=enterprise

Provide with --type 2
--jump-cluster-broker-subnet-config            	| JUMP_CLUSTER_BROKER_SUBNET_CONFIG="us-east-1a:10.0.34.0/24,us-east-1b:10.0.35.0/24,us-east-1c:10.0.36.0/24"
--ansible-control-node-subnet-cidr              | ANSIBLE_CONTROL_NODE_SUBNET_CIDR=10.0.24.0/24
--cc-cluster-type                               | CC_CLUSTER_TYPE=enterprise
`,
		SilenceErrors: true,
		PreRunE:       preRunCreateMigrationInfra,
		RunE:          runCreateMigrationInfra,
	}

	migrationInfraCmd.Flags().StringVar(&region, "region", "", "The AWS region")
	migrationInfraCmd.Flags().StringVar(&vpcId, "vpc-id", "", "The VPC ID")
	migrationInfraCmd.Flags().StringVar(&ccEnvName, "cc-env-name", "", "The Confluent Cloud environment name")
	migrationInfraCmd.Flags().StringVar(&ccClusterName, "cc-cluster-name", "", "The Confluent Cloud cluster name")
	migrationInfraCmd.Flags().StringVar(&ccClusterType, "cc-cluster-type", "", "The Confluent Cloud cluster type")
	migrationInfraCmd.Flags().StringVar(&clusterFile, "cluster-file", "", "The cluster json file produced from 'scan cluster' command")
	migrationInfraCmd.Flags().StringVar(&migrationInfraType, "type", "", "The migration infra type")

	//optional depending on type
	migrationInfraCmd.Flags().StringVar(&jumpClusterBrokerSubnetConfig, "jump-cluster-broker-subnet-config", "", "The Jump cluster broker subnet config")
	migrationInfraCmd.Flags().StringVar(&ansibleControlNodeSubnetCIDR, "ansible-control-node-subnet-cidr", "", "The Ansible control node subnet CIDR")
	migrationInfraCmd.Flags().StringVar(&jumpClusterBrokerIAMRoleName, "jump-cluster-broker-iam-role-name", "", "The Jump cluster broker IAM role name")

	migrationInfraCmd.MarkFlagRequired("region")
	migrationInfraCmd.MarkFlagRequired("vpc-id")
	migrationInfraCmd.MarkFlagRequired("cc-env-name")
	migrationInfraCmd.MarkFlagRequired("cc-cluster-name")
	migrationInfraCmd.MarkFlagRequired("cluster-file")
	migrationInfraCmd.MarkFlagRequired("type")

	return migrationInfraCmd
}

// sets flag values from corresponding environment variables if flags weren't explicitly provided
func preRunCreateMigrationInfra(cmd *cobra.Command, args []string) error {
	if err := utils.BindEnvToFlags(cmd); err != nil {
		return err
	}

	targetType, err := types.ToMigrationInfraType(migrationInfraType)
	if err != nil {
		return fmt.Errorf("invalid --type: %v", err)
	}

	switch targetType {
	case types.MskCpCcPrivateSaslIam:
		cmd.MarkFlagRequired("jump-cluster-broker-subnet-config")
		cmd.MarkFlagRequired("ansible-control-node-subnet-cidr")
		cmd.MarkFlagRequired("cc-cluster-type")
		cmd.MarkFlagRequired("jump-cluster-broker-iam-role-name")

	case types.MskCpCcPrivateSaslScram:
		cmd.MarkFlagRequired("jump-cluster-broker-subnet-config")
		cmd.MarkFlagRequired("ansible-control-node-subnet-cidr")
		cmd.MarkFlagRequired("cc-cluster-type")
	}

	return nil
}

func runCreateMigrationInfra(cmd *cobra.Command, args []string) error {
	opts, err := parseMigrationInfraOpts()
	if err != nil {
		return fmt.Errorf("failed to parse migration infra opts: %v", err)
	}

	migrationInfraAssetGenerator := migration_infra.NewMigrationInfraAssetGenerator(*opts)
	if err := migrationInfraAssetGenerator.Run(); err != nil {
		return fmt.Errorf("failed to create migration infrastructure assets: %v", err)
	}

	return nil
}

func parseMigrationInfraOpts() (*migration_infra.MigrationInfraOpts, error) {
	// ignoring error as already validated in preRunCreateMigrationInfra
	migrationInfraType, _ := types.ToMigrationInfraType(migrationInfraType)

	// Parse cluster information from JSON file
	file, err := os.ReadFile(clusterFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read cluster file: %v", err)
	}

	var clusterInfo types.ClusterInformation
	if err := json.Unmarshal(file, &clusterInfo); err != nil {
		return nil, fmt.Errorf("failed to unmarshal cluster info: %v", err)
	}

	opts := migration_infra.MigrationInfraOpts{
		Region:                        region,
		VPCId:                         vpcId,
		JumpClusterBrokerSubnetConfig: jumpClusterBrokerSubnetConfig,
		CCEnvName:                     ccEnvName,
		CCClusterName:                 ccClusterName,
		CCClusterType:                 ccClusterType,
		AnsibleControlNodeSubnetCIDR:  ansibleControlNodeSubnetCIDR,
		JumpClusterBrokerIAMRoleName:  jumpClusterBrokerIAMRoleName,
		ClusterInfo:                   clusterInfo,
		MigrationInfraType:            migrationInfraType,
	}

	return &opts, nil
}
