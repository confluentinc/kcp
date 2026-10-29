# Credentials

Every `credentials`-shaped slot (`spec.source.credentials`,
`spec.target.kafka.clusterCredentials`, `spec.clusterLink.linkCredentials`) is a
**path to an external credentials file** — inline credential blocks are not
accepted, so the manifest itself never holds secret material:

```yaml
credentials: /etc/kcp/source-creds.yaml
```

The referenced file's top-level content is the credential (a Kafka-family block
for the Kafka legs, a REST block for `linkCredentials`). Prefer a credential
mounted as a Kubernetes Secret file. Each credentials file is secret-bearing —
`kcp` warns (not errors) if it is group- or world-readable, so keep it `0600`.

## Kafka credentials (`spec.source.credentials`, `spec.target.kafka.clusterCredentials`)

Specify **exactly one** method block — its **presence** selects it (no
`auth_method:` wrapper, no `use:` flag). An optional top-level
`insecure_skip_tls_verify: false` sibling applies to test environments only,
and takes effect only where a TLS handshake happens (not for
`unauthenticated_plaintext` or `sasl_plain` over plaintext). REST credentials
use a different key, `insecure_skip_verify`, inside their block.

| Method                      | Required fields             | Notes                                                                                                                                                         |
| --------------------------- | --------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `iam`                       | `region`                    | MSK source only; Confluent Cloud can't present IAM (a link to MSK uses SCRAM instead).                                                                        |
| `sasl_scram`                | `username`, `password`, `mechanism` | `mechanism` is `SHA256` or `SHA512` (`SCRAM-SHA-256` / `SCRAM-SHA-512` are accepted too; MSK requires `SHA512`) and must be set — an omitted mechanism is rejected. Optional `ca_cert`. Always TLS (SASL_SSL). |
| `sasl_plain`                | `username`, `password`      | Optional `ca_cert`, `tls`. `ca_cert` present ⇒ SASL_SSL against that CA; `tls: true` ⇒ SASL_SSL over the system/public trust store; neither ⇒ SASL_PLAINTEXT on the source leg (the destination leg always uses TLS — see [`spec.target`](connections.md#spectarget)). |
| `mtls`                      | `client_cert`, `client_key` | Optional `ca_cert`. Client is authenticated via its certificate.                                                                                              |
| `unauthenticated_tls`       | —                           | Optional `ca_cert`. One-way TLS; client is not authenticated.                                                                                                 |
| `unauthenticated_plaintext` | —                           | Written `unauthenticated_plaintext: {}` — a block is selected by its presence, so a bare key with no value is rejected. No auth, no TLS. Test/lab only.                                                                                                                               |

`ca_cert` (on `sasl_scram`, `sasl_plain`, `mtls`, `unauthenticated_tls`) is a
PEM file path used to verify the broker's TLS certificate. Supply it only for a
**private/internal CA**; public-CA brokers (AWS MSK, Confluent Cloud) validate
against the system trust store and need no `ca_cert`. Every `ca_cert`,
`client_cert` and `client_key` path must exist when the credentials file is
read.

Examples — each is the complete contents of a credentials file:

SASL/SCRAM (e.g. an MSK source):

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

Specify **exactly one** block (or the `api_key`/`api_secret` pair).

| Method                   | Required fields             | Notes                                            |
| ------------------------ | --------------------------- | ------------------------------------------------ |
| `api_key` + `api_secret` | `api_key`, `api_secret`     | Confluent Cloud; flat top-level pair; public CA. |
| `basic`                  | `username`, `password`      | e.g. Confluent Platform MDS.                     |
| `bearer`                 | `token`                     | e.g. MDS/OAuth.                                  |
| `mtls`                   | `client_cert`, `client_key` | Auth at the TLS layer.                           |

`basic`, `bearer`, and `mtls` each accept an optional `ca_cert` and
`insecure_skip_verify` inside their block to reach a TLS endpoint fronted by a
private/internal CA (e.g. self-managed CP/MDS). The flat `api_key` form takes
the same two keys at the top level instead, and they are rejected alongside
the block forms. Public-CA endpoints (Confluent Cloud) need neither. `basic`
does not check at load time that `username` and `password` are non-empty; a
missing value surfaces as an authentication error at runtime.

Examples — each is the complete contents of a credentials file:

Confluent Cloud (`api_key` + `api_secret`):

```yaml
api_key: <api-key>
api_secret: <api-secret>
```

Self-managed Confluent Platform, with a private CA (`basic`):

```yaml
basic:
  username: kcp-migration
  password: <password>
  ca_cert: /etc/kcp/certs/ca.crt
```

One restriction is specific to **this** manifest, narrower than the two
tables above:

- `spec.source.credentials.iam` is valid only when `spec.source.type: msk`.
  `spec.target.kafka.clusterCredentials.iam` is rejected outright — the
  destination is Confluent Cloud/Platform, never MSK.
