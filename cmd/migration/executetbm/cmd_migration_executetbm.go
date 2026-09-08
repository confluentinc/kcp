package executetbm

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/IBM/sarama"
	"github.com/confluentinc/kcp/internal/client"
	"github.com/confluentinc/kcp/internal/manifest"
	"github.com/confluentinc/kcp/internal/services/gateway"
	"github.com/confluentinc/kcp/internal/services/migplan"
	"github.com/confluentinc/kcp/internal/services/migration/tbm"
	"github.com/confluentinc/kcp/internal/services/offset"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/confluentinc/kcp/internal/utils"
	"github.com/spf13/cobra"
)

var (
	manifestFile         string
	tbmStateFile         string
	lagThresholdOverride int
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

const executeTBMLong = `Execute a Topic-Batch Migration (TBM) run.

This is a scaffold: every FSM transition except initialize and wait_for_lags is
currently a noop (it sleeps to simulate real execution timing, then logs).
initialize validates the already-computed reconcile plan (see the migplan
package) and captures its promote topic list plus fence/switchover artifacts
for later transitions to consume. wait_for_lags polls source and destination
Kafka offsets for those topics until every one is under spec.defaultPolicies.
lagThreshold (overridable per run with --lag-threshold). This command exists
to validate the state-machine shape and command wiring ahead of the real
per-batch migration logic described in the TBM design proposal.

The migration is identified by metadata.name in the GatewayMigration manifest at
--migration-yaml — there is no separate init step and no --migration-id flag. The first
run for a given name creates a fresh entry in the TBM state file; a later run with the
SAME manifest content resumes from the last completed step. A later run with a CHANGED
manifest for the SAME name is refused outright, with no override — a genuinely new
migration needs a new metadata.name.`

// NewMigrationExecuteTBMCmd builds the `execute-tbm` command bound to the real
// reconciliation engine and real Kafka connections.
func NewMigrationExecuteTBMCmd() *cobra.Command {
	return newExecuteTBMCmd(migplan.Reconcile, buildOffsetProviders)
}

// newExecuteTBMCmd builds the command with the reconcile entry point and
// offset-provider builder injected, so tests can pass stubs instead of the
// live engine and live Kafka connections.
func newExecuteTBMCmd(reconcile reconcileFunc, buildOffsets offsetProvidersFunc) *cobra.Command {
	cmd := &cobra.Command{
		Use:           "execute-tbm",
		Short:         "Execute a Topic-Batch Migration run (scaffold: noop transitions)",
		Long:          executeTBMLong,
		Example:       `  kcp migration execute-tbm --migration-yaml gateway-migration.yaml --tbm-state-file tbm-state.json`,
		Hidden:        true, // scaffold: initialize/wait_for_lags are real, other transitions still noop; kept in the binary but not user-facing (cascades to --help and gen-docs)
		SilenceErrors: true,
		SilenceUsage:  true,
		Args:          cobra.NoArgs,
		PreRunE:       func(c *cobra.Command, _ []string) error { return utils.BindEnvToFlags(c) },
		RunE:          func(c *cobra.Command, _ []string) error { return runMigrationExecuteTBM(c, reconcile, buildOffsets) },
	}

	cmd.Flags().StringVar(&manifestFile, "migration-yaml", "", "Path to the GatewayMigration manifest describing this migration.")
	cmd.Flags().StringVar(&tbmStateFile, "tbm-state-file", "", "Path to the TBM state file. Created if it doesn't exist.")
	cmd.Flags().IntVar(&lagThresholdOverride, "lag-threshold", 0, "Override spec.defaultPolicies.lagThreshold: total replication lag (sum of all partition lags) tolerated before proceeding.")

	_ = cmd.MarkFlagRequired("migration-yaml")
	_ = cmd.MarkFlagRequired("tbm-state-file")

	return cmd
}

func runMigrationExecuteTBM(cmd *cobra.Command, reconcile reconcileFunc, buildOffsets offsetProvidersFunc) error {
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

	config, err := resolveTBMConfig(tbmState, g.Metadata.Name, hash, g.Spec.Gateway.Namespace, g.Spec.Gateway.CrName)
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

	// TODO(task 4): replace this placeholder with the real gatewayServiceFunc
	// injection machinery (kubeconfig-backed gateway.NewK8sService wiring,
	// consistent with reconcileFunc/offsetProvidersFunc above). NewTBMActions
	// now requires a gateway.Service; this satisfies the 3-argument
	// constructor without a live Kubernetes connection, which is out of scope
	// for this change.
	actions := tbm.NewTBMActions(sourceOffset, destinationOffset, gateway.NewK8sService(""))

	// Produce the reconcile plan for this migration: the engine reads the live
	// Gateway CR + source/target/cluster-link state, renders its own report, and
	// returns the promote topic list plus the fence and switchover rules
	// artifacts. err here is an I/O failure (the plan could not be produced); an
	// infeasible-but-reachable plan is not an error — it surfaces as res.Refused
	// with res.Reasons, which the initialize transition turns into a failed run.
	//
	// This runs on every invocation, even a pure resume past initialize — its
	// result is only consumed by onInitialize (skipped via canTransition once
	// initialize has already completed), so a resume pays for a live reconcile
	// whose output then goes unused. Revisit if that cost matters in practice.
	res, err := reconcile(cmd.Context(), g)
	if err != nil {
		return fmt.Errorf("failed to produce the reconcile plan: %w", err)
	}

	orchestrator := tbm.NewTBMOrchestrator(config, actions, tbmState, tbmStateFile)

	if !orchestrator.HasPendingWork() {
		cmd.Printf("✅ TBM migration already complete: %s\n", config.MigrationId)
		return nil
	}

	if err := orchestrator.Execute(context.Background(), res, int64(g.Spec.DefaultPolicies.LagThreshold)); err != nil {
		return fmt.Errorf("failed to execute tbm migration: %w", err)
	}

	cmd.Printf("✅ TBM migration completed: %s\n", config.MigrationId)
	return nil
}

// resolveTBMConfig implements the identity & drift rule: no entry for this
// name -> create fresh at uninitialized (recording namespace/gatewayName,
// which never change for this migration again — protected by the same
// unconditional hash-drift refusal as every other manifest field); hash
// matches -> resume from the persisted state; hash differs -> refuse
// unconditionally, regardless of CurrentState. There is no override.
func resolveTBMConfig(state *tbm.TBMState, migrationId, hash, namespace, gatewayName string) (*tbm.TBMConfig, error) {
	existing, err := state.GetMigrationById(migrationId)
	if err != nil {
		return &tbm.TBMConfig{
			MigrationId:   migrationId,
			CurrentState:  tbm.StateUninitialized,
			ManifestHash:  hash,
			K8sNamespace:  namespace,
			InitialCrName: gatewayName,
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
