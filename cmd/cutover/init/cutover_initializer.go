package init

import (
	"context"
	"fmt"

	"github.com/confluentinc/kcp/internal/services/clusterlink"
	"github.com/confluentinc/kcp/internal/services/cutover"
	"github.com/confluentinc/kcp/internal/services/gateway"
)

type CutoverInitializerOpts struct {
	CutoverStateFile      string
	CutoverState          cutover.CutoverState
	CutoverConfig         cutover.CutoverConfig
	ClusterApiKey         string
	ClusterApiSecret      string
	ClusterRestCACert     string
	InsecureSkipTLSVerify bool
}

type CutoverInitializer struct {
	opts CutoverInitializerOpts
}

func NewCutoverInitializer(opts CutoverInitializerOpts) *CutoverInitializer {
	return &CutoverInitializer{
		opts: opts,
	}
}

func (m *CutoverInitializer) Run() error {
	config := m.opts.CutoverConfig

	// REST client for the destination cluster-link API: trusts a private CA
	// (--cluster-rest-ca-cert) and/or skips verification, else system roots (CC public CA).
	httpClient, err := cutover.NewRESTHTTPClient(m.opts.ClusterRestCACert, m.opts.InsecureSkipTLSVerify)
	if err != nil {
		return fmt.Errorf("building destination REST client: %w", err)
	}

	gatewayService := gateway.NewK8sService(config.KubeConfigPath)
	clusterLinkService := clusterlink.NewConfluentCloudService(httpClient)
	workflow := cutover.NewCutoverWorkflow(gatewayService, clusterLinkService)

	orchestrator := cutover.NewCutoverOrchestrator(
		&config,
		workflow,
		&m.opts.CutoverState,
		m.opts.CutoverStateFile,
	)

	ctx := context.Background()
	if err := orchestrator.Initialize(ctx, m.opts.ClusterApiKey, m.opts.ClusterApiSecret); err != nil {
		return fmt.Errorf("failed to initialize cutover: %w", err)
	}

	return nil
}
