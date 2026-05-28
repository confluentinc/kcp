// Package build_info exposes ldflag-injected build metadata (Version, Commit,
// Date) and small derived helpers (IsDev, DocsURL) that depend on it.
package build_info

import (
	"runtime/debug"
	"strings"
)

const (
	DefaultDevVersion = "0.0.0-localdev"

	docsSiteBase = "https://confluentinc.github.io/kcp"

	kcpModulePath = "github.com/confluentinc/kcp"
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

// ResolvedVersion returns the kcp version best identifying the running binary.
// Official kcp builds set Version via ldflags; for those it simply returns
// Version. Consumers that import this module as a Go library (where ldflags
// don't apply) get the kcp version recorded in their own binary's build info —
// either as the main module (e.g. `go install github.com/confluentinc/kcp`)
// or as a dependency (e.g. cc-growth-service importing pkg/lib). Falls back
// to Version (typically DefaultDevVersion) when build info is unavailable.
func ResolvedVersion() string {
	info, _ := debug.ReadBuildInfo()
	return resolveVersion(Version, info)
}

// resolveVersion is the pure, testable core of ResolvedVersion.
func resolveVersion(ldflagVersion string, info *debug.BuildInfo) string {
	if !isDev(ldflagVersion) {
		return ldflagVersion
	}
	if info == nil {
		return ldflagVersion
	}
	if info.Main.Path == kcpModulePath {
		if v := info.Main.Version; v != "" && v != "(devel)" {
			return v
		}
	}
	for _, dep := range info.Deps {
		if dep != nil && dep.Path == kcpModulePath && dep.Version != "" {
			return dep.Version
		}
	}
	return ldflagVersion
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
