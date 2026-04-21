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

// DocsURL returns the versioned documentation URL matching the running binary.
// Development builds resolve to the "dev" alias (tip of main); released builds
// resolve to the matching vX.Y.Z subdirectory.
func DocsURL() string {
	v := strings.TrimPrefix(Version, "v")
	if v == "" || v == "dev" || v == DefaultDevVersion {
		return docsSiteBase + "/dev/"
	}
	return docsSiteBase + "/" + v + "/"
}
