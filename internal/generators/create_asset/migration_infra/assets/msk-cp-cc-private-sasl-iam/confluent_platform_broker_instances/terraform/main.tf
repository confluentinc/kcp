data "aws_ami" "red_hat_linux_ami" {
  most_recent = true
  owners      = ["309956199498"]

  filter {
    name   = "name"
    values = ["RHEL-9.6.0_HVM_GA-*"]
  }

  filter {
    name   = "state"
    values = ["available"]
  }

  filter {
    name   = "architecture"
    values = ["x86_64"]
  }

  filter {
    name   = "virtualization-type"
    values = ["hvm"]
  }
}

resource "aws_instance" "confluent-platform-broker" {
  for_each                    = { for idx, subnet_id in var.confluent_platform_broker_subnet_ids : "broker-${idx}" => subnet_id }
  ami                         = data.aws_ami.red_hat_linux_ami.id
  instance_type               = "m5.xlarge"
  subnet_id                   = each.value
  vpc_security_group_ids      = [var.security_group_id]
  key_name                    = var.aws_key_pair_name
  associate_public_ip_address = false
  iam_instance_profile        = var.confluent_platform_broker_iam_role_name

  root_block_device {
    volume_size = 100
    volume_type = "gp3"
  }

  metadata_options {
    http_tokens                 = "required"
    http_put_response_hop_limit = 10
  }

  user_data = each.key == "broker-0" ? templatefile("${path.module}/confluent-platform-broker-with-cluster-links-user-data.tpl", {
    confluent_cloud_cluster_rest_endpoint      = var.confluent_cloud_cluster_rest_endpoint,
    confluent_cloud_cluster_id                 = var.confluent_cloud_cluster_id,
    confluent_cloud_cluster_key                = var.confluent_cloud_cluster_key,
    confluent_cloud_cluster_secret             = var.confluent_cloud_cluster_secret,
    confluent_cloud_cluster_bootstrap_endpoint = var.confluent_cloud_cluster_bootstrap_endpoint,
    msk_cluster_id                             = var.msk_cluster_id,
    msk_cluster_bootstrap_brokers              = var.msk_cluster_bootstrap_brokers,
  }) : templatefile("${path.module}/confluent-platform-broker-user-data.tpl", {})

  tags = {
    Name = "confluent-platform-${each.key}"
  }
}








