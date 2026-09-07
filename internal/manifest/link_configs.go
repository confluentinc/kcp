package manifest

import (
	"encoding/json"
	"strconv"
)

// Managed link-config keys (set from typed clusterLink fields). LinkConfigPrefix
// is cluster.link.prefix — NOT link.prefix, which is a read-only/derived key that
// silently ignores writes (verified against cp-server 8.1.2).
const (
	LinkConfigPrefix             = "cluster.link.prefix"
	LinkConfigOffsetSyncEnable   = "consumer.offset.sync.enable"
	LinkConfigOffsetSyncMs       = "consumer.offset.sync.ms"
	LinkConfigOffsetGroupFilters = "consumer.offset.group.filters"
	LinkConfigTopicConfigSyncMs  = "topic.config.sync.ms"
)

// ManagedLinkConfigKeys are the link-config keys owned by typed clusterLink
// fields; the free-form Configs escape hatch must not contain them.
var ManagedLinkConfigKeys = []string{
	LinkConfigPrefix, LinkConfigOffsetSyncEnable, LinkConfigOffsetSyncMs,
	LinkConfigOffsetGroupFilters, LinkConfigTopicConfigSyncMs,
}

// allGroupsFilter is the default consumer.offset.group.filters when offset sync
// is enabled but no filters are given: include every consumer group (an empty
// filter syncs none, which would defeat offset migration).
const allGroupsFilter = `{"groupFilters":[{"name":"*","patternType":"LITERAL","filterType":"INCLUDE"}]}`

// offsetSyncEnabled reports the resolved enable value: true by default (nil block
// or nil Enable), the explicit value otherwise.
func (cl *ClusterLink) offsetSyncEnabled() bool {
	if cl.ConsumerOffsetSync == nil || cl.ConsumerOffsetSync.Enable == nil {
		return true
	}
	return *cl.ConsumerOffsetSync.Enable
}

// ResolvedLinkConfigs returns the link-config map this section implies: typed
// fields with migration defaults applied, group filters JSON-encoded, merged with
// the free-form Configs escape hatch. It is the desired-config source of truth for
// both the create body and drift comparison. Overlap between Configs and a managed
// key is rejected by Validate(), so the merge here never conflicts.
func (cl *ClusterLink) ResolvedLinkConfigs() (map[string]string, error) {
	out := map[string]string{}

	if cl.Prefix != "" {
		out[LinkConfigPrefix] = cl.Prefix
	}

	enabled := cl.offsetSyncEnabled()
	out[LinkConfigOffsetSyncEnable] = boolStr(enabled)
	if cos := cl.ConsumerOffsetSync; cos != nil {
		if cos.IntervalMs > 0 {
			out[LinkConfigOffsetSyncMs] = strconv.Itoa(cos.IntervalMs)
		}
		if len(cos.GroupFilters) > 0 {
			j, err := marshalGroupFilters(cos.GroupFilters)
			if err != nil {
				return nil, err
			}
			out[LinkConfigOffsetGroupFilters] = j
		}
	}
	// Default to include-all only when sync is on and no explicit filters set.
	if enabled {
		if _, ok := out[LinkConfigOffsetGroupFilters]; !ok {
			out[LinkConfigOffsetGroupFilters] = allGroupsFilter
		}
	}

	if cl.TopicConfigSync != nil && cl.TopicConfigSync.IntervalMs > 0 {
		out[LinkConfigTopicConfigSyncMs] = strconv.Itoa(cl.TopicConfigSync.IntervalMs)
	}

	for k, v := range cl.Configs {
		out[k] = v
	}
	return out, nil
}

// marshalGroupFilters encodes filters as {"groupFilters":[...]} with the server's
// field names (name/patternType/filterType).
func marshalGroupFilters(filters []GroupFilter) (string, error) {
	type gf struct {
		Name        string `json:"name"`
		PatternType string `json:"patternType"`
		FilterType  string `json:"filterType"`
	}
	wrapper := struct {
		GroupFilters []gf `json:"groupFilters"`
	}{}
	for _, f := range filters {
		wrapper.GroupFilters = append(wrapper.GroupFilters, gf(f))
	}
	b, err := json.Marshal(wrapper)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
