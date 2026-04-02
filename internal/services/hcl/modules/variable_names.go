package modules

// Variable name constants — compile-time safe replacements for GetModuleVariableName().
// Organized by module to maintain clarity about which module each variable belongs to.
const (
	// Cluster Link module variables
	VarConfluentCloudClusterAPIKey    = "confluent_cloud_cluster_api_key"
	VarConfluentCloudClusterAPISecret = "confluent_cloud_cluster_api_secret"
	VarMSKClusterID                   = "msk_cluster_id"
	VarTargetClusterID                = "target_cluster_id"
	VarTargetClusterRestEndpoint      = "target_cluster_rest_endpoint"
	VarClusterLinkName                = "cluster_link_name"
	VarMSKSaslScramBootstrapServers   = "msk_sasl_scram_bootstrap_servers"
	VarMSKSaslScramUsername           = "msk_sasl_scram_username"
	VarMSKSaslScramPassword           = "msk_sasl_scram_password"

	// Jump Cluster Setup Host module variables
	VarJumpClusterSetupHostSubnetID   = "jump_cluster_setup_host_subnet_id"
	VarJumpClusterSecurityGroupIDs    = "jump_cluster_security_group_ids"
	VarJumpClusterSSHKeyPairName      = "jump_cluster_ssh_key_pair_name"
	VarJumpClusterInstancesPrivateDNS = "jump_cluster_instances_private_dns"
	VarPrivateKey                     = "private_key"

	// Jump Cluster module variables
	VarJumpClusterBrokerSubnetIDs             = "jump_cluster_broker_subnet_ids"
	VarJumpClusterInstanceType                = "jump_cluster_instance_type"
	VarJumpClusterBrokerStorage               = "jump_cluster_broker_storage"
	VarConfluentCloudClusterID                = "confluent_cloud_cluster_id"
	VarConfluentCloudClusterBootstrapEndpoint = "confluent_cloud_cluster_bootstrap_endpoint"
	VarConfluentCloudClusterRestEndpoint      = "confluent_cloud_cluster_rest_endpoint"
	VarMSKClusterBootstrapBrokers             = "msk_cluster_bootstrap_brokers"
	VarJumpClusterIAMAuthRoleName             = "jump_cluster_iam_auth_role_name"

	// Networking module variables
	VarVpcID                          = "vpc_id"
	VarJumpClusterBrokerSubnetCidrs   = "jump_cluster_broker_subnet_cidrs"
	VarJumpClusterSetupHostSubnetCidr = "jump_cluster_setup_host_subnet_cidr"
	VarExistingPrivateLinkVpceID      = "existing_private_link_vpce_id"

	// Confluent Cloud module variables
	VarEnvironmentName = "environment_name"
	VarEnvironmentID   = "environment_id"
	VarClusterName     = "cluster_name"
	VarAWSRegion       = "aws_region"

	// Private Link Target Cluster module variables
	VarSubnetCidrRanges                  = "subnet_cidr_ranges"
	VarNetworkID                         = "network_id"
	VarNetworkDNSDomain                  = "network_dns_domain"
	VarNetworkPrivateLinkEndpointService = "network_private_link_endpoint_service"
	VarNetworkZones                      = "network_zones"

	// External Outbound Cluster Link module variables
	VarSubnetID                   = "subnet_id"
	VarSecurityGroupID            = "security_group_id"
	VarMSKClusterBootstrapServers = "msk_cluster_bootstrap_servers"
)
