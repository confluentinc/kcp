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
	"github.com/confluentinc/kcp/internal/services/msk"
	"github.com/confluentinc/kcp/internal/services/offset"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/confluentinc/kcp/internal/utils"
)

type MigrationExecutorOpts struct {
	MigrationStateFile string
	MigrationState     types.MigrationState
	MigrationConfig    types.MigrationConfig
	LagThreshold       int64
	ClusterApiKey      string
	ClusterApiSecret   string
	CredentialsFile    string
	CCBootstrap        string
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

	slog.Debug("migration completed",
		"migrationId", config.MigrationId,
		"currentState", config.CurrentState)
	return nil
}

func (m *MigrationExecutor) createSourceOffset(ctx context.Context) (*offset.Service, error) {
	config := m.opts.MigrationConfig

	credentials, errs := types.NewCredentialsFromFile(m.opts.CredentialsFile)
	if len(errs) > 0 {
		return nil, fmt.Errorf("failed to parse credentials file: %v", errs[0])
	}

	clusterAuth, err := credentials.FindClusterByArn(config.SourceClusterArn)
	if err != nil {
		return nil, err
	}

	authType, err := clusterAuth.GetSelectedAuthType()
	if err != nil {
		return nil, fmt.Errorf("failed to determine auth type: %w", err)
	}

	region, err := utils.ExtractRegionFromArn(config.SourceClusterArn)
	if err != nil {
		return nil, err
	}

	slog.Debug("discovering MSK bootstrap brokers")
	mskAwsClient, err := client.NewMSKClient(region, 8, 1)
	if err != nil {
		return nil, fmt.Errorf("failed to create MSK API client: %w", err)
	}

	mskService := msk.NewMSKService(mskAwsClient)
	bootstrapOutput, err := mskService.GetBootstrapBrokers(ctx, config.SourceClusterArn)
	if err != nil {
		return nil, fmt.Errorf("failed to get bootstrap brokers: %w", err)
	}

	awsInfo := types.AWSClientInformation{BootstrapBrokers: *bootstrapOutput}
	brokerAddresses, err := awsInfo.GetBootstrapBrokersForAuthType(authType)
	if err != nil {
		return nil, err
	}

	slog.Debug("connecting to source cluster (MSK)")
	sourceClient, err := client.NewKafkaClient(brokerAddresses, region, client.AdminOptionForAuth(authType, *clusterAuth))
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

