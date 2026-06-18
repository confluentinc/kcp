package hcl

import (
	"github.com/confluentinc/kcp/internal/services/hcl/hclrequests"
	"github.com/confluentinc/kcp/internal/services/hcl/hcltypes"
)

// MigrationInfraGenerator generates Terraform modules for MSK-to-CC migration infrastructure.
type MigrationInfraGenerator interface {
	GenerateTerraformModules(request hclrequests.MigrationWizardRequest) hcltypes.MigrationInfraTerraformProject
}

// TargetInfraGenerator generates Terraform files for Confluent Cloud target infrastructure.
type TargetInfraGenerator interface {
	GenerateTerraformFiles(request hclrequests.TargetClusterWizardRequest) hcltypes.MigrationInfraTerraformProject
}

// MigrationScriptsGenerator generates Terraform files for migration scripts (mirror topics, ACLs, schemas).
type MigrationScriptsGenerator interface {
	GenerateMirrorTopicsFiles(request hclrequests.MirrorTopicsRequest) (hcltypes.MigrationScriptsTerraformProject, error)
	GenerateMigrateAclsFiles(request hclrequests.MigrateAclsRequest) (hcltypes.TerraformFiles, error)
	GenerateMigrateSchemasFiles(request hclrequests.MigrateSchemasRequest) (hcltypes.MigrationScriptsTerraformProject, error)
	GenerateMigrateGlueSchemasFiles(request hclrequests.MigrateGlueSchemasRequest) (hcltypes.MigrationScriptsTerraformProject, error)
}

// Compile-time interface satisfaction checks.
var (
	_ MigrationInfraGenerator   = (*MigrationInfraHCLService)(nil)
	_ TargetInfraGenerator      = (*TargetInfraHCLService)(nil)
	_ MigrationScriptsGenerator = (*MigrationScriptsHCLService)(nil)
)
