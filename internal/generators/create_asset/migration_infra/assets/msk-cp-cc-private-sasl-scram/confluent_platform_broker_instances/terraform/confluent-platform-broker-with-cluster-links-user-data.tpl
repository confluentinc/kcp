#!/bin/bash
sudo su - ec2-user
cd /home/ec2-user
sudo dnf install python3.11 -y
curl https://bootstrap.pypa.io/get-pip.py -o /home/ec2-user/get-pip.py
sudo python3.11 /home/ec2-user/get-pip.py
sudo python3.11 -m pip install packaging
sudo python3.11 -m pip install PyYAML
sudo yum install nc -y

cat > /home/ec2-user/create-cluster-links.sh << 'EOF'
#!/bin/bash
cd /home/ec2-user/

#
# Create MSK -> CP cluster link
#
echo "bootstrap.servers=${msk_cluster_bootstrap_brokers}
security.protocol=SASL_SSL
sasl.mechanism=SCRAM-SHA-512
sasl.jaas.config=org.apache.kafka.common.security.scram.ScramLoginModule required \
  username=\"${msk_sasl_scram_username}\" \
  password=\"${msk_sasl_scram_password}\";" > /home/ec2-user/client.properties

echo "bootstrap.servers=`hostname`:9092
security.protocol=PLAINTEXT" > /home/ec2-user/destination-cluster.properties

kafka-cluster-links --bootstrap-server `hostname`:9092 --cluster-id ${msk_cluster_id} --command-config destination-cluster.properties --create --link cp-initiated-msk-link --config-file client.properties

#
# Create CP -> CC destination cluster link
#

# Get CP cluster id (source cluster id)
CONFLUENTPLATFORM_CLUSTER_ID=`kafka-cluster cluster-id --bootstrap-server \`hostname\`:9092 | cut -d":" -f2 | xargs`
BASIC_AUTH_CREDENTIALS=$(echo -n "${confluent_cloud_cluster_key}:${confluent_cloud_cluster_secret}" | base64 -w 0)

curl --request POST \
    --url "${confluent_cloud_cluster_rest_endpoint}/kafka/v3/clusters/${confluent_cloud_cluster_id}/links/?link_name=cp-to-cc-link" \
  --header "Authorization: Basic $BASIC_AUTH_CREDENTIALS" \
  --header "Content-Type: application/json" \
  --data "{\"source_cluster_id\": \"$CONFLUENTPLATFORM_CLUSTER_ID\", \"configs\": [{\"name\": \"link.mode\", \"value\": \"DESTINATION\"}, {\"name\": \"connection.mode\", \"value\": \"INBOUND\"}]}"

#
# Create CC -> CP source cluster link
#
echo "link.mode=SOURCE
connection.mode=OUTBOUND
bootstrap.servers=${confluent_cloud_cluster_bootstrap_endpoint}
ssl.endpoint.identification.algorithm=https
security.protocol=SASL_SSL
sasl.mechanism=PLAIN
sasl.jaas.config=org.apache.kafka.common.security.plain.PlainLoginModule required username='${confluent_cloud_cluster_key}' password='${confluent_cloud_cluster_secret}';
local.listener.name=BROKER
local.security.protocol=PLAINTEXT" > /home/ec2-user/cp-cc-link.properties

kafka-cluster-links --bootstrap-server `hostname`:9092 --create --link cp-to-cc-link --config-file cp-cc-link.properties --cluster-id ${confluent_cloud_cluster_id} --command-config destination-cluster.properties

EOF

chmod +x /home/ec2-user/create-cluster-links.sh
chown ec2-user:ec2-user /home/ec2-user/create-cluster-links.sh
