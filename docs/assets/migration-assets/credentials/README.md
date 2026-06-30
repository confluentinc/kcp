# Credential file catalog

A migration manifest holds **no secrets**. Every connection in the manifest is an
`{address, credentials}` pair: the address (`bootstrapServers` / REST `endpoint`)
lives in the manifest, and `credentials:` points at an external file that holds
**auth only**. This folder is a catalog of every supported credential file — one
per auth method — that you can copy next to your manifest and point a slot at.

There are two credential families, matched to the kind of endpoint a slot talks to.

## Which family does a slot use?

| Manifest slot | Mode | Talks to | Family |
|---|---|---|---|
| `spec.source.credentials` | both | source Kafka (reads the cluster id) | **Kafka** |
| `spec.clusterLink.source.credentials` | destination | the link → source Kafka | **Kafka** |
| `spec.clusterLink.destination.credentials` | source | the source-side link → destination Kafka | **Kafka** |
| `spec.target.credentials` | both | target Kafka Admin REST | **REST** |
| `spec.clusterLink.sourceRest.credentials` | source | the source's Admin REST (creates the outbound link) | **REST** |

## Kafka credentials

Auth-only, single-cluster. Specify **exactly one** method block — its **presence**
selects it (no `auth_method:` wrapper, no `use:` flag). An optional top-level
`insecure_skip_tls_verify: false` applies to test environments only.

| File | Method | Notes |
|---|---|---|
| [`kafka-iam.yaml`](kafka-iam.yaml) | `iam` | MSK source read only; CC can't present IAM (links to MSK use SCRAM) |
| [`kafka-sasl-scram.yaml`](kafka-sasl-scram.yaml) | `sasl_scram` | always TLS (SASL_SSL); `mechanism` required (SHA256/SHA512) |
| [`kafka-sasl-plain.yaml`](kafka-sasl-plain.yaml) | `sasl_plain` | `ca_cert` present ⇒ SASL_SSL; absent ⇒ SASL_PLAINTEXT |
| [`kafka-mtls.yaml`](kafka-mtls.yaml) | `mtls` | client cert + key (client is authenticated) |
| [`kafka-unauthenticated-tls.yaml`](kafka-unauthenticated-tls.yaml) | `unauthenticated_tls` | one-way TLS; client not authenticated |
| [`kafka-unauthenticated-plaintext.yaml`](kafka-unauthenticated-plaintext.yaml) | `unauthenticated_plaintext` | no auth, no TLS; test/lab only |

`ca_cert` (on `sasl_scram`, `sasl_plain`, `mtls`, `unauthenticated_tls`) is the PEM
path used to verify the broker's TLS certificate, and is honoured on **both** the
source read and the cluster-link truststore. Supply it only for a **private/internal
CA**; public-CA brokers (AWS MSK, Confluent Cloud) validate against the system trust
store and need no `ca_cert`.

> This is **not** the `kcp scan` `apache-kafka-credentials.yaml` format. The scan
> format lists multiple `clusters:`, each with its own `bootstrap_servers`, an
> `auth_method:` wrapper with `use:` flags, and scan-only metrics blocks. Passing
> the scan format to a migrate credentials file is rejected with a hint.

## REST credentials

Authenticate to the Kafka Admin / Cluster Links REST API. Specify **exactly one**
block (or the `api_key`/`api_secret` pair).

| File | Method | Notes |
|---|---|---|
| [`rest-api-key.yaml`](rest-api-key.yaml) | `api_key` + `api_secret` | Confluent Cloud; top-level pair; public CA |
| [`rest-basic.yaml`](rest-basic.yaml) | `basic` | username/password (e.g. CP MDS) |
| [`rest-bearer.yaml`](rest-bearer.yaml) | `bearer` | `token` (e.g. MDS/OAuth) |
| [`rest-mtls.yaml`](rest-mtls.yaml) | `mtls` | client cert + key; auth at the TLS layer |

`basic`, `bearer`, and `mtls` each accept an optional `ca_cert` and
`insecure_skip_verify` to reach a TLS endpoint fronted by a **private/internal CA**
(e.g. self-managed CP / MDS). Public-CA endpoints (Confluent Cloud via `api_key`)
need neither.
