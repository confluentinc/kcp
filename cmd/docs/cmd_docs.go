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
		Long: "Print the documentation site URL matching the running kcp binary's version.\n" +
			"Development builds resolve to the 'dev' alias; released builds resolve to their vX.Y.Z subdirectory.",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Println(build_info.DocsURL())
		},
	}
}
