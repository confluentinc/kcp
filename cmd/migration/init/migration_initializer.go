package init

import (
	"context"
	"fmt"

	"github.com/confluentinc/kcp/internal/services/clusterlink"
	"github.com/confluentinc/kcp/internal/services/gateway"
	"github.com/confluentinc/kcp/internal/services/migration"
)

type MigrationInitializerOpts struct {
	MigrationStateFile    string
	MigrationState        migration.MigrationState
	MigrationConfig       migration.MigrationConfig
	ClusterApiKey         string
	ClusterApiSecret      string
	ClusterRestCACert     string
	InsecureSkipTLSVerify bool
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

	// REST client for the destination cluster-link API: trusts a private CA
	// (--cluster-rest-ca-cert) and/or skips verification, else system roots (CC public CA).
	httpClient, err := migration.NewRESTHTTPClient(m.opts.ClusterRestCACert, m.opts.InsecureSkipTLSVerify)
	if err != nil {
		return fmt.Errorf("building destination REST client: %w", err)
	}

	gatewayService := gateway.NewK8sService(config.KubeConfigPath)
	clusterLinkService := clusterlink.NewConfluentCloudService(httpClient)
	workflow := migration.NewMigrationWorkflow(gatewayService, clusterLinkService)

	orchestrator := migration.NewMigrationOrchestrator(
		&config,
		workflow,
		&m.opts.MigrationState,
		m.opts.MigrationStateFile,
	)

	ctx := context.Background()
	if err := orchestrator.Initialize(ctx, m.opts.ClusterApiKey, m.opts.ClusterApiSecret); err != nil {
		return fmt.Errorf("failed to initialize migration: %w", err)
	}

	return nil
}
