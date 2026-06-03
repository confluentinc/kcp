//go:build gov

package build_info

// Mode is the binary's edition, derived from the build tag (no ldflag), so the
// reported edition can never disagree with what is compiled in. Presence of the
// `gov` tag yields the slimmed `gov` edition (kcp-lite).
const Mode = "gov"
