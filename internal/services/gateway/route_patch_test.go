package gateway

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFragmentValue(t *testing.T) {
	v, err := FragmentValue([]byte("fence:\n  scope: all\n  errorCode: 42\n"), "fence")
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"scope": "all", "errorCode": uint64(42)}, v)

	_, err = FragmentValue([]byte("streamingDomain: cp-b\n"), "fence")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no top-level fence key")

	_, err = FragmentValue([]byte("fence: null\n"), "fence")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "fence key is null")
}
