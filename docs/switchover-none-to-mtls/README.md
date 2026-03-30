# Switchover: No Auth → mTLS

This example configures the gateway to accept unauthenticated client connections and authenticate to Confluent Cloud using mTLS. The gateway uses `auth: passthrough` — the client connects with no auth, and the gateway presents a client certificate to CCloud.

## Environment Variables

| Variable | Description |
|---|---|
| `KUBECTL_NAMESPACE` | Kubernetes namespace for the gateway |

## Certificate Requirements

Before creating secrets, you need the following certificate files:

- `ca-chain.pem` — Your CA chain (upload to CCloud as the mTLS identity provider)
- `client-cert.pem` — Gateway client certificate for authenticating to CCloud
- `client-key.pem` — Gateway client private key
- `public-cacerts.pem` — Public CA chain for verifying CCloud's server certificate (e.g., Let's Encrypt)

**Certificate generation:** See the [Gateway certificates guide](https://github.com/confluentinc/confluent-kubernetes-examples/blob/master/gateway/certificates/README.md) for instructions on generating certificates.

**CCloud mTLS setup:** Follow the [CCloud mTLS identity provider setup guide](https://docs.confluent.io/cloud/current/security/authenticate/workload-identities/identity-providers/mtls/configure.html) to upload the CA chain and create an identity pool.

## Secrets Setup

| Secret | Purpose |
|--------|---------|
| `ccloud-client-tls` | Client certificate and key the gateway presents to Confluent Cloud for mTLS authentication, plus the public CA chain to verify CCloud's server certificate (switchover state) |

### 1. CCloud Client mTLS Secret (`ccloud-client-tls`)

Create the client mTLS secret. The gateway uses this to authenticate to Confluent Cloud:

```bash
kubectl create secret generic ccloud-client-tls \
  --from-file=fullchain.pem=certs/client-cert.pem \
  --from-file=privkey.pem=certs/client-key.pem \
  --from-file=cacerts.pem=certs/public-cacerts.pem \
  -n ${KUBECTL_NAMESPACE}
```

## Gateway YAMLs

### gateway_init.yaml

Initial state — routes traffic to MSK:

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
          - id: UNAUTHED
            # Replace with your MSK bootstrap server (plaintext port 9092 for unauthenticated access)
            endpoint: PLAINTEXT://<msk-bootstrap-server>:9092
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
        bootstrapServerId: UNAUTHED
      security:
        client:
          authentication:
            type: none
        auth: passthrough
  externalAccess:
    type: loadBalancer
    loadBalancer:
      domain: <gateway-lb-hostname>
```

### gateway_fenced.yaml

Fenced state — blocks all client traffic:

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
          - id: UNAUTHED
            endpoint: PLAINTEXT://<msk-bootstrap-server>:9092
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
        bootstrapServerId: UNAUTHED
      security:
        client:
          authentication:
            type: none
        auth: passthrough
  externalAccess:
    type: loadBalancer
    loadBalancer:
      domain: <gateway-lb-hostname>
```

### gateway_switchover.yaml

Switchover state — routes traffic to Confluent Cloud:

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
            # Replace with your Confluent Cloud bootstrap server
            # NOTE: mTLS uses SSL:// (not SASL_SSL://) - authentication is at the TLS layer
            endpoint: SSL://<ccloud-bootstrap-server>:9092
            tls:
              secretRef: ccloud-client-tls
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
        bootstrapServerId: MTLS
      security:
        auth: passthrough
  externalAccess:
    type: loadBalancer
    loadBalancer:
      domain: <gateway-lb-hostname>
```

**Note on protocol:** The `gateway_switchover.yaml` uses `SSL://` (not `SASL_SSL://`) for the CCloud endpoint since mTLS authenticates at the TLS layer, not the SASL layer.

## Client Properties

No client configuration needed — client auth is `none`.
