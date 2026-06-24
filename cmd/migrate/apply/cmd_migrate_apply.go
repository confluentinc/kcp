package apply

import (
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

// newSourceReader builds the live source reader. It is a package-level var so
// tests can substitute a fake without opening a live Kafka connection.
var newSourceReader = func(cluster types.OSKClusterAuth) migrate.Source {
	return migrate.NewOSKSourceReader(cluster)
}

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

	cl := m.Spec.ClusterLink
	mode := cl.Mode
	if mode == "" {
		mode = manifest.ClusterLinkModeDestination
	}

	linkConfigs, err := resolveLinkConfigs(cl)
	if err != nil {
		return fmt.Errorf("resolving cluster-link configs: %w", err)
	}

	// --- source cluster id reader (spec.source.credentials → D1) ---
	// spec.source.credentials is used only to read the live source cluster id
	// (and, in destination mode, is independent of the link→source connection
	// auth, which comes from clusterLink.sourceCredentials).
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
	src := newSourceReader(srcCreds.Clusters[0])

	// --- destination target (confluent-platform) ---
	if m.Spec.Target.Type != manifest.TargetConfluentPlatform {
		return fmt.Errorf("phase 1 supports target type %q only", manifest.TargetConfluentPlatform)
	}
	tgtCreds, err := targets.LoadCredentials(m.Spec.Target.Credentials)
	if err != nil {
		return err
	}
	tgtClient, err := tgtCreds.HTTPClient()
	if err != nil {
		return err
	}
	tgt := targets.NewConfluentPlatformTarget(m.Spec.Target.Kafka.RestEndpoint, tgtCreds, tgtClient)

	// --- reconciler ---
	var rec *mclusterlink.Reconciler
	switch mode {
	case manifest.ClusterLinkModeDestination:
		// Destination-initiated: the link→source connection auth comes from
		// clusterLink.sourceCredentials (D2), NOT spec.source.credentials.
		linkCluster, err := loadSingleOSKCluster(cmd, "clusterLink.sourceCredentials", cl.SourceCredentials)
		if err != nil {
			return err
		}
		auth, err := mclusterlink.LinkAuthFromSource(linkCluster)
		if err != nil {
			return fmt.Errorf("deriving cluster-link source auth: %w", err)
		}
		rec = mclusterlink.New(mclusterlink.Config{
			LinkName:               cl.Name,
			Mode:                   manifest.ClusterLinkModeDestination,
			SourceBootstrapServers: linkCluster.BootstrapServers,
			Auth:                   auth,
			Configs:                linkConfigs,
		}, src, tgt)

	case manifest.ClusterLinkModeSource:
		// Source-initiated: a second link object lives on the source cluster's
		// REST (D4). It carries the destination address + source→destination
		// connection auth derived from clusterLink.destinationCredentials (D5).
		if cl.SourceRest == nil {
			return fmt.Errorf("clusterLink.sourceRest is required for mode %q", manifest.ClusterLinkModeSource)
		}
		srcRestCreds, err := targets.LoadCredentials(cl.SourceRest.Credentials)
		if err != nil {
			return err
		}
		srcRestClient, err := srcRestCreds.HTTPClient()
		if err != nil {
			return err
		}
		srcLinkTgt := targets.NewLinkEndpoint(cl.SourceRest.Endpoint, srcRestCreds, srcRestClient)

		destCluster, err := loadSingleOSKCluster(cmd, "clusterLink.destinationCredentials", cl.DestinationCredentials)
		if err != nil {
			return err
		}
		destAuth, err := mclusterlink.LinkAuthFromSource(destCluster)
		if err != nil {
			return fmt.Errorf("deriving cluster-link destination auth: %w", err)
		}
		rec = mclusterlink.NewSourceInitiated(mclusterlink.Config{
			LinkName:             cl.Name,
			Mode:                 manifest.ClusterLinkModeSource,
			DestBootstrapServers: destCluster.BootstrapServers,
			DestAuth:             destAuth,
			Configs:              linkConfigs,
		}, src, tgt, srcLinkTgt)

	default:
		return fmt.Errorf("unsupported clusterLink.mode %q", mode)
	}

	eng := reconcile.NewEngine(cmd.OutOrStdout())
	// Phase 1 relies on the engine to render outcomes; the structured Report is
	// not consumed yet (a later phase may use it for a machine-readable summary).
	_, err = eng.Run(cmd.Context(), []reconcile.Reconciler{rec}, dryRun)
	return err
}

// resolveLinkConfigs builds the link-config map from the manifest's typed
// clusterLink fields (with migration defaults), used as the reconciler's Configs.
func resolveLinkConfigs(cl *manifest.ClusterLink) (map[string]string, error) {
	return cl.ResolvedLinkConfigs()
}

// loadSingleOSKCluster validates+loads an apache-kafka credentials file that
// must contain exactly one cluster, surfacing per-field errors against the
// given manifest field name.
func loadSingleOSKCluster(cmd *cobra.Command, field, path string) (types.OSKClusterAuth, error) {
	if path == "" {
		return types.OSKClusterAuth{}, fmt.Errorf("%s is required", field)
	}
	creds, errs := types.NewOSKCredentialsFromFile(path)
	if len(errs) > 0 {
		for _, e := range errs {
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "✖ %v\n", e)
		}
		return types.OSKClusterAuth{}, fmt.Errorf("invalid %s: %d problem(s) found", field, len(errs))
	}
	if len(creds.Clusters) != 1 {
		return types.OSKClusterAuth{}, fmt.Errorf("%s must contain exactly one cluster, found %d", field, len(creds.Clusters))
	}
	return creds.Clusters[0], nil
}
