package types

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestState_LegacyOSKKeyIgnored documents and guards the hard-break contract:
// a state file written by an older binary used the retired "osk_sources" key.
// Loading it must NOT panic and must yield no Apache Kafka sources — the data is
// silently dropped (Go ignores unknown JSON keys), never migrated. This guards
// the renamed json tag against accidental re-aliasing back to the old key.
//
// Property holds by construction; this is a regression guard, not a RED-first
// feature test.
func TestState_LegacyOSKKeyIgnored(t *testing.T) {
	legacy := []byte(`{"osk_sources":{"clusters":[{"id":"old-cluster"}]}}`)

	var s State
	require.NotPanics(t, func() {
		require.NoError(t, json.Unmarshal(legacy, &s))
	})
	assert.Nil(t, s.ApacheKafkaSources, "retired osk_sources key must not populate ApacheKafkaSources")
}

// TestState_ApacheKafkaKeyRoundTrips asserts the post-rename state contract
// serializes under "apache_kafka_sources" and never the retired "osk_sources".
func TestState_ApacheKafkaKeyRoundTrips(t *testing.T) {
	s := State{ApacheKafkaSources: &ApacheKafkaSourcesState{
		Clusters: []ApacheKafkaDiscoveredCluster{{ID: "c1"}},
	}}

	b, err := json.Marshal(s)
	require.NoError(t, err)
	assert.Contains(t, string(b), `"apache_kafka_sources"`)
	assert.NotContains(t, string(b), `"osk_sources"`)

	var got State
	require.NoError(t, json.Unmarshal(b, &got))
	require.NotNil(t, got.ApacheKafkaSources)
	require.Len(t, got.ApacheKafkaSources.Clusters, 1)
	assert.Equal(t, "c1", got.ApacheKafkaSources.Clusters[0].ID)
}
