module "confluent_cloud" {
  source = "./confluent_cloud/terraform"

  providers = {
    confluent = confluent
  }

  confluent_cloud_api_key          = var.confluent_cloud_api_key
  confluent_cloud_api_secret       = var.confluent_cloud_api_secret
  confluent_cloud_provider         = var.confluent_cloud_provider
  confluent_cloud_region           = var.confluent_cloud_region
  confluent_cloud_environment_name = var.confluent_cloud_environment_name
  confluent_cloud_cluster_name     = var.confluent_cloud_cluster_name
  confluent_cloud_cluster_type     = var.confluent_cloud_cluster_type
}

module "private_link_connection" {
  source = "./private_link_connection/terraform"

  providers = {
    aws       = aws
    confluent = confluent
  }

  aws_region                           = var.aws_region
  confluent_cloud_environment_id       = module.confluent_cloud.confluent_cloud_environment_id
  vpc_id                               = var.customer_vpc_id
  confluent_platform_broker_subnet_ids = module.networking.confluent_platform_broker_subnet_ids
  security_group_id                    = module.networking.private_link_security_group_id
}

module "confluent_cloud_access_setup" {
  source = "./confluent_cloud_access_setup/terraform"

  providers = {
    confluent = confluent
  }

  confluent_cloud_environment_id = module.confluent_cloud.confluent_cloud_environment_id
  confluent_cloud_cluster_id         = module.confluent_cloud.confluent_cloud_cluster_id

  depends_on = [
    module.confluent_cloud,
    module.private_link_connection
  ]
}

module "confluent_platform_broker_instances" {
  source = "./confluent_platform_broker_instances/terraform"

  providers = {
    aws = aws
  }

  vpc_id                                     = var.customer_vpc_id
  aws_public_subnet_id                       = module.networking.public_subnet_id
  security_group_id                          = module.networking.confluent_platform_broker_instances_security_group_id
  aws_key_pair_name                          = module.networking.aws_key_pair_name
  aws_region                                 = var.aws_region
  private_key                                = module.networking.private_key
  confluent_platform_broker_subnet_ids       = module.networking.confluent_platform_broker_subnet_ids
  confluent_cloud_cluster_rest_endpoint      = module.confluent_cloud.confluent_cloud_cluster_rest_endpoint
  confluent_cloud_cluster_id                 = module.confluent_cloud.confluent_cloud_cluster_id
  confluent_cloud_cluster_key                = module.confluent_cloud_access_setup.confluent_cloud_cluster_key
  confluent_cloud_cluster_secret             = module.confluent_cloud_access_setup.confluent_cloud_cluster_secret
  confluent_cloud_cluster_bootstrap_endpoint = module.confluent_cloud.confluent_cloud_cluster_bootstrap_endpoint
  msk_cluster_id                             = var.msk_cluster_id
  msk_cluster_bootstrap_brokers              = var.msk_cluster_bootstrap_brokers
  confluent_platform_broker_iam_role_name    = var.confluent_platform_broker_iam_role_name
}

module "ansible_control_node_instance" {
  source = "./ansible_control_node_instance/terraform"

  providers = {
    aws = aws
  }

  vpc_id                                          = var.customer_vpc_id
  aws_public_subnet_id                            = module.networking.public_subnet_id
  security_group_id                               = module.networking.confluent_platform_broker_instances_security_group_id
  aws_key_pair_name                               = module.networking.aws_key_pair_name
  private_key                                     = module.networking.private_key
  confluent_platform_broker_subnet_ids            = module.networking.confluent_platform_broker_subnet_ids
  confluent_cloud_cluster_rest_endpoint           = module.confluent_cloud.confluent_cloud_cluster_rest_endpoint
  confluent_cloud_cluster_id                      = module.confluent_cloud.confluent_cloud_cluster_id
  confluent_cloud_cluster_key                     = module.confluent_cloud_access_setup.confluent_cloud_cluster_key
  confluent_cloud_cluster_secret                  = module.confluent_cloud_access_setup.confluent_cloud_cluster_secret
  confluent_cloud_cluster_bootstrap_endpoint      = module.confluent_cloud.confluent_cloud_cluster_bootstrap_endpoint
  confluent_platform_broker_instances_private_dns = module.confluent_platform_broker_instances.confluent_platform_broker_instances_private_dns

  depends_on = [module.confluent_platform_broker_instances]
}

module "networking" {
  source = "./networking/terraform"

  providers = {
    aws = aws
  }

  vpc_id                           = var.customer_vpc_id
  aws_zones                        = var.aws_zones
  ansible_control_node_subnet_cidr = var.ansible_control_node_subnet_cidr
  aws_security_group_ids           = var.aws_security_group_ids
}
