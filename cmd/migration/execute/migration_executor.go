package execute

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/confluentinc/kcp/internal/client"
	"github.com/confluentinc/kcp/internal/services/clusterlink"
	"github.com/confluentinc/kcp/internal/services/gateway"
	"github.com/confluentinc/kcp/internal/services/migplan"
	"github.com/confluentinc/kcp/internal/services/migration"
	"github.com/confluentinc/kcp/internal/services/offset"
	"github.com/confluentinc/kcp/internal/targets"
	"github.com/confluentinc/kcp/internal/types"
)

type MigrationExecutorOpts struct {
	MigrationConfig    migration.MigrationConfig
	LagThreshold       int64
	ClusterBootstrap   string
	SourceBootstrap    string
	AWSRegion          string
	AuthType           types.AuthType
	SaslScramUsername  string
	SaslScramPassword  string
	SaslScramMechanism string
	SaslPlainUsername  string
	SaslPlainPassword  string
	// SaslPlainUseTLS selects SASL_SSL over the system trust store when no
	// ca_cert is supplied. Without it a `tls: true` source block would be
	// silently downgraded to cleartext SASL_PLAINTEXT.
	SaslPlainUseTLS bool
	TlsCaCert       string
	TlsClientCert   string
	TlsClientKey    string
	// DestAuthType / DestAuthMethod select and configure the destination Kafka
	// leg's auth, mapped via client.AdminOptionForAuthMethod — the same mapper
	// the source leg uses. Carrying the full per-method config (rather than a
	// scalar key/secret pair) is what makes SASL/SCRAM, mTLS and unauthenticated
	// destinations possible, not just SASL/PLAIN.
	DestAuthType   types.AuthType
	DestAuthMethod types.AuthMethodConfig
	// RestCreds authenticates the destination cluster-link REST surface. It is
	// the full resolved credential (basic, bearer, mtls, or the api_key form),
	// not just an api_key/api_secret pair, and is kept separate from the Kafka
	// leg's DestAuthMethod because spec.clusterLink.linkCredentials may name a
	// different principal — the Kafka leg must not silently authenticate as the
	// REST one.
	RestCreds *targets.Credentials
	// TLS trust is per leg. One shared boolean meant relaxing verification for a
	// self-signed source also stopped verifying the destination connections,
	// which carry the destination API key as SASL/PLAIN and as HTTP Basic.
	SourceInsecureSkipTLSVerify    bool
	DestKafkaInsecureSkipTLSVerify bool
	// RolloutTimeout bounds the gateway-readiness wait during fence and
	// switch. A value of 0 means no deadline — the wait runs until the
	// operator reports ready or the user cancels.
	RolloutTimeout time.Duration
	// HotReloadTimeout bounds the per-pod configId verification used when the
	// gateway supports hot-reload. 0 means use gateway.DefaultHotReloadTimeout;
	// it is deliberately never unbounded, since a hot-reload moves no Kubernetes
	// signal to wait on.
	HotReloadTimeout time.Duration
	// GatewayConfigPort is the port serving the gateway's /config endpoint.
	// 0 means use the configured value, falling back to the gateway default.
	GatewayConfigPort int
	// PromoteBatchSize caps how many mirror topics are promoted per batch. A
	// value of 0 means unlimited (all at once); >0 processes topics in
	// synchronous batches of this size, waiting for each batch to reach
	// STOPPED before promoting the next.
	PromoteBatchSize int
	// RunReportPath, when non-empty, is where per-stage timings are written as
	// JSON. Empty (the default) disables the report.
	RunReportPath string
	// ReconcileResult is the migplan.Result the command layer computed live,
	// via migplan.Reconcile, on THIS invocation (see cmd_migration_execute.go).
	// The AAO FSM always starts at uninitialized and onInitialize consumes it.
	ReconcileResult *migplan.Result
}

type MigrationExecutor struct {
	opts MigrationExecutorOpts
	deps aaoDependencies
}

// offsetProviderCloser is an offset source the executor closes when Run ends.
type offsetProviderCloser interface {
	offset.Provider
	Close() error
}

// aaoDependencies builds the AAO branch's downstream services. Run takes them
// from here rather than constructing them inline so this package's tests can
// substitute stubs, as runTBMBranch's builder functions allow for the dynamic
// branch.
type aaoDependencies struct {
	sourceOffset       func(MigrationExecutorOpts) (offsetProviderCloser, error)
	destinationOffset  func(MigrationExecutorOpts) (offsetProviderCloser, error)
	gatewayService     func(kubeConfigPath string) gateway.Service
	clusterLinkService func(clusterlink.HTTPClient) clusterlink.Service
}

// liveAAODependencies dials the real source and destination Kafka clusters,
// Kubernetes, and the cluster-link REST endpoint.
var liveAAODependencies = aaoDependencies{
	sourceOffset:      createSourceOffset,
	destinationOffset: createDestinationOffset,
	gatewayService: func(kubeConfigPath string) gateway.Service {
		return gateway.NewK8sService(kubeConfigPath)
	},
	clusterLinkService: func(httpClient clusterlink.HTTPClient) clusterlink.Service {
		return clusterlink.NewConfluentCloudService(httpClient)
	},
}

func NewMigrationExecutor(opts MigrationExecutorOpts) *MigrationExecutor {
	return newMigrationExecutorWithDeps(opts, liveAAODependencies)
}

func newMigrationExecutorWithDeps(opts MigrationExecutorOpts, deps aaoDependencies) *MigrationExecutor {
	return &MigrationExecutor{opts: opts, deps: deps}
}

func (m *MigrationExecutor) Run() error {
	config := m.opts.MigrationConfig
	ctx := context.Background()

	sourceOffset, err := m.deps.sourceOffset(m.opts)
	if err != nil {
		return err
	}
	defer func() { _ = sourceOffset.Close() }()

	destinationOffset, err := m.deps.destinationOffset(m.opts)
	if err != nil {
		return err
	}
	defer func() { _ = destinationOffset.Close() }()

	// REST client for the destination cluster-link API: presents whichever TLS
	// trust (and, for mTLS, client cert) the resolved REST credentials carry.
	httpClient, err := m.opts.RestCreds.HTTPClient()
	if err != nil {
		return fmt.Errorf("building destination REST client: %w", err)
	}

	gatewayService := m.deps.gatewayService(config.KubeConfigPath)
	clusterLinkService := m.deps.clusterLinkService(httpClient)
	actions := migration.NewMigrationActionsWithOffsets(gatewayService, clusterLinkService, sourceOffset, destinationOffset)
	actions.SetRolloutTimeout(m.opts.RolloutTimeout)
	actions.SetHotReloadTimeout(m.opts.HotReloadTimeout)
	actions.SetPromoteBatchSize(m.opts.PromoteBatchSize)

	// An explicit gateway config port (manifest policy or flag) overrides the
	// config's default.
	if m.opts.GatewayConfigPort > 0 {
		config.GatewayConfigPort = m.opts.GatewayConfigPort
	}

	// The FSM always starts at uninitialized (start-from-zero) — there is no
	// resume position: reconcile (run every invocation) + idempotent applies
	// determine what happens.
	orchestrator := migration.NewMigrationOrchestrator(
		&config,
		actions,
	)

	// Gateway capability is NOT resolved here: config.FenceYAML/SwitchoverYAML
	// aren't populated until Execute reaches the fence/switch step, so a blanket
	// pre-Execute check would have nothing to derive a CR from. Capability
	// resolves lazily instead, at most once per process, from whichever of
	// FenceGateway/SwitchGateway orchestrator.Execute reaches first (mirrors
	// tbm.TBMActions.ensureGatewayCapability).

	// The run report is stamped on the way out whatever the outcome: a migration
	// that failed — or one whose lag never converged — is a result worth
	// recording, and a caller timing the run needs the stages that did complete.
	runReport := migration.NewRunReportRecorder(
		m.opts.RunReportPath,
		config.MigrationId,
		len(config.Topics),
		m.opts.LagThreshold,
		orchestrator.CurrentState(),
	)
	orchestrator.SetRunReportRecorder(runReport)
	var execErr error
	defer func() { runReport.Finish(orchestrator.CurrentState(), execErr) }()

	// The cluster-link REST API authenticates with the REST credentials, which
	// may name a broader principal than the destination KAFKA credentials — the
	// two are kept separate so neither leg silently authenticates as the other.
	restAuth := m.opts.RestCreds.Authenticator()
	clusterLinkConfig := migration.BuildClusterLinkConfig(&config, restAuth)

	// The consumer-offset-sync pause runs INSIDE the FSM (the
	// pause_offset_sync stage, right after fencing) so destination offsets
	// stay fresh through the lag and fence phases instead of going stale for
	// the whole run. Only the restore below remains a bookend.
	if execErr = orchestrator.Execute(ctx, m.opts.LagThreshold, restAuth, m.opts.ReconcileResult); execErr != nil {
		migration.WarnIfPausedOnExecuteFailure(&config, execErr)
		return fmt.Errorf("failed to execute migration: %w", execErr)
	}

	// Post-execute bookend: restore consumer.offset.sync.enable. Soft-fail
	// so a restore error does not roll back a successful switchover.
	migration.RestoreOffsetSync(ctx, clusterLinkService, clusterLinkConfig, &config)

	fmt.Printf("✅ Migration completed: %s\n", config.MigrationId)
	return nil
}

// sourceClusterAuth builds the source ClusterAuth from the execute flags.
// TlsCaCert is the CA that verifies the source broker's TLS server certificate
// and is applied to EVERY TLS-fronted auth method — SASL/SCRAM and SASL/PLAIN over
// TLS (SASL_SSL), one-way unauthenticated TLS, and mTLS — not only the mTLS path.
// For SASL/PLAIN, supplying it selects SASL_SSL over cleartext SASL_PLAINTEXT.
func sourceClusterAuth(opts MigrationExecutorOpts) types.ClusterAuth {
	clusterAuth := types.ClusterAuth{}
	switch opts.AuthType {
	case types.AuthTypeSASLSCRAM:
		clusterAuth.AuthMethod.SASLScram = &types.SASLScramConfig{
			Use:       true,
			Username:  opts.SaslScramUsername,
			Password:  opts.SaslScramPassword,
			Mechanism: opts.SaslScramMechanism,
			CACert:    opts.TlsCaCert,
		}
	case types.AuthTypeTLS:
		clusterAuth.AuthMethod.TLS = &types.TLSConfig{
			Use:        true,
			CACert:     opts.TlsCaCert,
			ClientCert: opts.TlsClientCert,
			ClientKey:  opts.TlsClientKey,
		}
	case types.AuthTypeSASLPlain:
		clusterAuth.AuthMethod.SASLPlain = &types.SASLPlainConfig{
			Use:      true,
			Username: opts.SaslPlainUsername,
			Password: opts.SaslPlainPassword,
			CACert:   opts.TlsCaCert,
			UseTLS:   opts.SaslPlainUseTLS,
		}
	case types.AuthTypeIAM:
		clusterAuth.AuthMethod.IAM = &types.IAMConfig{Use: true}
	case types.AuthTypeUnauthenticatedTLS:
		clusterAuth.AuthMethod.UnauthenticatedTLS = &types.UnauthenticatedTLSConfig{Use: true, CACert: opts.TlsCaCert}
	case types.AuthTypeUnauthenticatedPlaintext:
		clusterAuth.AuthMethod.UnauthenticatedPlaintext = &types.UnauthenticatedPlaintextConfig{Use: true}
	}
	return clusterAuth
}

func createSourceOffset(o MigrationExecutorOpts) (offsetProviderCloser, error) {
	authType := o.AuthType
	brokerAddresses := strings.Split(o.SourceBootstrap, ",")

	region := o.AWSRegion

	clusterAuth := sourceClusterAuth(o)

	// skipTLSVerify is threaded through the mapper into every TLS path, so no
	// separate WithInsecureSkipVerify() override is needed.
	authOpt, err := client.AdminOptionForAuthMethod(authType, clusterAuth.AuthMethod, o.SourceInsecureSkipTLSVerify)
	if err != nil {
		return nil, fmt.Errorf("resolving source auth option: %w", err)
	}
	opts := []client.AdminOption{authOpt}

	slog.Debug("connecting to source cluster",
		"brokers", len(brokerAddresses),
		"auth_type", authType,
		"region", region,
		"insecure_skip_tls_verify", o.SourceInsecureSkipTLSVerify,
	)
	sourceClient, err := client.NewKafkaClient(brokerAddresses, region, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to source cluster: %w", err)
	}
	slog.Debug("source cluster connected")

	return offset.NewOffsetService(sourceClient), nil
}

func createDestinationOffset(o MigrationExecutorOpts) (offsetProviderCloser, error) {
	ccBrokers := strings.Split(o.ClusterBootstrap, ",")
	slog.Debug("connecting to destination cluster",
		"brokers", len(ccBrokers),
		"auth_type", o.DestAuthType,
		"insecure_skip_tls_verify", o.DestKafkaInsecureSkipTLSVerify,
	)
	// The credential is the destination KAFKA one, not the REST one — they may
	// differ. skipTLSVerify is threaded through the mapper into every TLS path,
	// mirroring createSourceOffset.
	authOpt, err := client.AdminOptionForAuthMethod(o.DestAuthType, o.DestAuthMethod, o.DestKafkaInsecureSkipTLSVerify)
	if err != nil {
		return nil, fmt.Errorf("resolving destination auth option: %w", err)
	}
	destClient, err := client.NewKafkaClient(ccBrokers, "", authOpt)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to destination cluster: %w", err)
	}
	slog.Debug("destination cluster connected")

	return offset.NewOffsetService(destClient), nil
}
