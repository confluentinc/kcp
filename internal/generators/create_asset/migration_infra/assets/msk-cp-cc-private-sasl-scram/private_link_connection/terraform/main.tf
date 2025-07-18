resource "random_string" "suffix" {
  length  = 4
  special = false
  numeric = false
  upper   = false
}

resource "confluent_private_link_attachment" "aws" {
  display_name = "migration-privatelink-attachment-${random_string.suffix.result}"
  cloud        = "AWS"
  region       = var.aws_region
  environment {
    id = var.confluent_cloud_environment_id
  }
}

resource "aws_vpc_endpoint" "main" {
  depends_on         = [confluent_private_link_attachment.aws]
  vpc_id             = var.vpc_id
  service_name       = confluent_private_link_attachment.aws.aws[0].vpc_endpoint_service_name
  vpc_endpoint_type  = "Interface"
  subnet_ids         = var.confluent_platform_broker_subnet_ids
  security_group_ids = [var.security_group_id]

  tags = {
    Name = "migration-vpc-endpoint"
  }
}

resource "confluent_private_link_attachment_connection" "aws" {
  display_name = "migration-privatelink-connection-${random_string.suffix.result}"
  environment {
    id = var.confluent_cloud_environment_id
  }

  aws {
    vpc_endpoint_id = aws_vpc_endpoint.main.id
  }

  private_link_attachment {
    id = confluent_private_link_attachment.aws.id
  }
}

resource "aws_route53_zone" "private" {
  name = confluent_private_link_attachment.aws.dns_domain

  vpc {
    vpc_id = var.vpc_id
  }

  tags = {
    Name = "migration-private-dns-zone"
  }
}

resource "aws_route53_record" "entries" {
  zone_id = aws_route53_zone.private.zone_id
  name    = "*"
  type    = "CNAME"
  ttl     = 60
  records = [aws_vpc_endpoint.main.dns_entry[0].dns_name]
}

resource "time_sleep" "destroy_delay" {
  depends_on = [
    confluent_private_link_attachment.aws,
    confluent_private_link_attachment_connection.aws,
    aws_vpc_endpoint.main,
    aws_route53_zone.private,
    aws_route53_record.entries
  ]
  destroy_duration = "1m"
}
