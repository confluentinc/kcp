# Credentials in the Migration Manifest

Every `credentials`-shaped slot (`spec.source.credentials`,
`spec.target.kafka.clusterCredentials`, `spec.clusterLink.linkCredentials`) is a
path to an external credentials file. Inline credential blocks are not
accepted, so the manifest itself never holds secret material:

```yaml
credentials: /etc/kcp/source-creds.yaml
```

The referenced file's top-level content is the credential: for the Apache Kafka®
legs, a Kafka-family block, and for `linkCredentials`, a REST block. Prefer a credential
mounted as a Kubernetes Secret file. Each credentials file is secret-bearing.
If one is group- or world-readable, `kcp` warns rather than fails, so keep it `0600`.

## Kafka credentials (`spec.source.credentials`, `spec.target.kafka.clusterCredentials`)

Specify exactly one method block. Its presence selects it (no
`auth_method:` wrapper, no `use:` flag). An optional top-level
`insecure_skip_tls_verify: false` sibling applies to test environments only,
and takes effect only where a TLS handshake happens (not for
`unauthenticated_plaintext` or `sasl_plain` over plaintext). REST credentials
use a different key, `insecure_skip_verify`, inside their block.

| Method                      | Required fields             | Notes                                                                                                                                                         |
| --------------------------- | --------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `iam`                       | `region`                    | MSK source only. Confluent Cloud can't present IAM (a link to MSK uses SCRAM instead).                                                                        |
| `sasl_scram`                | `username`, `password`, `mechanism` | `mechanism` is `SHA256` or `SHA512` and must be set: an omitted mechanism is rejected. `SCRAM-SHA-256` and `SCRAM-SHA-512` are accepted too. MSK requires `SHA512`. Optional `ca_cert`. Always TLS (SASL_SSL). |
| `sasl_plain`                | `username`, `password`      | Optional `ca_cert`, `tls`. `ca_cert` present ⇒ SASL_SSL against that CA. `tls: true` ⇒ SASL_SSL over the system (public) truststore. Neither ⇒ SASL_PLAINTEXT on the source leg (the destination leg always uses TLS, as described in [`spec.target`](connections.md#spectarget)). |
| `mtls`                      | `client_cert`, `client_key` | Optional `ca_cert`. The client is authenticated by its certificate.                                                                                              |
| `unauthenticated_tls`       | —                           | Optional `ca_cert`. One-way TLS. The client is not authenticated.                                                                                                 |
| `unauthenticated_plaintext` | —                           | Write it as `unauthenticated_plaintext: {}`. A block is selected by its presence, so a bare key with no value is rejected. No auth, no TLS. Test or lab only.                                                                                                                               |

`ca_cert` (on `sasl_scram`, `sasl_plain`, `mtls`, `unauthenticated_tls`) is a
PEM file path used to verify the broker's TLS certificate. Supply it only for a
private or internal CA. Public-CA brokers (AWS MSK, Confluent Cloud) validate
against the system truststore and need no `ca_cert`. Every `ca_cert`,
`client_cert`, and `client_key` path must exist when the credentials file is
read.

Each of the following examples is the complete contents of a credentials file.

SASL/SCRAM (for example, an MSK source):

```yaml
sasl_scram:
  username: kcp-migration
  password: <password>
  mechanism: SHA512
```

mTLS, with a private CA:

```yaml
mtls:
  client_cert: /etc/kcp/certs/client.crt
  client_key: /etc/kcp/certs/client.key
  ca_cert: /etc/kcp/certs/ca.crt
```

IAM (MSK source only):

```yaml
iam:
  region: us-east-1
```

## REST credentials (`spec.clusterLink.linkCredentials`)

Specify exactly one block (or the `api_key` and `api_secret` pair).

| Method                   | Required fields             | Notes                                            |
| ------------------------ | --------------------------- | ------------------------------------------------ |
| `api_key` + `api_secret` | `api_key`, `api_secret`     | Confluent Cloud. Flat top-level pair. Public CA. |
| `basic`                  | `username`, `password`      | For example, Confluent Platform Metadata Service (MDS). |
| `bearer`                 | `token`                     | For example, MDS or OAuth.                       |
| `mtls`                   | `client_cert`, `client_key` | Auth at the TLS layer.                           |

`basic`, `bearer`, and `mtls` each accept an optional `ca_cert` and
`insecure_skip_verify` inside their block to reach a TLS endpoint fronted by a
private or internal CA (for example, self-managed Confluent Platform or MDS). The flat `api_key` form takes
the same two keys at the top level instead, and they are rejected alongside
the block forms. Public-CA endpoints (Confluent Cloud) need neither. `basic`
does not check at load time that `username` and `password` are non-empty. A
missing value surfaces as an authentication error at runtime.

Each of the following examples is the complete contents of a credentials file.

Confluent Cloud (`api_key` + `api_secret`):

```yaml
api_key: <api_key>
api_secret: <api_secret>
```

Self-managed Confluent Platform, with a private CA (`basic`):

```yaml
basic:
  username: kcp-migration
  password: <password>
  ca_cert: /etc/kcp/certs/ca.crt
```

One restriction is specific to this manifest, narrower than the two
tables above: `spec.source.credentials.iam` is valid only when `spec.source.type: msk`,
and `spec.target.kafka.clusterCredentials.iam` is rejected outright, because the
destination is Confluent Cloud or Confluent Platform, never MSK.
