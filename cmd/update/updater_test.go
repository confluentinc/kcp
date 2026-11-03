package update

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInContainer(t *testing.T) {
	t.Setenv(containerEnv, "true")
	assert.True(t, inContainer())

	t.Setenv(containerEnv, "")
	assert.False(t, inContainer())

	t.Setenv(containerEnv, "false")
	assert.False(t, inContainer())
}

func TestContainerUpdate_CheckOnlyPointsToNewImage(t *testing.T) {
	msg, err := containerUpdate("0.10.3", true)
	require.NoError(t, err)
	assert.Contains(t, msg, "docker pull confluentinc/kcp:0.10.3")
	assert.NotContains(t, msg, "sudo")
}

func TestContainerUpdate_UpdateFailsWithPullInstruction(t *testing.T) {
	msg, err := containerUpdate("0.10.3", false)
	require.Error(t, err)
	assert.Empty(t, msg)
	assert.Contains(t, err.Error(), "docker pull confluentinc/kcp:0.10.3")
	assert.NotContains(t, err.Error(), "sudo")
}

func TestContainerUpdate_MentionsImageMayLagRelease(t *testing.T) {
	msg, err := containerUpdate("0.10.3", true)
	require.NoError(t, err)
	assert.Contains(t, msg, "try again later")

	_, err = containerUpdate("0.10.3", false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "try again later")
}
