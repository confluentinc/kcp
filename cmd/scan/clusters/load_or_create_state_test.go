package clusters

import (
	"os"
	"testing"

	"github.com/confluentinc/kcp/internal/build_info"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadOrCreateState_NewFile_StampsVersion(t *testing.T) {
	// Use a path that does not exist to trigger new state creation
	path := t.TempDir() + "/kcp-state.json"

	state, err := loadOrCreateState(path)

	require.NoError(t, err)
	require.NotNil(t, state)
	assert.Equal(t, build_info.Version, state.KcpBuildInfo.Version, "version should be stamped from running binary")
	assert.Equal(t, build_info.Commit, state.KcpBuildInfo.Commit, "commit should be stamped from running binary")
	assert.Equal(t, build_info.Date, state.KcpBuildInfo.Date, "date should be stamped from running binary")
}

func TestLoadOrCreateState_NewFile_InitialisesRequiredFields(t *testing.T) {
	path := t.TempDir() + "/kcp-state.json"

	state, err := loadOrCreateState(path)

	require.NoError(t, err)
	require.NotNil(t, state.MSKSources, "MSKSources should be initialised")
	require.NotNil(t, state.OSKSources, "OSKSources should be initialised")
	require.NotNil(t, state.SchemaRegistries, "SchemaRegistries should be initialised")
	assert.Empty(t, state.MSKSources.Regions)
	assert.Empty(t, state.OSKSources.Clusters)
}

func TestLoadOrCreateState_ExistingFile_LoadsState(t *testing.T) {
	// Write a valid state file and confirm it is loaded correctly
	tmpFile, err := os.CreateTemp("", "kcp-state-*.json")
	require.NoError(t, err)
	defer func() { _ = os.Remove(tmpFile.Name()) }()

	_, err = tmpFile.WriteString(`{"kcp_build_info":{"version":"` + build_info.Version + `","commit":"abc","date":"2024-01-01"}}`)
	require.NoError(t, err)
	require.NoError(t, tmpFile.Close())

	state, err := loadOrCreateState(tmpFile.Name())

	require.NoError(t, err)
	require.NotNil(t, state)
	assert.Equal(t, build_info.Version, state.KcpBuildInfo.Version)
}

func TestLoadOrCreateState_CorruptFile_ReturnsError(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "kcp-state-*.json")
	require.NoError(t, err)
	defer func() { _ = os.Remove(tmpFile.Name()) }()

	_, err = tmpFile.WriteString("not valid json {{{")
	require.NoError(t, err)
	require.NoError(t, tmpFile.Close())

	_, err = loadOrCreateState(tmpFile.Name())

	assert.Error(t, err, "corrupt state file should return error")
}
