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
	"github.com/confluentinc/kcp/internal/services/migration"
	"github.com/confluentinc/kcp/internal/services/offset"
	"github.com/confluentinc/kcp/internal/targets"
	"github.com/confluentinc/kcp/internal/types"
)

type MigrationExecutorOpts struct {
	MigrationStateFile string
	MigrationState     migration.MigrationState
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
	// leg's DestAuthMethod because an explicit spec.target.kafka.restCredentials
	// may name a different principal — the Kafka leg must not silently
	// authenticate as the REST one.
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
	// 0 means use the persisted value, falling back to the gateway default.
	GatewayConfigPort int
	// PromoteBatchSize caps how many mirror topics are promoted per batch. A
	// value of 0 means unlimited (all at once); >0 processes topics in
	// synchronous batches of this size, waiting for each batch to reach
	// STOPPED before promoting the next.
	PromoteBatchSize int
	// RunReportPath, when non-empty, is where per-stage timings are written as
	// JSON. Empty (the default) disables the report.
	RunReportPath string
}

type MigrationExecutor struct {
	opts MigrationExecutorOpts
}

func NewMigrationExecutor(opts MigrationExecutorOpts) *MigrationExecutor {
	return &MigrationExecutor{
		opts: opts,
	}
}

func (m *MigrationExecutor) Run() error {
	config := m.opts.MigrationConfig
	ctx := context.Background()

	// Create source Kafka client (MSK)
	sourceOffset, err := m.createSourceOffset(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = sourceOffset.Close() }()

	// Create destination Kafka client (CC)
	destinationOffset, err := m.createDestinationOffset()
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

	gatewayService := gateway.NewK8sService(config.KubeConfigPath)
	clusterLinkService := clusterlink.NewConfluentCloudService(httpClient)
	actions := migration.NewMigrationActionsWithOffsets(gatewayService, clusterLinkService, sourceOffset, destinationOffset)
	actions.SetRolloutTimeout(m.opts.RolloutTimeout)
	actions.SetHotReloadTimeout(m.opts.HotReloadTimeout)
	actions.SetPromoteBatchSize(m.opts.PromoteBatchSize)

	// An explicit --gateway-config-port overrides whatever init recorded.
	if m.opts.GatewayConfigPort > 0 {
		config.GatewayConfigPort = m.opts.GatewayConfigPort
	}

	// The orchestrator is the single writer for migration state. Build it up
	// front — both so its PersistState can back the offset-sync bookends, and
	// so its FSM tells us whether there is any step left to run before
	// anything below touches the gateway. A migration that already switched
	// over must stay a side-effect-free no-op on re-run: without this check
	// every re-run wrote a fresh spec.configId to the production gateway and
	// waited on it, real cluster effects for a command that had nothing left
	// to do.
	orchestrator := migration.NewMigrationOrchestrator(
		&config,
		actions,
		&m.opts.MigrationState,
		m.opts.MigrationStateFile,
	)

	if !orchestrator.HasPendingWork() {
		fmt.Printf("✅ Migration already complete: %s\n", config.MigrationId)
		return nil
	}

	// Re-derive the gateway verification capability against the live cluster.
	// The mode recorded at init is only what the operator was told to expect —
	// the cluster can be upgraded, or rolled back, in between, and a downgrade
	// matters for correctness: writing spec.configId to a CRD that no longer
	// declares it makes server-side apply fail outright.
	if err := actions.ResolveGatewayCapability(ctx, &config); err != nil {
		return err
	}

	// Prove hot-reload actually works before anything blocks traffic. The gateway
	// gates its config watcher on an Enterprise licence, and when that gate is
	// shut CFK still reports success while the gateway serves stale config — so
	// this is the only place the failure is visible, and the only safe time to
	// look is before fencing.
	if err := actions.VerifyHotReloadCapability(ctx, &config); err != nil {
		return err
	}

	// The run report is stamped on the way out whatever the outcome: a migration
	// that failed — or one whose lag never converged — is a result worth
	// recording, and a caller timing the run needs the stages that did complete.
	runReport := migration.NewRunReportRecorder(
		m.opts.RunReportPath,
		config.MigrationId,
		len(config.Topics),
		m.opts.LagThreshold,
		config.CurrentState,
	)
	orchestrator.SetRunReportRecorder(runReport)
	var execErr error
	defer func() { runReport.Finish(config.CurrentState, execErr) }()

	// The cluster-link REST API authenticates with the REST credentials, which
	// may name a broader principal than the destination KAFKA credentials — the
	// two are kept separate so neither leg silently authenticates as the other.
	restAuth := m.opts.RestCreds.Authenticator()
	clusterLinkConfig := migration.BuildClusterLinkConfig(&config, restAuth)

	// The consumer-offset-sync pause runs INSIDE the FSM (the
	// pause_offset_sync stage, right after fencing) so destination offsets
	// stay fresh through the lag and fence phases instead of going stale for
	// the whole run. Only the restore below remains a bookend.
	if execErr = orchestrator.Execute(ctx, m.opts.LagThreshold, restAuth); execErr != nil {
		migration.WarnIfPausedOnExecuteFailure(&config, execErr)
		return fmt.Errorf("failed to execute migration: %w", execErr)
	}

	// Post-execute bookend: restore consumer.offset.sync.enable. Soft-fail
	// so a restore error does not roll back a successful switchover.
	migration.RestoreOffsetSync(ctx, clusterLinkService, clusterLinkConfig, &config, orchestrator.PersistState)

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

func (m *MigrationExecutor) createSourceOffset(_ context.Context) (*offset.Service, error) {
	authType := m.opts.AuthType
	brokerAddresses := strings.Split(m.opts.SourceBootstrap, ",")

	region := m.opts.AWSRegion

	clusterAuth := sourceClusterAuth(m.opts)

	// skipTLSVerify is threaded through the mapper into every TLS path, so no
	// separate WithInsecureSkipVerify() override is needed.
	authOpt, err := client.AdminOptionForAuthMethod(authType, clusterAuth.AuthMethod, m.opts.SourceInsecureSkipTLSVerify)
	if err != nil {
		return nil, fmt.Errorf("resolving source auth option: %w", err)
	}
	opts := []client.AdminOption{authOpt}

	slog.Debug("connecting to source cluster",
		"brokers", len(brokerAddresses),
		"auth_type", authType,
		"region", region,
		"insecure_skip_tls_verify", m.opts.SourceInsecureSkipTLSVerify,
	)
	sourceClient, err := client.NewKafkaClient(brokerAddresses, region, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to source cluster: %w", err)
	}
	slog.Debug("source cluster connected")

	return offset.NewOffsetService(sourceClient), nil
}

func (m *MigrationExecutor) createDestinationOffset() (*offset.Service, error) {
	ccBrokers := strings.Split(m.opts.ClusterBootstrap, ",")
	slog.Debug("connecting to destination cluster",
		"brokers", len(ccBrokers),
		"auth_type", m.opts.DestAuthType,
		"insecure_skip_tls_verify", m.opts.DestKafkaInsecureSkipTLSVerify,
	)
	// The credential is the destination KAFKA one, not the REST one — they may
	// differ. skipTLSVerify is threaded through the mapper into every TLS path,
	// mirroring createSourceOffset.
	authOpt, err := client.AdminOptionForAuthMethod(m.opts.DestAuthType, m.opts.DestAuthMethod, m.opts.DestKafkaInsecureSkipTLSVerify)
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
