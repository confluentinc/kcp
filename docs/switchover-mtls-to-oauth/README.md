# Switchover: mTLS → OAuth

This example configures the gateway to accept client connections using mTLS and authenticate to Confluent Cloud using OAuth. The gateway swaps the client's mTLS certificate identity for OAuth credentials during the switchover state.

## mTLS Leg Behaviour

True TLS passthrough is not supported — TLS terminates on each leg independently (client→gateway, then gateway→cluster). In the init and fenced states, the gateway maintains the client's identity by using the same ACM PCA-issued certificate on both legs: the client presents it for mTLS authentication to the gateway, and the gateway presents the same certificate when connecting to MSK. MSK sees the same CN on both hops, making the gateway transparent to its ACL system.

In the switchover state, the client certificate CN is looked up in the file store to retrieve IdP client credentials for OAuth token exchange with Confluent Cloud.

## Environment Variables

| Variable | Description |
|----------|-------------|
| `KUBECTL_NAMESPACE` | Kubernetes namespace for gateway deployment |
| `OAUTH_CLIENT_ID` | IdP client ID for OAuth authentication |
| `OAUTH_CLIENT_SECRET` | IdP client secret for OAuth authentication |
| `CCLOUD_LOGICAL_CLUSTER_ID` | Confluent Cloud logical cluster ID (e.g., `lkc-xxxxx`) |
| `CCLOUD_IDENTITY_POOL_ID` | Confluent Cloud identity pool ID (e.g., `pool-xxxxx`) |
| `JKS_PASSWORD` | JKS keystore password (default: `changeit`) |
| `SSL_TRUSTSTORE_PASSWORD` | Client SSL truststore password |
| `SSL_KEYSTORE_PASSWORD` | Client SSL keystore password |
| `SSL_KEY_PASSWORD` | Client SSL key password |
| `JAVA_HOME` | Path to JDK home (e.g., `/Library/Java/JavaVirtualMachines/temurin-17.jdk/Contents/Home`) |

## Certificate Requirements

This example requires several certificates for mTLS authentication:

1. **Gateway server certificate**: Presented to clients during TLS handshake. Can be self-signed for testing.
2. **Client certificate**: Issued by ACM Private CA, used by the client to authenticate to the gateway and by the gateway to authenticate to MSK.
3. **CA certificate**: ACM Private CA root certificate, used by the gateway to verify client certificates.
4. **Amazon trust bundle**: Amazon root CAs plus intermediate CA, used by the gateway to verify MSK broker certificates.

See the [Gateway certificates guide](https://github.com/confluentinc/confluent-kubernetes-examples/blob/master/gateway/certificates/README.md) for additional certificate configuration details.

## Confluent Cloud Identity Provider Setup

This example assumes you have configured an OAuth-compatible identity provider for your Confluent Cloud cluster. See the [CCloud identity provider setup documentation](https://docs.confluent.io/cloud/current/security/authenticate/workload-identities/identity-providers/oauth/overview.html) for configuration steps.

## Secrets Setup

| Secret | Purpose |
|--------|---------|
| `gateway-tls` | Gateway server certificate presented to clients, plus the ACM PCA CA used to verify client mTLS certificates (all states) |
| `msk-client-tls` | Client certificate the gateway presents to MSK for mTLS authentication, plus the Amazon trust bundle to verify MSK's server certificate (init and fenced states) |
| `tls` | JKS truststore for verifying Confluent Cloud's server certificate (switchover state) |
| `file-store-idp-credentials` | Maps client certificate CN to IdP OAuth client credentials (switchover state) |
| `file-store-config` | File store separator configuration |
| `oauth-jaas` | OAuth JAAS config for gateway-to-CCloud authentication (switchover state) |

Create the following Kubernetes secrets before deploying the gateway:

**Gateway server TLS** (used by clients to connect to the gateway):
```bash
kubectl create secret generic gateway-tls \
  --from-file=fullchain.pem=certs/fullchain.pem \
  --from-file=privkey.pem=certs/privkey.pem \
  --from-file=cacerts.pem=certs/ca.pem \
  -n ${KUBECTL_NAMESPACE}
```

**Note:** `cacerts.pem` here is `certs/ca.pem` — the ACM PCA CA used to verify client mTLS certificates. Do not use `certs/cacerts.pem`, which is the local gateway CA used only to build the client truststore.

**MSK client TLS** (used by the gateway to authenticate to MSK):
```bash
kubectl create secret generic msk-client-tls \
  --from-file=fullchain.pem=certs/client.pem \
  --from-file=privkey.pem=certs/client.key \
  --from-file=cacerts.pem=certs/amazon-trust-bundle.pem \
  -n ${KUBECTL_NAMESPACE}
```

**Confluent Cloud TLS truststore** (for gateway→CCloud TLS):
```bash
cp ${JAVA_HOME}/lib/security/cacerts ccloud-truststore.jks
echo "jksPassword=${JKS_PASSWORD}" > ccloud-jksPassword.txt

kubectl create secret generic tls \
  --from-file=truststore.jks=ccloud-truststore.jks \
  --from-file=jksPassword.txt=ccloud-jksPassword.txt \
  -n ${KUBECTL_NAMESPACE}
```

**File store credential mapping** (maps client cert CN to IdP credentials):
```bash
kubectl create secret generic file-store-idp-credentials \
  --from-literal=gateway-client="${OAUTH_CLIENT_ID}/${OAUTH_CLIENT_SECRET}" \
  -n ${KUBECTL_NAMESPACE}
```

**File store config**:
```bash
kubectl create secret generic file-store-config \
  --from-literal=separator="/" \
  -n ${KUBECTL_NAMESPACE}
```

**OAuth JAAS config** for gateway→CCloud OAuth:

**Important:** The secret key must be `oauth-jass.conf` (single 'a'), not `oauth-jaas.conf`, due to a quirk in the CFK operator's validation.

```bash
kubectl create secret generic oauth-jaas \
  --from-literal=oauth-jass.conf="org.apache.kafka.common.security.oauthbearer.OAuthBearerLoginModule required clientId=\"%s\" clientSecret=\"%s\" extension_logicalCluster=\"${CCLOUD_LOGICAL_CLUSTER_ID}\" extension_identityPoolId=\"${CCLOUD_IDENTITY_POOL_ID}\";" \
  -n ${KUBECTL_NAMESPACE}
```

## Gateway YAMLs

### gateway_init.yaml

Initial state: clients connect with mTLS, traffic routed to MSK with `auth: passthrough`.

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

Fenced state: clients receive `BROKER_NOT_AVAILABLE` errors. Uses `auth: passthrough` with mTLS to MSK.

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

Switchover state: clients connect with mTLS, gateway swaps to OAuth when connecting to Confluent Cloud using `auth: swap` with file store.

**Important:** `<token-endpoint-uri>` appears in TWO places and both must match:
- `spec.podTemplate.envVars[0].value` (GATEWAY_OPTS)
- `spec.routes[0].security.cluster.authentication.oauthSettings.tokenEndpointUri`

The JAAS config uses `<logical-cluster-id>` and `<identity-pool-id>` placeholders.

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
      - name: GATEWAY_OPTS
        # Replace with your IdP token endpoint URI (must match tokenEndpointUri below)
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
          tls:
            secretRef: gateway-tls
          authentication:
            type: mtls
            mtls:
              principalMappingRules: ["RULE:^CN=([^,]+).*$/$1/"]
              sslClientAuthentication: "required"
        cluster:
          authentication:
            type: oauth
            jaasConfigPassThrough:
              secretRef: oauth-jaas
            oauthSettings:
              # Replace with your IdP token endpoint URI (must match GATEWAY_OPTS above)
              tokenEndpointUri: <token-endpoint-uri>
  externalAccess:
    type: loadBalancer
    loadBalancer:
      domain: <gateway-lb-hostname>
```

## Client Properties

Clients connect to the gateway using mTLS with the following configuration:

```properties
security.protocol=SSL
ssl.truststore.location=certs/client-truststore.jks
ssl.truststore.password=${SSL_TRUSTSTORE_PASSWORD}
ssl.keystore.location=certs/client-keystore.jks
ssl.keystore.password=${SSL_KEYSTORE_PASSWORD}
ssl.key.password=${SSL_KEY_PASSWORD}
```

This configuration remains unchanged across all three gateway states (init, fenced, switchover).
