package connectorutility

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/confluentinc/kcp/internal/services/markdown"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/confluentinc/kcp/internal/utils"
	"github.com/fatih/color"
)

type ConnectorUtilityOpts struct {
	ClustersByArn map[string]*types.DiscoveredCluster
	OutputDir     string
}

type ConnectorUtility struct {
	clustersByArn map[string]*types.DiscoveredCluster
	outputDir     string
}

type ConnectorConfig struct {
	Name   string                 `json:"name"`
	Config map[string]interface{} `json:"config"`
}

type ConnectorConfigsOutput struct {
	Connectors map[string]ConnectorConfig `json:"connectors"`
}

func NewConnectorUtility(opts ConnectorUtilityOpts) *ConnectorUtility {
	return &ConnectorUtility{
		clustersByArn: opts.ClustersByArn,
		outputDir:     opts.OutputDir,
	}
}

func (cu *ConnectorUtility) Run() error {
	if len(cu.clustersByArn) == 0 {
		slog.Warn("⚠️ No clusters found to write")
		return nil
	}

	if err := os.MkdirAll(cu.outputDir, 0755); err != nil {
		return fmt.Errorf("failed to create output directory %s: %w", cu.outputDir, err)
	}

	totalConnectors := 0
	for clusterArn, cluster := range cu.clustersByArn {
		clusterName := utils.ExtractClusterNameFromArn(clusterArn)
		filename := fmt.Sprintf("%s-connector-configs.json", clusterName)
		filepath := filepath.Join(cu.outputDir, filename)

		connectorConfigs := cu.buildConnectorConfigs(cluster)
		if len(connectorConfigs.Connectors) == 0 {
			slog.Info(fmt.Sprintf("⏭️ Skipping: %s (no connectors found)", filename))
			continue
		}

		file, err := os.Create(filepath)
		if err != nil {
			return fmt.Errorf("failed to create file %s: %w", filepath, err)
		}

		encoder := json.NewEncoder(file)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(connectorConfigs); err != nil {
			file.Close()
			return fmt.Errorf("failed to encode connectors to JSON for cluster %s: %w", clusterArn, err)
		}

		if err := file.Close(); err != nil {
			return fmt.Errorf("failed to close file %s: %w", filepath, err)
		}

		totalConnectors += len(connectorConfigs.Connectors)
		slog.Info(fmt.Sprintf("✅ Generated: %s (%d connector(s))", filename, len(connectorConfigs.Connectors)))
	}

	slog.Info(fmt.Sprintf("✅ Successfully generated connector config files for %d cluster(s) with %d total connector(s) in %s", len(cu.clustersByArn), totalConnectors, cu.outputDir))

	readmePath := filepath.Join(cu.outputDir, "README.md")
	if err := cu.generateREADME(readmePath); err != nil {
		return fmt.Errorf("failed to generate README: %w", err)
	}
	
	fmt.Println()
	color.Green("See the README.md file in the output directory for next steps.")

	return nil
}

func (cu *ConnectorUtility) generateREADME(filePath string) error {
	md := markdown.New()

	md.AddHeading("Connect Migration Utility", 1)
	md.AddParagraph(`
This directory contains connector configuration JSON files extracted from your MSK cluster(s). These JSON configuration files can be used in conjuction with the [connect-migration-utility](https://github.com/confluentinc/connect-migration-utility)
to translate MSK Connect and self-managed connector configs before migrating them to Confluent Cloud. `)
	md.AddParagraph("A blog post on the 'connect-migration-utility' tool can be found [here](https://www.confluent.io/blog/migrate-self-fully-managed-connectors/).")

	md.AddHeading("Files", 2)
	md.AddParagraph("Each JSON file contains connector configurations for a specific cluster in the format expected by the connect-migration-utility:")
	fileList := []string{}
	for clusterArn := range cu.clustersByArn {
		clusterName := utils.ExtractClusterNameFromArn(clusterArn)
		filename := fmt.Sprintf("%s-connector-configs.json", clusterName)
		fileList = append(fileList, filename)
	}
	md.AddList(fileList)

	md.AddHeading("Next Steps", 2)
	md.AddParagraph("To continue with the connector migration, ensure you have the following prerequisites installed:")
	md.AddList([]string{
		"Python 3.8+",
		"Confluent Cloud environment with API access. (optional)",
	})
	md.AddParagraph("Then, clone the [connect-migration-utility](https://github.com/confluentinc/connect-migration-utility) repository and follow the instructions in the README.")
	md.AddCodeBlock("git clone https://github.com/confluentinc/connect-migration-utility.git", "bash")
	md.AddParagraph("Finally, you can use the connect-migration-utility to migrate the connectors to Confluent Cloud.")
	md.AddParagraph("Note: The discovery of connectors has already been performed by kcp, therefore you can may [proceed directly to the next step](https://github.com/confluentinc/connect-migration-utility?tab=readme-ov-file#using-configuration-file-or-directory).")

	return md.Print(markdown.PrintOptions{ToTerminal: false, ToFile: filePath})
}

func (cu *ConnectorUtility) buildConnectorConfigs(cluster *types.DiscoveredCluster) ConnectorConfigsOutput {
	connectors := make(map[string]ConnectorConfig)

	// MSK Connect connectors.
	for _, mskConnector := range cluster.AWSClientInformation.Connectors {
		config := make(map[string]any)
		for k, v := range mskConnector.ConnectorConfiguration {
			config[k] = v
		}

		connectors[mskConnector.ConnectorName] = ConnectorConfig{
			Name:   mskConnector.ConnectorName,
			Config: config,
		}
	}

	// Self-managed connectors.
	if cluster.KafkaAdminClientInformation.SelfManagedConnectors != nil {
		for _, selfManagedConnector := range cluster.KafkaAdminClientInformation.SelfManagedConnectors.Connectors {
			connectors[selfManagedConnector.Name] = ConnectorConfig{
				Name:   selfManagedConnector.Name,
				Config: selfManagedConnector.Config,
			}
		}
	}

	return ConnectorConfigsOutput{
		Connectors: connectors,
	}
}
