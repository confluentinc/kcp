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

// TestGetStatusColor_EveryState pins the color of every known lifecycle state so
// a recoloring regression (e.g. moving fence_verified out of the fenced-family
// yellow, or dropping the bold on switched) can't slip through unnoticed.
func TestGetStatusColor_EveryState(t *testing.T) {
	ml := &MigrationLister{}

	tests := []struct {
		state string
		want  *color.Color
	}{
		{"uninitialized", color.New(color.FgYellow)},
		{"initialized", color.New(color.FgCyan)},
		{"lags_ok", color.New(color.FgCyan)},
		{"fenced", color.New(color.FgYellow)},
		{"offset_sync_paused", color.New(color.FgYellow)},
		{"fence_verified", color.New(color.FgYellow)},
		{"promoted", color.New(color.FgGreen)},
		{"switched", color.New(color.FgGreen, color.Bold)},
		{"some_future_state", color.New(color.FgWhite)},
		{"", color.New(color.FgWhite)},
	}

	for _, tt := range tests {
		t.Run(tt.state, func(t *testing.T) {
			assert.Equal(t, tt.want, ml.getStatusColor(tt.state))
		})
	}
}
