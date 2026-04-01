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
	assertMatchesGoldenFiles(t, "TestMigrationInfra_Public", files)
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
	assertMatchesGoldenFiles(t, "TestMigrationInfra_PrivateJumpCluster", files)
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
	assertMatchesGoldenFiles(t, "TestMigrationInfra_ExternalOutbound", files)
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
	assertMatchesGoldenFiles(t, "TestMigrationInfra_ExternalOutboundUnauthTls", files)
}
