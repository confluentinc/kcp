variable "vpc_id" {
  description = "The ID of the VPC"
  type        = string
}

variable "public_subnet_cidr" {
  description = "CIDR block for the public subnet"
  type        = string
}

variable "aws_region" {
  description = "The AWS region"
  type        = string
}
