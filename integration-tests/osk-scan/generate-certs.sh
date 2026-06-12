#!/bin/bash
# Generate TLS certificates for the OSK scan test.
# Self-contained — no dependencies on other test infrastructure.

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CERTS_DIR="$SCRIPT_DIR/certs"

# Skip if certs already exist
if [ -f "$CERTS_DIR/kafka.server.keystore.jks" ]; then
    echo "TLS certificates already exist, skipping generation."
    exit 0
fi

rm -rf "$CERTS_DIR"
mkdir -p "$CERTS_DIR"
cd "$CERTS_DIR"

CA_PASSWORD="capassword"
KEYSTORE_PASSWORD="keystorepass"
TRUSTSTORE_PASSWORD="truststorepass"

# Check required tools
for cmd in openssl keytool; do
    if ! command -v "$cmd" &> /dev/null; then
        echo "Error: $cmd is not installed"
        exit 1
    fi
done

echo "Generating TLS certificates..."

# 1. Generate CA
openssl req -new -x509 -keyout ca-key.pem -out ca-cert.pem -days 365 -passout pass:$CA_PASSWORD \
    -subj "/C=US/ST=CA/L=San Francisco/O=Test/OU=TestCA/CN=TestCA" 2>/dev/null

# 2. Create server keystore
keytool -genkey -noprompt \
    -alias kafka-server \
    -dname "CN=osk-kafka,OU=Test,O=Test,L=San Francisco,S=CA,C=US" \
    -keystore kafka.server.keystore.jks \
    -keyalg RSA \
    -storepass $KEYSTORE_PASSWORD \
    -keypass $KEYSTORE_PASSWORD \
    -validity 365 2>/dev/null

# 3. Create server CSR
keytool -keystore kafka.server.keystore.jks -alias kafka-server -certreq -file server-cert-req.pem \
    -storepass $KEYSTORE_PASSWORD -keypass $KEYSTORE_PASSWORD 2>/dev/null

# 4. Sign server cert with CA (with SAN for localhost and container hostname)
cat > server-cert-extensions.cnf <<EOF
subjectAltName = DNS:osk-kafka,DNS:localhost,IP:127.0.0.1
EOF

openssl x509 -req -CA ca-cert.pem -CAkey ca-key.pem -in server-cert-req.pem \
    -out server-cert-signed.pem -days 365 -CAcreateserial -passin pass:$CA_PASSWORD \
    -extfile server-cert-extensions.cnf 2>/dev/null

# 5. Import CA into server keystore
keytool -keystore kafka.server.keystore.jks -alias CARoot -import -file ca-cert.pem \
    -storepass $KEYSTORE_PASSWORD -keypass $KEYSTORE_PASSWORD -noprompt 2>/dev/null

# 6. Import signed cert into server keystore
keytool -keystore kafka.server.keystore.jks -alias kafka-server -import -file server-cert-signed.pem \
    -storepass $KEYSTORE_PASSWORD -keypass $KEYSTORE_PASSWORD -noprompt 2>/dev/null

# 7. Create server truststore
keytool -keystore kafka.server.truststore.jks -alias CARoot -import -file ca-cert.pem \
    -storepass $TRUSTSTORE_PASSWORD -keypass $TRUSTSTORE_PASSWORD -noprompt 2>/dev/null

# 8. Generate client keystore
keytool -genkey -noprompt \
    -alias kafka-client \
    -dname "CN=kafka-client,OU=Test,O=Test,L=San Francisco,S=CA,C=US" \
    -keystore kafka.client.keystore.jks \
    -keyalg RSA \
    -storepass $KEYSTORE_PASSWORD \
    -keypass $KEYSTORE_PASSWORD \
    -validity 365 2>/dev/null

# 9. Create client CSR
keytool -keystore kafka.client.keystore.jks -alias kafka-client -certreq -file client-cert-req.pem \
    -storepass $KEYSTORE_PASSWORD -keypass $KEYSTORE_PASSWORD 2>/dev/null

# 10. Sign client cert with CA
openssl x509 -req -CA ca-cert.pem -CAkey ca-key.pem -in client-cert-req.pem \
    -out client-cert-signed.pem -days 365 -CAcreateserial -passin pass:$CA_PASSWORD 2>/dev/null

# 11. Import CA into client keystore
keytool -keystore kafka.client.keystore.jks -alias CARoot -import -file ca-cert.pem \
    -storepass $KEYSTORE_PASSWORD -keypass $KEYSTORE_PASSWORD -noprompt 2>/dev/null

# 12. Import signed client cert
keytool -keystore kafka.client.keystore.jks -alias kafka-client -import -file client-cert-signed.pem \
    -storepass $KEYSTORE_PASSWORD -keypass $KEYSTORE_PASSWORD -noprompt 2>/dev/null

# 13. Create client truststore
keytool -keystore kafka.client.truststore.jks -alias CARoot -import -file ca-cert.pem \
    -storepass $TRUSTSTORE_PASSWORD -keypass $TRUSTSTORE_PASSWORD -noprompt 2>/dev/null

# 14. Export client cert and key as PEM (for Go TLS client)
keytool -importkeystore -srckeystore kafka.client.keystore.jks -destkeystore client.p12 \
    -deststoretype PKCS12 -srcalias kafka-client \
    -srcstorepass $KEYSTORE_PASSWORD -deststorepass $KEYSTORE_PASSWORD -noprompt 2>/dev/null
openssl pkcs12 -in client.p12 -nokeys -out client-cert.pem -passin pass:$KEYSTORE_PASSWORD 2>/dev/null
openssl pkcs12 -in client.p12 -nodes -nocerts -out client-key.pem -passin pass:$KEYSTORE_PASSWORD 2>/dev/null

# 15. Create password files for Docker
echo "$KEYSTORE_PASSWORD" > keystore_password.txt
echo "$TRUSTSTORE_PASSWORD" > truststore_password.txt

# 16. Generate self-signed cert for Prometheus TLS
openssl req -new -x509 -nodes -keyout prometheus-server.key -out prometheus-server.crt \
    -days 365 -subj "/CN=localhost" \
    -addext "subjectAltName=DNS:localhost,DNS:osk-prometheus-tls,IP:127.0.0.1" 2>/dev/null

# Clean up intermediate files
rm -f server-cert-req.pem client-cert-req.pem ca-key.pem ca-cert.srl client.p12 server-cert-extensions.cnf client-cert.pem

echo "TLS certificates generated."
