package version

import (
	"fmt"

	"github.com/confluentinc/kcp/internal/build_info"
	"github.com/spf13/cobra"
)

func NewVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "version",
		Short:   "Show version information",
		Long:    "Display version, commit, and build date information",
		Example: "  kcp version",
		RunE: func(cmd *cobra.Command, args []string) error {
			_, err := fmt.Fprintf(cmd.OutOrStdout(),
				"Version: %s\nCommit:  %s\nDate:    %s\nEdition: %s\n",
				build_info.Version, build_info.Commit, build_info.Date, build_info.Mode)
			return err
		},
	}
}
