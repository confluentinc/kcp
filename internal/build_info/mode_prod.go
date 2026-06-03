//go:build !gov

package build_info

// Mode is the binary's edition, derived from the build tag (no ldflag), so the
// reported edition can never disagree with what is compiled in. Absence of the
// `gov` tag yields the full `prod` edition.
const Mode = "prod"
