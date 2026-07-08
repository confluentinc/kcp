package version

import (
	"github.com/confluentinc/kcp/internal/build_info"
	"github.com/confluentinc/kcp/internal/output"
	"github.com/spf13/cobra"
)

func NewVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "version",
		Short:   "Show version information",
		Long:    "Display version, commit, and build date information",
		Example: "  kcp version",
		Run: func(cmd *cobra.Command, args []string) {
			output.Printf("Version: %s\n", build_info.Version)
			output.Printf("Commit:  %s\n", build_info.Commit)
			output.Printf("Date:    %s\n", build_info.Date)
		},
	}
}
