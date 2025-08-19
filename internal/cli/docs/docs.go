package docs

import (
	"bytes"
	"fmt"
	"os"
	"slices"
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
	orderedCommands := getOrderedCommands(root)

	for _, subCmd := range orderedCommands {
		generateCommandDocs(subCmd, &output, 0)
	}

	return os.WriteFile("README.md", []byte(output.String()), 0644)
}

func generateCommandDocs(cmd *cobra.Command, output *strings.Builder, depth int) {
	// Create heading based on depth
	heading := strings.Repeat("#", depth+2) // h2, h3, h4, etc.

	commandPath := getCommandPath(cmd)

	output.WriteString(heading + " `" + commandPath + "`\n\n")

	// Check if command has documentation content in annotations
	if docContent, exists := cmd.Annotations["prerequisites"]; exists {
		fmt.Println("found prerequisites", docContent, "hhh")
		output.WriteString("Prerequisites\n\n")
		output.WriteString(docContent)
		output.WriteString("\n\n")
	}

	// Show help output for runnable commands
	if cmd.Runnable() {
		helpOutput := getCommandHelp(cmd)
		if helpOutput != "" {
			output.WriteString("```\n")
			output.WriteString(helpOutput)
			output.WriteString("\n```\n\n")
		}

		output.WriteString("---\n\n")
	}

	// Recurse for subcommands
	for _, subCmd := range cmd.Commands() {
		generateCommandDocs(subCmd, output, depth+1)
	}
}

// Helper to build full command path
func getCommandPath(cmd *cobra.Command) string {
	parts := []string{}
	current := cmd

	for current != nil && current.Name() != "" {
		parts = append([]string{current.Name()}, parts...)
		current = current.Parent()
	}

	return strings.Join(parts, " ")
}

func getCommandHelp(cmd *cobra.Command) string {
	var buf bytes.Buffer

	// Set the command's output to our buffer
	cmd.SetOut(&buf)

	// Call the usage function (which now writes to c.OutOrStdout())
	cmd.Usage()

	// Reset output back to default
	cmd.SetOut(nil)

	return buf.String()
}

func getOrderedCommands(root *cobra.Command) []*cobra.Command {
	commandOrder := []string{
		"init",
		"scan",
		"report",
		"create-asset",
	}

	cmdMap := make(map[string]*cobra.Command)
	for _, cmd := range root.Commands() {
		if !isBuiltInCommand(cmd.Name()) {
			cmdMap[cmd.Name()] = cmd
		}
	}

	var orderedCommands []*cobra.Command
	for _, cmdName := range commandOrder {
		if cmd, exists := cmdMap[cmdName]; exists {
			orderedCommands = append(orderedCommands, cmd)
		}
	}

	return orderedCommands
}

func isBuiltInCommand(cmdName string) bool {
	builtInCommands := []string{
		"help",
		"completion",
		"docs",
		"version",
	}

	return slices.Contains(builtInCommands, cmdName)
}
