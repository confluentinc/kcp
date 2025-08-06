package kafka_acls

import (
	"fmt"

	"github.com/confluentinc/kcp/internal/generators/create_asset/migrate_acls/kafka_acls"
	"github.com/confluentinc/kcp/internal/utils"
	"github.com/spf13/cobra"
)

var (
	clusterFile string
	outputDir   string
)

func NewConvertKafkaAclsCmd() *cobra.Command {
	aclsCmd := &cobra.Command{
		Use:   "kafka-acls",
		Short: "Convert Kafka ACLs to Confluent Cloud ACLs.",
		Long: `Convert Kafka ACLs to Confluent Cloud ACLs as individual Terraform resources.

All flags can be provided via environment variables (uppercase, with underscores):

FLAG                     | ENV_VAR
-------------------------|---------------------------
--cluster-file           | CLUSTER_FILE=path/to/cluster.json
--output-dir             | OUTPUT_DIR=path/to/output
`,
		SilenceErrors: true,
		PreRunE:       preRunConvertKafkaAcls,
		RunE:          runConvertKafkaAcls,
	}

	aclsCmd.Flags().StringVar(&clusterFile, "cluster-file", "", "The cluster json file produced from 'scan cluster' command")
	aclsCmd.Flags().StringVar(&outputDir, "output-dir", "", "The directory to write the ACL files to")

	aclsCmd.MarkFlagRequired("cluster-file")
	aclsCmd.MarkFlagRequired("output-dir")

	return aclsCmd
}

// sets flag values from corresponding environment variables if flags weren't explicitly provided
func preRunConvertKafkaAcls(cmd *cobra.Command, args []string) error {
	if err := utils.BindEnvToFlags(cmd); err != nil {
		return err
	}

	return nil
}

func runConvertKafkaAcls(cmd *cobra.Command, args []string) error {
	if err := kafka_acls.RunConvertKafkaAcls(clusterFile, outputDir); err != nil {
		return fmt.Errorf("failed to convert Kafka ACLs: %v", err)
	}

	return nil
}
