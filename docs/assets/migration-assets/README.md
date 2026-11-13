# Manifests and Credential Files for kcp Migrations

kcp drives its migration commands from YAML manifests, and keeps credentials in separate files that hold only authentication. This section covers the two manifests, one for `kcp migration cutover` and one for `kcp migration resources`, and the credential files they reference.

## Cutover and resources manifests

- **Cutover manifest.** `kcp migration cutover execute` and `kcp migration cutover lag-check` use a manifest of `kind: GatewayMigration`, such as `gateway-static-migration.yaml`, with one example for each migration type. For its format, see the [cutover migration manifest reference](cutover/overview.md) and [`gateway-examples/gateway-static-migration.yaml`](../gateway-examples/gateway-static-migration.yaml).
- **Resources manifest.** `kcp migration resources validate` and `kcp migration resources create` use `migration.yaml` (`kind: Migration`). The rest of this page covers it.

The two manifests share `apiVersion` and the parser, but nothing else. If you pass a file written for one `kind` to the other command, kcp rejects it with a clear error.

## Resources manifest

A `kcp migration resources` migration moves resources from an Amazon MSK, Apache Kafka®, or Confluent Platform source. It uses a single manifest, `migration.yaml`, that describes the desired end state, such as the cluster link, topics, service accounts, and access control lists (ACLs) to create, plus external credential files that hold only authentication. These resources cover the manifest:

- [Manifest reference](manifest-reference.md): the field-by-field reference for every manifest section, and the single source of truth for field details.
- [`migration.example.yaml`](migration.example.yaml): the fully annotated reference manifest, with every field and comments.
- [Examples](#examples): minimal, validated manifests for each scenario.
- [Credential file catalog](credentials/README.md): one credential file for each authentication method, with a table of the slots each file applies to.

### Examples

| Example                                                       | Topics mode | Cluster link          | Source to target                              | ACLs                                                    |
| ------------------------------------------------------------- | ----------- | --------------------- | --------------------------------------------- | ------------------------------------------------------- |
| [new-topics](examples/new-topics/README.md)                   | `new`       | None                  | Apache Kafka to Confluent Cloud               | —                                                       |
| [mirror-topics](examples/mirror-topics/README.md)             | `mirror`    | Destination-initiated | MSK to Confluent Cloud                        | —                                                       |
| [source-initiated](examples/source-initiated/README.md)       | `mirror`    | Source-initiated      | Private Confluent Platform to Confluent Cloud | —                                                       |
| [acls-native](examples/acls-native/README.md)                 | —           | None                  | Apache Kafka to Confluent Cloud               | Native ACLs only                                        |
| [acls-iam-explicit](examples/acls-iam-explicit/README.md)     | —           | None                  | MSK to Confluent Cloud                        | IAM plane, explicit named roles                         |
| [acls-iam-discover](examples/acls-iam-discover/README.md)     | —           | None                  | MSK to Confluent Cloud                        | IAM plane, auto-discovered roles                        |
| [acls-native-and-iam](examples/acls-native-and-iam/README.md) | —           | None                  | MSK to Confluent Cloud                        | Native and IAM union, explicit service account mapping  |

To validate, preview, and then create any of them, run:

```bash
kcp migration resources validate -f migration.yaml
kcp migration resources create -f migration.yaml --dry-run   # preview; changes nothing
kcp migration resources create -f migration.yaml
```

`create` is additive: it creates only what's missing, and it reports what already exists without changing or deleting it. `--dry-run` prints the same plan without making any changes.

### Manifest at a glance

The manifest carries all addresses, and its `credentials:` fields reference external files that carry only secrets. Each connection is a uniform `{address, credentials}` pair:

- **Kafka slots.** `spec.source`, `spec.clusterLink.source`, and `spec.clusterLink.destination` each take `bootstrapServers` and `credentials`.
- **REST slots.** `spec.target` (with `spec.target.kafka.restEndpoint`) and `spec.clusterLink.sourceRest` each take `endpoint` or `restEndpoint`, and `credentials`. On `spec.target`, this Kafka cluster and REST v3 credential is `clusterCredentials`, which covers the cluster link, topics, and ACLs. A Confluent Cloud target also takes `cloudCredentials`, a separate Cloud or Global API key, whenever `spec.acls` is present. kcp uses it to provision service accounts and to reconcile ACL principals through IAM v2.

For the credential family each slot uses, and every supported authentication method, see the [credential file catalog](credentials/README.md).

### Manifest sections

`spec.source` and `spec.target` are always required. `spec.clusterLink`, `spec.topics`, `spec.serviceAccounts`, and `spec.acls` are optional, and each one is a unit of work that kcp reconciles independently. For every field, including types, defaults, required and forbidden rules, and behavior, see the [manifest reference](manifest-reference.md).

### Set up editor support

`migration.example.yaml` and every example carry a `# yaml-language-server: $schema=…` modeline, so Visual Studio Code with the [Red Hat YAML extension](https://marketplace.visualstudio.com/items?itemName=redhat.vscode-yaml) gives autocomplete and inline validation. For offline or repository-local use, map the schema in your workspace settings instead:

```jsonc
// .vscode/settings.json
"yaml.schemas": {
  "internal/manifest/migration.schema.json": ["migration*.yaml"]
}
```

## Related content

- [Cutover migration manifest reference](cutover/overview.md)
- [Run a migration cutover](cutover/running-a-cutover.md)
- [Migration resources manifest reference](manifest-reference.md)
- [Credential file catalog for the resources manifest](credentials/README.md)
- [Get started with kcp](../getting-started.md)
