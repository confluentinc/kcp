#!/bin/bash
# Generate TLS material for the HTTPS / mTLS Schema Registry instances:
#   - ca-cert.pem                 CA (kcp --tls-ca-cert; verifies the SR server)
#   - client-cert.pem/-key.pem    client identity kcp presents for --use-mtls
#   - sr.server.keystore.jks      SR server cert (signed by CA) for the HTTPS listener
#   - sr.server.truststore.jks    CA (so the mTLS SR verifies the client cert)
#   - keystore_password.txt / truststore_password.txt
set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CERTS_DIR="$SCRIPT_DIR/certs"

if [ -f "$CERTS_DIR/sr.server.keystore.jks" ]; then
  echo "Schema Registry TLS certificates already exist, skipping generation."
  exit 0
fi

rm -rf "$CERTS_DIR"; mkdir -p "$CERTS_DIR"; cd "$CERTS_DIR"

CA_PASSWORD="capassword"
KEYSTORE_PASSWORD="keystorepass"
TRUSTSTORE_PASSWORD="truststorepass"

for cmd in openssl keytool; do
  command -v "$cmd" >/dev/null 2>&1 || { echo "Error: $cmd is not installed"; exit 1; }
done

echo "Generating Schema Registry TLS certificates..."

# 1. CA
openssl req -new -x509 -keyout ca-key.pem -out ca-cert.pem -days 365 -passout pass:$CA_PASSWORD \
  -subj "/C=US/ST=CA/L=San Francisco/O=Test/OU=TestCA/CN=SchemaRegistryTestCA" 2>/dev/null

# 2. Server keystore (the SR HTTPS listener's identity)
keytool -genkey -noprompt -alias sr-server \
  -dname "CN=schema-registry,OU=Test,O=Test,L=San Francisco,S=CA,C=US" \
  -keystore sr.server.keystore.jks -keyalg RSA \
  -storepass $KEYSTORE_PASSWORD -keypass $KEYSTORE_PASSWORD -validity 365 2>/dev/null

# 3. Server CSR + sign with CA (SAN so kcp can reach it as localhost)
keytool -keystore sr.server.keystore.jks -alias sr-server -certreq -file server-req.pem \
  -storepass $KEYSTORE_PASSWORD -keypass $KEYSTORE_PASSWORD 2>/dev/null
cat > server-ext.cnf <<EOF
subjectAltName = DNS:schema-registry-basic-tls,DNS:schema-registry-mtls,DNS:localhost,IP:127.0.0.1
EOF
openssl x509 -req -CA ca-cert.pem -CAkey ca-key.pem -in server-req.pem \
  -out server-signed.pem -days 365 -CAcreateserial -passin pass:$CA_PASSWORD \
  -extfile server-ext.cnf 2>/dev/null

# 4. Import CA + signed server cert into the server keystore
keytool -keystore sr.server.keystore.jks -alias CARoot -import -file ca-cert.pem \
  -storepass $KEYSTORE_PASSWORD -keypass $KEYSTORE_PASSWORD -noprompt 2>/dev/null
keytool -keystore sr.server.keystore.jks -alias sr-server -import -file server-signed.pem \
  -storepass $KEYSTORE_PASSWORD -keypass $KEYSTORE_PASSWORD -noprompt 2>/dev/null

# 5. Server truststore (CA) — verifies the client cert on the mTLS SR
keytool -keystore sr.server.truststore.jks -alias CARoot -import -file ca-cert.pem \
  -storepass $TRUSTSTORE_PASSWORD -keypass $TRUSTSTORE_PASSWORD -noprompt 2>/dev/null

# 6. Client identity kcp presents for --use-mtls
openssl genrsa -out client-key.pem 2048 2>/dev/null
openssl req -new -key client-key.pem -out client-req.pem \
  -subj "/C=US/ST=CA/L=San Francisco/O=Test/OU=Test/CN=sr-client" 2>/dev/null
openssl x509 -req -CA ca-cert.pem -CAkey ca-key.pem -in client-req.pem \
  -out client-cert.pem -days 365 -CAcreateserial -passin pass:$CA_PASSWORD 2>/dev/null

echo "$KEYSTORE_PASSWORD" > keystore_password.txt
echo "$TRUSTSTORE_PASSWORD" > truststore_password.txt

rm -f ca-key.pem server-req.pem server-signed.pem server-ext.cnf client-req.pem ca-cert.srl

echo "Schema Registry TLS certificates generated in $CERTS_DIR."
