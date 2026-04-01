package hcl

import (
	"testing"

	"github.com/confluentinc/kcp/internal/types"
)

func TestMigrationInfra_Public(t *testing.T) {
	service := &MigrationInfraHCLService{SSHKeySuffix: "test1", DeploymentID: "testdeploy"}
	request := types.MigrationWizardRequest{
		HasPublicMskEndpoints:   true,
		MskClusterId:            "msk-cluster-123",
		MskRegion:               "us-east-1",
		TargetEnvironmentId:     "env-abc123",
		TargetClusterId:         "lkc-xyz789",
		TargetRestEndpoint:      "https://pkc-abc123.us-east-1.aws.confluent.cloud:443",
		TargetBootstrapEndpoint: "pkc-abc123.us-east-1.aws.confluent.cloud:9092",
		ClusterLinkName:         "msk-to-cc-link",
	}

	project := service.GenerateTerraformModules(request)
	files := projectToFiles(project)
	validateTerraformProject(t, files)
}

func TestMigrationInfra_PrivateJumpCluster(t *testing.T) {
	service := &MigrationInfraHCLService{SSHKeySuffix: "test1", DeploymentID: "testdeploy"}
	request := types.MigrationWizardRequest{
		HasPublicMskEndpoints:          false,
		UseJumpClusters:                true,
		VpcId:                          "vpc-0123456789abcdef0",
		HasExistingInternetGateway:     true,
		JumpClusterInstanceType:        "kafka.m5.large",
		JumpClusterBrokerStorage:       100,
		JumpClusterBrokerSubnetCidr:    []string{"10.0.1.0/24", "10.0.2.0/24", "10.0.3.0/24"},
		JumpClusterSetupHostSubnetCidr: "10.0.4.0/24",
		MskJumpClusterAuthType:         "iam",
		MskClusterId:                   "msk-cluster-123",
		JumpClusterIamAuthRoleName:     "msk-iam-role",
		MskSaslIamBootstrapServers:     "b-1.mskcluster.abc123.c1.kafka.us-east-1.amazonaws.com:9098",
		MskRegion:                      "us-east-1",
		TargetEnvironmentId:            "env-abc123",
		TargetClusterId:                "lkc-xyz789",
		TargetRestEndpoint:             "https://pkc-abc123.us-east-1.aws.confluent.cloud:443",
		TargetBootstrapEndpoint:        "pkc-abc123.us-east-1.aws.confluent.cloud:9092",
		ClusterLinkName:                "msk-to-cc-link",
	}

	project := service.GenerateTerraformModules(request)
	files := projectToFiles(project)
	validateTerraformProject(t, files)
}

func TestMigrationInfra_ExternalOutbound(t *testing.T) {
	service := &MigrationInfraHCLService{SSHKeySuffix: "test1", DeploymentID: "testdeploy"}
	request := types.MigrationWizardRequest{
		HasPublicMskEndpoints:      false,
		UseJumpClusters:            false,
		VpcId:                      "vpc-0123456789abcdef0",
		ExtOutboundSecurityGroupId: "sg-0123456789abcdef0",
		ExtOutboundSubnetId:        "subnet-0123456789abcdef0",
		ExtOutboundBrokers: []types.ExtOutboundClusterKafkaBroker{
			{
				ID:       "b-1",
				SubnetID: "subnet-1",
				Endpoints: []types.ExtOutboundClusterKafkaEndpoint{
					{Host: "b-1.example.com", Port: 9092, IP: "10.0.1.1"},
				},
			},
		},
		MskClusterId:            "msk-cluster-123",
		MskRegion:               "us-east-1",
		TargetEnvironmentId:     "env-abc123",
		TargetClusterId:         "lkc-xyz789",
		TargetRestEndpoint:      "https://pkc-abc123.us-east-1.aws.confluent.cloud:443",
		TargetBootstrapEndpoint: "pkc-abc123.us-east-1.aws.confluent.cloud:9092",
		ClusterLinkName:         "msk-to-cc-link",
	}

	project := service.GenerateTerraformModules(request)
	files := projectToFiles(project)
	validateTerraformProject(t, files)
}

func TestMigrationInfra_ExternalOutboundUnauthTls(t *testing.T) {
	service := &MigrationInfraHCLService{SSHKeySuffix: "test1", DeploymentID: "testdeploy"}
	request := types.MigrationWizardRequest{
		HasPublicMskEndpoints:      false,
		UseJumpClusters:            false,
		VpcId:                      "vpc-0123456789abcdef0",
		ExtOutboundSecurityGroupId: "sg-0123456789abcdef0",
		ExtOutboundSubnetId:        "subnet-0123456789abcdef0",
		ExtOutboundBrokers: []types.ExtOutboundClusterKafkaBroker{
			{
				ID:       "b-1",
				SubnetID: "subnet-1",
				Endpoints: []types.ExtOutboundClusterKafkaEndpoint{
					{Host: "b-1.example.com", Port: 9094, IP: "10.0.1.1"},
				},
			},
		},
		MskJumpClusterAuthType:       "unauth_tls",
		MskUnauthTlsBootstrapServers: "b-1.mskcluster.abc123.c1.kafka.us-east-1.amazonaws.com:9094",
		MskClusterId:                 "msk-cluster-123",
		MskRegion:                    "us-east-1",
		TargetEnvironmentId:          "env-abc123",
		TargetClusterId:              "lkc-xyz789",
		TargetRestEndpoint:           "https://pkc-abc123.us-east-1.aws.confluent.cloud:443",
		TargetBootstrapEndpoint:      "pkc-abc123.us-east-1.aws.confluent.cloud:9092",
		ClusterLinkName:              "msk-to-cc-link",
	}

	project := service.GenerateTerraformModules(request)
	files := projectToFiles(project)
	validateTerraformProject(t, files)
}

// Edge case tests: Empty inputs
func TestMigrationInfra_JumpCluster_EmptySubnetArray(t *testing.T) {
	service := &MigrationInfraHCLService{SSHKeySuffix: "test1", DeploymentID: "testdeploy"}
	request := types.MigrationWizardRequest{
		HasPublicMskEndpoints:          false,
		UseJumpClusters:                true,
		VpcId:                          "vpc-0123456789abcdef0",
		HasExistingInternetGateway:     true,
		JumpClusterInstanceType:        "kafka.m5.large",
		JumpClusterBrokerStorage:       100,
		JumpClusterBrokerSubnetCidr:    []string{}, // Empty array
		JumpClusterSetupHostSubnetCidr: "10.0.4.0/24",
		MskJumpClusterAuthType:         "iam",
		MskClusterId:                   "msk-cluster-123",
		JumpClusterIamAuthRoleName:     "msk-iam-role",
		MskSaslIamBootstrapServers:     "b-1.mskcluster.abc123.c1.kafka.us-east-1.amazonaws.com:9098",
		MskRegion:                      "us-east-1",
		TargetEnvironmentId:            "env-abc123",
		TargetClusterId:                "lkc-xyz789",
		TargetRestEndpoint:             "https://pkc-abc123.us-east-1.aws.confluent.cloud:443",
		TargetBootstrapEndpoint:        "pkc-abc123.us-east-1.aws.confluent.cloud:9092",
		ClusterLinkName:                "empty-subnet-link",
	}

	project := service.GenerateTerraformModules(request)
	files := projectToFiles(project)

	// Should handle gracefully - either generate valid TF with defaults or skip
	if len(files) > 0 {
		validateTerraformProject(t, files)
	}
}

func TestMigrationInfra_JumpCluster_ZeroStorage(t *testing.T) {
	service := &MigrationInfraHCLService{SSHKeySuffix: "test1", DeploymentID: "testdeploy"}
	request := types.MigrationWizardRequest{
		HasPublicMskEndpoints:          false,
		UseJumpClusters:                true,
		VpcId:                          "vpc-0123456789abcdef0",
		HasExistingInternetGateway:     true,
		JumpClusterInstanceType:        "kafka.m5.large",
		JumpClusterBrokerStorage:       0, // Zero value
		JumpClusterBrokerSubnetCidr:    []string{"10.0.1.0/24", "10.0.2.0/24"},
		JumpClusterSetupHostSubnetCidr: "10.0.4.0/24",
		MskJumpClusterAuthType:         "iam",
		MskClusterId:                   "msk-cluster-123",
		JumpClusterIamAuthRoleName:     "msk-iam-role",
		MskSaslIamBootstrapServers:     "b-1.mskcluster.abc123.c1.kafka.us-east-1.amazonaws.com:9098",
		MskRegion:                      "us-east-1",
		TargetEnvironmentId:            "env-abc123",
		TargetClusterId:                "lkc-xyz789",
		TargetRestEndpoint:             "https://pkc-abc123.us-east-1.aws.confluent.cloud:443",
		TargetBootstrapEndpoint:        "pkc-abc123.us-east-1.aws.confluent.cloud:9092",
		ClusterLinkName:                "zero-storage-link",
	}

	project := service.GenerateTerraformModules(request)
	files := projectToFiles(project)
	validateTerraformProject(t, files)
}

func TestMigrationInfra_ExternalOutbound_NilBrokers(t *testing.T) {
	service := &MigrationInfraHCLService{SSHKeySuffix: "test1", DeploymentID: "testdeploy"}
	request := types.MigrationWizardRequest{
		HasPublicMskEndpoints:      false,
		UseJumpClusters:            false,
		VpcId:                      "vpc-0123456789abcdef0",
		ExtOutboundSecurityGroupId: "sg-0123456789abcdef0",
		ExtOutboundSubnetId:        "subnet-0123456789abcdef0",
		ExtOutboundBrokers:         nil, // Nil slice
		MskClusterId:               "msk-cluster-123",
		MskRegion:                  "us-east-1",
		TargetEnvironmentId:        "env-abc123",
		TargetClusterId:            "lkc-xyz789",
		TargetRestEndpoint:         "https://pkc-abc123.us-east-1.aws.confluent.cloud:443",
		TargetBootstrapEndpoint:    "pkc-abc123.us-east-1.aws.confluent.cloud:9092",
		ClusterLinkName:            "nil-brokers-link",
	}

	// Should handle gracefully - validation should catch this or it should fail cleanly
	project := service.GenerateTerraformModules(request)
	files := projectToFiles(project)

	if len(files) > 0 {
		validateTerraformProject(t, files)
	}
}

// Multi-subnet tests
func TestMigrationInfra_JumpCluster_SingleSubnet(t *testing.T) {
	service := &MigrationInfraHCLService{SSHKeySuffix: "test1", DeploymentID: "testdeploy"}
	request := types.MigrationWizardRequest{
		HasPublicMskEndpoints:          false,
		UseJumpClusters:                true,
		VpcId:                          "vpc-0123456789abcdef0",
		HasExistingInternetGateway:     true,
		JumpClusterInstanceType:        "kafka.m5.large",
		JumpClusterBrokerStorage:       100,
		JumpClusterBrokerSubnetCidr:    []string{"10.0.1.0/24"}, // Single subnet
		JumpClusterSetupHostSubnetCidr: "10.0.4.0/24",
		MskJumpClusterAuthType:         "iam",
		MskClusterId:                   "msk-cluster-123",
		JumpClusterIamAuthRoleName:     "msk-iam-role",
		MskSaslIamBootstrapServers:     "b-1.mskcluster.abc123.c1.kafka.us-east-1.amazonaws.com:9098",
		MskRegion:                      "us-east-1",
		TargetEnvironmentId:            "env-abc123",
		TargetClusterId:                "lkc-xyz789",
		TargetRestEndpoint:             "https://pkc-abc123.us-east-1.aws.confluent.cloud:443",
		TargetBootstrapEndpoint:        "pkc-abc123.us-east-1.aws.confluent.cloud:9092",
		ClusterLinkName:                "single-subnet-link",
	}

	project := service.GenerateTerraformModules(request)
	files := projectToFiles(project)
	validateTerraformProject(t, files)
}

func TestMigrationInfra_JumpCluster_TwoSubnets(t *testing.T) {
	service := &MigrationInfraHCLService{SSHKeySuffix: "test1", DeploymentID: "testdeploy"}
	request := types.MigrationWizardRequest{
		HasPublicMskEndpoints:          false,
		UseJumpClusters:                true,
		VpcId:                          "vpc-0123456789abcdef0",
		HasExistingInternetGateway:     true,
		JumpClusterInstanceType:        "kafka.m5.large",
		JumpClusterBrokerStorage:       100,
		JumpClusterBrokerSubnetCidr:    []string{"10.0.1.0/24", "10.0.2.0/24"}, // Two subnets
		JumpClusterSetupHostSubnetCidr: "10.0.4.0/24",
		MskJumpClusterAuthType:         "iam",
		MskClusterId:                   "msk-cluster-123",
		JumpClusterIamAuthRoleName:     "msk-iam-role",
		MskSaslIamBootstrapServers:     "b-1.mskcluster.abc123.c1.kafka.us-east-1.amazonaws.com:9098",
		MskRegion:                      "us-east-1",
		TargetEnvironmentId:            "env-abc123",
		TargetClusterId:                "lkc-xyz789",
		TargetRestEndpoint:             "https://pkc-abc123.us-east-1.aws.confluent.cloud:443",
		TargetBootstrapEndpoint:        "pkc-abc123.us-east-1.aws.confluent.cloud:9092",
		ClusterLinkName:                "two-subnet-link",
	}

	project := service.GenerateTerraformModules(request)
	files := projectToFiles(project)
	validateTerraformProject(t, files)
}

func TestMigrationInfra_JumpCluster_FiveSubnets(t *testing.T) {
	service := &MigrationInfraHCLService{SSHKeySuffix: "test1", DeploymentID: "testdeploy"}
	request := types.MigrationWizardRequest{
		HasPublicMskEndpoints:       false,
		UseJumpClusters:             true,
		VpcId:                       "vpc-0123456789abcdef0",
		HasExistingInternetGateway:  true,
		JumpClusterInstanceType:     "kafka.m5.large",
		JumpClusterBrokerStorage:    100,
		JumpClusterBrokerSubnetCidr: []string{ // Five subnets
			"10.0.1.0/24",
			"10.0.2.0/24",
			"10.0.3.0/24",
			"10.0.4.0/24",
			"10.0.5.0/24",
		},
		JumpClusterSetupHostSubnetCidr: "10.0.10.0/24",
		MskJumpClusterAuthType:         "sasl_scram",
		MskClusterId:                   "msk-cluster-123",
		MskSaslScramBootstrapServers:   "b-1.mskcluster.abc123.c1.kafka.us-east-1.amazonaws.com:9096",
		MskRegion:                      "us-east-1",
		TargetEnvironmentId:            "env-abc123",
		TargetClusterId:                "lkc-xyz789",
		TargetRestEndpoint:             "https://pkc-abc123.us-east-1.aws.confluent.cloud:443",
		TargetBootstrapEndpoint:        "pkc-abc123.us-east-1.aws.confluent.cloud:9092",
		ClusterLinkName:                "five-subnet-link",
	}

	project := service.GenerateTerraformModules(request)
	files := projectToFiles(project)
	validateTerraformProject(t, files)
}

