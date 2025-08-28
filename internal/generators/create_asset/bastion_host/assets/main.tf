resource "random_string" "suffix" {
  length  = 4
  special = false
  numeric = false
  upper   = false
}

data "http" "ec2_instance_connect" {
  url = "https://ip-ranges.amazonaws.com/ip-ranges.json"
}

locals {
  ec2_instance_connect_ip = [
    for e in jsondecode(data.http.ec2_instance_connect.response_body)["prefixes"] : e.ip_prefix if e.region == "${var.aws_region}" && e.service == "EC2_INSTANCE_CONNECT"
  ]

  sg_list = var.aws_security_group_ids == "" ? [] : split(",", var.aws_security_group_ids)
}

resource "tls_private_key" "ssh_key" {
  algorithm = "RSA"
  rsa_bits  = 4096
}

resource "local_file" "private_key" {
  content         = tls_private_key.ssh_key.private_key_pem
  filename        = "./.ssh/migration_rsa"
  file_permission = "400"
}

resource "local_file" "public_key" {
  content         = tls_private_key.ssh_key.public_key_openssh
  filename        = "./.ssh/migration_rsa.pub"
  file_permission = "400"
}

resource "aws_key_pair" "deployer" {
  key_name   = "migration-ssh-key-${random_string.suffix.result}"
  public_key = tls_private_key.ssh_key.public_key_openssh
}

data "aws_ami" "amzn_linux_ami" {
  most_recent = true
  owners      = ["137112412989"]

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

resource "aws_internet_gateway" "igw" {
  count = var.create_igw ? 1 : 0

  vpc_id = var.vpc_id
  tags = {
    Name = "migration-jumpserver-igw"
  }
}

data "aws_internet_gateway" "existing_internet_gateway" {
  count = var.create_igw ? 0 : 1

  filter {
    name   = "attachment.vpc-id"
    values = [var.vpc_id]
  }
}

resource "aws_route_table" "public_rt" {
  vpc_id = var.vpc_id
  route {
    cidr_block = "0.0.0.0/0"
    gateway_id = var.create_igw ? aws_internet_gateway.igw[0].id : data.aws_internet_gateway.existing_internet_gateway[0].id
  }

  tags = {
    Name = "migration-bastion-host-public-rt"
  }
}

data "aws_availability_zones" "available" {
  state = "available"

  # Filter out wavelength zones that restrict `map_public_ip_on_launch`.
  filter {
    name   = "opt-in-status"
    values = ["opt-in-not-required"]
  }
}

resource "aws_subnet" "public_subnet" {
  vpc_id                  = var.vpc_id
  cidr_block              = var.public_subnet_cidr
  availability_zone       = data.aws_availability_zones.available.names[0]
  map_public_ip_on_launch = true

  tags = {
    Name = "migration-bastion-host-public-subnet"
  }
}

resource "aws_route_table_association" "public_rt_association" {
  subnet_id      = aws_subnet.public_subnet.id
  route_table_id = aws_route_table.public_rt.id
}

resource "aws_security_group" "bastion_host_security_group" {
  count  = length(local.sg_list) == 0 ? 1 : 0
  vpc_id = var.vpc_id

  ingress {
    from_port   = 0
    to_port     = 22
    protocol    = "TCP"
    cidr_blocks = ["${local.ec2_instance_connect_ip[0]}"]
  }

  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }

  tags = {
    Name = "migration-bastion-host-security-group"
  }
}

resource "aws_instance" "migration_bastion_host" {
  ami                         = data.aws_ami.amzn_linux_ami.id
  instance_type               = "t2.medium"
  subnet_id                   = aws_subnet.public_subnet.id
  vpc_security_group_ids      = length(local.sg_list) == 0 ? [aws_security_group.bastion_host_security_group[0].id] : local.sg_list
  key_name                    = aws_key_pair.deployer.key_name
  associate_public_ip_address = true

  user_data = templatefile("${path.module}/bastion-host-user-data.tpl", {})

  user_data_replace_on_change = true

  tags = {
    Name = "migration-bastion-host"
  }
}
