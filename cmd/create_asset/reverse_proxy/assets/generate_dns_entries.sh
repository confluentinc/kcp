#!/bin/bash

ip=$1
base_hostname=$2

# Add bootstrap server hostname
sudo sh -c "echo ${ip} ${base_hostname} >> dns_entries.txt"

# Add all the possible broker hostnames (g000, g001, g002, ...g100)
for i in $(seq -w 0 100); do
    hostname="${base_hostname/./-g${i}.}"
    sudo sh -c "echo $ip $hostname >> dns_entries.txt"
done



