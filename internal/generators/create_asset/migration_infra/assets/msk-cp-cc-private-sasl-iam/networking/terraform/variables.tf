variable "vpc_id" {
  type      = string
  sensitive = false
}

variable "aws_zones" {
  description = "AWS Zones"
  type = list(object({
    zone = string
    cidr = string
  }))
}

variable "ansible_control_node_subnet_cidr" {
  description = "CIDR block for the public subnet"
  type        = string
}

variable "aws_security_group_ids" {
  description = "Comma separated AWS Security Group Ids"
  type        = string
}
