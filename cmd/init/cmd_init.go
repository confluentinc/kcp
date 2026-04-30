package initcmd

import (
	_ "embed"
	"errors"
	"fmt"
	"os"

	"github.com/confluentinc/kcp/internal/utils"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

//go:embed osk_credentials_template.yaml
var oskTemplate []byte

const defaultOutputPath = "osk-credentials.yaml"

var (
	outputPath string
)

func NewInitCmd() *cobra.Command {
	initCmd := &cobra.Command{
		Use:   "init",
		Short: "Scaffold an osk-credentials.yaml template for Open Source Kafka",
		Long: `Scaffold an osk-credentials.yaml template for Open Source Kafka (OSK) clusters.

This command writes a commented template that covers all five supported auth methods
(SASL/SCRAM, SASL/PLAIN, mTLS, unauthenticated TLS, unauthenticated plaintext) and the
optional Jolokia and Prometheus metrics sections. SASL/SCRAM-SHA-256 is the active
default; the other methods are present but commented out.

Edit the generated file before running 'kcp scan clusters' — at minimum, replace the
placeholder bootstrap servers and the REPLACE_ME SASL/SCRAM password.

This command is OSK only. For AWS MSK clusters, use 'kcp discover' instead — it scans
the AWS account and writes msk-credentials.yaml automatically based on the discovered
footprint.`,
		Example: `  # Write osk-credentials.yaml in the current directory
  kcp init

  # Write to a custom path + file name
  kcp init --output ./my-cluster/osk-dev-credentials.yaml`,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		PreRunE:       preRunInit,
		RunE:          runInit,
	}

	optionalFlags := pflag.NewFlagSet("optional", pflag.ExitOnError)
	optionalFlags.SortFlags = false
	optionalFlags.StringVar(&outputPath, "output", defaultOutputPath, "Path to write the osk-credentials.yaml template")
	initCmd.Flags().AddFlagSet(optionalFlags)

	initCmd.SetUsageFunc(func(c *cobra.Command) error {
		fmt.Printf("%s\n\n", c.Short)

		if usage := optionalFlags.FlagUsages(); usage != "" {
			fmt.Printf("Optional Flags:\n%s\n", usage)
		}

		fmt.Println("All flags can be provided via environment variables (uppercase, with underscores).")

		return nil
	})

	return initCmd
}

func preRunInit(cmd *cobra.Command, args []string) error {
	return utils.BindEnvToFlags(cmd)
}

func runInit(cmd *cobra.Command, args []string) error {
	f, err := os.OpenFile(outputPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("file %q already exists; remove it first if you want to regenerate", outputPath)
		}
		return fmt.Errorf("failed to open output file %q: %w", outputPath, err)
	}

	if _, err := f.Write(oskTemplate); err != nil {
		_ = f.Close()
		return fmt.Errorf("failed to write template to %q: %w", outputPath, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("failed to close %q: %w", outputPath, err)
	}

	out := cmd.OutOrStdout()
	_, _ = fmt.Fprintf(out, "✅ Wrote osk-credentials template to %s\n", outputPath)
	_, _ = fmt.Fprintln(out, "⚠️  Edit the file before scanning: replace the placeholder bootstrap servers and the REPLACE_ME SASL/SCRAM password.")
	_, _ = fmt.Fprintf(out, "Next step:\n  kcp scan clusters --source-type osk --credentials-file %s\n", outputPath)

	return nil
}
