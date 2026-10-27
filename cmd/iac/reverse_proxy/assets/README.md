# Reverse Proxy

This Terraform configuration provisions a reverse proxy server that enables secure access to Confluent Cloud clusters through SSL/TLS termination and traffic routing.

## Overview

The reverse proxy is an Nginx-based proxy server that acts as an intermediary between your local applications and the Confluent Cloud cluster. It handles SSL/TLS termination and routes traffic based on Server Name Indication (SNI) to the appropriate backend services. This allows you to connect to Confluent Cloud clusters that are only accessible through private networking.

## Creating the Reverse Proxy

### Step 1: Deploy the Reverse Proxy

Navigate to the generated directory and deploy the reverse proxy:

```bash
cd reverse-proxy

# Initialize Terraform
terraform init

# Review the deployment plan
terraform plan

# Apply the configuration
terraform apply
```

When prompted, type `yes` to confirm the deployment.

### Step 2: Configure Local DNS

The reverse proxy generates a `dns_entries.txt` file containing DNS entries that you must manually add to your local machine's `/etc/hosts` file:

```bash
# Check the generated DNS entries
cat dns_entries.txt
```

> ⚠️ **IMPORTANT**: You must manually add these entries to your `/etc/hosts` file to enable local applications to connect through the reverse proxy:

```bash
# Add the entries to /etc/hosts (requires sudo)
sudo cp /etc/hosts /etc/hosts.backup
sudo cat dns_entries.txt >> /etc/hosts
```

The entries should include mappings for:

- Confluent Cloud cluster bootstrap endpoint
- Kafka REST API endpoint
- Individual broker endpoints

## How It Works

The reverse proxy uses Nginx with the stream module to handle SSL/TLS traffic:

### Traffic Flow

1. **Client connects** to the reverse proxy on ports 443 (REST API) or 9092 (Kafka)
2. **SSL/TLS termination** occurs at the proxy
3. **SNI inspection** determines the target backend based on the server name
4. **Traffic routing** forwards the connection to the appropriate Confluent Cloud endpoint
5. **Response routing** returns the response through the same path

### Configuration Details

- **Port 443**: Handles REST API traffic to Confluent Cloud
- **Port 9092**: Handles Kafka protocol traffic
- **SSL Preread**: Uses SNI to determine routing without terminating SSL
- **DNS Resolution**: Uses AWS internal DNS (169.254.169.253)
- **Logging**: Detailed access logs with connection information

## Security Considerations

- The reverse proxy is deployed with SSH access open to the internet (0.0.0.0/0)
- Consider restricting SSH access to specific IP ranges for production environments
- The SSH private key is stored locally - keep it secure
- SSL/TLS traffic is proxied but not terminated, maintaining end-to-end encryption
- Regularly update the reverse proxy and rotate credentials

## Cleanup

If you wish to remove the reverse proxy infrastructure:

```bash
terraform destroy
```

> ⚠️ **IMPORTANT**: This will permanently delete the reverse proxy and all associated resources. To complete the cleanup process, remove the previously added entries from `/etc/hosts`.

## Usage Examples

### Connecting to Kafka

Once the reverse proxy is configured, you can connect to your Confluent Cloud cluster using standard Kafka clients:

```bash
# Using kafka-console-producer
kafka-console-producer.sh --bootstrap-server <CLUSTER_HOSTNAME>:9092 \
  --topic test-topic \
  --producer.config client.properties

# Using kafka-console-consumer
kafka-console-consumer.sh --bootstrap-server <CLUSTER_HOSTNAME>:9092 \
  --topic test-topic \
  --consumer.config client.properties
```

### Connecting to REST API

For REST API access:

```bash
# Using curl
curl -X GET "https://<CLUSTER_HOSTNAME>:443/kafka/v3/clusters" \
  -H "Authorization: Bearer <API_KEY>"
```
