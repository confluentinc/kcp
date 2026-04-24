# Switchover: mTLS → mTLS

This example configures the gateway to accept client connections using mTLS and authenticate to both MSK (init) and Confluent Cloud (switchover) using the same client certificate. The gateway uses `auth: passthrough` in all states — the same certificate identity appears on both legs.

## mTLS Leg Behaviour

True TLS passthrough is not supported — TLS terminates on each leg independently (client→gateway, then gateway→cluster). In the init and fenced states, the gateway maintains the client's identity by using the same ACM PCA-issued certificate on both legs: the client presents it for mTLS authentication to the gateway, and the gateway presents the same certificate when connecting to MSK. MSK sees the same CN on both hops, making the gateway transparent to its ACL system.

In the switchover state, the same client certificate is presented to Confluent Cloud, which must be configured as an mTLS identity provider with the ACM PCA CA uploaded as the certificate chain.

## Environment Variables

| Variable | Description |
|----------|-------------|
| `KUBECTL_NAMESPACE` | Kubernetes namespace for gateway deployment |
| `SSL_TRUSTSTORE_PASSWORD` | Password for client truststore |
| `SSL_KEYSTORE_PASSWORD` | Password for client keystore |
| `SSL_KEY_PASSWORD` | Password for client key in keystore |

## Certificate Requirements

This example requires the following certificates:

1. **Gateway server certificate** — TLS certificate presented by the gateway to clients (can be self-signed for testing)
2. **Client certificate** — mTLS certificate issued by ACM Private CA, used by:
   - Kafka clients to authenticate to the gateway
   - The gateway to authenticate to MSK (init state)
   - The gateway to authenticate to Confluent Cloud (switchover state)
3. **CA chains for verification**:
   - **ACM PCA CA** — used by the gateway to verify client certificates
   - **Amazon trust bundle** — used by the gateway to verify MSK's server certificate
   - **Let's Encrypt CA chain** — used by the gateway to verify Confluent Cloud's server certificate

For detailed certificate generation, see the [Gateway certificates guide](https://github.com/confluentinc/confluent-kubernetes-examples/blob/master/gateway/certificates/README.md).

For Confluent Cloud mTLS setup, see [Configure mTLS identity provider on CCloud](https://docs.confluent.io/cloud/current/security/authenticate/workload-identities/identity-providers/mtls/configure.html).

## Secrets Setup

| Secret | Purpose |
|--------|---------|
| `gateway-tls` | Gateway server certificate presented to clients, plus the ACM PCA CA used to verify client mTLS certificates (all states) |
| `msk-client-tls` | Client certificate the gateway presents to MSK for mTLS authentication, plus the Amazon trust bundle to verify MSK's server certificate (init and fenced states) |
| `ccloud-client-tls` | Client certificate the gateway presents to Confluent Cloud for mTLS authentication, plus the public CA chain to verify CCloud's server certificate (switchover state) |

**Gateway server TLS** (presented to clients, used to verify client certificates):

```bash
kubectl create secret generic gateway-tls \
  --from-file=fullchain.pem=certs/fullchain.pem \
  --from-file=privkey.pem=certs/privkey.pem \
  --from-file=cacerts.pem=certs/ca.pem \
  -n ${KUBECTL_NAMESPACE}
```

**Note:** `cacerts.pem` here is `certs/ca.pem` — the ACM PCA CA used to verify client mTLS certificates. Do not use `certs/cacerts.pem`, which is the local gateway CA used only to build the client truststore.

**MSK client TLS** (used by gateway to authenticate to MSK):

```bash
kubectl create secret generic msk-client-tls \
  --from-file=fullchain.pem=certs/client.pem \
  --from-file=privkey.pem=certs/client.key \
  --from-file=cacerts.pem=certs/amazon-trust-bundle.pem \
  -n ${KUBECTL_NAMESPACE}
```

**Confluent Cloud client TLS** (used by gateway to authenticate to CCloud):

```bash
kubectl create secret generic ccloud-client-tls \
  --from-file=fullchain.pem=certs/client.pem \
  --from-file=privkey.pem=certs/client.key \
  --from-file=cacerts.pem=certs/public-cacerts.pem \
  -n ${KUBECTL_NAMESPACE}
```

## Gateway YAMLs

### gateway_init.yaml

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
              # cacerts.pem (Amazon trust bundle to verify MSK's server cert)
              secretRef: msk-client-tls
        nodeIdRanges:
          - name: pool-1
            start: 1  # Adjust to match your MSK cluster broker IDs
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
        auth: passthrough
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
            start: 1  # Adjust to match your MSK cluster broker IDs
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
        auth: passthrough
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
    - name: confluent-cloud
      type: kafka
      kafkaCluster:
        bootstrapServers:
          - id: MTLS
            # Replace with your Confluent Cloud mTLS bootstrap server (port 9092)
            endpoint: SSL://<ccloud-bootstrap-server>:9092
            tls:
              # PEM secret containing: fullchain.pem (client cert), privkey.pem (client key),
              # cacerts.pem (Let's Encrypt CA chain to verify CCloud's server cert)
              secretRef: ccloud-client-tls
        nodeIdRanges:
          - name: pool-1
            start: 0  # Adjust to match your CCloud cluster broker ID range
            end: 17
  routes:
    - name: migration-route
      endpoint: <gateway-lb-hostname>:9595
      brokerIdentificationStrategy:
        type: port
      streamingDomain:
        name: confluent-cloud
        bootstrapServerId: MTLS
      security:
        auth: passthrough
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

## Client Properties

Client configuration using mTLS to connect to the gateway:

```properties
security.protocol=SSL
ssl.truststore.location=certs/client-truststore.jks
ssl.truststore.password=${SSL_TRUSTSTORE_PASSWORD}
ssl.keystore.location=certs/client-keystore.jks
ssl.keystore.password=${SSL_KEYSTORE_PASSWORD}
ssl.key.password=${SSL_KEY_PASSWORD}
```

Note: `security.protocol=SSL` (not `SASL_SSL`) because mTLS uses TLS client certificates for authentication, not SASL.
