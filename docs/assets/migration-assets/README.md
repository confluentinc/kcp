# Migration assets

This folder is scoped to **one** manifest: `migration.yaml` (`kind: Migration`),
which drives the separate, still-hidden `kcp migrate` direct-API command. That
command isn't user-facing yet, which is why this whole folder is excluded from
the built docs site (`mkdocs.yml`'s `exclude_docs`) — keep any future addition
here scoped to that manifest, not the unrelated one below.

`kcp migration init|execute|lag-check` (fully user-facing) is driven by a
different manifest, `gateway-migration.yaml` (`kind: GatewayMigration`), which
shares this one's `apiVersion` and parser but nothing else. Its docs live
**outside** this folder, published normally: see
[gateway manifest reference](../gateway-manifest-reference.md) and
[`gateway-examples/gateway-migration.yaml`](../gateway-examples/gateway-migration.yaml).
A file written for one `kind` is rejected with a clear error if pointed at the
other command.

---

A `kcp migrate` migration is driven by a single **manifest** (`migration.yaml`)
that describes the desired end state, plus external **credential files** that hold
auth only. This section provides a ready-to-copy reference manifest, worked
examples for each scenario, and a catalog of every credential format.

| | |
|---|---|
| [manifest reference](manifest-reference.md) | field-by-field reference for every manifest section — the single source of truth for field detail |
| [`migration.example.yaml`](migration.example.yaml) | the fully-annotated reference manifest — every field, with comments |
| [examples/](examples/) | minimal, validated manifests per scenario (below) |
| [credentials/](credentials/README.md) | one credential file per auth method, with a slot-applicability table |

## Examples

| Example | Topics mode | Cluster link | Source → target |
|---|---|---|---|
| [new-topics](examples/new-topics/) | `new` | none | Apache Kafka → Confluent Cloud |
| [mirror-topics](examples/mirror-topics/) | `mirror` | destination-initiated | MSK → Confluent Cloud |
| [source-initiated](examples/source-initiated/) | `mirror` | source-initiated | private Confluent Platform → Confluent Cloud |
| [acls-native](examples/acls-native/) | — | none | Apache Kafka → Confluent Cloud — native ACLs only |
| [acls-iam-explicit](examples/acls-iam-explicit/) | — | none | MSK → Confluent Cloud — IAM plane, explicit named roles |
| [acls-iam-discover](examples/acls-iam-discover/) | — | none | MSK → Confluent Cloud — IAM plane, auto-discovered roles |
| [acls-native-and-iam](examples/acls-native-and-iam/) | — | none | MSK → Confluent Cloud — native + IAM union, explicit service-account mapping |

Validate, preview, then apply any of them:

```bash
kcp migrate validate -f migration.yaml
kcp migrate apply -f migration.yaml --dry-run   # preview; changes nothing
kcp migrate apply -f migration.yaml
```

`apply` is **additive**: it only creates what is absent and reports — never alters
or deletes — what already exists. `--dry-run` prints the same plan without making
any change.

## Manifest at a glance

The manifest carries all **addresses**; `credentials:` fields reference external
files that carry **secrets only**. Each connection is a uniform `{address,
credentials}` pair:

- **Kafka slots** (`spec.source`, `spec.clusterLink.source`,
  `spec.clusterLink.destination`) — `bootstrapServers` + `credentials`.
- **REST slots** (`spec.target` + `spec.target.kafka.restEndpoint`,
  `spec.clusterLink.sourceRest`) — `endpoint`/`restEndpoint` + `credentials`.
  On `spec.target` this Kafka cluster / REST-v3 credential is
  `clusterCredentials` (cluster link, topics, ACLs); a Confluent Cloud target
  also takes `cloudCredentials` — a distinct Cloud/Global API key — whenever
  `spec.acls` is present (to provision service accounts and reconcile ACL
  principals via IAM v2).

Which credential family each slot uses, and every supported auth method, is in the
[credential catalog](credentials/README.md).

## Manifest sections

`spec.source` and `spec.target` are always required; `spec.clusterLink`,
`spec.topics`, `spec.serviceAccounts`, and `spec.acls` are opt-in and correspond
to independently reconciled units of work. Every field — types, defaults,
required/forbidden rules, and behaviour — is documented in the
[manifest reference](manifest-reference.md).

## Editor support

`migration.example.yaml` and every example carry a `# yaml-language-server:
$schema=…` modeline, so VS Code with the
[Red Hat YAML extension](https://marketplace.visualstudio.com/items?itemName=redhat.vscode-yaml)
gives autocomplete and inline validation automatically. For offline/repo-local use,
map the schema in your workspace settings instead:

```jsonc
// .vscode/settings.json
"yaml.schemas": {
  "internal/manifest/migration.schema.json": ["migration*.yaml"]
}
```
