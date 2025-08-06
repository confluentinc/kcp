package kafka_acls

import (
	"github.com/confluentinc/kcp/internal/generators/create_asset/migrate_acls/kafka_acls"

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
		Long:  "Convert Kafka ACLs to Confluent Cloud ACLs as individual Terraform resources.",

		RunE: func(cmd *cobra.Command, args []string) error {
			return kafka_acls.RunConvertKafkaAcls(clusterFile, outputDir)
		},
	}

	aclsCmd.Flags().StringVar(&clusterFile, "cluster-file", "", "The cluster json file produced from 'scan cluster' command")
	aclsCmd.Flags().StringVar(&outputDir, "output-dir", "", "The directory to write the ACL files to")

	aclsCmd.MarkFlagRequired("cluster-file")

	return aclsCmd
}
