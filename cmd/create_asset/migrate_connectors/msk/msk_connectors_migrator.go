package msk_connectors

import (
	"bytes"
	"embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/confluentinc/kcp/internal/types"
	connector_utils "github.com/confluentinc/kcp/internal/utils"
)

//go:embed assets
var assetsFs embed.FS

type TemplateData struct {
	ConnectorName   string
	EnvironmentId   string
	ClusterId       string
	ConnectorConfig map[string]interface{}
	Warnings        []Warning
}

type TranslateResponse struct {
	Config   map[string]interface{} `json:"config"`
	Warnings []Warning              `json:"warnings"`
}

type Warning struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

type MigrateMskConnectorOpts struct {
	EnvironmentId string
	ClusterId     string

	CcApiKey    string
	CcApiSecret string

	Connectors []types.ConnectorSummary
	OutputDir  string
}

type MskConnectorMigrator struct {
	EnvironmentId string
	ClusterId     string

	CcApiKey    string
	CcApiSecret string

	Connectors []types.ConnectorSummary
	OutputDir  string
}

func NewMskConnectorMigrator(opts MigrateMskConnectorOpts) *MskConnectorMigrator {
	return &MskConnectorMigrator{
		EnvironmentId: opts.EnvironmentId,
		ClusterId:     opts.ClusterId,
		CcApiKey:      opts.CcApiKey,
		CcApiSecret:   opts.CcApiSecret,
		Connectors:    opts.Connectors,
		OutputDir:     opts.OutputDir,
	}
}

func (mc *MskConnectorMigrator) Run() error {
	if len(mc.Connectors) == 0 {
		slog.Warn("⚠️ No MSK Connect connectors found to migrate for the MSK cluster.")
		return nil
	}

	if mc.OutputDir != "" {
		if err := os.MkdirAll(mc.OutputDir, 0755); err != nil {
			return fmt.Errorf("failed to create output directory %s: %w", mc.OutputDir, err)
		}
	}

	slog.Info(fmt.Sprintf("Found %d connector(s) to migrate", len(mc.Connectors)))

	tmplContent, err := assetsFs.ReadFile("assets/connector.tmpl")
	if err != nil {
		return fmt.Errorf("failed to read template file: %w", err)
	}

	tmpl, err := template.New("connector").Funcs(template.FuncMap{
		"lower": strings.ToLower,
	}).Parse(string(tmplContent))
	if err != nil {
		return fmt.Errorf("failed to parse template: %w", err)
	}

	for _, connector := range mc.Connectors {
		translatedConfig, warnings, err := mc.translateConnectorConfig(connector)
		if err != nil {
			slog.Warn(fmt.Sprintf("❌ Failed to translate connector %s: %v", connector.ConnectorName, err))
			continue
		}

		if len(warnings) > 0 {
			slog.Info(fmt.Sprintf("⚠️ %d validation warnings for connector %s", len(warnings), connector.ConnectorName))
		}

		filename := fmt.Sprintf("%s-connector.tf", connector.ConnectorName)
		filepath := filepath.Join(mc.OutputDir, filename)

		file, err := os.Create(filepath)
		if err != nil {
			return fmt.Errorf("failed to create file %s: %w", filepath, err)
		}
		defer file.Close()

		templateData := TemplateData{
			ConnectorName:   connector.ConnectorName,
			EnvironmentId:   mc.EnvironmentId,
			ClusterId:       mc.ClusterId,
			ConnectorConfig: translatedConfig,
			Warnings:        warnings,
		}

		if err := tmpl.Execute(file, templateData); err != nil {
			return fmt.Errorf("failed to execute template for connector %s: %w", connector.ConnectorName, err)
		}

		slog.Info(fmt.Sprintf("✅ Generated: %s", filename))
	}

	slog.Info(fmt.Sprintf("✅ Successfully generated connector files for %d connectors in %s", len(mc.Connectors), mc.OutputDir))

	return nil
}

func (mc *MskConnectorMigrator) translateConnectorConfig(connector types.ConnectorSummary) (map[string]any, []Warning, error) {
	connectorClass, ok := connector.ConnectorConfiguration["connector.class"]
	if !ok {
		return nil, nil, fmt.Errorf("'connector.class' not found in config")
	}

	pluginName, err := connector_utils.InferPluginName(connectorClass)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to determine plugin name: %w", err)
	}

	url := fmt.Sprintf(
		"https://api.confluent.cloud/connect/v1/environments/%s/clusters/%s/connector-plugins/%s/config/translate",
		mc.EnvironmentId,
		mc.ClusterId,
		pluginName,
	)

	configJSON, err := json.Marshal(connector.ConnectorConfiguration)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to marshal connector config: %w", err)
	}

	req, err := http.NewRequest("PUT", url, bytes.NewBuffer(configJSON))
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create HTTP request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	auth := base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf("%s:%s", mc.CcApiKey, mc.CcApiSecret)))
	req.Header.Set("Authorization", fmt.Sprintf("Basic %s", auth))

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to execute HTTP request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("API request failed with status %d: %s", resp.StatusCode, string(body))
	}

	var response TranslateResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	return response.Config, response.Warnings, nil
}
