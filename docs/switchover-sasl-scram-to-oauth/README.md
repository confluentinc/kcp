# Switchover: SASL/SCRAM → OAuth

This example configures the gateway to accept client connections using SASL/SCRAM-SHA-512 and swap credentials to OAuth when authenticating to Confluent Cloud. This is a common pattern when migrating from MSK (which supports SASL/SCRAM) to Confluent Cloud.

## Overview

The gateway transitions through three states by sequentially applying Custom Resource updates:

| State | Description |
|-------|-------------|
| **Init** | Two routes: the main client route passes SCRAM traffic through to MSK; the pre-registration route is backed by Confluent Cloud and stores SCRAM verifiers in Vault |
| **Fenced** | Client route fenced (returns `BROKER_NOT_AVAILABLE`), pre-registration route retained |
| **Switchover** | Single client route with SCRAM→OAuth credential swap to Confluent Cloud |

The client configuration remains unchanged throughout all transitions. The gateway transparently swaps SCRAM credentials for OAuth when authenticating to Confluent Cloud.

> **Required before the fenced state:** All SCRAM users must be pre-registered via the dedicated pre-registration route while the gateway is in the init state. KCP will apply the fenced state as part of migration — registration cannot happen after that. Failure to pre-register users means clients will be unable to authenticate after switchover.
>
> **Note:** SCRAM passwords must not contain `[` or `]` characters — these conflict with the `kafka-configs.sh` bracket syntax used during registration.
>
> See [SCRAM Registration](#scram-registration) below for instructions.

## Environment Variables

| Variable | Description |
|----------|-------------|
| `KUBECTL_NAMESPACE` | Kubernetes namespace |
| `JKS_PASSWORD` | Password for JKS truststores |
| `JAVA_HOME` | Path to JDK home (for extracting cacerts) |
| `OAUTH_CLIENT_ID` | OAuth client ID for gateway-to-CCloud authentication |
| `OAUTH_CLIENT_SECRET` | OAuth client secret for gateway-to-CCloud authentication |
| `CCLOUD_LOGICAL_CLUSTER` | Confluent Cloud logical cluster ID (e.g. `lkc-xxxxx`) |
| `CCLOUD_IDENTITY_POOL_ID` | Confluent Cloud identity pool ID (e.g. `pool-xxxxx`) |
| `SCRAM_USERNAME` | SASL/SCRAM username for client |
| `SCRAM_PASSWORD` | SASL/SCRAM password for client (must not contain `[` or `]`) |
| `SCRAM_ADMIN_USERNAME` | SCRAM admin username for registration |
| `SCRAM_ADMIN_PASSWORD` | SCRAM admin password for registration |

## Secrets Setup

| Secret | Purpose |
|--------|---------|
| `msk-tls` | JKS truststore for verifying MSK's server certificate (init and fenced states) |
| `tls` | JKS truststore for verifying Confluent Cloud's server certificate (all states — used by the pre-registration route in init/fenced, and the main route in switchover) |
| `vault-config` | Vault address, auth token, and path configuration for the credential store (all states) |
| `scram-admin-credentials` | SCRAM admin username and password used by the gateway to authenticate SCRAM registration requests (all states) |
| `oauth-jaas` | OAuth JAAS config for gateway-to-CCloud authentication (all states) |

Create MSK truststore:
```bash
cp $JAVA_HOME/lib/security/cacerts msk-truststore.jks
echo "jksPassword=${JKS_PASSWORD}" > msk-jksPassword.txt

kubectl create secret generic msk-tls \
  --from-file=truststore.jks=msk-truststore.jks \
  --from-file=jksPassword.txt=msk-jksPassword.txt \
  -n ${KUBECTL_NAMESPACE}
```

Create Confluent Cloud truststore:
```bash
cp $JAVA_HOME/lib/security/cacerts ccloud-truststore.jks
echo "jksPassword=${JKS_PASSWORD}" > ccloud-jksPassword.txt

kubectl create secret generic tls \
  --from-file=truststore.jks=ccloud-truststore.jks \
  --from-file=jksPassword.txt=ccloud-jksPassword.txt \
  -n ${KUBECTL_NAMESPACE}
```

Create Vault configuration (Vault must be accessible from the cluster):
```bash
kubectl create secret generic vault-config \
  --from-literal=address=<vault-address> \
  --from-literal=authToken=<vault-auth-token> \
  --from-literal=prefixPath=secret/ \
  --from-literal=separator=/ \
  -n ${KUBECTL_NAMESPACE}
```

Create SCRAM admin credentials:
```bash
kubectl create secret generic scram-admin-credentials \
  --from-literal=username="${SCRAM_ADMIN_USERNAME}" \
  --from-literal=password="${SCRAM_ADMIN_PASSWORD}" \
  -n ${KUBECTL_NAMESPACE}
```

Create OAuth JAAS configuration:

**Important:** The secret key must be `oauth-jass.conf` (single 'a'), not `oauth-jaas.conf` — this is a quirk in the CFK operator's validation code.

```bash
kubectl create secret generic oauth-jaas \
  --from-literal=oauth-jass.conf="org.apache.kafka.common.security.oauthbearer.OAuthBearerLoginModule required clientId=\"%s\" clientSecret=\"%s\" extension_logicalCluster=\"${CCLOUD_LOGICAL_CLUSTER}\" extension_identityPoolId=\"${CCLOUD_IDENTITY_POOL_ID}\";" \
  -n ${KUBECTL_NAMESPACE}
```

Pre-populate Vault with SCRAM-to-OAuth credential mappings:
```bash
vault kv put secret/${SCRAM_USERNAME} value="${OAUTH_CLIENT_ID}/${OAUTH_CLIENT_SECRET}"
vault kv put secret/${SCRAM_ADMIN_USERNAME} value="${OAUTH_CLIENT_ID}/${OAUTH_CLIENT_SECRET}"
```

**These Vault entries must exist before deploying the init state** — the pre-registration route needs the admin's mapping to authenticate to Confluent Cloud on first connection.

## Gateway YAMLs

### gateway_init.yaml

Two routes: port 9595 passthrough to MSK, port 9599 registration route to Confluent Cloud with SCRAM→OAuth swap. Two streaming domains (MSK and Confluent Cloud).

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
        value: DEBUG # Can be changed to INFO for production
      - name: GATEWAY_OPTS
        # Replace with your token endpoint URI (must match tokenEndpointUri below)
        value: "-Dorg.apache.kafka.sasl.oauthbearer.allowed.urls=<token-endpoint-uri>"
  secretStores:
    - name: vault-store
      provider:
        type: Vault
        configSecretRef: vault-config
  streamingDomains:
    - name: source-kafka-cluster
      type: kafka
      kafkaCluster:
        bootstrapServers:
          - id: SCRAM
            # MSK SCRAM uses SASL_SSL port 9096
            endpoint: SASL_SSL://<msk-bootstrap-server>:9096
            tls:
              secretRef: msk-tls
        nodeIdRanges:
          - name: pool-1
            start: 1
            end: 3
    - name: confluent-cloud
      type: kafka
      kafkaCluster:
        bootstrapServers:
          - id: OAUTH
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
        name: source-kafka-cluster
        bootstrapServerId: SCRAM
      security:
        auth: passthrough
    - name: scram-registration-route
      endpoint: <gateway-lb-hostname>:9599
      brokerIdentificationStrategy:
        type: port
      streamingDomain:
        name: confluent-cloud
        bootstrapServerId: OAUTH
      security:
        auth: swap
        secretStore: vault-store
        client:
          authentication:
            type: scram
            scram:
              alterScramCredentials: true
              admin:
                secretRef: scram-admin-credentials
        cluster:
          authentication:
            type: oauth
            jaasConfigPassThrough:
              secretRef: oauth-jaas
            oauthSettings:
              # Must match GATEWAY_OPTS value above
              tokenEndpointUri: <token-endpoint-uri>
  externalAccess:
    type: loadBalancer
    loadBalancer:
      domain: <gateway-lb-hostname>
```

### gateway_fenced.yaml

Client route fenced (returns `BROKER_NOT_AVAILABLE` during SASL handshake), registration route retained. Two streaming domains.

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
        value: DEBUG # Can be changed to INFO for production
      - name: GATEWAY_OPTS
        # Replace with your token endpoint URI (must match tokenEndpointUri below)
        value: "-Dorg.apache.kafka.sasl.oauthbearer.allowed.urls=<token-endpoint-uri>"
  secretStores:
    - name: vault-store
      provider:
        type: Vault
        configSecretRef: vault-config
  streamingDomains:
    - name: source-kafka-cluster
      type: kafka
      kafkaCluster:
        bootstrapServers:
          - id: SCRAM
            # MSK SCRAM uses SASL_SSL port 9096
            endpoint: SASL_SSL://<msk-bootstrap-server>:9096
            tls:
              secretRef: msk-tls
        nodeIdRanges:
          - name: pool-1
            start: 1
            end: 3
    - name: confluent-cloud
      type: kafka
      kafkaCluster:
        bootstrapServers:
          - id: OAUTH
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
      fence:
        scope: ALL
        errorCode: BROKER_NOT_AVAILABLE
      brokerIdentificationStrategy:
        type: port
      streamingDomain:
        name: source-kafka-cluster
        bootstrapServerId: SCRAM
      security:
        auth: passthrough
    - name: scram-registration-route
      endpoint: <gateway-lb-hostname>:9599
      brokerIdentificationStrategy:
        type: port
      streamingDomain:
        name: confluent-cloud
        bootstrapServerId: OAUTH
      security:
        auth: swap
        secretStore: vault-store
        client:
          authentication:
            type: scram
            scram:
              alterScramCredentials: true
              admin:
                secretRef: scram-admin-credentials
        cluster:
          authentication:
            type: oauth
            jaasConfigPassThrough:
              secretRef: oauth-jaas
            oauthSettings:
              # Must match GATEWAY_OPTS value above
              tokenEndpointUri: <token-endpoint-uri>
  externalAccess:
    type: loadBalancer
    loadBalancer:
      domain: <gateway-lb-hostname>
```

### gateway_switchover.yaml

Single route with SCRAM→OAuth credential swap to Confluent Cloud. Single streaming domain (Confluent Cloud only). Note that `<token-endpoint-uri>` appears in **two places** and both must be set to the same value.

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
        value: DEBUG # Can be changed to INFO for production
      - name: GATEWAY_OPTS
        # Replace with your token endpoint URI (must match tokenEndpointUri below)
        value: "-Dorg.apache.kafka.sasl.oauthbearer.allowed.urls=<token-endpoint-uri>"
  secretStores:
    - name: vault-store
      provider:
        type: Vault
        configSecretRef: vault-config
  streamingDomains:
    - name: confluent-cloud
      type: kafka
      kafkaCluster:
        bootstrapServers:
          - id: OAUTH
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
        secretStore: vault-store
        client:
          authentication:
            type: scram
            scram:
              alterScramCredentials: true
              admin:
                secretRef: scram-admin-credentials
        cluster:
          authentication:
            type: oauth
            jaasConfigPassThrough:
              secretRef: oauth-jaas
            oauthSettings:
              # Must match GATEWAY_OPTS value above
              tokenEndpointUri: <token-endpoint-uri>
  externalAccess:
    type: loadBalancer
    loadBalancer:
      domain: <gateway-lb-hostname>
```

## SCRAM Registration

Before clients can authenticate during the switchover state, their SCRAM credentials must be registered with the gateway via the dedicated pre-registration route.

Create `scram-admin.properties`:
```properties
security.protocol=SASL_PLAINTEXT
sasl.mechanism=SCRAM-SHA-512
sasl.jaas.config=org.apache.kafka.common.security.scram.ScramLoginModule required username="${SCRAM_ADMIN_USERNAME}" password="${SCRAM_ADMIN_PASSWORD}";
```

**Important:** SCRAM passwords must not contain `[` or `]` characters — these conflict with the bracket syntax used by `kafka-configs.sh`.

Register SCRAM user:
```bash
kafka-configs.sh \
  --bootstrap-server <gateway-lb-hostname>:9599 \
  --command-config scram-admin.properties \
  --alter \
  --add-config "SCRAM-SHA-512=[iterations=8192,password=${SCRAM_PASSWORD}]" \
  --entity-type users \
  --entity-name ${SCRAM_USERNAME}
```

All users must be pre-registered before transitioning to the fenced state.

## Client Properties

Client configuration remains unchanged throughout all state transitions:

```properties
security.protocol=SASL_PLAINTEXT
sasl.mechanism=SCRAM-SHA-512
sasl.jaas.config=org.apache.kafka.common.security.scram.ScramLoginModule required username="${SCRAM_USERNAME}" password="${SCRAM_PASSWORD}";
```

## Client Behaviour During State Transitions

- **Fenced state**: Clients receive `BROKER_NOT_AVAILABLE` during the SASL handshake. Consumers exit with a fatal `IllegalSaslStateException` and must be restarted after switchover. Producers stay alive and automatically reconnect after switchover.
- **Switchover state**: Producers reconnect automatically. Consumers must be restarted (with the same configuration).
