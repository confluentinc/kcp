package init

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/confluentinc/kcp/internal/services/clusterlink"
	"github.com/confluentinc/kcp/internal/services/gateway"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/goccy/go-yaml"
)

type MigrationInitializerOpts struct {
	MigrationStateFile string
	MigrationState     types.MigrationState
	MigrationConfig    types.MigrationConfig
	ClusterApiKey      string
	ClusterApiSecret   string
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
	config := &m.opts.MigrationConfig
	ctx := context.Background()

	// Validate YAML files are parseable
	if err := validateYAML(config.FencedCrYAML, "fenced CR"); err != nil {
		return err
	}
	if err := validateYAML(config.SwitchoverCrYAML, "switchover CR"); err != nil {
		return err
	}

	// Fetch and store the initial CR YAML from k8s
	gatewayService := gateway.NewK8sService(config.KubeConfigPath)
	initialCrYAML, err := gatewayService.GetGatewayYAML(ctx, config.K8sNamespace, config.PassthroughCrName)
	if err != nil {
		return fmt.Errorf("failed to get initial CR YAML: %w", err)
	}
	config.InitialCrYAML = initialCrYAML

	// Validate cluster link and topics
	clusterLinkService := clusterlink.NewConfluentCloudService(&http.Client{})
	clusterLinkConfig := clusterlink.Config{
		RestEndpoint: config.ClusterRestEndpoint,
		ClusterID:    config.ClusterId,
		LinkName:     config.ClusterLinkName,
		APIKey:       m.opts.ClusterApiKey,
		APISecret:    m.opts.ClusterApiSecret,
		Topics:       config.Topics,
	}

	slog.Info("describing cluster link", "clusterId", config.ClusterId, "clusterLinkName", config.ClusterLinkName)

	mirrorTopics, err := clusterLinkService.ListMirrorTopics(ctx, clusterLinkConfig)
	if err != nil {
		return fmt.Errorf("failed to list mirror topics: %w", err)
	}

	clusterLinkTopics, inactiveTopics := clusterlink.ClassifyMirrorTopics(mirrorTopics)
	if len(inactiveTopics) > 0 {
		return fmt.Errorf("%d mirror topics are not active: %v", len(inactiveTopics), inactiveTopics)
	}

	if len(config.Topics) > 0 {
		slog.Info("validating topics in cluster link", "topic count", len(config.Topics))
		if err := clusterLinkService.ValidateTopics(config.Topics, clusterLinkTopics); err != nil {
			return fmt.Errorf("failed to validate topics in cluster link: %w", err)
		}
	} else {
		config.Topics = clusterLinkTopics
	}

	configs, err := clusterLinkService.ListConfigs(ctx, clusterLinkConfig)
	if err != nil {
		return fmt.Errorf("failed to list cluster link configs: %w", err)
	}

	config.ClusterLinkTopics = clusterLinkTopics
	config.ClusterLinkConfigs = configs

	// Update state to initialized and persist
	config.CurrentState = types.StateInitialized
	m.opts.MigrationState.UpsertMigration(*config)
	if err := m.opts.MigrationState.WriteToFile(m.opts.MigrationStateFile); err != nil {
		return fmt.Errorf("failed to save migration state: %w", err)
	}

	slog.Info("migration initialized successfully")
	return nil
}

func validateYAML(data []byte, name string) error {
	var out any
	if err := yaml.Unmarshal(data, &out); err != nil {
		return fmt.Errorf("%s YAML is not valid: %w", name, err)
	}
	return nil
}
