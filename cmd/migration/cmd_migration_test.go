package migration

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMigrationCmd_IsHiddenDeprecationStub(t *testing.T) {
	cmd := NewMigrationCmd()

	assert.Equal(t, "migration", cmd.Use)
	assert.True(t, cmd.Hidden, "deprecated migration command must be hidden from help")
	assert.Empty(t, cmd.Commands(), "deprecation stub must not register the old subcommands")
}

func TestMigrationCmd_AlwaysPointsToCutover(t *testing.T) {
	// Old invocations — bare, with a subcommand token, and with old flags —
	// must all route to the deprecation notice (not an unknown-command/flag error).
	cases := [][]string{
		{},
		{"init"},
		{"execute", "--migration-id", "x", "--migration-state-file", "y"},
		{"lag-check"},
		{"list"},
	}

	for _, args := range cases {
		cmd := NewMigrationCmd()
		cmd.SetArgs(args)
		err := cmd.Execute()

		require.Error(t, err, "args %v should return a deprecation error (non-zero exit)", args)
		msg := err.Error()
		assert.Contains(t, msg, "kcp cutover", "deprecation message should point users to 'kcp cutover' (args %v)", args)
		assert.True(t, strings.Contains(msg, "renamed") || strings.Contains(msg, "deprecat"),
			"deprecation message should explain the rename (args %v): %q", args, msg)
	}
}
