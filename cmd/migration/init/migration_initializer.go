package init

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"

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
	actions := migration.NewMigrationActions(gatewayService, clusterLinkService)

	orchestrator := migration.NewMigrationOrchestrator(
		&config,
		actions,
		&m.opts.MigrationState,
		m.opts.MigrationStateFile,
	)

	ctx := context.Background()
	if err := orchestrator.Initialize(ctx, m.opts.ClusterApiKey, m.opts.ClusterApiSecret); err != nil {
		return fmt.Errorf("failed to initialize migration: %w", err)
	}

	return nil
}
