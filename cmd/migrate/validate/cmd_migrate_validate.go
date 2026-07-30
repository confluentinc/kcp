package validate

import (
	"fmt"
	"os"

	"github.com/confluentinc/kcp/internal/manifest"
	"github.com/confluentinc/kcp/internal/utils"
	"github.com/spf13/cobra"
)

func NewMigrateValidateCmd() *cobra.Command {
	var file string

	cmd := &cobra.Command{
		Use:   "validate",
		Short: "Validate a migration manifest",
		// We return errors for a non-zero exit; silence Cobra's usage dump and
		// duplicate error print so validation output stays readable.
		SilenceUsage:  true,
		SilenceErrors: true,
		PreRunE: func(cmd *cobra.Command, args []string) error {
			return utils.BindEnvToFlags(cmd)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			data, err := os.ReadFile(file)
			if err != nil {
				return fmt.Errorf("reading manifest: %w", err)
			}
			m, err := manifest.Parse(data)
			if err != nil {
				return err
			}
			if errs := m.Validate(); len(errs) > 0 {
				for _, e := range errs {
					_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "✖ %v\n", e)
				}
				return fmt.Errorf("manifest is invalid: %d problem(s) found", len(errs))
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "✓ %s is valid\n", file)
			return nil
		},
	}

	cmd.Flags().StringVarP(&file, "file", "f", "", "Path to the migration manifest (required)")
	_ = cmd.MarkFlagRequired("file")
	return cmd
}
