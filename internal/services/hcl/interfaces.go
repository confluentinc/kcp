package hcl

import "github.com/confluentinc/kcp/internal/types"

// MigrationInfraGenerator generates Terraform modules for MSK-to-CC migration infrastructure.
type MigrationInfraGenerator interface {
	GenerateTerraformModules(request types.MigrationWizardRequest) types.MigrationInfraTerraformProject
}

// TargetInfraGenerator generates Terraform files for Confluent Cloud target infrastructure.
type TargetInfraGenerator interface {
	GenerateTerraformFiles(request types.TargetClusterWizardRequest) types.MigrationInfraTerraformProject
}

// MigrationScriptsGenerator generates Terraform files for migration scripts (mirror topics, ACLs, schemas).
type MigrationScriptsGenerator interface {
	GenerateMirrorTopicsFiles(request types.MirrorTopicsRequest) (types.TerraformFiles, error)
	GenerateMigrateAclsFiles(request types.MigrateAclsRequest) (types.TerraformFiles, error)
	GenerateMigrateSchemasFiles(request types.MigrateSchemasRequest) (types.MigrationScriptsTerraformProject, error)
	GenerateMigrateGlueSchemasFiles(request types.MigrateGlueSchemasRequest) (types.MigrationScriptsTerraformProject, error)
}

// Compile-time interface satisfaction checks.
var (
	_ MigrationInfraGenerator   = (*MigrationInfraHCLService)(nil)
	_ TargetInfraGenerator      = (*TargetInfraHCLService)(nil)
	_ MigrationScriptsGenerator = (*MigrationScriptsHCLService)(nil)
)
