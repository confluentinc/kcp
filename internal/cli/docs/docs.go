package docs

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

func NewDocsCmd() *cobra.Command {
	docsCmd := &cobra.Command{
		Use:           "docs",
		Short:         "Generate a README.md and set of env vars to export",
		Long:          "Generates a README.md to guide usage of kcp and a script to export environment variables for various kcp commands",
		RunE:          runDocs,
		SilenceErrors: true,
	}

	docsCmd.SetUsageFunc(func(c *cobra.Command) error {
		fmt.Fprintf(c.OutOrStdout(), "%s\n\n", c.Short)
		fmt.Fprintf(c.OutOrStdout(), "All flags can be provided via environment variables (uppercase, with underscores).")
		return nil
	})

	return docsCmd
}

func runDocs(cmd *cobra.Command, args []string) error {
	root := cmd.Root()

	var output strings.Builder

	output.WriteString("# KCP CLI Documentation\n\n")

	// Get commands in desired order
	topLevelCommands := getOrderedCommands(root)

	for _, subCmd := range topLevelCommands {
		generateCommandDocs(subCmd, &output, 0)
	}

	return os.WriteFile("README.md", []byte(output.String()), 0644)
}

func getOrderedCommands(root *cobra.Command) []*cobra.Command {
	commandOrder := []string{
		"init",
		"scan",
		"report",
		"create-asset",
	}

	var orderedCommands []*cobra.Command
	for _, cmdName := range commandOrder {
		for _, cmd := range root.Commands() {
			if cmd.Name() == cmdName {
				orderedCommands = append(orderedCommands, cmd)
			}
		}
	}

	return orderedCommands
}

func generateCommandDocs(cmd *cobra.Command, output *strings.Builder, depth int) {
	// Create heading based on depth
	heading := strings.Repeat("#", depth+2) // h2, h3, h4, etc.

	commandPath := cmd.CommandPath()

	output.WriteString(heading + " `" + commandPath + "`\n\n")

	// Show help output for runnable commands
	if cmd.Runnable() {
		usage := cmd.UsageString()
		if usage != "" {
			output.WriteString("```\n")
			output.WriteString(usage)
			output.WriteString("\n```\n\n")
		}

		output.WriteString("---\n\n")
	}

	// Recurse for subcommands
	for _, subCmd := range cmd.Commands() {
		generateCommandDocs(subCmd, output, depth+1)
	}
}
