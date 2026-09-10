package executetbm

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/IBM/sarama"
	"github.com/confluentinc/kcp/internal/client"
	"github.com/confluentinc/kcp/internal/manifest"
	"github.com/confluentinc/kcp/internal/services/clusterlink"
	"github.com/confluentinc/kcp/internal/services/gateway"
	"github.com/confluentinc/kcp/internal/services/migplan"
	"github.com/confluentinc/kcp/internal/services/migration/tbm"
	"github.com/confluentinc/kcp/internal/services/offset"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/confluentinc/kcp/internal/utils"
	"github.com/spf13/cobra"
)

var (
	manifestFile                            string
	tbmStateFile                            string
	lagThresholdOverride                    int
	rolloutTimeoutOverride                  time.Duration
	hotReloadTimeoutOverride                time.Duration
	detectUnroutedProducersDurationOverride time.Duration
)

// reconcileFunc is the engine entry point the command calls to produce the
// reconcile plan. Injected via newExecuteTBMCmd so the command's own tests,
// which exercise the FSM scaffold with unreachable placeholder endpoints, can
// pass a stub; production binds the real engine.
type reconcileFunc func(context.Context, *manifest.GatewayMigration, ...migplan.Option) (*migplan.Result, error)

// offsetProvidersFunc builds the source and destination offset providers used
// by wait_for_lags, plus a close function for both. Injected via
// newExecuteTBMCmd so the command's own tests (whose manifests point at
// unreachable placeholder endpoints) can pass a stub; production dials real
// Kafka connections.
type offsetProvidersFunc func(g *manifest.GatewayMigration) (source, destination offset.Provider, closeFn func() error, err error)

// gatewayServiceFunc builds the gateway.Service used by fence (and later
// switch) to apply and verify Gateway CR changes. Injected via
// newExecuteTBMCmd so the command's own tests can pass a stub instead of
// dialing a real Kubernetes cluster; production builds a real K8sService from
// the manifest's kubeconfig.
type gatewayServiceFunc func(g *manifest.GatewayMigration) (gateway.Service, error)

// clusterLinkServiceFunc builds the clusterlink.Service used by promote to
// poll and promote mirror topics. Injected via newExecuteTBMCmd so the
// command's own tests can pass a stub instead of dialing a real cluster-link
// REST endpoint; production builds a real ConfluentCloudService (the name is
// historical — it serves Confluent Platform destinations over the same REST
// surface, confirmed by the TBM e2e suite's own test harness) from the
// manifest's destination REST credentials.
type clusterLinkServiceFunc func(g *manifest.GatewayMigration) (clusterlink.Service, error)

// buildClusterLinkService opens a real clusterlink.Service using the
// manifest's destination REST credentials (spec.target.kafka.restCredentials,
// or derived from spec.target.kafka.credentials when that leg is sasl_plain —
// see (*GatewayMigration).RestCredentials's own doc comment).
func buildClusterLinkService(g *manifest.GatewayMigration) (clusterlink.Service, error) {
	restCreds, err := g.RestCredentials()
	if err != nil {
		return nil, err
	}
	httpClient, err := restCreds.HTTPClient()
	if err != nil {
		return nil, err
	}
	return clusterlink.NewConfluentCloudService(httpClient), nil
}

// buildGatewayService opens a real gateway.Service using the manifest's
// spec.gateway.kubeconfig (a leading ~/ is expanded by KubeconfigPath).
func buildGatewayService(g *manifest.GatewayMigration) (gateway.Service, error) {
	kubeconfig, err := g.KubeconfigPath()
	if err != nil {
		return nil, err
	}
	return gateway.NewK8sService(kubeconfig), nil
}

const executeTBMLong = `Execute a Topic-Batch Migration (TBM) run.

This is a scaffold: only verify_fence remains noop (it sleeps to simulate real
execution timing, then logs). initialize validates the already-computed
reconcile plan (see the migplan package) and captures its promote topic list
plus fence/switchover artifacts for later transitions to consume.
wait_for_lags polls source and destination Kafka offsets for those topics
until every one is under spec.defaultPolicies.lagThreshold (overridable per
run with --lag-threshold). fence reconfigures the gateway's named route by
applying the plan's fence rules to the live Gateway CR, then waits for the
operator to report the gateway ready (and, if it supports hot-reload, for
every pod to confirm the new config revision). promote polls source and
destination Kafka offsets for those same topics until each reaches exact zero
lag, then promotes that topic's cluster-link mirror, confirming it reaches the
terminal STOPPED status. switch applies the plan's switchover rules to the
live Gateway CR the same way fence applies its fence rules, then waits for the
operator to report it ready. This command exists to validate the
state-machine shape and command wiring ahead of the real per-batch migration
logic described in the TBM design proposal.

The migration is identified by metadata.name in the GatewayMigration manifest at
--migration-yaml — there is no separate init step and no --migration-id flag. The first
run for a given name creates a fresh entry in the TBM state file; a later run with the
SAME manifest content resumes from the last completed step. A later run with a CHANGED
manifest for the SAME name is refused outright, with no override — a genuinely new
migration needs a new metadata.name.`

// NewMigrationExecuteTBMCmd builds the `execute-tbm` command bound to the real
// reconciliation engine, real Kafka connections, and a real gateway service.
func NewMigrationExecuteTBMCmd() *cobra.Command {
	return newExecuteTBMCmd(migplan.Reconcile, buildOffsetProviders, buildGatewayService, buildClusterLinkService)
}

// newExecuteTBMCmd builds the command with the reconcile entry point,
// offset-provider builder, gateway-service builder, and cluster-link-service
// builder all injected, so tests can pass stubs instead of the live engine,
// live Kafka connections, a live Kubernetes cluster, and a live cluster-link
// REST endpoint.
func newExecuteTBMCmd(reconcile reconcileFunc, buildOffsets offsetProvidersFunc, buildGateway gatewayServiceFunc, buildClusterLink clusterLinkServiceFunc) *cobra.Command {
	cmd := &cobra.Command{
		Use:           "execute-tbm",
		Short:         "Execute a Topic-Batch Migration run (scaffold: verify_fence still noop)",
		Long:          executeTBMLong,
		Example:       `  kcp migration execute-tbm --migration-yaml gateway-migration.yaml --tbm-state-file tbm-state.json`,
		Hidden:        true, // scaffold: only verify_fence is still noop; kept in the binary but not user-facing (cascades to --help and gen-docs)
		SilenceErrors: true,
		SilenceUsage:  true,
		Args:          cobra.NoArgs,
		PreRunE:       func(c *cobra.Command, _ []string) error { return utils.BindEnvToFlags(c) },
		RunE: func(c *cobra.Command, _ []string) error {
			return runMigrationExecuteTBM(c, reconcile, buildOffsets, buildGateway, buildClusterLink)
		},
	}

	cmd.Flags().StringVar(&manifestFile, "migration-yaml", "", "Path to the GatewayMigration manifest describing this migration.")
	cmd.Flags().StringVar(&tbmStateFile, "tbm-state-file", "", "Path to the TBM state file. Created if it doesn't exist.")
	cmd.Flags().IntVar(&lagThresholdOverride, "lag-threshold", 0, "Override spec.defaultPolicies.lagThreshold: total replication lag (sum of all partition lags) tolerated before proceeding.")
	cmd.Flags().DurationVar(&rolloutTimeoutOverride, "rollout-timeout", 0, "Max wait for the operator to report the gateway Ready during fence (and, later, switchover). 0 means no deadline.")
	cmd.Flags().DurationVar(&hotReloadTimeoutOverride, "hot-reload-timeout", 0, "Max wait for every gateway pod to report the new config revision when the gateway supports hot-reload. 0 uses the built-in 90s budget; never unbounded.")
	cmd.Flags().DurationVar(&detectUnroutedProducersDurationOverride, "detect-unrouted-producers-duration", 0, "Override spec.defaultPolicies.detectUnroutedProducersDuration: monitoring window verify_fence uses to detect a producer bypassing the gateway. 0 disables the check.")

	_ = cmd.MarkFlagRequired("migration-yaml")
	_ = cmd.MarkFlagRequired("tbm-state-file")

	return cmd
}

func runMigrationExecuteTBM(cmd *cobra.Command, reconcile reconcileFunc, buildOffsets offsetProvidersFunc, buildGateway gatewayServiceFunc, buildClusterLink clusterLinkServiceFunc) error {
	g, err := manifest.LoadGatewayMigrationFile(manifestFile)
	if err != nil {
		return err
	}

	// Command-line override replaces the manifest's lagThreshold default for
	// this run, then the effective policy is re-validated: an override can
	// carry a value the manifest itself never did. Only a flag the operator
	// explicitly set (checked via Flags().Changed) overrides, so 0 — a
	// legitimate manifest value — is not confused with "unset".
	if cmd.Flags().Changed("lag-threshold") {
		g.Spec.DefaultPolicies.LagThreshold = lagThresholdOverride
	}
	if cmd.Flags().Changed("detect-unrouted-producers-duration") {
		g.Spec.DefaultPolicies.DetectUnroutedProducersDuration = detectUnroutedProducersDurationOverride
	}
	if errs := g.Spec.DefaultPolicies.Validate(); len(errs) > 0 {
		return manifest.JoinProblems("the effective migration policy (manifest defaults with command-line overrides applied)", errs)
	}

	manifestBytes, err := os.ReadFile(manifestFile)
	if err != nil {
		return fmt.Errorf("failed to read migration manifest: %w", err)
	}
	hash := tbm.HashManifest(manifestBytes)

	var tbmState *tbm.TBMState
	if _, statErr := os.Stat(tbmStateFile); statErr == nil {
		tbmState, err = tbm.NewTBMStateFromFile(tbmStateFile)
		if err != nil {
			return fmt.Errorf("failed to load tbm state: %w", err)
		}
	} else if os.IsNotExist(statErr) {
		tbmState = tbm.NewTBMState()
	} else {
		return fmt.Errorf("failed to check tbm state file: %w", statErr)
	}

	var clusterRestEndpoint string
	if g.Spec.Target.Kafka != nil {
		clusterRestEndpoint = g.Spec.Target.Kafka.RestEndpoint
	}
	config, err := resolveTBMConfig(tbmState, g.Metadata.Name, hash, g.Spec.Gateway.Namespace, g.Spec.Gateway.CrName,
		g.Spec.Target.ClusterID, clusterRestEndpoint, g.Spec.ClusterLink.Name)
	if err != nil {
		return err
	}

	// Early write, mirroring kcp migration init's Phase 3: the file must exist
	// (and record a freshly-created migration) before the orchestrator runs.
	tbmState.UpsertMigration(*config)
	if err := tbmState.WriteToFile(tbmStateFile); err != nil {
		return fmt.Errorf("failed to write tbm state file: %w", err)
	}

	// Open the Kafka connections wait_for_lags needs. Like the reconcile call
	// below, this runs on every invocation, even a pure resume past
	// wait_for_lags — accepted for now, same as the reconcile cost, rather
	// than restructuring the command's control flow to check HasPendingWork
	// first (that would need checking pending work from config.CurrentState
	// alone, without an actions-bearing orchestrator).
	sourceOffset, destinationOffset, closeOffsets, err := buildOffsets(g)
	if err != nil {
		return fmt.Errorf("failed to connect to source/destination clusters: %w", err)
	}
	defer func() { _ = closeOffsets() }()

	gatewayService, err := buildGateway(g)
	if err != nil {
		return fmt.Errorf("failed to build gateway service: %w", err)
	}

	clusterLinkService, err := buildClusterLink(g)
	if err != nil {
		return fmt.Errorf("failed to build cluster-link service: %w", err)
	}
	restCreds, err := g.RestCredentials()
	if err != nil {
		return fmt.Errorf("failed to resolve cluster-link REST credentials: %w", err)
	}

	actions := tbm.NewTBMActions(sourceOffset, destinationOffset, gatewayService, clusterLinkService)
	actions.SetRolloutTimeout(rolloutTimeoutOverride)
	actions.SetHotReloadTimeout(hotReloadTimeoutOverride)
	orchestrator := tbm.NewTBMOrchestrator(config, actions, tbmState, tbmStateFile)

	// An already-complete migration short-circuits before any live I/O: a resumed
	// run with the same manifest must succeed offline, since the source, target,
	// or gateway may already be torn down once the migration is done. The
	// reconcile plan below therefore runs only when there is pending work.
	if !orchestrator.HasPendingWork() {
		cmd.Printf("✅ TBM migration already complete: %s\n", config.MigrationId)
		return nil
	}

	// Produce the reconcile plan for this migration: the engine reads the live
	// Gateway CR + source/target/cluster-link state, renders its own report to
	// the command's writer, and returns the promote topic list plus the fence
	// and switchover rules artifacts. err here is an I/O failure (the plan
	// could not be produced); an infeasible-but-reachable plan is not an
	// error — it surfaces as res.Refused with res.Reasons, which the
	// initialize transition turns into a failed run.
	//
	// This runs on every invocation, even a pure resume past initialize — its
	// result is only consumed by onInitialize (skipped via canTransition once
	// initialize has already completed), so a resume pays for a live reconcile
	// whose output then goes unused. Revisit if that cost matters in practice.
	res, err := reconcile(cmd.Context(), g, migplan.WithOutput(cmd.OutOrStdout()))
	if err != nil {
		return fmt.Errorf("failed to produce the reconcile plan: %w", err)
	}

	if err := orchestrator.Execute(context.Background(), res, int64(g.Spec.DefaultPolicies.LagThreshold), g.Spec.DefaultPolicies.DetectUnroutedProducersDuration, restCreds.Authenticator()); err != nil {
		return fmt.Errorf("failed to execute tbm migration: %w", err)
	}

	cmd.Printf("✅ TBM migration completed: %s\n", config.MigrationId)
	return nil
}

// resolveTBMConfig implements the identity & drift rule: no entry for this
// name -> create fresh at uninitialized (recording namespace/gatewayName/clusterId/clusterRestEndpoint/clusterLinkName,
// which never change for this migration again — protected by the same
// unconditional hash-drift refusal as every other manifest field); hash
// matches -> resume from the persisted state; hash differs -> refuse
// unconditionally, regardless of CurrentState. There is no override.
func resolveTBMConfig(state *tbm.TBMState, migrationId, hash, namespace, gatewayName, clusterId, clusterRestEndpoint, clusterLinkName string) (*tbm.TBMConfig, error) {
	existing, err := state.GetMigrationById(migrationId)
	if err != nil {
		return &tbm.TBMConfig{
			MigrationId:         migrationId,
			CurrentState:        tbm.StateUninitialized,
			ManifestHash:        hash,
			K8sNamespace:        namespace,
			InitialCrName:       gatewayName,
			ClusterId:           clusterId,
			ClusterRestEndpoint: clusterRestEndpoint,
			ClusterLinkName:     clusterLinkName,
		}, nil
	}

	if existing.ManifestHash != hash {
		return nil, fmt.Errorf( //nolint:staticcheck // multi-line operator guidance
			"the manifest for migration %q has changed since it was last run (recorded hash %s, current hash %s).\n"+
				"A migration's manifest must not change once started. Use a new metadata.name for a new migration, "+
				"or revert this file to match the run already in progress.",
			migrationId, existing.ManifestHash, hash)
	}

	return existing, nil
}

// buildOffsetProviders opens real Kafka connections to the source and
// destination clusters described in the manifest, for wait_for_lags. Mirrors
// internal/services/migplan/run.go's unexported buildSourceTopicLister/
// buildTargetTopicLister auth-resolution pattern (same manifest, same
// credential resolution), swapped to client.NewKafkaClient (a sarama.Client)
// wrapped in offset.NewOffsetService instead of client.NewKafkaAdmin wrapped
// in a topic lister.
func buildOffsetProviders(g *manifest.GatewayMigration) (offset.Provider, offset.Provider, func() error, error) {
	srcCreds, errs := g.SourceCredentials()
	if len(errs) > 0 {
		return nil, nil, nil, manifest.JoinProblems("spec.source.credentials", errs)
	}
	srcConn := types.MigrateConn(g.Spec.Source.BootstrapServers, srcCreds)
	srcClient, err := newKafkaClientForConn(srcConn)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("connecting to source cluster: %w", err)
	}

	if g.Spec.Target.Kafka == nil {
		_ = srcClient.Close()
		return nil, nil, nil, fmt.Errorf("spec.target.kafka: required")
	}
	destCreds, errs := g.DestinationKafkaCredentials()
	if len(errs) > 0 {
		_ = srcClient.Close()
		return nil, nil, nil, manifest.JoinProblems("spec.target.kafka.credentials", errs)
	}
	destConn := types.MigrateConn(g.Spec.Target.Kafka.BootstrapServers, destCreds)
	// Backward-compat parity with migration execute (and migplan): a
	// Confluent Cloud destination that supplies neither ca_cert nor an
	// explicit tls signal must still dial SASL_SSL, not cleartext
	// SASL_PLAINTEXT — the destination is a managed cluster, always TLS.
	if sp := destConn.AuthMethod.SASLPlain; sp != nil && sp.CACert == "" && !sp.UseTLS {
		sp.UseTLS = true
	}
	destClient, err := newKafkaClientForConn(destConn)
	if err != nil {
		_ = srcClient.Close()
		return nil, nil, nil, fmt.Errorf("connecting to destination cluster: %w", err)
	}

	closeFn := func() error {
		return errors.Join(srcClient.Close(), destClient.Close())
	}
	return offset.NewOffsetService(srcClient), offset.NewOffsetService(destClient), closeFn, nil
}

// newKafkaClientForConn resolves conn's auth option and dials it as a
// sarama.Client, for offset.NewOffsetService. Mirrors migplan's
// buildTopicLister auth resolution.
func newKafkaClientForConn(conn types.KafkaSourceConn) (sarama.Client, error) {
	authType, err := conn.GetSelectedAuthType()
	if err != nil {
		return nil, fmt.Errorf("determining auth type: %w", err)
	}
	region := ""
	if authType == types.AuthTypeIAM && conn.AuthMethod.IAM != nil {
		region = conn.AuthMethod.IAM.Region
	}
	authOpt, err := client.AdminOptionForAuthMethod(authType, conn.AuthMethod, conn.InsecureSkipTLSVerify)
	if err != nil {
		return nil, fmt.Errorf("resolving auth option: %w", err)
	}
	return client.NewKafkaClient(conn.BootstrapServers, region, authOpt)
}
