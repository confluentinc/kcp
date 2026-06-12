// Package build_info exposes ldflag-injected build metadata (Version, Commit,
// Date) and small derived helpers (IsDev, DocsURL) that depend on it.
package build_info

import "strings"

const (
	DefaultDevVersion = "0.0.0-localdev"

	docsSiteBase = "https://confluentinc.github.io/kcp"
)

// Build information variables - set via ldflags during build
var (
	Version = DefaultDevVersion
	Commit  = "unknown"
	Date    = "unknown"
)

// IsDev reports whether the binary is a development (non-released) build.
// Treats the Makefile default (DefaultDevVersion), the historical "dev"
// sentinel, and an unset Version all as development.
func IsDev() bool {
	return isDev(Version)
}

// isDev is the testable core used by both IsDev and DocsURL.
func isDev(v string) bool {
	v = strings.TrimPrefix(v, "v")
	return v == "" || v == "dev" || v == DefaultDevVersion
}

// DocsURL returns the versioned documentation URL matching the running binary.
// Development builds resolve to the "dev" alias (tip of main); released builds
// resolve to the matching vX.Y.Z subdirectory.
func DocsURL() string {
	return docsURLForVersion(Version)
}

// docsURLForVersion is the pure, testable core of DocsURL.
// It expects a version string as injected via ldflags; an optional leading
// "v" is stripped. Release workflows must publish mike aliases using the
// stripped (no-"v") form — e.g. `mike deploy 1.2.3`, not `v1.2.3`.
func docsURLForVersion(v string) string {
	if isDev(v) {
		return docsSiteBase + "/dev/"
	}
	return docsSiteBase + "/" + strings.TrimPrefix(v, "v") + "/"
}
