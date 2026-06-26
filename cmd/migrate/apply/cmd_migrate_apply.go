package apply

import (
	"fmt"
	"os"

	"github.com/confluentinc/kcp/internal/manifest"
	migrate "github.com/confluentinc/kcp/internal/migrate"
	mclusterlink "github.com/confluentinc/kcp/internal/migrate/clusterlink"
	mmirror "github.com/confluentinc/kcp/internal/migrate/mirrortopics"
	mnew "github.com/confluentinc/kcp/internal/migrate/newtopics"
	"github.com/confluentinc/kcp/internal/services/reconcile"
	"github.com/confluentinc/kcp/internal/targets"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/confluentinc/kcp/internal/utils"
	"github.com/spf13/cobra"
)

// newSourceReader builds the live source reader. It is a package-level var so
// tests can substitute a fake without opening a live Kafka connection.
var newSourceReader = func(conn types.KafkaSourceConn) migrate.Source {
	return migrate.NewKafkaSourceReader(conn)
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

	// Phase 1 supports cluster-link and/or topics sections.
	if m.Spec.ClusterLink == nil && m.Spec.Topics == nil {
		return fmt.Errorf("nothing to apply: spec.clusterLink and/or spec.topics is required in this phase")
	}

	// --- source cluster id reader (spec.source → D1) ---
	// spec.source.credentials is used only to read the live source cluster id
	// (and, in destination mode, is independent of the link→source connection
	// auth, which comes from clusterLink.source).
	srcCluster, err := loadMigrateCluster(cmd, "spec.source", m.Spec.Source.BootstrapServers, m.Spec.Source.Credentials)
	if err != nil {
		return err
	}
	src := newSourceReader(srcCluster)

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

	// --- reconcilers ---
	// The engine runs reconcilers in order; the cluster link (when present) is the
	// precondition for mirror topics, so it is appended first.
	var recs []reconcile.Reconciler

	// cl and mode are retained for the topic wiring below; both are zero when
	// no cluster-link section is present (mode:new can run without a link).
	var cl *manifest.ClusterLink
	var mode string
	// srcLinkTgt is the source-side OUTBOUND link endpoint (source-initiated mode
	// only). It carries cluster.link.prefix, so it is the prefix target for mirror
	// topics in source mode. nil in destination mode.
	var srcLinkTgt *targets.LinkEndpoint

	if m.Spec.ClusterLink != nil {
		cl = m.Spec.ClusterLink
		mode = cl.Mode
		if mode == "" {
			mode = manifest.ClusterLinkModeDestination
		}

		linkConfigs, err := resolveLinkConfigs(cl)
		if err != nil {
			return fmt.Errorf("resolving cluster-link configs: %w", err)
		}

		var rec *mclusterlink.Reconciler
		switch mode {
		case manifest.ClusterLinkModeDestination:
			// Destination-initiated: the link→source connection auth comes from
			// clusterLink.source (D2), NOT spec.source.
			linkCluster, err := loadMigrateCluster(cmd, "spec.clusterLink.source", cl.Source.BootstrapServers, cl.Source.Credentials)
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
				SourceBootstrapServers: cl.Source.BootstrapServers,
				Auth:                   auth,
				Configs:                linkConfigs,
			}, src, tgt)

		case manifest.ClusterLinkModeSource:
			// Source-initiated: a second link object lives on the source cluster's
			// REST (D4). It carries the destination address + source→destination
			// connection auth derived from clusterLink.destination (D5).
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
			srcLinkTgt = targets.NewLinkEndpoint(cl.SourceRest.Endpoint, srcRestCreds, srcRestClient)

			destCluster, err := loadMigrateCluster(cmd, "spec.clusterLink.destination", cl.Destination.BootstrapServers, cl.Destination.Credentials)
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
				DestBootstrapServers: cl.Destination.BootstrapServers,
				DestAuth:             destAuth,
				Configs:              linkConfigs,
			}, src, tgt, srcLinkTgt)

		default:
			return fmt.Errorf("unsupported clusterLink.mode %q", mode)
		}
		recs = append(recs, rec)
	}

	if t := m.Spec.Topics; t != nil {
		switch t.Mode {
		case manifest.TopicModeMirror:
			// Validation guarantees a cluster link is present for mode:mirror;
			// guard defensively in case this is reached without one.
			if cl == nil {
				return fmt.Errorf("spec.topics.mode %q requires spec.clusterLink", manifest.TopicModeMirror)
			}
			// The prefix (cluster.link.prefix) lives on the link object. In
			// destination mode that is the destination target; in source mode it is
			// the source-side OUTBOUND link.
			prefixTgt := tgt
			if mode == manifest.ClusterLinkModeSource {
				prefixTgt = srcLinkTgt
			}
			recs = append(recs, mmirror.New(mmirror.Config{
				LinkName: cl.Name,
				Include:  t.Include,
				Exclude:  t.Exclude,
				Prefix:   cl.Prefix,
			}, src, tgt, prefixTgt))

		case manifest.TopicModeNew:
			recs = append(recs, mnew.New(mnew.Config{
				Include: t.Include,
				Exclude: t.Exclude,
			}, src, tgt))

		default:
			return fmt.Errorf("unsupported topics.mode %q", t.Mode)
		}
	}

	eng := reconcile.NewEngine(cmd.OutOrStdout())
	// Phase 1 relies on the engine to render outcomes; the structured Report is
	// not consumed yet (a later phase may use it for a machine-readable summary).
	_, err = eng.Run(cmd.Context(), recs, dryRun)
	return err
}

// resolveLinkConfigs builds the link-config map from the manifest's typed
// clusterLink fields (with migration defaults), used as the reconciler's Configs.
func resolveLinkConfigs(cl *manifest.ClusterLink) (map[string]string, error) {
	return cl.ResolvedLinkConfigs()
}

// loadMigrateCluster loads + validates a flat migrate credentials file and composes
// the result with the given bootstrap servers from the manifest into a KafkaSourceConn.
func loadMigrateCluster(cmd *cobra.Command, field string, bootstrapServers []string, path string) (types.KafkaSourceConn, error) {
	if path == "" {
		return types.KafkaSourceConn{}, fmt.Errorf("%s.credentials is required", field)
	}
	creds, errs := types.LoadMigrateClusterCredentials(path)
	if len(errs) > 0 {
		for _, e := range errs {
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "✖ %v\n", e)
		}
		return types.KafkaSourceConn{}, fmt.Errorf("invalid %s.credentials: %d problem(s) found", field, len(errs))
	}
	return types.MigrateConn(bootstrapServers, creds), nil
}
