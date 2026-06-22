package apply

import (
	"context"
	"fmt"
	"os"

	"github.com/confluentinc/kcp/internal/manifest"
	migrate "github.com/confluentinc/kcp/internal/migrate"
	mclusterlink "github.com/confluentinc/kcp/internal/migrate/clusterlink"
	"github.com/confluentinc/kcp/internal/services/reconcile"
	"github.com/confluentinc/kcp/internal/targets"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/confluentinc/kcp/internal/utils"
	"github.com/spf13/cobra"
)

func NewMigrateApplyCmd() *cobra.Command {
	var file string
	var dryRun bool

	cmd := &cobra.Command{
		Use:           "apply",
		Short:         "Apply a migration manifest (additively reconcile the target)",
		SilenceUsage:  true,
		SilenceErrors: true,
		PreRunE:       func(cmd *cobra.Command, _ []string) error { return utils.BindEnvToFlags(cmd) },
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runApply(cmd, file, dryRun)
		},
	}
	cmd.Flags().StringVarP(&file, "file", "f", "", "Path to the migration manifest (required)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview the changes without applying them")
	_ = cmd.MarkFlagRequired("file")
	return cmd
}

func runApply(cmd *cobra.Command, file string, dryRun bool) error {
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

	// Phase 1 supports a cluster-link section only.
	if m.Spec.ClusterLink == nil {
		return fmt.Errorf("nothing to apply: spec.clusterLink is required in this phase")
	}

	// --- source (apache-kafka, exactly one cluster) ---
	srcCreds, errs := types.NewOSKCredentialsFromFile(m.Spec.Source.Credentials)
	if len(errs) > 0 {
		for _, e := range errs {
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "✖ %v\n", e)
		}
		return fmt.Errorf("invalid source credentials: %d problem(s) found", len(errs))
	}
	if len(srcCreds.Clusters) != 1 {
		return fmt.Errorf("source credentials must contain exactly one cluster, found %d", len(srcCreds.Clusters))
	}
	srcCluster := srcCreds.Clusters[0]

	var src migrate.Source = migrate.NewOSKSourceReader(srcCluster)
	if id := os.Getenv("KCP_TEST_SOURCE_CLUSTER_ID"); id != "" {
		src = staticSource(id) // test hook: avoids a live Kafka connection in unit tests
	}

	// --- target (confluent-platform) ---
	if m.Spec.Target.Type != manifest.TargetConfluentPlatform {
		return fmt.Errorf("phase 1 supports target type %q only", manifest.TargetConfluentPlatform)
	}
	tgtCreds, err := targets.LoadCredentials(m.Spec.Target.Credentials)
	if err != nil {
		return err
	}
	tgt := targets.NewConfluentPlatformTarget(m.Spec.Target.Kafka.RestEndpoint, tgtCreds, nil)

	// --- reconciler ---
	rec := mclusterlink.New(mclusterlink.Config{
		LinkName:               m.Spec.ClusterLink.Name,
		SourceBootstrapServers: srcCluster.BootstrapServers,
		SecurityProtocol:       "PLAINTEXT", // Phase 1
		Configs:                m.Spec.ClusterLink.Configs,
	}, src, tgt)

	eng := reconcile.NewEngine(cmd.OutOrStdout())
	_, err = eng.Run(cmd.Context(), []reconcile.Reconciler{rec}, dryRun)
	return err
}

// staticSource is a test hook returning a fixed cluster id.
type staticSource string

func (s staticSource) ClusterID(context.Context) (string, error) { return string(s), nil }
