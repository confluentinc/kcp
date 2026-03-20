package hcl

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/confluentinc/kcp/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var update = flag.Bool("update", false, "update golden files")

// assertMatchesGoldenFiles compares generated files against golden files in testdata/<dir>.
// When run with -update, it writes the golden files instead.
func assertMatchesGoldenFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()

	goldenDir := filepath.Join("testdata", dir)

	if *update {
		require.NoError(t, os.MkdirAll(goldenDir, 0o755))
		for name, content := range files {
			path := filepath.Join(goldenDir, name+".golden")
			require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
			require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
		}
		return
	}

	// Sort file names for deterministic test output
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		content := files[name]
		path := filepath.Join(goldenDir, name+".golden")
		expected, err := os.ReadFile(path)
		require.NoError(t, err, "golden file %s not found; run with -update to create", path)
		assert.Equal(t, string(expected), content, "mismatch in %s", name)
	}
}

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
