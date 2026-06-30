# Example: mirror topics (destination-initiated link)

`mode: mirror` creates read-only **mirror topics** on a cluster link, one per
selected source topic, fed live through the link. It **requires** a
`spec.clusterLink`. Mirror topic names are `<prefix>+<sourceName>` (here, `msk.`);
with no prefix the mirror keeps the source name.

This is the **destination-initiated** topology (`mode: destination`, the default):
the destination — Confluent Cloud — dials the source (MSK) to establish the link,
so the source must be reachable from the destination.

## Credential slots

| Slot | Family | This example | Why |
|---|---|---|---|
| `spec.source.credentials` | Kafka | [IAM](../../credentials/kafka-iam.yaml) | reads the MSK cluster id |
| `spec.clusterLink.source.credentials` | Kafka | [SASL/SCRAM](../../credentials/kafka-sasl-scram.yaml) | the link dials MSK — Confluent Cloud cannot present IAM, so SCRAM |
| `spec.target.credentials` | REST | [API key](../../credentials/rest-api-key.yaml) | Confluent Cloud Admin API |

This shows three different credential files in one migration. See the full
[credential catalog](../../credentials/README.md) for the other auth methods.

## Run

```bash
kcp migrate validate -f migration.yaml
kcp migrate apply -f migration.yaml --dry-run   # preview the link + mirror plan
kcp migrate apply -f migration.yaml             # create the link + mirror topics
```

An existing mirror is reported `Present`; a name already taken on the destination
(by a plain topic, or a mirror on another link) is reported as **drift** and
skipped — apply never blindly recreates it.
