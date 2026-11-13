# Credential File Catalog for the Resources Manifest

A migration manifest holds no secrets: each connection pairs an address in the manifest with a credentials file that holds only authentication. This page catalogs every supported credential file, one for each authentication method, that you can copy next to your manifest and point a slot at.

Every connection in the manifest is an `{address, credentials}` pair. The address (`bootstrapServers` or REST `endpoint`) lives in the manifest, and `credentials:` points at an external file. Credential files come in two families, matched to the kind of endpoint a slot talks to: Apache Kafka® brokers or a REST API.

## Credential family for each slot

| Manifest slot | Mode | Talks to | Family |
|---|---|---|---|
| `spec.source.credentials` | Both | Source Kafka cluster (reads the cluster ID) | Kafka |
| `spec.clusterLink.source.credentials` | Destination | The link, to the source Kafka cluster | Kafka |
| `spec.clusterLink.destination.credentials` | Source | The source-side link, to the destination Kafka cluster | Kafka |
| `spec.target.clusterCredentials` | Both | Target Kafka Admin REST API (`/kafka/v3`), for the cluster link, topics, and access control lists (ACLs) | REST |
| `spec.target.cloudCredentials` | Both | Confluent Cloud Cloud or Global API. Required when `spec.acls` is present. Confluent Cloud only. | REST |
| `spec.clusterLink.sourceRest.credentials` | Source | The source's Admin REST API, which creates the outbound link | REST |

## Kafka credentials

Each Kafka credential file holds authentication only, for a single cluster. Specify exactly one method block: its presence selects the method, with no `auth_method:` wrapper and no `use:` flag. An optional top-level `insecure_skip_tls_verify: false` applies to test environments only.

| File | Method | Notes |
|---|---|---|
| [`kafka-iam.yaml`](kafka-iam.yaml) | `iam` | For reading an MSK source only. Confluent Cloud can't present IAM, so links to MSK use SCRAM. |
| [`kafka-sasl-scram.yaml`](kafka-sasl-scram.yaml) | `sasl_scram` | Always TLS (SASL_SSL). `mechanism` is required (`SHA256` or `SHA512`). |
| [`kafka-sasl-plain.yaml`](kafka-sasl-plain.yaml) | `sasl_plain` | With `ca_cert`, SASL_SSL with that CA. With `tls: true`, SASL_SSL with the system's public roots. With neither, SASL_PLAINTEXT. |
| [`kafka-mtls.yaml`](kafka-mtls.yaml) | `mtls` | Client certificate and key. The client is authenticated. |
| [`kafka-unauthenticated-tls.yaml`](kafka-unauthenticated-tls.yaml) | `unauthenticated_tls` | One-way TLS. The client isn't authenticated. |
| [`kafka-unauthenticated-plaintext.yaml`](kafka-unauthenticated-plaintext.yaml) | `unauthenticated_plaintext` | No authentication and no TLS. For test and lab environments only. |

`ca_cert`, on `sasl_scram`, `sasl_plain`, `mtls`, and `unauthenticated_tls`, is the PEM path that kcp uses to verify the broker's TLS certificate. kcp honors it for both the source read and the cluster link truststore. Supply it only for a private or internal certificate authority (CA). Brokers with a public CA, such as Amazon MSK and Confluent Cloud, validate against the system trust store and need no `ca_cert`. For `sasl_plain` on a listener with a public CA, set `tls: true` with no `ca_cert` to select SASL_SSL over the system trust store. Without either setting, `sasl_plain` is SASL_PLAINTEXT, which is cleartext.

!!! note
    This format isn't the `apache-kafka-credentials.yaml` format that `kcp scan` uses. The scan format lists several `clusters:`, each with its own `bootstrap_servers`, an `auth_method:` wrapper with `use:` flags, and metrics blocks used only for scans. If you pass the scan format as a migration credentials file, kcp rejects it with a hint.

## REST credentials

REST credential files authenticate to the Kafka Admin and Cluster Links REST APIs. Specify exactly one block, or the `api_key` and `api_secret` pair.

| File | Method | Notes |
|---|---|---|
| [`rest-api-key.yaml`](rest-api-key.yaml) | `api_key` and `api_secret` | Confluent Cloud. A top-level pair. Public CA. |
| [`rest-basic.yaml`](rest-basic.yaml) | `basic` | Username and password, such as for Confluent Platform Metadata Service (MDS). |
| [`rest-bearer.yaml`](rest-bearer.yaml) | `bearer` | `token`, such as for MDS or OAuth. |
| [`rest-mtls.yaml`](rest-mtls.yaml) | `mtls` | Client certificate and key, with authentication at the TLS layer. |

`basic`, `bearer`, and `mtls` each accept an optional `ca_cert` and `insecure_skip_verify`, to reach a TLS endpoint that uses a private or internal CA, such as self-managed Confluent Platform or MDS. Endpoints with a public CA, such as Confluent Cloud with `api_key`, need neither.

## Related content

- [Migration resources manifest reference](../manifest-reference.md)
- [Manifests and credential files for kcp migrations](../README.md)
- [Apache Kafka credentials file reference](../../apache-kafka-configuration/credentials.md)
- [Credentials in the migration manifest](../cutover/credentials.md)
