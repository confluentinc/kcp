// Package confluent: topic.go generates Terraform for plain Confluent Cloud
// topics (used by `kcp create-asset migrate-topics --mode new`).
//
// The CCSupportedTopicConfigs allow-list captures the subset of Apache Kafka
// topic-level configs that Confluent Cloud accepts at create time. Any source
// config outside the list is dropped during HCL generation — including
// replication.factor, which CC manages itself.
//
// The list snapshot was taken from
// https://docs.confluent.io/cloud/current/topics/manage.html#ak-topic-configurations-for-all-ccloud-cluster-types
// on 2026-05-11. CC may add or remove supported configs over time; if drift is
// observed on real clusters, update the list and the snapshot date.
package confluent

import (
	"github.com/confluentinc/kcp/internal/utils"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"
)

// CCSupportedTopicConfigs is the allow-list of source topic configs that are
// preserved in --mode new output. Keys not in this set are dropped silently.
var CCSupportedTopicConfigs = map[string]struct{}{
	"cleanup.policy":                        {},
	"delete.retention.ms":                   {},
	"max.message.bytes":                     {},
	"max.compaction.lag.ms":                 {},
	"message.timestamp.difference.max.ms":   {},
	"message.timestamp.before.max.ms":       {},
	"message.timestamp.after.max.ms":        {},
	"message.timestamp.type":                {},
	"min.compaction.lag.ms":                 {},
	"min.insync.replicas":                   {},
	"retention.bytes":                       {},
	"retention.ms":                          {},
	"segment.bytes":                         {},
	"segment.ms":                            {},
	"confluent.key.schema.validation":       {},
	"confluent.value.schema.validation":     {},
	"confluent.key.subject.name.strategy":   {},
	"confluent.value.subject.name.strategy": {},
}

// filterCCSupportedConfigs returns only the entries of src whose keys are in
// CCSupportedTopicConfigs, with values dereferenced. Nil values (which represent
// "CC default") are skipped so we never emit `key = ""` artifacts.
//
// replication.factor is not in the allow-list and is therefore dropped here —
// no special-case branch needed.
func filterCCSupportedConfigs(src map[string]*string) map[string]string {
	out := make(map[string]string, len(src))
	for k, v := range src {
		if _, ok := CCSupportedTopicConfigs[k]; !ok {
			continue
		}
		if v == nil {
			continue
		}
		out[k] = *v
	}
	return out
}

// GenerateNewTopic produces an `hclwrite.Block` for a `confluent_kafka_topic`
// resource. Source configs are filtered through the CC allow-list; partitions
// are emitted verbatim. The credentials block references the cluster-scoped API
// key/secret variables provided by the migrate-topics variables.tf.
func GenerateNewTopic(tfResourceName, topicName string, partitions int, srcConfigs map[string]*string, clusterId, clusterRestEndpoint string) *hclwrite.Block {
	topicBlock := hclwrite.NewBlock("resource", []string{"confluent_kafka_topic", tfResourceName})
	body := topicBlock.Body()

	kafkaClusterBlock := hclwrite.NewBlock("kafka_cluster", nil)
	kafkaClusterBlock.Body().SetAttributeValue("id", cty.StringVal(clusterId))
	body.AppendBlock(kafkaClusterBlock)
	body.AppendNewline()

	body.SetAttributeValue("topic_name", cty.StringVal(topicName))
	body.SetAttributeValue("partitions_count", cty.NumberIntVal(int64(partitions)))
	body.SetAttributeValue("rest_endpoint", cty.StringVal(clusterRestEndpoint))

	configs := filterCCSupportedConfigs(srcConfigs)
	if len(configs) > 0 {
		ctyConfigs := make(map[string]cty.Value, len(configs))
		for k, v := range configs {
			ctyConfigs[k] = cty.StringVal(v)
		}
		body.SetAttributeValue("config", cty.ObjectVal(ctyConfigs))
	}

	body.AppendNewline()
	credentialsBlock := hclwrite.NewBlock("credentials", nil)
	credentialsBlock.Body().SetAttributeRaw("key", utils.TokensForResourceReference("var.confluent_cloud_cluster_api_key"))
	credentialsBlock.Body().SetAttributeRaw("secret", utils.TokensForResourceReference("var.confluent_cloud_cluster_api_secret"))
	body.AppendBlock(credentialsBlock)

	return topicBlock
}
