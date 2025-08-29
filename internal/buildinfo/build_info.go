package buildinfo

// Build information variables - set via ldflags during build
var (
	Version = "dev"
	Commit  = "unknown"
	Date    = "unknown"
)
