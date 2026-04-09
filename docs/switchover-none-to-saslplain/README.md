# Switchover: No Auth → SASL/PLAIN

This example configures the gateway to accept unauthenticated client connections and authenticate to Confluent Cloud using SASL/PLAIN.

## Environment Variables

| Variable | Description |
|---|---|
| `KUBECTL_NAMESPACE` | Kubernetes namespace for the gateway |
| `JKS_PASSWORD` | Password for JKS truststores |
| `JAVA_HOME` | Path to JDK installation (for cacerts) |
| `SASL_PLAIN_USERNAME` | Confluent Cloud API key |
| `SASL_PLAIN_PASSWORD` | Confluent Cloud API secret |

## Secrets Setup

| Secret | Purpose |
|--------|---------|
| `tls` | JKS truststore for verifying Confluent Cloud's server certificate (switchover state) |
| `file-store-noauth-credentials` | Maps the `ANONYMOUS` identity to CCloud SASL/PLAIN credentials (switchover state) |
| `file-store-config` | File store separator configuration |
| `plain-jaas` | JAAS config template for gateway-to-CCloud SASL/PLAIN authentication (switchover state) |

Create a truststore for verifying Confluent Cloud's TLS certificate (needed for the switchover state):
```bash
cp $JAVA_HOME/lib/security/cacerts ccloud-truststore.jks
echo "jksPassword=${JKS_PASSWORD}" > ccloud-jksPassword.txt

kubectl create secret generic tls \
  --from-file=truststore.jks=ccloud-truststore.jks \
  --from-file=jksPassword.txt=ccloud-jksPassword.txt \
  -n ${KUBECTL_NAMESPACE}
```

Create the file store credential mapping (maps anonymous clients to CCloud credentials, used in the switchover state):
```bash
kubectl create secret generic file-store-noauth-credentials \
  --from-literal=ANONYMOUS="${SASL_PLAIN_USERNAME}/${SASL_PLAIN_PASSWORD}" \
  -n ${KUBECTL_NAMESPACE}
```

Create the file store config:
```bash
kubectl create secret generic file-store-config \
  --from-literal=separator="/" \
  -n ${KUBECTL_NAMESPACE}
```

Create the JAAS config template for the gateway-to-CCloud SASL/PLAIN connection:
```bash
kubectl create secret generic plain-jaas \
  --from-literal=plain-jaas.conf='org.apache.kafka.common.security.plain.PlainLoginModule required username="%s" password="%s";' \
  -n ${KUBECTL_NAMESPACE}
```

## Gateway YAMLs

### gateway_init.yaml

Initial state: routes client traffic to MSK. Customize `<msk-bootstrap-server>` and `<gateway-lb-hostname>`.

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

Fenced state: blocks all client traffic with `BROKER_NOT_AVAILABLE`. Customize `<msk-bootstrap-server>` and `<gateway-lb-hostname>`.

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

Switchover state: routes client traffic to Confluent Cloud via SASL/PLAIN. Customize `<ccloud-bootstrap-server>` and `<gateway-lb-hostname>`.

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
        clientCredentialsRef: file-store-noauth-credentials
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
        auth: swap
        secretStore: file-store
        client:
          authentication:
            type: none
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

Since client auth is `none`, no client configuration file is needed. Clients connect directly to the gateway load balancer hostname without authentication.
