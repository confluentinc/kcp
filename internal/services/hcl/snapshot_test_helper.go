package hcl

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/confluentinc/kcp/internal/types"
	"github.com/gruntwork-io/terratest/modules/terraform"
	"github.com/stretchr/testify/require"
)

// projectToFiles flattens a MigrationInfraTerraformProject into a map of filename → content.
func projectToFiles(project types.MigrationInfraTerraformProject) map[string]string {
	files := map[string]string{}

	if project.MainTf != "" {
		files["main.tf"] = project.MainTf
	}
	if project.ProvidersTf != "" {
		files["providers.tf"] = project.ProvidersTf
	}
	if project.VariablesTf != "" {
		files["variables.tf"] = project.VariablesTf
	}
	if project.OutputsTf != "" {
		files["outputs.tf"] = project.OutputsTf
	}
	if project.ReadmeMd != "" {
		files["README.md"] = project.ReadmeMd
	}
	if project.InputsAutoTfvars != "" {
		files["inputs.auto.tfvars"] = project.InputsAutoTfvars
	}

	for _, mod := range project.Modules {
		prefix := fmt.Sprintf("modules/%s/", mod.Name)
		if mod.MainTf != "" {
			files[prefix+"main.tf"] = mod.MainTf
		}
		if mod.VariablesTf != "" {
			files[prefix+"variables.tf"] = mod.VariablesTf
		}
		if mod.OutputsTf != "" {
			files[prefix+"outputs.tf"] = mod.OutputsTf
		}
		if mod.VersionsTf != "" {
			files[prefix+"versions.tf"] = mod.VersionsTf
		}
		for name, content := range mod.AdditionalFiles {
			files[prefix+name] = content
		}
	}

	return files
}

// terraformFilesToMap flattens a TerraformFiles into a map of filename → content.
func terraformFilesToMap(tf types.TerraformFiles) map[string]string {
	files := map[string]string{}

	if tf.MainTf != "" {
		files["main.tf"] = tf.MainTf
	}
	if tf.ProvidersTf != "" {
		files["providers.tf"] = tf.ProvidersTf
	}
	if tf.VariablesTf != "" {
		files["variables.tf"] = tf.VariablesTf
	}
	if tf.OutputsTf != "" {
		files["outputs.tf"] = tf.OutputsTf
	}
	if tf.InputsAutoTfvars != "" {
		files["inputs.auto.tfvars"] = tf.InputsAutoTfvars
	}
	for name, content := range tf.PerPrincipalTf {
		files["per_principal/"+name] = content
	}

	return files
}

// schemaProjectToFiles flattens a MigrationScriptsTerraformProject into a map.
func schemaProjectToFiles(project types.MigrationScriptsTerraformProject) map[string]string {
	files := map[string]string{}

	for _, folder := range project.Folders {
		prefix := fmt.Sprintf("folders/%s/", folder.Name)
		if folder.MainTf != "" {
			files[prefix+"main.tf"] = folder.MainTf
		}
		if folder.ProvidersTf != "" {
			files[prefix+"providers.tf"] = folder.ProvidersTf
		}
		if folder.VariablesTf != "" {
			files[prefix+"variables.tf"] = folder.VariablesTf
		}
		if folder.InputsAutoTfvars != "" {
			files[prefix+"inputs.auto.tfvars"] = folder.InputsAutoTfvars
		}
	}

	return files
}

// validateTerraformProject validates Terraform syntax by writing files to a temp
// directory and running terraform init + validate. This does NOT deploy infrastructure.
func validateTerraformProject(t *testing.T, files map[string]string) {
	t.Helper()

	// Skip if SKIP_TERRAFORM_VALIDATION env var is set (for faster local iteration)
	if os.Getenv("SKIP_TERRAFORM_VALIDATION") == "true" {
		t.Log("Skipping Terraform validation (SKIP_TERRAFORM_VALIDATION=true)")
		return
	}

	// Create temp directory (auto-cleanup after test)
	tempDir := t.TempDir()

	// Create plugin cache directory if it doesn't exist
	pluginCacheDir := filepath.Join(os.TempDir(), "terraform-plugin-cache")
	if err := os.MkdirAll(pluginCacheDir, 0o755); err != nil {
		t.Logf("Warning: could not create plugin cache directory: %v", err)
	}

	// Write all generated files to temp directory
	for filename, content := range files {
		// The files map has keys like "modules/cluster_link/main.tf", but Terraform
		// expects module sources like "./cluster_link", so we need to strip "modules/"
		// prefix to match the expected directory structure
		// Note: files map uses forward slashes regardless of OS (programmatic generation)
		writePath := filename
		if strings.HasPrefix(filename, "modules/") {
			writePath = strings.TrimPrefix(filename, "modules/")
		} else if strings.HasPrefix(filename, "per_principal/") {
			writePath = strings.TrimPrefix(filename, "per_principal/")
		} else if strings.HasPrefix(filename, "folders/") {
			writePath = strings.TrimPrefix(filename, "folders/")
		}

		path := filepath.Join(tempDir, writePath)

		// Create parent directories for nested modules
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))

		// Write file
		require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	}

	// Configure Terratest options
	terraformOptions := &terraform.Options{
		TerraformDir: tempDir,
		NoColor:      true,

		// Fake credentials - only needed for provider initialization
		// We're not deploying anything, just validating syntax
		EnvVars: map[string]string{
			"AWS_ACCESS_KEY_ID":          "fake",
			"AWS_SECRET_ACCESS_KEY":      "fake",
			"AWS_DEFAULT_REGION":         "us-east-1",
			"CONFLUENT_CLOUD_API_KEY":    "fake",
			"CONFLUENT_CLOUD_API_SECRET": "fake",
			"TF_PLUGIN_CACHE_DIR":        filepath.Join(os.TempDir(), "terraform-plugin-cache"),
		},
	}

	// Run terraform init and validate
	terraform.Init(t, terraformOptions)
	terraform.Validate(t, terraformOptions)
}
