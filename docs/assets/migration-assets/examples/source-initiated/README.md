# Example: Mirror Topics Over a Source-Initiated Link

This example creates mirror topics on a cluster link that the source initiates outbound, for a Confluent Platform cluster that the destination can't reach inbound.

The outcome is the same `mode: mirror` result as the [destination-initiated example](../mirror-topics/README.md), but the link is source-initiated (`mode: source`). Use this topology when the destination can't reach the source inbound, such as for a private or firewalled Confluent Platform cluster. The source initiates the link outbound to the destination.

Source-initiated mode requires `spec.source.type: confluent-platform` or `confluent-cloud`. Amazon MSK and Apache Kafka® sources can't initiate a link.

## How the slots differ from destination mode

Source mode replaces `clusterLink.source` with two slots:

- `sourceRest`: kcp connects to the source's Admin or Metadata Service (MDS) REST API to create the outbound link object.
- `destination`: the source-side link's connection to the destination Kafka cluster.

| Slot | Family | This example | Role |
|---|---|---|---|
| `spec.source.credentials` | Kafka | [SASL/SCRAM](../../credentials/kafka-sasl-scram.yaml) | Reads the Confluent Platform cluster ID. |
| `spec.clusterLink.sourceRest.credentials` | REST | [Basic](../../credentials/rest-basic.yaml) | kcp to Confluent Platform MDS, which creates the link. |
| `spec.clusterLink.destination.credentials` | Kafka | [SASL/SCRAM](../../credentials/kafka-sasl-scram.yaml) | The source-side link to Confluent Cloud. |
| `spec.target.clusterCredentials` | REST | [API key](../../credentials/rest-api-key.yaml) | Confluent Cloud Admin API. |

For every authentication method, see the [credential file catalog](../../credentials/README.md).

## Run the example

```bash
kcp migration resources validate -f migration.yaml
kcp migration resources create -f migration.yaml --dry-run
kcp migration resources create -f migration.yaml
```

## Related content

- [Example: mirror topics over a destination-initiated link](../mirror-topics/README.md)
- [Cluster link modes in the manifest reference](../../manifest-reference.md#modes)
- [Credential file catalog for the resources manifest](../../credentials/README.md)
- [Resources manifest examples](../../README.md#examples)
