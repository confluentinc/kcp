package migration

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// R1: the command is registered alongside init, execute, lag-check and list, and
// so appears in `kcp migration --help`.
func TestMigrationCmd_RegistersTxnDiscovery(t *testing.T) {
	cmd := NewMigrationCmd()

	var names []string
	for _, sub := range cmd.Commands() {
		names = append(names, sub.Name())
	}
	require.Contains(t, names, "txn-discovery")

	sub, _, err := cmd.Find([]string{"txn-discovery"})
	require.NoError(t, err)
	assert.NotEmpty(t, sub.Short, "the subcommand needs a Short so it renders in the parent's help")
}
