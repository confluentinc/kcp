# Switchover: mTLS → SASL/PLAIN

This example configures the gateway to accept client connections using mTLS and authenticate to Confluent Cloud using SASL/PLAIN. The gateway swaps the client's mTLS certificate identity for SASL/PLAIN credentials during the switchover state.

## mTLS Leg Behaviour

True TLS passthrough is not supported — TLS terminates on each leg independently (client→gateway, then gateway→cluster). In the init and fenced states, the gateway maintains the client's identity by using the same ACM PCA-issued certificate on both legs: the client presents it for mTLS authentication to the gateway, and the gateway presents the same certificate when connecting to MSK. MSK sees the same CN on both hops, making the gateway transparent to its ACL system.

In the switchover state, the client certificate CN is looked up in the file store to retrieve SASL/PLAIN credentials for Confluent Cloud.

## Environment Variables

| Variable | Description |
|----------|-------------|
| `KUBECTL_NAMESPACE` | Kubernetes namespace for gateway resources |
| `SASL_PLAIN_USERNAME` | Confluent Cloud API key |
| `SASL_PLAIN_PASSWORD` | Confluent Cloud API secret |
| `JKS_PASSWORD` | Password for Confluent Cloud truststore JKS |
| `SSL_TRUSTSTORE_PASSWORD` | Password for client truststore |
| `SSL_KEYSTORE_PASSWORD` | Password for client keystore |
| `SSL_KEY_PASSWORD` | Password for client key |
| `JAVA_HOME` | Path to JDK home directory |

## Certificate Requirements

This example requires three certificate components:

1. **Gateway server certificate**: A self-signed certificate presented by the gateway to clients during the TLS handshake
2. **Client mTLS certificate**: An ACM Private CA-issued certificate used by clients to authenticate to the gateway
3. **Client verification CA**: The ACM Private CA certificate used by the gateway to verify client certificates

For detailed certificate generation instructions, see the [Gateway certificates guide](https://github.com/confluentinc/confluent-kubernetes-examples/blob/master/gateway/certificates/README.md).

## Secrets Setup

| Secret | Purpose |
|--------|---------|
| `gateway-tls` | Gateway server certificate presented to clients, plus the ACM PCA CA used to verify client mTLS certificates (all states) |
| `msk-client-tls` | Client certificate the gateway presents to MSK for mTLS authentication, plus the Amazon trust bundle to verify MSK's server certificate (init and fenced states) |
| `tls` | JKS truststore for verifying Confluent Cloud's server certificate (switchover state) |
| `file-store-ccloud-credentials` | Maps client certificate CN to CCloud SASL/PLAIN credentials (switchover state) |
| `file-store-config` | File store separator configuration |
| `plain-jaas` | JAAS config template for gateway-to-CCloud SASL/PLAIN authentication (switchover state) |

Create the following Kubernetes secrets before deploying the gateway:

**Gateway server TLS** (gateway server certificate and client verification CA):
```bash
kubectl create secret generic gateway-tls \
  --from-file=fullchain.pem=certs/fullchain.pem \
  --from-file=privkey.pem=certs/privkey.pem \
  --from-file=cacerts.pem=certs/ca.pem \
  -n ${KUBECTL_NAMESPACE}
```

**Note:** `cacerts.pem` here is `certs/ca.pem` — the ACM PCA CA used to verify client mTLS certificates. Do not use `certs/cacerts.pem`, which is the local gateway CA used only to build the client truststore.

**MSK client TLS** (client certificate used by gateway to authenticate to MSK):
```bash
kubectl create secret generic msk-client-tls \
  --from-file=fullchain.pem=certs/client.pem \
  --from-file=privkey.pem=certs/client.key \
  --from-file=cacerts.pem=certs/amazon-trust-bundle.pem \
  -n ${KUBECTL_NAMESPACE}
```

**Confluent Cloud TLS truststore** (for gateway to CCloud TLS verification):
```bash
cp ${JAVA_HOME}/lib/security/cacerts ccloud-truststore.jks
echo "jksPassword=${JKS_PASSWORD}" > ccloud-jksPassword.txt

kubectl create secret generic tls \
  --from-file=truststore.jks=ccloud-truststore.jks \
  --from-file=jksPassword.txt=ccloud-jksPassword.txt \
  -n ${KUBECTL_NAMESPACE}
```

**File store credential mapping** (maps client certificate CN to CCloud credentials):
```bash
kubectl create secret generic file-store-ccloud-credentials \
  --from-literal=gateway-client="${SASL_PLAIN_USERNAME}/${SASL_PLAIN_PASSWORD}" \
  -n ${KUBECTL_NAMESPACE}
```

**File store config** (separator for username/password parsing):
```bash
kubectl create secret generic file-store-config \
  --from-literal=separator="/" \
  -n ${KUBECTL_NAMESPACE}
```

**JAAS config template** (for gateway to CCloud SASL/PLAIN authentication):
```bash
kubectl create secret generic plain-jaas \
  --from-literal=plain-jaas.conf='org.apache.kafka.common.security.plain.PlainLoginModule required username="%s" password="%s";' \
  -n ${KUBECTL_NAMESPACE}
```

## Gateway YAMLs

### gateway_init.yaml

Initial state: clients connect with mTLS, gateway routes to MSK using `auth: passthrough` (same mTLS certificate on both legs).

```yaml
apiVersion: platform.confluent.io/v1beta1
kind: Gateway
metadata:
  labels:
    app.kubernetes.io/name: confluent-for-kubernetes
    app.kubernetes.io/managed-by: kustomize
  name: migration-gateway
spec:
  replicas: 3
  image:
    application: <gateway-image>
    init: confluentinc/confluent-init-container:3.1.0
  podTemplate:
    envVars:
      - name: GATEWAY_ROOT_LOG_LEVEL
        value: DEBUG  # Can be changed to INFO for production
  streamingDomains:
    - name: source-kafka-cluster
      type: kafka
      kafkaCluster:
        bootstrapServers:
          - id: MTLS
            # Replace with your MSK TLS bootstrap server (port 9094)
            endpoint: SSL://<msk-bootstrap-server>:9094
            tls:
              # PEM secret containing: fullchain.pem (client cert), privkey.pem (client key),
              # cacerts.pem (Amazon Root CA to verify MSK's server cert)
              secretRef: msk-client-tls
        nodeIdRanges:
          - name: pool-1
            start: 1
            end: 3
  routes:
    - name: migration-route
      endpoint: <gateway-lb-hostname>:9595
      brokerIdentificationStrategy:
        type: port
      streamingDomain:
        name: source-kafka-cluster
        bootstrapServerId: MTLS
      security:
        auth: passthrough  # Gateway uses same mTLS cert as client when connecting to MSK
        client:
          tls:
            secretRef: gateway-tls
          authentication:
            type: mtls
            mtls:
              principalMappingRules: ["RULE:^CN=([^,]+).*$/$1/"]
              sslClientAuthentication: "required"
  externalAccess:
    type: loadBalancer
    loadBalancer:
      domain: <gateway-lb-hostname>
```

### gateway_fenced.yaml

Fenced state: clients receive `BROKER_NOT_AVAILABLE` errors. Same as init state with added `fence` block.

```yaml
apiVersion: platform.confluent.io/v1beta1
kind: Gateway
metadata:
  labels:
    app.kubernetes.io/name: confluent-for-kubernetes
    app.kubernetes.io/managed-by: kustomize
  name: migration-gateway
spec:
  replicas: 3
  image:
    application: <gateway-image>
    init: confluentinc/confluent-init-container:3.1.0
  podTemplate:
    envVars:
      - name: GATEWAY_ROOT_LOG_LEVEL
        value: DEBUG  # Can be changed to INFO for production
  streamingDomains:
    - name: source-kafka-cluster
      type: kafka
      kafkaCluster:
        bootstrapServers:
          - id: MTLS
            # Replace with your MSK TLS bootstrap server (port 9094)
            endpoint: SSL://<msk-bootstrap-server>:9094
            tls:
              secretRef: msk-client-tls
        nodeIdRanges:
          - name: pool-1
            start: 1
            end: 3
  routes:
    - name: migration-route
      endpoint: <gateway-lb-hostname>:9595
      fence:
        scope: ALL
        errorCode: BROKER_NOT_AVAILABLE
      brokerIdentificationStrategy:
        type: port
      streamingDomain:
        name: source-kafka-cluster
        bootstrapServerId: MTLS
      security:
        auth: passthrough  # Gateway uses same mTLS cert as client when connecting to MSK
        client:
          tls:
            secretRef: gateway-tls
          authentication:
            type: mtls
            mtls:
              principalMappingRules: ["RULE:^CN=([^,]+).*$/$1/"]
              sslClientAuthentication: "required"
  externalAccess:
    type: loadBalancer
    loadBalancer:
      domain: <gateway-lb-hostname>
```

### gateway_switchover.yaml

Switchover state: clients connect with mTLS, gateway swaps to SASL/PLAIN when connecting to Confluent Cloud using `auth: swap` with file store credential mapping.

```yaml
apiVersion: platform.confluent.io/v1beta1
kind: Gateway
metadata:
  labels:
    app.kubernetes.io/name: confluent-for-kubernetes
    app.kubernetes.io/managed-by: kustomize
  name: migration-gateway
spec:
  replicas: 3
  image:
    application: <gateway-image>
    init: confluentinc/confluent-init-container:3.1.0
  podTemplate:
    envVars:
      - name: GATEWAY_ROOT_LOG_LEVEL
        value: DEBUG  # Can be changed to INFO for production
  secretStores:
    - name: file-store
      provider:
        type: File
        configSecretRef: file-store-config
        clientCredentialsRef: file-store-ccloud-credentials
  streamingDomains:
    - name: confluent-cloud
      type: kafka
      kafkaCluster:
        bootstrapServers:
          - id: SASL_PLAIN
            # Replace with your Confluent Cloud bootstrap server
            endpoint: SASL_SSL://<ccloud-bootstrap-server>:9092
            tls:
              secretRef: tls
        nodeIdRanges:
          - name: pool-1
            start: 0
            end: 17
  routes:
    - name: migration-route
      endpoint: <gateway-lb-hostname>:9595
      brokerIdentificationStrategy:
        type: port
      streamingDomain:
        name: confluent-cloud
        bootstrapServerId: SASL_PLAIN
      security:
        auth: swap  # Gateway swaps client mTLS identity to SASL/PLAIN credentials via file store
        secretStore: file-store
        client:
          tls:
            secretRef: gateway-tls
          authentication:
            type: mtls
            mtls:
              principalMappingRules: ["RULE:^CN=([^,]+).*$/$1/"]
              sslClientAuthentication: "required"
        cluster:
          authentication:
            type: plain
            jaasConfigPassThrough:
              secretRef: plain-jaas
  externalAccess:
    type: loadBalancer
    loadBalancer:
      domain: <gateway-lb-hostname>
```

## Client Properties

Clients use mTLS configuration throughout all gateway states:

```properties
security.protocol=SSL
ssl.truststore.location=certs/client-truststore.jks
ssl.truststore.password=${SSL_TRUSTSTORE_PASSWORD}
ssl.keystore.location=certs/client-keystore.jks
ssl.keystore.password=${SSL_KEYSTORE_PASSWORD}
ssl.key.password=${SSL_KEY_PASSWORD}
```
