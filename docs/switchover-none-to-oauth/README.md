# Switchover: No Auth → OAuth

This example configures the gateway to accept unauthenticated client connections and authenticate to Confluent Cloud using OAuth.

## Environment Variables

| Variable | Description |
|---|---|
| `KUBECTL_NAMESPACE` | Kubernetes namespace for the gateway |
| `JKS_PASSWORD` | Password for JKS truststores |
| `JAVA_HOME` | Path to JDK installation |
| `OAUTH_CLIENT_ID` | OAuth client ID from your identity provider |
| `OAUTH_CLIENT_SECRET` | OAuth client secret from your identity provider |
| `CCLOUD_LOGICAL_CLUSTER` | Confluent Cloud logical cluster ID (e.g., `lkc-xxxxx`) |
| `CCLOUD_IDENTITY_POOL_ID` | Confluent Cloud identity pool ID (e.g., `pool-xxxxx`) |

## Secrets Setup

| Secret | Purpose |
|--------|---------|
| `tls` | JKS truststore for verifying Confluent Cloud's server certificate (switchover state) |
| `file-store-idp-credentials` | Maps the `ANONYMOUS` identity to IdP OAuth client credentials (switchover state) |
| `file-store-config` | File store separator configuration |
| `oauth-jaas` | OAuth JAAS config for gateway-to-CCloud authentication (switchover state) |

### 1. Confluent Cloud TLS truststore

```bash
cp $JAVA_HOME/lib/security/cacerts ccloud-truststore.jks
echo "jksPassword=${JKS_PASSWORD}" > ccloud-jksPassword.txt

kubectl create secret generic tls \
  --from-file=truststore.jks=ccloud-truststore.jks \
  --from-file=jksPassword.txt=ccloud-jksPassword.txt \
  -n ${KUBECTL_NAMESPACE}
```

### 2. File store credential mapping

Maps `ANONYMOUS` to OAuth client credentials:

```bash
kubectl create secret generic file-store-idp-credentials \
  --from-literal=ANONYMOUS="${OAUTH_CLIENT_ID}/${OAUTH_CLIENT_SECRET}" \
  -n ${KUBECTL_NAMESPACE}
```

### 3. File store config

```bash
kubectl create secret generic file-store-config \
  --from-literal=separator="/" \
  -n ${KUBECTL_NAMESPACE}
```

### 4. OAuth JAAS config

**Important:** The secret key must be `oauth-jass.conf` (single 'a'), not `oauth-jaas.conf`.

```bash
kubectl create secret generic oauth-jaas \
  --from-literal=oauth-jass.conf="org.apache.kafka.common.security.oauthbearer.OAuthBearerLoginModule required clientId=\"%s\" clientSecret=\"%s\" extension_logicalCluster=\"${CCLOUD_LOGICAL_CLUSTER}\" extension_identityPoolId=\"${CCLOUD_IDENTITY_POOL_ID}\";" \
  -n ${KUBECTL_NAMESPACE}
```

**Note:** Set `CCLOUD_LOGICAL_CLUSTER` and `CCLOUD_IDENTITY_POOL_ID` environment variables to your Confluent Cloud values before running this command. These IDs link the OAuth token to your CCloud identity pool.

## Confluent Cloud Identity Provider Setup

Before deploying the gateway, configure an OAuth-compatible identity provider on Confluent Cloud:

https://docs.confluent.io/cloud/current/security/authenticate/workload-identities/identity-providers/oauth/overview.html

Key steps:
1. Create an identity provider pointing to your IdP's JWKS endpoint
2. Create an identity pool that maps token claims to a CCloud identity
3. Note the logical cluster ID (`lkc-xxxxx`) and identity pool ID (`pool-xxxxx`) — these go into the `oauth-jaas` secret

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

**Note:** MSK uses `nodeIdRanges` with `start: 1, end: 3`.

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
      - name: GATEWAY_OPTS
        # Replace with your token endpoint URI (must match tokenEndpointUri below)
        value: "-Dorg.apache.kafka.sasl.oauthbearer.allowed.urls=<token-endpoint-uri>"
  secretStores:
    - name: file-store
      provider:
        type: File
        configSecretRef: file-store-config
        clientCredentialsRef: file-store-idp-credentials
  streamingDomains:
    - name: confluent-cloud
      type: kafka
      kafkaCluster:
        bootstrapServers:
          - id: OAUTH
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
        bootstrapServerId: OAUTH
      security:
        auth: swap
        secretStore: file-store
        client:
          authentication:
            type: none
        cluster:
          authentication:
            type: oauth
            jaasConfigPassThrough:
              secretRef: oauth-jaas
            oauthSettings:
              # Replace with your token endpoint URI (must match tokenEndpointUri below)
              tokenEndpointUri: <token-endpoint-uri>
  externalAccess:
    type: loadBalancer
    loadBalancer:
      domain: <gateway-lb-hostname>
```

**Important:** `<token-endpoint-uri>` appears in **two places** and both must be set to the same value:
1. `spec.podTemplate.envVars[GATEWAY_OPTS].value`
2. `spec.routes[0].security.cluster.authentication.oauthSettings.tokenEndpointUri`

**Note:** Confluent Cloud uses `nodeIdRanges` with `start: 0, end: 17`.

## Client Configuration

No client configuration needed. Client authentication is `none` — clients connect without credentials.
