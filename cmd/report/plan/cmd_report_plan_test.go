package plan

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Pre-0.7 state files used `regions` at the top level instead of
// `msk_sources.regions`. migrateLegacyState rebuilds them into the
// modern shape so `kcp report plan` can still load them.
func TestMigrateLegacyState_TopLevelRegions(t *testing.T) {
	legacy := []byte(`{
		"regions": [
			{ "name": "us-east-1", "clusters": [] }
		],
		"kcp_build_info": { "version": "0.6.6" },
		"timestamp": "2026-03-12T13:24:50Z"
	}`)
	state, err := migrateLegacyState(legacy)
	require.NoError(t, err)
	require.NotNil(t, state.MSKSources)
	require.Len(t, state.MSKSources.Regions, 1)
	assert.Equal(t, "us-east-1", state.MSKSources.Regions[0].Name)
}

// Pre-0.7 state files used a flat `schema_registries: [...]` array with
// a `type` discriminator. migrateLegacyState buckets each entry into
// `confluent_schema_registry` or `aws_glue`.
func TestMigrateLegacyState_FlatSchemaRegistriesArray(t *testing.T) {
	legacy := []byte(`{
		"regions": [],
		"schema_registries": [
			{ "type": "confluent", "url": "http://sr1:8081" },
			{ "type": "glue", "name": "glue-registry-1", "arn": "arn:aws:glue:us-east-1:111:registry/glue-registry-1" }
		],
		"kcp_build_info": { "version": "0.6.6" }
	}`)
	state, err := migrateLegacyState(legacy)
	require.NoError(t, err)
	require.NotNil(t, state.SchemaRegistries)
	assert.Len(t, state.SchemaRegistries.ConfluentSchemaRegistry, 1)
	assert.Len(t, state.SchemaRegistries.AWSGlue, 1)
}

// Modern state files (no legacy keys) must not be misidentified as
// legacy — migrateLegacyState returns an error so loadState surfaces
// the original strict-decode error to the user.
func TestMigrateLegacyState_ModernShapeRejected(t *testing.T) {
	modern := []byte(`{
		"msk_sources": { "regions": [] },
		"schema_registries": { "confluent_schema_registry": [] },
		"kcp_build_info": { "version": "0.7.0" }
	}`)
	_, err := migrateLegacyState(modern)
	assert.Error(t, err, "modern shape (no legacy keys) must not match the migration path")
}

// Forward-incompatible state file (kcp >= 0.7) that happens to use a
// top-level `regions` field must NOT be silently migrated — the
// strict-decode error path is the right signal. Otherwise a future
// shape change could be quietly mangled by the legacy shim.
func TestMigrateLegacyState_PostLegacyVersionRefused(t *testing.T) {
	postLegacy := []byte(`{
		"regions": [{ "name": "us-east-1", "clusters": [] }],
		"kcp_build_info": { "version": "0.8.2" }
	}`)
	_, err := migrateLegacyState(postLegacy)
	assert.Error(t, err, "kcp >= 0.7 must refuse the pre-0.7 migration shim")
}

func TestIsPostLegacyVersion(t *testing.T) {
	cases := []struct {
		v    string
		want bool
	}{
		{"", false},
		{"0.6.6", false},
		{"0.6.99", false},
		{"0.7.0", true},
		{"0.8.2", true},
		{"1.0.0", true},
		{"v1.2.3", true},
		{"0.7.0-rc1", true},
		{"0.0.0-localdev", false},
		{"garbage", false},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, isPostLegacyVersion(c.v), c.v)
	}
}
