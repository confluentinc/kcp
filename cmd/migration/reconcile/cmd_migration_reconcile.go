// Package reconcile is a thin CLI over the in-code entry point
// migplan.Reconcile: it loads the GatewayMigration manifest, runs the
// reconciliation engine against live source/target/cluster-link state, renders
// the per-topic report, and echoes the three artifacts to stdout. It writes no
// files and never mutates the gateway. Hidden prototype command; the real caller
// is the migration state machine, which calls migplan.Reconcile directly.
package reconcile

import (
	"encoding/json"
	"fmt"

	"github.com/confluentinc/kcp/internal/manifest"
	"github.com/confluentinc/kcp/internal/services/migplan"
	"github.com/confluentinc/kcp/internal/utils"
	"github.com/spf13/cobra"
)

type reconcileFlags struct {
	manifestPath string
}

func NewMigrationReconcileCmd() *cobra.Command {
	f := &reconcileFlags{}

	cmd := &cobra.Command{
		Use:   "reconcile",
		Short: "Reconcile the migration plan against live source, target, and cluster-link state",
		Long: `Prototype command that drives the migration reconciliation engine.

It loads the GatewayMigration manifest, pulls the live Gateway CR named in
spec.gateway, reads the live source topics, target topics, and cluster-link
state, then reconciles them against the route + target streaming domain + topic
selection declared in the manifest's spec.topicGroup. It renders a per-topic
report and echoes the three artifacts (topics.json, fence-rules.yaml,
switchover-rules.yaml) to stdout.

The command writes no files and never mutates the gateway.`,
		Hidden:        true,
		SilenceErrors: true,
		SilenceUsage:  true,
		Args:          cobra.NoArgs,
		PreRunE: func(cmd *cobra.Command, args []string) error {
			return utils.BindEnvToFlags(cmd)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return runReconcile(cmd, f)
		},
	}

	cmd.Flags().StringVar(&f.manifestPath, "migration-yaml", "", "Path to the GatewayMigration manifest (route, target domain and topic selection come from its spec.topicGroup; the gateway CR is pulled live from spec.gateway).")

	_ = cmd.MarkFlagRequired("migration-yaml")

	return cmd
}

func runReconcile(cmd *cobra.Command, f *reconcileFlags) error {
	g, err := manifest.LoadGatewayMigrationFile(f.manifestPath)
	if err != nil {
		return err
	}

	w := cmd.OutOrStdout()

	// Reconcile renders its own report to the command's writer; this prototype
	// command additionally echoes the raw artifacts so they can be eyeballed.
	res, err := migplan.Reconcile(cmd.Context(), g, migplan.WithOutput(w))
	if err != nil {
		return err
	}

	if !res.Refused && len(res.Topics) > 0 {
		topicsJSON, _ := json.MarshalIndent(res.Topics, "", "  ")
		_, _ = fmt.Fprintf(w, "\n=== topics.json ===\n%s\n", topicsJSON)
		_, _ = fmt.Fprintf(w, "\n=== fence-rules.yaml ===\n%s\n", res.FenceYAML)
		_, _ = fmt.Fprintf(w, "\n=== switchover-rules.yaml ===\n%s\n", res.SwitchoverYAML)
	}
	return nil
}
