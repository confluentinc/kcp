// Package registry decouples create-asset subcommand membership from a central
// list. Each subcommand package registers its constructor from an init(), and
// cmd_create_asset.go builds the command tree from whatever registered.
//
// Edition gating is achieved by controlling which subcommand packages are
// imported (see register_base.go / register_full.go): an unimported package's
// init() never runs, so its command never registers — and, because Go only
// compiles imported packages, its code is absent from the binary entirely.
package registry

import (
	"sort"

	"github.com/spf13/cobra"
)

// ctors holds the registered subcommand constructors. Populated from package
// init()s, which run single-threaded before main, so no locking is needed.
var ctors []func() *cobra.Command

// Register records a subcommand constructor. Call it from a package init().
func Register(f func() *cobra.Command) {
	ctors = append(ctors, f)
}

// Commands invokes every registered constructor and returns the commands sorted
// by Use, so help output ordering is deterministic regardless of init() order.
func Commands() []*cobra.Command {
	cmds := make([]*cobra.Command, 0, len(ctors))
	for _, f := range ctors {
		cmds = append(cmds, f())
	}
	sort.Slice(cmds, func(i, j int) bool {
		return cmds[i].Use < cmds[j].Use
	})
	return cmds
}
