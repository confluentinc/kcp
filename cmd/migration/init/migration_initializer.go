package init

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/confluentinc/kcp/internal/types"
	"github.com/google/uuid"
)

type MigrationInitializerOpts struct {
	migrationStateFile string
	skipValidate       bool

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
	// Load or create migration state (following KCP pattern)
	var migrationState *types.MigrationState
	if _, err := os.Stat(m.opts.migrationStateFile); err == nil {
		// File exists, load it
		migrationState, err = types.NewMigrationStateFromFile(m.opts.migrationStateFile)
		if err != nil {
			return fmt.Errorf("failed to load migration state: %w", err)
		}
	} else {
		// File doesn't exist, create new state
		migrationState = types.NewMigrationState()
	}

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

	// Generate UUID-based migration ID
	migrationId := fmt.Sprintf("migration-%s", uuid.New().String())
	migration := types.NewMigration(migrationId, migrationOpts)

	// Provide save function for FSM callbacks
	migration.SetSaveStateFunc(func() error {
		migrationState.UpsertMigration(*migration)
		return migrationState.WriteToFile(m.opts.migrationStateFile)
	})

	// Skip validation if flag is set (useful for testing without infrastructure)
	if m.opts.skipValidate {
		// Save migration metadata without calling Initialize()
		migrationState.UpsertMigration(*migration)
		if err := migrationState.WriteToFile(m.opts.migrationStateFile); err != nil {
			return fmt.Errorf("failed to save migration state: %w", err)
		}
		slog.Info("migration created (validation skipped)",
			"migrationId", migration.MigrationId,
			"currentState", migration.GetCurrentState(),
			"stateFile", m.opts.migrationStateFile)
		return nil
	}

	// Normal path: validate infrastructure and initialize
	if err := migration.Initialize(context.Background()); err != nil {
		return fmt.Errorf("failed to initialize migration: %w", err)
	}

	slog.Info("migration initialized",
		"migrationId", migration.MigrationId,
		"currentState", migration.GetCurrentState(),
		"stateFile", m.opts.migrationStateFile)
	return nil
}
