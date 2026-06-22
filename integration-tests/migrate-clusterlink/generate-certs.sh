#!/bin/bash
# Generate TLS material for the cluster-link migrate E2E (Phase 2 auth).
# Produces:
#   - source.keystore.jks / source.truststore.jks  (the source broker's SSL/SASL_SSL listeners)
#   - ca.crt                                        (PEM CA: KCP truststore + link truststore)
#   - client.crt / client.key                       (PEM client cert+key: KCP mTLS read + link mTLS keystore)
# Self-contained; idempotent (skips if already generated).
set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CERTS_DIR="$SCRIPT_DIR/certs"

if [ -f "$CERTS_DIR/source.keystore.jks" ]; then
    echo "certs already exist, skipping."
    exit 0
fi

for cmd in openssl keytool; do
    command -v "$cmd" >/dev/null 2>&1 || { echo "Error: $cmd not installed"; exit 1; }
done

rm -rf "$CERTS_DIR"; mkdir -p "$CERTS_DIR"; cd "$CERTS_DIR"

CA_PW="capassword"; KS_PW="keystorepass"; TS_PW="truststorepass"

echo "Generating CA..."
openssl req -new -x509 -keyout ca.key -out ca.crt -days 365 -nodes \
    -subj "/C=US/ST=CA/L=SF/O=Test/OU=TestCA/CN=TestCA" 2>/dev/null

echo "Generating source broker keystore (SAN: source, localhost)..."
keytool -genkey -noprompt -alias source -keyalg RSA -validity 365 \
    -dname "CN=source,OU=Test,O=Test,L=SF,S=CA,C=US" \
    -keystore source.keystore.jks -storepass $KS_PW -keypass $KS_PW 2>/dev/null
keytool -keystore source.keystore.jks -alias source -certreq -file source.csr \
    -storepass $KS_PW -keypass $KS_PW 2>/dev/null
cat > source-ext.cnf <<EOF
subjectAltName = DNS:source,DNS:localhost,IP:127.0.0.1
EOF
openssl x509 -req -CA ca.crt -CAkey ca.key -in source.csr -out source-signed.crt \
    -days 365 -CAcreateserial -extfile source-ext.cnf 2>/dev/null
keytool -keystore source.keystore.jks -alias CARoot -import -file ca.crt \
    -storepass $KS_PW -keypass $KS_PW -noprompt 2>/dev/null
keytool -keystore source.keystore.jks -alias source -import -file source-signed.crt \
    -storepass $KS_PW -keypass $KS_PW -noprompt 2>/dev/null

echo "Generating source broker truststore (trusts CA -> trusts client certs)..."
keytool -keystore source.truststore.jks -alias CARoot -import -file ca.crt \
    -storepass $TS_PW -keypass $TS_PW -noprompt 2>/dev/null

echo "Generating client cert+key (PEM) for KCP mTLS read + link mTLS keystore..."
openssl req -new -newkey rsa:2048 -nodes -keyout client.key -out client.csr \
    -subj "/C=US/ST=CA/L=SF/O=Test/OU=Test/CN=kcp" 2>/dev/null
openssl x509 -req -CA ca.crt -CAkey ca.key -in client.csr -out client.crt \
    -days 365 -CAcreateserial 2>/dev/null

# Credential files: the cp-server image reads JKS passwords from files in the
# mounted secrets dir (KAFKA_SSL_*_CREDENTIALS point at these by filename).
echo "$KS_PW" > keystore_creds
echo "$KS_PW" > key_creds
echo "$TS_PW" > truststore_creds

# Tidy intermediate files; keep what the broker/KCP/link consume.
rm -f source.csr source-signed.crt source-ext.cnf client.csr ca.srl ca.key
echo "Done. Artifacts in $CERTS_DIR: source.{keystore,truststore}.jks {keystore,key,truststore}_creds ca.crt client.crt client.key"
