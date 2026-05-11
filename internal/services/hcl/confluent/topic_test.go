package confluent

import (
	"regexp"
	"strings"
	"testing"

	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func strPtr(s string) *string { return &s }

func renderBlock(t *testing.T, block *hclwrite.Block) string {
	t.Helper()
	f := hclwrite.NewEmptyFile()
	f.Body().AppendBlock(block)
	return string(f.Bytes())
}

// normalizeSpaces collapses runs of spaces to a single space so assertions
// don't have to track hclwrite's column-alignment of consecutive attribute
// lines (it pads `=` signs to a common column).
var multiSpaceRe = regexp.MustCompile(` +`)

func normalizeSpaces(s string) string {
	return multiSpaceRe.ReplaceAllString(s, " ")
}

func TestCCSupportedTopicConfigs_HasExpectedEntries(t *testing.T) {
	t.Parallel()

	expected := []string{
		"cleanup.policy",
		"delete.retention.ms",
		"max.message.bytes",
		"max.compaction.lag.ms",
		"message.timestamp.difference.max.ms",
		"message.timestamp.before.max.ms",
		"message.timestamp.after.max.ms",
		"message.timestamp.type",
		"min.compaction.lag.ms",
		"min.insync.replicas",
		"retention.bytes",
		"retention.ms",
		"segment.bytes",
		"segment.ms",
		"confluent.key.schema.validation",
		"confluent.value.schema.validation",
		"confluent.key.subject.name.strategy",
		"confluent.value.subject.name.strategy",
	}

	for _, k := range expected {
		_, ok := CCSupportedTopicConfigs[k]
		assert.True(t, ok, "expected allow-list to contain %q", k)
	}
	assert.Len(t, CCSupportedTopicConfigs, len(expected), "allow-list should have exactly %d entries", len(expected))
}

func TestCCSupportedTopicConfigs_DoesNotContainReplicationFactor(t *testing.T) {
	t.Parallel()
	_, ok := CCSupportedTopicConfigs["replication.factor"]
	assert.False(t, ok, "replication.factor must NOT be in the allow-list — CC manages it")
}

func TestFilterCCSupportedConfigs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    map[string]*string
		expected map[string]string
	}{
		{
			name: "allow-listed configs pass through",
			input: map[string]*string{
				"cleanup.policy": strPtr("compact"),
				"retention.ms":   strPtr("604800000"),
			},
			expected: map[string]string{
				"cleanup.policy": "compact",
				"retention.ms":   "604800000",
			},
		},
		{
			name: "non-allow-listed configs are dropped",
			input: map[string]*string{
				"cleanup.policy":                 strPtr("compact"),
				"unclean.leader.election.enable": strPtr("true"),
				"compression.type":               strPtr("snappy"),
			},
			expected: map[string]string{
				"cleanup.policy": "compact",
			},
		},
		{
			name: "replication.factor is dropped without special handling",
			input: map[string]*string{
				"replication.factor": strPtr("3"),
				"cleanup.policy":     strPtr("delete"),
			},
			expected: map[string]string{
				"cleanup.policy": "delete",
			},
		},
		{
			name: "nil values are skipped (no empty-string artifacts)",
			input: map[string]*string{
				"cleanup.policy": strPtr("compact"),
				"retention.ms":   nil,
			},
			expected: map[string]string{
				"cleanup.policy": "compact",
			},
		},
		{
			name:     "empty input returns empty",
			input:    map[string]*string{},
			expected: map[string]string{},
		},
		{
			name: "all confluent.*.schema.validation keys preserved",
			input: map[string]*string{
				"confluent.key.schema.validation":       strPtr("true"),
				"confluent.value.schema.validation":     strPtr("false"),
				"confluent.key.subject.name.strategy":   strPtr("TopicNameStrategy"),
				"confluent.value.subject.name.strategy": strPtr("TopicNameStrategy"),
			},
			expected: map[string]string{
				"confluent.key.schema.validation":       "true",
				"confluent.value.schema.validation":     "false",
				"confluent.key.subject.name.strategy":   "TopicNameStrategy",
				"confluent.value.subject.name.strategy": "TopicNameStrategy",
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := filterCCSupportedConfigs(tt.input)
			assert.Equal(t, tt.expected, got)
		})
	}
}

func TestGenerateNewTopic_BasicShape(t *testing.T) {
	t.Parallel()

	configs := map[string]*string{
		"cleanup.policy": strPtr("compact"),
		"retention.ms":   strPtr("604800000"),
	}

	block := GenerateNewTopic("orders", "orders", 6, configs, "lkc-xyz", "https://cc.example.com:443")
	out := renderBlock(t, block)
	norm := normalizeSpaces(out)

	assert.Contains(t, norm, `resource "confluent_kafka_topic" "orders"`)
	assert.Contains(t, norm, `topic_name = "orders"`)
	assert.Contains(t, norm, `partitions_count = 6`)
	assert.Contains(t, norm, `rest_endpoint = "https://cc.example.com:443"`)
	assert.Contains(t, norm, `kafka_cluster {`)
	assert.Contains(t, norm, `id = "lkc-xyz"`)
	assert.Contains(t, norm, `credentials {`)
	assert.Contains(t, norm, `key = var.confluent_cloud_cluster_api_key`)
	assert.Contains(t, norm, `secret = var.confluent_cloud_cluster_api_secret`)

	assert.Contains(t, norm, `"cleanup.policy" = "compact"`)
	assert.Contains(t, norm, `"retention.ms" = "604800000"`)
}

func TestGenerateNewTopic_FiltersNonAllowListedConfigs(t *testing.T) {
	t.Parallel()

	configs := map[string]*string{
		"cleanup.policy":                 strPtr("compact"),
		"unclean.leader.election.enable": strPtr("true"),
		"compression.type":               strPtr("snappy"),
	}

	block := GenerateNewTopic("orders", "orders", 6, configs, "lkc-xyz", "https://cc.example.com:443")
	out := renderBlock(t, block)

	assert.Contains(t, out, `"cleanup.policy"`)
	assert.NotContains(t, out, "unclean.leader.election.enable")
	assert.NotContains(t, out, "compression.type")
}

func TestGenerateNewTopic_NeverEmitsReplicationFactor(t *testing.T) {
	t.Parallel()

	configs := map[string]*string{
		"replication.factor": strPtr("3"),
		"cleanup.policy":     strPtr("delete"),
	}

	block := GenerateNewTopic("orders", "orders", 6, configs, "lkc-xyz", "https://cc.example.com:443")
	out := renderBlock(t, block)

	assert.NotContains(t, out, "replication.factor")
	assert.Contains(t, out, `"cleanup.policy"`)
}

func TestGenerateNewTopic_EmptyConfigsOmitsConfigBlock(t *testing.T) {
	t.Parallel()

	block := GenerateNewTopic("orders", "orders", 6, nil, "lkc-xyz", "https://cc.example.com:443")
	out := renderBlock(t, block)

	assert.NotContains(t, out, "config =")
	assert.Contains(t, normalizeSpaces(out), `topic_name = "orders"`)
}

func TestGenerateNewTopic_NilValuesAreSkipped(t *testing.T) {
	t.Parallel()

	configs := map[string]*string{
		"cleanup.policy": strPtr("compact"),
		"retention.ms":   nil,
	}

	block := GenerateNewTopic("orders", "orders", 6, configs, "lkc-xyz", "https://cc.example.com:443")
	out := renderBlock(t, block)

	assert.Contains(t, out, `"cleanup.policy"`)
	// retention.ms must not be emitted as a key — neither bare nor with empty value
	assert.NotContains(t, out, `"retention.ms"`)
}

func TestGenerateNewTopic_PartitionsCountVerbatim(t *testing.T) {
	t.Parallel()

	for _, n := range []int{1, 6, 100} {
		block := GenerateNewTopic("orders", "orders", n, nil, "lkc-xyz", "https://cc.example.com:443")
		norm := normalizeSpaces(renderBlock(t, block))
		require.Contains(t, norm, "partitions_count = ")
		assert.True(t, strings.Contains(norm, "partitions_count = "+itoa(n)), "missing partitions_count = %d in output:\n%s", n, norm)
	}
}

func TestGenerateNewTopic_FormatsToValidHCL(t *testing.T) {
	t.Parallel()

	configs := map[string]*string{
		"cleanup.policy": strPtr("compact"),
		"retention.ms":   strPtr("604800000"),
	}

	block := GenerateNewTopic("orders_dlq", "orders.dlq", 3, configs, "lkc-xyz", "https://cc.example.com:443")
	out := renderBlock(t, block)

	// hclwrite.Format should produce identical output when given an already-formatted block.
	formatted := string(hclwrite.Format([]byte(out)))
	assert.Equal(t, out, formatted, "GenerateNewTopic output should be canonical HCL")

	// Topic name containing a dot is emitted with quotes preserved.
	assert.Contains(t, normalizeSpaces(out), `topic_name = "orders.dlq"`)
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := false
	if i < 0 {
		neg = true
		i = -i
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}
