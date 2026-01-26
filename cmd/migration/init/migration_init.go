package migration_init

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/confluentinc/kcp/internal/types"
)

type MigrationInitOpts struct {
	stateFile string
	state     types.State

	gatewayNamespace     string
	gatewayCrdName       string
	sourceName           string
	destinationName      string
	sourceRouteName      string
	destinationRouteName string
	kubeConfigPath       string

	clusterId           string
	clusterRestEndpoint string
	clusterLinkName     string
	clusterApiKey       string
	clusterApiSecret    string
	topics              []string
	authMode            string
}

type MigrationInit struct {
	stateFile string
	state     types.State

	gatewayNamespace     string
	gatewayCrdName       string
	sourceName           string
	destinationName      string
	sourceRouteName      string
	destinationRouteName string
	kubeConfigPath       string

	clusterId           string
	clusterRestEndpoint string
	clusterLinkName     string
	clusterApiKey       string
	clusterApiSecret    string
	topics              []string
	authMode            string
}

func NewMigrationInit(opts MigrationInitOpts) *MigrationInit {
	return &MigrationInit{
		stateFile:            opts.stateFile,
		state:                opts.state,
		gatewayNamespace:     opts.gatewayNamespace,
		gatewayCrdName:       opts.gatewayCrdName,
		sourceName:           opts.sourceName,
		destinationName:      opts.destinationName,
		sourceRouteName:      opts.sourceRouteName,
		destinationRouteName: opts.destinationRouteName,
		kubeConfigPath:       opts.kubeConfigPath,
		clusterId:            opts.clusterId,
		clusterRestEndpoint:  opts.clusterRestEndpoint,
		clusterLinkName:      opts.clusterLinkName,
		clusterApiKey:        opts.clusterApiKey,
		clusterApiSecret:     opts.clusterApiSecret,
		topics:               opts.topics,
		authMode:             opts.authMode,
	}
}

func (m *MigrationInit) Run() error {

	migrationOpts := types.MigrationOpts{
		GatewayNamespace:     m.gatewayNamespace,
		GatewayCrdName:       m.gatewayCrdName,
		SourceName:           m.sourceName,
		DestinationName:      m.destinationName,
		SourceRouteName:      m.sourceRouteName,
		DestinationRouteName: m.destinationRouteName,
		KubeConfigPath:       m.kubeConfigPath,
		ClusterId:            m.clusterId,
		ClusterRestEndpoint:  m.clusterRestEndpoint,
		ClusterLinkName:      m.clusterLinkName,
		Topics:               m.topics,
		AuthMode:             m.authMode,
		ClusterApiKey:        m.clusterApiKey,
		ClusterApiSecret:     m.clusterApiSecret,
	}

	migrationId := fmt.Sprintf("migration-%s", time.Now().Format("20060102-150405"))
	migration := types.NewMigration(migrationId, migrationOpts)
	err := migration.FSM.Event(context.Background(), types.EventKcpInit)
	if err != nil {
		return fmt.Errorf("failed to initialize migration: %v", err)
	}
	slog.Info("migration initialized", "migrationId", migration.MigrationId, "currentState", migration.CurrentState, "fsm", migration.FSM.Current())
	m.state.UpsertMigration(*migration)
	return m.state.PersistStateFile(m.stateFile)
}
