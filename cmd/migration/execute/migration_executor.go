package execute

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/confluentinc/kcp/internal/client"
	"github.com/confluentinc/kcp/internal/services/clusterlink"
	"github.com/confluentinc/kcp/internal/services/gateway"
	"github.com/confluentinc/kcp/internal/services/migration"
	"github.com/confluentinc/kcp/internal/services/offset"
	"github.com/confluentinc/kcp/internal/types"
)

type MigrationExecutorOpts struct {
	MigrationStateFile string
	MigrationState     types.MigrationState
	MigrationConfig    types.MigrationConfig
	LagThreshold       int64
	ClusterApiKey      string
	ClusterApiSecret   string
	CCBootstrap        string
	SourceBootstrap    string
	AWSRegion          string
	AuthType           types.AuthType
	SaslScramUsername   string
	SaslScramPassword   string
	TlsCaCert           string
	TlsClientCert      string
	TlsClientKey       string
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
	defer sourceOffset.Close()

	// Create destination Kafka client (CC)
	destinationOffset, err := m.createDestinationOffset()
	if err != nil {
		return err
	}
	defer destinationOffset.Close()

	gatewayService := gateway.NewK8sService(config.KubeConfigPath)
	clusterLinkService := clusterlink.NewConfluentCloudService(http.DefaultClient)
	workflow := migration.NewMigrationWorkflowWithOffsets(gatewayService, clusterLinkService, sourceOffset, destinationOffset)

	orchestrator := migration.NewMigrationOrchestrator(
		&config,
		workflow,
		&m.opts.MigrationState,
		m.opts.MigrationStateFile,
	)

	if err := orchestrator.Execute(ctx, m.opts.LagThreshold, m.opts.ClusterApiKey, m.opts.ClusterApiSecret); err != nil {
		return fmt.Errorf("failed to execute migration: %w", err)
	}

	fmt.Printf("✅ Migration completed: %s\n", config.MigrationId)
	return nil
}

func (m *MigrationExecutor) createSourceOffset(_ context.Context) (*offset.Service, error) {
	authType := m.opts.AuthType
	brokerAddresses := strings.Split(m.opts.SourceBootstrap, ",")

	region := m.opts.AWSRegion

	// Build ClusterAuth from flag values
	clusterAuth := types.ClusterAuth{}
	switch authType {
	case types.AuthTypeSASLSCRAM:
		clusterAuth.AuthMethod.SASLScram = &types.SASLScramConfig{
			Use:      true,
			Username: m.opts.SaslScramUsername,
			Password: m.opts.SaslScramPassword,
		}
	case types.AuthTypeTLS:
		clusterAuth.AuthMethod.TLS = &types.TLSConfig{
			Use:        true,
			CACert:     m.opts.TlsCaCert,
			ClientCert: m.opts.TlsClientCert,
			ClientKey:  m.opts.TlsClientKey,
		}
	case types.AuthTypeIAM:
		clusterAuth.AuthMethod.IAM = &types.IAMConfig{Use: true}
	case types.AuthTypeUnauthenticatedTLS:
		clusterAuth.AuthMethod.UnauthenticatedTLS = &types.UnauthenticatedTLSConfig{Use: true}
	case types.AuthTypeUnauthenticatedPlaintext:
		clusterAuth.AuthMethod.UnauthenticatedPlaintext = &types.UnauthenticatedPlaintextConfig{Use: true}
	}

	slog.Debug("connecting to source cluster")
	sourceClient, err := client.NewKafkaClient(brokerAddresses, region, client.AdminOptionForAuth(authType, clusterAuth))
	if err != nil {
		return nil, fmt.Errorf("failed to connect to source cluster: %w", err)
	}
	slog.Debug("source cluster connected")

	return offset.NewOffsetService(sourceClient), nil
}

func (m *MigrationExecutor) createDestinationOffset() (*offset.Service, error) {
	slog.Debug("connecting to destination cluster (Confluent Cloud)")
	ccBrokers := strings.Split(m.opts.CCBootstrap, ",")
	destClient, err := client.NewKafkaClient(ccBrokers, "", client.WithSASLPlainAuth(m.opts.ClusterApiKey, m.opts.ClusterApiSecret))
	if err != nil {
		return nil, fmt.Errorf("failed to connect to destination cluster: %w", err)
	}
	slog.Debug("destination cluster connected")

	return offset.NewOffsetService(destClient), nil
}

