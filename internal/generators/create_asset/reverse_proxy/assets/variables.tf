variable "vpc_id" {
  description = "The ID of the VPC"
  type        = string
}

variable "public_subnet_cidr" {
  description = "CIDR block for the public subnet"
  type        = string
  default     = "10.0.30.0/24"
}

variable "confluent_cloud_cluster_bootstrap_endpoint" {
  description = "The bootstrap endpoint of the Confluent cluster"
  type        = string
}

variable "aws_region" {
  description = "AWS Region"
  type        = string
  default     = "us-east-1"
}
