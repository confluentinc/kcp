#!/bin/bash
sudo su - ec2-user
cd /home/ec2-user

cat > /home/ec2-user/create-external-outbound-cluster-link.sh << 'EOF'
#!/bin/bash

BASIC_AUTH_CREDENTIALS=$(echo -n "${target_cluster_api_key}:${target_cluster_api_secret}" | base64 -w 0)

curl --request POST \
  --url '${target_cluster_rest_endpoint}/kafka/v3/clusters/${target_cluster_id}/links/?link_name=${cluster_link_name}' \
  --header "Authorization: Basic $BASIC_AUTH_CREDENTIALS" \
  --header "Content-Type: application/json" \
  --data '{
    "source_cluster_id": "${msk_cluster_id}",
    "configs": [
      {
        "name": "bootstrap.servers",
        "value": "${msk_cluster_bootstrap_brokers}"
      },
      {
        "name": "link.mode",
        "value": "DESTINATION"
      },
      {
        "name": "security.protocol",
        "value": "SASL_SSL"
      },
      {
        "name": "sasl.mechanism",
        "value": "SCRAM-SHA-512"
      },
      {
        "name": "sasl.jaas.config",
        "value": "org.apache.kafka.common.security.scram.ScramLoginModule required username=\"${msk_sasl_scram_username}\" password=\"${msk_sasl_scram_password}\";"
      }
    ]
  }'

EOF

chmod +x /home/ec2-user/create-external-outbound-cluster-link.sh
chown ec2-user:ec2-user /home/ec2-user/create-external-outbound-cluster-link.sh

./create-external-outbound-cluster-link.sh
