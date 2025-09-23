output "bastion_host_public_ip" {
  value = aws_instance.migration_bastion_host.public_ip
}
