package docs

import (
	"fmt"

	"github.com/confluentinc/kcp/internal/build_info"
	"github.com/spf13/cobra"
)

func NewDocsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "docs",
		Short: "Show the documentation URL for this build",
		Long: `Print the documentation site URL matching the running kcp binary's version.
Development builds resolve to the 'dev' alias; released builds resolve to their vX.Y.Z subdirectory.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			_, err := fmt.Fprintln(cmd.OutOrStdout(), build_info.DocsURL())
			return err
		},
	}
}
