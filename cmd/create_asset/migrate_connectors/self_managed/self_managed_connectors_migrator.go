package self_managed_connectors

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

	"github.com/confluentinc/kcp/internal/redact"
	"github.com/confluentinc/kcp/internal/services/hcl"
	"github.com/confluentinc/kcp/internal/types"
	connector_utils "github.com/confluentinc/kcp/internal/utils"
)

//go:embed assets
var assetsFs embed.FS

// defaultTranslateBaseURL is the Confluent Cloud API host the connector-config
// translate endpoint lives under. Overridable via the migrator's baseURL field
// so tests can point translation at a local stub.
const defaultTranslateBaseURL = "https://api.confluent.cloud"

// countRedactedConnectors reports how many connectors carry at least one redacted
// sensitive field (a value equal to redact.Placeholder) anywhere in their source
// configuration, including nested maps/lists. Used to decide whether to warn the
// operator that the generated assets need manual secret replacement. Count only —
// never the connector names or field keys.
func countRedactedConnectors(connectors []types.SelfManagedConnector) int {
	count := 0
	for _, c := range connectors {
		if redact.AnyMapContainsRedacted(c.Config) {
			count++
		}
	}
	return count
}

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

type MigrateSelfManagedConnectorOpts struct {
	EnvironmentId string
	ClusterId     string

	CcApiKey    string
	CcApiSecret string

	Connectors []types.SelfManagedConnector
	OutputDir  string
}

type SelfManagedConnectorMigrator struct {
	EnvironmentId string
	ClusterId     string

	CcApiKey    string
	CcApiSecret string

	Connectors []types.SelfManagedConnector
	OutputDir  string

	// baseURL is the host the translate endpoint is called under; defaults to
	// defaultTranslateBaseURL and is overridable in tests.
	baseURL string
}

func NewSelfManagedConnectorMigrator(opts MigrateSelfManagedConnectorOpts) *SelfManagedConnectorMigrator {
	return &SelfManagedConnectorMigrator{
		EnvironmentId: opts.EnvironmentId,
		ClusterId:     opts.ClusterId,
		CcApiKey:      opts.CcApiKey,
		CcApiSecret:   opts.CcApiSecret,
		Connectors:    opts.Connectors,
		OutputDir:     opts.OutputDir,
		baseURL:       defaultTranslateBaseURL,
	}
}

func (mc *SelfManagedConnectorMigrator) Run() error {
	if len(mc.Connectors) == 0 {
		slog.Warn("no self-managed connectors found to migrate for the MSK cluster")
		return nil
	}

	if mc.OutputDir != "" {
		if err := connector_utils.ValidateOutputDir(mc.OutputDir); err != nil {
			return err
		}
		if err := os.MkdirAll(mc.OutputDir, 0755); err != nil {
			return fmt.Errorf("failed to create output directory %s: %w", mc.OutputDir, err)
		}
	}

	fmt.Printf("🔍 Found %d connector(s) to migrate\n", len(mc.Connectors))

	// Warn (count only — never names or field keys) when generated assets will
	// carry redaction placeholders the operator must replace before applying.
	if redacted := countRedactedConnectors(mc.Connectors); redacted > 0 {
		fmt.Printf("⚠️  %d of %d connector(s) contain redacted sensitive fields (%s) — replace with real values in the generated Terraform before applying\n", redacted, len(mc.Connectors), redact.Placeholder)
	}

	// Write shared Terraform infrastructure files (providers.tf, variables.tf)
	if err := hcl.WriteMigrateConnectorsInfraFiles(mc.OutputDir); err != nil {
		return err
	}

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
			slog.Warn(fmt.Sprintf("failed to translate connector %s: %v", connector.Name, err))
			continue
		}

		if warnings != nil {
			if len(warnings) > 0 {
				slog.Warn(fmt.Sprintf("%d validation warnings for connector %s", len(warnings), connector.Name))
			}
		}

		// connector.Name is untrusted (ListConnectors JSON / state file) and Kafka
		// Connect allows "/" and ".." in names — sanitize to a single safe path
		// segment so the write cannot escape OutputDir.
		filename := fmt.Sprintf("%s-connector.tf", connector_utils.SanitizeConnectorFilename(connector.Name))
		path := filepath.Join(mc.OutputDir, filename)

		templateData := TemplateData{
			ConnectorName:   connector.Name,
			EnvironmentId:   mc.EnvironmentId,
			ClusterId:       mc.ClusterId,
			ConnectorConfig: translatedConfig,
			Warnings:        warnings,
		}

		if err := writeConnectorFile(tmpl, path, templateData); err != nil {
			return err
		}

		slog.Debug(fmt.Sprintf("generated: %s", filename))
	}

	fmt.Printf("✅ Successfully generated connector files for %d connectors in %s\n", len(mc.Connectors), mc.OutputDir)

	return nil
}

// writeConnectorFile renders templateData into a single connector .tf file at
// path. It is a standalone function (not inlined in the Run loop) so the file
// handle is closed when this returns — once per connector — rather than via a
// deferred close that would accumulate open handles until Run() returns and
// could exhaust file descriptors (or, on Windows, block subsequent file ops).
// The deferred Close also surfaces a write error that only manifests on close.
func writeConnectorFile(tmpl *template.Template, path string, templateData TemplateData) (err error) {
	file, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("failed to create file %s: %w", path, err)
	}
	defer func() {
		if cerr := file.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("failed to close file %s: %w", path, cerr)
		}
	}()

	if err := tmpl.Execute(file, templateData); err != nil {
		return fmt.Errorf("failed to execute template for connector %s: %w", templateData.ConnectorName, err)
	}

	return nil
}

func (mc *SelfManagedConnectorMigrator) translateConnectorConfig(connector types.SelfManagedConnector) (map[string]any, []Warning, error) {
	connectorClass, ok := connector.Config["connector.class"]
	if !ok {
		return nil, nil, fmt.Errorf("'connector.class' not found in config")
	}

	// connector.Config is map[string]any (JSON-decoded from the Connect REST API
	// or rehydrated from the state file), so connector.class can deserialize to a
	// non-string (number/bool/object/null) on a malformed or tampered source. A
	// bare type assertion would panic and crash the whole migrate-connectors run;
	// the comma-ok form turns it into a per-connector error the Run loop skips.
	connectorClassStr, ok := connectorClass.(string)
	if !ok {
		return nil, nil, fmt.Errorf("'connector.class' is not a string")
	}

	pluginName, err := connector_utils.InferPluginName(connectorClassStr)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to determine plugin name: %w", err)
	}

	baseURL := mc.baseURL
	if baseURL == "" {
		baseURL = defaultTranslateBaseURL
	}
	url := fmt.Sprintf(
		"%s/connect/v1/environments/%s/clusters/%s/connector-plugins/%s/config/translate",
		baseURL,
		mc.EnvironmentId,
		mc.ClusterId,
		pluginName,
	)

	configJSON, err := json.Marshal(connector.Config)
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
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("api request failed with status %d: %s", resp.StatusCode, string(body))
	}

	var response TranslateResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	return response.Config, response.Warnings, nil
}
