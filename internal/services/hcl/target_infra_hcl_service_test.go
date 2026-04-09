package hcl

import (
	"testing"

	"github.com/confluentinc/kcp/internal/types"
	"github.com/stretchr/testify/require"
)

func TestTargetInfra_Dedicated(t *testing.T) {
	t.Parallel()

	service := &TargetInfraHCLService{ResourceNames: NewTerraformResourceNames(), DeploymentID: "testdeploy"}
	request := types.TargetClusterWizardRequest{
		AwsRegion:           "us-east-1",
		NeedsEnvironment:    true,
		EnvironmentName:     "production",
		NeedsCluster:        true,
		ClusterName:         "prod-cluster",
		ClusterType:         "dedicated",
		ClusterAvailability: "SINGLE_ZONE",
		ClusterCku:          1,
		NeedsPrivateLink:    false,
		PreventDestroy:      true,
	}

	project := service.GenerateTerraformFiles(request)
	files := projectToFiles(project)
	validateTerraformProject(t, files)
}

func TestTargetInfra_Enterprise(t *testing.T) {
	t.Parallel()

	service := &TargetInfraHCLService{ResourceNames: NewTerraformResourceNames(), DeploymentID: "testdeploy"}
	request := types.TargetClusterWizardRequest{
		AwsRegion:           "us-west-2",
		NeedsEnvironment:    true,
		EnvironmentName:     "staging",
		NeedsCluster:        true,
		ClusterName:         "staging-cluster",
		ClusterType:         "enterprise",
		ClusterAvailability: "MULTI_ZONE",
		NeedsPrivateLink:    false,
		PreventDestroy:      false,
	}

	project := service.GenerateTerraformFiles(request)
	files := projectToFiles(project)
	validateTerraformProject(t, files)
}

func TestTargetInfra_DedicatedPrivateLink(t *testing.T) {
	t.Parallel()

	service := &TargetInfraHCLService{ResourceNames: NewTerraformResourceNames(), DeploymentID: "testdeploy"}
	request := types.TargetClusterWizardRequest{
		AwsRegion:           "us-east-1",
		NeedsEnvironment:    true,
		EnvironmentName:     "production",
		NeedsCluster:        true,
		ClusterName:         "prod-cluster-pl",
		ClusterType:         "dedicated",
		ClusterAvailability: "SINGLE_ZONE",
		ClusterCku:          2,
		NeedsPrivateLink:    true,
		VpcId:               "vpc-0123456789abcdef0",
		SubnetCidrRanges:    []string{"10.0.1.0/24", "10.0.2.0/24"},
		PreventDestroy:      true,
	}

	project := service.GenerateTerraformFiles(request)
	files := projectToFiles(project)
	validateTerraformProject(t, files)
}

func TestTargetInfra_EnterpriseTrailingHyphen(t *testing.T) {
	t.Parallel()

	service := &TargetInfraHCLService{ResourceNames: NewTerraformResourceNames(), DeploymentID: "testdeploy"}
	request := types.TargetClusterWizardRequest{
		AwsRegion:           "us-east-1",
		NeedsEnvironment:    true,
		EnvironmentName:     "refac-env",
		NeedsCluster:        true,
		ClusterName:         "refac-cluster",
		ClusterType:         "enterprise",
		ClusterAvailability: "MULTI_ZONE",
		NeedsPrivateLink:    false,
		PreventDestroy:      false,
	}

	project := service.GenerateTerraformFiles(request)
	files := projectToFiles(project)
	validateTerraformProject(t, files)
}

func TestTargetInfra_EnterprisePrivateLink_ExistingRoute53Zone(t *testing.T) {
	t.Parallel()

	service := &TargetInfraHCLService{ResourceNames: NewTerraformResourceNames(), DeploymentID: "testdeploy"}
	request := types.TargetClusterWizardRequest{
		AwsRegion:              "eu-west-1",
		NeedsEnvironment:       true,
		EnvironmentName:        "eu-prod",
		NeedsCluster:           true,
		ClusterName:            "eu-cluster",
		ClusterType:            "enterprise",
		ClusterAvailability:    "MULTI_ZONE",
		NeedsPrivateLink:       true,
		UseExistingRoute53Zone: true,
		VpcId:                  "vpc-eu-0123456789abcdef0",
		SubnetCidrRanges:       []string{"10.0.10.0/24", "10.0.11.0/24", "10.0.12.0/24"},
		PreventDestroy:         false,
	}

	project := service.GenerateTerraformFiles(request)
	files := projectToFiles(project)
	validateTerraformProject(t, files)
}

func TestTargetInfra_DedicatedPrivateLink_ExistingRoute53Zone(t *testing.T) {
	t.Parallel()

	service := &TargetInfraHCLService{ResourceNames: NewTerraformResourceNames(), DeploymentID: "testdeploy"}
	request := types.TargetClusterWizardRequest{
		AwsRegion:              "us-east-1",
		NeedsEnvironment:       true,
		EnvironmentName:        "production",
		NeedsCluster:           true,
		ClusterName:            "prod-cluster-pl",
		ClusterType:            "dedicated",
		ClusterAvailability:    "SINGLE_ZONE",
		ClusterCku:             2,
		NeedsPrivateLink:       true,
		UseExistingRoute53Zone: true,
		VpcId:                  "vpc-0123456789abcdef0",
		SubnetCidrRanges:       []string{"10.0.1.0/24", "10.0.2.0/24"},
		PreventDestroy:         true,
	}

	project := service.GenerateTerraformFiles(request)
	files := projectToFiles(project)
	validateTerraformProject(t, files)
}

func TestTargetInfra_EnterprisePrivateLink(t *testing.T) {
	t.Parallel()

	service := &TargetInfraHCLService{ResourceNames: NewTerraformResourceNames(), DeploymentID: "testdeploy"}
	request := types.TargetClusterWizardRequest{
		AwsRegion:           "eu-west-1",
		NeedsEnvironment:    true,
		EnvironmentName:     "eu-prod",
		NeedsCluster:        true,
		ClusterName:         "eu-cluster",
		ClusterType:         "enterprise",
		ClusterAvailability: "MULTI_ZONE",
		NeedsPrivateLink:    true,
		VpcId:               "vpc-eu-0123456789abcdef0",
		SubnetCidrRanges:    []string{"10.0.10.0/24", "10.0.11.0/24", "10.0.12.0/24"},
		PreventDestroy:      false,
	}

	project := service.GenerateTerraformFiles(request)
	files := projectToFiles(project)
	validateTerraformProject(t, files)
}

// Edge case tests: Empty inputs
func TestTargetInfra_PrivateLink_EmptySubnetCidrArray(t *testing.T) {
	t.Parallel()

	service := &TargetInfraHCLService{ResourceNames: NewTerraformResourceNames(), DeploymentID: "testdeploy"}
	request := types.TargetClusterWizardRequest{
		AwsRegion:           "us-east-1",
		NeedsEnvironment:    true,
		EnvironmentName:     "test-env",
		NeedsCluster:        true,
		ClusterName:         "test-cluster",
		ClusterType:         "dedicated",
		ClusterAvailability: "SINGLE_ZONE",
		ClusterCku:          2,
		NeedsPrivateLink:    true,
		VpcId:               "vpc-0123456789abcdef0",
		SubnetCidrRanges:    []string{}, // Empty array
		PreventDestroy:      true,
	}

	// Should handle gracefully or validation should catch this
	project := service.GenerateTerraformFiles(request)
	files := projectToFiles(project)

	require.NotEmpty(t, files, "expected files to be generated for edge case")
	validateTerraformProject(t, files)
}

// Resource naming edge cases
func TestTargetInfra_VeryLongResourceNames(t *testing.T) {
	t.Parallel()

	service := &TargetInfraHCLService{ResourceNames: NewTerraformResourceNames(), DeploymentID: "testdeploy"}

	// 63 characters (max for some AWS resources)
	longName := "this-is-a-very-long-environment-name-for-testing-limits-max"

	request := types.TargetClusterWizardRequest{
		AwsRegion:           "us-east-1",
		NeedsEnvironment:    true,
		EnvironmentName:     longName,
		NeedsCluster:        true,
		ClusterName:         "long-cluster-name-that-tests-aws-resource-naming-limits",
		ClusterType:         "dedicated",
		ClusterAvailability: "SINGLE_ZONE",
		ClusterCku:          1,
		PreventDestroy:      true,
	}

	project := service.GenerateTerraformFiles(request)
	files := projectToFiles(project)
	validateTerraformProject(t, files)
}

func TestTargetInfra_SpecialCharactersInNames(t *testing.T) {
	t.Parallel()

	service := &TargetInfraHCLService{ResourceNames: NewTerraformResourceNames(), DeploymentID: "testdeploy"}
	request := types.TargetClusterWizardRequest{
		AwsRegion:           "us-east-1",
		NeedsEnvironment:    true,
		EnvironmentName:     "env-with-dashes-and_underscores",
		NeedsCluster:        true,
		ClusterName:         "cluster_with_underscores",
		ClusterType:         "enterprise",
		ClusterAvailability: "MULTI_ZONE",
		PreventDestroy:      false,
	}

	project := service.GenerateTerraformFiles(request)
	files := projectToFiles(project)
	validateTerraformProject(t, files)
}

func TestTargetInfra_NumericOnlyNames(t *testing.T) {
	t.Parallel()

	service := &TargetInfraHCLService{ResourceNames: NewTerraformResourceNames(), DeploymentID: "testdeploy"}
	request := types.TargetClusterWizardRequest{
		AwsRegion:           "us-east-1",
		NeedsEnvironment:    true,
		EnvironmentName:     "12345",
		NeedsCluster:        true,
		ClusterName:         "67890",
		ClusterType:         "dedicated",
		ClusterAvailability: "SINGLE_ZONE",
		ClusterCku:          1,
		PreventDestroy:      true,
	}

	project := service.GenerateTerraformFiles(request)
	files := projectToFiles(project)
	validateTerraformProject(t, files)
}

// Boundary conditions
func TestTargetInfra_MultiZone_MinimumCKUs(t *testing.T) {
	t.Parallel()

	service := &TargetInfraHCLService{ResourceNames: NewTerraformResourceNames(), DeploymentID: "testdeploy"}
	request := types.TargetClusterWizardRequest{
		AwsRegion:           "us-east-1",
		NeedsEnvironment:    true,
		EnvironmentName:     "multi-zone-test",
		NeedsCluster:        true,
		ClusterName:         "mz-cluster",
		ClusterType:         "dedicated",
		ClusterAvailability: "MULTI_ZONE",
		ClusterCku:          2, // Minimum for MULTI_ZONE
		PreventDestroy:      true,
	}

	project := service.GenerateTerraformFiles(request)
	files := projectToFiles(project)
	validateTerraformProject(t, files)
}
