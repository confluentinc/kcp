package hcl

import (
	"testing"

	"github.com/confluentinc/kcp/internal/types"
)

func TestTargetInfra_Dedicated(t *testing.T) {
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
	assertMatchesGoldenFiles(t, "TestTargetInfra_Dedicated", files)
}

func TestTargetInfra_Enterprise(t *testing.T) {
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
	assertMatchesGoldenFiles(t, "TestTargetInfra_Enterprise", files)
}

func TestTargetInfra_DedicatedPrivateLink(t *testing.T) {
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
	assertMatchesGoldenFiles(t, "TestTargetInfra_DedicatedPrivateLink", files)
}

func TestTargetInfra_EnterpriseTrailingHyphen(t *testing.T) {
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
	assertMatchesGoldenFiles(t, "TestTargetInfra_EnterpriseTrailingHyphen", files)
}

func TestTargetInfra_EnterprisePrivateLink_ExistingRoute53Zone(t *testing.T) {
	service := &TargetInfraHCLService{ResourceNames: NewTerraformResourceNames(), DeploymentID: "testdeploy"}
	request := types.TargetClusterWizardRequest{
		AwsRegion:             "eu-west-1",
		NeedsEnvironment:      true,
		EnvironmentName:       "eu-prod",
		NeedsCluster:          true,
		ClusterName:           "eu-cluster",
		ClusterType:           "enterprise",
		ClusterAvailability:   "MULTI_ZONE",
		NeedsPrivateLink:      true,
		ExistingRoute53ZoneId: "Z0563969185NB7OCVYNCN",
		VpcId:                 "vpc-eu-0123456789abcdef0",
		SubnetCidrRanges:      []string{"10.0.10.0/24", "10.0.11.0/24", "10.0.12.0/24"},
		PreventDestroy:        false,
	}

	project := service.GenerateTerraformFiles(request)
	files := projectToFiles(project)
	assertMatchesGoldenFiles(t, "TestTargetInfra_EnterprisePrivateLink_ExistingRoute53Zone", files)
}

func TestTargetInfra_DedicatedPrivateLink_ExistingRoute53Zone(t *testing.T) {
	service := &TargetInfraHCLService{ResourceNames: NewTerraformResourceNames(), DeploymentID: "testdeploy"}
	request := types.TargetClusterWizardRequest{
		AwsRegion:             "us-east-1",
		NeedsEnvironment:      true,
		EnvironmentName:       "production",
		NeedsCluster:          true,
		ClusterName:           "prod-cluster-pl",
		ClusterType:           "dedicated",
		ClusterAvailability:   "SINGLE_ZONE",
		ClusterCku:            2,
		NeedsPrivateLink:      true,
		ExistingRoute53ZoneId: "Z0563969185NB7OCVYNCN",
		VpcId:                 "vpc-0123456789abcdef0",
		SubnetCidrRanges:      []string{"10.0.1.0/24", "10.0.2.0/24"},
		PreventDestroy:        true,
	}

	project := service.GenerateTerraformFiles(request)
	files := projectToFiles(project)
	assertMatchesGoldenFiles(t, "TestTargetInfra_DedicatedPrivateLink_ExistingRoute53Zone", files)
}

func TestTargetInfra_EnterprisePrivateLink(t *testing.T) {
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
	assertMatchesGoldenFiles(t, "TestTargetInfra_EnterprisePrivateLink", files)
}
