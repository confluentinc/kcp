package execute

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"net/http"
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

	httpClient := http.DefaultClient
	if m.opts.InsecureSkipTLSVerify {
		httpClient = &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // user-controlled flag
			},
		}
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

func (m *CutoverExecutor) createSourceOffset(_ context.Context) (*offset.Service, error) {
	authType := m.opts.AuthType
	brokerAddresses := strings.Split(m.opts.SourceBootstrap, ",")

	region := m.opts.AWSRegion

	// Build ClusterAuth from flag values
	clusterAuth := types.ClusterAuth{}
	switch authType {
	case types.AuthTypeSASLSCRAM:
		clusterAuth.AuthMethod.SASLScram = &types.SASLScramConfig{
			Use:       true,
			Username:  m.opts.SaslScramUsername,
			Password:  m.opts.SaslScramPassword,
			Mechanism: m.opts.SaslScramMechanism,
		}
	case types.AuthTypeTLS:
		clusterAuth.AuthMethod.TLS = &types.TLSConfig{
			Use:        true,
			CACert:     m.opts.TlsCaCert,
			ClientCert: m.opts.TlsClientCert,
			ClientKey:  m.opts.TlsClientKey,
		}
	case types.AuthTypeSASLPlain:
		clusterAuth.AuthMethod.SASLPlain = &types.SASLPlainConfig{
			Use:      true,
			Username: m.opts.SaslPlainUsername,
			Password: m.opts.SaslPlainPassword,
		}
	case types.AuthTypeIAM:
		clusterAuth.AuthMethod.IAM = &types.IAMConfig{Use: true}
	case types.AuthTypeUnauthenticatedTLS:
		clusterAuth.AuthMethod.UnauthenticatedTLS = &types.UnauthenticatedTLSConfig{Use: true}
	case types.AuthTypeUnauthenticatedPlaintext:
		clusterAuth.AuthMethod.UnauthenticatedPlaintext = &types.UnauthenticatedPlaintextConfig{Use: true}
	}

	opts := []client.AdminOption{client.AdminOptionForAuth(authType, clusterAuth)}
	if m.opts.InsecureSkipTLSVerify {
		opts = append(opts, client.WithInsecureSkipVerify())
	}

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
	destOpts := []client.AdminOption{client.WithSASLPlainAuth(m.opts.ClusterApiKey, m.opts.ClusterApiSecret, "")}
	if m.opts.InsecureSkipTLSVerify {
		destOpts = append(destOpts, client.WithInsecureSkipVerify())
	}
	destClient, err := client.NewKafkaClient(ccBrokers, "", destOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to destination cluster: %w", err)
	}
	slog.Debug("destination cluster connected")

	return offset.NewOffsetService(destClient), nil
}
