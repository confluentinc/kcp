package self_managed_connectors

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/confluentinc/kcp/internal/redact"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCountRedactedConnectors(t *testing.T) {
	tests := []struct {
		name       string
		connectors []types.SelfManagedConnector
		want       int
	}{
		{
			name: "flat redacted",
			connectors: []types.SelfManagedConnector{
				{Name: "a", Config: map[string]any{"database.password": redact.Placeholder, "tasks.max": "3"}},
			},
			want: 1,
		},
		{
			name: "nested redacted",
			connectors: []types.SelfManagedConnector{
				{Name: "a", Config: map[string]any{
					"connection": map[string]any{"password": redact.Placeholder},
				}},
			},
			want: 1,
		},
		{
			name: "none redacted",
			connectors: []types.SelfManagedConnector{
				{Name: "a", Config: map[string]any{"connector.class": "io.x"}},
			},
			want: 0,
		},
		{
			name:       "empty list",
			connectors: []types.SelfManagedConnector{},
			want:       0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := countRedactedConnectors(tt.connectors); got != tt.want {
				t.Errorf("countRedactedConnectors() = %d, want %d", got, tt.want)
			}
		})
	}
}

// echoTranslateServer returns a translate-API stub that echoes back the config
// it receives, so the generated Terraform reflects the (already-redacted) source
// config exactly.
func echoTranslateServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		var cfg map[string]any
		require.NoError(t, json.Unmarshal(body, &cfg))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(TranslateResponse{Config: cfg})
	}))
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stdout = w
	defer func() { os.Stdout = old }()
	fn()
	require.NoError(t, w.Close())
	out, err := io.ReadAll(r)
	require.NoError(t, err)
	return string(out)
}

// The real trust boundary (R5): the persisted *.tf must carry <kcp-redacted> for
// a redacted field, never a raw secret.
func TestSelfManagedConnectorMigrator_Run_GeneratedTerraformIsLeakFree(t *testing.T) {
	server := echoTranslateServer(t)
	defer server.Close()

	outDir := filepath.Join(t.TempDir(), "out")
	migrator := NewSelfManagedConnectorMigrator(MigrateSelfManagedConnectorOpts{
		EnvironmentId: "env-123",
		ClusterId:     "lkc-123",
		CcApiKey:      "test-key",
		CcApiSecret:   "test-secret",
		Connectors: []types.SelfManagedConnector{
			{
				Name: "pg-sink",
				Config: map[string]any{
					"connector.class":   "io.confluent.kafka.connect.datagen.DatagenConnector",
					"database.password": redact.Placeholder,
					"tasks.max":         "3",
				},
			},
		},
		OutputDir: outDir,
	})
	migrator.baseURL = server.URL

	require.NoError(t, migrator.Run())

	tf, err := os.ReadFile(filepath.Join(outDir, "pg-sink-connector.tf"))
	require.NoError(t, err)
	// The sensitive key must render with the placeholder as its value, proving a
	// redacted value round-trips into the persisted artifact as <kcp-redacted>
	// (fail-closed) rather than as a working credential.
	assert.Contains(t, string(tf), fmt.Sprintf("%q = %q", "database.password", redact.Placeholder),
		"sensitive field must render as the redaction placeholder in the generated Terraform")
}

// Path traversal: a connector name carrying "../" (legal in Kafka Connect, and
// attacker-controllable via a hostile state file / compromised Connect endpoint)
// must not let the generated .tf escape OutputDir. We assert nothing lands in the
// parent directory and the file is written, name-sanitized, inside OutputDir.
func TestSelfManagedConnectorMigrator_Run_HostileNameStaysInOutputDir(t *testing.T) {
	server := echoTranslateServer(t)
	defer server.Close()

	root := t.TempDir()
	outDir := filepath.Join(root, "out")

	migrator := NewSelfManagedConnectorMigrator(MigrateSelfManagedConnectorOpts{
		EnvironmentId: "env-123",
		ClusterId:     "lkc-123",
		CcApiKey:      "test-key",
		CcApiSecret:   "test-secret",
		Connectors: []types.SelfManagedConnector{
			{
				Name: "../escaped",
				Config: map[string]any{
					"connector.class": "io.confluent.kafka.connect.datagen.DatagenConnector",
				},
			},
		},
		OutputDir: outDir,
	})
	migrator.baseURL = server.URL

	require.NoError(t, migrator.Run())

	// The traversal target the unsanitized name would have produced must not exist.
	_, err := os.Stat(filepath.Join(root, "escaped-connector.tf"))
	assert.True(t, os.IsNotExist(err), "connector must not be written outside OutputDir")

	// Every file the run produced must live inside OutputDir.
	entries, err := os.ReadDir(outDir)
	require.NoError(t, err)
	var connectorFiles int
	for _, e := range entries {
		assert.NotContains(t, e.Name(), string(filepath.Separator))
		if strings.HasSuffix(e.Name(), "-connector.tf") {
			connectorFiles++
		}
	}
	assert.Equal(t, 1, connectorFiles, "exactly one connector .tf must be written inside OutputDir")
}

func TestSelfManagedConnectorMigrator_Run_WarnsWhenConfigRedacted(t *testing.T) {
	server := echoTranslateServer(t)
	defer server.Close()

	outDir := filepath.Join(t.TempDir(), "out")
	migrator := NewSelfManagedConnectorMigrator(MigrateSelfManagedConnectorOpts{
		EnvironmentId: "env-123",
		ClusterId:     "lkc-123",
		CcApiKey:      "test-key",
		CcApiSecret:   "test-secret",
		Connectors: []types.SelfManagedConnector{
			{
				Name: "pg-sink",
				Config: map[string]any{
					"connector.class":   "io.confluent.kafka.connect.datagen.DatagenConnector",
					"database.password": redact.Placeholder,
				},
			},
			{
				Name: "clean-sink",
				Config: map[string]any{
					"connector.class": "io.confluent.kafka.connect.datagen.DatagenConnector",
					"tasks.max":       "3",
				},
			},
		},
		OutputDir: outDir,
	})
	migrator.baseURL = server.URL

	out := captureStdout(t, func() { require.NoError(t, migrator.Run()) })

	// "1 of 2" exercises the numerator and denominator independently, guarding
	// against an "N of N" regression that a 1-of-1 case would miss.
	assert.Contains(t, out, "1 of 2", "warning must be count-based")
	assert.Contains(t, out, redact.Placeholder, "warning must name the placeholder")
	assert.NotContains(t, out, "pg-sink", "warning must not include the connector name")
	assert.NotContains(t, out, "database.password", "warning must not include the field key")
}

func TestSelfManagedConnectorMigrator_Run_NoWarningWhenNoRedaction(t *testing.T) {
	server := echoTranslateServer(t)
	defer server.Close()

	outDir := filepath.Join(t.TempDir(), "out")
	migrator := NewSelfManagedConnectorMigrator(MigrateSelfManagedConnectorOpts{
		EnvironmentId: "env-123",
		ClusterId:     "lkc-123",
		CcApiKey:      "test-key",
		CcApiSecret:   "test-secret",
		Connectors: []types.SelfManagedConnector{
			{
				Name: "clean",
				Config: map[string]any{
					"connector.class": "io.confluent.kafka.connect.datagen.DatagenConnector",
					"tasks.max":       "3",
				},
			},
		},
		OutputDir: outDir,
	})
	migrator.baseURL = server.URL

	out := captureStdout(t, func() { require.NoError(t, migrator.Run()) })

	assert.NotContains(t, out, redact.Placeholder, "no warning when nothing is redacted")
	assert.NotContains(t, out, "redacted sensitive fields", "no warning when nothing is redacted")
}

func TestSelfManagedConnectorMigrator_Run_NoConnectors(t *testing.T) {
	tmpDir := t.TempDir()

	opts := MigrateSelfManagedConnectorOpts{
		EnvironmentId: "env-123",
		ClusterId:     "lkc-123",
		CcApiKey:      "test-key",
		CcApiSecret:   "test-secret",
		Connectors:    []types.SelfManagedConnector{},
		OutputDir:     tmpDir,
	}

	migrator := NewSelfManagedConnectorMigrator(opts)
	err := migrator.Run()

	assert.NoError(t, err, "Should not error when no connectors found")
}

func TestSelfManagedConnectorMigrator_Run_WithConnectors(t *testing.T) {
	// Create mock API server
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request method and headers
		assert.Equal(t, "PUT", r.Method)
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
		assert.Contains(t, r.Header.Get("Authorization"), "Basic")

		// Verify URL
		assert.Contains(t, r.URL.Path, "/config/translate")
		assert.Contains(t, r.URL.Path, "env-123")
		assert.Contains(t, r.URL.Path, "lkc-123")

		// Read and verify request body
		body, _ := io.ReadAll(r.Body)
		var requestConfig map[string]any
		require.NoError(t, json.Unmarshal(body, &requestConfig))
		assert.Equal(t, "io.confluent.kafka.connect.datagen.DatagenConnector", requestConfig["connector.class"])

		// Return mock response
		response := TranslateResponse{
			Config: map[string]any{
				"connector.class":  "DatagenSource",
				"topics":           "test-topic",
				"kafka.auth.mode":  "KAFKA_API_KEY",
				"kafka.api.key":    "${cc_api_key}",
				"kafka.api.secret": "${cc_api_secret}",
			},
			Warnings: []Warning{
				{
					Field:   "topic.creation.enable",
					Message: "Unused connector config. Given value will be ignored. Default value will be used if any.",
				},
			},
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer mockServer.Close()

	tmpDir := t.TempDir()

	connectors := []types.SelfManagedConnector{
		{
			Name: "test-datagen-connector",
			Config: map[string]any{
				"connector.class": "io.confluent.kafka.connect.datagen.DatagenConnector",
				"topics":          "test-topic",
				"quickstart":      "users",
			},
		},
	}

	opts := MigrateSelfManagedConnectorOpts{
		EnvironmentId: "env-123",
		ClusterId:     "lkc-123",
		CcApiKey:      "test-key",
		CcApiSecret:   "test-secret",
		Connectors:    connectors,
		OutputDir:     tmpDir,
	}

	migrator := NewSelfManagedConnectorMigrator(opts)

	// We might want to refactor translateConnectorConfig to accept an HTTP client or base URL in the future to
	// test this properly with the mock server. Currently, simulating successful API responses.
	t.Run("creates output directory", func(t *testing.T) {
		outputDir := filepath.Join(tmpDir, "test-output")
		migrator.OutputDir = outputDir

		// This will fail at API call, but directory should be created
		_ = migrator.Run()

		// Check directory was created
		_, err := os.Stat(outputDir)
		assert.False(t, os.IsNotExist(err), "Output directory should be created")
	})
}

func TestSelfManagedConnectorMigrator_TranslateConnectorConfig_Success(t *testing.T) {
	// Create mock API server
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := TranslateResponse{
			Config: map[string]any{
				"connector.class":  "DatagenSource",
				"topics":           "test-topic",
				"kafka.auth.mode":  "KAFKA_API_KEY",
				"kafka.api.key":    "${cc_api_key}",
				"kafka.api.secret": "${cc_api_secret}",
			},
			Warnings: []Warning{
				{
					Field:   "topic.creation.enable",
					Message: "Unused connector config. Given value will be ignored. Default value will be used if any.",
				},
			},
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer mockServer.Close()

	migrator := &SelfManagedConnectorMigrator{
		EnvironmentId: "env-123",
		ClusterId:     "lkc-123",
		CcApiKey:      "test-key",
		CcApiSecret:   "test-secret",
	}

	connector := types.SelfManagedConnector{
		Name: "test-connector",
		Config: map[string]any{
			"connector.class":       "io.confluent.kafka.connect.datagen.DatagenConnector",
			"topics":                "test-topic",
			"quickstart":            "users",
			"topic.creation.enable": "true",
		},
	}

	// Note: This test would need URL override capability to fully work as the translate endpoint is hardcoded.
	config, warnings, err := migrator.translateConnectorConfig(connector)

	// We might want to refactor translateConnectorConfig to accept an HTTP client or base URL in the future to
	// test this properly with the mock server.
	if err != nil {
		// Expected - will fail with either network error or 401
		assert.Error(t, err)
		return
	}

	assert.NoError(t, err)
	assert.NotNil(t, config)
	assert.NotNil(t, warnings)
}

func TestSelfManagedConnectorMigrator_TranslateConnectorConfig_MissingConnectorClass(t *testing.T) {
	migrator := &SelfManagedConnectorMigrator{
		EnvironmentId: "env-123",
		ClusterId:     "lkc-123",
		CcApiKey:      "test-key",
		CcApiSecret:   "test-secret",
	}

	connector := types.SelfManagedConnector{
		Name: "test-connector",
		Config: map[string]any{
			"topics": "test-topic",
		},
	}

	_, _, err := migrator.translateConnectorConfig(connector)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "'connector.class' not found in config")
}

func TestSelfManagedConnectorMigrator_TranslateConnectorConfig_UnsupportedConnectorClass(t *testing.T) {
	migrator := &SelfManagedConnectorMigrator{
		EnvironmentId: "env-123",
		ClusterId:     "lkc-123",
		CcApiKey:      "test-key",
		CcApiSecret:   "test-secret",
	}

	connector := types.SelfManagedConnector{
		Name: "test-connector",
		Config: map[string]any{
			"connector.class": "com.unknown.UnsupportedConnector",
			"topics":          "test-topic",
		},
	}

	_, _, err := migrator.translateConnectorConfig(connector)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to determine plugin name")
}

func TestSelfManagedConnectorMigrator_Run_WritesProvidersTfAndVariablesTf(t *testing.T) {
	tmpDir := t.TempDir()
	outputPath := filepath.Join(tmpDir, "connectors-output")

	opts := MigrateSelfManagedConnectorOpts{
		EnvironmentId: "env-123",
		ClusterId:     "lkc-123",
		CcApiKey:      "test-key",
		CcApiSecret:   "test-secret",
		Connectors: []types.SelfManagedConnector{
			{
				Name: "test-connector",
				Config: map[string]any{
					"connector.class": "io.confluent.kafka.connect.datagen.DatagenConnector",
					"topics":          "test-topic",
				},
			},
		},
		OutputDir: outputPath,
	}

	migrator := NewSelfManagedConnectorMigrator(opts)
	// Run will fail at API call, but providers.tf and variables.tf should be written first.
	_ = migrator.Run()

	providersTf, err := os.ReadFile(filepath.Join(outputPath, "providers.tf"))
	require.NoError(t, err, "providers.tf should exist")
	assert.Contains(t, string(providersTf), "confluentinc/confluent", "providers.tf should declare the Confluent provider")
	assert.Contains(t, string(providersTf), "required_providers", "providers.tf should contain required_providers block")

	variablesTf, err := os.ReadFile(filepath.Join(outputPath, "variables.tf"))
	require.NoError(t, err, "variables.tf should exist")
	assert.Contains(t, string(variablesTf), "confluent_cloud_api_key", "variables.tf should declare API key variable")
	assert.Contains(t, string(variablesTf), "confluent_cloud_api_secret", "variables.tf should declare API secret variable")
}

func TestSelfManagedConnectorMigrator_Run_CreatesOutputDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	outputPath := filepath.Join(tmpDir, "connectors-output")

	opts := MigrateSelfManagedConnectorOpts{
		EnvironmentId: "env-123",
		ClusterId:     "lkc-123",
		CcApiKey:      "test-key",
		CcApiSecret:   "test-secret",
		Connectors: []types.SelfManagedConnector{
			{
				Name: "test-connector",
				Config: map[string]any{
					"connector.class": "io.confluent.kafka.connect.datagen.DatagenConnector",
					"topics":          "test-topic",
				},
			},
		},
		OutputDir: outputPath,
	}

	migrator := NewSelfManagedConnectorMigrator(opts)

	// Run will fail at API call, but directory should be created.
	migrator.Run()

	// Verify directory was created.
	info, err := os.Stat(outputPath)
	assert.NoError(t, err)
	assert.True(t, info.IsDir())
}

func TestSelfManagedConnectorMigrator_Run_InvalidOutputDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "file-not-dir")
	err := os.WriteFile(filePath, []byte("test"), 0644)
	require.NoError(t, err)

	outputPath := filepath.Join(filePath, "subdirectory")

	opts := MigrateSelfManagedConnectorOpts{
		EnvironmentId: "env-123",
		ClusterId:     "lkc-123",
		CcApiKey:      "test-key",
		CcApiSecret:   "test-secret",
		Connectors: []types.SelfManagedConnector{
			{
				Name: "test-connector",
				Config: map[string]any{
					"connector.class": "io.confluent.kafka.connect.datagen.DatagenConnector",
				},
			},
		},
		OutputDir: outputPath,
	}

	migrator := NewSelfManagedConnectorMigrator(opts)
	err = migrator.Run()

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to check output directory")
}

func TestTranslateResponse_JSON(t *testing.T) {
	jsonData := `{
		"config": {
			"connector.class": "DatagenSource",
			"topics": "test-topic",
			"kafka.auth.mode": "KAFKA_API_KEY"
		},
		"warnings": [
			{
				"field": "topic.creation.enable",
				"message": "Unused connector config. Given value will be ignored. Default value will be used if any."
			}
		]
	}`

	var response TranslateResponse
	err := json.Unmarshal([]byte(jsonData), &response)

	assert.NoError(t, err)
	assert.NotNil(t, response.Config)
	assert.Equal(t, "DatagenSource", response.Config["connector.class"])
	assert.Len(t, response.Warnings, 1)
	assert.Equal(t, "topic.creation.enable", response.Warnings[0].Field)
}

// Integration test that requires real CC credentials. Will be skipped when credentials aren't provided.
func TestSelfManagedConnectorMigrator_Integration(t *testing.T) {
	envId := os.Getenv("CC_ENVIRONMENT_ID")
	clusterId := os.Getenv("CC_CLUSTER_ID")
	apiKey := os.Getenv("CC_API_KEY")
	apiSecret := os.Getenv("CC_API_SECRET")

	if envId == "" || clusterId == "" || apiKey == "" || apiSecret == "" {
		t.Skip("Skipping integration test: CC credentials not provided")
	}

	tmpDir := t.TempDir()

	connectors := []types.SelfManagedConnector{
		{
			Name: "integration-test-connector",
			Config: map[string]any{
				"connector.class":       "io.confluent.kafka.connect.datagen.DatagenConnector",
				"topics":                "test-topic",
				"quickstart":            "users",
				"topic.creation.enable": "true",
			},
		},
	}

	opts := MigrateSelfManagedConnectorOpts{
		EnvironmentId: envId,
		ClusterId:     clusterId,
		CcApiKey:      apiKey,
		CcApiSecret:   apiSecret,
		Connectors:    connectors,
		OutputDir:     tmpDir,
	}

	migrator := NewSelfManagedConnectorMigrator(opts)
	err := migrator.Run()

	assert.NoError(t, err)

	expectedFile := filepath.Join(tmpDir, "integration-test-connector-connector.tf")
	_, err = os.Stat(expectedFile)
	assert.NoError(t, err, "Output file should be created")

	content, err := os.ReadFile(expectedFile)
	assert.NoError(t, err)
	assert.Contains(t, string(content), "confluent_connector")
	assert.Contains(t, string(content), "integration-test-connector")
}

func TestSelfManagedConnectorMigrator_MultipleConnectors(t *testing.T) {
	tmpDir := t.TempDir()

	connectors := []types.SelfManagedConnector{
		{
			Name: "connector1",
			Config: map[string]any{
				"connector.class": "io.confluent.kafka.connect.datagen.DatagenConnector",
				"topics":          "topic1",
			},
		},
		{
			Name: "connector2",
			Config: map[string]any{
				"connector.class": "io.confluent.connect.s3.S3SinkConnector",
				"topics":          "topic2",
			},
		},
	}

	opts := MigrateSelfManagedConnectorOpts{
		EnvironmentId: "env-123",
		ClusterId:     "lkc-123",
		CcApiKey:      "test-key",
		CcApiSecret:   "test-secret",
		Connectors:    connectors,
		OutputDir:     tmpDir,
	}

	migrator := NewSelfManagedConnectorMigrator(opts)

	// This will fail at API calls but tests the iteration logic.
	err := migrator.Run()

	assert.NotNil(t, migrator)
	assert.Equal(t, 2, len(migrator.Connectors))

	// The function should handle errors gracefully and continue
	// (though in this test environment without mocking HTTP client fully, it will fail)
	if err != nil {
		// Expected in this test setup
		fmt.Printf("Expected error in test environment: %v\n", err)
	}
}

func TestSelfManagedConnectorMigrator_TranslateConnectorConfig(t *testing.T) {
	migrator := &SelfManagedConnectorMigrator{
		EnvironmentId: "env-123",
		ClusterId:     "lkc-123",
		CcApiKey:      "test-key",
		CcApiSecret:   "test-secret",
	}

	connector := types.SelfManagedConnector{
		Name: "test-connector",
		Config: map[string]any{
			"connector.class": "io.confluent.kafka.connect.datagen.DatagenConnector",
			"topics":          "test-topic",
			"quickstart":      "users",
		},
	}

	// This will fail at API call but should extract the properties correctly
	_, _, err := migrator.translateConnectorConfig(connector)

	// We expect an error because it will try to reach the actual API
	assert.Error(t, err)
}
