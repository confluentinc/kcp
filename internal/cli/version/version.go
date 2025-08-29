package version

import (
	"fmt"

	"github.com/confluentinc/kcp/internal/buildinfo"
	"github.com/spf13/cobra"
)

func NewVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Show version information",
		Long:  "Display version, commit, and build date information",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("Version: %s\n", buildinfo.Version)
			fmt.Printf("Commit:  %s\n", buildinfo.Commit)
			fmt.Printf("Date:    %s\n", buildinfo.Date)
		},
	}
}
