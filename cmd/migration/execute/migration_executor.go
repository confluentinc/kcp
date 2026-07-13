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
	"github.com/confluentinc/kcp/internal/types"
)

type MigrationExecutorOpts struct {
	MigrationStateFile    string
	MigrationState        migration.MigrationState
	MigrationConfig       migration.MigrationConfig
	LagThreshold          int64
	ClusterApiKey         string
	ClusterApiSecret      string
	ClusterBootstrap      string
	SourceBootstrap       string
	AWSRegion             string
	AuthType              types.AuthType
	SaslScramUsername     string
	SaslScramPassword     string
	SaslScramMechanism    string
	SaslPlainUsername     string
	SaslPlainPassword     string
	TlsCaCert             string
	TlsClientCert         string
	TlsClientKey          string
	ClusterRestCACert     string
	InsecureSkipTLSVerify bool
	// RolloutTimeout bounds the gateway-readiness wait during fence and
	// switch. A value of 0 means no deadline — the wait runs until the
	// operator reports ready or the user cancels.
	RolloutTimeout time.Duration
	// PromoteBatchSize caps how many mirror topics are promoted per batch. A
	// value of 0 means unlimited (all at once); >0 processes topics in
	// synchronous batches of this size, waiting for each batch to reach
	// STOPPED before promoting the next.
	PromoteBatchSize int
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

	// REST client for the destination cluster-link API: trusts a private CA
	// (--cluster-rest-ca-cert) and/or skips verification, else system roots (CC public CA).
	httpClient, err := migration.NewRESTHTTPClient(m.opts.ClusterRestCACert, m.opts.InsecureSkipTLSVerify)
	if err != nil {
		return fmt.Errorf("building destination REST client: %w", err)
	}

	gatewayService := gateway.NewK8sService(config.KubeConfigPath)
	clusterLinkService := clusterlink.NewConfluentCloudService(httpClient)
	actions := migration.NewMigrationActionsWithOffsets(gatewayService, clusterLinkService, sourceOffset, destinationOffset)
	actions.SetRolloutTimeout(m.opts.RolloutTimeout)
	actions.SetPromoteBatchSize(m.opts.PromoteBatchSize)

	// The orchestrator is the single writer for migration state. Build it up
	// front so its PersistState can back the offset-sync bookends too, rather
	// than a parallel write closure.
	orchestrator := migration.NewMigrationOrchestrator(
		&config,
		actions,
		&m.opts.MigrationState,
		m.opts.MigrationStateFile,
	)

	clusterLinkConfig := migration.BuildClusterLinkConfig(&config, m.opts.ClusterApiKey, m.opts.ClusterApiSecret)

	// The consumer-offset-sync pause runs INSIDE the FSM (the
	// pause_offset_sync stage, right after fencing) so destination offsets
	// stay fresh through the lag and fence phases instead of going stale for
	// the whole run. Only the restore below remains a bookend.
	if err := orchestrator.Execute(ctx, m.opts.LagThreshold, m.opts.ClusterApiKey, m.opts.ClusterApiSecret); err != nil {
		migration.WarnIfPausedOnExecuteFailure(&config, err)
		return fmt.Errorf("failed to execute migration: %w", err)
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
	authOpt, err := client.AdminOptionForAuthMethod(authType, clusterAuth.AuthMethod, m.opts.InsecureSkipTLSVerify)
	if err != nil {
		return nil, fmt.Errorf("resolving source auth option: %w", err)
	}
	opts := []client.AdminOption{authOpt}

	slog.Debug("connecting to source cluster",
		"brokers", len(brokerAddresses),
		"auth_type", authType,
		"region", region,
		"insecure_skip_tls_verify", m.opts.InsecureSkipTLSVerify,
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
	slog.Debug("connecting to destination cluster (Confluent Cloud)",
		"brokers", len(ccBrokers),
		"insecure_skip_tls_verify", m.opts.InsecureSkipTLSVerify,
	)
	// Confluent Cloud: SASL/PLAIN over TLS (SASL_SSL), public CA (no ca_cert).
	destOpts := []client.AdminOption{client.WithSASLPlainAuth(m.opts.ClusterApiKey, m.opts.ClusterApiSecret, "", m.opts.InsecureSkipTLSVerify)}
	destClient, err := client.NewKafkaClient(ccBrokers, "", destOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to destination cluster: %w", err)
	}
	slog.Debug("destination cluster connected")

	return offset.NewOffsetService(destClient), nil
}
