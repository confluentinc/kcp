#!/bin/bash
# Generate TLS material for the mTLS Connect worker:
#   - ca-cert.pem                      CA (kcp --tls-ca-cert; verifies the server)
#   - client-cert.pem / client-key.pem client identity kcp presents (--tls-client-cert/-key)
#   - connect.server.keystore.jks      server cert (signed by CA) for the HTTPS listener
#   - connect.server.truststore.jks    CA (so the server verifies the client cert; client.auth=required)
#   - keystore_password.txt / truststore_password.txt
set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CERTS_DIR="$SCRIPT_DIR/certs"

if [ -f "$CERTS_DIR/connect.server.keystore.jks" ]; then
  echo "Connect TLS certificates already exist, skipping generation."
  exit 0
fi

rm -rf "$CERTS_DIR"; mkdir -p "$CERTS_DIR"; cd "$CERTS_DIR"

CA_PASSWORD="capassword"
KEYSTORE_PASSWORD="keystorepass"
TRUSTSTORE_PASSWORD="truststorepass"

for cmd in openssl keytool; do
  command -v "$cmd" >/dev/null 2>&1 || { echo "Error: $cmd is not installed"; exit 1; }
done

echo "Generating Connect mTLS certificates..."

# 1. CA
openssl req -new -x509 -keyout ca-key.pem -out ca-cert.pem -days 365 -passout pass:$CA_PASSWORD \
  -subj "/C=US/ST=CA/L=San Francisco/O=Test/OU=TestCA/CN=ConnectTestCA" 2>/dev/null

# 2. Server keystore (the Connect HTTPS listener's identity)
keytool -genkey -noprompt -alias connect-server \
  -dname "CN=connect-worker-mtls,OU=Test,O=Test,L=San Francisco,S=CA,C=US" \
  -keystore connect.server.keystore.jks -keyalg RSA \
  -storepass $KEYSTORE_PASSWORD -keypass $KEYSTORE_PASSWORD -validity 365 2>/dev/null

# 3. Server CSR + sign with CA (SAN so kcp can reach it as localhost)
keytool -keystore connect.server.keystore.jks -alias connect-server -certreq -file server-req.pem \
  -storepass $KEYSTORE_PASSWORD -keypass $KEYSTORE_PASSWORD 2>/dev/null
cat > server-ext.cnf <<EOF
subjectAltName = DNS:connect-worker-mtls,DNS:localhost,IP:127.0.0.1
EOF
openssl x509 -req -CA ca-cert.pem -CAkey ca-key.pem -in server-req.pem \
  -out server-signed.pem -days 365 -CAcreateserial -passin pass:$CA_PASSWORD \
  -extfile server-ext.cnf 2>/dev/null

# 4. Import CA + signed server cert into the server keystore
keytool -keystore connect.server.keystore.jks -alias CARoot -import -file ca-cert.pem \
  -storepass $KEYSTORE_PASSWORD -keypass $KEYSTORE_PASSWORD -noprompt 2>/dev/null
keytool -keystore connect.server.keystore.jks -alias connect-server -import -file server-signed.pem \
  -storepass $KEYSTORE_PASSWORD -keypass $KEYSTORE_PASSWORD -noprompt 2>/dev/null

# 5. Server truststore (CA) — used to verify the client cert (client.auth=required)
keytool -keystore connect.server.truststore.jks -alias CARoot -import -file ca-cert.pem \
  -storepass $TRUSTSTORE_PASSWORD -keypass $TRUSTSTORE_PASSWORD -noprompt 2>/dev/null

# 6. Client identity kcp presents — key + cert (PEM), signed by the CA
openssl genrsa -out client-key.pem 2048 2>/dev/null
openssl req -new -key client-key.pem -out client-req.pem \
  -subj "/C=US/ST=CA/L=San Francisco/O=Test/OU=Test/CN=connect-client" 2>/dev/null
openssl x509 -req -CA ca-cert.pem -CAkey ca-key.pem -in client-req.pem \
  -out client-cert.pem -days 365 -CAcreateserial -passin pass:$CA_PASSWORD 2>/dev/null

# 7. Password files for the compose env
echo "$KEYSTORE_PASSWORD" > keystore_password.txt
echo "$TRUSTSTORE_PASSWORD" > truststore_password.txt

# Clean up intermediates (keep ca-cert.pem — kcp needs it)
rm -f ca-key.pem server-req.pem server-signed.pem server-ext.cnf client-req.pem ca-cert.srl

echo "Connect mTLS certificates generated in $CERTS_DIR."
