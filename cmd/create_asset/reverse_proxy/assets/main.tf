locals {
  # Extract the hostname from the bootstrap endpoint
  cluster_hostname = regex("(.*):", var.confluent_cloud_cluster_bootstrap_endpoint)[0]
}

resource "random_string" "suffix" {
  length  = 4
  special = false
  numeric = false
  upper   = false
}

resource "tls_private_key" "ssh_key" {
  algorithm = "RSA"
  rsa_bits  = 4096
}

resource "local_file" "private_key" {
  content         = tls_private_key.ssh_key.private_key_pem
  filename        = "./.ssh/reverse_proxy_rsa"
  file_permission = "400"
}

resource "local_file" "public_key" {
  content         = tls_private_key.ssh_key.public_key_openssh
  filename        = "./.ssh/reverse_proxy_rsa.pub"
  file_permission = "400"
}

resource "aws_key_pair" "deployer" {
  key_name   = "reverse-proxy-ssh-key-${random_string.suffix.result}"
  public_key = tls_private_key.ssh_key.public_key_openssh
}

data "aws_availability_zones" "available" {
  state = "available"
}

data "aws_internet_gateway" "existing_internet_gateway" {
  filter {
    name   = "attachment.vpc-id"
    values = [var.vpc_id]
  }
}

resource "aws_route_table" "public_rt" {
  vpc_id = var.vpc_id
  route {
    cidr_block = "0.0.0.0/0"
    gateway_id = data.aws_internet_gateway.existing_internet_gateway.id
  }
  tags = {
    Name = "reverse-proxy-public-rt"
  }
}

resource "aws_subnet" "public_subnet" {
  vpc_id                  = var.vpc_id
  cidr_block              = var.public_subnet_cidr
  availability_zone       = data.aws_availability_zones.available.names[0]
  map_public_ip_on_launch = true
  tags = {
    Name = "reverse-proxy-public-subnet"
  }
}

resource "aws_route_table_association" "public_rt_association" {
  subnet_id      = aws_subnet.public_subnet.id
  route_table_id = aws_route_table.public_rt.id
}

resource "aws_security_group" "public" {
  count  = length(var.aws_security_group_ids) == 0 ? 1 : 0
  vpc_id = var.vpc_id

  ingress {
    from_port   = 22
    to_port     = 22
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
    Name = "reverse-proxy-security-group-public"
  }
}

resource "aws_instance" "proxy" {
  ami                         = data.aws_ami.ubuntu_ami.id
  instance_type               = "t2.micro"
  subnet_id                   = aws_subnet.public_subnet.id
  vpc_security_group_ids      = length(var.aws_security_group_ids) == 0 ? [aws_security_group.public[0].id] : var.aws_security_group_ids
  key_name                    = aws_key_pair.deployer.key_name
  associate_public_ip_address = true

  user_data = templatefile("${path.module}/reverse-proxy-user-data.tpl", {})

  provisioner "local-exec" {
    when        = create
    on_failure  = continue
    command     = <<-EOF
      bash ./generate_dns_entries.sh ${self.public_ip} ${local.cluster_hostname}
    EOF
    working_dir = path.module
  }

  tags = {
    Name = "migration-reverse-proxy"
  }
}

data "aws_ami" "ubuntu_ami" {
  most_recent = true
  owners      = ["099720109477"]

  filter {
    name   = "name"
    values = ["ubuntu/images/hvm-ssd/ubuntu-*-amd64-server-*"]
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
