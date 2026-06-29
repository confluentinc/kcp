package list

import (
	"fmt"

	"github.com/confluentinc/kcp/internal/services/cutover"
	"github.com/confluentinc/kcp/internal/utils"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

var (
	cutoverStateFile string
)

func NewCutoverListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List all cutovers from the cutover state file",
		Long:  "Display all cutovers from the cutover state file in a human-readable format, showing cutover IDs, status, gateway configuration, and topics.",
		Example: `  # Default state file
  kcp cutover list

  # Specific state file
  kcp cutover list --cutover-state-file /path/to/cutover-state.json`,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		PreRunE:       preRunCutoverList,
		RunE:          runCutoverList,
	}

	optionalFlags := pflag.NewFlagSet("optional", pflag.ExitOnError)
	optionalFlags.SortFlags = false
	optionalFlags.StringVar(&cutoverStateFile, "cutover-state-file", "cutover-state.json", "The path to the cutover state file to read.")
	cmd.Flags().AddFlagSet(optionalFlags)

	cmd.SetUsageFunc(func(c *cobra.Command) error {
		fmt.Printf("%s\n\n", c.Short)
		fmt.Printf("Optional:\n%s\n", optionalFlags.FlagUsages())
		fmt.Println("All flags can be provided via environment variables (uppercase, with underscores).")
		return nil
	})

	return cmd
}

func preRunCutoverList(cmd *cobra.Command, args []string) error {
	return utils.BindEnvToFlags(cmd)
}

func runCutoverList(cmd *cobra.Command, args []string) error {
	// Load cutover state (following KCP pattern)
	state, err := cutover.NewCutoverStateFromFile(cutoverStateFile)
	if err != nil {
		return fmt.Errorf("failed to load cutover state file %q: %w\nEnsure the file exists or run 'kcp cutover init' to create a new cutover", cutoverStateFile, err)
	}

	opts := CutoverListerOpts{
		CutoverStateFile: cutoverStateFile,
		CutoverState:     *state,
	}

	lister := NewCutoverLister(opts)
	return lister.Run()
}
