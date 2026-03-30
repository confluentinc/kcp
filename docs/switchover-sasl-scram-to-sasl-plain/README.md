# Switchover: SASL/SCRAM → SASL/PLAIN

This example configures the gateway to accept client connections using SASL/SCRAM-SHA-512 and swap credentials to SASL/PLAIN when authenticating to Confluent Cloud. This is a common pattern when migrating from MSK (which supports SASL/SCRAM) to Confluent Cloud.

## Overview

The switchover follows a three-state pattern:

| State | YAML | Description |
|-------|------|-------------|
| **Initial** | `gateway_init.yaml` | Two routes: the main client route passes SCRAM traffic through to MSK; the pre-registration route is backed by Confluent Cloud for registering SCRAM credentials |
| **Fenced** | `gateway_fenced.yaml` | Client route is fenced; pre-registration route remains available |
| **Switchover** | `gateway_switchover.yaml` | Single client route with auth swap (SCRAM → SASL/PLAIN) routing to Confluent Cloud |

The client configuration does not change across any state transition. The gateway transparently swaps SCRAM credentials for SASL/PLAIN when authenticating to Confluent Cloud.

> **Required before the fenced state:** All SCRAM users must be pre-registered via the dedicated pre-registration route while the gateway is in the init state. KCP will apply the fenced state as part of migration — registration cannot happen after that. Failure to pre-register users means clients will be unable to authenticate after switchover.
>
> **Note:** SCRAM passwords must not contain `[` or `]` characters — these conflict with the `kafka-configs.sh` bracket syntax used during registration.
>
> See [SCRAM Registration](#scram-registration) below for instructions.

## Environment Variables

| Variable | Description |
|----------|-------------|
| `KUBECTL_NAMESPACE` | Kubernetes namespace for the gateway |
| `JKS_PASSWORD` | Password for JKS truststores |
| `JAVA_HOME` | Path to JDK installation |
| `SASL_PLAIN_USERNAME` | Confluent Cloud API key |
| `SASL_PLAIN_PASSWORD` | Confluent Cloud API secret |
| `SCRAM_USERNAME` | Client SCRAM username |
| `SCRAM_PASSWORD` | Client SCRAM password (must not contain `[` or `]`) |
| `SCRAM_ADMIN_USERNAME` | Admin SCRAM username for credential registration |
| `SCRAM_ADMIN_PASSWORD` | Admin SCRAM password |

## Prerequisites

- CFK operator installed on Kubernetes cluster
- `kubectl` configured to access the cluster
- MSK cluster with SASL/SCRAM-SHA-512 and TLS (port 9096)
- Confluent Cloud cluster with SASL/PLAIN credentials (API key/secret)
- HashiCorp Vault accessible from the cluster
- JDK installation (for truststore generation)

## Secrets Setup

| Secret | Purpose |
|--------|---------|
| `msk-tls` | JKS truststore for verifying MSK's server certificate (init and fenced states) |
| `tls` | JKS truststore for verifying Confluent Cloud's server certificate (all states — used by the pre-registration route in init/fenced, and the main route in switchover) |
| `vault-config` | Vault address, auth token, and path configuration for the credential store (all states) |
| `scram-admin-credentials` | SCRAM admin username and password used by the gateway to authenticate SCRAM registration requests (all states) |
| `plain-jaas` | JAAS config template for gateway-to-CCloud SASL/PLAIN authentication (all states) |

### 1. MSK TLS Truststore

Create a truststore for verifying MSK's TLS certificate:

```bash
cp $JAVA_HOME/lib/security/cacerts msk-truststore.jks
echo "jksPassword=${JKS_PASSWORD}" > msk-jksPassword.txt

kubectl create secret generic msk-tls \
  --from-file=truststore.jks=msk-truststore.jks \
  --from-file=jksPassword.txt=msk-jksPassword.txt \
  -n ${KUBECTL_NAMESPACE}
```

### 2. Confluent Cloud TLS Truststore

Create a truststore for verifying Confluent Cloud's TLS certificate:

```bash
cp $JAVA_HOME/lib/security/cacerts ccloud-truststore.jks
echo "jksPassword=${JKS_PASSWORD}" > ccloud-jksPassword.txt

kubectl create secret generic tls \
  --from-file=truststore.jks=ccloud-truststore.jks \
  --from-file=jksPassword.txt=ccloud-jksPassword.txt \
  -n ${KUBECTL_NAMESPACE}
```

### 3. Vault Configuration

Create the Vault config secret. Vault must be accessible from the cluster.

```bash
kubectl create secret generic vault-config \
  --from-literal=address=<vault-address> \
  --from-literal=authToken=<vault-auth-token> \
  --from-literal=prefixPath=secret/ \
  --from-literal=separator=/ \
  -n ${KUBECTL_NAMESPACE}
```

### 4. SCRAM Admin Credentials

Create the SCRAM admin credentials secret:

```bash
kubectl create secret generic scram-admin-credentials \
  --from-literal=username="${SCRAM_ADMIN_USERNAME}" \
  --from-literal=password="${SCRAM_ADMIN_PASSWORD}" \
  -n ${KUBECTL_NAMESPACE}
```

### 5. JAAS Config Template

Create the JAAS config template for gateway-to-CCloud SASL/PLAIN authentication:

```bash
kubectl create secret generic plain-jaas \
  --from-literal=plain-jaas.conf='org.apache.kafka.common.security.plain.PlainLoginModule required username="%s" password="%s";' \
  -n ${KUBECTL_NAMESPACE}
```

### 6. Vault Credential Mappings

Pre-populate Vault with SCRAM-to-PLAIN credential mappings for each user:

```bash
vault kv put secret/${SCRAM_USERNAME} value="${SASL_PLAIN_USERNAME}/${SASL_PLAIN_PASSWORD}"
vault kv put secret/${SCRAM_ADMIN_USERNAME} value="${SASL_PLAIN_USERNAME}/${SASL_PLAIN_PASSWORD}"
```

These mappings are used:
- **Init state (pre-registration route)**: When the admin connects to the pre-registration route with SCRAM credentials to register a user, the gateway swaps to SASL/PLAIN to authenticate to Confluent Cloud
- **Switchover state**: When clients connect, the gateway swaps their SCRAM credentials for SASL/PLAIN to authenticate to Confluent Cloud

**These Vault entries must exist before deploying the init state** — the pre-registration route needs the admin's mapping to authenticate to Confluent Cloud on first connection.

## Gateway YAMLs

### gateway_init.yaml

Two routes and two streaming domains:

- **Main client route**: Normal SCRAM clients connect here. Traffic is routed to MSK with `auth: passthrough` (no credential transformation)
- **Pre-registration route**: A dedicated route backed by Confluent Cloud where SCRAM credentials are registered via `kafka-configs.sh`. Uses `auth: swap` (SCRAM → SASL/PLAIN) so the gateway can authenticate to CCloud. The gateway intercepts `AlterUserScramCredentials` requests and stores SCRAM verifiers in Vault

Every user that needs to authenticate during switchover must be pre-registered via the pre-registration route before applying the fenced state.

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
            # Replace with your MSK bootstrap server (SASL_SSL port, typically 9096)
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
        bootstrapServerId: SASL_PLAIN
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
            type: plain
            jaasConfigPassThrough:
              secretRef: plain-jaas
  externalAccess:
    type: loadBalancer
    loadBalancer:
      domain: <gateway-lb-hostname>
```

### gateway_fenced.yaml

The client route on port 9595 is fenced with `errorCode: BROKER_NOT_AVAILABLE`. The registration route on port 9599 is retained.

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
            # Replace with your MSK bootstrap server (SASL_SSL port, typically 9096)
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
        bootstrapServerId: SASL_PLAIN
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
            type: plain
            jaasConfigPassThrough:
              secretRef: plain-jaas
  externalAccess:
    type: loadBalancer
    loadBalancer:
      domain: <gateway-lb-hostname>
```

### gateway_switchover.yaml

Single route on port 9595 with `auth: swap` (SCRAM → SASL/PLAIN) routing to Confluent Cloud. Only the CCloud streaming domain remains.

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
    - name: vault-store
      provider:
        type: Vault
        configSecretRef: vault-config
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
            type: plain
            jaasConfigPassThrough:
              secretRef: plain-jaas
  externalAccess:
    type: loadBalancer
    loadBalancer:
      domain: <gateway-lb-hostname>
```

## SCRAM Registration

Before clients can authenticate, their SCRAM credentials must be registered with the gateway.

Create a `scram-admin.properties` file:

```properties
security.protocol=SASL_PLAINTEXT
sasl.mechanism=SCRAM-SHA-512
sasl.jaas.config=org.apache.kafka.common.security.scram.ScramLoginModule required username="${SCRAM_ADMIN_USERNAME}" password="${SCRAM_ADMIN_PASSWORD}";
```

Register each user via the registration route on port 9599:

```bash
kafka-configs.sh \
  --bootstrap-server <gateway-lb-hostname>:9599 \
  --command-config scram-admin.properties \
  --alter \
  --add-config "SCRAM-SHA-512=[iterations=8192,password=${SCRAM_PASSWORD}]" \
  --entity-type users \
  --entity-name ${SCRAM_USERNAME}
```

**Important:** SCRAM passwords must not contain `[` or `]` characters (these conflict with the bracket syntax used by `kafka-configs.sh`).

## Client Properties

Clients connect using SCRAM credentials:

```properties
security.protocol=SASL_PLAINTEXT
sasl.mechanism=SCRAM-SHA-512
sasl.jaas.config=org.apache.kafka.common.security.scram.ScramLoginModule required username="${SCRAM_USERNAME}" password="${SCRAM_PASSWORD}";
```

These properties remain unchanged across all three states.

## Client Behaviour During State Transitions

When the gateway is fenced, `BROKER_NOT_AVAILABLE` is returned during the SASL handshake. `kafka-console-consumer.sh` treats this as a fatal `IllegalSaslStateException` and exits — it must be restarted after applying the switchover state. `kafka-console-producer.sh` stays alive and reconnects automatically. Production clients with proper retry logic behave like the producer.

