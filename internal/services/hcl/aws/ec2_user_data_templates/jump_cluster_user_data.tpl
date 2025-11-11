#!/bin/bash

sudo su - ec2-user
cd /home/ec2-user

sudo dnf install python3.11 -y

curl https://bootstrap.pypa.io/get-pip.py -o /home/ec2-user/get-pip.py

sudo python3.11 /home/ec2-user/get-pip.py

sudo python3.11 -m pip install packaging

sudo python3.11 -m pip install PyYAML
