# Example: new topics (no cluster link)

`mode: new` reads the source topic list and reproduces the selected topics as
**plain topics** directly on the target — copying each source topic's partition
count, replication factor, and explicitly-set (non-default) configs. It needs
**no cluster link**, so there is no `spec.clusterLink` block.

Apply is additive: it only creates absent topics. An existing target topic whose
partition count differs from the source is reported as **drift** and left
untouched; it is never altered or deleted.

## Credential slots

| Slot | Family | This example | Catalog |
|---|---|---|---|
| `spec.source.credentials` | Kafka | SASL/SCRAM | [`kafka-sasl-scram.yaml`](../../credentials/kafka-sasl-scram.yaml) |
| `spec.target.credentials` | REST | API key | [`rest-api-key.yaml`](../../credentials/rest-api-key.yaml) |

Any Kafka credential works for the source (swap in IAM, mTLS, SASL/PLAIN, …); any
REST credential works for the target. See the [credential catalog](../../credentials/README.md).

## Run

```bash
kcp migrate validate -f migration.yaml   # structural check
kcp migrate apply -f migration.yaml --dry-run   # preview, change nothing
kcp migrate apply -f migration.yaml             # create the topics
```

> Credential paths here point at the shared catalog (`../../credentials/…`) to
> avoid duplication. For a real migration, copy the credential files next to your
> manifest and update the paths.
