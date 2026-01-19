package build_info

const (
	DefaultDevVersion = "0.0.0-localdev"
)

// Build information variables - set via ldflags during build
var (
	Version = DefaultDevVersion
	Commit  = "unknown"
	Date    = "unknown"
)
