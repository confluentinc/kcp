package hcl

import (
	"github.com/confluentinc/kcp/internal/services/hcl/hcltypes"
	"github.com/confluentinc/kcp/internal/types"
)

// MigrationInfraGenerator generates Terraform modules for MSK-to-CC migration infrastructure.
type MigrationInfraGenerator interface {
	GenerateTerraformModules(request types.MigrationWizardRequest) hcltypes.MigrationInfraTerraformProject
}

// TargetInfraGenerator generates Terraform files for Confluent Cloud target infrastructure.
type TargetInfraGenerator interface {
	GenerateTerraformFiles(request types.TargetClusterWizardRequest) hcltypes.MigrationInfraTerraformProject
}

// MigrationScriptsGenerator generates Terraform files for migration scripts (mirror topics, ACLs, schemas).
type MigrationScriptsGenerator interface {
	GenerateMirrorTopicsFiles(request types.MirrorTopicsRequest) (hcltypes.MigrationScriptsTerraformProject, error)
	GenerateMigrateAclsFiles(request types.MigrateAclsRequest) (hcltypes.TerraformFiles, error)
	GenerateMigrateSchemasFiles(request types.MigrateSchemasRequest) (hcltypes.MigrationScriptsTerraformProject, error)
	GenerateMigrateGlueSchemasFiles(request types.MigrateGlueSchemasRequest) (hcltypes.MigrationScriptsTerraformProject, error)
}

// Compile-time interface satisfaction checks.
var (
	_ MigrationInfraGenerator   = (*MigrationInfraHCLService)(nil)
	_ TargetInfraGenerator      = (*TargetInfraHCLService)(nil)
	_ MigrationScriptsGenerator = (*MigrationScriptsHCLService)(nil)
)
