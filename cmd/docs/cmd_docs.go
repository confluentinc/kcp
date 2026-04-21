package docs

import (
	"fmt"
	"os/exec"
	"runtime"

	"github.com/confluentinc/kcp/internal/build_info"
	"github.com/spf13/cobra"
)

func NewDocsCmd() *cobra.Command {
	var open bool

	cmd := &cobra.Command{
		Use:   "docs",
		Short: "Show the documentation URL for this build",
		Long: "Print (or open) the documentation site URL matching the running kcp binary's version.\n" +
			"Development builds resolve to the 'dev' alias; released builds resolve to their vX.Y.Z subdirectory.",
		RunE: func(cmd *cobra.Command, args []string) error {
			url := build_info.DocsURL()
			if !open {
				fmt.Println(url)
				return nil
			}
			fmt.Println(url)
			return openInBrowser(url)
		},
	}

	cmd.Flags().BoolVar(&open, "open", false, "Open the docs URL in the default browser")
	return cmd
}

func openInBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to open browser for %s: %w", url, err)
	}
	return nil
}
