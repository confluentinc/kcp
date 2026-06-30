# Example: mirror topics (source-initiated link)

Same `mode: mirror` outcome as the [destination-initiated example](../mirror-topics/),
but the link is **source-initiated** (`mode: source`). Use this when the
destination **cannot reach the source inbound** — e.g. a private or firewalled
Confluent Platform cluster. The source initiates the link **outbound** to the
destination.

Source-initiated mode requires `spec.source.type: confluent-platform` (or
`confluent-cloud`); MSK / Apache Kafka cannot initiate a link.

## How the slots differ from destination mode

Source mode replaces `clusterLink.source` with two slots:

- `sourceRest` — KCP connects to the **source's** Admin/MDS REST to create the
  outbound link object.
- `destination` — the **source-side link's** connection to the destination Kafka.

| Slot | Family | This example | Role |
|---|---|---|---|
| `spec.source.credentials` | Kafka | [SASL/SCRAM](../../credentials/kafka-sasl-scram.yaml) | reads the CP cluster id |
| `spec.clusterLink.sourceRest.credentials` | REST | [Basic](../../credentials/rest-basic.yaml) | KCP → CP MDS (creates the link) |
| `spec.clusterLink.destination.credentials` | Kafka | [SASL/SCRAM](../../credentials/kafka-sasl-scram.yaml) | source-side link → Confluent Cloud |
| `spec.target.credentials` | REST | [API key](../../credentials/rest-api-key.yaml) | Confluent Cloud Admin API |

See the full [credential catalog](../../credentials/README.md) for every auth method.

## Run

```bash
kcp migrate validate -f migration.yaml
kcp migrate apply -f migration.yaml --dry-run
kcp migrate apply -f migration.yaml
```
