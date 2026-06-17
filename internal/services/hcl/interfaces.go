package hcl

import (
	"github.com/confluentinc/kcp/internal/services/hcl/hclrequests"
	"github.com/confluentinc/kcp/internal/types"
)

// MigrationInfraGenerator generates Terraform modules for MSK-to-CC migration infrastructure.
type MigrationInfraGenerator interface {
	GenerateTerraformModules(request hclrequests.MigrationWizardRequest) types.MigrationInfraTerraformProject
}

// TargetInfraGenerator generates Terraform files for Confluent Cloud target infrastructure.
type TargetInfraGenerator interface {
	GenerateTerraformFiles(request hclrequests.TargetClusterWizardRequest) types.MigrationInfraTerraformProject
}

// MigrationScriptsGenerator generates Terraform files for migration scripts (mirror topics, ACLs, schemas).
type MigrationScriptsGenerator interface {
	GenerateMirrorTopicsFiles(request hclrequests.MirrorTopicsRequest) (types.MigrationScriptsTerraformProject, error)
	GenerateMigrateAclsFiles(request hclrequests.MigrateAclsRequest) (types.TerraformFiles, error)
	GenerateMigrateSchemasFiles(request hclrequests.MigrateSchemasRequest) (types.MigrationScriptsTerraformProject, error)
	GenerateMigrateGlueSchemasFiles(request hclrequests.MigrateGlueSchemasRequest) (types.MigrationScriptsTerraformProject, error)
}

// Compile-time interface satisfaction checks.
var (
	_ MigrationInfraGenerator   = (*MigrationInfraHCLService)(nil)
	_ TargetInfraGenerator      = (*TargetInfraHCLService)(nil)
	_ MigrationScriptsGenerator = (*MigrationScriptsHCLService)(nil)
)
