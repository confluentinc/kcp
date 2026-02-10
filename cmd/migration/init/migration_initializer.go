package init

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/confluentinc/kcp/internal/types"
)

type MigrationInitializerOpts struct {
	stateFile string

	gatewayNamespace     string
	gatewayCrdName       string
	sourceName           string
	destinationName      string
	sourceRouteName      string
	destinationRouteName string
	kubeConfigPath       string

	clusterId            string
	clusterRestEndpoint  string
	clusterLinkName      string
	clusterApiKey        string
	clusterApiSecret     string
	topics               []string
	authMode             string
	ccBootstrapEndpoint  string
	loadBalancerEndpoint string
}

type MigrationInitializer struct {
	opts MigrationInitializerOpts
}

func NewMigrationInitializer(opts MigrationInitializerOpts) *MigrationInitializer {
	return &MigrationInitializer{
		opts: opts,
	}
}

func (m *MigrationInitializer) Run() error {
	migrationOpts := types.MigrationOpts{
		GatewayNamespace:     m.opts.gatewayNamespace,
		GatewayCrdName:       m.opts.gatewayCrdName,
		SourceName:           m.opts.sourceName,
		DestinationName:      m.opts.destinationName,
		SourceRouteName:      m.opts.sourceRouteName,
		DestinationRouteName: m.opts.destinationRouteName,
		KubeConfigPath:       m.opts.kubeConfigPath,
		ClusterId:            m.opts.clusterId,
		ClusterRestEndpoint:  m.opts.clusterRestEndpoint,
		ClusterLinkName:      m.opts.clusterLinkName,
		Topics:               m.opts.topics,
		AuthMode:             m.opts.authMode,
		ClusterApiKey:        m.opts.clusterApiKey,
		ClusterApiSecret:     m.opts.clusterApiSecret,
		CCBootstrapEndpoint:  m.opts.ccBootstrapEndpoint,
		LoadBalancerEndpoint: m.opts.loadBalancerEndpoint,
	}

	migrationId := fmt.Sprintf("migration-%s", time.Now().Format("20060102-150405"))
	migration := types.NewMigration(migrationId, m.opts.stateFile, migrationOpts)

	if err := migration.Initialize(context.Background()); err != nil {
		return fmt.Errorf("failed to initialize migration: %w", err)
	}

	slog.Info("migration initialized",
		"migrationId", migration.MigrationId,
		"currentState", migration.GetCurrentState())
	return nil
}
