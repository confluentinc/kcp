package manifest_test

import (
	"testing"

	"github.com/confluentinc/kcp/internal/manifest"
	"github.com/stretchr/testify/require"
)

const allGroupsFilter = `{"groupFilters":[{"name":"*","patternType":"LITERAL","filterType":"INCLUDE"}]}`

func TestResolvedLinkConfigs_DefaultsOffsetSyncOn(t *testing.T) {
	cl := &manifest.ClusterLink{Name: "l"}
	got, err := cl.ResolvedLinkConfigs()
	require.NoError(t, err)
	require.Equal(t, "true", got["consumer.offset.sync.enable"])
	require.Equal(t, allGroupsFilter, got["consumer.offset.group.filters"])
	require.NotContains(t, got, "cluster.link.prefix")
	require.NotContains(t, got, "consumer.offset.sync.ms")
	require.NotContains(t, got, "topic.config.sync.ms")
}

func TestResolvedLinkConfigs_ExplicitDisable(t *testing.T) {
	no := false
	cl := &manifest.ClusterLink{Name: "l", ConsumerOffsetSync: &manifest.ConsumerOffsetSync{Enable: &no}}
	got, err := cl.ResolvedLinkConfigs()
	require.NoError(t, err)
	require.Equal(t, "false", got["consumer.offset.sync.enable"])
	require.NotContains(t, got, "consumer.offset.group.filters", "no include-all default when sync disabled")
}

func TestResolvedLinkConfigs_PrefixAndIntervals(t *testing.T) {
	cl := &manifest.ClusterLink{
		Name:               "l",
		Prefix:             "clusterA.",
		ConsumerOffsetSync: &manifest.ConsumerOffsetSync{IntervalMs: 1000},
		TopicConfigSync:    &manifest.TopicConfigSync{IntervalMs: 5000},
	}
	got, err := cl.ResolvedLinkConfigs()
	require.NoError(t, err)
	require.Equal(t, "clusterA.", got["cluster.link.prefix"])
	require.Equal(t, "1000", got["consumer.offset.sync.ms"])
	require.Equal(t, "5000", got["topic.config.sync.ms"])
	require.Equal(t, "true", got["consumer.offset.sync.enable"], "enable still defaults on")
}

func TestResolvedLinkConfigs_ExplicitGroupFilters(t *testing.T) {
	cl := &manifest.ClusterLink{Name: "l", ConsumerOffsetSync: &manifest.ConsumerOffsetSync{
		GroupFilters: []manifest.GroupFilter{
			{Name: "orders-*", PatternType: "PREFIXED", FilterType: "INCLUDE"},
			{Name: "tmp", PatternType: "LITERAL", FilterType: "EXCLUDE"},
		},
	}}
	got, err := cl.ResolvedLinkConfigs()
	require.NoError(t, err)
	require.JSONEq(t,
		`{"groupFilters":[{"name":"orders-*","patternType":"PREFIXED","filterType":"INCLUDE"},{"name":"tmp","patternType":"LITERAL","filterType":"EXCLUDE"}]}`,
		got["consumer.offset.group.filters"])
}

func TestResolvedLinkConfigs_MergesEscapeHatch(t *testing.T) {
	cl := &manifest.ClusterLink{Name: "l", Configs: map[string]string{"some.other.config": "v"}}
	got, err := cl.ResolvedLinkConfigs()
	require.NoError(t, err)
	require.Equal(t, "v", got["some.other.config"])
}
