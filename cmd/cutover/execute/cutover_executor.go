package execute

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/confluentinc/kcp/internal/client"
	"github.com/confluentinc/kcp/internal/services/clusterlink"
	"github.com/confluentinc/kcp/internal/services/cutover"
	"github.com/confluentinc/kcp/internal/services/gateway"
	"github.com/confluentinc/kcp/internal/services/offset"
	"github.com/confluentinc/kcp/internal/types"
)

type CutoverExecutorOpts struct {
	CutoverStateFile      string
	CutoverState          cutover.CutoverState
	CutoverConfig         cutover.CutoverConfig
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
}

type CutoverExecutor struct {
	opts CutoverExecutorOpts
}

func NewCutoverExecutor(opts CutoverExecutorOpts) *CutoverExecutor {
	return &CutoverExecutor{
		opts: opts,
	}
}

func (m *CutoverExecutor) Run() error {
	config := m.opts.CutoverConfig
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
	httpClient, err := cutover.NewRESTHTTPClient(m.opts.ClusterRestCACert, m.opts.InsecureSkipTLSVerify)
	if err != nil {
		return fmt.Errorf("building destination REST client: %w", err)
	}

	gatewayService := gateway.NewK8sService(config.KubeConfigPath)
	clusterLinkService := clusterlink.NewConfluentCloudService(httpClient)
	workflow := cutover.NewCutoverWorkflowWithOffsets(gatewayService, clusterLinkService, sourceOffset, destinationOffset)
	workflow.SetRolloutTimeout(m.opts.RolloutTimeout)

	clusterLinkConfig := cutover.BuildClusterLinkConfig(&config, m.opts.ClusterApiKey, m.opts.ClusterApiSecret)
	persist := func() error {
		m.opts.CutoverState.UpsertCutover(config)
		return m.opts.CutoverState.WriteToFile(m.opts.CutoverStateFile)
	}

	// Pre-execute bookend: disable consumer.offset.sync.enable if the
	// operator opted in at init time. Idempotent and safe on resume.
	if err := cutover.DisableOffsetSync(ctx, clusterLinkService, clusterLinkConfig, &config, persist); err != nil {
		return err
	}

	orchestrator := cutover.NewCutoverOrchestrator(
		&config,
		workflow,
		&m.opts.CutoverState,
		m.opts.CutoverStateFile,
	)

	if err := orchestrator.Execute(ctx, m.opts.LagThreshold, m.opts.ClusterApiKey, m.opts.ClusterApiSecret); err != nil {
		cutover.WarnIfPausedOnExecuteFailure(&config, err)
		return fmt.Errorf("failed to execute cutover: %w", err)
	}

	// Post-execute bookend: restore consumer.offset.sync.enable. Soft-fail
	// so a restore error does not roll back a successful switchover.
	cutover.RestoreOffsetSync(ctx, clusterLinkService, clusterLinkConfig, &config, persist)

	fmt.Printf("✅ Cutover completed: %s\n", config.CutoverId)
	return nil
}

// sourceClusterAuth builds the source ClusterAuth from the execute flags.
// TlsCaCert is the CA that verifies the source broker's TLS server certificate
// and is applied to EVERY TLS-fronted auth method — SASL/SCRAM and SASL/PLAIN over
// TLS (SASL_SSL), one-way unauthenticated TLS, and mTLS — not only the mTLS path.
// For SASL/PLAIN, supplying it selects SASL_SSL over cleartext SASL_PLAINTEXT.
func sourceClusterAuth(opts CutoverExecutorOpts) types.ClusterAuth {
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

func (m *CutoverExecutor) createSourceOffset(_ context.Context) (*offset.Service, error) {
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

	slog.Debug("connecting to source cluster")
	sourceClient, err := client.NewKafkaClient(brokerAddresses, region, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to source cluster: %w", err)
	}
	slog.Debug("source cluster connected")

	return offset.NewOffsetService(sourceClient), nil
}

func (m *CutoverExecutor) createDestinationOffset() (*offset.Service, error) {
	slog.Debug("connecting to destination cluster (Confluent Cloud)")
	ccBrokers := strings.Split(m.opts.ClusterBootstrap, ",")
	// Confluent Cloud: SASL/PLAIN over TLS (SASL_SSL), public CA (no ca_cert).
	destOpts := []client.AdminOption{client.WithSASLPlainAuth(m.opts.ClusterApiKey, m.opts.ClusterApiSecret, "", m.opts.InsecureSkipTLSVerify)}
	destClient, err := client.NewKafkaClient(ccBrokers, "", destOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to destination cluster: %w", err)
	}
	slog.Debug("destination cluster connected")

	return offset.NewOffsetService(destClient), nil
}
