# Example: Mirror Topics Over a Destination-Initiated Link

This example creates mirror topics on a cluster link that Confluent Cloud, as the destination, establishes to an Amazon MSK source.

`mode: mirror` creates read-only mirror topics on a cluster link, one for each selected source topic, fed live through the link. It requires a `spec.clusterLink`. Mirror topic names are `<prefix>+<sourceName>`, and the prefix in this example is `msk.`. With no prefix, the mirror keeps the source name.

This is the destination-initiated topology (`mode: destination`, the default): the destination, Confluent Cloud, connects to the source, MSK, to establish the link, so the source must be reachable from the destination.

## Credential slots

| Slot | Family | This example | Why |
|---|---|---|---|
| `spec.source.credentials` | Kafka | [IAM](../../credentials/kafka-iam.yaml) | Reads the MSK cluster ID. |
| `spec.clusterLink.source.credentials` | Kafka | [SASL/SCRAM](../../credentials/kafka-sasl-scram.yaml) | The link connects to MSK. Confluent Cloud can't present IAM, so the link uses SCRAM. |
| `spec.target.clusterCredentials` | REST | [API key](../../credentials/rest-api-key.yaml) | Confluent Cloud Admin API. |

This example uses three different credential files in one migration. For the other authentication methods, see the [credential file catalog](../../credentials/README.md).

## Run the example

```bash
kcp migration resources validate -f migration.yaml
kcp migration resources create -f migration.yaml --dry-run   # preview the link + mirror plan
kcp migration resources create -f migration.yaml             # create the link + mirror topics
```

kcp reports an existing mirror as `Present`. If a plain topic or a mirror on another link already has the name on the destination, kcp reports it as drift and skips it. `create` never recreates it blindly.

## Related content

- [Example: mirror topics over a source-initiated link](../source-initiated/README.md)
- [`spec.clusterLink` in the manifest reference](../../manifest-reference.md#specclusterlink)
- [Credential file catalog for the resources manifest](../../credentials/README.md)
- [Resources manifest examples](../../README.md#examples)
