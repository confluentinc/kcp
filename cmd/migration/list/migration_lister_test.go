package list

import (
	"testing"

	"github.com/fatih/color"
	"github.com/stretchr/testify/assert"
)

func TestGetStatusColor(t *testing.T) {
	ml := &MigrationLister{}

	// The new pause stage colors like the other fenced-family states.
	assert.Equal(t, color.New(color.FgYellow), ml.getStatusColor("offset_sync_paused"),
		"offset_sync_paused should color like fenced/fence_verified")

	// Unknown states keep the white default.
	assert.Equal(t, color.New(color.FgWhite), ml.getStatusColor("some_future_state"))
}
