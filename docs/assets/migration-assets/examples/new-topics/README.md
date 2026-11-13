# Example: Migrate to New Topics Without a Cluster Link

This example reproduces selected source topics as plain topics on the target, with no cluster link. Use it when you want new topics on Confluent Cloud instead of mirror topics.

`mode: new` reads the source topic list and creates the selected topics as plain topics directly on the target, copying each source topic's partition count and its explicitly set (non-default) configurations. kcp creates the topics with the target's default replication factor, which is 3 on Confluent Cloud. This mode needs no cluster link, so the manifest has no `spec.clusterLink` block.

`create` is additive: it creates only missing topics. If an existing target topic's partition count differs from the source, kcp reports it as drift and leaves it unchanged. kcp never alters or deletes it.

## Credential slots

| Slot | Family | This example | Catalog file |
|---|---|---|---|
| `spec.source.credentials` | Kafka | SASL/SCRAM | [`kafka-sasl-scram.yaml`](../../credentials/kafka-sasl-scram.yaml) |
| `spec.target.clusterCredentials` | REST | API key | [`rest-api-key.yaml`](../../credentials/rest-api-key.yaml) |

Any Kafka-family credential works for the source, such as IAM, mTLS, or SASL/PLAIN, and any REST credential works for the target. For every format, see the [credential file catalog](../../credentials/README.md).

## Run the example

```bash
kcp migration resources validate -f migration.yaml   # structural check
kcp migration resources create -f migration.yaml --dry-run   # preview, change nothing
kcp migration resources create -f migration.yaml             # create the topics
```

The credential paths in this example point at the shared catalog (`../../credentials/…`) to avoid duplication. For a real migration, copy the credential files next to your manifest and update the paths.

## Related content

- [`spec.topics` in the manifest reference](../../manifest-reference.md#spectopics)
- [Credential file catalog for the resources manifest](../../credentials/README.md)
- [Resources manifest examples](../../README.md#examples)
