package init

import (
	"context"
	"fmt"

	"github.com/confluentinc/kcp/internal/services/clusterlink"
	"github.com/confluentinc/kcp/internal/services/gateway"
	"github.com/confluentinc/kcp/internal/services/migration"
	"github.com/confluentinc/kcp/internal/targets"
)

type MigrationInitializerOpts struct {
	MigrationStateFile string
	MigrationState     migration.MigrationState
	MigrationConfig    migration.MigrationConfig
	// RestCreds authenticates the destination REST surface — the only client
	// this initializer builds. Carrying the full resolved credential (rather
	// than a scalar api_key/api_secret pair) is what lets a basic, bearer or
	// mTLS destination REST leg reach a Confluent Platform cluster.
	RestCreds *targets.Credentials
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
	config := m.opts.MigrationConfig

	// REST client for the destination cluster-link API: presents whichever TLS
	// trust (and, for mTLS, client cert) the resolved REST credentials carry.
	httpClient, err := m.opts.RestCreds.HTTPClient()
	if err != nil {
		return fmt.Errorf("building destination REST client: %w", err)
	}

	gatewayService := gateway.NewK8sService(config.KubeConfigPath)
	clusterLinkService := clusterlink.NewConfluentCloudService(httpClient)
	actions := migration.NewMigrationActions(gatewayService, clusterLinkService)

	orchestrator := migration.NewMigrationOrchestrator(
		&config,
		actions,
		&m.opts.MigrationState,
		m.opts.MigrationStateFile,
	)

	ctx := context.Background()
	if err := orchestrator.Initialize(ctx, m.opts.RestCreds.Authenticator()); err != nil {
		return fmt.Errorf("failed to initialize migration: %w", err)
	}

	return nil
}
