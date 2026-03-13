#!/bin/bash
# Generate TLS certificates for Kafka mTLS authentication

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CERTS_DIR="$SCRIPT_DIR/../certs"

# Clean and create certs directory
rm -rf "$CERTS_DIR"
mkdir -p "$CERTS_DIR"
cd "$CERTS_DIR"

echo "Generating certificates in $CERTS_DIR..."

# Certificate passwords
CA_PASSWORD="capassword"
KEYSTORE_PASSWORD="keystorepass"
TRUSTSTORE_PASSWORD="truststorepass"

# 1. Generate CA (Certificate Authority)
echo "1. Generating Certificate Authority (CA)..."
openssl req -new -x509 -keyout ca-key.pem -out ca-cert.pem -days 365 -passout pass:$CA_PASSWORD \
    -subj "/C=US/ST=CA/L=San Francisco/O=Test/OU=TestCA/CN=TestCA"

# 2. Create server keystore and certificate
echo "2. Generating server keystore and certificate..."
keytool -genkey -noprompt \
    -alias kafka-server \
    -dname "CN=kafka-tls,OU=Test,O=Test,L=San Francisco,S=CA,C=US" \
    -keystore kafka.server.keystore.jks \
    -keyalg RSA \
    -storepass $KEYSTORE_PASSWORD \
    -keypass $KEYSTORE_PASSWORD \
    -validity 365

# 3. Create certificate signing request (CSR) for server
echo "3. Creating server certificate signing request..."
keytool -keystore kafka.server.keystore.jks -alias kafka-server -certreq -file server-cert-req.pem \
    -storepass $KEYSTORE_PASSWORD -keypass $KEYSTORE_PASSWORD

# 4. Sign the server certificate with CA
echo "4. Signing server certificate with CA (with SAN for localhost)..."
# Create extensions file for SAN
cat > server-cert-extensions.cnf <<EOF
subjectAltName = DNS:kafka-tls,DNS:localhost,IP:127.0.0.1
EOF

openssl x509 -req -CA ca-cert.pem -CAkey ca-key.pem -in server-cert-req.pem \
    -out server-cert-signed.pem -days 365 -CAcreateserial -passin pass:$CA_PASSWORD \
    -extfile server-cert-extensions.cnf

# 5. Import CA certificate into server keystore
echo "5. Importing CA into server keystore..."
keytool -keystore kafka.server.keystore.jks -alias CARoot -import -file ca-cert.pem \
    -storepass $KEYSTORE_PASSWORD -keypass $KEYSTORE_PASSWORD -noprompt

# 6. Import signed certificate into server keystore
echo "6. Importing signed server certificate into keystore..."
keytool -keystore kafka.server.keystore.jks -alias kafka-server -import -file server-cert-signed.pem \
    -storepass $KEYSTORE_PASSWORD -keypass $KEYSTORE_PASSWORD -noprompt

# 7. Create server truststore and import CA
echo "7. Creating server truststore..."
keytool -keystore kafka.server.truststore.jks -alias CARoot -import -file ca-cert.pem \
    -storepass $TRUSTSTORE_PASSWORD -keypass $TRUSTSTORE_PASSWORD -noprompt

# 8. Generate client certificate
echo "8. Generating client keystore and certificate..."
keytool -genkey -noprompt \
    -alias kafka-client \
    -dname "CN=kafka-client,OU=Test,O=Test,L=San Francisco,S=CA,C=US" \
    -keystore kafka.client.keystore.jks \
    -keyalg RSA \
    -storepass $KEYSTORE_PASSWORD \
    -keypass $KEYSTORE_PASSWORD \
    -validity 365

# 9. Create CSR for client
echo "9. Creating client certificate signing request..."
keytool -keystore kafka.client.keystore.jks -alias kafka-client -certreq -file client-cert-req.pem \
    -storepass $KEYSTORE_PASSWORD -keypass $KEYSTORE_PASSWORD

# 10. Sign client certificate with CA
echo "10. Signing client certificate with CA..."
openssl x509 -req -CA ca-cert.pem -CAkey ca-key.pem -in client-cert-req.pem \
    -out client-cert-signed.pem -days 365 -CAcreateserial -passin pass:$CA_PASSWORD

# 11. Import CA into client keystore
echo "11. Importing CA into client keystore..."
keytool -keystore kafka.client.keystore.jks -alias CARoot -import -file ca-cert.pem \
    -storepass $KEYSTORE_PASSWORD -keypass $KEYSTORE_PASSWORD -noprompt

# 12. Import signed client certificate into client keystore
echo "12. Importing signed client certificate into client keystore..."
keytool -keystore kafka.client.keystore.jks -alias kafka-client -import -file client-cert-signed.pem \
    -storepass $KEYSTORE_PASSWORD -keypass $KEYSTORE_PASSWORD -noprompt

# 13. Create client truststore
echo "13. Creating client truststore..."
keytool -keystore kafka.client.truststore.jks -alias CARoot -import -file ca-cert.pem \
    -storepass $TRUSTSTORE_PASSWORD -keypass $TRUSTSTORE_PASSWORD -noprompt

# 14. Export client certificate and key in PEM format for Go clients
echo "14. Exporting client certificate and key in PEM format..."
# Export from keystore to PKCS12 first
keytool -importkeystore -srckeystore kafka.client.keystore.jks -destkeystore client.p12 \
    -deststoretype PKCS12 -srcalias kafka-client \
    -srcstorepass $KEYSTORE_PASSWORD -deststorepass $KEYSTORE_PASSWORD -noprompt

# Extract certificate
openssl pkcs12 -in client.p12 -nokeys -out client-cert.pem -passin pass:$KEYSTORE_PASSWORD

# Extract private key
openssl pkcs12 -in client.p12 -nodes -nocerts -out client-key.pem -passin pass:$KEYSTORE_PASSWORD

# 15. Create password files for Docker compose
echo "15. Creating password files..."
echo "$KEYSTORE_PASSWORD" > keystore_password.txt
echo "$TRUSTSTORE_PASSWORD" > truststore_password.txt

# Clean up intermediate files
rm -f server-cert-req.pem client-cert-req.pem ca-key.pem ca-cert.srl client.p12 server-cert-extensions.cnf

echo ""
echo "Certificate generation complete!"
echo "Generated files in $CERTS_DIR:"
ls -lh "$CERTS_DIR"
echo ""
echo "Passwords:"
echo "  Keystore password: $KEYSTORE_PASSWORD"
echo "  Truststore password: $TRUSTSTORE_PASSWORD"
