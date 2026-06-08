package types

type TerraformState struct {
	Outputs TerraformOutputOld `json:"outputs"`
}

// a type for the output.json file in the target_env folder
// NOTE: This will be deprecated once we are completely on the HCL-servrice based approach.
type TerraformOutputOld struct {
	ConfluentCloudClusterApiKey                TerraformOutputValue `json:"confluent_cloud_cluster_api_key"`
	ConfluentCloudClusterApiKeySecret          TerraformOutputValue `json:"confluent_cloud_cluster_api_key_secret"`
	ConfluentCloudClusterId                    TerraformOutputValue `json:"confluent_cloud_cluster_id"`
	ConfluentCloudClusterRestEndpoint          TerraformOutputValue `json:"confluent_cloud_cluster_rest_endpoint"`
	ConfluentCloudClusterBootstrapEndpoint     TerraformOutputValue `json:"confluent_cloud_cluster_bootstrap_endpoint"`
	ConfluentPlatformControllerBootstrapServer TerraformOutputValue `json:"confluent_platform_controller_bootstrap_server"`
}

type TerraformOutputValue struct {
	Sensitive bool   `json:"sensitive"`
	Type      string `json:"type"`
	Value     any    `json:"value"`
}

type TerraformFiles struct {
	MainTf           string            `json:"main.tf"`
	ProvidersTf      string            `json:"providers.tf"`
	VariablesTf      string            `json:"variables.tf"`
	InputsAutoTfvars string            `json:"inputs.auto.tfvars"`
	OutputsTf        string            `json:"outputs.tf"`
	PerPrincipalTf   map[string]string `json:"per_principal_tf,omitempty"`
}

type TerraformVariable struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Sensitive   bool   `json:"sensitive"`
	Type        string `json:"type"`
}

type TerraformOutput struct {
	Name        string
	Description string
	Sensitive   bool
	Value       string
}

// MigrationInfraTerraformModule represents a Terraform module within the migration infrastructure
// configuration. Each module contains its own Terraform files and additional assets.
type MigrationInfraTerraformModule struct {
	Name            string            `json:"name"`
	MainTf          string            `json:"main.tf"`
	VariablesTf     string            `json:"variables.tf"`
	OutputsTf       string            `json:"outputs.tf"`
	VersionsTf      string            `json:"versions.tf"`
	AdditionalFiles map[string]string `json:"additional_files"`
}

// MigrationInfraTerraformProject represents the complete Terraform configuration for migration
// infrastructure. "project" = root config + modules
type MigrationInfraTerraformProject struct {
	MainTf           string                          `json:"main.tf"`
	ProvidersTf      string                          `json:"providers.tf"`
	VariablesTf      string                          `json:"variables.tf"`
	OutputsTf        string                          `json:"outputs.tf"`
	ReadmeMd         string                          `json:"README.md"`
	InputsAutoTfvars string                          `json:"inputs.auto.tfvars"`
	Modules          []MigrationInfraTerraformModule `json:"modules"`
}

// MigrationScriptsTerraformProject represents the complete Terraform configuration for migration scripts
type MigrationScriptsTerraformProject struct {
	// not really a module, but its the same structure
	Folders []MigrationScriptsTerraformFolder `json:"modules"`
}

// MigrationScriptsTerraformFolder represents a Terraform folder within the migration scripts
type MigrationScriptsTerraformFolder struct {
	Name             string            `json:"name"`
	MainTf           string            `json:"main.tf"`
	ProvidersTf      string            `json:"providers.tf"`
	VariablesTf      string            `json:"variables.tf"`
	InputsAutoTfvars string            `json:"inputs.auto.tfvars"`
	AdditionalFiles  map[string]string `json:"additional_files,omitempty"`
}
