package init

import (
	"context"
	"fmt"
	"net/http"

	"github.com/confluentinc/kcp/internal/services/clusterlink"
	"github.com/confluentinc/kcp/internal/services/cutover"
	"github.com/confluentinc/kcp/internal/services/gateway"
	"github.com/confluentinc/kcp/internal/utils"
)

type CutoverInitializerOpts struct {
	CutoverStateFile      string
	CutoverState          cutover.CutoverState
	CutoverConfig         cutover.CutoverConfig
	ClusterApiKey         string
	ClusterApiSecret      string
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

	httpClient := http.DefaultClient
	if m.opts.InsecureSkipTLSVerify {
		httpClient = &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: utils.TLSClientConfig(nil, true),
			},
		}
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
