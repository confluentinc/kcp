#!/bin/bash

sudo yum install -y yum-utils
sudo yum-config-manager --add-repo https://rpm.releases.hashicorp.com/AmazonLinux/hashicorp.repo
sudo yum -y install terraform
sudo yum install java-17-amazon-corretto-headless -y


sudo su - ec2-user
cd /home/ec2-user
curl -O https://packages.confluent.io/archive/8.0/confluent-8.0.0.tar.gz
tar xzf confluent-8.0.0.tar.gz

echo "export PATH=/home/ec2-user/confluent-8.0.0/bin:$PATH" >> /home/ec2-user/.bashrc




