data "external" "my_public_ip" {
  program = ["bash", "-c", "curl -s 'https://api.ipify.org?format=json'"]
}

data "aws_internet_gateway" "existing_internet_gateway" {
  filter {
    name   = "attachment.vpc-id"
    values = [var.vpc_id]
  }
}

data "aws_availability_zones" "available" {
  state = "available"
}

locals {
  sg_list = var.aws_security_group_ids == "" ? [] : split(",", var.aws_security_group_ids)
}

resource "random_string" "suffix" {
  length  = 4
  special = false
  numeric = false
  upper   = false
}

resource "aws_security_group" "confluent_platform_broker_instances_security_group" {
  count  = length(local.sg_list) == 0 ? 1 : 0
  vpc_id = var.vpc_id

  ingress {
    from_port   = 22
    to_port     = 22
    protocol    = "tcp"
    cidr_blocks = ["0.0.0.0/0"]
  }

  ingress {
    from_port   = 9091
    to_port     = 9091
    protocol    = "tcp"
    cidr_blocks = ["0.0.0.0/0"]
  }

  ingress {
    from_port   = 9092
    to_port     = 9092
    protocol    = "tcp"
    cidr_blocks = ["0.0.0.0/0"]
  }

  ingress {
    from_port   = 9093
    to_port     = 9093
    protocol    = "tcp"
    cidr_blocks = ["0.0.0.0/0"]
  }

  ingress {
    from_port   = 8090
    to_port     = 8090
    protocol    = "tcp"
    cidr_blocks = ["0.0.0.0/0"]
  }

  ingress {
    from_port   = 8081
    to_port     = 8081
    protocol    = "tcp"
    cidr_blocks = ["0.0.0.0/0"]
  }

  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }

  tags = {
    Name = "migration-confluent-platform-broker-security-group-public"
  }
}

resource "aws_subnet" "confluent_platform_broker_subnet_ids" {
  for_each          = { for z in var.aws_zones : z.zone => { zone = z.zone, cidr = z.cidr } }
  vpc_id            = var.vpc_id
  availability_zone = each.value.zone
  cidr_block        = each.value.cidr

  tags = {
    Name = "migration-confluent-platform-broker-subnet-${each.key}"
  }
}

resource "aws_subnet" "ansible_control_node_instance_public_subnet" {
  vpc_id                  = var.vpc_id
  cidr_block              = var.ansible_control_node_subnet_cidr
  availability_zone       = data.aws_availability_zones.available.names[0]
  map_public_ip_on_launch = true

  tags = {
    Name = "migration-ansible-instance-subnet"
  }
}

resource "aws_eip" "nat_eip" {
  domain = "vpc"
  tags = {
    Name = "migration-nat-eip"
  }
}

resource "aws_nat_gateway" "nat_gw" {
  allocation_id = aws_eip.nat_eip.id
  subnet_id     = aws_subnet.ansible_control_node_instance_public_subnet.id
  tags = {
    Name = "migration-nat-gw"
  }
}

resource "aws_route_table" "private_subnet_rt" {
  vpc_id = var.vpc_id
  route {
    cidr_block = "0.0.0.0/0"
    gateway_id = aws_nat_gateway.nat_gw.id
  }

  tags = {
    Name = "migration-private-subnet-rt"
  }
}

resource "aws_route_table_association" "private_rt_association" {
  for_each       = aws_subnet.confluent_platform_broker_subnet_ids
  subnet_id      = each.value.id
  route_table_id = aws_route_table.private_subnet_rt.id
}

resource "aws_security_group" "private_link_security_group" {
  count  = length(local.sg_list) == 0 ? 1 : 0
  vpc_id = var.vpc_id

  ingress {
    from_port   = 80
    to_port     = 80
    protocol    = "tcp"
    cidr_blocks = ["0.0.0.0/0"]
  }

  ingress {
    from_port   = 443
    to_port     = 443
    protocol    = "tcp"
    cidr_blocks = ["0.0.0.0/0"]
  }

  ingress {
    from_port   = 9092
    to_port     = 9092
    protocol    = "tcp"
    cidr_blocks = ["0.0.0.0/0"]
  }

  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }

  tags = {
    Name = "migration-private-link-security-group"
  }
}

resource "tls_private_key" "ansible_confluent_platform_broker_ssh_key" {
  algorithm = "RSA"
  rsa_bits  = 4096
}

resource "local_file" "ansible_confluent_platform_broker_private_key" {
  content         = tls_private_key.ansible_confluent_platform_broker_ssh_key.private_key_pem
  filename        = "./.ssh/confluent_platform_brokers_private_key_rsa"
  file_permission = "400"
}

resource "local_file" "ansible_confluent_platform_broker_public_key" {
  content         = tls_private_key.ansible_confluent_platform_broker_ssh_key.public_key_openssh
  filename        = "./.ssh/confluent_platform_brokers_public_key_rsa.pub"
  file_permission = "400"
}

resource "aws_key_pair" "ansible_confluent_platform_broker_ssh_key" {
  key_name   = "migration-ansible-confluent-platform-broker-ssh-key-${random_string.suffix.result}"
  public_key = tls_private_key.ansible_confluent_platform_broker_ssh_key.public_key_openssh
}

resource "aws_route_table" "ansible_control_node_instance_public_rt" {
  vpc_id = var.vpc_id

  route {
    cidr_block = "0.0.0.0/0"
    gateway_id = data.aws_internet_gateway.existing_internet_gateway.id
  }
}

resource "aws_route_table_association" "ansible_control_node_instance_public_rt_association" {
  subnet_id      = aws_subnet.ansible_control_node_instance_public_subnet.id
  route_table_id = aws_route_table.ansible_control_node_instance_public_rt.id
}
