data "aws_ami" "amzn_linux_ami" {
  most_recent = true
  owners      = ["137112412989"] # Amazon's official account ID

  filter {
    name   = "name"
    values = ["al2023-ami-2023.*-kernel-6.1-x86_64"]
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

resource "aws_instance" "ansible_control_node_instance" {
  ami                         = data.aws_ami.amzn_linux_ami.id
  instance_type               = "t2.medium"
  subnet_id                   = var.aws_public_subnet_id
  vpc_security_group_ids      = var.security_group_id
  key_name                    = var.aws_key_pair_name
  associate_public_ip_address = true

  user_data = templatefile("${path.module}/ansible-control-node-user-data.tpl", {
    broker_ips  = var.confluent_platform_broker_instances_private_dns,
    private_key = var.private_key
  })

  tags = {
    Name = "ansible_control_node_instance"
  }
}
