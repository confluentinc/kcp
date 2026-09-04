// Package reconcile wires the migration reconciliation engine (internal/services/migplan)
// to live inputs: it loads the GatewayMigration manifest plus a static gateway-config
// file, builds the engine-owned ReconcileInput from interim selector flags, constructs
// the four live providers, runs the engine, renders the report and — unless --dry-run —
// writes the three artifacts. It is a hidden prototype command; it never mutates the
// gateway.
package reconcile

import (
	"fmt"
	"io"

	kafkatypes "github.com/aws/aws-sdk-go-v2/service/kafka/types"
	"github.com/confluentinc/kcp/internal/client"
	"github.com/confluentinc/kcp/internal/manifest"
	"github.com/confluentinc/kcp/internal/services/clusterlink"
	"github.com/confluentinc/kcp/internal/services/migplan"
	"github.com/confluentinc/kcp/internal/services/migplan/providers"
	"github.com/confluentinc/kcp/internal/services/migplan/reconcile"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/confluentinc/kcp/internal/utils"
	"github.com/spf13/cobra"
)

// defaultKafkaVersion mirrors the version the other migrate/scan admin builders
// pin (internal/migrate/source.go, internal/sources/osk). The exact protocol
// version is inert for topic listing; parity with the rest of KCP is what matters.
const defaultKafkaVersion = "3.6.0"

// reconcileFlags is the interim flag surface. The selector (route/target-domain/
// topics/patterns) comes from flags because the working-tree manifest has no
// spec.topicGroup yet; the connections come from the manifest.
type reconcileFlags struct {
	manifestPath      string
	gatewayConfig     string
	route             string
	targetDomain      string
	topics            []string
	topicPatterns     []string
	dryRun            bool
	outDir            string
	offsetSyncEnabled bool
}

func NewMigrationReconcileCmd() *cobra.Command {
	f := &reconcileFlags{}

	cmd := &cobra.Command{
		Use:   "reconcile",
		Short: "Reconcile the migration plan against live source, target, and cluster-link state",
		Long: `Prototype command that drives the migration reconciliation engine.

It loads the GatewayMigration manifest and a static gateway-config file, reads the
live source topics, target topics, and cluster-link mirror state, then reconciles
them against the requested route and target streaming domain. It renders a per-topic
report and, unless --dry-run is set, writes topics.json, fence-rules.yaml and
switchover-rules.yaml into --out-dir.

The command never mutates the gateway; it only reads and writes local files.`,
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

	cmd.Flags().StringVar(&f.manifestPath, "migration-yaml", "", "Path to the GatewayMigration manifest describing this migration.")
	cmd.Flags().StringVar(&f.gatewayConfig, "gateway-config", "", "Path to the static gateway CR YAML (prototype stand-in for the live k8s pull).")
	cmd.Flags().StringVar(&f.route, "route", "", "Gateway route to reconcile.")
	cmd.Flags().StringVar(&f.targetDomain, "target-domain", "", "Target streaming-domain name the route switches to.")
	cmd.Flags().StringSliceVar(&f.topics, "topics", nil, "Literal topic names to migrate (comma-separated, repeatable).")
	cmd.Flags().StringSliceVar(&f.topicPatterns, "topic-patterns", nil, "Topic name regex patterns to migrate (comma-separated, repeatable).")
	cmd.Flags().BoolVar(&f.dryRun, "dry-run", false, "Render the report but do not write artifacts.")
	cmd.Flags().StringVar(&f.outDir, "out-dir", ".", "Directory to write the reconciliation artifacts into.")
	cmd.Flags().BoolVar(&f.offsetSyncEnabled, "offset-sync-enabled", false, "Interim flag: whether the cluster link has consumer offset sync enabled (live read lands in Phase C).")

	for _, name := range []string{"migration-yaml", "gateway-config", "route", "target-domain"} {
		_ = cmd.MarkFlagRequired(name)
	}

	return cmd
}

func runReconcile(cmd *cobra.Command, f *reconcileFlags) error {
	g, err := manifest.LoadGatewayMigrationFile(f.manifestPath)
	if err != nil {
		return err
	}

	in, err := buildReconcileInput(*f)
	if err != nil {
		return err
	}

	gw := providers.NewGatewayFile(f.gatewayConfig, f.route)

	link, err := buildLinkStatusProvider(g, f.offsetSyncEnabled)
	if err != nil {
		return err
	}

	src, srcCloser, err := buildSourceTopicLister(g)
	if err != nil {
		return err
	}
	defer func() { _ = srcCloser.Close() }()

	tgt, tgtCloser, err := buildTargetTopicLister(g)
	if err != nil {
		return err
	}
	defer func() { _ = tgtCloser.Close() }()

	engine := migplan.NewReconciliationEngine(gw, src, tgt, link)
	plan, err := engine.Run(cmd.Context(), in)
	if err != nil {
		return err
	}

	migplan.RenderReport(cmd.OutOrStdout(), plan.Report)

	if !f.dryRun && plan.Artifacts != nil {
		if err := migplan.WriteArtifacts(f.outDir, plan.Artifacts); err != nil {
			return err
		}
	}
	return nil
}

// buildReconcileInput maps the interim selector flags onto the engine-owned
// ReconcileInput. (When spec.topicGroup lands on main, switch this to read it.)
func buildReconcileInput(f reconcileFlags) (reconcile.ReconcileInput, error) {
	if f.route == "" {
		return reconcile.ReconcileInput{}, fmt.Errorf("--route is required")
	}
	if f.targetDomain == "" {
		return reconcile.ReconcileInput{}, fmt.Errorf("--target-domain is required")
	}
	if len(f.topics) == 0 && len(f.topicPatterns) == 0 {
		return reconcile.ReconcileInput{}, fmt.Errorf("at least one of --topics / --topic-patterns is required")
	}
	return reconcile.ReconcileInput{
		Topics:        f.topics,
		TopicPatterns: f.topicPatterns,
		Route:         f.route,
		TargetDomain:  f.targetDomain,
	}, nil
}

// buildLinkStatusProvider mirrors lagcheck.buildLagCheckConfig: the destination
// REST leg (whichever auth form the manifest resolves) drives the cluster-link
// service. Topics is empty ⇒ the provider reports every mirror on the link.
func buildLinkStatusProvider(g *manifest.GatewayMigration, offsetSyncEnabled bool) (migplan.LinkStatusProvider, error) {
	if g.Spec.Target.Kafka == nil {
		return nil, fmt.Errorf("spec.target.kafka: required")
	}
	restCreds, err := g.RestCredentials()
	if err != nil {
		return nil, fmt.Errorf("resolving destination REST credentials: %w", err)
	}
	httpClient, err := restCreds.HTTPClient()
	if err != nil {
		return nil, fmt.Errorf("building destination REST client: %w", err)
	}
	svc := clusterlink.NewConfluentCloudService(httpClient)
	cfg := clusterlink.Config{
		RestEndpoint: g.Spec.Target.Kafka.RestEndpoint,
		ClusterID:    g.Spec.Target.ClusterID,
		LinkName:     g.Spec.ClusterLink.Name,
		Auth:         restCreds.Authenticator(),
		Topics:       []string{}, // empty ⇒ all mirrors
	}
	return providers.NewClusterLinkStatus(svc, cfg, offsetSyncEnabled), nil
}

// buildSourceTopicLister builds the source-cluster topic lister from the
// manifest source credentials, following the same auth resolution as
// `kcp migration execute` (applySourceAuth / createSourceOffset). The returned
// io.Closer is the underlying Kafka admin; the caller owns closing it.
func buildSourceTopicLister(g *manifest.GatewayMigration) (migplan.TopicLister, io.Closer, error) {
	creds, errs := g.SourceCredentials()
	if len(errs) > 0 {
		return nil, nil, manifest.JoinProblems("spec.source.credentials", errs)
	}
	conn := types.MigrateConn(g.Spec.Source.BootstrapServers, creds)
	return buildTopicLister(conn)
}

// buildTargetTopicLister builds the destination-cluster topic lister from the
// destination KAFKA leg (not the REST leg — they may differ), following the same
// auth resolution as `kcp migration execute` (createDestinationOffset). The
// returned io.Closer is the underlying Kafka admin; the caller owns closing it.
func buildTargetTopicLister(g *manifest.GatewayMigration) (migplan.TopicLister, io.Closer, error) {
	if g.Spec.Target.Kafka == nil {
		return nil, nil, fmt.Errorf("spec.target.kafka: required")
	}
	creds, errs := g.DestinationKafkaCredentials()
	if len(errs) > 0 {
		return nil, nil, manifest.JoinProblems("spec.target.kafka.credentials", errs)
	}
	conn := types.MigrateConn(g.Spec.Target.Kafka.BootstrapServers, creds)

	// Backward-compat parity with migration execute: a Confluent Cloud
	// destination that supplies neither ca_cert nor an explicit tls signal must
	// still dial SASL_SSL, not cleartext SASL_PLAINTEXT — the destination is a
	// managed cluster, always TLS. (A source may legitimately be plaintext, so
	// this default is destination-only.)
	if sp := conn.AuthMethod.SASLPlain; sp != nil && sp.CACert == "" && !sp.UseTLS {
		sp.UseTLS = true
	}
	return buildTopicLister(conn)
}

// buildTopicLister opens a Kafka admin for conn and wraps it as a TopicLister.
// It mirrors internal/migrate.buildKafkaSourceAdmin: auth is dispatched through
// the shared client.AdminOptionForAuthMethod mapper (skipTLSVerify threaded from
// the connection), and the encryption-in-transit arg is inert (the auth option
// determines TLS) — ClientBrokerTls is passed for parity with the rest of KCP.
// The returned io.Closer is the admin itself; the caller owns closing it.
func buildTopicLister(conn types.KafkaSourceConn) (migplan.TopicLister, io.Closer, error) {
	authType, err := conn.GetSelectedAuthType()
	if err != nil {
		return nil, nil, fmt.Errorf("determining auth type: %w", err)
	}
	region := ""
	if authType == types.AuthTypeIAM && conn.AuthMethod.IAM != nil {
		region = conn.AuthMethod.IAM.Region
	}
	authOpt, err := client.AdminOptionForAuthMethod(authType, conn.AuthMethod, conn.InsecureSkipTLSVerify)
	if err != nil {
		return nil, nil, fmt.Errorf("resolving auth option: %w", err)
	}
	admin, err := client.NewKafkaAdmin(conn.BootstrapServers, kafkatypes.ClientBrokerTls, region, defaultKafkaVersion, authOpt)
	if err != nil {
		return nil, nil, fmt.Errorf("connecting to cluster: %w", err)
	}
	return providers.NewKafkaTopicLister(admin), admin, nil
}
