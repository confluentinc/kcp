// Package logging provides a file-only slog sink for components that own rich,
// pre-formatted terminal output (e.g. the migration reporter) and need to
// mirror a clean, structured copy into kcp.log — without echoing to the
// console, where they already print their own formatted version.
//
// This is a deliberately narrow escape hatch. The default path for emitting a
// line is the standard slog levels (see the output-routing table in CLAUDE.md);
// reach for the file-only sink only when a component prints rich terminal
// output that cannot go through the clean slog.Info console render.
package logging

import "log/slog"

// file writes ONLY to kcp.log (no console leg). It defaults to a discard sink
// so mirror calls made before SetFile — and in unit tests — are safe no-ops.
var file = slog.New(slog.DiscardHandler)

// SetFile installs the file-only logger. Called once during logging setup.
func SetFile(l *slog.Logger) { file = l }

// File returns the file-only logger.
func File() *slog.Logger { return file }
